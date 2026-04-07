package build

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	internalasset "github.com/simp-lee/obsite/internal/asset"
	internalconfig "github.com/simp-lee/obsite/internal/config"
	"github.com/simp-lee/obsite/internal/diag"
	"github.com/simp-lee/obsite/internal/link"
	"github.com/simp-lee/obsite/internal/markdown"
	"github.com/simp-lee/obsite/internal/model"
	"github.com/simp-lee/obsite/internal/render"
	"github.com/simp-lee/obsite/internal/seo"
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

type buildOptions struct {
	concurrency       int
	diagnosticsWriter io.Writer
	minifier          *minify.M
}

type renderedNote struct {
	source   *model.Note
	rendered *model.Note
	outLinks []model.LinkRef
	diag     *diag.Collector
}

type diagnosticBuildError struct {
	count int
}

type managedOutputDirState struct {
	exists    bool
	isDir     bool
	hasMarker bool
	empty     bool
}

type stagedOutputPublisher struct {
	outputPath        string
	stagingPath       string
	backupPath        string
	hasExistingOutput bool
}

const (
	managedOutputMarkerFilename = ".obsite-output"
	managedOutputMarkerContents = "managed by obsite\n"
)

var minimalSiteLastModified = time.Unix(0, 0).UTC()

var renderMarkdownNote = func(idx *model.VaultIndex, note *model.Note, assetSink *internalasset.AssetCollector) (*renderedNote, error) {
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

	return &renderedNote{
		source:   note,
		rendered: rendered,
		outLinks: renderResult.OutLinks(),
		diag:     localDiag,
	}, nil
}

var buildVaultIndex = func(scanResult vault.ScanResult, frontmatterResult vault.FrontmatterResult, diagCollector *diag.Collector, concurrency int) (*model.VaultIndex, error) {
	return vault.BuildIndexWithConcurrency(scanResult, frontmatterResult, diagCollector, concurrency)
}

func (e diagnosticBuildError) Error() string {
	return fmt.Sprintf("build failed with %d diagnostic error(s)", e.count)
}

// Build runs the full Obsite site-generation pipeline.
func Build(cfg model.SiteConfig, vaultPath string, outputPath string) (*BuildResult, error) {
	return buildWithOptions(cfg, vaultPath, outputPath, buildOptions{})
}

func buildWithOptions(cfg model.SiteConfig, vaultPath string, outputPath string, options buildOptions) (result *BuildResult, err error) {
	options = normalizeBuildOptions(options)
	diagnostics := diag.NewCollector()
	result = &BuildResult{}

	defer func() {
		result, err = finalizeBuild(result, diagnostics, options.diagnosticsWriter, err)
	}()

	normalizedVaultPath, err := NormalizeVaultPath(vaultPath)
	if err != nil {
		return result, err
	}

	normalizedOutputPath, err := normalizeBuildOutputPath(outputPath)
	if err != nil {
		return result, err
	}
	result.OutputPath = normalizedOutputPath

	if err := validateManagedOutputPath(normalizedVaultPath, normalizedOutputPath); err != nil {
		return result, err
	}
	normalizedCfg, err := internalconfig.NormalizeSiteConfig(cfg)
	if err != nil {
		return result, fmt.Errorf("validate config: %w", err)
	}
	cfg = normalizedCfg

	publisher, err := prepareStagedOutputPublisher(normalizedVaultPath, normalizedOutputPath)
	if err != nil {
		return result, err
	}
	defer func() {
		if finalizeErr := publisher.Finalize(err == nil); finalizeErr != nil {
			if err == nil {
				err = finalizeErr
			} else {
				err = errors.Join(err, finalizeErr)
			}
		}
	}()

	scanResult, err := vault.Scan(normalizedVaultPath)
	if err != nil {
		return result, fmt.Errorf("scan vault: %w", err)
	}
	recordCanvasDiagnostics(scanResult.ResourceFiles, diagnostics)

	frontmatterResult, err := vault.ParseFrontmatter(scanResult, cfg)
	if err != nil {
		return result, fmt.Errorf("parse frontmatter: %w", err)
	}

	idx, err := buildVaultIndex(scanResult, frontmatterResult, diagnostics, options.concurrency)
	result.Index = idx
	if err != nil {
		return result, fmt.Errorf("build index: %w", err)
	}

	assetCollector := internalasset.NewCollector(scanResult.VaultPath, idx.Assets)
	renderedNotes, err := renderMarkdownPass(idx, assetCollector, options.concurrency)
	for _, note := range renderedNotes {
		if note == nil {
			continue
		}
		diagnostics.Merge(note.diag)
	}
	if err != nil {
		return result, fmt.Errorf("render markdown: %w", err)
	}
	result.NotePages = len(renderedNotes)

	renderedByPath := make(map[string]*renderedNote, len(renderedNotes))
	resolvedOutLinks := make(map[string][]model.LinkRef, len(renderedNotes))
	for _, note := range renderedNotes {
		if note == nil || note.source == nil {
			continue
		}
		renderedByPath[note.source.RelPath] = note
		resolvedOutLinks[note.source.RelPath] = append([]model.LinkRef(nil), note.outLinks...)
	}

	result.Graph = link.BuildGraph(idx, resolvedOutLinks)

	recentNotes := buildRecentNotes(idx, "index.html")
	result.RecentNotes = append([]model.NoteSummary(nil), recentNotes...)
	siteLastModified := siteLastModified(allPublicNotes(idx))
	stagingOutputPath := publisher.OutputPath()

	sitemapPages := make([]model.PageData, 0, len(renderedNotes)+len(idx.Tags)+1)
	notePages, err := writeNotePages(cfg, idx, renderedByPath, result.Graph, stagingOutputPath, options.minifier, diagnostics)
	if err != nil {
		return result, err
	}
	sitemapPages = append(sitemapPages, notePages...)

	tagPages, err := writeTagPages(cfg, idx, stagingOutputPath, options.minifier)
	if err != nil {
		return result, err
	}
	result.TagPages = len(tagPages)
	sitemapPages = append(sitemapPages, tagPages...)

	indexPage, err := render.RenderIndex(render.IndexPageInput{
		Site:         cfg,
		RecentNotes:  append([]model.NoteSummary(nil), recentNotes...),
		LastModified: siteLastModified,
	})
	if err != nil {
		return result, fmt.Errorf("render index: %w", err)
	}
	if err := writeHTMLPage(stagingOutputPath, indexPage.Page.RelPath, indexPage.HTML, options.minifier); err != nil {
		return result, err
	}
	sitemapPages = append(sitemapPages, indexPage.Page)

	notFoundPage, err := render.Render404(render.NotFoundPageInput{
		Site:         cfg,
		RecentNotes:  append([]model.NoteSummary(nil), recentNotes...),
		LastModified: siteLastModified,
	})
	if err != nil {
		return result, fmt.Errorf("render 404 page: %w", err)
	}
	if err := writeHTMLPage(stagingOutputPath, notFoundPage.Page.RelPath, notFoundPage.HTML, options.minifier); err != nil {
		return result, err
	}

	if err := render.EmitStyleCSS(stagingOutputPath); err != nil {
		return result, fmt.Errorf("emit style.css: %w", err)
	}
	if err := minifyCSSFile(filepath.Join(stagingOutputPath, "style.css"), options.minifier); err != nil {
		return result, err
	}

	mergedAssets := internalasset.MergeAssets(scanResult.VaultPath, idx.Assets, assetCollector)
	result.Assets = mergedAssets
	if err := internalasset.CopyAssets(scanResult.VaultPath, stagingOutputPath, mergedAssets, diagnostics); err != nil {
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

func normalizeBuildOutputPath(outputPath string) (string, error) {
	outputPath = strings.TrimSpace(outputPath)
	if outputPath == "" {
		return "", fmt.Errorf("output path is required")
	}

	absPath, err := filepath.Abs(outputPath)
	if err != nil {
		return "", fmt.Errorf("resolve output path %q: %w", outputPath, err)
	}
	return absPath, nil
}

// NormalizeVaultPath validates the vault path and returns its absolute form.
func NormalizeVaultPath(vaultPath string) (string, error) {
	vaultPath = strings.TrimSpace(vaultPath)
	if vaultPath == "" {
		return "", fmt.Errorf("vault path is required")
	}

	absPath, err := filepath.Abs(vaultPath)
	if err != nil {
		return "", fmt.Errorf("resolve vault path %q: %w", vaultPath, err)
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return "", fmt.Errorf("stat vault path %q: %w", absPath, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("vault path %q is not a directory", absPath)
	}

	return absPath, nil
}

func validateManagedOutputPath(vaultPath string, outputPath string) error {
	relPath, err := filepath.Rel(vaultPath, outputPath)
	if err != nil {
		return fmt.Errorf("compare output path %q against vault %q: %w", outputPath, vaultPath, err)
	}
	if relPath == "." {
		return fmt.Errorf("output path %q must not equal the vault root %q", outputPath, vaultPath)
	}
	if pathContainsPath(outputPath, vaultPath) {
		return fmt.Errorf("output path %q must not contain the vault %q", outputPath, vaultPath)
	}
	return nil
}

func prepareStagedOutputPublisher(vaultPath string, outputPath string) (*stagedOutputPublisher, error) {
	state, err := validateManagedOutputDir(outputPath)
	if err != nil {
		return nil, err
	}

	publisher := &stagedOutputPublisher{
		outputPath:        outputPath,
		hasExistingOutput: state.exists,
	}
	if pathContainsPath(vaultPath, outputPath) && state.exists {
		backupPath, err := reserveManagedOutputPath(outputPath, "backup")
		if err != nil {
			return nil, err
		}
		if err := os.Rename(outputPath, backupPath); err != nil {
			return nil, fmt.Errorf("hide managed output %q before scan: %w", outputPath, err)
		}
		publisher.backupPath = backupPath
	}

	stagingPath, err := os.MkdirTemp(filepath.Dir(outputPath), managedOutputTempPattern(outputPath, "stage"))
	if err != nil {
		rollbackErr := publisher.rollback()
		if rollbackErr != nil {
			return nil, errors.Join(fmt.Errorf("create staged output for %q: %w", outputPath, err), rollbackErr)
		}
		return nil, fmt.Errorf("create staged output for %q: %w", outputPath, err)
	}
	publisher.stagingPath = stagingPath
	if err := writeManagedOutputMarker(stagingPath); err != nil {
		rollbackErr := publisher.rollback()
		if rollbackErr != nil {
			return nil, errors.Join(err, rollbackErr)
		}
		return nil, err
	}

	return publisher, nil
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
		return publisher.publish()
	}
	return publisher.rollback()
}

func (publisher *stagedOutputPublisher) publish() error {
	if publisher == nil || publisher.stagingPath == "" {
		return nil
	}
	if publisher.backupPath == "" && publisher.hasExistingOutput {
		backupPath, err := reserveManagedOutputPath(publisher.outputPath, "backup")
		if err != nil {
			return err
		}
		if err := os.Rename(publisher.outputPath, backupPath); err != nil {
			return fmt.Errorf("backup managed output %q: %w", publisher.outputPath, err)
		}
		publisher.backupPath = backupPath
	}
	if err := os.Rename(publisher.stagingPath, publisher.outputPath); err != nil {
		return fmt.Errorf("publish staged output %q -> %q: %w", publisher.stagingPath, publisher.outputPath, err)
	}
	publisher.stagingPath = ""
	if publisher.backupPath != "" {
		if err := os.RemoveAll(publisher.backupPath); err != nil {
			return fmt.Errorf("remove previous output backup %q: %w", publisher.backupPath, err)
		}
		publisher.backupPath = ""
	}
	return nil
}

func (publisher *stagedOutputPublisher) rollback() error {
	if publisher == nil {
		return nil
	}

	var cleanupErr error
	if publisher.stagingPath != "" {
		if err := os.RemoveAll(publisher.stagingPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("remove staged output %q: %w", publisher.stagingPath, err))
		}
		publisher.stagingPath = ""
	}
	if publisher.backupPath == "" {
		return cleanupErr
	}

	if _, err := os.Stat(publisher.backupPath); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("stat previous output backup %q: %w", publisher.backupPath, err))
		}
		publisher.backupPath = ""
		return cleanupErr
	}

	if _, err := os.Stat(publisher.outputPath); errors.Is(err, os.ErrNotExist) {
		if restoreErr := os.Rename(publisher.backupPath, publisher.outputPath); restoreErr != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("restore previous output %q: %w", publisher.outputPath, restoreErr))
		} else {
			publisher.backupPath = ""
		}
		return cleanupErr
	} else if err != nil {
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("stat output path %q: %w", publisher.outputPath, err))
		return cleanupErr
	}

	if err := os.RemoveAll(publisher.backupPath); err != nil {
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("remove previous output backup %q: %w", publisher.backupPath, err))
	} else {
		publisher.backupPath = ""
	}
	return cleanupErr
}

func inspectManagedOutputDir(outputPath string) (managedOutputDirState, error) {
	info, err := os.Stat(outputPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return managedOutputDirState{}, nil
		}
		return managedOutputDirState{}, fmt.Errorf("stat output path %q: %w", outputPath, err)
	}

	state := managedOutputDirState{exists: true, isDir: info.IsDir()}
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

func pathContainsPath(rootPath string, candidatePath string) bool {
	relPath, err := filepath.Rel(rootPath, candidatePath)
	if err != nil {
		return false
	}
	if relPath == "." {
		return true
	}
	return relPath != ".." && !strings.HasPrefix(relPath, ".."+string(filepath.Separator))
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

func renderMarkdownPass(idx *model.VaultIndex, assetSink *internalasset.AssetCollector, concurrency int) ([]*renderedNote, error) {
	notes := allPublicNotes(idx)
	return runOrderedPipeline(notes, concurrency, func(note *model.Note) (*renderedNote, error) {
		return renderMarkdownNote(idx, note, assetSink)
	})
}

func writeNotePages(
	cfg model.SiteConfig,
	idx *model.VaultIndex,
	renderedByPath map[string]*renderedNote,
	graph *model.LinkGraph,
	outputPath string,
	minifier *minify.M,
	diagnostics *diag.Collector,
) ([]model.PageData, error) {
	paths := sortedRenderedPaths(renderedByPath)
	pages := make([]model.PageData, 0, len(paths))

	for _, relPath := range paths {
		renderedNote := renderedByPath[relPath]
		if renderedNote == nil || renderedNote.rendered == nil {
			continue
		}

		page, err := render.RenderNote(render.NotePageInput{
			Site:      cfg,
			Note:      renderedNote.rendered,
			Tags:      buildTagLinks(notePageRelPath(renderedNote.rendered), idx, renderedNote.rendered.Tags),
			Backlinks: buildBacklinks(notePageRelPath(renderedNote.rendered), idx, graph, renderedNote.source.RelPath),
		})
		for _, diagnostic := range page.Diagnostics {
			if diagnostics != nil {
				diagnostics.Add(diagnostic)
			}
		}
		if err != nil {
			return nil, fmt.Errorf("render note page %q: %w", relPath, err)
		}
		if err := writeHTMLPage(outputPath, page.Page.RelPath, page.HTML, minifier); err != nil {
			return nil, err
		}
		pages = append(pages, page.Page)
	}

	return pages, nil
}

func writeTagPages(cfg model.SiteConfig, idx *model.VaultIndex, outputPath string, minifier *minify.M) ([]model.PageData, error) {
	tagNames := sortedTagNames(idx)
	pages := make([]model.PageData, 0, len(tagNames))

	for _, tagName := range tagNames {
		tag := idx.Tags[tagName]
		if tag == nil {
			continue
		}

		tagPageRelPath := path.Join(cleanSitePath(tag.Slug), "index.html")
		notes := notesForTag(idx, tag)
		page, err := render.RenderTagPage(render.TagPageInput{
			Site:         cfg,
			Tag:          tag,
			ChildTags:    buildChildTagLinks(tagPageRelPath, idx, tag),
			Notes:        buildNoteSummaries(tagPageRelPath, idx, notes),
			LastModified: maxLastModified(notes),
		})
		if err != nil {
			return nil, fmt.Errorf("render tag page %q: %w", tag.Name, err)
		}
		if err := writeHTMLPage(outputPath, page.Page.RelPath, page.HTML, minifier); err != nil {
			return nil, err
		}
		pages = append(pages, page.Page)
	}

	return pages, nil
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

func writeHTMLPage(outputRoot string, relPath string, html []byte, minifier *minify.M) error {
	minified := html
	if minifier != nil {
		var err error
		minified, err = minifier.Bytes("text/html", html)
		if err != nil {
			return fmt.Errorf("minify HTML %q: %w", relPath, err)
		}
	}
	return writeOutputFile(outputRoot, relPath, minified)
}

func writeOutputFile(outputRoot string, relPath string, content []byte) error {
	cleanRelPath := cleanSitePath(relPath)
	if cleanRelPath == "" {
		return fmt.Errorf("output path is required")
	}

	absPath := filepath.Join(outputRoot, filepath.FromSlash(cleanRelPath))
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return fmt.Errorf("create output directory for %q: %w", cleanRelPath, err)
	}
	if err := os.WriteFile(absPath, content, 0o644); err != nil {
		return fmt.Errorf("write output file %q: %w", cleanRelPath, err)
	}
	return nil
}

func buildRecentNotes(idx *model.VaultIndex, currentRelPath string) []model.NoteSummary {
	notes := allPublicNotes(idx)
	sort.SliceStable(notes, func(i int, j int) bool {
		return model.LessRecentNote(notes[i], notes[j])
	})
	return buildNoteSummaries(currentRelPath, idx, notes)
}

func buildNoteSummaries(currentRelPath string, idx *model.VaultIndex, notes []*model.Note) []model.NoteSummary {
	summaries := make([]model.NoteSummary, 0, len(notes))
	for _, note := range notes {
		if note == nil || note.Slug == "" {
			continue
		}
		summaries = append(summaries, model.NoteSummary{
			Title: noteDisplayTitle(note),
			URL:   relativePageURL(currentRelPath, note.Slug, true),
			Date:  note.PublishedAt(),
			Tags:  buildTagLinks(currentRelPath, idx, note.Tags),
		})
	}
	return summaries
}

func buildTagLinks(currentRelPath string, idx *model.VaultIndex, tagNames []string) []model.TagLink {
	if len(tagNames) == 0 || idx == nil {
		return nil
	}

	links := make([]model.TagLink, 0, len(tagNames))
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
	if len(note.HeadingSections) > 0 {
		cloned.HeadingSections = make(map[string]model.SectionRange, len(note.HeadingSections))
		for id, section := range note.HeadingSections {
			cloned.HeadingSections[id] = section
		}
	}
	return &cloned
}
