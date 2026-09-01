package build

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"math"
	"os"
	"path/filepath"
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
	cacheManifestVersion         = 7
	defaultTemplateSigKey        = "default"
	cacheSignatureSaltKey        = "obsite-cache-signature-v1"
	buildCacheABIKey             = "obsite-build-cache-abi-v1"
	derivedSignatureKeyBacklinks = "backlinks"
	derivedSignatureKeyRelated   = "related"
)

var listTemplateAssetsForSignature = internalrender.EmbeddedTemplateAssetNames

var readDefaultTemplateAssetForSignature = internalrender.ReadEmbeddedTemplateAsset

var readBuildABISignature = sync.OnceValues(computeBuildABISignature)

func templateAssetNamesForCacheSignature() []string {
	names := listTemplateAssetsForSignature()
	filtered := make([]string, 0, len(names))
	for _, name := range names {
		if strings.EqualFold(filepath.Ext(strings.TrimSpace(name)), ".html") {
			filtered = append(filtered, name)
		}
	}
	return filtered
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
	Version                int                          `json:"version"`
	BuildABISignature      string                       `json:"buildABISignature"`
	NotePageSignature      string                       `json:"notePageSignature"`
	TemplateSignature      string                       `json:"templateSignature"`
	PageInputSignature     string                       `json:"pageInputSignature"`
	DefaultImagePath       string                       `json:"defaultImagePath,omitempty"`
	ManagedAssetSignatures map[string]string            `json:"managedAssetSignatures,omitempty"`
	Pages                  map[string]string            `json:"pages,omitempty"`
	Notes                  map[string]cacheManifestNote `json:"notes"`
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
	if strings.TrimSpace(manifest.BuildABISignature) == "" || strings.TrimSpace(manifest.NotePageSignature) == "" || strings.TrimSpace(manifest.TemplateSignature) == "" || strings.TrimSpace(manifest.PageInputSignature) == "" || manifest.ManagedAssetSignatures == nil || manifest.Notes == nil || manifest.Pages == nil {
		return nil, nil
	}
	var raw struct {
		Notes map[string]json.RawMessage `json:"notes"`
	}
	if err := json.Unmarshal(data, &raw); err != nil || raw.Notes == nil {
		return nil, nil
	}
	for relPath, noteData := range raw.Notes {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(noteData, &fields); err != nil {
			return nil, nil
		}
		for _, required := range []string{"contentHash", "renderSignature", "htmlContent"} {
			if _, ok := fields[required]; !ok {
				return nil, nil
			}
		}
		entry := manifest.Notes[relPath]
		if strings.TrimSpace(entry.ContentHash) == "" || strings.TrimSpace(entry.RenderSignature) == "" {
			return nil, nil
		}
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
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal cache manifest: %w", err)
	}
	data = append(data, '\n')

	return writeOutputFile(outputRoot, cacheManifestRelPath, data)
}

func buildNotePageConfigSignature(cfg model.SiteConfig) string {
	hasher := newCacheSignatureHasher("note-page-config")
	cacheHashWriteString(hasher, cfg.Title)
	cacheHashWriteString(hasher, cfg.BaseURL)
	cacheHashWriteString(hasher, cfg.Author)
	cacheHashWriteString(hasher, cfg.Description)
	cacheHashWriteString(hasher, cfg.Language)
	cacheHashWriteBool(hasher, cfg.Sidebar.Enabled)
	cacheHashWriteBool(hasher, cfg.Popover.Enabled)
	cacheHashWriteBool(hasher, cfg.RSS.Enabled)
	cacheHashWriteBool(hasher, cfg.Related.Enabled)
	return hex.EncodeToString(hasher.Sum(nil))
}

func buildPageInputSignature(cfg model.SiteConfig) string {
	hasher := newCacheSignatureHasher("page-inputs")
	cacheHashWriteBool(hasher, strings.TrimSpace(cfg.ThemeCSS) != "")
	cacheHashWriteBool(hasher, strings.TrimSpace(cfg.CustomCSS) != "")
	cacheHashWriteString(hasher, cfg.ThemeSlots)
	cacheHashWriteString(hasher, strings.TrimSpace(cfg.RuntimeJSURL))
	return hex.EncodeToString(hasher.Sum(nil))
}

func buildManagedAssetSignatures(outputRoot string, cfg model.SiteConfig, theme themeInputs) (map[string]string, error) {
	pathSet := map[string]struct{}{"style.css": {}}
	for _, relPath := range internalrender.RuntimeAssetOutputPaths() {
		pathSet[relPath] = struct{}{}
	}
	if cfg.Sidebar.Enabled {
		pathSet[sidebarJSONOutputPath] = struct{}{}
	}
	if theme.stylesheet != "" {
		pathSet[themeCSSOutputPath] = struct{}{}
	}
	for _, asset := range theme.assets {
		pathSet[asset.outputPath] = struct{}{}
	}
	if strings.TrimSpace(cfg.CustomCSS) != "" {
		pathSet[customCSSOutputPath] = struct{}{}
	}

	paths := make([]string, 0, len(pathSet))
	for relPath := range pathSet {
		paths = append(paths, relPath)
	}
	sort.Strings(paths)
	signatures := make(map[string]string, len(paths))
	for _, relPath := range paths {
		data, err := os.ReadFile(filepath.Join(outputRoot, filepath.FromSlash(relPath)))
		if err != nil {
			return nil, fmt.Errorf("read managed owner output %q: %w", relPath, err)
		}
		hash := sha256.Sum256(data)
		signatures[relPath] = hex.EncodeToString(hash[:])
	}
	return signatures, nil
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
	info, ok := debug.ReadBuildInfo()
	if !ok {
		info = nil
	}
	return buildABISignature(info), nil
}

func buildABISignature(info *debug.BuildInfo) string {
	hasher := newCacheSignatureHasher("build-abi")
	cacheHashWriteString(hasher, buildCacheABIKey)
	if info == nil {
		return hex.EncodeToString(hasher.Sum(nil))
	}

	cacheHashWriteString(hasher, info.Main.Path)
	cacheHashWriteString(hasher, info.Main.Version)
	cacheHashWriteString(hasher, info.Main.Sum)
	revision := ""
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" {
			revision = setting.Value
			break
		}
	}
	cacheHashWriteString(hasher, "vcs.revision")
	cacheHashWriteString(hasher, revision)
	return hex.EncodeToString(hasher.Sum(nil))
}

func cachePageSiteConfig(cfg model.SiteConfig) model.SiteConfig {
	cfg.ThemeDir = ""
	cfg.ThemeCSS = ""
	cfg.CustomCSS = ""
	return cfg
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
		_, data, _, err := internalfsutil.ReadContainedRegularFile(vaultRoot, filepath.FromSlash(relPath))
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

func buildCacheManifest(buildABISignature string, notePageSignature string, templateSignature string, pageInputSignature string, defaultImagePath string, managedAssetSignatures map[string]string, noteStates map[string]*noteBuildState, pageSignatures map[string]string) *CacheManifest {
	manifest := &CacheManifest{
		Version:                cacheManifestVersion,
		BuildABISignature:      strings.TrimSpace(buildABISignature),
		NotePageSignature:      notePageSignature,
		TemplateSignature:      templateSignature,
		PageInputSignature:     pageInputSignature,
		DefaultImagePath:       strings.TrimSpace(defaultImagePath),
		ManagedAssetSignatures: cloneSignatureMap(managedAssetSignatures),
		Pages:                  cloneSignatureMap(pageSignatures),
		Notes:                  make(map[string]cacheManifestNote, len(noteStates)),
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
