package build

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func copyFixtureVault(t *testing.T, fixtureName string) string {
	t.Helper()
	srcRoot := filepath.Join("..", "..", "test", "testdata", "e2e", filepath.FromSlash(fixtureName))
	dstRoot := t.TempDir()
	stamp := time.Date(2026, time.April, 6, 12, 0, 0, 0, time.UTC)
	if err := filepath.Walk(srcRoot, func(source string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(srcRoot, source)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		destination := filepath.Join(dstRoot, rel)
		if info.IsDir() {
			return os.MkdirAll(destination, 0o755)
		}
		data, err := os.ReadFile(source)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(destination, data, 0o644); err != nil {
			return err
		}
		return os.Chtimes(destination, stamp, stamp)
	}); err != nil {
		t.Fatalf("copy fixture %q: %v", fixtureName, err)
	}
	return dstRoot
}

func readBuildOutputFile(t *testing.T, root, relPath string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relPath)))
	if err != nil {
		t.Fatalf("read output %q: %v", relPath, err)
	}
	return data
}

func TestStrictBuildUsesCanonicalSectionPlanAndRichMarkdown(t *testing.T) {
	vaultPath := copyFixtureVault(t, "feature-vault")
	outputPath := filepath.Join(t.TempDir(), "site")
	result, err := BuildWithOptions(vaultPath, outputPath, Options{})
	if err != nil {
		t.Fatalf("BuildWithOptions() error = %v", err)
	}
	if result.NotePages != 2 {
		t.Fatalf("NotePages = %d, want 2", result.NotePages)
	}
	for _, rel := range []string{"index.html", "guide/index.html", "guide/article/index.html", "roadmap/index.html", "tags/feature/index.html", "updates/index.html", "404.html", "assets/custom.css", ".obsite-cache/manifest.json", "sitemap.xml", "index.xml"} {
		if _, err := os.Stat(filepath.Join(outputPath, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("missing output %q: %v", rel, err)
		}
	}
	article := readBuildOutputFile(t, outputPath, "guide/article/index.html")
	for _, want := range []string{"Feature Article", "deprecated", "Alice Example", "Developers", "2.0", "Releases", "application/ld+json", "summary_large_image", "page-banner"} {
		if !bytes.Contains(article, []byte(want)) {
			t.Fatalf("article missing %q\n%s", want, article)
		}
	}
	if strings.Contains(string(article), "assets/social") == false {
		t.Fatal("article missing generated social image")
	}
	rss := string(readBuildOutputFile(t, outputPath, "index.xml"))
	for _, want := range []string{"<link>https://example.com/blog/</link>", "<description>Integration coverage for strict section features.</description>", "<obsite:status>deprecated</obsite:status>", "<obsite:audience>Developers</obsite:audience>", "<obsite:productVersion>2.0</obsite:productVersion>", "<obsite:series>Releases</obsite:series>"} {
		if !strings.Contains(rss, want) {
			t.Fatalf("RSS missing %q: %s", want, rss)
		}
	}
	if got := readBuildOutputFile(t, outputPath, "assets/custom.css"); len(bytes.TrimSpace(got)) == 0 {
		t.Fatal("custom CSS output is empty")
	}
}

func TestStrictBuildPreservesManagedOutputOnPlanningFailure(t *testing.T) {
	vaultPath := copyFixtureVault(t, "slug-conflict-vault")
	outputPath := filepath.Join(t.TempDir(), "site")
	if err := os.MkdirAll(outputPath, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(outputPath, managedOutputMarkerFilename)
	if err := os.WriteFile(marker, []byte(managedOutputMarkerContents), 0o644); err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(outputPath, "index.html")
	before := []byte("published output")
	if err := os.WriteFile(index, before, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildWithOptions(vaultPath, outputPath, Options{}); err == nil {
		t.Fatal("BuildWithOptions() error = nil, want route conflict")
	}
	if got, err := os.ReadFile(index); err != nil || !bytes.Equal(got, before) {
		t.Fatalf("managed output changed after planning failure: %q, %v", got, err)
	}
}
