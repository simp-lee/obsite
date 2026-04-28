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
	themeRoot := filepath.Join(configDir, "themes", "feature")
	writeRequiredThemeTemplates(t, themeRoot)
	configPath := writeConfigFileAt(t, configDir, strings.Join([]string{
		"title: Garden Notes",
		"baseURL: https://example.com/blog",
		"author: Alice",
		"description: Public notes",
		"language: fr",
		"defaultImg: images/og.png",
		"defaultPublish: false",
		"themes:",
		"  feature:",
		"    root: themes/feature",
		"defaultTheme: feature",
		"search:",
		"  enabled: true",
		"  pagefindPath: tools/pagefind_extended",
		"  pagefindVersion: 1.5.2",
		"pagination:",
		"  pageSize: 30",
		"sidebar:",
		"  enabled: true",
		"popover:",
		"  enabled: true",
		"related:",
		"  enabled: true",
		"  count: 7",
		"rss:",
		"  enabled: false",
		"timeline:",
		"  enabled: true",
		"  asHomepage: true",
		"  path: timeline",
	}, "\n"))

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
	themeCfg, ok := cfg.Themes["feature"]
	if !ok {
		t.Fatalf("Themes[feature] missing from %#v", cfg.Themes)
	}
	if themeCfg.Root != themeRoot {
		t.Fatalf("Themes[feature].Root = %q, want %q", themeCfg.Root, themeRoot)
	}
	if cfg.DefaultTheme != "feature" {
		t.Fatalf("DefaultTheme = %q, want %q", cfg.DefaultTheme, "feature")
	}
	if cfg.ActiveThemeName != "feature" {
		t.Fatalf("ActiveThemeName = %q, want %q", cfg.ActiveThemeName, "feature")
	}
	if cfg.ThemeRoot != themeRoot {
		t.Fatalf("ThemeRoot = %q, want %q", cfg.ThemeRoot, themeRoot)
	}
	if cfg.CustomCSS != "" {
		t.Fatalf("CustomCSS = %q, want empty string", cfg.CustomCSS)
	}
	if !cfg.Search.Enabled {
		t.Fatal("Search.Enabled = false, want true")
	}
	if cfg.Search.PagefindPath != filepath.Join(configDir, "tools", "pagefind_extended") {
		t.Fatalf("Search.PagefindPath = %q, want %q", cfg.Search.PagefindPath, filepath.Join(configDir, "tools", "pagefind_extended"))
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

func TestLoadRejectsLegacyTemplateFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{
			name: "templateDir",
			content: `
title: Garden Notes
baseURL: https://example.com
templateDir: themes/feature
`,
			wantErr: "templateDir is no longer supported",
		},
		{
			name: "customCSS",
			content: `
title: Garden Notes
baseURL: https://example.com
customCSS: styles/site.css
`,
			wantErr: "customCSS is no longer supported",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			configPath := writeConfigFile(t, tt.content)
			_, err := LoadForBuild(configPath, Overrides{})
			if err == nil {
				t.Fatal("LoadForBuild() error = nil, want non-nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("LoadForBuild() error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
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

func TestLoadRejectsInvalidThemeDeclarations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{
			name: "blank theme name",
			content: `
title: Garden Notes
baseURL: https://example.com
themes:
  "   ":
    root: themes/blank
`,
			wantErr: "themes contains an empty theme name",
		},
		{
			name: "duplicate theme name",
			content: `
title: Garden Notes
baseURL: https://example.com
themes:
  feature:
    root: themes/one
  feature:
    root: themes/two
`,
			wantErr: `themes contains duplicate theme name "feature"`,
		},
		{
			name: "blank theme root",
			content: `
title: Garden Notes
baseURL: https://example.com
themes:
  feature:
    root: "   "
defaultTheme: feature
`,
			wantErr: "themes.feature.root must not be empty",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			configPath := writeConfigFile(t, tt.content)
			_, err := LoadForBuild(configPath, Overrides{})
			if err == nil {
				t.Fatal("LoadForBuild() error = nil, want non-nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("LoadForBuild() error = %q, want substring %q", err.Error(), tt.wantErr)
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

func TestLoadAppliesThemeSelectionPriority(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	featureRoot := filepath.Join(configDir, "themes", "feature")
	serifRoot := filepath.Join(configDir, "themes", "serif")
	writeRequiredThemeTemplates(t, featureRoot)
	writeRequiredThemeTemplates(t, serifRoot)

	configPath := writeConfigFileAt(t, configDir, `
title: Garden Notes
baseURL: https://example.com
themes:
  feature:
    root: themes/feature
  serif:
    root: themes/serif
defaultTheme: feature
`)

	tests := []struct {
		name            string
		overrides       Overrides
		wantThemeName   string
		wantThemeRoot   string
		wantDefaultName string
	}{
		{
			name:            "override beats defaultTheme",
			overrides:       Overrides{Theme: "serif"},
			wantThemeName:   "serif",
			wantThemeRoot:   serifRoot,
			wantDefaultName: "feature",
		},
		{
			name:            "defaultTheme selected when no override",
			overrides:       Overrides{},
			wantThemeName:   "feature",
			wantThemeRoot:   featureRoot,
			wantDefaultName: "feature",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			loaded, err := LoadForBuild(configPath, tt.overrides)
			if err != nil {
				t.Fatalf("LoadForBuild() error = %v", err)
			}
			cfg := loaded.Config
			if cfg.DefaultTheme != tt.wantDefaultName {
				t.Fatalf("DefaultTheme = %q, want %q", cfg.DefaultTheme, tt.wantDefaultName)
			}
			if cfg.ActiveThemeName != tt.wantThemeName {
				t.Fatalf("ActiveThemeName = %q, want %q", cfg.ActiveThemeName, tt.wantThemeName)
			}
			if cfg.ThemeRoot != tt.wantThemeRoot {
				t.Fatalf("ThemeRoot = %q, want %q", cfg.ThemeRoot, tt.wantThemeRoot)
			}
		})
	}
}

func TestLoadResolvesRelativeThemeRootsAgainstObsiteYAMLAndPreservesAbsoluteRoots(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	relativeRoot := filepath.Join(configDir, "themes", "feature")
	absoluteRoot := filepath.Join(t.TempDir(), "themes", "serif")
	writeRequiredThemeTemplates(t, relativeRoot)
	writeRequiredThemeTemplates(t, absoluteRoot)

	configPath := writeConfigFileAt(t, configDir, strings.Join([]string{
		"title: Garden Notes",
		"baseURL: https://example.com",
		"themes:",
		"  feature:",
		"    root: themes/feature",
		"  serif:",
		"    root: " + absoluteRoot,
		"defaultTheme: feature",
	}, "\n"))

	loaded, err := LoadForBuild(configPath, Overrides{})
	if err != nil {
		t.Fatalf("LoadForBuild() error = %v", err)
	}
	if got := loaded.Config.Themes["feature"].Root; got != relativeRoot {
		t.Fatalf("Themes[feature].Root = %q, want %q", got, relativeRoot)
	}
	if got := loaded.Config.Themes["serif"].Root; got != absoluteRoot {
		t.Fatalf("Themes[serif].Root = %q, want %q", got, absoluteRoot)
	}
	if loaded.Config.ThemeRoot != relativeRoot {
		t.Fatalf("ThemeRoot = %q, want %q", loaded.Config.ThemeRoot, relativeRoot)
	}
}

func TestLoadFallsBackToEmbeddedDefaultThemeWhenNoThemeIsSelected(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	featureRoot := filepath.Join(configDir, "themes", "feature")
	writeRequiredThemeTemplates(t, featureRoot)

	configPath := writeConfigFileAt(t, configDir, `
title: Garden Notes
baseURL: https://example.com
themes:
  feature:
    root: themes/feature
`)

	loaded, err := LoadForBuild(configPath, Overrides{})
	if err != nil {
		t.Fatalf("LoadForBuild() error = %v", err)
	}
	if loaded.Config.ActiveThemeName != "" {
		t.Fatalf("ActiveThemeName = %q, want empty string for embedded default theme", loaded.Config.ActiveThemeName)
	}
	if loaded.Config.ThemeRoot != "" {
		t.Fatalf("ThemeRoot = %q, want empty string for embedded default theme", loaded.Config.ThemeRoot)
	}
	if got := loaded.Config.Themes["feature"].Root; got != featureRoot {
		t.Fatalf("Themes[feature].Root = %q, want %q", got, featureRoot)
	}
}

func TestLoadRejectsUnknownSelectedTheme(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	featureRoot := filepath.Join(configDir, "themes", "feature")
	writeRequiredThemeTemplates(t, featureRoot)

	tests := []struct {
		name      string
		content   string
		overrides Overrides
		wantErr   string
	}{
		{
			name: "defaultTheme missing from map",
			content: `
title: Garden Notes
baseURL: https://example.com
themes:
  feature:
    root: themes/feature
defaultTheme: missing
`,
			wantErr: `defaultTheme "missing" was not found in themes`,
		},
		{
			name: "override theme missing from map",
			content: `
title: Garden Notes
baseURL: https://example.com
themes:
  feature:
    root: themes/feature
`,
			overrides: Overrides{Theme: "missing"},
			wantErr:   `theme "missing" was not found in themes`,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			configPath := writeConfigFileAt(t, configDir, tt.content)
			_, err := LoadForBuild(configPath, tt.overrides)
			if err == nil {
				t.Fatal("LoadForBuild() error = nil, want non-nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("LoadForBuild() error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestLoadRejectsSelectedThemeRootProblems(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		prepareRoot  func(t *testing.T, root string)
		wantErrParts []string
	}{
		{
			name: "missing theme root directory",
			prepareRoot: func(t *testing.T, root string) {
				t.Helper()
			},
			wantErrParts: []string{"selected theme \"feature\" root", "does not exist"},
		},
		{
			name: "theme root must be directory",
			prepareRoot: func(t *testing.T, root string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(root), 0o755); err != nil {
					t.Fatalf("os.MkdirAll(%q) error = %v", filepath.Dir(root), err)
				}
				if err := os.WriteFile(root, []byte("not a directory\n"), 0o644); err != nil {
					t.Fatalf("os.WriteFile(%q) error = %v", root, err)
				}
			},
			wantErrParts: []string{"selected theme \"feature\" root", "is not a directory"},
		},
		{
			name: "missing required templates",
			prepareRoot: func(t *testing.T, root string) {
				t.Helper()
				writeRequiredThemeTemplatesExcept(t, root, "tag.html", "timeline.html")
			},
			wantErrParts: []string{"missing required HTML templates", "tag.html", "timeline.html"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			configDir := t.TempDir()
			themeRoot := filepath.Join(configDir, "themes", "feature")
			tt.prepareRoot(t, themeRoot)
			configPath := writeConfigFileAt(t, configDir, `
title: Garden Notes
baseURL: https://example.com
themes:
  feature:
    root: themes/feature
defaultTheme: feature
`)

			_, err := LoadForBuild(configPath, Overrides{})
			if err == nil {
				t.Fatal("LoadForBuild() error = nil, want non-nil")
			}
			for _, wantErrPart := range tt.wantErrParts {
				if !strings.Contains(err.Error(), wantErrPart) {
					t.Fatalf("LoadForBuild() error = %q, want substring %q", err.Error(), wantErrPart)
				}
			}
		})
	}
}

func TestLoadRejectsUnselectedMissingThemeRootWhenAnotherThemeIsSelected(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	featureRoot := filepath.Join(configDir, "themes", "feature")
	writeRequiredThemeTemplates(t, featureRoot)

	configPath := writeConfigFileAt(t, configDir, `
title: Garden Notes
baseURL: https://example.com
themes:
  feature:
    root: themes/feature
  broken:
    root: themes/broken
`)

	_, err := LoadForBuild(configPath, Overrides{Theme: "feature"})
	if err == nil {
		t.Fatal("LoadForBuild() error = nil, want unselected missing theme root failure")
	}
	for _, wantErrPart := range []string{"theme \"broken\" root", "does not exist"} {
		if !strings.Contains(err.Error(), wantErrPart) {
			t.Fatalf("LoadForBuild() error = %q, want substring %q", err.Error(), wantErrPart)
		}
	}
	if strings.Contains(err.Error(), "selected theme \"feature\" root") {
		t.Fatalf("LoadForBuild() error = %q, want unselected theme root failure before selected theme contract validation", err.Error())
	}
}

func TestLoadRejectsUnselectedNonDirectoryThemeRootWhenFallingBackToEmbeddedTheme(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	featureRoot := filepath.Join(configDir, "themes", "feature")
	if err := os.MkdirAll(featureRoot, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v", featureRoot, err)
	}
	brokenRoot := filepath.Join(configDir, "themes", "broken")
	if err := os.MkdirAll(filepath.Dir(brokenRoot), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v", filepath.Dir(brokenRoot), err)
	}
	if err := os.WriteFile(brokenRoot, []byte("not a directory\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", brokenRoot, err)
	}

	configPath := writeConfigFileAt(t, configDir, `
title: Garden Notes
baseURL: https://example.com
themes:
  feature:
    root: themes/feature
  broken:
    root: themes/broken
`)

	_, err := LoadForBuild(configPath, Overrides{})
	if err == nil {
		t.Fatal("LoadForBuild() error = nil, want unselected non-directory theme root failure")
	}
	for _, wantErrPart := range []string{"theme \"broken\" root", "is not a directory"} {
		if !strings.Contains(err.Error(), wantErrPart) {
			t.Fatalf("LoadForBuild() error = %q, want substring %q", err.Error(), wantErrPart)
		}
	}
	if strings.Contains(err.Error(), "selected theme") {
		t.Fatalf("LoadForBuild() error = %q, want failure before any selected-theme-only validation because embedded theme stays active", err.Error())
	}
}

func TestLoadRejectsSymlinkedThemeOwnedFiles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		prepareRoot  func(t *testing.T, root string)
		wantErrParts []string
	}{
		{
			name: "required template symlink",
			prepareRoot: func(t *testing.T, root string) {
				t.Helper()

				writeRequiredThemeTemplatesExcept(t, root, "tag.html")
				targetPath := filepath.Join(t.TempDir(), "tag.html")
				if err := os.WriteFile(targetPath, []byte("{{define \"content-tag\"}}tag{{end}}\n"), 0o644); err != nil {
					t.Fatalf("os.WriteFile(%q) error = %v", targetPath, err)
				}
				if err := os.Symlink(targetPath, filepath.Join(root, "tag.html")); err != nil {
					t.Skipf("os.Symlink(%q, %q) unsupported: %v", targetPath, filepath.Join(root, "tag.html"), err)
				}
			},
			wantErrParts: []string{"selected theme \"feature\" root", "tag.html", "regular non-symlink file"},
		},
		{
			name: "theme style symlink",
			prepareRoot: func(t *testing.T, root string) {
				t.Helper()

				writeRequiredThemeTemplates(t, root)
				targetPath := filepath.Join(t.TempDir(), "style.css")
				if err := os.WriteFile(targetPath, []byte("body { color: tomato; }\n"), 0o644); err != nil {
					t.Fatalf("os.WriteFile(%q) error = %v", targetPath, err)
				}
				if err := os.Symlink(targetPath, filepath.Join(root, "style.css")); err != nil {
					t.Skipf("os.Symlink(%q, %q) unsupported: %v", targetPath, filepath.Join(root, "style.css"), err)
				}
			},
			wantErrParts: []string{"selected theme \"feature\" root", "style.css", "regular non-symlink file"},
		},
		{
			name: "optional html partial symlink",
			prepareRoot: func(t *testing.T, root string) {
				t.Helper()

				writeRequiredThemeTemplates(t, root)
				targetPath := filepath.Join(t.TempDir(), "badge.html")
				if err := os.WriteFile(targetPath, []byte("{{define \"theme-badge\"}}badge{{end}}\n"), 0o644); err != nil {
					t.Fatalf("os.WriteFile(%q) error = %v", targetPath, err)
				}
				partialPath := filepath.Join(root, "partials", "badge.html")
				if err := os.MkdirAll(filepath.Dir(partialPath), 0o755); err != nil {
					t.Fatalf("os.MkdirAll(%q) error = %v", filepath.Dir(partialPath), err)
				}
				if err := os.Symlink(targetPath, partialPath); err != nil {
					t.Skipf("os.Symlink(%q, %q) unsupported: %v", targetPath, partialPath, err)
				}
			},
			wantErrParts: []string{"selected theme \"feature\" root", "partials/badge.html", "regular non-symlink file"},
		},
		{
			name: "theme static asset symlink",
			prepareRoot: func(t *testing.T, root string) {
				t.Helper()

				writeRequiredThemeTemplates(t, root)
				targetPath := filepath.Join(t.TempDir(), "logo.svg")
				if err := os.WriteFile(targetPath, []byte("<svg></svg>\n"), 0o644); err != nil {
					t.Fatalf("os.WriteFile(%q) error = %v", targetPath, err)
				}
				assetPath := filepath.Join(root, "assets", "logo.svg")
				if err := os.MkdirAll(filepath.Dir(assetPath), 0o755); err != nil {
					t.Fatalf("os.MkdirAll(%q) error = %v", filepath.Dir(assetPath), err)
				}
				if err := os.Symlink(targetPath, assetPath); err != nil {
					t.Skipf("os.Symlink(%q, %q) unsupported: %v", targetPath, assetPath, err)
				}
			},
			wantErrParts: []string{"selected theme \"feature\" root", "assets/logo.svg", "regular non-symlink file"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			configDir := t.TempDir()
			themeRoot := filepath.Join(configDir, "themes", "feature")
			tt.prepareRoot(t, themeRoot)
			configPath := writeConfigFileAt(t, configDir, `
title: Garden Notes
baseURL: https://example.com
themes:
  feature:
    root: themes/feature
defaultTheme: feature
`)

			_, err := LoadForBuild(configPath, Overrides{})
			if err == nil {
				t.Fatal("LoadForBuild() error = nil, want non-nil")
			}
			for _, wantErrPart := range tt.wantErrParts {
				if !strings.Contains(err.Error(), wantErrPart) {
					t.Fatalf("LoadForBuild() error = %q, want substring %q", err.Error(), wantErrPart)
				}
			}
		})
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
	if cfg.CustomCSS != "" {
		t.Fatalf("CustomCSS = %q, want empty string", cfg.CustomCSS)
	}
	if cfg.DefaultTheme != "" {
		t.Fatalf("DefaultTheme = %q, want empty string", cfg.DefaultTheme)
	}
	if cfg.ActiveThemeName != "" {
		t.Fatalf("ActiveThemeName = %q, want empty string", cfg.ActiveThemeName)
	}
	if cfg.ThemeRoot != "" {
		t.Fatalf("ThemeRoot = %q, want empty string", cfg.ThemeRoot)
	}
	if len(cfg.Themes) != 0 {
		t.Fatalf("Themes = %#v, want empty map", cfg.Themes)
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
}

func TestLoadIgnoresConfigDirCustomCSSWithoutVaultCustomCSS(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	configDir := t.TempDir()
	configPath := writeConfigFileAt(t, configDir, `
title: Garden Notes
baseURL: https://example.com
`)
	customCSSPath := filepath.Join(configDir, defaultCustomCSSName)
	if err := os.WriteFile(customCSSPath, []byte("body { color: rebeccapurple; }\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", customCSSPath, err)
	}

	loaded, err := LoadForBuild(configPath, Overrides{VaultPath: vaultPath})
	if err != nil {
		t.Fatalf("LoadForBuild() error = %v", err)
	}
	if loaded.Config.CustomCSS != "" {
		t.Fatalf("CustomCSS = %q, want empty string when only config-dir custom.css exists", loaded.Config.CustomCSS)
	}
}

func TestLoadIgnoresInvalidConfigDirCustomCSSWhenVaultRootHasCustomCSS(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	configDir := t.TempDir()
	configPath := writeConfigFileAt(t, configDir, `
title: Garden Notes
baseURL: https://example.com
`)
	configCustomCSSPath := filepath.Join(configDir, defaultCustomCSSName)
	vaultCustomCSSPath := filepath.Join(vaultPath, defaultCustomCSSName)
	if err := os.Mkdir(configCustomCSSPath, 0o755); err != nil {
		t.Fatalf("os.Mkdir(%q) error = %v", configCustomCSSPath, err)
	}
	if err := os.WriteFile(vaultCustomCSSPath, []byte("body { color: royalblue; }\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", vaultCustomCSSPath, err)
	}

	loaded, err := LoadForBuild(configPath, Overrides{VaultPath: vaultPath})
	if err != nil {
		t.Fatalf("LoadForBuild() error = %v", err)
	}
	cfg := loaded.Config
	if cfg.CustomCSS != vaultCustomCSSPath {
		t.Fatalf("CustomCSS = %q, want %q", cfg.CustomCSS, vaultCustomCSSPath)
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

func writeRequiredThemeTemplates(t *testing.T, root string) {
	t.Helper()

	writeRequiredThemeTemplatesExcept(t, root)
}

func writeRequiredThemeTemplatesExcept(t *testing.T, root string, missing ...string) {
	t.Helper()

	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v", root, err)
	}

	missingSet := make(map[string]struct{}, len(missing))
	for _, name := range missing {
		missingSet[name] = struct{}{}
	}

	for _, name := range requiredThemeTemplateNames() {
		if _, skip := missingSet[name]; skip {
			continue
		}
		filePath := filepath.Join(root, filepath.FromSlash(name))
		if err := os.WriteFile(filePath, []byte(name+"\n"), 0o644); err != nil {
			t.Fatalf("os.WriteFile(%q) error = %v", filePath, err)
		}
	}
}
