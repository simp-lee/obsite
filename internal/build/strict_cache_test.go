package build

import (
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
