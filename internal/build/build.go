package build

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	xhtml "golang.org/x/net/html"

	internalasset "github.com/simp-lee/obsite/internal/asset"
	internalconfig "github.com/simp-lee/obsite/internal/config"
	"github.com/simp-lee/obsite/internal/diag"
	internalfsutil "github.com/simp-lee/obsite/internal/fsutil"
	"github.com/simp-lee/obsite/internal/link"
	"github.com/simp-lee/obsite/internal/markdown"
	"github.com/simp-lee/obsite/internal/model"
	"github.com/simp-lee/obsite/internal/recommend"
	"github.com/simp-lee/obsite/internal/render"
	"github.com/simp-lee/obsite/internal/seo"
	internalslug "github.com/simp-lee/obsite/internal/slug"
	"github.com/simp-lee/obsite/internal/vault"
	"github.com/tdewolff/minify/v2"
	mincss "github.com/tdewolff/minify/v2/css"
	minhtml "github.com/tdewolff/minify/v2/html"
)

// BuildResult exposes the key outputs that later CLI work needs from a build.
type BuildResult struct {
	OutputPath   string
	Index        *model.VaultIndex
	Graph        *model.LinkGraph
	Assets       map[string]*model.Asset
	Diagnostics  []diag.Diagnostic
	NotePages    int
	TagPages     int
	RecentNotes  []model.NoteSummary
	WarningCount int
	ErrorCount   int
}

// SiteInput is the build-owned contract for site generation.
type SiteInput struct {
	Config model.SiteConfig
}

type buildOptions struct {
	concurrency       int
	diagnosticsWriter io.Writer
	force             bool
	minifier          *minify.M
	testNotePageHook  func(render.NotePageInput)
}

type renderedNote struct {
	source   *model.Note
	rendered *model.Note
	outLinks []model.LinkRef
	diag     *diag.Collector
}

type noteBuildState struct {
	rendered          *renderedNote
	contentHash       string
	renderSignature   string
	derivedSignatures map[string]string
	assets            map[string]*model.Asset
	renderDiagnostics []diag.Diagnostic
	pageDiagnostics   []diag.Diagnostic
	fromCache         bool
}

type noteAssetRecorder struct {
	shared markdown.AssetSink
	assets map[string]*model.Asset
}

type folderPageSpec struct {
	Path  string
	Notes []*model.Note
}

type sidebarTreeBuilder struct {
	name     string
	sitePath string
	isDir    bool
	children map[string]*sidebarTreeBuilder
}

type diagnosticBuildError struct {
	count int
}

type managedOutputDirState struct {
	info      os.FileInfo
	exists    bool
	isDir     bool
	hasMarker bool
	empty     bool
}

type stagedOutputPublisher struct {
	outputPath        string
	stagingPath       string
	backupPath        string
	createdParentDirs []string
	initialOutput     managedOutputDirState
}

var (
	stagedOutputRename     = os.Rename
	stagedOutputRemoveAll  = os.RemoveAll
	stagedOutputStat       = os.Stat
	buildRenderHookBarrier sync.RWMutex
	buildRenderHookOwnerID atomic.Uint64
)

func lockBuildRenderHookIsolation() {
	buildRenderHookBarrier.Lock()
	buildRenderHookOwnerID.Store(currentGoroutineID())
}

func unlockBuildRenderHookIsolation() {
	buildRenderHookOwnerID.Store(0)
	buildRenderHookBarrier.Unlock()
}

func lockBuildExecutionForRenderHooks() func() {
	if ownerID := buildRenderHookOwnerID.Load(); ownerID != 0 && ownerID == currentGoroutineID() {
		return func() {}
	}

	buildRenderHookBarrier.RLock()
	return func() {
		buildRenderHookBarrier.RUnlock()
	}
}

func currentGoroutineID() uint64 {
	var stack [64]byte
	n := runtime.Stack(stack[:], false)
	fields := bytes.Fields(stack[:n])
	if len(fields) < 2 {
		return 0
	}

	id, err := strconv.ParseUint(string(fields[1]), 10, 64)
	if err != nil {
		return 0
	}

	return id
}

const (
	managedOutputMarkerFilename = ".obsite-output"
	managedOutputMarkerContents = "managed by obsite\n"
	customCSSOutputPath         = "assets/custom.css"
)

var paginationGeneratedHrefPattern = regexp.MustCompile(`(<(?:link\b[^>]*\brel=(?:"(?:prev|next)"|(?:prev|next))\b[^>]*|a\b[^>]*\bclass=(?:"[^"]*\bpagination-(?:link|page)\b[^"]*"|'[^']*\bpagination-(?:link|page)\b[^']*'|[^\s>]*pagination-(?:link|page)[^\s>]*)[^>]*?)\bhref=)(?:"([^"]*)"|'([^']*)'|([^\s>]+))`)

var minimalSiteLastModified = time.Unix(0, 0).UTC()

var renderMarkdownNote = func(idx *model.VaultIndex, note *model.Note, assetSink markdown.AssetSink) (*renderedNote, error) {
	localDiag := diag.NewCollector()
	md, renderResult := markdown.NewMarkdown(idx, note, assetSink, localDiag)

	var html bytes.Buffer
	if err := md.Convert(note.RawContent, &html); err != nil {
		return nil, fmt.Errorf("render %q: %w", note.RelPath, err)
	}

	rendered := cloneNote(note)
	rendered.HTMLContent = html.String()
	rendered.HasMath = renderResult.HasMath()
	rendered.HasMermaid = renderResult.HasMermaid()
	rendered.Summary = render.VisibleSummary(rendered)

	return &renderedNote{
		source:   note,
		rendered: rendered,
		outLinks: renderResult.OutLinks(),
		diag:     localDiag,
	}, nil
}

var (
	renderNotePage     = render.RenderNote
	renderIndexPage    = render.RenderIndex
	renderTagPage      = render.RenderTagPage
	renderFolderPage   = render.RenderFolderPage
	renderTimelinePage = render.RenderTimelinePage
)

func newNoteAssetRecorder(shared markdown.AssetSink) *noteAssetRecorder {
	return &noteAssetRecorder{
		shared: shared,
		assets: make(map[string]*model.Asset),
	}
}

func (r *noteAssetRecorder) Register(vaultRelPath string) string {
	if r == nil || r.shared == nil {
		return ""
	}

	dstPath := r.shared.Register(vaultRelPath)
	srcPath := normalizeNoteAssetPath(vaultRelPath)
	if srcPath == "" {
		return dstPath
	}

	asset := r.assets[srcPath]
	if asset == nil {
		asset = &model.Asset{SrcPath: srcPath}
		r.assets[srcPath] = asset
	}
	asset.RefCount++
	asset.DstPath = dstPath

	return dstPath
}

func (r *noteAssetRecorder) Snapshot() map[string]*model.Asset {
	if r == nil || len(r.assets) == 0 {
		return nil
	}

	snapshot := make(map[string]*model.Asset, len(r.assets))
	for srcPath, asset := range r.assets {
		if asset == nil {
			continue
		}
		cloned := *asset
		snapshot[srcPath] = &cloned
	}

	return snapshot
}

func normalizeNoteAssetPath(value string) string {
	cleaned := path.Clean(strings.TrimSpace(strings.ReplaceAll(value, `\`, "/")))
	cleaned = strings.TrimPrefix(cleaned, "./")
	cleaned = strings.TrimPrefix(cleaned, "/")
	if cleaned == "." || cleaned == "" {
		return ""
	}
	if !internalasset.IsPublishableAssetPath(cleaned) {
		return ""
	}
	return cleaned
}

var buildVaultIndex = func(scanResult vault.ScanResult, frontmatterResult vault.FrontmatterResult, diagCollector *diag.Collector, options vault.BuildIndexOptions) (vault.IndexResult, error) {
	return vault.BuildIndex(scanResult, frontmatterResult, diagCollector, options)
}

func (e diagnosticBuildError) Error() string {
	return fmt.Sprintf("build failed with %d diagnostic error(s)", e.count)
}

// LoadSiteInput loads build input using config-layer normalization.
func LoadSiteInput(resolvedVault string) (SiteInput, error) {
	cfg, err := internalconfig.LoadForBuild(resolvedVault)
	if err != nil {
		return SiteInput{}, err
	}

	return SiteInput{Config: cfg}, nil
}

// BuildWithOptions runs the full Obsite site-generation pipeline from a build.SiteInput contract.
func BuildWithOptions(input SiteInput, vaultPath string, outputPath string, options Options) (*BuildResult, error) {
	return buildWithOptions(input.Config, vaultPath, outputPath, buildOptions{
		force:             options.Force,
		diagnosticsWriter: options.DiagnosticsWriter,
	})
}

func buildWithOptions(cfg model.SiteConfig, vaultPath string, outputPath string, options buildOptions) (result *BuildResult, err error) {
	releaseRenderHookBuildLock := lockBuildExecutionForRenderHooks()
	defer releaseRenderHookBuildLock()

	options = normalizeBuildOptions(options)
	result = &BuildResult{}
	diagnostics := diag.NewCollector()

	defer func() {
		result, err = finalizeBuild(result, diagnostics, options.diagnosticsWriter, err)
	}()

	boundary, err := internalfsutil.ResolveVaultOutput(vaultPath, outputPath)
	if err != nil {
		return result, err
	}
	normalizedVaultPath := boundary.VaultPath
	normalizedOutputPath := boundary.OutputPath
	result.OutputPath = normalizedOutputPath

	normalizedCfg, err := internalconfig.NormalizeSiteConfig(cfg)
	if err != nil {
		return result, fmt.Errorf("validate config: %w", err)
	}
	cfg = normalizedCfg
	cfg.CustomCSS, err = resolveCustomCSSSource(normalizedVaultPath, cfg.CustomCSS)
	if err != nil {
		return result, fmt.Errorf("resolve custom CSS: %w", err)
	}
	theme, err := resolveThemeInputs(normalizedVaultPath, cfg.ThemeDir)
	if err != nil {
		return result, fmt.Errorf("resolve theme inputs: %w", err)
	}
	cfg.ThemeDir = theme.directory
	cfg.ThemeCSS = theme.stylesheet
	cfg.ThemeSlots = ""
	if theme.slots != "" {
		_, slots, _, readErr := internalfsutil.ReadContainedRegularFile(normalizedVaultPath, theme.slots)
		if readErr != nil {
			return result, fmt.Errorf("read theme slots %q: %w", theme.slots, readErr)
		}
		cfg.ThemeSlots = string(slots)
		if err := render.ValidateThemeSlots(cfg.ThemeSlots); err != nil {
			return result, fmt.Errorf("validate theme slots %q: %w", theme.slots, err)
		}
	}
	reservedAssetOutputPaths := buildReservedAssetOutputPaths(cfg.CustomCSS, theme)

	publisher, err := prepareStagedOutputPublisher(normalizedVaultPath, normalizedOutputPath)
	if err != nil {
		return result, err
	}
	previousOutputPath := previousManagedOutputPath(publisher)
	defer func() {
		if finalizeErr := publisher.Finalize(err == nil); finalizeErr != nil {
			if err == nil {
				err = finalizeErr
			} else {
				err = errors.Join(err, finalizeErr)
			}
		}
	}()

	configSignature, err := buildConfigSignature(cfg)
	if err != nil {
		return result, err
	}
	disableCacheReuse := false
	buildABISignature, err := readBuildABISignature()
	if err != nil {
		var abiSourceErr *buildABISourceSignatureUnavailableError
		if !errors.As(err, &abiSourceErr) {
			return result, fmt.Errorf("compute build ABI signature: %w", err)
		}

		disableCacheReuse = true
		buildABISignature = abiSourceErr.fallbackSignature
		warnBuildABISourceSignatureFailure(diagnostics, abiSourceErr)
	}
	templateSignature, err := buildEmbeddedTemplateSignature()
	if err != nil {
		return result, err
	}

	var previousManifest *CacheManifest
	if !options.force && !disableCacheReuse {
		loadedManifest, loadErr := loadCacheManifest(previousOutputPath)
		if loadErr == nil {
			previousManifest = loadedManifest
		} else {
			warnCacheManifestLoadFailure(diagnostics, loadErr)
		}
	}
	fullDirty := options.force || disableCacheReuse || previousManifest == nil || previousManifest.BuildABISignature != buildABISignature || previousManifest.ConfigSignature != configSignature || previousManifest.TemplateSignature != templateSignature

	scanResult, err := vault.ScanWithOptions(normalizedVaultPath, vault.ScanOptions{OutputPath: normalizedOutputPath})
	if err != nil {
		return result, fmt.Errorf("scan vault: %w", err)
	}
	recordCanvasDiagnostics(scanResult.ResourceFiles, diagnostics)

	frontmatterResult, err := vault.ParseFrontmatter(scanResult, cfg)
	if err != nil {
		return result, fmt.Errorf("parse frontmatter: %w", err)
	}

	indexResult, err := buildVaultIndex(scanResult, frontmatterResult, diagnostics, vault.BuildIndexOptions{
		Concurrency:            options.concurrency,
		CollectRelatedSemantic: cfg.Related.Enabled,
	})
	idx := indexResult.Index
	result.Index = idx
	if err != nil {
		return result, fmt.Errorf("build index: %w", err)
	}

	var relatedRanking *recommend.EngineResult
	if cfg.Related.Enabled {
		sourceGraph := link.BuildSourceGraph(idx)
		relatedRanking, err = preparePreRenderRelatedRanking(
			&indexResult.RelatedSemantic,
			idx,
			sourceGraph,
			recommend.ProductionEngineParameters(cfg.Related.Count, options.concurrency),
		)
		if err != nil {
			return result, fmt.Errorf("rank related articles: %w", err)
		}
	}

	folderPages := buildFolderPageSpecs(idx)
	sidebarTree := buildSidebarTree(idx)
	if err := detectFolderPageConflicts(idx, folderPages, diagnostics); err != nil {
		return result, fmt.Errorf("build folder pages: %w", err)
	}
	if err := detectTimelinePageConflicts(cfg, idx, folderPages, diagnostics); err != nil {
		return result, fmt.Errorf("build timeline page: %w", err)
	}
	if err := detectGeneratedPageRouteConflicts(cfg, idx, folderPages, diagnostics); err != nil {
		return result, fmt.Errorf("build route manifest: %w", err)
	}

	noteHashes, err := buildNoteHashes(normalizedVaultPath, idx)
	if err != nil {
		return result, err
	}
	noteRenderSignatures := buildNoteRenderSignatures(idx, noteHashes)
	noteDerivedSignatures := buildNoteDerivedSignatures(idx)
	hashSnapshot := diffNoteHashes(previousManifest, noteHashes)

	assetCollector, err := internalasset.NewCollectorWithResourceFiles(scanResult.VaultPath, idx.Assets, reservedAssetOutputPaths, nil)
	if err != nil {
		return result, fmt.Errorf("create asset collector: %w", err)
	}
	noteStates, renderErr := buildNoteStates(idx, assetCollector, options.concurrency, previousManifest, noteHashes, noteRenderSignatures, noteDerivedSignatures, fullDirty)
	if renderErr != nil {
		for _, state := range noteStates {
			if state == nil {
				continue
			}
			for _, diagnostic := range state.renderDiagnostics {
				diagnostics.Add(diagnostic)
			}
		}
		return result, fmt.Errorf("render markdown: %w", renderErr)
	}
	noteStatesByPath := make(map[string]*noteBuildState, len(noteStates))
	contentDirtyPaths := make(map[string]struct{}, len(noteStates))
	for _, state := range noteStates {
		if state == nil || state.rendered == nil || state.rendered.source == nil {
			continue
		}
		relPath := state.rendered.source.RelPath
		noteStatesByPath[relPath] = state
		if !state.fromCache {
			contentDirtyPaths[relPath] = struct{}{}
		}
	}
	assetDestinationPlan := assetCollector.PlanDestinations(mergeBuildAssets(idx.Assets, noteStatesByPath))
	assetDestinationDirtyPaths, err := rerenderNotesWithMismatchedAssetDestinations(idx, assetCollector, noteStatesByPath, assetDestinationPlan)
	if err != nil {
		return result, fmt.Errorf("rerender notes for asset path changes: %w", err)
	}
	applyAssetDestinationPlanToNoteStates(noteStatesByPath, assetDestinationPlan)

	renderedByPath := make(map[string]*renderedNote, len(noteStatesByPath))
	resolvedOutLinks := make(map[string][]model.LinkRef, len(noteStatesByPath))
	for relPath, state := range noteStatesByPath {
		if state == nil || state.rendered == nil || state.rendered.source == nil {
			continue
		}
		renderedByPath[relPath] = state.rendered
		resolvedOutLinks[relPath] = cloneLinkRefs(state.rendered.outLinks)
	}
	for _, relPath := range sortedNoteBuildStatePaths(noteStatesByPath) {
		state := noteStatesByPath[relPath]
		if state == nil {
			continue
		}
		for _, diagnostic := range state.renderDiagnostics {
			diagnostics.Add(diagnostic)
		}
	}

	summaryByPath := buildRenderedSummaryMap(renderedByPath)
	result.Graph = link.BuildGraph(idx, resolvedOutLinks)
	backlinkSignatures := buildBacklinkDerivedSignatures(idx, result.Graph)
	mergeDerivedSignatures(noteDerivedSignatures, noteStatesByPath, derivedSignatureKeyBacklinks, backlinkSignatures)
	relatedArticlesByPath, relatedSignatures := materializeRelatedArticlesByPath(cfg, idx, relatedRanking, summaryByPath)
	mergeDerivedSignatures(noteDerivedSignatures, noteStatesByPath, derivedSignatureKeyRelated, relatedSignatures)
	notePageDirty := determineDirtyNotePages(cfg, idx, contentDirtyPaths, hashSnapshot.removed, previousManifest, noteDerivedSignatures, fullDirty)
	for relPath := range assetDestinationDirtyPaths {
		notePageDirty[relPath] = struct{}{}
	}
	result.NotePages = len(notePageDirty)
	popoverMarker, err := newPopoverLinkMarker(cfg, renderedByPath)
	if err != nil {
		return result, fmt.Errorf("build popover link marker: %w", err)
	}

	recentNotes := buildRecentNotes(idx, "index.html", summaryByPath)
	result.RecentNotes = append([]model.NoteSummary(nil), recentNotes...)
	siteLastModified := siteLastModified(allPublicNotes(idx))
	mergedAssets := mergeBuildAssets(idx.Assets, noteStatesByPath)
	writeStyleCSS := true
	if err := validateOutputDestinations(cfg, idx, folderPages, mergedAssets, theme, writeStyleCSS, !disableCacheReuse); err != nil {
		return result, fmt.Errorf("plan generated output: %w", err)
	}
	stagingOutputPath := publisher.OutputPath()
	if err := writeManagedOutputMarker(stagingOutputPath); err != nil {
		return result, err
	}
	writeSitePages := func(pageDiagnostics *diag.Collector) ([]model.PageData, map[string]string, error) {
		sitemapPages := make([]model.PageData, 0, len(noteStatesByPath)+len(idx.Tags)+len(folderPages)+2)
		pageSignatures := make(map[string]string)

		notePages, err := writeNotePages(cfg, idx, renderedByPath, result.Graph, previousOutputPath, stagingOutputPath, options.minifier, pageDiagnostics, popoverMarker, sidebarTree, notePageDirty, noteStatesByPath, relatedArticlesByPath, options.testNotePageHook)
		if err != nil {
			return nil, nil, err
		}
		sitemapPages = append(sitemapPages, notePages...)

		tagPages, tagSignatures, err := writeTagPages(cfg, idx, summaryByPath, previousManifest, previousOutputPath, stagingOutputPath, options.minifier, popoverMarker, sidebarTree, fullDirty)
		if err != nil {
			return nil, nil, err
		}
		mergePageSignatures(pageSignatures, tagSignatures)
		result.TagPages = len(tagPages)
		sitemapPages = append(sitemapPages, tagPages...)

		renderedFolderPages, folderSignatures, err := writeFolderPages(cfg, idx, summaryByPath, folderPages, previousManifest, previousOutputPath, stagingOutputPath, options.minifier, popoverMarker, sidebarTree, fullDirty)
		if err != nil {
			return nil, nil, err
		}
		mergePageSignatures(pageSignatures, folderSignatures)
		sitemapPages = append(sitemapPages, renderedFolderPages...)

		timelinePages, timelineSignatures, err := writeTimelinePages(cfg, idx, summaryByPath, previousManifest, previousOutputPath, stagingOutputPath, options.minifier, siteLastModified, popoverMarker, sidebarTree, fullDirty)
		if err != nil {
			return nil, nil, err
		}
		mergePageSignatures(pageSignatures, timelineSignatures)
		sitemapPages = append(sitemapPages, timelinePages...)

		if !cfg.Timeline.Enabled || !cfg.Timeline.AsHomepage {
			indexPages, indexSignatures, err := writeIndexPages(cfg, idx, summaryByPath, previousManifest, previousOutputPath, stagingOutputPath, options.minifier, siteLastModified, popoverMarker, sidebarTree, fullDirty)
			if err != nil {
				return nil, nil, err
			}
			mergePageSignatures(pageSignatures, indexSignatures)
			sitemapPages = append(sitemapPages, indexPages...)
		}

		notFoundPage, err := render.Render404(render.NotFoundPageInput{
			Site:         cfg,
			RecentNotes:  append([]model.NoteSummary(nil), recentNotes...),
			LastModified: siteLastModified,
			SidebarTree:  sidebarTreeForPage(cfg, sidebarTree, ""),
		})
		if err != nil {
			return nil, nil, fmt.Errorf("render 404 page: %w", err)
		}
		if err := writeRenderedPage(stagingOutputPath, notFoundPage.Page, notFoundPage.HTML, options.minifier, popoverMarker); err != nil {
			return nil, nil, err
		}

		return sitemapPages, pageSignatures, nil
	}

	sitemapPages, pageSignatures, err := writeSitePages(diagnostics)
	if err != nil {
		return result, err
	}
	if err := writePopoverPayloads(cfg, renderedByPath, stagingOutputPath); err != nil {
		return result, fmt.Errorf("write popover payloads: %w", err)
	}

	wroteStyleCSS, err := render.EmitStyleCSS(stagingOutputPath)
	if err != nil {
		return result, fmt.Errorf("emit style.css: %w", err)
	}
	if wroteStyleCSS {
		if err := minifyCSSFile(filepath.Join(stagingOutputPath, "style.css"), options.minifier); err != nil {
			return result, err
		}
	}
	if err := render.EmitRuntimeAssets(stagingOutputPath); err != nil {
		return result, fmt.Errorf("emit runtime assets: %w", err)
	}
	if err := copyThemeInputs(normalizedVaultPath, theme, stagingOutputPath); err != nil {
		return result, err
	}
	if err := copyCustomCSS(normalizedVaultPath, cfg.CustomCSS, stagingOutputPath); err != nil {
		return result, err
	}

	result.Assets = mergedAssets
	if err := internalasset.CopyAssetsWithReservedPaths(scanResult.VaultPath, stagingOutputPath, mergedAssets, diagnostics, reservedAssetOutputPaths); err != nil {
		return result, fmt.Errorf("copy assets: %w", err)
	}

	sitemapXML, err := seo.BuildSitemap(sitemapPages)
	if err != nil {
		return result, fmt.Errorf("build sitemap: %w", err)
	}
	if err := writeOutputFile(stagingOutputPath, "sitemap.xml", sitemapXML); err != nil {
		return result, err
	}

	if err := writeOutputFile(stagingOutputPath, "robots.txt", []byte(seo.BuildRobots(cfg.BaseURL))); err != nil {
		return result, err
	}

	if cfg.RSS.Enabled {
		rssXML, err := seo.BuildRSS(cfg, recentNotes)
		if err != nil {
			return result, fmt.Errorf("build rss: %w", err)
		}
		if err := writeOutputFile(stagingOutputPath, "index.xml", rssXML); err != nil {
			return result, err
		}
	}

	if !disableCacheReuse {
		manifest := buildCacheManifest(buildABISignature, configSignature, templateSignature, result.Graph, noteStatesByPath, pageSignatures)
		if err := writeCacheManifest(stagingOutputPath, manifest); err != nil {
			return result, err
		}
	}

	return result, nil
}

func normalizeBuildOptions(options buildOptions) buildOptions {
	options.concurrency = normalizeConcurrency(options.concurrency)
	if options.diagnosticsWriter == nil {
		options.diagnosticsWriter = os.Stderr
	}
	if options.minifier == nil {
		options.minifier = newSiteMinifier()
	}
	return options
}

func prepareStagedOutputPublisher(vaultPath string, outputPath string) (*stagedOutputPublisher, error) {
	boundary, err := internalfsutil.ResolveVaultOutput(vaultPath, outputPath)
	if err != nil {
		return nil, err
	}
	outputPath = boundary.OutputPath

	state, err := validateManagedOutputDir(outputPath)
	if err != nil {
		return nil, err
	}

	publisher := &stagedOutputPublisher{
		outputPath:    outputPath,
		initialOutput: state,
	}
	stagingParent, err := nearestExistingOutputAncestor(outputPath)
	if err != nil {
		return nil, err
	}
	stagingPath, err := os.MkdirTemp(stagingParent, managedOutputTempPattern(outputPath, "stage"))
	if err != nil {
		return nil, fmt.Errorf("create staged output for %q: %w", outputPath, err)
	}
	publisher.stagingPath = stagingPath
	return publisher, nil
}

func nearestExistingOutputAncestor(outputPath string) (string, error) {
	current := filepath.Dir(outputPath)
	for {
		info, err := os.Lstat(current)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return "", fmt.Errorf("output parent path %q must be a non-symlink directory", current)
			}
			return current, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("stat output parent path %q: %w", current, err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("resolve output parent for %q: reached missing filesystem root", outputPath)
		}
		current = parent
	}
}

func validateManagedOutputDir(outputPath string) (managedOutputDirState, error) {
	state, err := inspectManagedOutputDir(outputPath)
	if err != nil {
		return managedOutputDirState{}, err
	}
	if state.exists {
		if !state.isDir {
			return managedOutputDirState{}, fmt.Errorf("output path %q exists and is not a directory", outputPath)
		}
		if !state.empty && !state.hasMarker {
			return managedOutputDirState{}, fmt.Errorf("output path %q already contains unmanaged content; choose an empty directory or rebuild into a previously managed output directory", outputPath)
		}
	}
	return state, nil
}

func writeManagedOutputMarker(outputPath string) error {
	if err := os.MkdirAll(outputPath, 0o755); err != nil {
		return fmt.Errorf("create output directory %q: %w", outputPath, err)
	}
	if err := os.WriteFile(filepath.Join(outputPath, managedOutputMarkerFilename), []byte(managedOutputMarkerContents), 0o644); err != nil {
		return fmt.Errorf("write output marker for %q: %w", outputPath, err)
	}
	return nil
}

func managedOutputTempPattern(outputPath string, purpose string) string {
	base := strings.ReplaceAll(filepath.Base(outputPath), "*", "_")
	if strings.TrimSpace(base) == "" || base == "." {
		base = "site"
	}
	return fmt.Sprintf(".%s-obsite-%s-*", base, purpose)
}

func reserveManagedOutputPath(outputPath string, purpose string) (string, error) {
	reservedPath, err := os.MkdirTemp(filepath.Dir(outputPath), managedOutputTempPattern(outputPath, purpose))
	if err != nil {
		return "", fmt.Errorf("reserve %s path for %q: %w", purpose, outputPath, err)
	}
	if err := os.Remove(reservedPath); err != nil {
		return "", fmt.Errorf("reserve %s path for %q: %w", purpose, outputPath, err)
	}
	return reservedPath, nil
}

func (publisher *stagedOutputPublisher) OutputPath() string {
	if publisher == nil {
		return ""
	}
	return publisher.stagingPath
}

func (publisher *stagedOutputPublisher) Finalize(success bool) error {
	if publisher == nil {
		return nil
	}
	if success {
		if err := publisher.publish(); err != nil {
			rollbackErr := publisher.rollback()
			if rollbackErr != nil {
				return errors.Join(err, rollbackErr)
			}
			return err
		}
		return nil
	}
	return publisher.rollback()
}

func (publisher *stagedOutputPublisher) publish() error {
	if publisher == nil || publisher.stagingPath == "" {
		return nil
	}
	if err := publisher.revalidateOutput(); err != nil {
		return err
	}
	if err := publisher.createMissingOutputParents(); err != nil {
		return err
	}
	if publisher.backupPath == "" && publisher.initialOutput.exists {
		backupPath, err := reserveManagedOutputPath(publisher.outputPath, "backup")
		if err != nil {
			return err
		}
		if err := stagedOutputRename(publisher.outputPath, backupPath); err != nil {
			return fmt.Errorf("backup managed output %q: %w", publisher.outputPath, err)
		}
		publisher.backupPath = backupPath
	}
	if err := stagedOutputRename(publisher.stagingPath, publisher.outputPath); err != nil {
		return fmt.Errorf("publish staged output %q -> %q: %w", publisher.stagingPath, publisher.outputPath, err)
	}
	publisher.stagingPath = ""
	publisher.createdParentDirs = nil
	if publisher.backupPath != "" {
		if err := stagedOutputRemoveAll(publisher.backupPath); err != nil {
			return fmt.Errorf("remove previous output backup %q: %w", publisher.backupPath, err)
		}
		publisher.backupPath = ""
	}
	return nil
}

func (publisher *stagedOutputPublisher) revalidateOutput() error {
	current, err := inspectManagedOutputDir(publisher.outputPath)
	if err != nil {
		return err
	}
	initial := publisher.initialOutput
	if initial.exists != current.exists {
		return fmt.Errorf("output path %q changed while the site was being staged", publisher.outputPath)
	}
	if !initial.exists {
		return nil
	}
	if initial.info == nil || current.info == nil || !os.SameFile(initial.info, current.info) {
		return fmt.Errorf("output path %q was replaced while the site was being staged", publisher.outputPath)
	}
	if initial.empty && !current.empty {
		return fmt.Errorf("empty output path %q gained content while the site was being staged", publisher.outputPath)
	}
	if initial.hasMarker && !current.hasMarker {
		return fmt.Errorf("managed output path %q lost its marker while the site was being staged", publisher.outputPath)
	}
	return nil
}

func (publisher *stagedOutputPublisher) createMissingOutputParents() error {
	parent := filepath.Dir(publisher.outputPath)
	missing := make([]string, 0, 2)
	for current := parent; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return fmt.Errorf("output parent path %q must be a non-symlink directory", current)
			}
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat output parent path %q: %w", current, err)
		}
		missing = append(missing, current)
		if next := filepath.Dir(current); next == current {
			return fmt.Errorf("create output parent for %q: reached missing filesystem root", publisher.outputPath)
		}
	}

	for index := len(missing) - 1; index >= 0; index-- {
		if err := os.Mkdir(missing[index], 0o755); err != nil {
			cleanupErr := publisher.removeCreatedOutputParents()
			return errors.Join(fmt.Errorf("create output parent %q: %w", missing[index], err), cleanupErr)
		}
		publisher.createdParentDirs = append(publisher.createdParentDirs, missing[index])
	}
	return nil
}

func (publisher *stagedOutputPublisher) removeCreatedOutputParents() error {
	var cleanupErr error
	for index := len(publisher.createdParentDirs) - 1; index >= 0; index-- {
		if err := os.Remove(publisher.createdParentDirs[index]); err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("remove created output parent %q: %w", publisher.createdParentDirs[index], err))
		}
	}
	publisher.createdParentDirs = nil
	return cleanupErr
}

func (publisher *stagedOutputPublisher) rollback() (cleanupErr error) {
	if publisher == nil {
		return nil
	}
	defer func() {
		cleanupErr = errors.Join(cleanupErr, publisher.removeCreatedOutputParents())
	}()

	if publisher.stagingPath != "" {
		if err := stagedOutputRemoveAll(publisher.stagingPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("remove staged output %q: %w", publisher.stagingPath, err))
		}
		publisher.stagingPath = ""
	}
	if publisher.backupPath == "" {
		return cleanupErr
	}

	if _, err := stagedOutputStat(publisher.backupPath); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("stat previous output backup %q: %w", publisher.backupPath, err))
		}
		publisher.backupPath = ""
		return cleanupErr
	}

	if _, err := stagedOutputStat(publisher.outputPath); errors.Is(err, os.ErrNotExist) {
		if restoreErr := stagedOutputRename(publisher.backupPath, publisher.outputPath); restoreErr != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("restore previous output %q: %w", publisher.outputPath, restoreErr))
		} else {
			publisher.backupPath = ""
		}
		return cleanupErr
	} else if err != nil {
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("stat output path %q: %w", publisher.outputPath, err))
		return cleanupErr
	}

	if err := stagedOutputRemoveAll(publisher.backupPath); err != nil {
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("remove previous output backup %q: %w", publisher.backupPath, err))
	} else {
		publisher.backupPath = ""
	}
	return cleanupErr
}

func inspectManagedOutputDir(outputPath string) (managedOutputDirState, error) {
	info, err := os.Lstat(outputPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return managedOutputDirState{}, nil
		}
		return managedOutputDirState{}, fmt.Errorf("stat output path %q: %w", outputPath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return managedOutputDirState{}, fmt.Errorf("output path %q must not be a symbolic link", outputPath)
	}

	state := managedOutputDirState{info: info, exists: true, isDir: info.IsDir()}
	if !state.isDir {
		return state, nil
	}

	entries, err := os.ReadDir(outputPath)
	if err != nil {
		return managedOutputDirState{}, fmt.Errorf("read output path %q: %w", outputPath, err)
	}
	state.empty = len(entries) == 0
	for _, entry := range entries {
		if entry.Name() == managedOutputMarkerFilename {
			state.hasMarker = true
			break
		}
	}

	return state, nil
}

func previousManagedOutputPath(publisher *stagedOutputPublisher) string {
	if publisher == nil {
		return ""
	}
	if strings.TrimSpace(publisher.backupPath) != "" {
		return publisher.backupPath
	}
	return publisher.outputPath
}

func finalizeBuild(result *BuildResult, diagnostics *diag.Collector, writer io.Writer, buildErr error) (*BuildResult, error) {
	if result == nil {
		result = &BuildResult{}
	}
	if diagnostics == nil {
		diagnostics = diag.NewCollector()
	}

	result.Diagnostics = diagnostics.Diagnostics()
	result.WarningCount = diagnostics.WarningCount()
	result.ErrorCount = diagnostics.ErrorCount()

	if buildErr == nil && diagnostics.HasErrors() {
		buildErr = diagnosticBuildError{count: diagnostics.ErrorCount()}
	}
	if summaryErr := writeDiagnosticsSummary(writer, diagnostics, buildErr); summaryErr != nil {
		if buildErr == nil {
			buildErr = summaryErr
		} else {
			buildErr = errors.Join(buildErr, summaryErr)
		}
	}

	return result, buildErr
}

func buildNoteStates(idx *model.VaultIndex, assetSink *internalasset.AssetCollector, concurrency int, previous *CacheManifest, noteHashes map[string]string, noteRenderSignatures map[string]string, noteDerivedSignatures map[string]map[string]string, fullDirty bool) ([]*noteBuildState, error) {
	notes := allPublicNotes(idx)
	return runOrderedPipeline(notes, concurrency, func(note *model.Note) (*noteBuildState, error) {
		if note == nil {
			return nil, nil
		}

		contentHash := noteHashes[note.RelPath]
		renderSignature := noteRenderSignatures[note.RelPath]
		derivedSignatures := cloneSignatureMap(noteDerivedSignatures[note.RelPath])
		if !fullDirty {
			if entry, ok := cacheManifestEntry(previous, note.RelPath); ok && entry.ContentHash == contentHash && entry.RenderSignature == renderSignature {
				return &noteBuildState{
					rendered:          rebuildCachedRenderedNote(note, entry),
					contentHash:       contentHash,
					renderSignature:   renderSignature,
					derivedSignatures: derivedSignatures,
					assets:            cacheManifestAssets(entry.Assets),
					renderDiagnostics: cloneDiagnostics(entry.RenderDiagnostics),
					pageDiagnostics:   cloneDiagnostics(entry.PageDiagnostics),
					fromCache:         true,
				}, nil
			}
		}

		recorder := newNoteAssetRecorder(assetSink)
		rendered, err := renderMarkdownNote(idx, note, recorder)
		state := &noteBuildState{
			rendered:          rendered,
			contentHash:       contentHash,
			renderSignature:   renderSignature,
			derivedSignatures: derivedSignatures,
			assets:            recorder.Snapshot(),
		}
		if rendered != nil && rendered.diag != nil {
			state.renderDiagnostics = rendered.diag.Diagnostics()
		}
		return state, err
	})
}

func rebuildCachedRenderedNote(note *model.Note, entry cacheManifestNote) *renderedNote {
	rendered := cloneNote(note)
	rendered.HTMLContent = entry.HTMLContent
	rendered.HasMath = entry.HasMath
	rendered.HasMermaid = entry.HasMermaid
	rendered.Summary = render.VisibleSummary(rendered)

	return &renderedNote{
		source:   note,
		rendered: rendered,
		outLinks: cloneLinkRefs(entry.OutLinks),
	}
}

func buildRenderedSummaryMap(renderedByPath map[string]*renderedNote) map[string]string {
	if len(renderedByPath) == 0 {
		return map[string]string{}
	}

	summaries := make(map[string]string, len(renderedByPath))
	for relPath, rendered := range renderedByPath {
		if rendered == nil {
			continue
		}

		note := rendered.rendered
		if note == nil {
			note = rendered.source
		}
		if note == nil {
			continue
		}

		summaries[relPath] = note.Summary
	}

	return summaries
}

func noteSummary(note *model.Note, summaryByPath map[string]string) string {
	if note == nil {
		return ""
	}
	if summary, ok := summaryByPath[note.RelPath]; ok {
		return summary
	}
	return note.Summary
}

func determineDirtyNotePages(
	cfg model.SiteConfig,
	idx *model.VaultIndex,
	contentDirtyPaths map[string]struct{},
	removedPaths map[string]struct{},
	previous *CacheManifest,
	currentDerivedSignatures map[string]map[string]string,
	fullDirty bool,
) map[string]struct{} {
	if idx == nil {
		return map[string]struct{}{}
	}
	if fullDirty {
		return allPublicNotePathSet(idx)
	}

	relatedDirtyPaths := map[string]struct{}{}
	if cfg.Related.Enabled {
		var comparable bool
		relatedDirtyPaths, comparable = relatedDerivedSignaturesChanged(idx, currentDerivedSignatures, previous, contentDirtyPaths)
		if !comparable {
			return allPublicNotePathSet(idx)
		}
	}
	sidebarTreeDirty := cfg.Sidebar.Enabled && sidebarDerivedSignaturesChanged(idx, currentDerivedSignatures, previous)
	if sidebarTreeDirty {
		return allPublicNotePathSet(idx)
	}

	backlinkDirtyPaths := map[string]struct{}{}
	if len(contentDirtyPaths) > 0 || len(removedPaths) > 0 {
		var comparable bool
		backlinkDirtyPaths, comparable = backlinkDerivedSignaturesChanged(idx, currentDerivedSignatures, previous, contentDirtyPaths)
		if !comparable {
			return allPublicNotePathSet(idx)
		}
	}
	if len(contentDirtyPaths) == 0 && len(removedPaths) == 0 && len(relatedDirtyPaths) == 0 && len(backlinkDirtyPaths) == 0 {
		return map[string]struct{}{}
	}

	dirty := make(map[string]struct{}, len(contentDirtyPaths)+len(relatedDirtyPaths)+len(backlinkDirtyPaths))

	for relPath := range contentDirtyPaths {
		dirty[relPath] = struct{}{}
	}
	for relPath := range relatedDirtyPaths {
		dirty[relPath] = struct{}{}
	}
	for relPath := range backlinkDirtyPaths {
		dirty[relPath] = struct{}{}
	}

	current := allPublicNotePathSet(idx)
	for relPath := range dirty {
		if _, ok := current[relPath]; !ok {
			delete(dirty, relPath)
		}
	}

	return dirty
}

func allPublicNotePathSet(idx *model.VaultIndex) map[string]struct{} {
	paths := make(map[string]struct{})
	if idx == nil {
		return paths
	}
	for relPath := range idx.Notes {
		paths[relPath] = struct{}{}
	}
	return paths
}

func mergeBuildAssets(indexed map[string]*model.Asset, noteStates map[string]*noteBuildState) map[string]*model.Asset {
	merged := make(map[string]*model.Asset, len(indexed))
	mergeBuildAssetMap(merged, indexed)
	for _, relPath := range sortedNoteBuildStatePaths(noteStates) {
		state := noteStates[relPath]
		if state == nil {
			continue
		}
		mergeBuildAssetMap(merged, state.assets)
	}
	return merged
}

func mergeBuildAssetMap(dst map[string]*model.Asset, src map[string]*model.Asset) {
	if len(src) == 0 {
		return
	}
	for srcPath, asset := range src {
		if strings.TrimSpace(srcPath) == "" || asset == nil {
			continue
		}
		existing := dst[srcPath]
		if existing == nil {
			cloned := *asset
			dst[srcPath] = &cloned
			continue
		}
		existing.RefCount += asset.RefCount
		if existing.DstPath == "" {
			existing.DstPath = asset.DstPath
		}
	}
}

func rerenderNotesWithMismatchedAssetDestinations(idx *model.VaultIndex, assetSink markdown.AssetSink, states map[string]*noteBuildState, plannedDestinations map[string]string) (map[string]struct{}, error) {
	dirty := make(map[string]struct{})
	for _, relPath := range sortedNoteBuildStatePaths(states) {
		state := states[relPath]
		if state == nil || state.rendered == nil || state.rendered.source == nil {
			continue
		}
		if assetDestinationsMatch(state.assets, plannedDestinations) {
			continue
		}

		wasFromCache := state.fromCache
		recorder := newNoteAssetRecorder(assetSink)
		rendered, err := renderMarkdownNote(idx, state.rendered.source, recorder)
		if err != nil {
			return nil, err
		}

		state.rendered = rendered
		state.assets = recorder.Snapshot()
		state.fromCache = false
		state.renderDiagnostics = nil
		state.pageDiagnostics = nil
		if rendered != nil && rendered.diag != nil {
			state.renderDiagnostics = rendered.diag.Diagnostics()
		}
		if wasFromCache {
			dirty[relPath] = struct{}{}
		}
	}

	return dirty, nil
}

func assetDestinationsMatch(assets map[string]*model.Asset, plannedDestinations map[string]string) bool {
	if len(assets) == 0 {
		return true
	}

	for srcPath, asset := range assets {
		if strings.TrimSpace(srcPath) == "" || asset == nil {
			continue
		}
		if normalizeAssetDestinationPath(asset.DstPath) != normalizeAssetDestinationPath(plannedDestinations[srcPath]) {
			return false
		}
	}

	return true
}

func applyAssetDestinationPlanToNoteStates(states map[string]*noteBuildState, plannedDestinations map[string]string) {
	for _, relPath := range sortedNoteBuildStatePaths(states) {
		state := states[relPath]
		if state == nil {
			continue
		}
		applyAssetDestinationPlan(state.assets, plannedDestinations)
	}
}

func applyAssetDestinationPlan(assets map[string]*model.Asset, plannedDestinations map[string]string) {
	if len(assets) == 0 {
		return
	}

	for srcPath, asset := range assets {
		if strings.TrimSpace(srcPath) == "" || asset == nil {
			continue
		}
		asset.DstPath = normalizeAssetDestinationPath(plannedDestinations[srcPath])
	}
}

func normalizeAssetDestinationPath(value string) string {
	cleaned := strings.TrimSpace(strings.ReplaceAll(value, `\`, "/"))
	cleaned = strings.TrimPrefix(cleaned, "/")
	if cleaned == "" {
		return ""
	}

	cleaned = path.Clean(cleaned)
	if cleaned == "." {
		return ""
	}

	return cleaned
}

func sortedNoteBuildStatePaths(states map[string]*noteBuildState) []string {
	paths := make([]string, 0, len(states))
	for relPath := range states {
		paths = append(paths, relPath)
	}
	sort.Strings(paths)
	return paths
}

func writeNotePages(
	cfg model.SiteConfig,
	idx *model.VaultIndex,
	renderedByPath map[string]*renderedNote,
	graph *model.LinkGraph,
	previousOutputPath string,
	outputPath string,
	minifier *minify.M,
	diagnostics *diag.Collector,
	popoverMarker *popoverLinkMarker,
	sidebarTree []model.SidebarNode,
	notePageDirty map[string]struct{},
	noteStates map[string]*noteBuildState,
	relatedArticlesByPath map[string][]model.RelatedArticle,
	notePageHook func(render.NotePageInput),
) ([]model.PageData, error) {
	paths := sortedRenderedPaths(renderedByPath)
	pages := make([]model.PageData, 0, len(paths))

	for _, relPath := range paths {
		renderedNote := renderedByPath[relPath]
		if renderedNote == nil || renderedNote.rendered == nil {
			continue
		}
		state := noteStates[relPath]
		_, pageIsDirty := notePageDirty[relPath]

		if !pageIsDirty {
			pageRelPath := notePageRelPath(renderedNote.rendered)
			copied, err := copyPageFromPreviousOutput(previousOutputPath, outputPath, pageRelPath)
			if err != nil {
				return nil, err
			}
			if copied {
				if diagnostics != nil && state != nil {
					for _, diagnostic := range state.pageDiagnostics {
						diagnostics.Add(diagnostic)
					}
				}
				pages = append(pages, buildNotePageSitemapData(cfg, renderedNote.rendered))
				continue
			}
		}

		renderInput := render.NotePageInput{
			Site:            cfg,
			Note:            renderedNote.rendered,
			Tags:            buildTagLinks(notePageRelPath(renderedNote.rendered), idx, renderedNote.rendered.Tags),
			Backlinks:       buildBacklinks(notePageRelPath(renderedNote.rendered), idx, graph, renderedNote.source.RelPath),
			RelatedArticles: cloneRelatedArticles(relatedArticlesByPath[renderedNote.source.RelPath]),
			SidebarTree:     sidebarTreeForPage(cfg, sidebarTree, renderedNote.rendered.Slug),
		}
		if notePageHook != nil {
			notePageHook(renderInput)
		}
		page, err := renderNotePage(renderInput)
		if state != nil {
			state.pageDiagnostics = cloneDiagnostics(page.Diagnostics)
		}
		for _, diagnostic := range page.Diagnostics {
			if diagnostics != nil {
				diagnostics.Add(diagnostic)
			}
		}
		if err != nil {
			return nil, fmt.Errorf("render note page %q: %w", relPath, err)
		}
		if err := writeRenderedPage(outputPath, page.Page, page.HTML, minifier, popoverMarker); err != nil {
			return nil, err
		}
		pages = append(pages, page.Page)
	}

	return pages, nil
}

func copyPageFromPreviousOutput(previousOutputPath string, outputPath string, relPath string) (bool, error) {
	if strings.TrimSpace(previousOutputPath) == "" {
		return false, nil
	}
	data, err := os.ReadFile(filepath.Join(previousOutputPath, filepath.FromSlash(relPath)))
	if err != nil {
		return false, nil
	}
	if err := writeOutputFile(outputPath, relPath, data); err != nil {
		return false, err
	}
	return true, nil
}

func mergePageSignatures(target map[string]string, source map[string]string) {
	if len(source) == 0 {
		return
	}

	for relPath, signature := range source {
		target[relPath] = signature
	}
}

func buildStaticPageSitemapData(kind model.PageKind, cfg model.SiteConfig, relPath string, lastModified time.Time) model.PageData {
	page := model.PageData{
		Kind:         kind,
		Site:         cfg,
		RelPath:      cleanSitePath(relPath),
		LastModified: lastModified,
	}
	page.Canonical = seo.Build(page, nil).Canonical
	return page
}

func buildNotePageSitemapData(cfg model.SiteConfig, note *model.Note) model.PageData {
	page := model.PageData{
		Kind:         model.PageNote,
		Site:         cfg,
		Slug:         note.Slug,
		RelPath:      notePageRelPath(note),
		Date:         note.Frontmatter.Date,
		LastModified: note.LastModified,
	}
	page.Canonical = seo.Build(page, note).Canonical
	return page
}

type popoverPayload struct {
	Title   string   `json:"title"`
	Summary string   `json:"summary"`
	Tags    []string `json:"tags"`
}

type popoverLinkMarker struct {
	siteBaseURL *url.URL
	notePaths   map[string]string
}

func newPopoverLinkMarker(cfg model.SiteConfig, renderedByPath map[string]*renderedNote) (*popoverLinkMarker, error) {
	if !cfg.Popover.Enabled {
		return nil, nil
	}

	baseURL, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse base URL: %w", err)
	}
	if baseURL.Path == "" {
		baseURL.Path = "/"
	}
	if !strings.HasSuffix(baseURL.Path, "/") {
		baseURL.Path += "/"
	}

	notePaths := make(map[string]string, len(renderedByPath)*2)
	for _, rendered := range renderedByPath {
		if rendered == nil {
			continue
		}

		note := rendered.rendered
		if note == nil {
			note = rendered.source
		}
		if note == nil {
			continue
		}

		popoverPath := cleanSitePath(note.Slug)
		if popoverPath == "" {
			continue
		}

		notePaths[popoverPath] = popoverPath
		notePaths[path.Join(popoverPath, "index.html")] = popoverPath
	}

	if len(notePaths) == 0 {
		return nil, nil
	}

	return &popoverLinkMarker{siteBaseURL: baseURL, notePaths: notePaths}, nil
}

func (m *popoverLinkMarker) annotate(relPath string, html []byte) ([]byte, error) {
	if m == nil || len(m.notePaths) == 0 || len(html) == 0 {
		return html, nil
	}

	doc, err := xhtml.Parse(bytes.NewReader(html))
	if err != nil {
		return nil, fmt.Errorf("parse HTML: %w", err)
	}

	pageURL := m.pageURL(relPath)
	changed := false

	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node == nil {
			return
		}

		if node.Type == xhtml.ElementNode && strings.EqualFold(node.Data, "a") {
			if popoverPath := m.popoverPathForNode(pageURL, node); popoverPath != "" {
				if setHTMLAttr(node, "data-popover-path", popoverPath) {
					changed = true
				}
			}
		}

		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}

	walk(doc)
	if !changed {
		return html, nil
	}

	var buf bytes.Buffer
	if err := xhtml.Render(&buf, doc); err != nil {
		return nil, fmt.Errorf("render HTML: %w", err)
	}

	return buf.Bytes(), nil
}

func (m *popoverLinkMarker) popoverPathForNode(pageURL *url.URL, node *xhtml.Node) string {
	if node == nil {
		return ""
	}

	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, "href") {
			return m.popoverPathForHref(pageURL, attr.Val)
		}
	}

	return ""
}

func (m *popoverLinkMarker) popoverPathForHref(pageURL *url.URL, href string) string {
	trimmed := strings.TrimSpace(href)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return ""
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return ""
	}
	if parsed.Scheme != "" {
		switch strings.ToLower(parsed.Scheme) {
		case "http", "https":
		default:
			return ""
		}
	}
	if pageURL == nil {
		return ""
	}

	targetURL := pageURL.ResolveReference(parsed)
	siteRelativePath := m.siteRelativePath(targetURL)
	if siteRelativePath == "" {
		return ""
	}

	return m.notePaths[siteRelativePath]
}

func (m *popoverLinkMarker) pageURL(relPath string) *url.URL {
	if m == nil || m.siteBaseURL == nil {
		return nil
	}

	return m.siteBaseURL.ResolveReference(&url.URL{Path: pageURLPath(relPath)})
}

func (m *popoverLinkMarker) siteRelativePath(targetURL *url.URL) string {
	if m == nil || m.siteBaseURL == nil || targetURL == nil {
		return ""
	}
	if !samePopoverOrigin(targetURL, m.siteBaseURL) {
		return ""
	}

	basePath := m.siteBaseURL.Path
	if basePath == "" {
		basePath = "/"
	}
	if !strings.HasSuffix(basePath, "/") {
		basePath += "/"
	}

	targetPath := targetURL.Path
	if targetPath == "" {
		targetPath = "/"
	}

	if basePath == "/" {
		return normalizePopoverTargetPath(strings.TrimPrefix(targetPath, "/"))
	}

	trimmedBasePath := strings.TrimSuffix(basePath, "/")
	if targetPath == trimmedBasePath || targetPath == basePath {
		return ""
	}
	if !strings.HasPrefix(targetPath, basePath) {
		return ""
	}

	return normalizePopoverTargetPath(strings.TrimPrefix(targetPath, basePath))
}

func samePopoverOrigin(targetURL *url.URL, siteBaseURL *url.URL) bool {
	if targetURL == nil || siteBaseURL == nil {
		return false
	}
	if !strings.EqualFold(targetURL.Scheme, siteBaseURL.Scheme) {
		return false
	}
	if !strings.EqualFold(targetURL.Hostname(), siteBaseURL.Hostname()) {
		return false
	}

	return popoverEffectivePort(targetURL) == popoverEffectivePort(siteBaseURL)
}

func popoverEffectivePort(targetURL *url.URL) string {
	if targetURL == nil {
		return ""
	}
	if port := targetURL.Port(); port != "" {
		return port
	}

	switch strings.ToLower(targetURL.Scheme) {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}

func normalizePopoverTargetPath(sitePath string) string {
	trimmed := strings.TrimSpace(strings.ReplaceAll(sitePath, `\`, "/"))
	trimmed = strings.TrimPrefix(trimmed, "/")
	if trimmed == "" {
		return ""
	}

	lower := strings.ToLower(trimmed)
	if lower == "index.html" {
		return ""
	}
	if strings.HasSuffix(lower, "/index.html") {
		trimmed = trimmed[:len(trimmed)-len("/index.html")]
	}

	trimmed = strings.TrimSuffix(trimmed, "/")
	if trimmed == "" {
		return ""
	}

	return cleanSitePath(trimmed)
}

func pageURLPath(relPath string) string {
	clean := cleanSitePath(relPath)
	if clean == "" || clean == "index.html" {
		return ""
	}
	if strings.HasSuffix(clean, "/index.html") {
		return strings.TrimSuffix(clean, "index.html")
	}
	return clean
}

func setHTMLAttr(node *xhtml.Node, key string, value string) bool {
	if node == nil {
		return false
	}

	for index := range node.Attr {
		if strings.EqualFold(node.Attr[index].Key, key) {
			if node.Attr[index].Val == value {
				return false
			}
			node.Attr[index].Val = value
			return true
		}
	}

	node.Attr = append(node.Attr, xhtml.Attribute{Key: key, Val: value})
	return true
}

func writePopoverPayloads(cfg model.SiteConfig, renderedByPath map[string]*renderedNote, outputPath string) error {
	if !cfg.Popover.Enabled {
		return nil
	}

	for _, relPath := range sortedRenderedPaths(renderedByPath) {
		rendered := renderedByPath[relPath]
		if rendered == nil {
			continue
		}

		note := rendered.rendered
		if note == nil {
			note = rendered.source
		}
		if note == nil {
			continue
		}

		slug := cleanSitePath(note.Slug)
		if slug == "" {
			continue
		}

		payload := popoverPayload{
			Title:   noteDisplayTitle(note),
			Summary: note.Summary,
			Tags:    append([]string{}, note.Tags...),
		}
		if payload.Tags == nil {
			payload.Tags = []string{}
		}

		content, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal popover payload %q: %w", relPath, err)
		}
		if err := writeOutputFile(outputPath, path.Join("_popover", slug+".json"), content); err != nil {
			return err
		}
	}

	return nil
}

func writeIndexPages(cfg model.SiteConfig, idx *model.VaultIndex, summaryByPath map[string]string, previous *CacheManifest, previousOutputPath string, outputPath string, minifier *minify.M, lastModified time.Time, popoverMarker *popoverLinkMarker, sidebarTree []model.SidebarNode, fullDirty bool) ([]model.PageData, map[string]string, error) {
	recentNotes := recentPublicNotes(idx)
	paginatedNotes := paginate(recentNotes, cfg.Pagination.PageSize)
	pages := make([]model.PageData, 0, len(paginatedNotes))
	signatures := make(map[string]string, len(paginatedNotes))
	baseRelPath := "index.html"

	for pageNumber, notesPage := range paginatedNotes {
		currentPage := pageNumber + 1
		currentRelPath := paginatedListPageRelPath(baseRelPath, currentPage)
		input := render.IndexPageInput{
			Site:         cfg,
			RecentNotes:  buildNoteSummaries(currentRelPath, idx, notesPage, summaryByPath),
			LastModified: lastModified,
			RelPath:      currentRelPath,
			Pagination:   buildPaginationData(currentRelPath, baseRelPath, currentPage, len(paginatedNotes)),
			SidebarTree:  sidebarTreeForPage(cfg, sidebarTree, ""),
		}
		signature, err := buildInputSignature(input)
		if err != nil {
			return nil, nil, fmt.Errorf("build index page signature %q: %w", currentRelPath, err)
		}
		signatures[currentRelPath] = signature
		if shouldReuseCachedPage(previous, currentRelPath, signature, fullDirty) {
			copied, err := copyPageFromPreviousOutput(previousOutputPath, outputPath, currentRelPath)
			if err != nil {
				return nil, nil, err
			}
			if copied {
				pages = append(pages, buildStaticPageSitemapData(model.PageIndex, cfg, currentRelPath, lastModified))
				continue
			}
		}

		page, err := renderIndexPage(input)
		if err != nil {
			return nil, nil, fmt.Errorf("render index: %w", err)
		}
		if err := writeRenderedPage(outputPath, page.Page, page.HTML, minifier, popoverMarker); err != nil {
			return nil, nil, err
		}
		pages = append(pages, page.Page)
	}

	return pages, signatures, nil
}

func writeTagPages(cfg model.SiteConfig, idx *model.VaultIndex, summaryByPath map[string]string, previous *CacheManifest, previousOutputPath string, outputPath string, minifier *minify.M, popoverMarker *popoverLinkMarker, sidebarTree []model.SidebarNode, fullDirty bool) ([]model.PageData, map[string]string, error) {
	tagNames := sortedTagNames(idx)
	pages := make([]model.PageData, 0, len(tagNames))
	signatures := make(map[string]string)

	for _, tagName := range tagNames {
		tag := idx.Tags[tagName]
		if tag == nil {
			continue
		}

		tagPageRelPath := path.Join(cleanSitePath(tag.Slug), "index.html")
		notes := notesForTag(idx, tag)
		lastModified := maxLastModified(notes)
		paginatedNotes := paginate(notes, cfg.Pagination.PageSize)
		for pageNumber, notesPage := range paginatedNotes {
			currentPage := pageNumber + 1
			currentRelPath := paginatedListPageRelPath(tagPageRelPath, currentPage)
			input := render.TagPageInput{
				Site:         cfg,
				Tag:          tag,
				ChildTags:    buildChildTagLinks(currentRelPath, idx, tag),
				Notes:        buildNoteSummaries(currentRelPath, idx, notesPage, summaryByPath),
				LastModified: lastModified,
				RelPath:      currentRelPath,
				Pagination:   buildPaginationData(currentRelPath, tagPageRelPath, currentPage, len(paginatedNotes)),
				SidebarTree:  sidebarTreeForPage(cfg, sidebarTree, ""),
			}
			signature, err := buildInputSignature(input)
			if err != nil {
				return nil, nil, fmt.Errorf("build tag page signature %q: %w", currentRelPath, err)
			}
			signatures[currentRelPath] = signature
			if shouldReuseCachedPage(previous, currentRelPath, signature, fullDirty) {
				copied, err := copyPageFromPreviousOutput(previousOutputPath, outputPath, currentRelPath)
				if err != nil {
					return nil, nil, err
				}
				if copied {
					pages = append(pages, buildStaticPageSitemapData(model.PageTag, cfg, currentRelPath, lastModified))
					continue
				}
			}

			page, err := renderTagPage(input)
			if err != nil {
				return nil, nil, fmt.Errorf("render tag page %q: %w", tag.Name, err)
			}
			if err := writeRenderedPage(outputPath, page.Page, page.HTML, minifier, popoverMarker); err != nil {
				return nil, nil, err
			}
			pages = append(pages, page.Page)
		}
	}

	return pages, signatures, nil
}

func writeFolderPages(cfg model.SiteConfig, idx *model.VaultIndex, summaryByPath map[string]string, folders []folderPageSpec, previous *CacheManifest, previousOutputPath string, outputPath string, minifier *minify.M, popoverMarker *popoverLinkMarker, sidebarTree []model.SidebarNode, fullDirty bool) ([]model.PageData, map[string]string, error) {
	pages := make([]model.PageData, 0, len(folders))
	signatures := make(map[string]string)

	for _, folder := range folders {
		folderPath := cleanSitePath(folder.Path)
		if folderPath == "" {
			continue
		}

		folderPageRelPath := path.Join(folderPath, "index.html")
		lastModified := maxLastModified(folder.Notes)
		paginatedNotes := paginate(folder.Notes, cfg.Pagination.PageSize)
		for pageNumber, notesPage := range paginatedNotes {
			currentPage := pageNumber + 1
			currentRelPath := paginatedListPageRelPath(folderPageRelPath, currentPage)
			input := render.FolderPageInput{
				Site:         cfg,
				FolderPath:   folderPath,
				Children:     buildNoteSummaries(currentRelPath, idx, notesPage, summaryByPath),
				LastModified: lastModified,
				RelPath:      currentRelPath,
				Pagination:   buildPaginationData(currentRelPath, folderPageRelPath, currentPage, len(paginatedNotes)),
				SidebarTree:  sidebarTreeForPage(cfg, sidebarTree, folderPath),
			}
			signature, err := buildInputSignature(input)
			if err != nil {
				return nil, nil, fmt.Errorf("build folder page signature %q: %w", currentRelPath, err)
			}
			signatures[currentRelPath] = signature
			if shouldReuseCachedPage(previous, currentRelPath, signature, fullDirty) {
				copied, err := copyPageFromPreviousOutput(previousOutputPath, outputPath, currentRelPath)
				if err != nil {
					return nil, nil, err
				}
				if copied {
					pages = append(pages, buildStaticPageSitemapData(model.PageFolder, cfg, currentRelPath, lastModified))
					continue
				}
			}

			page, err := renderFolderPage(input)
			if err != nil {
				return nil, nil, fmt.Errorf("render folder page %q: %w", folderPath, err)
			}
			if err := writeRenderedPage(outputPath, page.Page, page.HTML, minifier, popoverMarker); err != nil {
				return nil, nil, err
			}
			pages = append(pages, page.Page)
		}
	}

	return pages, signatures, nil
}

func writeTimelinePages(cfg model.SiteConfig, idx *model.VaultIndex, summaryByPath map[string]string, previous *CacheManifest, previousOutputPath string, outputPath string, minifier *minify.M, lastModified time.Time, popoverMarker *popoverLinkMarker, sidebarTree []model.SidebarNode, fullDirty bool) ([]model.PageData, map[string]string, error) {
	if !cfg.Timeline.Enabled {
		return nil, nil, nil
	}

	timelineRelPath := timelinePageRelPath(cfg.Timeline)
	recentNotes := recentPublicNotes(idx)
	paginatedNotes := paginate(recentNotes, cfg.Pagination.PageSize)
	pages := make([]model.PageData, 0, len(paginatedNotes))
	signatures := make(map[string]string, len(paginatedNotes))

	for pageNumber, notesPage := range paginatedNotes {
		currentPage := pageNumber + 1
		currentRelPath := paginatedListPageRelPath(timelineRelPath, currentPage)
		input := render.TimelinePageInput{
			Site:         cfg,
			TimelinePath: cfg.Timeline.Path,
			Notes:        buildNoteSummaries(currentRelPath, idx, notesPage, summaryByPath),
			LastModified: lastModified,
			AsHomepage:   cfg.Timeline.AsHomepage,
			RelPath:      currentRelPath,
			Pagination:   buildPaginationData(currentRelPath, timelineRelPath, currentPage, len(paginatedNotes)),
			SidebarTree:  sidebarTreeForPage(cfg, sidebarTree, ""),
		}
		signature, err := buildInputSignature(input)
		if err != nil {
			return nil, nil, fmt.Errorf("build timeline page signature %q: %w", currentRelPath, err)
		}
		signatures[currentRelPath] = signature
		if shouldReuseCachedPage(previous, currentRelPath, signature, fullDirty) {
			copied, err := copyPageFromPreviousOutput(previousOutputPath, outputPath, currentRelPath)
			if err != nil {
				return nil, nil, err
			}
			if copied {
				pages = append(pages, buildStaticPageSitemapData(model.PageTimeline, cfg, currentRelPath, lastModified))
				continue
			}
		}

		page, err := renderTimelinePage(input)
		if err != nil {
			return nil, nil, fmt.Errorf("render timeline page: %w", err)
		}
		if err := writeRenderedPage(outputPath, page.Page, page.HTML, minifier, popoverMarker); err != nil {
			return nil, nil, err
		}
		pages = append(pages, page.Page)
	}

	return pages, signatures, nil
}

func newSiteMinifier() *minify.M {
	m := minify.New()
	m.Add("text/html", &minhtml.Minifier{})
	m.Add("text/css", &mincss.Minifier{})
	return m
}

func minifyCSSFile(filePath string, minifier *minify.M) error {
	if minifier == nil {
		return nil
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("read stylesheet %q: %w", filePath, err)
	}
	minified, err := minifier.Bytes("text/css", data)
	if err != nil {
		return fmt.Errorf("minify stylesheet %q: %w", filePath, err)
	}
	if err := os.WriteFile(filePath, minified, 0o644); err != nil {
		return fmt.Errorf("write stylesheet %q: %w", filePath, err)
	}
	return nil
}

func buildReservedAssetOutputPaths(customCSSPath string, theme themeInputs) []string {
	reserved := make([]string, 0, len(render.RuntimeAssetOutputPaths())+len(theme.assets)+2)
	seen := make(map[string]struct{}, len(render.RuntimeAssetOutputPaths())+len(theme.assets)+2)
	appendReserved := func(value string) {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return
		}
		if _, ok := seen[trimmed]; ok {
			return
		}
		seen[trimmed] = struct{}{}
		reserved = append(reserved, trimmed)
	}

	for _, outputPath := range render.RuntimeAssetOutputPaths() {
		appendReserved(outputPath)
	}
	if theme.stylesheet != "" {
		appendReserved(themeCSSOutputPath)
	}
	for _, asset := range theme.assets {
		appendReserved(asset.outputPath)
	}
	if strings.TrimSpace(customCSSPath) != "" {
		appendReserved(customCSSOutputPath)
	}

	return reserved
}

func resolveCustomCSSSource(vaultRoot string, customCSSPath string) (string, error) {
	trimmedPath := strings.TrimSpace(customCSSPath)
	fixedPath := filepath.Join(vaultRoot, internalconfig.CustomCSSFilename)
	if trimmedPath != "" {
		configuredPath := trimmedPath
		if !filepath.IsAbs(configuredPath) {
			configuredPath = filepath.Join(vaultRoot, configuredPath)
		}
		configuredPath, err := filepath.Abs(configuredPath)
		if err != nil {
			return "", fmt.Errorf("resolve custom CSS %q: %w", trimmedPath, err)
		}
		if filepath.Clean(configuredPath) != filepath.Clean(fixedPath) {
			return "", fmt.Errorf("custom CSS %q must be the fixed vault input %q", trimmedPath, fixedPath)
		}
	}

	resolvedPath, _, err := internalfsutil.InspectContainedRegularFile(vaultRoot, fixedPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if trimmedPath == "" {
				return "", nil
			}
			return "", fmt.Errorf("custom CSS %q does not exist", fixedPath)
		}
		if errors.Is(err, internalfsutil.ErrUnsupportedRegularFileSource) || errors.Is(err, internalfsutil.ErrSymlinkPath) {
			return "", fmt.Errorf("custom CSS %q must be a regular non-symlink file inside the vault", fixedPath)
		}
		return "", fmt.Errorf("inspect custom CSS %q: %w", fixedPath, err)
	}

	return resolvedPath, nil
}

func copyCustomCSS(vaultRoot string, customCSSPath string, outputRoot string) error {
	trimmedPath := strings.TrimSpace(customCSSPath)
	if trimmedPath == "" {
		return nil
	}

	_, data, _, err := internalfsutil.ReadContainedRegularFile(vaultRoot, trimmedPath)
	if err != nil {
		return fmt.Errorf("read custom CSS %q: %w", trimmedPath, err)
	}
	if err := writeOutputFile(outputRoot, customCSSOutputPath, data); err != nil {
		return fmt.Errorf("write custom CSS %q: %w", trimmedPath, err)
	}

	return nil
}

func writeRenderedPage(outputRoot string, page model.PageData, html []byte, minifier *minify.M, popoverMarker *popoverLinkMarker) error {
	annotated := html
	if popoverMarker != nil {
		var err error
		annotated, err = popoverMarker.annotate(page.RelPath, html)
		if err != nil {
			return fmt.Errorf("annotate popover links %q: %w", page.RelPath, err)
		}
	}

	return writeHTMLPage(outputRoot, page.RelPath, annotated, minifier)
}

func writeHTMLPage(outputRoot string, relPath string, html []byte, minifier *minify.M) error {
	minified := html
	if minifier != nil {
		var err error
		minified, err = minifier.Bytes("text/html", html)
		if err != nil {
			return fmt.Errorf("minify HTML %q: %w", relPath, err)
		}
	}
	minified = normalizePaginationGeneratedHrefs(minified)
	return writeOutputFile(outputRoot, relPath, minified)
}

func normalizePaginationGeneratedHrefs(html []byte) []byte {
	if len(html) == 0 {
		return html
	}

	return paginationGeneratedHrefPattern.ReplaceAllFunc(html, func(match []byte) []byte {
		submatches := paginationGeneratedHrefPattern.FindSubmatch(match)
		if len(submatches) != 5 {
			return match
		}

		prefix := string(submatches[1])
		switch {
		case len(submatches[2]) > 0:
			normalized, changed := normalizeRelativeDirectoryURL(string(submatches[2]))
			if !changed {
				return match
			}
			return []byte(prefix + "\"" + normalized + "\"")
		case len(submatches[3]) > 0:
			normalized, changed := normalizeRelativeDirectoryURL(string(submatches[3]))
			if !changed {
				return match
			}
			return []byte(prefix + "'" + normalized + "'")
		default:
			normalized, changed := normalizeRelativeDirectoryURL(string(submatches[4]))
			if !changed {
				return match
			}
			return []byte(prefix + normalized)
		}
	})
}

func normalizeRelativeDirectoryURL(value string) (string, bool) {
	if value == "" || strings.HasPrefix(value, "#") || strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") || hasURLScheme(value) {
		return value, false
	}

	pathPart, suffix := splitRelativeURLSuffix(value)
	if pathPart == "" {
		return value, false
	}

	hasTrailingSlash := strings.HasSuffix(pathPart, "/")
	normalizedPath := path.Clean(pathPart)
	if normalizedPath == "." {
		normalizedPath = "./"
	} else if hasTrailingSlash {
		normalizedPath = strings.TrimSuffix(normalizedPath, "/") + "/"
	}
	if strings.HasPrefix(pathPart, "./") && normalizedPath != "./" && !strings.HasPrefix(normalizedPath, "./") && !strings.HasPrefix(normalizedPath, "../") {
		normalizedPath = "./" + normalizedPath
		if hasTrailingSlash && !strings.HasSuffix(normalizedPath, "/") {
			normalizedPath += "/"
		}
	}

	normalized := normalizedPath + suffix
	return normalized, normalized != value
}

func splitRelativeURLSuffix(value string) (string, string) {
	index := strings.IndexAny(value, "?#")
	if index == -1 {
		return value, ""
	}

	return value[:index], value[index:]
}

func hasURLScheme(value string) bool {
	index := strings.IndexByte(value, ':')
	if index <= 0 {
		return false
	}

	slashIndex := strings.IndexAny(value, "/?#")
	return slashIndex == -1 || index < slashIndex
}

func writeOutputFile(outputRoot string, relPath string, content []byte) error {
	cleanRelPath, absPath, err := resolveOutputWritePath(outputRoot, relPath)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return fmt.Errorf("create output directory for %q: %w", cleanRelPath, err)
	}
	if err := os.WriteFile(absPath, content, 0o644); err != nil {
		return fmt.Errorf("write output file %q: %w", cleanRelPath, err)
	}
	return nil
}

func resolveOutputWritePath(outputRoot string, relPath string) (string, string, error) {
	cleanRelPath := cleanSitePath(relPath)
	if cleanRelPath == "" {
		return "", "", fmt.Errorf("output path is required")
	}

	absOutputRoot, err := filepath.Abs(outputRoot)
	if err != nil {
		return "", "", fmt.Errorf("resolve output root %q: %w", outputRoot, err)
	}
	absPath := filepath.Join(absOutputRoot, filepath.FromSlash(cleanRelPath))
	relToRoot, err := filepath.Rel(absOutputRoot, absPath)
	if err != nil {
		return "", "", fmt.Errorf("resolve output path %q: %w", cleanRelPath, err)
	}
	if relToRoot == ".." || strings.HasPrefix(relToRoot, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("output path %q must stay within output root", cleanRelPath)
	}

	return cleanRelPath, absPath, nil
}

func buildRecentNotes(idx *model.VaultIndex, currentRelPath string, summaryByPath map[string]string) []model.NoteSummary {
	return buildNoteSummaries(currentRelPath, idx, recentPublicNotes(idx), summaryByPath)
}

func recentPublicNotes(idx *model.VaultIndex) []*model.Note {
	notes := allPublicNotes(idx)
	sort.SliceStable(notes, func(i int, j int) bool {
		return model.LessRecentNote(notes[i], notes[j])
	})
	return notes
}

func buildNoteSummaries(currentRelPath string, idx *model.VaultIndex, notes []*model.Note, summaryByPath map[string]string) []model.NoteSummary {
	noteSummaries := make([]model.NoteSummary, 0, len(notes))
	for _, note := range notes {
		if note == nil || note.Slug == "" {
			continue
		}
		noteSummaries = append(noteSummaries, model.NoteSummary{
			Title:        noteDisplayTitle(note),
			Summary:      noteSummary(note, summaryByPath),
			URL:          relativePageURL(currentRelPath, note.Slug, true),
			Date:         note.PublishedAt(),
			LastModified: note.LastModified,
			Tags:         buildTagLinks(currentRelPath, idx, note.Tags),
		})
	}
	return noteSummaries
}

func buildTagLinks(currentRelPath string, idx *model.VaultIndex, tagNames []string) []model.TagLink {
	return buildTagLinksLimited(currentRelPath, idx, tagNames, len(tagNames))
}

func buildTagLinksLimited(currentRelPath string, idx *model.VaultIndex, tagNames []string, limit int) []model.TagLink {
	if len(tagNames) == 0 || idx == nil || limit <= 0 {
		return nil
	}
	if limit > len(tagNames) {
		limit = len(tagNames)
	}

	links := make([]model.TagLink, 0, limit)
	for _, tagName := range tagNames {
		tag := idx.Tags[tagName]
		if tag == nil || tag.Slug == "" {
			continue
		}
		links = append(links, model.TagLink{
			Name: tag.Name,
			Slug: tag.Slug,
			URL:  relativePageURL(currentRelPath, tag.Slug, true),
		})
		if len(links) == limit {
			break
		}
	}
	return links
}

func buildBacklinks(currentRelPath string, idx *model.VaultIndex, graph *model.LinkGraph, noteRelPath string) []model.BacklinkEntry {
	if graph == nil || idx == nil {
		return nil
	}

	backlinkPaths := append([]string(nil), graph.Backward[noteRelPath]...)
	sort.SliceStable(backlinkPaths, func(i int, j int) bool {
		return model.LessRecentNote(idx.Notes[backlinkPaths[i]], idx.Notes[backlinkPaths[j]])
	})

	entries := make([]model.BacklinkEntry, 0, len(backlinkPaths))
	for _, relPath := range backlinkPaths {
		note := idx.Notes[relPath]
		if note == nil || note.Slug == "" {
			continue
		}
		entries = append(entries, model.BacklinkEntry{
			Title: noteDisplayTitle(note),
			URL:   relativePageURL(currentRelPath, note.Slug, true),
		})
	}
	return entries
}

func buildBacklinkDerivedSignatures(idx *model.VaultIndex, graph *model.LinkGraph) map[string]string {
	if idx == nil || len(idx.Notes) == 0 {
		return map[string]string{}
	}

	signatures := make(map[string]string, len(idx.Notes))
	for _, note := range allPublicNotes(idx) {
		if note == nil || strings.TrimSpace(note.RelPath) == "" {
			continue
		}
		signatures[note.RelPath] = buildBacklinkDerivedSignature(buildBacklinks(notePageRelPath(note), idx, graph, note.RelPath))
	}

	return signatures
}

func mergeDerivedSignatures(current map[string]map[string]string, states map[string]*noteBuildState, key string, values map[string]string) {
	if strings.TrimSpace(key) == "" || len(values) == 0 {
		return
	}

	for relPath, value := range values {
		signatures := current[relPath]
		if signatures == nil {
			signatures = make(map[string]string)
			current[relPath] = signatures
		}
		signatures[key] = value

		state := states[relPath]
		if state == nil {
			continue
		}
		if state.derivedSignatures == nil {
			state.derivedSignatures = make(map[string]string)
		}
		state.derivedSignatures[key] = value
	}
}

func cloneRelatedArticles(src []model.RelatedArticle) []model.RelatedArticle {
	if len(src) == 0 {
		return nil
	}

	dst := make([]model.RelatedArticle, len(src))
	for index := range src {
		dst[index] = src[index]
		dst[index].Tags = append([]model.TagLink(nil), src[index].Tags...)
	}

	return dst
}

func buildChildTagLinks(currentRelPath string, idx *model.VaultIndex, parent *model.Tag) []model.TagLink {
	if idx == nil || parent == nil {
		return nil
	}

	prefix := strings.TrimSpace(parent.Name)
	if prefix == "" {
		return nil
	}

	tags := make([]*model.Tag, 0)
	for _, candidate := range idx.Tags {
		if candidate == nil || candidate.Name == parent.Name {
			continue
		}
		if !isDirectChildTag(prefix, candidate.Name) {
			continue
		}
		tags = append(tags, candidate)
	}

	sort.SliceStable(tags, func(i int, j int) bool {
		if tags[i].Slug != tags[j].Slug {
			return tags[i].Slug < tags[j].Slug
		}
		return tags[i].Name < tags[j].Name
	})

	links := make([]model.TagLink, 0, len(tags))
	for _, tag := range tags {
		links = append(links, model.TagLink{
			Name: tag.Name,
			Slug: tag.Slug,
			URL:  relativePageURL(currentRelPath, tag.Slug, true),
		})
	}
	return links
}

func notesForTag(idx *model.VaultIndex, tag *model.Tag) []*model.Note {
	if idx == nil || tag == nil || len(tag.Notes) == 0 {
		return nil
	}

	notes := make([]*model.Note, 0, len(tag.Notes))
	for _, relPath := range tag.Notes {
		if note := idx.Notes[relPath]; note != nil {
			notes = append(notes, note)
		}
	}
	return notes
}

func sortedRenderedPaths(renderedByPath map[string]*renderedNote) []string {
	paths := make([]string, 0, len(renderedByPath))
	for relPath := range renderedByPath {
		paths = append(paths, relPath)
	}
	sort.Strings(paths)
	return paths
}

func sortedTagNames(idx *model.VaultIndex) []string {
	if idx == nil {
		return nil
	}

	names := make([]string, 0, len(idx.Tags))
	for name := range idx.Tags {
		names = append(names, name)
	}
	sort.SliceStable(names, func(i int, j int) bool {
		left := idx.Tags[names[i]]
		right := idx.Tags[names[j]]
		if left != nil && right != nil && left.Slug != right.Slug {
			return left.Slug < right.Slug
		}
		return names[i] < names[j]
	})
	return names
}

func buildFolderPageSpecs(idx *model.VaultIndex) []folderPageSpec {
	if idx == nil {
		return nil
	}

	notesByFolder := make(map[string][]*model.Note)
	for _, note := range allPublicNotes(idx) {
		if note == nil {
			continue
		}

		folderPath := cleanSitePath(path.Dir(strings.ReplaceAll(note.RelPath, `\`, "/")))
		for _, ancestor := range folderAncestors(folderPath) {
			notesByFolder[ancestor] = append(notesByFolder[ancestor], note)
		}
	}

	paths := make([]string, 0, len(notesByFolder))
	for folderPath := range notesByFolder {
		paths = append(paths, folderPath)
	}
	sort.Strings(paths)

	folders := make([]folderPageSpec, 0, len(paths))
	for _, folderPath := range paths {
		notes := append([]*model.Note(nil), notesByFolder[folderPath]...)
		sort.SliceStable(notes, func(i int, j int) bool {
			return model.LessRecentNote(notes[i], notes[j])
		})
		folders = append(folders, folderPageSpec{Path: folderPath, Notes: notes})
	}

	return folders
}

func buildSidebarTree(idx *model.VaultIndex) []model.SidebarNode {
	if idx == nil {
		return nil
	}

	root := make(map[string]*sidebarTreeBuilder)
	for _, note := range allPublicNotes(idx) {
		if note == nil {
			continue
		}

		noteSitePath := cleanSitePath(note.Slug)
		if noteSitePath == "" {
			continue
		}

		folderPath := cleanSitePath(path.Dir(strings.ReplaceAll(note.RelPath, `\`, "/")))
		parentPath := ""
		children := root
		for _, segment := range sidebarPathSegments(folderPath) {
			currentPath := segment
			if parentPath != "" {
				currentPath = path.Join(parentPath, segment)
			}

			key := "dir:" + currentPath
			node := children[key]
			if node == nil {
				node = &sidebarTreeBuilder{
					name:     segment,
					sitePath: currentPath,
					isDir:    true,
					children: make(map[string]*sidebarTreeBuilder),
				}
				children[key] = node
			}

			children = node.children
			parentPath = currentPath
		}

		children["note:"+note.RelPath] = &sidebarTreeBuilder{
			name:     noteDisplayTitle(note),
			sitePath: noteSitePath,
		}
	}

	return buildSidebarNodes(root)
}

func buildSidebarNodes(nodes map[string]*sidebarTreeBuilder) []model.SidebarNode {
	if len(nodes) == 0 {
		return nil
	}

	ordered := make([]*sidebarTreeBuilder, 0, len(nodes))
	for _, node := range nodes {
		ordered = append(ordered, node)
	}

	sort.SliceStable(ordered, func(i int, j int) bool {
		if ordered[i].isDir != ordered[j].isDir {
			return ordered[i].isDir
		}

		leftName := strings.ToLower(strings.TrimSpace(ordered[i].name))
		rightName := strings.ToLower(strings.TrimSpace(ordered[j].name))
		if leftName != rightName {
			return leftName < rightName
		}

		return ordered[i].sitePath < ordered[j].sitePath
	})

	result := make([]model.SidebarNode, 0, len(ordered))
	for _, node := range ordered {
		if node == nil {
			continue
		}

		sitePath := cleanSitePath(node.sitePath)
		if sitePath == "" {
			continue
		}

		result = append(result, model.SidebarNode{
			Name:     node.name,
			URL:      sitePath + "/",
			IsDir:    node.isDir,
			Children: buildSidebarNodes(node.children),
		})
	}

	return result
}

func sidebarPathSegments(raw string) []string {
	clean := cleanSitePath(raw)
	if clean == "" {
		return nil
	}

	segments := strings.Split(clean, "/")
	filtered := segments[:0]
	for _, segment := range segments {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			continue
		}
		filtered = append(filtered, segment)
	}

	return filtered
}

func pageSidebarTree(tree []model.SidebarNode, activeSitePath string) []model.SidebarNode {
	if len(tree) == 0 {
		return nil
	}

	activeSitePath = cleanSitePath(activeSitePath)
	result := make([]model.SidebarNode, len(tree))
	for index := range tree {
		result[index] = tree[index]
		result[index].IsActive = activeSitePath != "" && cleanSitePath(tree[index].URL) == activeSitePath
		result[index].Children = pageSidebarTree(tree[index].Children, activeSitePath)
	}

	return result
}

func sidebarTreeForPage(cfg model.SiteConfig, tree []model.SidebarNode, activeSitePath string) []model.SidebarNode {
	if !cfg.Sidebar.Enabled {
		return nil
	}

	return pageSidebarTree(tree, activeSitePath)
}

func folderAncestors(folderPath string) []string {
	clean := cleanSitePath(folderPath)
	if clean == "" {
		return nil
	}

	ancestors := make([]string, 0, strings.Count(clean, "/")+1)
	for clean != "" {
		ancestors = append(ancestors, clean)
		clean = cleanSitePath(path.Dir(clean))
	}

	return ancestors
}

type generatedPageRoute struct {
	relPath string
	source  string
}

type generatedPageRouteRegistry struct {
	seen map[string]generatedPageRoute
}

func detectGeneratedPageRouteConflicts(cfg model.SiteConfig, idx *model.VaultIndex, folders []folderPageSpec, diagnostics *diag.Collector) error {
	routes := buildGeneratedPageRoutes(cfg, idx, folders)
	if len(routes) == 0 {
		return nil
	}

	registry := generatedPageRouteRegistry{seen: make(map[string]generatedPageRoute, len(routes))}
	for _, route := range routes {
		if err := registry.add(route, diagnostics); err != nil {
			return err
		}
	}

	return nil
}

func buildGeneratedPageRoutes(cfg model.SiteConfig, idx *model.VaultIndex, folders []folderPageSpec) []generatedPageRoute {
	routes := make([]generatedPageRoute, 0)

	for _, note := range allPublicNotes(idx) {
		if note == nil || strings.TrimSpace(note.RelPath) == "" {
			continue
		}

		relPath := notePageRelPath(note)
		if relPath == "" {
			continue
		}

		routes = append(routes, generatedPageRoute{relPath: relPath, source: note.RelPath})
	}

	for _, tagName := range sortedTagNames(idx) {
		tag := idx.Tags[tagName]
		if tag == nil {
			continue
		}

		tagPageRelPath := path.Join(cleanSitePath(tag.Slug), "index.html")
		if tagPageRelPath == "" {
			continue
		}

		routes = appendGeneratedPaginatedRoutes(routes, tagPageRelPath, len(paginate(notesForTag(idx, tag), cfg.Pagination.PageSize)), func(pageNumber int) string {
			return tagGeneratedPageRouteSource(tag.Slug, pageNumber)
		})
	}

	for _, folder := range folders {
		folderPath := cleanSitePath(folder.Path)
		if folderPath == "" {
			continue
		}

		folderPageRelPath := path.Join(folderPath, "index.html")
		routes = appendGeneratedPaginatedRoutes(routes, folderPageRelPath, len(paginate(folder.Notes, cfg.Pagination.PageSize)), func(pageNumber int) string {
			return folderGeneratedPageRouteSource(folderPath, pageNumber)
		})
	}

	if cfg.Timeline.Enabled {
		timelineRelPath := timelinePageRelPath(cfg.Timeline)
		routes = appendGeneratedPaginatedRoutes(routes, timelineRelPath, len(paginate(recentPublicNotes(idx), cfg.Pagination.PageSize)), func(pageNumber int) string {
			return timelineGeneratedPageRouteSource(cfg.Timeline, pageNumber)
		})
	}

	if !cfg.Timeline.Enabled || !cfg.Timeline.AsHomepage {
		routes = appendGeneratedPaginatedRoutes(routes, "index.html", len(paginate(recentPublicNotes(idx), cfg.Pagination.PageSize)), indexGeneratedPageRouteSource)
	}

	routes = append(routes, generatedPageRoute{relPath: "404.html", source: "404 page"})

	return routes
}

func appendGeneratedPaginatedRoutes(routes []generatedPageRoute, baseRelPath string, totalPages int, sourceForPage func(int) string) []generatedPageRoute {
	for pageNumber := 1; pageNumber <= totalPages; pageNumber++ {
		relPath := paginatedListPageRelPath(baseRelPath, pageNumber)
		source := strings.TrimSpace(sourceForPage(pageNumber))
		if relPath == "" || source == "" {
			continue
		}

		routes = append(routes, generatedPageRoute{relPath: relPath, source: source})
	}

	return routes
}

func (r *generatedPageRouteRegistry) add(route generatedPageRoute, diagnostics *diag.Collector) error {
	cleanRelPath := cleanSitePath(route.relPath)
	source := strings.TrimSpace(route.source)
	if cleanRelPath == "" || source == "" {
		return nil
	}

	if r.seen == nil {
		r.seen = make(map[string]generatedPageRoute)
	}

	key := internalslug.Canonicalize(cleanRelPath)
	if existing, ok := r.seen[key]; ok {
		message := fmt.Sprintf("generated route %q conflicts with %s, %s", cleanRelPath, existing.source, source)
		if diagnostics != nil {
			diagnostics.Errorf(diag.KindSlugConflict, diag.Location{Path: existing.source}, "%s", message)
			diagnostics.Errorf(diag.KindSlugConflict, diag.Location{Path: source}, "%s", message)
		}

		return fmt.Errorf("generated route conflict for %q across %s, %s", cleanRelPath, existing.source, source)
	}

	r.seen[key] = generatedPageRoute{relPath: cleanRelPath, source: source}
	return nil
}

func indexGeneratedPageRouteSource(pageNumber int) string {
	if pageNumber <= 1 {
		return "index page /"
	}

	return fmt.Sprintf("index pagination / page %d", pageNumber)
}

func folderGeneratedPageRouteSource(folderPath string, pageNumber int) string {
	source := folderConflictSource(folderPath)
	if pageNumber <= 1 {
		return source
	}

	return fmt.Sprintf("folder pagination %s page %d", source, pageNumber)
}

func tagGeneratedPageRouteSource(tagPath string, pageNumber int) string {
	source := tagPageConflictSource(tagPath)
	if pageNumber <= 1 {
		return source
	}

	clean := cleanSitePath(tagPath)
	if clean == "" {
		return ""
	}

	return fmt.Sprintf("tag pagination %s/ page %d", clean, pageNumber)
}

func timelineGeneratedPageRouteSource(cfg model.TimelineConfig, pageNumber int) string {
	if cfg.AsHomepage {
		if pageNumber <= 1 {
			return "timeline page /"
		}

		return fmt.Sprintf("timeline pagination / page %d", pageNumber)
	}

	source := timelinePageConflictSource(cfg.Path)
	if pageNumber <= 1 {
		return source
	}

	clean := cleanSitePath(cfg.Path)
	if clean == "" {
		return ""
	}

	return fmt.Sprintf("timeline pagination %s/ page %d", clean, pageNumber)
}

func detectFolderPageConflicts(idx *model.VaultIndex, folders []folderPageSpec, diagnostics *diag.Collector) error {
	if len(folders) == 0 {
		return nil
	}

	notes := allPublicNotes(idx)
	candidates := make([]internalslug.Candidate, 0, len(folders)+len(notes)+len(idx.Tags))
	folderSources := make(map[string]struct{}, len(folders))
	for _, note := range notes {
		if note == nil || strings.TrimSpace(note.Slug) == "" {
			continue
		}
		candidates = append(candidates, internalslug.Candidate{Source: note.RelPath, Slug: folderPageConflictKey(note.Slug)})
	}
	for _, tagName := range sortedTagNames(idx) {
		tag := idx.Tags[tagName]
		if tag == nil || strings.TrimSpace(tag.Slug) == "" {
			continue
		}
		candidates = append(candidates, internalslug.Candidate{Source: tagPageConflictSource(tag.Slug), Slug: folderPageConflictKey(tag.Slug)})
	}
	for _, folder := range folders {
		folderPath := cleanSitePath(folder.Path)
		if folderPath == "" {
			continue
		}
		source := folderConflictSource(folderPath)
		folderSources[source] = struct{}{}
		candidates = append(candidates, internalslug.Candidate{Source: source, Slug: folderPageConflictKey(folderPath)})
	}

	conflicts, invalid := internalslug.DetectConflicts(candidates)
	if len(invalid) > 0 {
		return fmt.Errorf("invalid folder slug for %q", invalid[0].Source)
	}
	conflicts = filterConflictsBySources(conflicts, folderSources)
	if len(conflicts) == 0 {
		return nil
	}

	for _, conflict := range conflicts {
		for _, source := range conflict.Sources {
			if diagnostics != nil {
				diagnostics.Errorf(diag.KindSlugConflict, diag.Location{Path: source}, "slug %q conflicts with %s", conflict.Slug, strings.Join(conflict.Sources, ", "))
			}
		}
	}

	first := conflicts[0]
	return fmt.Errorf("slug conflict for %q across %s", first.Slug, strings.Join(first.Sources, ", "))
}

func detectTimelinePageConflicts(cfg model.SiteConfig, idx *model.VaultIndex, folders []folderPageSpec, diagnostics *diag.Collector) error {
	if !cfg.Timeline.Enabled || cfg.Timeline.AsHomepage {
		return nil
	}

	timelinePath := cleanSitePath(cfg.Timeline.Path)
	if timelinePath == "" {
		return fmt.Errorf("invalid timeline path %q", cfg.Timeline.Path)
	}

	notes := allPublicNotes(idx)
	candidates := make([]internalslug.Candidate, 0, len(folders)+len(notes)+len(idx.Tags)+1)
	timelineSource := timelinePageConflictSource(timelinePath)
	relevantSources := map[string]struct{}{timelineSource: {}}
	for _, note := range notes {
		if note == nil || strings.TrimSpace(note.Slug) == "" {
			continue
		}
		candidates = append(candidates, internalslug.Candidate{Source: note.RelPath, Slug: folderPageConflictKey(note.Slug)})
	}
	for _, tagName := range sortedTagNames(idx) {
		tag := idx.Tags[tagName]
		if tag == nil || strings.TrimSpace(tag.Slug) == "" {
			continue
		}
		candidates = append(candidates, internalslug.Candidate{Source: tagPageConflictSource(tag.Slug), Slug: folderPageConflictKey(tag.Slug)})
	}
	for _, folder := range folders {
		folderPath := cleanSitePath(folder.Path)
		if folderPath == "" {
			continue
		}
		candidates = append(candidates, internalslug.Candidate{Source: folderConflictSource(folderPath), Slug: folderPageConflictKey(folderPath)})
	}
	candidates = append(candidates, internalslug.Candidate{Source: timelineSource, Slug: folderPageConflictKey(timelinePath)})

	conflicts, invalid := internalslug.DetectConflicts(candidates)
	if len(invalid) > 0 {
		return fmt.Errorf("invalid timeline slug for %q", invalid[0].Source)
	}
	conflicts = filterConflictsBySources(conflicts, relevantSources)
	if len(conflicts) == 0 {
		return nil
	}

	for _, conflict := range conflicts {
		for _, source := range conflict.Sources {
			if diagnostics != nil {
				diagnostics.Errorf(diag.KindSlugConflict, diag.Location{Path: source}, "slug %q conflicts with %s", conflict.Slug, strings.Join(conflict.Sources, ", "))
			}
		}
	}

	first := conflicts[0]
	return fmt.Errorf("slug conflict for %q across %s", first.Slug, strings.Join(first.Sources, ", "))
}

func filterConflictsBySources(conflicts []internalslug.Conflict, sources map[string]struct{}) []internalslug.Conflict {
	if len(conflicts) == 0 {
		return nil
	}

	filtered := make([]internalslug.Conflict, 0, len(conflicts))
	for _, conflict := range conflicts {
		for _, source := range conflict.Sources {
			if _, ok := sources[source]; ok {
				filtered = append(filtered, conflict)
				break
			}
		}
	}

	return filtered
}

func folderPageConflictKey(sitePath string) string {
	clean := cleanSitePath(sitePath)
	if clean == "" {
		return ""
	}

	return strings.ToLower(clean)
}

func folderConflictSource(folderPath string) string {
	clean := cleanSitePath(folderPath)
	if clean == "" {
		return ""
	}

	return clean + "/"
}

func tagPageConflictSource(tagPath string) string {
	clean := cleanSitePath(tagPath)
	if clean == "" {
		return ""
	}

	return "tag page " + clean + "/"
}

func timelinePageConflictSource(timelinePath string) string {
	clean := cleanSitePath(timelinePath)
	if clean == "" {
		return ""
	}

	return "timeline page " + clean + "/"
}

func allPublicNotes(idx *model.VaultIndex) []*model.Note {
	if idx == nil || len(idx.Notes) == 0 {
		return nil
	}

	notes := make([]*model.Note, 0, len(idx.Notes))
	for _, note := range idx.Notes {
		notes = append(notes, note)
	}
	sort.SliceStable(notes, func(i int, j int) bool {
		if notes[i].RelPath != notes[j].RelPath {
			return notes[i].RelPath < notes[j].RelPath
		}
		return notes[i].Slug < notes[j].Slug
	})
	return notes
}

func maxLastModified(notes []*model.Note) time.Time {
	var latest time.Time
	for _, note := range notes {
		if note == nil || note.LastModified.IsZero() {
			continue
		}
		if latest.IsZero() || note.LastModified.After(latest) {
			latest = note.LastModified
		}
	}
	return latest
}

func siteLastModified(notes []*model.Note) time.Time {
	if latest := maxLastModified(notes); !latest.IsZero() {
		return latest
	}
	return minimalSiteLastModified
}

func timelinePageRelPath(cfg model.TimelineConfig) string {
	if cfg.AsHomepage {
		return "index.html"
	}

	cleanPath := cleanSitePath(cfg.Path)
	if cleanPath == "" {
		return "index.html"
	}

	return path.Join(cleanPath, "index.html")
}

func paginate[T any](items []T, pageSize int) [][]T {
	if len(items) == 0 {
		return [][]T{nil}
	}
	if pageSize <= 0 || len(items) <= pageSize {
		return [][]T{append([]T(nil), items...)}
	}

	pages := make([][]T, 0, (len(items)+pageSize-1)/pageSize)
	for start := 0; start < len(items); start += pageSize {
		end := start + pageSize
		if end > len(items) {
			end = len(items)
		}
		pages = append(pages, append([]T(nil), items[start:end]...))
	}

	return pages
}

func paginatedListPageRelPath(baseRelPath string, pageNumber int) string {
	cleanBase := cleanSitePath(baseRelPath)
	if cleanBase == "" || pageNumber <= 1 {
		return cleanBase
	}

	baseDir := path.Dir(cleanBase)
	if baseDir == "." {
		baseDir = ""
	}

	return path.Join(baseDir, "page", strconv.Itoa(pageNumber), "index.html")
}

func buildPaginationData(currentRelPath string, baseRelPath string, currentPage int, totalPages int) *model.PaginationData {
	if totalPages <= 1 {
		return nil
	}

	pagination := &model.PaginationData{
		CurrentPage: currentPage,
		TotalPages:  totalPages,
		Pages:       make([]model.PageLink, 0, totalPages),
	}
	for pageNumber := 1; pageNumber <= totalPages; pageNumber++ {
		pagination.Pages = append(pagination.Pages, model.PageLink{
			Number: pageNumber,
			URL:    paginationPageURL(currentRelPath, baseRelPath, pageNumber),
		})
	}
	if currentPage > 1 {
		pagination.PrevURL = paginationPageURL(currentRelPath, baseRelPath, currentPage-1)
	}
	if currentPage < totalPages {
		pagination.NextURL = paginationPageURL(currentRelPath, baseRelPath, currentPage+1)
	}

	return pagination
}

func paginationPageURL(currentRelPath string, baseRelPath string, pageNumber int) string {
	return relativeDirURL(currentRelPath, path.Dir(paginatedListPageRelPath(baseRelPath, pageNumber)))
}

func relativeDirURL(fromRelPath string, targetDir string) string {
	fromDir := path.Dir(cleanSitePath(fromRelPath))
	if fromDir == "" {
		fromDir = "."
	}

	target := cleanSitePath(targetDir)
	if target == "" {
		target = "."
	}

	rel, err := filepath.Rel(filepath.FromSlash(fromDir), filepath.FromSlash(target))
	if err != nil {
		return "./"
	}

	clean := filepath.ToSlash(rel)
	if clean == "." || clean == "" {
		return "./"
	}

	return strings.TrimSuffix(clean, "/") + "/"
}

func noteDisplayTitle(note *model.Note) string {
	if note == nil {
		return ""
	}
	if title := strings.TrimSpace(note.Frontmatter.Title); title != "" {
		return title
	}
	base := path.Base(strings.ReplaceAll(note.RelPath, `\`, "/"))
	if base == "." || base == "" || base == "/" {
		return ""
	}
	return strings.TrimSuffix(base, path.Ext(base))
}

func relativePageURL(fromRelPath string, targetSitePath string, directory bool) string {
	target := cleanSitePath(targetSitePath)
	if target == "" {
		if directory {
			return "./"
		}
		return ""
	}

	fromDir := path.Dir(cleanSitePath(fromRelPath))
	if fromDir == "" {
		fromDir = "."
	}

	rel, err := filepath.Rel(filepath.FromSlash(fromDir), filepath.FromSlash(target))
	if err != nil {
		rel = target
	}

	clean := filepath.ToSlash(rel)
	if clean == "." || clean == "" {
		if directory {
			return "./"
		}
		return clean
	}
	if directory && !strings.HasSuffix(clean, "/") {
		clean += "/"
	}
	return clean
}

func notePageRelPath(note *model.Note) string {
	if note == nil {
		return ""
	}
	return path.Join(cleanSitePath(note.Slug), "index.html")
}

func cleanSitePath(value string) string {
	trimmed := strings.TrimSpace(strings.ReplaceAll(value, `\`, "/"))
	trimmed = strings.TrimPrefix(trimmed, "/")
	if trimmed == "" {
		return ""
	}
	clean := path.Clean(trimmed)
	if clean == "." {
		return ""
	}
	return clean
}

func isDirectChildTag(parent string, candidate string) bool {
	prefix := strings.TrimSpace(parent)
	name := strings.TrimSpace(candidate)
	if prefix == "" || name == "" || !strings.HasPrefix(name, prefix+"/") {
		return false
	}
	remainder := strings.TrimPrefix(name, prefix+"/")
	return remainder != "" && !strings.Contains(remainder, "/")
}

func cloneNote(note *model.Note) *model.Note {
	if note == nil {
		return nil
	}

	cloned := *note
	cloned.Aliases = append([]string(nil), note.Aliases...)
	cloned.Tags = append([]string(nil), note.Tags...)
	cloned.Headings = append([]model.Heading(nil), note.Headings...)
	cloned.RawContent = append([]byte(nil), note.RawContent...)
	cloned.OutLinks = append([]model.LinkRef(nil), note.OutLinks...)
	cloned.Embeds = append([]model.EmbedRef(nil), note.Embeds...)
	cloned.ImageRefs = append([]model.ImageRef(nil), note.ImageRefs...)
	if len(note.HeadingSections) > 0 {
		cloned.HeadingSections = make(map[string]model.SectionRange, len(note.HeadingSections))
		for id, section := range note.HeadingSections {
			cloned.HeadingSections[id] = section
		}
	}
	return &cloned
}
