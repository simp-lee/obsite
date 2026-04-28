package build

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/simp-lee/obsite/internal/diag"
	internalfsutil "github.com/simp-lee/obsite/internal/fsutil"
	internalembed "github.com/simp-lee/obsite/internal/markdown/embed"
	markdownwikilink "github.com/simp-lee/obsite/internal/markdown/wikilink"
	"github.com/simp-lee/obsite/internal/model"
	internalrender "github.com/simp-lee/obsite/internal/render"
	"github.com/simp-lee/obsite/internal/resourcepath"
)

const (
	cacheManifestDir             = ".obsite-cache"
	cacheManifestRelPath         = cacheManifestDir + "/manifest.json"
	cacheManifestVersion         = 2
	defaultTemplateSigKey        = "default"
	missingTemplateSigKey        = "missing"
	cacheSignatureSaltKey        = "phase-21-step-50"
	derivedSignatureKeyBacklinks = "backlinks"
	derivedSignatureKeySidebar   = "sidebar"
	derivedSignatureKeyRelated   = "related"
)

var listTemplateAssetsForSignature = internalrender.EmbeddedTemplateAssetNames

var readDefaultTemplateAssetForSignature = internalrender.ReadEmbeddedTemplateAsset

var readBuildABISourceSignature = buildABISourceSignature

var readBuildABISignature = sync.OnceValues(computeBuildABISignature)

type buildABISourceSignatureUnavailableError struct {
	cause             error
	fallbackSignature string
}

func (err *buildABISourceSignatureUnavailableError) Error() string {
	if err == nil {
		return ""
	}
	if err.cause == nil {
		return "build ABI source signature unavailable"
	}
	return fmt.Sprintf("build ABI source signature unavailable: %v", err.cause)
}

func (err *buildABISourceSignatureUnavailableError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.cause
}

func templateAssetNamesForCacheSignature() []string {
	names := listTemplateAssetsForSignature()
	filtered := make([]string, 0, len(names))
	for _, name := range names {
		if strings.EqualFold(strings.TrimSpace(name), "style.css") {
			continue
		}
		filtered = append(filtered, name)
	}
	return filtered
}

type themeTemplateSignatureFile struct {
	relPath string
	absPath string
}

type noteRenderSignatureBuilder struct {
	idx        *model.VaultIndex
	noteHashes map[string]string
	memo       map[string]string
}

// Options controls build-time behavior that should remain stable across the CLI and serve paths.
type Options struct {
	Force             bool
	DiagnosticsWriter io.Writer
}

// CacheManifest stores the incremental-build state that can be safely reused on the next run.
type CacheManifest struct {
	Version              int                          `json:"version"`
	BuildABISignature    string                       `json:"buildABISignature"`
	ConfigSignature      string                       `json:"configSignature"`
	TemplateSignature    string                       `json:"templateSignature"`
	SearchIndexSignature string                       `json:"searchIndexSignature,omitempty"`
	Graph                model.LinkGraph              `json:"graph"`
	Pages                map[string]string            `json:"pages,omitempty"`
	Notes                map[string]cacheManifestNote `json:"notes"`
}

type cacheManifestNote struct {
	ContentHash       string            `json:"contentHash"`
	RenderSignature   string            `json:"renderSignature"`
	DerivedSignatures map[string]string `json:"derivedSignatures,omitempty"`
	HTMLContent       string            `json:"htmlContent"`
	HasMath           bool              `json:"hasMath,omitempty"`
	HasMermaid        bool              `json:"hasMermaid,omitempty"`
	OutLinks          []model.LinkRef   `json:"outLinks,omitempty"`
	Assets            []model.Asset     `json:"assets,omitempty"`
	RenderDiagnostics []diag.Diagnostic `json:"renderDiagnostics,omitempty"`
	PageDiagnostics   []diag.Diagnostic `json:"pageDiagnostics,omitempty"`
}

type noteHashSnapshot struct {
	current map[string]string
	changed map[string]struct{}
	removed map[string]struct{}
}

func loadCacheManifest(outputRoot string) (*CacheManifest, error) {
	manifestPath := filepath.Join(strings.TrimSpace(outputRoot), filepath.FromSlash(cacheManifestRelPath))
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read cache manifest %q: %w", manifestPath, err)
	}

	var manifest CacheManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("parse cache manifest %q: %w", manifestPath, err)
	}
	if manifest.Version != cacheManifestVersion {
		return nil, nil
	}
	if manifest.Notes == nil {
		manifest.Notes = map[string]cacheManifestNote{}
	}
	if manifest.Pages == nil {
		manifest.Pages = map[string]string{}
	}
	if manifest.Graph.Forward == nil {
		manifest.Graph.Forward = map[string][]string{}
	}
	if manifest.Graph.Backward == nil {
		manifest.Graph.Backward = map[string][]string{}
	}

	return &manifest, nil
}

func warnCacheManifestLoadFailure(collector *diag.Collector, loadErr error) {
	if collector == nil || loadErr == nil {
		return
	}

	collector.Warningf(
		diag.KindStructuredData,
		diag.Location{Path: cacheManifestRelPath},
		"incremental cache manifest could not be loaded (%v); falling back to a full rebuild. Delete %q if this warning repeats",
		loadErr,
		cacheManifestRelPath,
	)
}

func warnBuildABISourceSignatureFailure(collector *diag.Collector, signatureErr error) {
	if collector == nil || signatureErr == nil {
		return
	}

	collector.Warningf(
		diag.KindStructuredData,
		diag.Location{Path: cacheManifestRelPath},
		"build ABI source signature could not be collected (%v); disabling incremental cache reuse and forcing a full rebuild for this run",
		signatureErr,
	)
}

func writeCacheManifest(outputRoot string, manifest *CacheManifest) error {
	if manifest == nil {
		return nil
	}

	manifest.Version = cacheManifestVersion
	if manifest.Notes == nil {
		manifest.Notes = map[string]cacheManifestNote{}
	}
	if manifest.Pages == nil {
		manifest.Pages = map[string]string{}
	}
	if manifest.Graph.Forward == nil {
		manifest.Graph.Forward = map[string][]string{}
	}
	if manifest.Graph.Backward == nil {
		manifest.Graph.Backward = map[string][]string{}
	}

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal cache manifest: %w", err)
	}
	data = append(data, '\n')

	return writeOutputFile(outputRoot, cacheManifestRelPath, data)
}

func buildConfigSignature(cfg model.SiteConfig) (string, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("marshal config signature: %w", err)
	}

	hasher := sha256.New()
	_, _ = hasher.Write([]byte(cacheSignatureSaltKey))
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write(data)
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func buildTemplateSignature(themeName string, themeRoot string, themeAssets []internalrender.ThemeStaticAsset) (string, error) {
	trimmedRoot := strings.TrimSpace(themeRoot)
	if trimmedRoot == "" {
		return buildEmbeddedTemplateSignature()
	}

	cleanRoot := filepath.Clean(trimmedRoot)
	if info, err := os.Stat(cleanRoot); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return missingThemeTemplateSignature(themeName, cleanRoot, missingTemplateSigKey), nil
		}
		return "", fmt.Errorf("stat theme root %q: %w", cleanRoot, err)
	} else if !info.IsDir() {
		return missingThemeTemplateSignature(themeName, cleanRoot, "not-a-directory"), nil
	}

	htmlFiles, err := listThemeTemplateFilesForSignature(cleanRoot)
	if err != nil {
		return "", err
	}

	hasher := newCacheSignatureHasher("theme-templates")
	cacheHashWriteString(hasher, strings.TrimSpace(themeName))
	cacheHashWriteString(hasher, cleanRoot)

	for _, file := range htmlFiles {
		data, err := os.ReadFile(file.absPath)
		if err != nil {
			return "", fmt.Errorf("read theme template %q: %w", file.relPath, err)
		}
		cacheHashWriteString(hasher, "html")
		cacheHashWriteString(hasher, file.relPath)
		cacheHashWriteString(hasher, string(data))
	}

	stylePath := filepath.Join(cleanRoot, "style.css")
	styleData, styleFound, err := readThemeStyleSignatureFile(stylePath)
	if err != nil {
		return "", err
	}
	if styleFound {
		cacheHashWriteString(hasher, "style.css")
		cacheHashWriteString(hasher, string(styleData))
	}

	for _, asset := range sortThemeStaticAssetsForSignature(themeAssets) {
		data, err := os.ReadFile(asset.SourcePath)
		if err != nil {
			return "", fmt.Errorf("read theme static asset %q: %w", asset.ThemeRelativePath, err)
		}
		cacheHashWriteString(hasher, "asset")
		cacheHashWriteString(hasher, strings.TrimSpace(asset.ThemeRelativePath))
		cacheHashWriteString(hasher, strings.TrimSpace(asset.OutputPath))
		cacheHashWriteString(hasher, string(data))
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func missingThemeTemplateSignature(themeName string, themeRoot string, marker string) string {
	hasher := newCacheSignatureHasher("theme-templates")
	cacheHashWriteString(hasher, strings.TrimSpace(themeName))
	cacheHashWriteString(hasher, strings.TrimSpace(themeRoot))
	cacheHashWriteString(hasher, strings.TrimSpace(marker))
	return hex.EncodeToString(hasher.Sum(nil))
}

func listThemeTemplateFilesForSignature(themeRoot string) ([]themeTemplateSignatureFile, error) {
	if strings.TrimSpace(themeRoot) == "" {
		return nil, nil
	}

	files := make([]themeTemplateSignatureFile, 0, len(internalrender.RequiredHTMLTemplateNames))
	err := filepath.WalkDir(themeRoot, func(currentPath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk theme root %q: %w", themeRoot, walkErr)
		}
		if entry == nil || entry.IsDir() {
			return nil
		}

		relPath, err := filepath.Rel(themeRoot, currentPath)
		if err != nil {
			return fmt.Errorf("relative theme template path %q: %w", currentPath, err)
		}
		relPath = filepath.ToSlash(relPath)
		if !strings.EqualFold(filepath.Ext(relPath), ".html") {
			return nil
		}

		resolvedPath, _, err := internalfsutil.InspectRegularNonSymlinkFile(currentPath)
		if err != nil {
			if errors.Is(err, internalfsutil.ErrUnsupportedRegularFileSource) {
				return fmt.Errorf("theme HTML template %q must be a regular non-symlink file", currentPath)
			}

			return fmt.Errorf("stat theme HTML template %q: %w", currentPath, err)
		}

		files = append(files, themeTemplateSignatureFile{
			relPath: relPath,
			absPath: resolvedPath,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(files, func(i int, j int) bool {
		return files[i].relPath < files[j].relPath
	})

	return files, nil
}

func readThemeStyleSignatureFile(stylePath string) ([]byte, bool, error) {
	resolvedPath, _, err := internalfsutil.InspectRegularNonSymlinkFile(stylePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		if errors.Is(err, internalfsutil.ErrUnsupportedRegularFileSource) {
			return nil, false, fmt.Errorf("theme stylesheet %q must be a regular non-symlink file", stylePath)
		}
		return nil, false, fmt.Errorf("stat theme stylesheet %q: %w", stylePath, err)
	}

	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		return nil, false, fmt.Errorf("read theme stylesheet %q: %w", resolvedPath, err)
	}

	return data, true, nil
}

func sortThemeStaticAssetsForSignature(themeAssets []internalrender.ThemeStaticAsset) []internalrender.ThemeStaticAsset {
	if len(themeAssets) == 0 {
		return nil
	}

	ordered := append([]internalrender.ThemeStaticAsset(nil), themeAssets...)
	sort.Slice(ordered, func(i int, j int) bool {
		if ordered[i].ThemeRelativePath == ordered[j].ThemeRelativePath {
			return ordered[i].OutputPath < ordered[j].OutputPath
		}
		return ordered[i].ThemeRelativePath < ordered[j].ThemeRelativePath
	})

	return ordered
}

func buildEmbeddedTemplateSignature() (string, error) {
	hasher := newCacheSignatureHasher("embedded-templates")
	cacheHashWriteString(hasher, defaultTemplateSigKey)

	for _, name := range templateAssetNamesForCacheSignature() {
		cacheHashWriteString(hasher, name)

		data, err := readDefaultTemplateAssetForSignature(name)
		if err != nil {
			return "", fmt.Errorf("read embedded template asset %q: %w", name, err)
		}
		cacheHashWriteString(hasher, string(data))
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func computeBuildABISignature() (string, error) {
	hasher := newCacheSignatureHasher("build-abi")
	cacheHashWriteString(hasher, runtime.Version())

	if info, ok := debug.ReadBuildInfo(); ok && info != nil {
		cacheHashWriteString(hasher, info.String())
	}

	sourceSignature, ok, err := readBuildABISourceSignature()
	if err != nil {
		fallbackSignature := hex.EncodeToString(hasher.Sum(nil))
		return fallbackSignature, &buildABISourceSignatureUnavailableError{
			cause:             err,
			fallbackSignature: fallbackSignature,
		}
	}
	if ok {
		cacheHashWriteString(hasher, sourceSignature)
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func buildABISourceSignature() (string, bool, error) {
	repoRoot, ok := buildABISourceRoot()
	if !ok {
		return "", false, nil
	}

	signature, err := buildABISourceSignatureFromRoot(repoRoot)
	if err != nil {
		return "", false, err
	}
	if strings.TrimSpace(signature) == "" {
		return "", false, nil
	}

	return signature, true, nil
}

func buildABISourceRoot() (string, bool) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", false
	}

	candidates := []string{file}
	if !filepath.IsAbs(file) {
		if cwd, err := os.Getwd(); err == nil {
			candidates = append(candidates, filepath.Join(cwd, file))
		}
	}

	for _, candidate := range candidates {
		repoRoot := filepath.Clean(filepath.Join(filepath.Dir(candidate), "..", ".."))
		info, err := os.Stat(filepath.Join(repoRoot, "go.mod"))
		if err == nil && !info.IsDir() {
			return repoRoot, true
		}
	}

	return "", false
}

func buildABISourceSignatureFromRoot(repoRoot string) (string, error) {
	files, err := collectBuildABISourceFiles(repoRoot)
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "", nil
	}

	hasher := newCacheSignatureHasher("build-abi-source")
	for _, relPath := range files {
		cacheHashWriteString(hasher, relPath)

		data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(relPath)))
		if err != nil {
			return "", fmt.Errorf("read build ABI source %q: %w", relPath, err)
		}
		cacheHashWriteString(hasher, string(data))
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func collectBuildABISourceFiles(repoRoot string) ([]string, error) {
	repoRoot = strings.TrimSpace(repoRoot)
	if repoRoot == "" {
		return nil, nil
	}

	files := make([]string, 0, 128)
	for _, relDir := range []string{"cmd", "internal", "templates"} {
		absDir := filepath.Join(repoRoot, relDir)
		info, err := os.Stat(absDir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("stat build ABI dir %q: %w", relDir, err)
		}
		if !info.IsDir() {
			continue
		}

		if err := filepath.WalkDir(absDir, func(currentPath string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}

			name := entry.Name()
			if filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
				return nil
			}

			relPath, err := filepath.Rel(repoRoot, currentPath)
			if err != nil {
				return err
			}
			files = append(files, filepath.ToSlash(relPath))
			return nil
		}); err != nil {
			return nil, fmt.Errorf("walk build ABI dir %q: %w", relDir, err)
		}
	}

	for _, relPath := range []string{"go.mod", "go.sum", "go.work"} {
		info, err := os.Stat(filepath.Join(repoRoot, relPath))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("stat build ABI file %q: %w", relPath, err)
		}
		if info.IsDir() {
			continue
		}

		files = append(files, relPath)
	}

	sort.Strings(files)
	return files, nil
}

func buildInputSignature(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("marshal input signature: %w", err)
	}

	hasher := newCacheSignatureHasher("input")
	cacheHashWriteString(hasher, string(data))
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func buildSearchIndexInputSignature(outputRoot string) (string, error) {
	trimmedRoot := strings.TrimSpace(outputRoot)
	if trimmedRoot == "" {
		return "", fmt.Errorf("search index output root is required")
	}

	hasher := newCacheSignatureHasher("search-index")
	err := filepath.Walk(trimmedRoot, func(currentPath string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if currentPath == trimmedRoot {
			return nil
		}

		relPath, err := filepath.Rel(trimmedRoot, currentPath)
		if err != nil {
			return err
		}
		relPath = filepath.ToSlash(relPath)

		if info.IsDir() {
			switch relPath {
			case cacheManifestDir, pagefindOutputSubdir:
				return filepath.SkipDir
			}
			return nil
		}

		if !strings.EqualFold(filepath.Ext(relPath), ".html") {
			return nil
		}

		data, err := os.ReadFile(currentPath)
		if err != nil {
			return err
		}
		data = normalizeHTMLForSearchSignature(data)

		cacheHashWriteString(hasher, relPath)
		cacheHashWriteString(hasher, string(data))
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("walk search index inputs in %q: %w", trimmedRoot, err)
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func buildNoteHashes(vaultRoot string, idx *model.VaultIndex) (map[string]string, error) {
	if idx == nil || len(idx.Notes) == 0 {
		return map[string]string{}, nil
	}

	paths := make([]string, 0, len(idx.Notes))
	for relPath := range idx.Notes {
		paths = append(paths, relPath)
	}
	sort.Strings(paths)

	hashes := make(map[string]string, len(paths))
	for _, relPath := range paths {
		data, err := os.ReadFile(filepath.Join(vaultRoot, filepath.FromSlash(relPath)))
		if err != nil {
			return nil, fmt.Errorf("read note source %q: %w", relPath, err)
		}
		sum := sha256.Sum256(data)
		hashes[relPath] = hex.EncodeToString(sum[:])
	}

	return hashes, nil
}

func buildNoteRenderSignatures(idx *model.VaultIndex, noteHashes map[string]string) map[string]string {
	if idx == nil || len(idx.Notes) == 0 {
		return map[string]string{}
	}

	builder := noteRenderSignatureBuilder{
		idx:        idx,
		noteHashes: noteHashes,
		memo:       make(map[string]string, len(idx.Notes)*2),
	}
	signatures := make(map[string]string, len(idx.Notes))
	for _, relPath := range sortedNoteSignaturePaths(idx.Notes) {
		signatures[relPath] = builder.signatureFor(relPath, "", 0, nil)
	}

	return signatures
}

func buildNoteDerivedSignatures(idx *model.VaultIndex) map[string]map[string]string {
	if idx == nil || len(idx.Notes) == 0 {
		return map[string]map[string]string{}
	}

	derived := make(map[string]map[string]string, len(idx.Notes))
	for _, note := range allPublicNotes(idx) {
		if note == nil || strings.TrimSpace(note.RelPath) == "" {
			continue
		}

		derived[note.RelPath] = map[string]string{
			derivedSignatureKeySidebar: buildSidebarDerivedSignature(note),
		}
	}

	return derived
}

func buildSidebarDerivedSignature(note *model.Note) string {
	hasher := newCacheSignatureHasher("sidebar")
	cacheHashWriteString(hasher, strings.TrimSpace(noteDisplayTitle(note)))
	cacheHashWriteString(hasher, strings.TrimSpace(note.RelPath))
	cacheHashWriteString(hasher, strings.TrimSpace(note.Slug))
	return hex.EncodeToString(hasher.Sum(nil))
}

func buildBacklinkDerivedSignature(entries []model.BacklinkEntry) string {
	hasher := newCacheSignatureHasher("backlinks")
	cacheHashWriteInt(hasher, len(entries))
	for _, entry := range entries {
		cacheHashWriteString(hasher, strings.TrimSpace(entry.Title))
		cacheHashWriteString(hasher, strings.TrimSpace(entry.URL))
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

func buildRelatedDerivedSignature(articles []model.RelatedArticle) string {
	hasher := newCacheSignatureHasher("related")
	cacheHashWriteInt(hasher, len(articles))
	for _, article := range articles {
		cacheHashWriteString(hasher, strings.TrimSpace(article.Title))
		cacheHashWriteString(hasher, strings.TrimSpace(article.URL))
		cacheHashWriteString(hasher, strings.TrimSpace(article.Summary))
		cacheHashWriteFloat64(hasher, article.Score)
		cacheHashWriteInt(hasher, len(article.Tags))
		for _, tag := range article.Tags {
			cacheHashWriteString(hasher, strings.TrimSpace(tag.Name))
			cacheHashWriteString(hasher, strings.TrimSpace(tag.Slug))
			cacheHashWriteString(hasher, strings.TrimSpace(tag.URL))
		}
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

func sortedNoteSignaturePaths(notes map[string]*model.Note) []string {
	paths := make([]string, 0, len(notes))
	for relPath := range notes {
		paths = append(paths, relPath)
	}
	sort.Strings(paths)
	return paths
}

func noteRenderVisitKey(relPath string, fragmentID string) string {
	relPath = strings.TrimSpace(relPath)
	fragmentID = strings.TrimSpace(fragmentID)
	if relPath == "" {
		return ""
	}
	if fragmentID == "" {
		return relPath
	}
	return relPath + "#" + fragmentID
}

func noteRenderMemoKey(relPath string, fragmentID string, depth int) string {
	visitKey := noteRenderVisitKey(relPath, fragmentID)
	if visitKey == "" {
		return ""
	}
	return visitKey + "@depth=" + strconv.Itoa(depth)
}

func (b *noteRenderSignatureBuilder) signatureFor(relPath string, fragmentID string, depth int, stack map[string]struct{}) string {
	relPath = strings.TrimSpace(relPath)
	fragmentID = strings.TrimSpace(fragmentID)
	visitKey := noteRenderVisitKey(relPath, fragmentID)
	memoKey := noteRenderMemoKey(relPath, fragmentID, depth)
	if memoKey == "" {
		return ""
	}
	if b == nil || b.idx == nil {
		return missingNoteRenderSignature(visitKey)
	}
	if signature, ok := b.memo[memoKey]; ok {
		return signature
	}

	note := b.idx.Notes[relPath]
	if note == nil {
		return missingNoteRenderSignature(visitKey)
	}

	scopedNote := note
	if fragmentID != "" {
		scopedNote = internalembed.ScopeNoteToFragment(note, fragmentID)
		if scopedNote == nil {
			return missingNoteRenderSignature(visitKey)
		}
	}
	if stack == nil {
		stack = make(map[string]struct{})
	}
	if _, ok := stack[visitKey]; ok {
		return noteRenderCycleSignature(visitKey)
	}
	stack[visitKey] = struct{}{}
	defer delete(stack, visitKey)

	hasher := newCacheSignatureHasher("note-render")
	cacheHashWriteString(hasher, visitKey)
	cacheHashWriteInt(hasher, depth)
	if fragmentID == "" {
		cacheHashWriteString(hasher, b.noteHashes[relPath])
		cacheHashWriteString(hasher, normalizeCacheTime(note.LastModified))
	} else {
		cacheHashWriteInt(hasher, scopedNote.BodyStartLine)
		cacheHashWriteString(hasher, string(scopedNote.RawContent))
	}
	for _, ref := range scopedNote.OutLinks {
		cacheHashWriteString(hasher, b.linkSignature(scopedNote, ref))
	}
	for _, ref := range scopedNote.Embeds {
		cacheHashWriteString(hasher, b.embedSignature(scopedNote, ref, stack, depth))
	}
	for _, ref := range scopedNote.ImageRefs {
		cacheHashWriteString(hasher, b.imageSignature(scopedNote, ref))
	}

	signature := hex.EncodeToString(hasher.Sum(nil))
	b.memo[memoKey] = signature
	return signature
}

func (b *noteRenderSignatureBuilder) linkSignature(source *model.Note, ref model.LinkRef) string {
	target, fragment := splitLinkTarget(ref.RawTarget, ref.Fragment)
	lookup := markdownwikilink.LookupTarget(b.idx, source, target, fragment)

	hasher := newCacheSignatureHasher("note-link")
	cacheHashWriteString(hasher, strings.TrimSpace(ref.RawTarget))
	cacheHashWriteString(hasher, lookupTargetSignature(lookup))
	return hex.EncodeToString(hasher.Sum(nil))
}

func (b *noteRenderSignatureBuilder) imageSignature(source *model.Note, ref model.ImageRef) string {
	hasher := newCacheSignatureHasher("note-image")
	cacheHashWriteString(hasher, strings.TrimSpace(ref.RawTarget))
	cacheHashWriteString(hasher, b.assetResolutionSignature(source, ref.RawTarget, false))
	return hex.EncodeToString(hasher.Sum(nil))
}

func (b *noteRenderSignatureBuilder) embedSignature(source *model.Note, ref model.EmbedRef, stack map[string]struct{}, depth int) string {
	hasher := newCacheSignatureHasher("note-embed")
	cacheHashWriteString(hasher, strings.TrimSpace(ref.Target))
	cacheHashWriteString(hasher, strings.TrimSpace(ref.Fragment))

	if isImageEmbedRef(ref) {
		cacheHashWriteString(hasher, b.assetResolutionSignature(source, ref.Target, true))
		return hex.EncodeToString(hasher.Sum(nil))
	}

	fragment := strings.TrimSpace(ref.Fragment)
	if strings.HasPrefix(fragment, "^") {
		lookup := markdownwikilink.LookupTarget(b.idx, source, strings.TrimSpace(ref.Target), "")
		cacheHashWriteString(hasher, "block-reference")
		cacheHashWriteString(hasher, lookupTargetSignature(lookup))
		return hex.EncodeToString(hasher.Sum(nil))
	}

	lookup := markdownwikilink.LookupTarget(b.idx, source, strings.TrimSpace(ref.Target), fragment)
	cacheHashWriteString(hasher, lookupTargetSignature(lookup))
	if lookup.Note != nil && !lookup.Unpublished && !lookup.MissingFragment {
		if depth >= internalembed.MaxRenderDepth() {
			cacheHashWriteString(hasher, "max-depth")
		} else {
			cacheHashWriteString(hasher, b.signatureFor(lookup.Note.RelPath, lookup.FragmentID, depth+1, stack))
		}
	}

	return hex.EncodeToString(hasher.Sum(nil))
}

func (b *noteRenderSignatureBuilder) assetResolutionSignature(source *model.Note, rawDestination string, imageEmbed bool) string {
	var idx *model.VaultIndex
	if b != nil && b.idx != nil {
		idx = b.idx
	}

	lookup := resourcepath.LookupIndexedAssetPath(source, idx, rawDestination)
	if imageEmbed {
		lookup = resourcepath.LookupIndexedImageEmbedAssetPath(source, idx, rawDestination)
	}
	hasher := newCacheSignatureHasher("asset-resolution")
	cacheHashWriteString(hasher, strings.TrimSpace(lookup.Path))
	cacheHashWriteInt(hasher, len(lookup.Ambiguous))
	for _, candidate := range lookup.Ambiguous {
		cacheHashWriteString(hasher, candidate)
	}

	return hex.EncodeToString(hasher.Sum(nil))
}

func isImageEmbedRef(ref model.EmbedRef) bool {
	return ref.IsImage || resourcepath.LooksLikeImage(strings.TrimSpace(ref.Target))
}

func splitLinkTarget(rawTarget string, fragment string) (string, string) {
	trimmedRawTarget := strings.TrimSpace(rawTarget)
	trimmedFragment := strings.TrimSpace(fragment)
	if trimmedFragment == "" {
		target, resolvedFragment, found := strings.Cut(trimmedRawTarget, "#")
		if !found {
			return trimmedRawTarget, ""
		}
		return strings.TrimSpace(target), strings.TrimSpace(resolvedFragment)
	}

	target := trimmedRawTarget
	if candidateTarget, candidateFragment, found := strings.Cut(trimmedRawTarget, "#"); found && strings.EqualFold(strings.TrimSpace(candidateFragment), trimmedFragment) {
		target = candidateTarget
	}

	return strings.TrimSpace(target), trimmedFragment
}

func lookupTargetSignature(lookup markdownwikilink.LookupResult) string {
	hasher := newCacheSignatureHasher("lookup-target")
	cacheHashWriteBool(hasher, lookup.Note != nil)
	cacheHashWriteBool(hasher, lookup.CanvasResource)
	cacheHashWriteBool(hasher, lookup.Unpublished)
	cacheHashWriteBool(hasher, lookup.MissingFragment)
	cacheHashWriteString(hasher, strings.TrimSpace(lookup.FragmentID))
	if lookup.Note != nil {
		cacheHashWriteString(hasher, lookup.Note.RelPath)
		cacheHashWriteString(hasher, lookup.Note.Slug)
	}
	for _, candidate := range lookup.Ambiguous {
		cacheHashWriteString(hasher, candidate)
	}

	return hex.EncodeToString(hasher.Sum(nil))
}

func missingNoteRenderSignature(relPath string) string {
	return "missing-note:" + strings.TrimSpace(relPath)
}

func noteRenderCycleSignature(relPath string) string {
	return "cycle:" + strings.TrimSpace(relPath)
}

func normalizeCacheTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func newCacheSignatureHasher(kind string) hash.Hash {
	hasher := sha256.New()
	cacheHashWriteString(hasher, cacheSignatureSaltKey)
	cacheHashWriteString(hasher, kind)
	return hasher
}

func cacheHashWriteString(hasher hash.Hash, value string) {
	if hasher == nil {
		return
	}
	_, _ = hasher.Write([]byte(value))
	_, _ = hasher.Write([]byte{0})
}

func cacheHashWriteInt(hasher hash.Hash, value int) {
	cacheHashWriteString(hasher, strconv.Itoa(value))
}

func cacheHashWriteFloat64(hasher hash.Hash, value float64) {
	cacheHashWriteString(hasher, strconv.FormatUint(math.Float64bits(value), 16))
}

func cacheHashWriteBool(hasher hash.Hash, value bool) {
	if value {
		cacheHashWriteString(hasher, "true")
		return
	}
	cacheHashWriteString(hasher, "false")
}

func diffNoteHashes(previous *CacheManifest, current map[string]string) noteHashSnapshot {
	snapshot := noteHashSnapshot{
		current: current,
		changed: map[string]struct{}{},
		removed: map[string]struct{}{},
	}

	if len(current) == 0 {
		if previous == nil {
			return snapshot
		}
		for relPath := range previous.Notes {
			snapshot.removed[relPath] = struct{}{}
		}
		return snapshot
	}

	for relPath, hashValue := range current {
		previousEntry, ok := cacheManifestEntry(previous, relPath)
		if !ok || previousEntry.ContentHash != hashValue {
			snapshot.changed[relPath] = struct{}{}
		}
	}

	if previous != nil {
		for relPath := range previous.Notes {
			if _, ok := current[relPath]; !ok {
				snapshot.removed[relPath] = struct{}{}
			}
		}
	}

	return snapshot
}

func cacheManifestEntry(manifest *CacheManifest, relPath string) (cacheManifestNote, bool) {
	if manifest == nil || manifest.Notes == nil {
		return cacheManifestNote{}, false
	}

	entry, ok := manifest.Notes[relPath]
	if !ok {
		return cacheManifestNote{}, false
	}
	return entry, true
}

func cacheManifestPageSignature(manifest *CacheManifest, relPath string) string {
	if manifest == nil || manifest.Pages == nil {
		return ""
	}

	return manifest.Pages[relPath]
}

func shouldReuseCachedPage(manifest *CacheManifest, relPath string, signature string, fullDirty bool) bool {
	if fullDirty || strings.TrimSpace(signature) == "" {
		return false
	}

	return cacheManifestPageSignature(manifest, relPath) == signature
}

func sidebarDerivedSignaturesChanged(idx *model.VaultIndex, current map[string]map[string]string, previous *CacheManifest) bool {
	if idx == nil {
		return false
	}

	currentPaths := allPublicNotePathSet(idx)
	for relPath := range currentPaths {
		currentSignatures := current[relPath]
		previousEntry, ok := cacheManifestEntry(previous, relPath)
		if !ok {
			return true
		}
		if derivedSignatureValue(currentSignatures, derivedSignatureKeySidebar) != derivedSignatureValue(previousEntry.DerivedSignatures, derivedSignatureKeySidebar) {
			return true
		}
	}

	if previous == nil {
		return false
	}
	for relPath := range previous.Notes {
		if _, ok := currentPaths[relPath]; !ok {
			return true
		}
	}

	return false
}

func backlinkDerivedSignaturesChanged(idx *model.VaultIndex, current map[string]map[string]string, previous *CacheManifest, contentDirtyPaths map[string]struct{}) (map[string]struct{}, bool) {
	changed := make(map[string]struct{})
	if idx == nil {
		return changed, true
	}

	currentPaths := allPublicNotePathSet(idx)
	for relPath := range currentPaths {
		currentValue := derivedSignatureValue(current[relPath], derivedSignatureKeyBacklinks)
		if currentValue == "" {
			return nil, false
		}

		previousEntry, ok := cacheManifestEntry(previous, relPath)
		if !ok {
			if _, dirty := contentDirtyPaths[relPath]; dirty {
				changed[relPath] = struct{}{}
				continue
			}
			return nil, false
		}

		previousValue := derivedSignatureValue(previousEntry.DerivedSignatures, derivedSignatureKeyBacklinks)
		if previousValue == "" {
			return nil, false
		}
		if currentValue != previousValue {
			changed[relPath] = struct{}{}
		}
	}

	return changed, true
}

func relatedDerivedSignaturesChanged(idx *model.VaultIndex, current map[string]map[string]string, previous *CacheManifest, contentDirtyPaths map[string]struct{}) (map[string]struct{}, bool) {
	changed := make(map[string]struct{})
	if idx == nil {
		return changed, true
	}

	currentPaths := allPublicNotePathSet(idx)
	for relPath := range currentPaths {
		currentValue := derivedSignatureValue(current[relPath], derivedSignatureKeyRelated)
		if currentValue == "" {
			return nil, false
		}

		previousEntry, ok := cacheManifestEntry(previous, relPath)
		if !ok {
			if _, dirty := contentDirtyPaths[relPath]; dirty {
				changed[relPath] = struct{}{}
				continue
			}
			return nil, false
		}

		previousValue := derivedSignatureValue(previousEntry.DerivedSignatures, derivedSignatureKeyRelated)
		if previousValue == "" {
			return nil, false
		}
		if currentValue != previousValue {
			changed[relPath] = struct{}{}
		}
	}

	return changed, true
}

func derivedSignatureValue(signatures map[string]string, key string) string {
	if len(signatures) == 0 {
		return ""
	}

	return strings.TrimSpace(signatures[key])
}

func cacheManifestAssets(list []model.Asset) map[string]*model.Asset {
	if len(list) == 0 {
		return nil
	}

	assets := make(map[string]*model.Asset, len(list))
	for index := range list {
		asset := list[index]
		if strings.TrimSpace(asset.SrcPath) == "" {
			continue
		}
		cloned := asset
		assets[asset.SrcPath] = &cloned
	}
	return assets
}

func cacheManifestAssetList(assets map[string]*model.Asset) []model.Asset {
	if len(assets) == 0 {
		return nil
	}

	paths := make([]string, 0, len(assets))
	for srcPath := range assets {
		paths = append(paths, srcPath)
	}
	sort.Strings(paths)

	list := make([]model.Asset, 0, len(paths))
	for _, srcPath := range paths {
		asset := assets[srcPath]
		if asset == nil {
			continue
		}
		list = append(list, model.Asset{
			SrcPath:  asset.SrcPath,
			DstPath:  asset.DstPath,
			RefCount: asset.RefCount,
		})
	}
	return list
}

func cloneLinkGraph(graph *model.LinkGraph) model.LinkGraph {
	if graph == nil {
		return model.LinkGraph{Forward: map[string][]string{}, Backward: map[string][]string{}}
	}

	return model.LinkGraph{
		Forward:  cloneStringSlices(graph.Forward),
		Backward: cloneStringSlices(graph.Backward),
	}
}

func cloneStringSlices(values map[string][]string) map[string][]string {
	if len(values) == 0 {
		return map[string][]string{}
	}

	cloned := make(map[string][]string, len(values))
	for key, members := range values {
		cloned[key] = append([]string(nil), members...)
	}
	return cloned
}

func cloneLinkRefs(values []model.LinkRef) []model.LinkRef {
	if len(values) == 0 {
		return nil
	}
	return append([]model.LinkRef(nil), values...)
}

func cloneSignatureMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}

	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}

	return cloned
}

func cloneDiagnostics(values []diag.Diagnostic) []diag.Diagnostic {
	if len(values) == 0 {
		return nil
	}
	cloned := append([]diag.Diagnostic(nil), values...)
	sort.Slice(cloned, func(i int, j int) bool {
		left := cloned[i]
		right := cloned[j]

		if left.Location.Path != right.Location.Path {
			return left.Location.Path < right.Location.Path
		}
		if left.Location.Line != right.Location.Line {
			return left.Location.Line < right.Location.Line
		}
		if cacheSeverityOrder(left.Severity) != cacheSeverityOrder(right.Severity) {
			return cacheSeverityOrder(left.Severity) < cacheSeverityOrder(right.Severity)
		}
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		return left.Message < right.Message
	})
	return cloned
}

func cacheSeverityOrder(severity diag.Severity) int {
	switch severity {
	case diag.SeverityError:
		return 0
	case diag.SeverityWarning:
		return 1
	default:
		return 2
	}
}

func buildCacheManifest(buildABISignature string, configSignature string, templateSignature string, graph *model.LinkGraph, noteStates map[string]*noteBuildState, pageSignatures map[string]string, searchIndexSignature string) *CacheManifest {
	manifest := &CacheManifest{
		Version:              cacheManifestVersion,
		BuildABISignature:    strings.TrimSpace(buildABISignature),
		ConfigSignature:      configSignature,
		TemplateSignature:    templateSignature,
		SearchIndexSignature: strings.TrimSpace(searchIndexSignature),
		Graph:                cloneLinkGraph(graph),
		Pages:                cloneSignatureMap(pageSignatures),
		Notes:                make(map[string]cacheManifestNote, len(noteStates)),
	}

	for _, relPath := range sortedNoteBuildStatePaths(noteStates) {
		state := noteStates[relPath]
		if state == nil || state.rendered == nil || state.rendered.rendered == nil {
			continue
		}

		entry := cacheManifestNote{
			ContentHash:       state.contentHash,
			RenderSignature:   state.renderSignature,
			DerivedSignatures: cloneSignatureMap(state.derivedSignatures),
			HTMLContent:       state.rendered.rendered.HTMLContent,
			HasMath:           state.rendered.rendered.HasMath,
			HasMermaid:        state.rendered.rendered.HasMermaid,
			OutLinks:          cloneLinkRefs(state.rendered.outLinks),
			Assets:            cacheManifestAssetList(state.assets),
			RenderDiagnostics: cloneDiagnostics(state.renderDiagnostics),
			PageDiagnostics:   cloneDiagnostics(state.pageDiagnostics),
		}

		manifest.Notes[relPath] = entry
	}

	return manifest
}
