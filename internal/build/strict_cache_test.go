package build

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStrictCacheManifestSeparatesInputsFromOutputs(t *testing.T) {
	vault := t.TempDir()
	writeStrictFile(t, vault, "obsite.yaml", "title: Cache\nbaseURL: https://example.test/\nnavigation: []\n")
	writeStrictFile(t, vault, "_index.md", "---\ntitle: Home\npublish: true\n---\nHome\n")
	writeStrictFile(t, vault, "custom.css", "body { color: red; }\n")
	output := t.TempDir()
	if _, err := BuildWithOptions(vault, output, Options{Strict: true}); err != nil {
		t.Fatal(err)
	}
	first := loadStrictCacheManifest(output)
	if first == nil || len(first.Dependencies) == 0 || len(first.Outputs) == 0 {
		t.Fatal("cache manifest does not contain separate dependency and output records")
	}
	firstDependency, firstOutput := cacheDependencyByOwner(first, "custom CSS"), cacheOutputByOwner(first, "custom CSS")
	if firstDependency.InputSignature == "" || firstOutput.OutputHash == "" {
		t.Fatal("cache records have empty signatures")
	}

	writeStrictFile(t, vault, "custom.css", "body { color: blue; }\n")
	if _, err := BuildWithOptions(vault, output, Options{Strict: true}); err != nil {
		t.Fatal(err)
	}
	second := loadStrictCacheManifest(output)
	secondDependency, secondOutput := cacheDependencyByOwner(second, "custom CSS"), cacheOutputByOwner(second, "custom CSS")
	if firstDependency.InputSignature == secondDependency.InputSignature {
		t.Fatal("custom CSS input signature did not change")
	}
	if firstOutput.OutputHash == secondOutput.OutputHash {
		t.Fatal("custom CSS output hash did not change")
	}
}

func TestStrictCacheTracksRecursiveCustomCSSDependencies(t *testing.T) {
	vault := t.TempDir()
	writeStrictFile(t, vault, "obsite.yaml", "title: Cache\nbaseURL: https://example.test/\nnavigation: []\n")
	writeStrictFile(t, vault, "_index.md", "---\ntitle: Home\npublish: true\n---\nHome\n")
	writeStrictFile(t, vault, "custom.css", `@import "styles/nested.css";`)
	writeStrictFile(t, vault, "styles/nested.css", `@font-face { src: url("../fonts/site.woff2"); }`)
	writeStrictFile(t, vault, "fonts/site.woff2", "first font")
	output := filepath.Join(t.TempDir(), "site")
	firstResult, err := BuildWithOptions(vault, output, Options{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	first := loadStrictCacheManifest(output)
	firstDependency, firstOutput := cacheDependencyByOwner(first, "custom CSS"), cacheOutputByOwner(first, "custom CSS")
	oldNested := firstResult.Assets["styles/nested.css"].DstPath

	writeStrictFile(t, vault, "fonts/site.woff2", "second font")
	secondResult, err := BuildWithOptions(vault, output, Options{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	second := loadStrictCacheManifest(output)
	secondDependency, secondOutput := cacheDependencyByOwner(second, "custom CSS"), cacheOutputByOwner(second, "custom CSS")
	if firstDependency.InputSignature == secondDependency.InputSignature || firstOutput.OutputHash == secondOutput.OutputHash {
		t.Fatal("recursive CSS change did not invalidate the fixed custom CSS output")
	}
	if secondResult.Assets["styles/nested.css"].DstPath == oldNested {
		t.Fatal("recursive CSS change did not invalidate the transformed stylesheet")
	}
	if _, err := os.Stat(filepath.Join(output, filepath.FromSlash(oldNested))); !os.IsNotExist(err) {
		t.Fatalf("stale transformed stylesheet remains after rebuild: %v", err)
	}
}

func cacheDependencyByOwner(manifest *strictCacheManifest, owner string) strictCacheDependency {
	for _, dependency := range manifest.Dependencies {
		if dependency.Owner == owner {
			return dependency
		}
	}
	return strictCacheDependency{}
}

func cacheOutputByOwner(manifest *strictCacheManifest, owner string) strictCacheOutput {
	for _, output := range manifest.Outputs {
		if output.Owner == owner {
			return output
		}
	}
	return strictCacheOutput{}
}
