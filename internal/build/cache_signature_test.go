package build

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
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
