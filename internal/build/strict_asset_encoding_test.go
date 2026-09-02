package build

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestStrictBuildPublishesEncodedAssetPathsForPreviewServing(t *testing.T) {
	vault := t.TempDir()
	writeStrictFile(t, vault, "obsite.yaml", "title: Assets\nbaseURL: https://example.test/\nnavigation: []\n")
	writeStrictFile(t, vault, "_index.md", "---\ntitle: Home\npublish: true\nbanner: images/My Banner.png\nbannerAlt: Banner\n---\nHome\n")
	fixture, err := os.ReadFile(filepath.Join("..", "..", "test", "testdata", "e2e", "feature-vault", "images", "cover.png"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(vault, "images"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vault, "images", "My Banner.png"), fixture, 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "site")
	if _, err := BuildWithOptions(vault, output, Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(output, "assets", "My%20Banner.png")); err != nil {
		t.Fatalf("encoded asset missing: %v", err)
	}
	page := readBuildOutputFile(t, output, "index.html")
	if !bytes.Contains(page, []byte("/assets/My%20Banner.png")) {
		t.Fatalf("page did not link encoded asset:\n%s", page)
	}
}
