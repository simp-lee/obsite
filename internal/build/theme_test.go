package build

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"
)

func TestBuildPublishesFixedThemeLayersAndAssets(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	themeCSS := ":root { --obsite-accent: rebeccapurple; }\n.hero { background-image: url(\"images/paper.txt\"); }\n"
	customCSS := ":root { --obsite-accent: tomato; }\n"
	writeBuildTestFile(t, vaultPath, ".obsite/theme/theme.css", themeCSS)
	writeBuildTestFile(t, vaultPath, ".obsite/theme/assets/images/paper.txt", "paper")
	writeBuildTestFile(t, vaultPath, "custom.css", customCSS)
	writeBuildTestFile(t, vaultPath, "notes/alpha.md", `---
title: Alpha
tags:
  - Topic
---
# Alpha

Inline math $x^2$.
`)

	cfg := testBuildSiteConfig()
	cfg.BaseURL = "https://example.com/blog/"
	if _, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{}); err != nil {
		t.Fatalf("buildWithOptions() error = %v", err)
	}

	if got := readBuildOutputFile(t, outputPath, themeCSSOutputPath); !bytes.Equal(got, []byte(themeCSS)) {
		t.Fatalf("theme.css = %q, want original bytes %q", got, themeCSS)
	}
	if got := readBuildOutputFile(t, outputPath, "assets/theme/images/paper.txt"); string(got) != "paper" {
		t.Fatalf("theme asset = %q, want %q", got, "paper")
	}
	if _, err := os.Stat(filepath.Join(outputPath, "assets", "theme", "assets")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("theme asset retained source assets prefix: %v", err)
	}
	if got := readBuildOutputFile(t, outputPath, customCSSOutputPath); !bytes.Equal(got, []byte(customCSS)) {
		t.Fatalf("custom.css = %q, want original bytes %q", got, customCSS)
	}

	noteHTML := readBuildOutputFile(t, outputPath, "alpha/index.html")
	assertStylesheetOrder(t, noteHTML,
		`../style.css`,
		`../assets/obsite-runtime/katex.min.css`,
		`../assets/theme/theme.css`,
		`../assets/custom.css`,
	)
	indexHTML := readBuildOutputFile(t, outputPath, "index.html")
	assertStylesheetOrder(t, indexHTML, `./style.css`, `./assets/theme/theme.css`, `./assets/custom.css`)
	tagHTML := readBuildOutputFile(t, outputPath, "tags/topic/index.html")
	assertStylesheetOrder(t, tagHTML, `../../style.css`, `../../assets/theme/theme.css`, `../../assets/custom.css`)

	stylesheetURL, err := url.Parse("https://example.com/blog/assets/theme/theme.css")
	if err != nil {
		t.Fatal(err)
	}
	assetURL, err := url.Parse("images/paper.txt")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := stylesheetURL.ResolveReference(assetURL).String(), "https://example.com/blog/assets/theme/images/paper.txt"; got != want {
		t.Fatalf("relative theme asset URL = %q, want %q", got, want)
	}
}

func TestBuildThemeLayerMutationsChangeOnlyOwnedOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		sourcePath string
		outputPath string
	}{
		{name: "theme stylesheet", sourcePath: ".obsite/theme/theme.css", outputPath: themeCSSOutputPath},
		{name: "custom stylesheet", sourcePath: "custom.css", outputPath: customCSSOutputPath},
		{name: "theme asset", sourcePath: ".obsite/theme/assets/icons/mark.svg", outputPath: "assets/theme/icons/mark.svg"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			vaultPath := t.TempDir()
			outputPath := filepath.Join(t.TempDir(), "site")
			writeBuildTestFile(t, vaultPath, "notes/alpha.md", "# Alpha\n")
			writeBuildTestFile(t, vaultPath, ".obsite/theme/theme.css", ":root { --obsite-accent: blue; }\n")
			writeBuildTestFile(t, vaultPath, ".obsite/theme/assets/icons/mark.svg", "<svg>blue</svg>\n")
			writeBuildTestFile(t, vaultPath, "custom.css", "body { outline-color: blue; }\n")
			if _, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{}); err != nil {
				t.Fatalf("first buildWithOptions() error = %v", err)
			}
			before := snapshotThemeTestOutput(t, outputPath)

			writeBuildTestFile(t, vaultPath, tt.sourcePath, "changed owned input\n")
			if _, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{}); err != nil {
				t.Fatalf("second buildWithOptions() error = %v", err)
			}
			changed := changedThemeTestOutputs(before, snapshotThemeTestOutput(t, outputPath))
			if want := []string{cacheManifestRelPath, tt.outputPath}; !slices.Equal(changed, want) {
				t.Fatalf("changed outputs = %#v, want %#v", changed, want)
			}
		})
	}
}

func TestBuildRejectsUnsupportedAndEscapingThemeInputsBeforePublication(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation may require elevated privileges")
	}

	tests := []struct {
		name  string
		setup func(t *testing.T, vaultPath string)
		want  string
	}{
		{
			name: "unsupported top-level file",
			setup: func(t *testing.T, vaultPath string) {
				writeBuildTestFile(t, vaultPath, ".obsite/theme/base.html", "not allowed")
			},
			want: "unsupported theme entry",
		},
		{
			name: "asset symlink",
			setup: func(t *testing.T, vaultPath string) {
				external := t.TempDir()
				writeBuildTestFile(t, external, "secret.txt", "secret")
				assetPath := filepath.Join(vaultPath, ".obsite", "theme", "assets", "secret.txt")
				if err := os.MkdirAll(filepath.Dir(assetPath), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(external, "secret.txt"), assetPath); err != nil {
					t.Fatal(err)
				}
			},
			want: "must not be a symbolic link",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vaultPath := t.TempDir()
			outputPath := filepath.Join(t.TempDir(), "site")
			writeBuildTestFile(t, vaultPath, "notes/alpha.md", "# Alpha\n")
			tt.setup(t, vaultPath)
			_, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("buildWithOptions() error = %v, want %q", err, tt.want)
			}
			if _, statErr := os.Stat(outputPath); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("os.Stat(output) error = %v, want no published output", statErr)
			}
		})
	}
}

func TestBuildRejectsThemeAssetBackslashTraversal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("backslash is a path separator on Windows")
	}

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	writeBuildTestFile(t, vaultPath, "notes/alpha.md", "# Alpha\n")
	writeBuildTestFile(t, vaultPath, `.obsite/theme/assets/..\..\banner.png`, "banner")

	_, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{})
	if err == nil || !strings.Contains(err.Error(), "portable path characters") {
		t.Fatalf("buildWithOptions() error = %v, want portable theme asset path error", err)
	}
	if _, statErr := os.Stat(outputPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("os.Stat(output) error = %v, want no published output", statErr)
	}
}

func TestBuildRejectsThemeAssetCollisionWithThemeStylesheet(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	writeBuildTestFile(t, vaultPath, "notes/alpha.md", "# Alpha\n")
	writeBuildTestFile(t, vaultPath, ".obsite/theme/theme.css", ":root {}\n")
	writeBuildTestFile(t, vaultPath, ".obsite/theme/assets/theme.css", "collision")

	_, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{})
	if err == nil || !strings.Contains(err.Error(), "output destination conflict") {
		t.Fatalf("buildWithOptions() error = %v, want output destination conflict", err)
	}
	if _, statErr := os.Stat(outputPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("os.Stat(output) error = %v, want no published output", statErr)
	}
}

func TestBuildRejectsNonFixedThemeDirectory(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	writeBuildTestFile(t, vaultPath, "notes/alpha.md", "# Alpha\n")
	writeBuildTestFile(t, vaultPath, "themes/other/theme.css", ":root {}\n")
	cfg := testBuildSiteConfig()
	cfg.ThemeDir = filepath.Join(vaultPath, "themes", "other")

	_, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{})
	if err == nil || !strings.Contains(err.Error(), "must be the fixed vault input") {
		t.Fatalf("buildWithOptions() error = %v, want fixed theme directory error", err)
	}
}

func snapshotThemeTestOutput(t *testing.T, outputRoot string) map[string][sha256.Size]byte {
	t.Helper()
	snapshot := make(map[string][sha256.Size]byte)
	err := filepath.Walk(outputRoot, func(current string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir() {
			return walkErr
		}
		data, err := os.ReadFile(current)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(outputRoot, current)
		if err != nil {
			return err
		}
		snapshot[filepath.ToSlash(relative)] = sha256.Sum256(data)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot output: %v", err)
	}
	return snapshot
}

func changedThemeTestOutputs(before map[string][sha256.Size]byte, after map[string][sha256.Size]byte) []string {
	changed := make([]string, 0)
	seen := make(map[string]struct{}, len(before)+len(after))
	for name := range before {
		seen[name] = struct{}{}
	}
	for name := range after {
		seen[name] = struct{}{}
	}
	for name := range seen {
		beforeHash, beforeOK := before[name]
		afterHash, afterOK := after[name]
		if beforeOK != afterOK || beforeHash != afterHash {
			changed = append(changed, name)
		}
	}
	sort.Strings(changed)
	return changed
}

func assertStylesheetOrder(t *testing.T, html []byte, hrefs ...string) {
	t.Helper()
	previous := -1
	for _, href := range hrefs {
		index := bytes.Index(html, []byte(href))
		if index < 0 {
			t.Fatalf("page missing stylesheet %q\n%s", href, html)
		}
		if index <= previous {
			t.Fatalf("stylesheet %q is out of order\n%s", href, html)
		}
		previous = index
	}
}
