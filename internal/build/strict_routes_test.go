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
	if _, err := BuildWithOptions(vault, output, Options{}); err != nil {
		t.Fatal(err)
	}
	page := readBuildOutputFile(t, output, "child/child/index.html")
	if !bytes.Contains(page, []byte(`href=../../reference/`)) {
		t.Fatalf("nested link did not use canonical target route:\n%s", page)
	}
	if !bytes.Contains(page, []byte(`data-popover-path=reference.md`)) {
		t.Fatalf("nested link did not receive its popover target:\n%s", page)
	}
	if _, err := os.Stat(filepath.Join(output, "_popover", "reference.md.json")); err != nil {
		t.Fatalf("missing popover payload: %v", err)
	}
}
