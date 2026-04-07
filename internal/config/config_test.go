package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/simp-lee/obsite/internal/model"
)

func TestLoadParsesYAML(t *testing.T) {
	t.Parallel()

	configPath := writeConfigFile(t, `
title: Garden Notes
baseURL: https://example.com/blog
author: Alice
description: Public notes
defaultImg: images/og.png
defaultPublish: false
`)

	cfg, err := Load(configPath, Overrides{})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

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
	if cfg.DefaultImg != "images/og.png" {
		t.Fatalf("DefaultImg = %q, want %q", cfg.DefaultImg, "images/og.png")
	}
	if cfg.DefaultPublish {
		t.Fatal("DefaultPublish = true, want false")
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
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			configPath := writeConfigFile(t, tt.content)
			_, err := Load(configPath, Overrides{})
			if err == nil {
				t.Fatal("Load() error = nil, want non-nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Load() error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestLoadRejectsUnknownYAMLFields(t *testing.T) {
	t.Parallel()

	configPath := writeConfigFile(t, `
title: Garden Notes
baseURL: https://example.com
defaultPublsh: false
`)

	_, err := Load(configPath, Overrides{})
	if err == nil {
		t.Fatal("Load() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "field defaultPublsh not found") {
		t.Fatalf("Load() error = %q, want substring %q", err.Error(), "field defaultPublsh not found")
	}
}

func TestLoadAppliesCLIOverrides(t *testing.T) {
	t.Parallel()

	configPath := writeConfigFile(t, `
title: File Title
baseURL: https://file.example.com/wiki
author: File Author
description: File description
language: fr
defaultPublish: false
`)

	cfg, err := Load(configPath, Overrides{
		Title:          "CLI Title",
		BaseURL:        "https://cli.example.com/docs",
		Author:         "CLI Author",
		DefaultPublish: boolPtr(true),
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

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

	cfg, err := Load("", Overrides{
		Title:   "Garden Notes",
		BaseURL: "https://example.com",
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.BaseURL != "https://example.com/" {
		t.Fatalf("BaseURL = %q, want %q", cfg.BaseURL, "https://example.com/")
	}
	if cfg.Language != defaultLanguage {
		t.Fatalf("Language = %q, want %q", cfg.Language, defaultLanguage)
	}
	if !cfg.DefaultPublish {
		t.Fatal("DefaultPublish = false, want true")
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

func TestNormalizeSiteConfigAppliesRuntimeDefaultsForUnsetFields(t *testing.T) {
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
		t.Fatal("DefaultPublish = false, want true")
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

func TestNormalizeSiteConfigPreservesExplicitDefaultPublishFalse(t *testing.T) {
	t.Parallel()

	cfg, err := NormalizeSiteConfig(model.SiteConfig{
		Title:             "Garden Notes",
		BaseURL:           "https://example.com/blog",
		DefaultPublish:    false,
		DefaultPublishSet: true,
	})
	if err != nil {
		t.Fatalf("NormalizeSiteConfig() error = %v", err)
	}
	if cfg.DefaultPublish {
		t.Fatal("DefaultPublish = true, want explicit false to be preserved")
	}
}

func writeConfigFile(t *testing.T, content string) string {
	t.Helper()

	configPath := filepath.Join(t.TempDir(), "obsite.yaml")
	if err := os.WriteFile(configPath, []byte(strings.TrimLeft(content, "\n")), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", configPath, err)
	}

	return configPath
}

func boolPtr(value bool) *bool {
	return &value
}
