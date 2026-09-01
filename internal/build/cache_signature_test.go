package build

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/simp-lee/obsite/internal/diag"
	"github.com/simp-lee/obsite/internal/model"
	internalrender "github.com/simp-lee/obsite/internal/render"
)

func TestTemplateAssetNamesForCacheSignatureIncludesOnlyHTMLTemplates(t *testing.T) {
	originalListTemplateAssetsForSignature := listTemplateAssetsForSignature
	t.Cleanup(func() {
		listTemplateAssetsForSignature = originalListTemplateAssetsForSignature
	})

	listTemplateAssetsForSignature = func() []string {
		return []string{"base.html", "style.css", "runtime.js", "note.html"}
	}

	got := templateAssetNamesForCacheSignature()
	want := []string{"base.html", "note.html"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("templateAssetNamesForCacheSignature() = %#v, want %#v", got, want)
	}
}

func TestBuildConfigAndPageInputSignaturesSeparateOutputOwners(t *testing.T) {
	t.Parallel()

	base := testBuildSiteConfig()
	baseNoteSignature := buildNotePageConfigSignature(base)
	ownerVariant := base
	ownerVariant.DefaultImg = "images/hero.png"
	ownerVariant.ThemeDir = "/vault/.obsite/theme"
	ownerVariant.ThemeCSS = "/vault/.obsite/theme/theme.css"
	ownerVariant.ThemeSlots = `{{define "obsite-head-end"}}slot{{end}}`
	ownerVariant.CustomCSS = "/vault/custom.css"
	ownerVariant.RuntimeJSURL = "assets/obsite/runtime.changed.js"
	ownerVariant.Pagination.PageSize++
	ownerVariant.Timeline.Enabled = !ownerVariant.Timeline.Enabled
	ownerVariant.Related.Count++
	if ownerSignature := buildNotePageConfigSignature(ownerVariant); ownerSignature != baseNoteSignature {
		t.Fatalf("non-note owners changed note page signature: %q != %q", ownerSignature, baseNoteSignature)
	}
	for _, mutate := range []func(*model.SiteConfig){
		func(cfg *model.SiteConfig) { cfg.Title = "Changed" },
		func(cfg *model.SiteConfig) { cfg.Popover.Enabled = !cfg.Popover.Enabled },
		func(cfg *model.SiteConfig) { cfg.RSS.Enabled = !cfg.RSS.Enabled },
		func(cfg *model.SiteConfig) { cfg.Related.Enabled = !cfg.Related.Enabled },
	} {
		candidate := base
		mutate(&candidate)
		if buildNotePageConfigSignature(candidate) == baseNoteSignature {
			t.Fatal("note page input did not change note page signature")
		}
	}

	baselinePage := buildPageInputSignature(base)
	for _, mutate := range []func(*model.SiteConfig){
		func(cfg *model.SiteConfig) { cfg.ThemeCSS = "/vault/.obsite/theme/theme.css" },
		func(cfg *model.SiteConfig) { cfg.CustomCSS = "/vault/custom.css" },
		func(cfg *model.SiteConfig) { cfg.ThemeSlots = `{{define "obsite-head-end"}}slot{{end}}` },
		func(cfg *model.SiteConfig) { cfg.RuntimeJSURL = "assets/obsite/runtime.changed.js" },
	} {
		candidate := base
		mutate(&candidate)
		if buildPageInputSignature(candidate) == baselinePage {
			t.Fatal("page-owned input did not change page input signature")
		}
	}
}

func TestBuildEmbeddedTemplateSignatureTracksRenderTemplateInventory(t *testing.T) {
	baseline, err := buildEmbeddedTemplateSignature()
	if err != nil {
		t.Fatalf("buildEmbeddedTemplateSignature() baseline error = %v", err)
	}

	originalListTemplateAssetsForSignature := listTemplateAssetsForSignature
	originalReadDefaultTemplateAssetForSignature := readDefaultTemplateAssetForSignature
	t.Cleanup(func() {
		listTemplateAssetsForSignature = originalListTemplateAssetsForSignature
		readDefaultTemplateAssetForSignature = originalReadDefaultTemplateAssetForSignature
	})

	listTemplateAssetsForSignature = func() []string {
		names := append([]string(nil), originalListTemplateAssetsForSignature()...)
		return append(names, "future.html")
	}
	readDefaultTemplateAssetForSignature = func(name string) ([]byte, error) {
		if name == "future.html" {
			return []byte(`{{define "content-future"}}future{{end}}`), nil
		}
		return originalReadDefaultTemplateAssetForSignature(name)
	}

	changed, err := buildEmbeddedTemplateSignature()
	if err != nil {
		t.Fatalf("buildEmbeddedTemplateSignature() changed error = %v", err)
	}
	if changed == baseline {
		t.Fatal("buildEmbeddedTemplateSignature() did not change after render template inventory changed")
	}
}

func TestBuildEmbeddedTemplateSignatureIgnoresStyleCSS(t *testing.T) {
	baseline, err := buildEmbeddedTemplateSignature()
	if err != nil {
		t.Fatalf("buildEmbeddedTemplateSignature() baseline error = %v", err)
	}

	originalReadDefaultTemplateAssetForSignature := readDefaultTemplateAssetForSignature
	t.Cleanup(func() {
		readDefaultTemplateAssetForSignature = originalReadDefaultTemplateAssetForSignature
	})

	readDefaultTemplateAssetForSignature = func(name string) ([]byte, error) {
		if name == "style.css" {
			return []byte("body { color: tomato; }\n"), nil
		}
		return originalReadDefaultTemplateAssetForSignature(name)
	}

	changed, err := buildEmbeddedTemplateSignature()
	if err != nil {
		t.Fatalf("buildEmbeddedTemplateSignature() changed error = %v", err)
	}
	if changed != baseline {
		t.Fatalf("buildEmbeddedTemplateSignature() = %q, want unchanged baseline %q when only embedded style.css changes", changed, baseline)
	}
}

func TestCacheManifestTracksConcreteManagedAssetOwners(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	writeBuildTestFile(t, vaultPath, "notes/alpha.md", "# Alpha\n")
	writeBuildTestFile(t, vaultPath, "custom.css", "body { color: tomato; }\n")
	writeBuildTestFile(t, vaultPath, ".obsite/theme/theme.css", ":root { --obsite-accent: purple; }\n")
	writeBuildTestFile(t, vaultPath, ".obsite/theme/assets/fixed.txt", "fixed\n")
	cfg := testBuildSiteConfig()
	cfg.Sidebar.Enabled = true
	if _, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{}); err != nil {
		t.Fatal(err)
	}

	var manifest CacheManifest
	if err := json.Unmarshal(readBuildOutputFile(t, outputPath, cacheManifestRelPath), &manifest); err != nil {
		t.Fatal(err)
	}
	expected := []string{"style.css", sidebarJSONOutputPath, themeCSSOutputPath, "assets/theme/fixed.txt", customCSSOutputPath}
	expected = append(expected, internalrender.RuntimeAssetOutputPaths()...)
	for _, relPath := range expected {
		data := readBuildOutputFile(t, outputPath, relPath)
		want := fmt.Sprintf("%x", sha256.Sum256(data))
		if got := manifest.ManagedAssetSignatures[relPath]; got != want {
			t.Fatalf("managed signature %q = %q, want %q", relPath, got, want)
		}
	}
	if len(manifest.ManagedAssetSignatures) != len(expected) {
		t.Fatalf("managed signatures = %#v, want exactly %d concrete outputs", manifest.ManagedAssetSignatures, len(expected))
	}
}

func TestNoteRenderSignatureDistinguishesUnresolvedAndAmbiguousImageEmbedAssetStates(t *testing.T) {
	note := &model.Note{
		RelPath:    "notes/gallery.md",
		RawContent: []byte("![[../images/CAFÉ Chart.png]]\n"),
		Embeds: []model.EmbedRef{{
			Target:  "../images/CAFÉ Chart.png",
			IsImage: true,
			Line:    1,
		}},
	}
	noteHashes := map[string]string{note.RelPath: "stable-note-hash"}

	signatureFor := func(assets map[string]*model.Asset) string {
		idx := &model.VaultIndex{
			Notes: map[string]*model.Note{
				note.RelPath: note,
			},
			Assets: assets,
		}

		return buildNoteRenderSignatures(idx, noteHashes)[note.RelPath]
	}

	unresolved := signatureFor(nil)
	ambiguous := signatureFor(map[string]*model.Asset{
		"images/Cafe\u0301 Chart.png": {SrcPath: "images/Cafe\u0301 Chart.png"},
		"images/Café Chart.png":       {SrcPath: "images/Café Chart.png"},
	})

	if unresolved == "" {
		t.Fatal("unresolved render signature = empty, want non-empty signature")
	}
	if ambiguous == "" {
		t.Fatal("ambiguous render signature = empty, want non-empty signature")
	}
	if ambiguous == unresolved {
		t.Fatal("render signature did not change when image embed lookup moved from unresolved to ambiguous")
	}
}

func TestNoteRenderSignatureDistinguishesUnresolvedAndResolvedSlashPathImageEmbedAssetStates(t *testing.T) {
	note := &model.Note{
		RelPath:    "notes/deep/gallery.md",
		RawContent: []byte("![[assets/diagram.png|600]]\n"),
		Embeds: []model.EmbedRef{{
			Target:  "assets/diagram.png",
			IsImage: true,
			Width:   600,
			Line:    1,
		}},
	}
	noteHashes := map[string]string{note.RelPath: "stable-note-hash"}

	signatureFor := func(assets map[string]*model.Asset) string {
		idx := &model.VaultIndex{
			Notes: map[string]*model.Note{
				note.RelPath: note,
			},
			Assets: assets,
		}

		return buildNoteRenderSignatures(idx, noteHashes)[note.RelPath]
	}

	unresolved := signatureFor(nil)
	resolved := signatureFor(map[string]*model.Asset{
		"assets/diagram.png": {SrcPath: "assets/diagram.png"},
	})

	if unresolved == "" {
		t.Fatal("unresolved render signature = empty, want non-empty signature")
	}
	if resolved == "" {
		t.Fatal("resolved render signature = empty, want non-empty signature")
	}
	if resolved == unresolved {
		t.Fatal("render signature did not change when slash-path image embed lookup moved from unresolved to resolved")
	}
}

func TestBuildRefreshesImageEmbedDiagnosticsWhenAssetInventoryChangesWithoutNoteEdits(t *testing.T) {
	noteRelPath := "notes/gallery.md"
	note := &model.Note{
		RelPath:    noteRelPath,
		RawContent: []byte("![[../images/CAFÉ Chart.png]]\n"),
		Embeds: []model.EmbedRef{{
			Target:  "../images/CAFÉ Chart.png",
			IsImage: true,
			Line:    1,
		}},
	}
	noteHashes := map[string]string{noteRelPath: "stable-note-hash"}
	noteDerivedSignatures := map[string]map[string]string{noteRelPath: {}}

	newIndex := func(assets map[string]*model.Asset) *model.VaultIndex {
		idx := &model.VaultIndex{
			Notes:       map[string]*model.Note{noteRelPath: note},
			NoteBySlug:  map[string]*model.Note{},
			NoteByName:  map[string][]*model.Note{},
			AliasByName: map[string][]*model.Note{},
		}
		idx.SetAssets(assets)
		return idx
	}

	runState := func(t *testing.T, idx *model.VaultIndex, previous *CacheManifest) (*noteBuildState, cacheManifestNote, *CacheManifest) {
		t.Helper()

		renderSignatures := buildNoteRenderSignatures(idx, noteHashes)
		states, err := buildNoteStates(idx, nil, 1, previous, noteHashes, renderSignatures, noteDerivedSignatures, false)
		if err != nil {
			t.Fatalf("buildNoteStates() error = %v", err)
		}
		if len(states) != 1 || states[0] == nil {
			t.Fatalf("buildNoteStates() = %#v, want single note state", states)
		}

		state := states[0]
		manifest := buildCacheManifest("abi", "config", "template", "", "", nil, map[string]*noteBuildState{noteRelPath: state}, nil)
		entry, ok := manifest.Notes[noteRelPath]
		if !ok {
			t.Fatalf("manifest missing %q entry", noteRelPath)
		}

		return state, entry, manifest
	}

	firstState, firstEntry, firstManifest := runState(t, newIndex(nil), nil)
	if firstState.fromCache {
		t.Fatal("first state.fromCache = true, want fresh render for unresolved baseline")
	}
	assertSingleUnresolvedAssetDiagnosticContains(t, firstState.renderDiagnostics, "could not be resolved to a vault asset")
	assertCachedUnresolvedAssetDiagnosticContains(t, firstEntry, "could not be resolved to a vault asset")

	ambiguousAssets := map[string]*model.Asset{
		"images/Cafe\u0301 Chart.png": {SrcPath: "images/Cafe\u0301 Chart.png"},
		"images/Café Chart.png":       {SrcPath: "images/Café Chart.png"},
	}
	secondState, secondEntry, secondManifest := runState(t, newIndex(ambiguousAssets), firstManifest)
	if secondState.fromCache {
		t.Fatal("second state.fromCache = true, want cache miss after asset inventory introduces canonical ambiguity")
	}
	if secondEntry.ContentHash != firstEntry.ContentHash {
		t.Fatalf("second manifest content hash = %q, want unchanged baseline %q when note contents did not change", secondEntry.ContentHash, firstEntry.ContentHash)
	}
	if secondEntry.RenderSignature == firstEntry.RenderSignature {
		t.Fatal("second render signature did not change when asset inventory introduced canonical ambiguity")
	}
	assertSingleUnresolvedAssetDiagnosticContains(t, secondState.renderDiagnostics, "matched multiple publishable vault assets")
	assertCachedUnresolvedAssetDiagnosticContains(t, secondEntry, "matched multiple publishable vault assets")

	thirdState, thirdEntry, _ := runState(t, newIndex(nil), secondManifest)
	if thirdState.fromCache {
		t.Fatal("third state.fromCache = true, want cache miss after ambiguity disappears")
	}
	if thirdEntry.ContentHash != firstEntry.ContentHash {
		t.Fatalf("third manifest content hash = %q, want unchanged baseline %q when note contents did not change", thirdEntry.ContentHash, firstEntry.ContentHash)
	}
	if thirdEntry.RenderSignature == secondEntry.RenderSignature {
		t.Fatal("third render signature did not change when canonical ambiguity disappeared")
	}
	if thirdEntry.RenderSignature != firstEntry.RenderSignature {
		t.Fatalf("third render signature = %q, want unresolved baseline signature %q after assets return to the original missing state", thirdEntry.RenderSignature, firstEntry.RenderSignature)
	}
	assertSingleUnresolvedAssetDiagnosticContains(t, thirdState.renderDiagnostics, "could not be resolved to a vault asset")
	assertCachedUnresolvedAssetDiagnosticContains(t, thirdEntry, "could not be resolved to a vault asset")
}

func assertSingleUnresolvedAssetDiagnosticContains(t *testing.T, diagnostics []diag.Diagnostic, want string) {
	t.Helper()

	if len(diagnostics) != 1 {
		t.Fatalf("len(diagnostics) = %d, want %d; diagnostics = %#v", len(diagnostics), 1, diagnostics)
	}
	if diagnostics[0].Kind != diag.KindUnresolvedAsset {
		t.Fatalf("diagnostics[0].Kind = %q, want %q", diagnostics[0].Kind, diag.KindUnresolvedAsset)
	}
	if !strings.Contains(diagnostics[0].Message, want) {
		t.Fatalf("diagnostics[0].Message = %q, want substring %q", diagnostics[0].Message, want)
	}
}

func assertCachedUnresolvedAssetDiagnosticContains(t *testing.T, entry cacheManifestNote, want string) {
	t.Helper()

	if len(entry.RenderDiagnostics) != 1 {
		t.Fatalf("len(entry.RenderDiagnostics) = %d, want %d; diagnostics = %#v", len(entry.RenderDiagnostics), 1, entry.RenderDiagnostics)
	}
	if entry.RenderDiagnostics[0].Kind != diag.KindUnresolvedAsset {
		t.Fatalf("entry.RenderDiagnostics[0].Kind = %q, want %q", entry.RenderDiagnostics[0].Kind, diag.KindUnresolvedAsset)
	}
	if !strings.Contains(entry.RenderDiagnostics[0].Message, want) {
		t.Fatalf("entry.RenderDiagnostics[0].Message = %q, want substring %q", entry.RenderDiagnostics[0].Message, want)
	}
}

func TestBuildABISignatureUsesTargetNeutralProductIdentity(t *testing.T) {
	t.Parallel()

	baselineInfo := &debug.BuildInfo{
		Main: debug.Module{Path: "github.com/simp-lee/obsite", Version: "v1.2.3", Sum: "h1:product"},
		Settings: []debug.BuildSetting{
			{Key: "GOOS", Value: "linux"},
			{Key: "GOARCH", Value: "amd64"},
			{Key: "vcs.revision", Value: "0123456789abcdef"},
			{Key: "vcs.modified", Value: "false"},
		},
	}
	baseline := buildABISignature(baselineInfo)
	if baseline == "" {
		t.Fatal("buildABISignature() = empty")
	}

	targetVariant := &debug.BuildInfo{
		GoVersion: "go1.27.0",
		Main:      baselineInfo.Main,
		Settings: []debug.BuildSetting{
			{Key: "GOARCH", Value: "arm64"},
			{Key: "GOOS", Value: "windows"},
			{Key: "CGO_ENABLED", Value: "0"},
			{Key: "vcs.modified", Value: "true"},
			{Key: "vcs.revision", Value: "0123456789abcdef"},
		},
	}
	if got := buildABISignature(targetVariant); got != baseline {
		t.Fatalf("target-variant build ABI signature = %q, want target-neutral %q", got, baseline)
	}

	versionVariant := *baselineInfo
	versionVariant.Main.Version = "v1.2.4"
	if got := buildABISignature(&versionVariant); got == baseline {
		t.Fatal("build ABI signature did not change with product version")
	}

	revisionVariant := *baselineInfo
	revisionVariant.Settings = append([]debug.BuildSetting(nil), baselineInfo.Settings...)
	revisionVariant.Settings[2].Value = "fedcba9876543210"
	if got := buildABISignature(&revisionVariant); got == baseline {
		t.Fatal("build ABI signature did not change with product revision")
	}
}
