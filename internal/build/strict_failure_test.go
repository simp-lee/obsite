package build

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

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
