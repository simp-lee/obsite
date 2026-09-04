package build

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestNormalBuildRejectsMissingMarkdownAssetsAndPreservesPreviousOutput(t *testing.T) {
	vault := t.TempDir()
	output := filepath.Join(t.TempDir(), "site")
	writeStrictFile(t, vault, "obsite.yaml", "title: Site\nbaseURL: https://example.test/\nnavigation: []\n")
	writeStrictFile(t, vault, "_index.md", "---\ntitle: Home\npublish: true\n---\n")
	writeStrictFile(t, vault, "article.md", "---\ntitle: Article\npublish: true\ntype: page\n---\nPublished body\n")
	if _, err := BuildWithOptions(vault, output, Options{}); err != nil {
		t.Fatal(err)
	}
	before := readBuildOutputFile(t, output, "article/index.html")

	writeStrictFile(t, vault, "article.md", "---\ntitle: Article\npublish: true\ntype: page\n---\n![Missing](missing.png)\n\n[Attachment](missing.pdf)\n\n![[missing-embed.png]]\n\n![[missing%2Epng]]\n\n![Outside](../outside.png)\n\n[Root attachment](/missing-root.pdf)\n\n[Fragment attachment](missing-fragment.pdf#page=2)\n\n[Windows attachment](C:/assets/manual.pdf)\n\n[Malformed attachment](missing%ZZ.pdf)\n")
	result, err := BuildWithOptions(vault, output, Options{})
	if err == nil {
		t.Fatal("normal build error = nil, want unresolved local assets to fail")
	}
	if result == nil || result.ErrorCount != 9 || result.WarningCount != 0 {
		t.Fatalf("build result = %#v, want nine unresolved asset errors", result)
	}
	for _, item := range result.Diagnostics {
		if item.Severity != "error" || item.Kind != "unresolved_asset" {
			t.Fatalf("diagnostic = %#v, want unresolved_asset error", item)
		}
	}
	after := readBuildOutputFile(t, output, "article/index.html")
	if !bytes.Equal(before, after) {
		t.Fatal("previous output changed after Markdown asset validation failure")
	}
}

func TestStrictBuildPreservesPreviousOutputOnAssetFailure(t *testing.T) {
	vault := copyFixtureVault(t, "feature-vault")
	output := filepath.Join(t.TempDir(), "site")
	if _, err := BuildWithOptions(vault, output, Options{}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(output, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vault, "images", "cover.png"), []byte("not an image"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildWithOptions(vault, output, Options{}); err == nil {
		t.Fatal("build error = nil")
	}
	after, err := os.ReadFile(filepath.Join(output, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("previous output changed after asset validation failure")
	}
}
