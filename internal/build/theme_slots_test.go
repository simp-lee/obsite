package build

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBuildRendersFixedThemeSlotsOnEveryPage(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	writeBuildTestFile(t, vaultPath, ".obsite/theme/assets/icons/logo.svg", "<svg></svg>\n")
	writeBuildTestFile(t, vaultPath, ".obsite/theme/slots.html", `
{{define "obsite-head-end"}}<meta name="slot-hd" content="{{.Kind}}">{{end}}
{{define "obsite-header-end"}}<span id="slot-hr">{{.Site.Title}}</span>{{end}}
{{define "obsite-main-end"}}<img id="slot-mn" src="{{themeAssetURL .SiteRootRel "icons/logo.svg"}}" alt="theme">{{end}}
{{define "obsite-footer-end"}}<span id="slot-ft">{{.RelPath}}</span>{{end}}
`)
	writeBuildTestFile(t, vaultPath, "notes/alpha.md", `---
title: Alpha
tags:
  - Topic
---
# Alpha

Body.
`)

	cfg := testBuildSiteConfig()
	cfg.BaseURL = "https://example.com/blog/"
	if _, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{}); err != nil {
		t.Fatalf("buildWithOptions() error = %v", err)
	}

	for _, relPath := range []string{"index.html", "alpha/index.html", "tags/topic/index.html", "notes/index.html", "404.html"} {
		html := readBuildOutputFile(t, outputPath, relPath)
		for _, marker := range [][]byte{[]byte("slot-hd"), []byte("slot-hr"), []byte("slot-mn"), []byte("slot-ft")} {
			if count := bytes.Count(html, marker); count != 1 {
				t.Fatalf("%s marker %q count = %d, want 1\n%s", relPath, marker, count, html)
			}
		}
	}
	if html := readBuildOutputFile(t, outputPath, "index.html"); !containsAny(html, `src="./assets/theme/icons/logo.svg"`, `src=./assets/theme/icons/logo.svg`) {
		t.Fatalf("root slot theme asset URL is incorrect\n%s", html)
	}
	if html := readBuildOutputFile(t, outputPath, "tags/topic/index.html"); !containsAny(html, `src="../../assets/theme/icons/logo.svg"`, `src=../../assets/theme/icons/logo.svg`) {
		t.Fatalf("nested slot theme asset URL is incorrect\n%s", html)
	}
	if _, err := os.Stat(filepath.Join(outputPath, "slots.html")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("slots.html was published: %v", err)
	}
}

func TestBuildRejectsInvalidOrSymlinkThemeSlotsBeforePublication(t *testing.T) {
	t.Run("invalid definition", func(t *testing.T) {
		vaultPath := t.TempDir()
		outputPath := filepath.Join(t.TempDir(), "site")
		writeBuildTestFile(t, vaultPath, "notes/alpha.md", "# Alpha\n")
		writeBuildTestFile(t, vaultPath, ".obsite/theme/slots.html", `{{define "base"}}replacement{{end}}`)

		_, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{})
		if err == nil || !strings.Contains(err.Error(), `unknown theme slot definition "base"`) {
			t.Fatalf("buildWithOptions() error = %v, want built-in redefinition rejection", err)
		}
		if _, statErr := os.Stat(outputPath); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("os.Stat(output) error = %v, want no published output", statErr)
		}
	})

	t.Run("symlink", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symlink creation may require elevated privileges")
		}
		vaultPath := t.TempDir()
		outputPath := filepath.Join(t.TempDir(), "site")
		external := t.TempDir()
		writeBuildTestFile(t, vaultPath, "notes/alpha.md", "# Alpha\n")
		writeBuildTestFile(t, external, "slots.html", `{{define "obsite-main-end"}}outside{{end}}`)
		themeDir := filepath.Join(vaultPath, ".obsite", "theme")
		if err := os.MkdirAll(themeDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(external, "slots.html"), filepath.Join(themeDir, "slots.html")); err != nil {
			t.Fatal(err)
		}

		_, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{})
		if err == nil || !strings.Contains(err.Error(), "regular non-symlink") {
			t.Fatalf("buildWithOptions() error = %v, want symlink rejection", err)
		}
		if _, statErr := os.Stat(outputPath); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("os.Stat(output) error = %v, want no published output", statErr)
		}
	})
}

func TestBuildThemeSlotMutationInvalidatesEveryPage(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	writeBuildTestFile(t, vaultPath, "notes/alpha.md", "# Alpha\n")
	writeBuildTestFile(t, vaultPath, ".obsite/theme/slots.html", `{{define "obsite-main-end"}}<span>slot-v1</span>{{end}}`)
	if _, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{}); err != nil {
		t.Fatalf("first buildWithOptions() error = %v", err)
	}
	before := snapshotThemeTestOutput(t, outputPath)

	writeBuildTestFile(t, vaultPath, ".obsite/theme/slots.html", `{{define "obsite-main-end"}}<span>slot-v2</span>{{end}}`)
	getRenderedMarkdown, markdownHook := captureRenderedMarkdownNotePaths(t)
	if _, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{testMarkdownNoteHook: markdownHook}); err != nil {
		t.Fatalf("second buildWithOptions() error = %v", err)
	}
	if got := getRenderedMarkdown(); len(got) != 0 {
		t.Fatalf("slot-only mutation rerendered Markdown notes: %#v", got)
	}
	after := snapshotThemeTestOutput(t, outputPath)
	changed := changedThemeTestOutputs(before, after)
	changedSet := make(map[string]struct{}, len(changed))
	for _, name := range changed {
		changedSet[name] = struct{}{}
		if !strings.HasSuffix(name, ".html") && name != cacheManifestRelPath {
			t.Fatalf("slot mutation unexpectedly changed %q; all changes: %#v", name, changed)
		}
	}
	for name := range before {
		if strings.HasSuffix(name, ".html") {
			if _, ok := changedSet[name]; !ok {
				t.Fatalf("slot mutation left page %q unchanged; all changes: %#v", name, changed)
			}
		}
	}
}
