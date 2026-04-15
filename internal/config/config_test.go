package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/simp-lee/obsite/internal/model"
)

func TestLoadParsesExtendedYAML(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	configPath := writeConfigFileAt(t, configDir, `
title: Garden Notes
baseURL: https://example.com/blog
author: Alice
description: Public notes
language: fr
defaultImg: images/og.png
defaultPublish: false
templateDir: templates/custom
customCSS: styles/custom.css
search:
  enabled: true
  pagefindPath: /usr/local/bin/pagefind_extended
  pagefindVersion: 1.5.2
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

	loaded, err := LoadForBuild(configPath, Overrides{})
	if err != nil {
		t.Fatalf("LoadForBuild() error = %v", err)
	}
	cfg := loaded.Config

	if cfg.Title != "Garden Notes" {
		t.Fatalf("Title = %q, want %q", cfg.Title, "Garden Notes")
	}
	if cfg.BaseURL != "https://example.com/blog/" {
		t.Fatalf("BaseURL = %q, want %q", cfg.BaseURL, "https://example.com/blog/")
	}
	if cfg.Author != "Alice" {
		t.Fatalf("Author = %q, want %q", cfg.Author, "Alice")
	}
	if cfg.Description != "Public notes" {
		t.Fatalf("Description = %q, want %q", cfg.Description, "Public notes")
	}
	if cfg.Language != "fr" {
		t.Fatalf("Language = %q, want %q", cfg.Language, "fr")
	}
	if cfg.DefaultImg != "images/og.png" {
		t.Fatalf("DefaultImg = %q, want %q", cfg.DefaultImg, "images/og.png")
	}
	if cfg.DefaultPublish {
		t.Fatal("DefaultPublish = true, want false")
	}
	if cfg.TemplateDir != filepath.Join(configDir, "templates", "custom") {
		t.Fatalf("TemplateDir = %q, want %q", cfg.TemplateDir, filepath.Join(configDir, "templates", "custom"))
	}
	if cfg.CustomCSS != filepath.Join(configDir, "styles", "custom.css") {
		t.Fatalf("CustomCSS = %q, want %q", cfg.CustomCSS, filepath.Join(configDir, "styles", "custom.css"))
	}
	if loaded.AllowMissingCustomCSS {
		t.Fatal("AllowMissingCustomCSS = true, want false for explicit customCSS")
	}
	if !cfg.Search.Enabled {
		t.Fatal("Search.Enabled = false, want true")
	}
	if cfg.Search.PagefindPath != "/usr/local/bin/pagefind_extended" {
		t.Fatalf("Search.PagefindPath = %q, want %q", cfg.Search.PagefindPath, "/usr/local/bin/pagefind_extended")
	}
	if cfg.Search.PagefindVersion != "1.5.2" {
		t.Fatalf("Search.PagefindVersion = %q, want %q", cfg.Search.PagefindVersion, "1.5.2")
	}
	if cfg.Pagination.PageSize != 30 {
		t.Fatalf("Pagination.PageSize = %d, want %d", cfg.Pagination.PageSize, 30)
	}
	if !cfg.Sidebar.Enabled {
		t.Fatal("Sidebar.Enabled = false, want true")
	}
	if !cfg.Popover.Enabled {
		t.Fatal("Popover.Enabled = false, want true")
	}
	if !cfg.Related.Enabled || cfg.Related.Count != 7 {
		t.Fatalf("Related = %#v, want enabled count=7", cfg.Related)
	}
	if cfg.RSS.Enabled {
		t.Fatal("RSS.Enabled = true, want false")
	}
	if !cfg.Timeline.Enabled || !cfg.Timeline.AsHomepage || cfg.Timeline.Path != "timeline" {
		t.Fatalf("Timeline = %#v, want enabled homepage timeline path", cfg.Timeline)
	}
}

func TestLoadValidatesRequiredFields(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		content string
		wantErr string
	}{
		{
			name: "missing title",
			content: `
baseURL: https://example.com
`,
			wantErr: "title is required",
		},
		{
			name: "missing baseURL",
			content: `
title: Garden Notes
`,
			wantErr: "baseURL is required",
		},
		{
			name: "relative baseURL",
			content: `
title: Garden Notes
baseURL: /blog
`,
			wantErr: "baseURL must be an absolute http or https URL",
		},
		{
			name: "query in baseURL",
			content: `
title: Garden Notes
baseURL: https://example.com/blog?ref=1
`,
			wantErr: "baseURL must not include query or fragment",
		},
		{
			name: "username in baseURL",
			content: `
title: Garden Notes
baseURL: https://alice@example.com/blog
`,
			wantErr: "baseURL must not include user info",
		},
		{
			name: "username and password in baseURL",
			content: `
title: Garden Notes
baseURL: https://alice:secret@example.com/blog
`,
			wantErr: "baseURL must not include user info",
		},
	}

	for _, tt := range testCases {
		caseData := tt
		t.Run(caseData.name, func(t *testing.T) {
			t.Parallel()

			configPath := writeConfigFile(t, caseData.content)
			_, err := LoadForBuild(configPath, Overrides{})
			if err == nil {
				t.Fatal("LoadForBuild() error = nil, want non-nil")
			}
			if !strings.Contains(err.Error(), caseData.wantErr) {
				t.Fatalf("LoadForBuild() error = %q, want substring %q", err.Error(), caseData.wantErr)
			}
		})
	}
}

func TestLoadRejectsUnknownYAMLFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{
			name: "top level typo",
			content: `
title: Garden Notes
baseURL: https://example.com
defaultPublsh: false
`,
			wantErr: "field defaultPublsh not found",
		},
		{
			name: "nested typo",
			content: `
title: Garden Notes
baseURL: https://example.com
search:
  enabledd: true
`,
			wantErr: "field enabledd not found",
		},
	}

	for _, tt := range tests {
		caseData := tt
		t.Run(caseData.name, func(t *testing.T) {
			t.Parallel()

			configPath := writeConfigFile(t, caseData.content)
			_, err := LoadForBuild(configPath, Overrides{})
			if err == nil {
				t.Fatal("LoadForBuild() error = nil, want non-nil")
			}
			if !strings.Contains(err.Error(), caseData.wantErr) {
				t.Fatalf("LoadForBuild() error = %q, want substring %q", err.Error(), caseData.wantErr)
			}
		})
	}
}

func TestLoadRejectsInvalidExtendedValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{
			name: "pagination page size must be positive",
			content: `
title: Garden Notes
baseURL: https://example.com
pagination:
  pageSize: 0
`,
			wantErr: "pagination.pageSize must be greater than 0",
		},
		{
			name: "related count must be positive",
			content: `
title: Garden Notes
baseURL: https://example.com
related:
  count: -1
`,
			wantErr: "related.count must be greater than 0",
		},
	}

	for _, tt := range tests {
		caseData := tt
		t.Run(caseData.name, func(t *testing.T) {
			t.Parallel()

			configPath := writeConfigFile(t, caseData.content)
			_, err := LoadForBuild(configPath, Overrides{})
			if err == nil {
				t.Fatal("LoadForBuild() error = nil, want non-nil")
			}
			if !strings.Contains(err.Error(), caseData.wantErr) {
				t.Fatalf("LoadForBuild() error = %q, want substring %q", err.Error(), caseData.wantErr)
			}
		})
	}
}

func TestLoadAppliesExplicitOverrides(t *testing.T) {
	t.Parallel()

	configPath := writeConfigFile(t, `
title: File Title
baseURL: https://file.example.com/wiki
author: File Author
description: File description
language: fr
defaultPublish: false
`)

	loaded, err := LoadForBuild(configPath, Overrides{
		Title:          "CLI Title",
		BaseURL:        "https://cli.example.com/docs",
		Author:         "CLI Author",
		DefaultPublish: boolPtr(true),
	})
	if err != nil {
		t.Fatalf("LoadForBuild() error = %v", err)
	}
	cfg := loaded.Config

	if cfg.Title != "CLI Title" {
		t.Fatalf("Title = %q, want %q", cfg.Title, "CLI Title")
	}
	if cfg.BaseURL != "https://cli.example.com/docs/" {
		t.Fatalf("BaseURL = %q, want %q", cfg.BaseURL, "https://cli.example.com/docs/")
	}
	if cfg.Author != "CLI Author" {
		t.Fatalf("Author = %q, want %q", cfg.Author, "CLI Author")
	}
	if cfg.Description != "File description" {
		t.Fatalf("Description = %q, want %q", cfg.Description, "File description")
	}
	if cfg.Language != "fr" {
		t.Fatalf("Language = %q, want %q", cfg.Language, "fr")
	}
	if !cfg.DefaultPublish {
		t.Fatal("DefaultPublish = false, want true")
	}
}

func TestLoadAppliesDefaults(t *testing.T) {
	t.Parallel()

	loaded, err := LoadForBuild("", Overrides{
		Title:   "Garden Notes",
		BaseURL: "https://example.com",
	})
	if err != nil {
		t.Fatalf("LoadForBuild() error = %v", err)
	}
	cfg := loaded.Config

	if cfg.BaseURL != "https://example.com/" {
		t.Fatalf("BaseURL = %q, want %q", cfg.BaseURL, "https://example.com/")
	}
	if cfg.Language != defaultLanguage {
		t.Fatalf("Language = %q, want %q", cfg.Language, defaultLanguage)
	}
	if !cfg.DefaultPublish {
		t.Fatal("DefaultPublish = false, want true")
	}
	if cfg.Search.PagefindPath != defaultPagefindPath {
		t.Fatalf("Search.PagefindPath = %q, want %q", cfg.Search.PagefindPath, defaultPagefindPath)
	}
	if cfg.Search.PagefindVersion != defaultPagefindVersion {
		t.Fatalf("Search.PagefindVersion = %q, want %q", cfg.Search.PagefindVersion, defaultPagefindVersion)
	}
	if cfg.Pagination.PageSize != defaultPaginationPageSize {
		t.Fatalf("Pagination.PageSize = %d, want %d", cfg.Pagination.PageSize, defaultPaginationPageSize)
	}
	if cfg.Related.Count != defaultRelatedCount {
		t.Fatalf("Related.Count = %d, want %d", cfg.Related.Count, defaultRelatedCount)
	}
	if !cfg.RSS.Enabled {
		t.Fatal("RSS.Enabled = false, want true")
	}
	if cfg.Timeline.Enabled {
		t.Fatal("Timeline.Enabled = true, want false")
	}
	if cfg.Timeline.AsHomepage {
		t.Fatal("Timeline.AsHomepage = true, want false")
	}
	if cfg.Timeline.Path != defaultTimelinePath {
		t.Fatalf("Timeline.Path = %q, want %q", cfg.Timeline.Path, defaultTimelinePath)
	}
	if cfg.TemplateDir != "" {
		t.Fatalf("TemplateDir = %q, want empty string", cfg.TemplateDir)
	}
	if cfg.CustomCSS != "" {
		t.Fatalf("CustomCSS = %q, want empty string", cfg.CustomCSS)
	}
	if cfg.KaTeXCSSURL != defaultKaTeXCSSURL {
		t.Fatalf("KaTeXCSSURL = %q, want %q", cfg.KaTeXCSSURL, defaultKaTeXCSSURL)
	}
	if cfg.KaTeXJSURL != defaultKaTeXJSURL {
		t.Fatalf("KaTeXJSURL = %q, want %q", cfg.KaTeXJSURL, defaultKaTeXJSURL)
	}
	if cfg.KaTeXAutoRenderURL != defaultKaTeXAutoRenderURL {
		t.Fatalf("KaTeXAutoRenderURL = %q, want %q", cfg.KaTeXAutoRenderURL, defaultKaTeXAutoRenderURL)
	}
	if cfg.MermaidJSURL != defaultMermaidJSURL {
		t.Fatalf("MermaidJSURL = %q, want %q", cfg.MermaidJSURL, defaultMermaidJSURL)
	}
}

func TestLoadAutoDetectsCustomCSSInVaultRoot(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	configDir := t.TempDir()
	configPath := writeConfigFileAt(t, configDir, `
title: Garden Notes
baseURL: https://example.com
`)
	customCSSPath := filepath.Join(vaultPath, defaultCustomCSSName)
	if err := os.WriteFile(customCSSPath, []byte("body { color: rebeccapurple; }\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", customCSSPath, err)
	}

	loaded, err := LoadForBuild(configPath, Overrides{VaultPath: vaultPath})
	if err != nil {
		t.Fatalf("LoadForBuild() error = %v", err)
	}
	cfg := loaded.Config
	if cfg.CustomCSS != customCSSPath {
		t.Fatalf("CustomCSS = %q, want %q", cfg.CustomCSS, customCSSPath)
	}
	if !loaded.AllowMissingCustomCSS {
		t.Fatal("AllowMissingCustomCSS = false, want true for auto-detected vault custom.css")
	}
}

func TestLoadAutoDetectsCustomCSSFromConfigDirWithoutVaultPath(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	configPath := writeConfigFileAt(t, configDir, `
title: Garden Notes
baseURL: https://example.com
`)
	customCSSPath := filepath.Join(configDir, defaultCustomCSSName)
	if err := os.WriteFile(customCSSPath, []byte("body { color: rebeccapurple; }\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", customCSSPath, err)
	}

	loaded, err := LoadForBuild(configPath, Overrides{})
	if err != nil {
		t.Fatalf("LoadForBuild() error = %v", err)
	}
	cfg := loaded.Config
	if cfg.CustomCSS != customCSSPath {
		t.Fatalf("CustomCSS = %q, want %q", cfg.CustomCSS, customCSSPath)
	}
	if !loaded.AllowMissingCustomCSS {
		t.Fatal("AllowMissingCustomCSS = false, want true for auto-detected config-dir custom.css")
	}
}

func TestLoadPrefersConfigDirCustomCSSOverVaultRoot(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	configDir := t.TempDir()
	configPath := writeConfigFileAt(t, configDir, `
title: Garden Notes
baseURL: https://example.com
`)
	configCustomCSSPath := filepath.Join(configDir, defaultCustomCSSName)
	vaultCustomCSSPath := filepath.Join(vaultPath, defaultCustomCSSName)
	if err := os.WriteFile(configCustomCSSPath, []byte("body { color: tomato; }\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", configCustomCSSPath, err)
	}
	if err := os.WriteFile(vaultCustomCSSPath, []byte("body { color: royalblue; }\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", vaultCustomCSSPath, err)
	}

	loaded, err := LoadForBuild(configPath, Overrides{VaultPath: vaultPath})
	if err != nil {
		t.Fatalf("LoadForBuild() error = %v", err)
	}
	cfg := loaded.Config
	if cfg.CustomCSS != configCustomCSSPath {
		t.Fatalf("CustomCSS = %q, want %q", cfg.CustomCSS, configCustomCSSPath)
	}
	if !loaded.AllowMissingCustomCSS {
		t.Fatal("AllowMissingCustomCSS = false, want true for auto-detected config-dir custom.css")
	}
}

func TestLoadRejectsInvalidAutoDetectedCustomCSSInConfigDir(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	configDir := t.TempDir()
	configPath := writeConfigFileAt(t, configDir, `
title: Garden Notes
baseURL: https://example.com
`)
	configCustomCSSPath := filepath.Join(configDir, defaultCustomCSSName)
	vaultCustomCSSPath := filepath.Join(vaultPath, defaultCustomCSSName)
	symlinkTargetPath := filepath.Join(t.TempDir(), "linked.css")

	if err := os.WriteFile(symlinkTargetPath, []byte("body { color: tomato; }\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", symlinkTargetPath, err)
	}
	if err := os.Symlink(symlinkTargetPath, configCustomCSSPath); err != nil {
		t.Skipf("os.Symlink(%q, %q) error = %v", symlinkTargetPath, configCustomCSSPath, err)
	}
	if err := os.WriteFile(vaultCustomCSSPath, []byte("body { color: royalblue; }\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", vaultCustomCSSPath, err)
	}

	_, err := LoadForBuild(configPath, Overrides{VaultPath: vaultPath})
	if err == nil {
		t.Fatal("LoadForBuild() error = nil, want explicit invalid auto-detected custom CSS error")
	}
	if !strings.Contains(err.Error(), `auto-detected custom CSS "`+configCustomCSSPath+`" must be a regular non-symlink file`) {
		t.Fatalf("LoadForBuild() error = %q, want invalid config-dir custom CSS path error", err.Error())
	}
}

func TestLoadRejectsInvalidAutoDetectedCustomCSSInVaultRoot(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	configDir := t.TempDir()
	configPath := writeConfigFileAt(t, configDir, `
title: Garden Notes
baseURL: https://example.com
`)
	vaultCustomCSSPath := filepath.Join(vaultPath, defaultCustomCSSName)
	if err := os.Mkdir(vaultCustomCSSPath, 0o755); err != nil {
		t.Fatalf("os.Mkdir(%q) error = %v", vaultCustomCSSPath, err)
	}

	_, err := LoadForBuild(configPath, Overrides{VaultPath: vaultPath})
	if err == nil {
		t.Fatal("LoadForBuild() error = nil, want explicit invalid auto-detected custom CSS error")
	}
	if !strings.Contains(err.Error(), `auto-detected custom CSS "`+vaultCustomCSSPath+`" must be a regular non-symlink file`) {
		t.Fatalf("LoadForBuild() error = %q, want invalid vault-root custom CSS path error", err.Error())
	}
}

func TestLoadResolvesRelativePagefindPathAgainstConfigDir(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	configPath := writeConfigFileAt(t, configDir, `
title: Garden Notes
baseURL: https://example.com
search:
  enabled: true
  pagefindPath: tools/pagefind_extended
`)

	loaded, err := LoadForBuild(configPath, Overrides{})
	if err != nil {
		t.Fatalf("LoadForBuild() error = %v", err)
	}
	cfg := loaded.Config
	if cfg.Search.PagefindPath != filepath.Join(configDir, "tools", "pagefind_extended") {
		t.Fatalf("Search.PagefindPath = %q, want %q", cfg.Search.PagefindPath, filepath.Join(configDir, "tools", "pagefind_extended"))
	}
}

func TestNormalizeSiteConfigAppliesDocumentedDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := NormalizeSiteConfig(model.SiteConfig{
		Title:   "Garden Notes",
		BaseURL: "https://example.com/blog",
	})
	if err != nil {
		t.Fatalf("NormalizeSiteConfig() error = %v", err)
	}

	if cfg.BaseURL != "https://example.com/blog/" {
		t.Fatalf("BaseURL = %q, want %q", cfg.BaseURL, "https://example.com/blog/")
	}
	if cfg.Language != defaultLanguage {
		t.Fatalf("Language = %q, want %q", cfg.Language, defaultLanguage)
	}
	if !cfg.DefaultPublish {
		t.Fatal("DefaultPublish = false, want documented default true")
	}
	if cfg.Search.PagefindPath != defaultPagefindPath {
		t.Fatalf("Search.PagefindPath = %q, want %q", cfg.Search.PagefindPath, defaultPagefindPath)
	}
	if cfg.Search.PagefindVersion != defaultPagefindVersion {
		t.Fatalf("Search.PagefindVersion = %q, want %q", cfg.Search.PagefindVersion, defaultPagefindVersion)
	}
	if cfg.Pagination.PageSize != defaultPaginationPageSize {
		t.Fatalf("Pagination.PageSize = %d, want %d", cfg.Pagination.PageSize, defaultPaginationPageSize)
	}
	if cfg.Related.Count != defaultRelatedCount {
		t.Fatalf("Related.Count = %d, want %d", cfg.Related.Count, defaultRelatedCount)
	}
	if !cfg.RSS.Enabled {
		t.Fatal("RSS.Enabled = false, want documented default true")
	}
	if cfg.Timeline.Path != defaultTimelinePath {
		t.Fatalf("Timeline.Path = %q, want %q", cfg.Timeline.Path, defaultTimelinePath)
	}
	if cfg.KaTeXCSSURL != defaultKaTeXCSSURL {
		t.Fatalf("KaTeXCSSURL = %q, want %q", cfg.KaTeXCSSURL, defaultKaTeXCSSURL)
	}
	if cfg.KaTeXJSURL != defaultKaTeXJSURL {
		t.Fatalf("KaTeXJSURL = %q, want %q", cfg.KaTeXJSURL, defaultKaTeXJSURL)
	}
	if cfg.KaTeXAutoRenderURL != defaultKaTeXAutoRenderURL {
		t.Fatalf("KaTeXAutoRenderURL = %q, want %q", cfg.KaTeXAutoRenderURL, defaultKaTeXAutoRenderURL)
	}
	if cfg.MermaidJSURL != defaultMermaidJSURL {
		t.Fatalf("MermaidJSURL = %q, want %q", cfg.MermaidJSURL, defaultMermaidJSURL)
	}
}

func TestNormalizeSiteConfigPreservesExplicitDefaultPoliciesWhenMarkedSet(t *testing.T) {
	t.Parallel()

	cfg, err := NormalizeSiteConfig(model.SiteConfig{
		Title:             "Garden Notes",
		BaseURL:           "https://example.com/blog",
		DefaultPublish:    false,
		DefaultPublishSet: true,
		RSS: model.RSSConfig{
			Enabled:    false,
			EnabledSet: true,
		},
	})
	if err != nil {
		t.Fatalf("NormalizeSiteConfig() error = %v", err)
	}
	if cfg.DefaultPublish {
		t.Fatal("DefaultPublish = true, want explicit false to be preserved")
	}
	if cfg.RSS.Enabled {
		t.Fatal("RSS.Enabled = true, want explicit false to be preserved")
	}
}

func TestValidateLoadedSiteConfigPreservesExplicitDefaultPublishFalse(t *testing.T) {
	t.Parallel()

	cfg, err := ValidateLoadedSiteConfig(model.SiteConfig{
		Title:             "Garden Notes",
		BaseURL:           "https://example.com/blog",
		DefaultPublish:    false,
		DefaultPublishSet: true,
	})
	if err != nil {
		t.Fatalf("ValidateLoadedSiteConfig() error = %v", err)
	}
	if cfg.DefaultPublish {
		t.Fatal("DefaultPublish = true, want explicit false to be preserved")
	}
}

func TestValidateLoadedSiteConfigPreservesExplicitRSSFalse(t *testing.T) {
	t.Parallel()

	cfg, err := ValidateLoadedSiteConfig(model.SiteConfig{
		Title:   "Garden Notes",
		BaseURL: "https://example.com/blog",
		RSS: model.RSSConfig{
			Enabled:    false,
			EnabledSet: true,
		},
	})
	if err != nil {
		t.Fatalf("ValidateLoadedSiteConfig() error = %v", err)
	}
	if cfg.RSS.Enabled {
		t.Fatal("RSS.Enabled = true, want explicit false to be preserved")
	}
}

func TestNormalizeSiteConfigRejectsTimelinePathTraversal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		timelinePath string
	}{
		{name: "slash separated", timelinePath: "../notes"},
		{name: "backslash separated", timelinePath: `..\notes`},
		{name: "mixed separators", timelinePath: `..\/notes`},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := NormalizeSiteConfig(model.SiteConfig{
				Title:   "Garden Notes",
				BaseURL: "https://example.com/blog",
				Timeline: model.TimelineConfig{
					Path: tt.timelinePath,
				},
			})
			if err == nil {
				t.Fatal("NormalizeSiteConfig() error = nil, want non-nil")
			}
			if !strings.Contains(err.Error(), "timeline.path") {
				t.Fatalf("NormalizeSiteConfig() error = %q, want timeline.path validation", err.Error())
			}
		})
	}
}

func TestNormalizeSiteConfigRejectsNonSiteRelativeTimelinePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		timelinePath string
	}{
		{name: "windows drive", timelinePath: `C:\notes`},
		{name: "unc path", timelinePath: `\\server\share`},
		{name: "query", timelinePath: "notes?draft=1"},
		{name: "fragment", timelinePath: "notes#archive"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := NormalizeSiteConfig(model.SiteConfig{
				Title:   "Garden Notes",
				BaseURL: "https://example.com/blog",
				Timeline: model.TimelineConfig{
					Path: tt.timelinePath,
				},
			})
			if err == nil {
				t.Fatal("NormalizeSiteConfig() error = nil, want non-nil")
			}
			if !strings.Contains(err.Error(), "timeline.path") {
				t.Fatalf("NormalizeSiteConfig() error = %q, want timeline.path validation", err.Error())
			}
		})
	}
}

func TestNormalizeSiteConfigNormalizesTimelinePathSeparators(t *testing.T) {
	t.Parallel()

	cfg, err := NormalizeSiteConfig(model.SiteConfig{
		Title:   "Garden Notes",
		BaseURL: "https://example.com/blog",
		Timeline: model.TimelineConfig{
			Path: `journal\2026`,
		},
	})
	if err != nil {
		t.Fatalf("NormalizeSiteConfig() error = %v", err)
	}
	if cfg.Timeline.Path != "journal/2026" {
		t.Fatalf("Timeline.Path = %q, want %q", cfg.Timeline.Path, "journal/2026")
	}
}

func writeConfigFile(t *testing.T, content string) string {
	t.Helper()

	return writeConfigFileAt(t, t.TempDir(), content)
}

func writeConfigFileAt(t *testing.T, dir string, content string) string {
	t.Helper()

	configPath := filepath.Join(dir, "obsite.yaml")
	if err := os.WriteFile(configPath, []byte(strings.TrimLeft(content, "\n")), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", configPath, err)
	}

	return configPath
}

func boolPtr(value bool) *bool {
	return &value
}
