package build

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/simp-lee/obsite/internal/diag"
	"github.com/simp-lee/obsite/internal/model"
)

func TestTemplateAssetNamesForCacheSignatureExcludesStyleCSS(t *testing.T) {
	originalListTemplateAssetsForSignature := listTemplateAssetsForSignature
	t.Cleanup(func() {
		listTemplateAssetsForSignature = originalListTemplateAssetsForSignature
	})

	listTemplateAssetsForSignature = func() []string {
		return []string{"base.html", "style.css", "note.html"}
	}

	got := templateAssetNamesForCacheSignature()
	want := []string{"base.html", "note.html"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("templateAssetNamesForCacheSignature() = %#v, want %#v", got, want)
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
		manifest := buildCacheManifest("abi", "config", "template", nil, map[string]*noteBuildState{noteRelPath: state}, nil)
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

func TestBuildABISourceSignatureIgnoresStylesheetAssetsAndTracksGoSources(t *testing.T) {
	t.Parallel()

	repoRoot := t.TempDir()
	write := func(relPath string, contents string) {
		t.Helper()

		absPath := filepath.Join(repoRoot, filepath.FromSlash(relPath))
		if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
			t.Fatalf("os.MkdirAll(%q) error = %v", filepath.Dir(absPath), err)
		}
		if err := os.WriteFile(absPath, []byte(contents), 0o644); err != nil {
			t.Fatalf("os.WriteFile(%q) error = %v", absPath, err)
		}
	}

	write("go.mod", "module example.com/obsite\n\ngo 1.24.0\n")
	write("internal/render/render.go", "package render\n\nconst buildABITest = 1\n")
	write("templates/style.css", "body { color: black; }\n")

	baseline, err := buildABISourceSignatureFromRoot(repoRoot)
	if err != nil {
		t.Fatalf("buildABISourceSignatureFromRoot() baseline error = %v", err)
	}
	if baseline == "" {
		t.Fatal("buildABISourceSignatureFromRoot() = empty baseline signature")
	}

	write("templates/style.css", "body { color: white; }\n")
	styleOnly, err := buildABISourceSignatureFromRoot(repoRoot)
	if err != nil {
		t.Fatalf("buildABISourceSignatureFromRoot() style-only error = %v", err)
	}
	if styleOnly != baseline {
		t.Fatalf("buildABISourceSignatureFromRoot() style-only signature = %q, want %q", styleOnly, baseline)
	}

	write("internal/render/render.go", "package render\n\nconst buildABITest = 2\n")
	codeChanged, err := buildABISourceSignatureFromRoot(repoRoot)
	if err != nil {
		t.Fatalf("buildABISourceSignatureFromRoot() code-change error = %v", err)
	}
	if codeChanged == baseline {
		t.Fatal("buildABISourceSignatureFromRoot() did not change after Go source changed")
	}
}
