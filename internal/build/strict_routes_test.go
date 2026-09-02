package build

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

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
	if !bytes.Contains(page, []byte(`href=../../assets/manual%20file.pdf`)) {
		t.Fatalf("nested attachment link did not use the asset planner:\n%s", page)
	}
	if _, err := os.Stat(filepath.Join(output, "assets", "manual%20file.pdf")); err != nil {
		t.Fatalf("missing published attachment: %v", err)
	}
	if !bytes.Contains(page, []byte(`data-popover-path=reference.md`)) {
		t.Fatalf("nested link did not receive its popover target:\n%s", page)
	}
	if _, err := os.Stat(filepath.Join(output, "_popover", "reference.md.json")); err != nil {
		t.Fatalf("missing popover payload: %v", err)
	}
}
