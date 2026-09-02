package build

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStrictBuildPublishesIndependentVersionTreesAndEscapedSources(t *testing.T) {
	vault := t.TempDir()
	writeStrictFile(t, vault, "obsite.yaml", `title: Versioned
baseURL: https://example.test/docs/
navigation:
  - name: Docs
    section: docs
source:
  editURL: https://git.example/edit/:path?ref=main
  viewURL: https://git.example/view/:path
versions:
  root: docs
  default: v1
  entries:
    - id: v1
      label: Version 1
      source: v1
    - id: v2
      label: Version 2
      source: v2
`)
	writeStrictFile(t, vault, "_index.md", "---\ntitle: Home\npublish: true\n---\nHome\n")
	writeStrictFile(t, vault, "docs/_index.md", "---\ntitle: Docs\npublish: true\n---\nDocs\n")
	for _, version := range []string{"v1", "v2"} {
		writeStrictFile(t, vault, "docs/"+version+"/_index.md", "---\ntitle: "+version+"\npublish: true\n---\n")
	}
	writeStrictFile(t, vault, "docs/v1/Start Here.md", "---\ntitle: Start V1\npublish: true\ntype: doc\n---\nV1\n")
	writeStrictFile(t, vault, "docs/v2/Start Here.md", "---\ntitle: Start V2\npublish: true\ntype: doc\n---\nV2\n")
	output := filepath.Join(t.TempDir(), "site")
	if _, err := BuildWithOptions(vault, output, Options{}); err != nil {
		t.Fatal(err)
	}
	docs := readBuildOutputFile(t, output, "docs/index.html")
	if !bytes.Contains(docs, []byte("Version 1")) || !bytes.Contains(docs, []byte("Version 2")) {
		t.Fatalf("version entry points missing from docs landing:\n%s", docs)
	}
	v1 := readBuildOutputFile(t, output, "docs/v1/Start%20Here/index.html")
	v2 := readBuildOutputFile(t, output, "docs/v2/Start%20Here/index.html")
	for _, page := range [][]byte{v1, v2} {
		if !bytes.Contains(page, []byte("version-selector")) || !bytes.Contains(page, []byte("Edit this page")) || !bytes.Contains(page, []byte("View source")) {
			t.Fatalf("version/source metadata missing:\n%s", page)
		}
	}
	if !strings.Contains(string(v1), `https://git.example/edit/docs/v1/Start%20Here.md?ref=main`) {
		t.Fatalf("v1 source URL not segment-escaped:\n%s", v1)
	}
	if !strings.Contains(string(v1), `/docs/v2/`) || !strings.Contains(string(v2), `/docs/v1/`) {
		t.Fatalf("version selector did not preserve same-path links")
	}
	sitemap := string(readBuildOutputFile(t, output, "sitemap.xml"))
	if !strings.Contains(sitemap, "https://example.test/docs/docs/v1/Start%20Here/") || !strings.Contains(sitemap, "https://example.test/docs/docs/v2/Start%20Here/") {
		t.Fatalf("version routes missing from sitemap: %s", sitemap)
	}
}

func writeStrictFile(t *testing.T, root, relPath, content string) {
	t.Helper()
	name := filepath.Join(root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
