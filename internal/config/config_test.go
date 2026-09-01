package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/simp-lee/obsite/internal/model"
)

func TestDefaultsAreCanonical(t *testing.T) {
	t.Parallel()

	cfg := Defaults()
	if cfg.Language != "en" || !cfg.DefaultPublish || cfg.DefaultImg != "" {
		t.Fatalf("core defaults = %#v", cfg)
	}
	if cfg.Pagination.PageSize != 20 || cfg.Sidebar.Enabled || cfg.Popover.Enabled {
		t.Fatalf("navigation defaults = %#v", cfg)
	}
	if cfg.Related.Enabled || cfg.Related.Count != 5 || !cfg.RSS.Enabled {
		t.Fatalf("feature defaults = %#v", cfg)
	}
	if cfg.Timeline.Enabled || cfg.Timeline.AsHomepage || cfg.Timeline.Path != "notes" {
		t.Fatalf("timeline defaults = %#v", cfg.Timeline)
	}
}

func TestLoadForBuildReadsOnlyVaultConfigAndAppliesValues(t *testing.T) {
	t.Parallel()

	vault := writeConfigVault(t, `
title: Garden Notes
baseURL: https://example.com/blog
author: Alice
description: Public notes
language: fr
defaultImg: images/og.png
defaultPublish: false
pagination:
  pageSize: 30
sidebar:
  enabled: true
popover:
  enabled: true
related:
  enabled: true
  count: 7
rss:
  enabled: false
timeline:
  enabled: true
  asHomepage: true
  path: timeline
`)

	cfg, err := LoadForBuild(vault)
	if err != nil {
		t.Fatalf("LoadForBuild() error = %v", err)
	}
	if cfg.Title != "Garden Notes" || cfg.BaseURL != "https://example.com/blog/" || cfg.Author != "Alice" || cfg.Description != "Public notes" || cfg.Language != "fr" {
		t.Fatalf("metadata = %#v", cfg)
	}
	if cfg.DefaultImg != "images/og.png" || cfg.DefaultPublish {
		t.Fatalf("publish defaults = %#v", cfg)
	}
	if cfg.Pagination.PageSize != 30 || !cfg.Sidebar.Enabled || !cfg.Popover.Enabled {
		t.Fatalf("navigation config = %#v", cfg)
	}
	if !cfg.Related.Enabled || cfg.Related.Count != 7 || cfg.RSS.Enabled {
		t.Fatalf("feature config = %#v", cfg)
	}
	if !cfg.Timeline.Enabled || !cfg.Timeline.AsHomepage || cfg.Timeline.Path != "timeline" {
		t.Fatalf("timeline = %#v", cfg.Timeline)
	}
}

func TestLoadForBuildUsesDefaultsForOmittedOptionalFields(t *testing.T) {
	t.Parallel()

	vault := writeConfigVault(t, "title: Garden\nbaseURL: https://example.com/\n")
	cfg, err := LoadForBuild(vault)
	if err != nil {
		t.Fatalf("LoadForBuild() error = %v", err)
	}
	defaults := Defaults()
	if cfg.Language != defaults.Language || cfg.DefaultPublish != defaults.DefaultPublish || cfg.Pagination != defaults.Pagination || cfg.Sidebar != defaults.Sidebar || cfg.Popover != defaults.Popover || cfg.Related != defaults.Related || cfg.RSS != defaults.RSS || cfg.Timeline != defaults.Timeline {
		t.Fatalf("loaded defaults = %#v, want %#v", cfg, defaults)
	}
}

func TestLoadForBuildRejectsRemovedAndUnknownFieldsWithLocation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		field string
		yaml  string
	}{
		{name: "search", field: "search", yaml: "search:\n  enabled: true"},
		{name: "pagefind path", field: "pagefindPath", yaml: "pagefindPath: pagefind"},
		{name: "pagefind version", field: "pagefindVersion", yaml: "pagefindVersion: 1.5.2"},
		{name: "themes", field: "themes", yaml: "themes: {}"},
		{name: "default theme", field: "defaultTheme", yaml: "defaultTheme: feature"},
		{name: "template dir", field: "templateDir", yaml: "templateDir: templates"},
		{name: "theme root", field: "themeRoot", yaml: "themeRoot: theme"},
		{name: "custom css", field: "customCSS", yaml: "customCSS: other.css"},
		{name: "katex css", field: "kaTeXCSSURL", yaml: "kaTeXCSSURL: https://cdn.example/katex.css"},
		{name: "katex js", field: "kaTeXJSURL", yaml: "kaTeXJSURL: https://cdn.example/katex.js"},
		{name: "katex auto render", field: "kaTeXAutoRenderURL", yaml: "kaTeXAutoRenderURL: https://cdn.example/auto.js"},
		{name: "mermaid", field: "mermaidJSURL", yaml: "mermaidJSURL: https://cdn.example/mermaid.js"},
		{name: "unknown nested", field: "extra", yaml: "sidebar:\n  extra: true"},
		{name: "removed field in second document", field: "search", yaml: "---\nsearch:\n  enabled: true"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vault := writeConfigVault(t, "title: Garden\nbaseURL: https://example.com/\n"+tt.yaml+"\n")
			_, err := LoadForBuild(vault)
			if err == nil {
				t.Fatal("LoadForBuild() error = nil")
			}
			if !strings.Contains(err.Error(), tt.field) || !strings.Contains(err.Error(), "line ") {
				t.Fatalf("LoadForBuild() error = %q, want field %q and line", err, tt.field)
			}
		})
	}
}

func TestLoadForBuildRejectsArbitraryConfigPath(t *testing.T) {
	t.Parallel()

	vault := writeConfigVault(t, "title: Garden\nbaseURL: https://example.com/\n")
	configPath := filepath.Join(vault, Filename)
	_, err := LoadForBuild(configPath)
	if err == nil {
		t.Fatal("LoadForBuild(config path) error = nil")
	}
}

func TestLoadForBuildValidatesRequiredAndBoundedValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		yaml string
		want string
	}{
		{name: "title", yaml: "baseURL: https://example.com/", want: "title is required"},
		{name: "base URL", yaml: "title: Garden", want: "baseURL is required"},
		{name: "relative base URL", yaml: "title: Garden\nbaseURL: /blog", want: "absolute http or https"},
		{name: "pagination", yaml: "title: Garden\nbaseURL: https://example.com/\npagination:\n  pageSize: 0", want: "pagination.pageSize"},
		{name: "related low", yaml: "title: Garden\nbaseURL: https://example.com/\nrelated:\n  count: 0", want: "related.count"},
		{name: "related high", yaml: "title: Garden\nbaseURL: https://example.com/\nrelated:\n  count: 21", want: "related.count"},
		{name: "timeline traversal", yaml: "title: Garden\nbaseURL: https://example.com/\ntimeline:\n  path: ../outside", want: "timeline.path"},
		{name: "timeline query", yaml: "title: Garden\nbaseURL: https://example.com/\ntimeline:\n  path: notes?draft=1", want: "timeline.path"},
		{name: "timeline fragment", yaml: "title: Garden\nbaseURL: https://example.com/\ntimeline:\n  path: notes#draft", want: "timeline.path"},
		{name: "timeline URI scheme", yaml: "title: Garden\nbaseURL: https://example.com/\ntimeline:\n  path: archive:2026", want: "timeline.path"},
		{name: "timeline non-portable character", yaml: "title: Garden\nbaseURL: https://example.com/\ntimeline:\n  path: notes/archive*", want: "timeline.path"},
		{name: "timeline reserved component", yaml: "title: Garden\nbaseURL: https://example.com/\ntimeline:\n  path: CON", want: "timeline.path"},
		{name: "timeline superscript device", yaml: "title: Garden\nbaseURL: https://example.com/\ntimeline:\n  path: COM¹", want: "timeline.path"},
		{name: "timeline superscript device extension", yaml: "title: Garden\nbaseURL: https://example.com/\ntimeline:\n  path: LPT².txt", want: "timeline.path"},
		{name: "timeline trailing dot", yaml: "title: Garden\nbaseURL: https://example.com/\ntimeline:\n  path: notes/archive.", want: "timeline.path"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vault := writeConfigVault(t, tt.yaml+"\n")
			_, err := LoadForBuild(vault)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("LoadForBuild() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestLoadForBuildDiscoversOnlyFixedVaultInputs(t *testing.T) {
	t.Parallel()

	vault := writeConfigVault(t, "title: Garden\nbaseURL: https://example.com/\n")
	writeConfigTestFile(t, vault, "custom.css", "body{}")
	writeConfigTestFile(t, vault, ".obsite/theme/theme.css", ":root{}")
	writeConfigTestFile(t, vault, "elsewhere/custom.css", "ignored")

	cfg, err := LoadForBuild(vault)
	if err != nil {
		t.Fatalf("LoadForBuild() error = %v", err)
	}
	if cfg.CustomCSS != filepath.Join(vault, "custom.css") {
		t.Fatalf("CustomCSS = %q", cfg.CustomCSS)
	}
	if cfg.ThemeDir != filepath.Join(vault, ".obsite", "theme") {
		t.Fatalf("ThemeDir = %q", cfg.ThemeDir)
	}
}

func TestLoadForBuildRejectsInvalidFixedVaultInputs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation may require elevated privileges")
	}

	tests := []struct {
		name  string
		setup func(t *testing.T, vault string, external string)
		want  string
	}{
		{
			name: "custom CSS directory",
			setup: func(t *testing.T, vault string, _ string) {
				if err := os.Mkdir(filepath.Join(vault, "custom.css"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
			want: "regular non-symlink file",
		},
		{
			name: "custom CSS symlink",
			setup: func(t *testing.T, vault string, external string) {
				writeConfigTestFile(t, external, "secret.css", "secret")
				if err := os.Symlink(filepath.Join(external, "secret.css"), filepath.Join(vault, "custom.css")); err != nil {
					t.Fatal(err)
				}
			},
			want: "regular non-symlink file",
		},
		{
			name: "theme file",
			setup: func(t *testing.T, vault string, _ string) {
				writeConfigTestFile(t, vault, ".obsite/theme", "not a directory")
			},
			want: "non-symlink directory",
		},
		{
			name: "theme symlink",
			setup: func(t *testing.T, vault string, external string) {
				if err := os.MkdirAll(filepath.Join(external, "theme"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Join(vault, ".obsite"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(external, "theme"), filepath.Join(vault, ".obsite", "theme")); err != nil {
					t.Fatal(err)
				}
			},
			want: "non-symlink directory",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vault := writeConfigVault(t, "title: Garden\nbaseURL: https://example.com/\n")
			external := t.TempDir()
			tt.setup(t, vault, external)
			_, err := LoadForBuild(vault)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("LoadForBuild() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestInitialYAMLUsesDefaultsAndStrictFields(t *testing.T) {
	t.Parallel()

	yaml := InitialYAML()
	for _, want := range []string{"baseURL: https://example.com/", "title: My Obsite Site", "language: en", "defaultPublish: true", "defaultImg: \"\"", "pageSize: 20", "count: 5", "path: notes"} {
		if !strings.Contains(yaml, want) {
			t.Fatalf("InitialYAML() missing %q\n%s", want, yaml)
		}
	}
	for _, forbidden := range []string{"search:", "pagefind", "themes:", "defaultTheme", "templateDir", "themeRoot", "customCSS", "kaTeX", "mermaid"} {
		if strings.Contains(yaml, forbidden) {
			t.Fatalf("InitialYAML() contains removed field %q\n%s", forbidden, yaml)
		}
	}
}

func TestNormalizeSiteConfigValidatesDefaultImageSemantics(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name     string
		value    string
		want     string
		external bool
	}{
		{name: "empty", value: "", want: ""},
		{name: "local", value: "images/../media/Hero.png", want: "media/Hero.png"},
		{name: "external HTTP", value: "http://images.example.test/hero.png?size=2#card", want: "http://images.example.test/hero.png?size=2#card", external: true},
		{name: "external HTTPS", value: "https://images.example.test/hero.png", want: "https://images.example.test/hero.png", external: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := NormalizeSiteConfig(model.SiteConfig{Title: "Garden", BaseURL: "https://example.com/", DefaultImg: tt.value})
			if err != nil {
				t.Fatalf("NormalizeSiteConfig() error = %v", err)
			}
			if cfg.DefaultImg != tt.want || cfg.DefaultImgExternal != tt.external {
				t.Fatalf("default image = (%q,%t), want (%q,%t)", cfg.DefaultImg, cfg.DefaultImgExternal, tt.want, tt.external)
			}
		})
	}

	for _, value := range []string{"images/hero.png?v=1", "images/hero.png#card", `images\\hero.png`, "/images/hero.png", "../hero.png", "C:/hero.png", "//server/share/hero.png", "data:image/png;base64,AA"} {
		t.Run(value, func(t *testing.T) {
			_, err := NormalizeSiteConfig(model.SiteConfig{Title: "Garden", BaseURL: "https://example.com/", DefaultImg: value})
			if err == nil || !strings.Contains(err.Error(), "defaultImg") {
				t.Fatalf("NormalizeSiteConfig(%q) error = %v, want defaultImg rejection", value, err)
			}
		})
	}
}

func TestNormalizeSiteConfigPreservesInternalBooleanPolicy(t *testing.T) {
	t.Parallel()

	cfg, err := NormalizeSiteConfig(model.SiteConfig{Title: "Garden", BaseURL: "https://example.com/", DefaultPublish: false, RSS: model.RSSConfig{Enabled: false}})
	if err != nil {
		t.Fatalf("NormalizeSiteConfig() error = %v", err)
	}
	if cfg.DefaultPublish || cfg.RSS.Enabled {
		t.Fatalf("NormalizeSiteConfig() changed explicit false policy: %#v", cfg)
	}
	if cfg.Language != "en" || cfg.Pagination.PageSize != 20 || cfg.Related.Count != 5 || cfg.Timeline.Path != "notes" {
		t.Fatalf("NormalizeSiteConfig() defaults = %#v", cfg)
	}
}

func writeConfigVault(t *testing.T, content string) string {
	t.Helper()
	vault := t.TempDir()
	writeConfigTestFile(t, vault, Filename, strings.TrimSpace(content)+"\n")
	return vault
}

func writeConfigTestFile(t *testing.T, root string, relPath string, content string) {
	t.Helper()
	filePath := filepath.Join(root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
