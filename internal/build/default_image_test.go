package build

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	xhtml "golang.org/x/net/html"
)

func TestBuildPublishesUnreferencedCanonicalDefaultImageAndMetadata(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	writeBuildTestFile(t, vaultPath, "notes/alpha.md", "# Alpha\n\nNo image reference.\n")
	writeBuildTestFile(t, vaultPath, "Images/Hero.PNG", "hero-image")
	cfg := testBuildSiteConfig()
	cfg.BaseURL = "https://example.com/blog/"
	cfg.DefaultImg = "images/hero.png"

	result, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{})
	if err != nil {
		t.Fatalf("buildWithOptions() error = %v", err)
	}
	asset := result.Assets["Images/Hero.PNG"]
	if asset == nil || asset.DstPath == "" {
		t.Fatalf("resolved default image asset = %#v", asset)
	}
	if got := readBuildOutputFile(t, outputPath, asset.DstPath); string(got) != "hero-image" {
		t.Fatalf("published default image = %q", got)
	}
	wantURL := "https://example.com/blog/" + asset.DstPath
	html := readBuildOutputFile(t, outputPath, "alpha/index.html")
	if got := metaContent(t, html, "property", "og:image"); got != wantURL {
		t.Fatalf("og:image = %q, want %q", got, wantURL)
	}
	if got := metaContent(t, html, "name", "twitter:image"); got != wantURL {
		t.Fatalf("twitter:image = %q, want %q", got, wantURL)
	}

	deployed := httptest.NewServer(http.StripPrefix("/blog/", http.FileServer(http.Dir(outputPath))))
	defer deployed.Close()
	mustHTTPStatus(t, deployed.Client(), deployed.URL+"/blog/"+asset.DstPath, http.StatusOK, "hero-image")
}

func TestBuildKeepsHostedDefaultImageExternal(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	writeBuildTestFile(t, vaultPath, "notes/alpha.md", "# Alpha\n")
	const external = "https://images.example.test/hero.png?size=2&mode=card#preview"
	cfg := testBuildSiteConfig()
	cfg.DefaultImg = external

	result, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{})
	if err != nil {
		t.Fatalf("buildWithOptions() error = %v", err)
	}
	if len(result.Assets) != 0 {
		t.Fatalf("external default image registered local assets: %#v", result.Assets)
	}
	html := readBuildOutputFile(t, outputPath, "alpha/index.html")
	if got := metaContent(t, html, "property", "og:image"); got != external {
		t.Fatalf("external og:image = %q, want %q", got, external)
	}
}

func TestBuildDefaultImageCollisionUsesFinalHashedURLAndInvalidatesMetadata(t *testing.T) {
	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	writeBuildTestFile(t, vaultPath, "notes/alpha.md", "# Alpha\n")
	writeBuildTestFile(t, vaultPath, "images/hero.png", "default-v1")
	writeBuildTestFile(t, vaultPath, "other/hero.png", "collision")
	cfg := testBuildSiteConfig()
	cfg.DefaultImg = "images/hero.png"

	first, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	firstAsset := first.Assets["images/hero.png"]
	if firstAsset == nil || firstAsset.DstPath == "assets/hero.png" || !strings.HasPrefix(firstAsset.DstPath, "assets/hero.") {
		t.Fatalf("first collision destination = %#v", firstAsset)
	}
	firstHTML := append([]byte(nil), readBuildOutputFile(t, outputPath, "alpha/index.html")...)
	firstDst := firstAsset.DstPath

	writeBuildTestFile(t, vaultPath, "images/hero.png", "default-v2")
	second, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	secondAsset := second.Assets["images/hero.png"]
	if secondAsset == nil || secondAsset.DstPath == firstDst {
		t.Fatalf("second collision destination = %#v, want changed hash from %q", secondAsset, firstDst)
	}
	if second.NotePages != 1 {
		t.Fatalf("second.NotePages = %d, want metadata page rewrite", second.NotePages)
	}
	secondHTML := readBuildOutputFile(t, outputPath, "alpha/index.html")
	if bytes.Equal(firstHTML, secondHTML) || !strings.Contains(string(secondHTML), secondAsset.DstPath) || strings.Contains(string(secondHTML), firstDst) {
		t.Fatalf("metadata did not follow changed default image destination\n%s", secondHTML)
	}
	if _, err := os.Stat(filepath.Join(outputPath, filepath.FromSlash(firstDst))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old default image destination remains: %v", err)
	}
}

func TestBuildEquivalentDefaultImageSpellingKeepsPagesCached(t *testing.T) {
	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	writeBuildTestFile(t, vaultPath, "notes/alpha.md", "# Alpha\n")
	writeBuildTestFile(t, vaultPath, "Images/Hero.PNG", "hero")
	cfg := testBuildSiteConfig()
	cfg.DefaultImg = "Images/Hero.PNG"
	if _, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{}); err != nil {
		t.Fatal(err)
	}
	before := append([]byte(nil), readBuildOutputFile(t, outputPath, "alpha/index.html")...)

	cfg.DefaultImg = "images/hero.png"
	getRenderedMarkdown := captureRenderedMarkdownNotePaths(t)
	getRenderedPages := captureRenderedNotePagePaths(t)
	result, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.NotePages != 0 || len(getRenderedMarkdown()) != 0 || len(getRenderedPages()) != 0 {
		t.Fatalf("equivalent spelling rerendered notes: NotePages=%d markdown=%#v pages=%#v", result.NotePages, getRenderedMarkdown(), getRenderedPages())
	}
	if got := readBuildOutputFile(t, outputPath, "alpha/index.html"); !bytes.Equal(got, before) {
		t.Fatal("equivalent defaultImg spelling changed page bytes")
	}
}

func TestBuildDefaultImageUnicodeEquivalentBasenameCollisionHashesDestination(t *testing.T) {
	t.Parallel()
	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	writeBuildTestFile(t, vaultPath, "notes/alpha.md", "# Alpha\n")
	writeBuildTestFile(t, vaultPath, "images/Café.png", "default")
	writeBuildTestFile(t, vaultPath, "other/Cafe\u0301.png", "peer")
	cfg := testBuildSiteConfig()
	cfg.DefaultImg = "images/Café.png"

	result, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	asset := result.Assets["images/Café.png"]
	if asset == nil || asset.DstPath == "assets/Café.png" || strings.Count(filepath.Base(asset.DstPath), ".") < 2 {
		t.Fatalf("Unicode collision destination = %#v", asset)
	}
	if _, err := os.Stat(filepath.Join(outputPath, filepath.FromSlash(asset.DstPath))); err != nil {
		t.Fatalf("published Unicode-collision default image: %v", err)
	}
	got := metaContent(t, readBuildOutputFile(t, outputPath, "alpha/index.html"), "property", "og:image")
	parsed, err := url.Parse(got)
	if err != nil || !strings.HasSuffix(parsed.Path, "/"+asset.DstPath) {
		t.Fatalf("og:image = %q, want decoded path suffix %q", got, asset.DstPath)
	}
}

func TestBuildDefaultImageRespectsReservedAssetDestination(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	writeBuildTestFile(t, vaultPath, "notes/alpha.md", "# Alpha\n")
	writeBuildTestFile(t, vaultPath, "custom.css", "custom-default-image")
	cfg := testBuildSiteConfig()
	cfg.DefaultImg = "custom.css"

	result, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	asset := result.Assets["custom.css"]
	if asset == nil || asset.DstPath == customCSSOutputPath || !strings.HasPrefix(asset.DstPath, "assets/custom.") {
		t.Fatalf("reserved default image destination = %#v", asset)
	}
	if got := metaContent(t, readBuildOutputFile(t, outputPath, "alpha/index.html"), "property", "og:image"); !strings.HasSuffix(got, asset.DstPath) {
		t.Fatalf("og:image = %q, want final reserved-collision path %q", got, asset.DstPath)
	}
}

func TestBuildRejectsUnavailableDefaultImage(t *testing.T) {
	tests := []struct {
		name  string
		value string
		setup func(t *testing.T, vaultPath string)
		want  string
	}{
		{name: "missing", value: "images/missing.png", want: "does not exist"},
		{name: "directory", value: "images/hero.png", setup: func(t *testing.T, vaultPath string) {
			if err := os.MkdirAll(filepath.Join(vaultPath, "images", "hero.png"), 0o755); err != nil {
				t.Fatal(err)
			}
		}, want: "regular file"},
		{name: "symlink", value: "images/hero.png", setup: func(t *testing.T, vaultPath string) {
			external := filepath.Join(t.TempDir(), "hero.png")
			if err := os.WriteFile(external, []byte("outside"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(vaultPath, "images"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(external, filepath.Join(vaultPath, "images", "hero.png")); err != nil {
				t.Fatal(err)
			}
		}, want: "symbolic link"},
		{name: "canonical ambiguity", value: "images/CAFÉ.png", setup: func(t *testing.T, vaultPath string) {
			writeBuildTestFile(t, vaultPath, "images/Café.png", "one")
			writeBuildTestFile(t, vaultPath, "images/Cafe\u0301.png", "two")
		}, want: "ambiguous"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.name == "symlink" && runtime.GOOS == "windows" {
				t.Skip("symlink creation may require elevated privileges")
			}
			vaultPath := t.TempDir()
			outputPath := filepath.Join(t.TempDir(), "site")
			writeBuildTestFile(t, vaultPath, "notes/alpha.md", "# Alpha\n")
			if tt.setup != nil {
				tt.setup(t, vaultPath)
			}
			cfg := testBuildSiteConfig()
			cfg.DefaultImg = tt.value
			_, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{})
			if err == nil || !strings.Contains(err.Error(), tt.want) || !strings.Contains(err.Error(), "defaultImg") {
				t.Fatalf("buildWithOptions() error = %v, want defaultImg %q", err, tt.want)
			}
			if _, statErr := os.Stat(outputPath); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("output exists after defaultImg failure: %v", statErr)
			}
		})
	}
}

func TestBuildEmptyDefaultImageOmitsImageMetadata(t *testing.T) {
	t.Parallel()
	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	writeBuildTestFile(t, vaultPath, "notes/alpha.md", "# Alpha\n")
	if _, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{}); err != nil {
		t.Fatal(err)
	}
	html := readBuildOutputFile(t, outputPath, "alpha/index.html")
	if metaContent(t, html, "property", "og:image") != "" || metaContent(t, html, "name", "twitter:image") != "" || metaContent(t, html, "name", "twitter:card") != "summary" {
		t.Fatalf("empty default image metadata is incorrect\n%s", html)
	}
}

func metaContent(t *testing.T, document []byte, key string, value string) string {
	t.Helper()
	root, err := xhtml.Parse(bytes.NewReader(document))
	if err != nil {
		t.Fatal(err)
	}
	var walk func(*xhtml.Node) string
	walk = func(node *xhtml.Node) string {
		if node.Type == xhtml.ElementNode && node.Data == "meta" {
			matches := false
			content := ""
			for _, attr := range node.Attr {
				if attr.Key == key && attr.Val == value {
					matches = true
				}
				if attr.Key == "content" {
					content = attr.Val
				}
			}
			if matches {
				return content
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if found := walk(child); found != "" {
				return found
			}
		}
		return ""
	}
	return walk(root)
}
