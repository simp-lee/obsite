package build

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStrictBuildPreservesUnicodeSourcePaths(t *testing.T) {
	vault := t.TempDir()
	writeStrictFile(t, vault, "obsite.yaml", `title: Unicode
baseURL: https://example.test/
navigation:
  - name: Home
    section: .
source:
  viewURL: https://git.example/view/:path
`)
	writeStrictFile(t, vault, "_index.md", "---\ntitle: Home\npublish: true\n---\nHome\n")
	writeStrictFile(t, vault, "Ａ.md", "---\ntitle: Fullwidth\npublish: true\ntype: page\n---\nContent\n")
	output := filepath.Join(t.TempDir(), "site")
	if _, err := BuildWithOptions(vault, output, Options{}); err != nil {
		t.Fatal(err)
	}
	page := string(readBuildOutputFile(t, output, "A/index.html"))
	if !strings.Contains(page, `https://git.example/view/%EF%BC%A1.md`) {
		t.Fatalf("source link did not preserve the source filename:\n%s", page)
	}
	if strings.Contains(page, `https://git.example/view/A.md`) {
		t.Fatalf("source link normalized the source filename:\n%s", page)
	}
}

func TestStrictBuildResolvesLinksToGeneratedTagPages(t *testing.T) {
	vault := t.TempDir()
	writeStrictFile(t, vault, "obsite.yaml", "title: Tags\nbaseURL: https://example.test/\nnavigation:\n  - name: Home\n    section: .\n")
	writeStrictFile(t, vault, "_index.md", "---\ntitle: Home\npublish: true\n---\nHome\n")
	writeStrictFile(t, vault, "article.md", "---\ntitle: Article\npublish: true\ntype: page\ntags: [foo]\n---\n[Foo](/tags/foo/)\n")
	output := filepath.Join(t.TempDir(), "site")
	if result, err := BuildWithOptions(vault, output, Options{Strict: true}); err != nil {
		t.Fatalf("BuildWithOptions() error = %v; diagnostics = %#v", err, result.Diagnostics)
	} else if result.WarningCount != 0 {
		t.Fatalf("WarningCount = %d, want no warning for generated tag link; diagnostics = %#v", result.WarningCount, result.Diagnostics)
	}
	page := string(readBuildOutputFile(t, output, "article/index.html"))
	if !strings.Contains(page, `href=/tags/foo/`) || !strings.Contains(page, `>Foo</a>`) {
		t.Fatalf("tag link was not preserved as a generated route:\n%s", page)
	}
	if _, err := os.Stat(filepath.Join(output, "tags", "foo", "index.html")); err != nil {
		t.Fatalf("generated tag page is missing: %v", err)
	}
}

func TestStrictBuildUsesCanonicalRoutesForNestedMarkdownLinks(t *testing.T) {
	vault := copyFixtureVault(t, "runtime-vault")
	output := filepath.Join(t.TempDir(), "site")
	writeStrictFile(t, vault, "manual file.pdf", "manual attachment")
	writeStrictFile(t, vault, "child/child.md", "---\ntitle: Child Article\npublish: true\ntype: doc\n---\n[Reference](../reference.md#reference) [Manual](../manual%20file.pdf)\n")
	if _, err := BuildWithOptions(vault, output, Options{}); err != nil {
		t.Fatal(err)
	}
	page := readBuildOutputFile(t, output, "child/child/index.html")
	if !bytes.Contains(page, []byte(`href=../../reference/`)) {
		t.Fatalf("nested link did not use canonical target route:\n%s", page)
	}
	if !bytes.Contains(page, []byte(`href=../../assets/manual%20file.`)) {
		t.Fatalf("nested attachment link did not use the asset planner:\n%s", page)
	}
	if entries, err := filepath.Glob(filepath.Join(output, "assets", "manual%20file.*.pdf")); err != nil || len(entries) != 1 {
		t.Fatalf("missing published content-addressed attachment: %v, err=%v", entries, err)
	}
	if !bytes.Contains(page, []byte(`data-popover-path=reference.md`)) {
		t.Fatalf("nested link did not receive its popover target:\n%s", page)
	}
	if _, err := os.Stat(filepath.Join(output, "_popover", "reference.md.json")); err != nil {
		t.Fatalf("missing popover payload: %v", err)
	}
}
