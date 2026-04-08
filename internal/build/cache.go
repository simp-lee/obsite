package build

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	internalasset "github.com/simp-lee/obsite/internal/asset"
	"github.com/simp-lee/obsite/internal/diag"
	markdownwikilink "github.com/simp-lee/obsite/internal/markdown/wikilink"
	"github.com/simp-lee/obsite/internal/model"
	templateassets "github.com/simp-lee/obsite/templates"
)

const (
	cacheManifestDir           = ".obsite-cache"
	cacheManifestRelPath       = cacheManifestDir + "/manifest.json"
	cacheManifestVersion       = 2
	defaultTemplateSigKey      = "default"
	missingTemplateSigKey      = "missing"
	cacheSignatureSaltKey      = "phase-21-step-50"
	derivedSignatureKeySidebar = "sidebar"
	derivedSignatureKeyRelated = "related"
)

var cacheTemplateFileNames = []string{
	"base.html",
	"note.html",
	"index.html",
	"tag.html",
	"folder.html",
	"timeline.html",
	"404.html",
	"style.css",
}

var readDefaultTemplateAssetForSignature = func(name string) ([]byte, error) {
	return templateassets.FS.ReadFile(name)
}

type noteRenderSignatureBuilder struct {
	idx        *model.VaultIndex
	noteHashes map[string]string
	memo       map[string]string
}

// Options controls build-time behavior that should remain stable across the CLI and serve paths.
type Options struct {
	Force bool
}

// CacheManifest stores the incremental-build state that can be safely reused on the next run.
type CacheManifest struct {
	Version           int                          `json:"version"`
	ConfigSignature   string                       `json:"configSignature"`
	TemplateSignature string                       `json:"templateSignature"`
	Graph             model.LinkGraph              `json:"graph"`
	Pages             map[string]string            `json:"pages,omitempty"`
	Notes             map[string]cacheManifestNote `json:"notes"`
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

func buildTemplateSignature(templateDir string) (string, error) {
	trimmedDir := strings.TrimSpace(templateDir)
	if trimmedDir == "" {
		return buildEmbeddedTemplateSignature()
	}

	cleanDir := filepath.Clean(trimmedDir)
	hasher := sha256.New()
	_, _ = hasher.Write([]byte(cacheSignatureSaltKey))
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write([]byte(cleanDir))

	if info, err := os.Stat(cleanDir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			_, _ = hasher.Write([]byte{0})
			_, _ = hasher.Write([]byte(missingTemplateSigKey))
			return hex.EncodeToString(hasher.Sum(nil)), nil
		}
		return "", fmt.Errorf("stat template dir %q: %w", cleanDir, err)
	} else if !info.IsDir() {
		_, _ = hasher.Write([]byte{0})
		_, _ = hasher.Write([]byte("not-a-directory"))
		return hex.EncodeToString(hasher.Sum(nil)), nil
	}

	// The effective template set always starts from embedded defaults; local
	// overrides only layer on top of that base.
	embeddedSignature, err := buildEmbeddedTemplateSignature()
	if err != nil {
		return "", err
	}
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write([]byte(defaultTemplateSigKey))
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write([]byte(embeddedSignature))

	for _, name := range cacheTemplateFileNames {
		_, _ = hasher.Write([]byte{0})
		_, _ = hasher.Write([]byte(name))

		data, err := os.ReadFile(filepath.Join(cleanDir, name))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				_, _ = hasher.Write([]byte{0, 0})
				continue
			}
			return "", fmt.Errorf("read template override %q: %w", filepath.Join(cleanDir, name), err)
		}

		_, _ = hasher.Write([]byte{0, 1})
		_, _ = hasher.Write(data)
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func buildEmbeddedTemplateSignature() (string, error) {
	hasher := newCacheSignatureHasher("embedded-templates")
	cacheHashWriteString(hasher, defaultTemplateSigKey)

	for _, name := range cacheTemplateFileNames {
		cacheHashWriteString(hasher, name)

		data, err := readDefaultTemplateAssetForSignature(name)
		if err != nil {
			return "", fmt.Errorf("read embedded template asset %q: %w", name, err)
		}
		cacheHashWriteString(hasher, string(data))
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
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
		memo:       make(map[string]string, len(idx.Notes)),
	}
	signatures := make(map[string]string, len(idx.Notes))
	for _, relPath := range sortedNoteSignaturePaths(idx.Notes) {
		signatures[relPath] = builder.signatureFor(relPath, nil)
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

func (b *noteRenderSignatureBuilder) signatureFor(relPath string, stack map[string]struct{}) string {
	relPath = strings.TrimSpace(relPath)
	if relPath == "" {
		return ""
	}
	if b == nil || b.idx == nil {
		return missingNoteRenderSignature(relPath)
	}
	if signature, ok := b.memo[relPath]; ok {
		return signature
	}

	note := b.idx.Notes[relPath]
	if note == nil {
		return missingNoteRenderSignature(relPath)
	}
	if stack == nil {
		stack = make(map[string]struct{})
	}
	if _, ok := stack[relPath]; ok {
		return noteRenderCycleSignature(relPath)
	}
	stack[relPath] = struct{}{}
	defer delete(stack, relPath)

	hasher := newCacheSignatureHasher("note-render")
	cacheHashWriteString(hasher, relPath)
	cacheHashWriteString(hasher, b.noteHashes[relPath])
	cacheHashWriteString(hasher, normalizeCacheTime(note.LastModified))
	for _, ref := range note.OutLinks {
		cacheHashWriteString(hasher, b.linkSignature(note, ref))
	}
	for _, ref := range note.Embeds {
		cacheHashWriteString(hasher, b.embedSignature(note, ref, stack))
	}

	signature := hex.EncodeToString(hasher.Sum(nil))
	b.memo[relPath] = signature
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

func (b *noteRenderSignatureBuilder) embedSignature(source *model.Note, ref model.EmbedRef, stack map[string]struct{}) string {
	hasher := newCacheSignatureHasher("note-embed")
	cacheHashWriteString(hasher, strings.TrimSpace(ref.Target))
	cacheHashWriteString(hasher, strings.TrimSpace(ref.Fragment))

	if isImageEmbedRef(ref) {
		cacheHashWriteString(hasher, b.assetResolutionSignature(source, ref.Target))
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
		cacheHashWriteString(hasher, b.signatureFor(lookup.Note.RelPath, stack))
	}

	return hex.EncodeToString(hasher.Sum(nil))
}

func (b *noteRenderSignatureBuilder) assetResolutionSignature(source *model.Note, rawDestination string) string {
	attachmentFolderPath := ""
	if b != nil && b.idx != nil {
		attachmentFolderPath = b.idx.AttachmentFolderPath
	}

	resolved := internalasset.ResolvePath(source, attachmentFolderPath, rawDestination, func(candidate string) bool {
		return b != nil && b.idx != nil && b.idx.Assets[candidate] != nil
	})
	if strings.TrimSpace(resolved) == "" {
		return "missing-asset"
	}

	return resolved
}

func isImageEmbedRef(ref model.EmbedRef) bool {
	return ref.IsImage || internalasset.HasImageExtension(strings.TrimSpace(ref.Target))
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

func buildCacheManifest(configSignature string, templateSignature string, graph *model.LinkGraph, noteStates map[string]*noteBuildState, pageSignatures map[string]string) *CacheManifest {
	manifest := &CacheManifest{
		Version:           cacheManifestVersion,
		ConfigSignature:   configSignature,
		TemplateSignature: templateSignature,
		Graph:             cloneLinkGraph(graph),
		Pages:             cloneSignatureMap(pageSignatures),
		Notes:             make(map[string]cacheManifestNote, len(noteStates)),
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
