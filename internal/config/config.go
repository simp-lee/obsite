package config

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"path"
	"strings"

	"github.com/simp-lee/obsite/internal/model"
	"gopkg.in/yaml.v3"
)

const (
	defaultLanguage = "en"

	defaultKaTeXVersion   = "0.16.44"
	defaultMermaidVersion = "11.14.0"

	defaultKaTeXCSSURL        = "https://cdn.jsdelivr.net/npm/katex@" + defaultKaTeXVersion + "/dist/katex.min.css"
	defaultKaTeXJSURL         = "https://cdn.jsdelivr.net/npm/katex@" + defaultKaTeXVersion + "/dist/katex.min.js"
	defaultKaTeXAutoRenderURL = "https://cdn.jsdelivr.net/npm/katex@" + defaultKaTeXVersion + "/dist/contrib/auto-render.min.js"
	defaultMermaidJSURL       = "https://cdn.jsdelivr.net/npm/mermaid@" + defaultMermaidVersion + "/dist/mermaid.esm.min.mjs"
)

// Overrides carries CLI-provided values. Empty strings mean the flag was not provided.
type Overrides struct {
	Title          string
	BaseURL        string
	Author         string
	Description    string
	Language       string
	DefaultImg     string
	DefaultPublish *bool
}

type fileConfig struct {
	Title          string `yaml:"title"`
	BaseURL        string `yaml:"baseURL"`
	Author         string `yaml:"author"`
	Description    string `yaml:"description"`
	Language       string `yaml:"language"`
	DefaultImg     string `yaml:"defaultImg"`
	DefaultPublish *bool  `yaml:"defaultPublish"`
}

// Load reads obsite.yaml, applies CLI overrides, normalizes values, and validates the result.
func Load(path string, overrides Overrides) (model.SiteConfig, error) {
	cfg := defaultSiteConfig()

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return model.SiteConfig{}, fmt.Errorf("read config %q: %w", path, err)
		}

		parsed, err := parseFileConfig(data)
		if err != nil {
			return model.SiteConfig{}, fmt.Errorf("parse config %q: %w", path, err)
		}

		cfg = applyFileConfig(cfg, parsed)
	}

	cfg = applyOverrides(cfg, overrides)

	normalized, err := NormalizeSiteConfig(cfg)
	if err != nil {
		if path == "" {
			return model.SiteConfig{}, fmt.Errorf("validate config: %w", err)
		}

		return model.SiteConfig{}, fmt.Errorf("validate config %q: %w", path, err)
	}

	return normalized, nil
}

// NormalizeSiteConfig applies runtime defaults for omitted fields, then validates
// and normalizes the caller-provided site config.
func NormalizeSiteConfig(cfg model.SiteConfig) (model.SiteConfig, error) {
	cfg = applyRuntimeDefaults(cfg)
	if err := validate(&cfg); err != nil {
		return model.SiteConfig{}, err
	}

	return cfg, nil
}

func defaultSiteConfig() model.SiteConfig {
	return model.SiteConfig{
		Language:           defaultLanguage,
		DefaultPublish:     true,
		KaTeXCSSURL:        defaultKaTeXCSSURL,
		KaTeXJSURL:         defaultKaTeXJSURL,
		KaTeXAutoRenderURL: defaultKaTeXAutoRenderURL,
		MermaidJSURL:       defaultMermaidJSURL,
	}
}

func applyRuntimeDefaults(cfg model.SiteConfig) model.SiteConfig {
	defaults := defaultSiteConfig()

	if strings.TrimSpace(cfg.Language) == "" {
		cfg.Language = defaults.Language
	}
	if !cfg.DefaultPublishSet {
		cfg.DefaultPublish = defaults.DefaultPublish
	}

	if value := strings.TrimSpace(cfg.KaTeXCSSURL); value != "" {
		cfg.KaTeXCSSURL = value
	} else {
		cfg.KaTeXCSSURL = defaults.KaTeXCSSURL
	}
	if value := strings.TrimSpace(cfg.KaTeXJSURL); value != "" {
		cfg.KaTeXJSURL = value
	} else {
		cfg.KaTeXJSURL = defaults.KaTeXJSURL
	}
	if value := strings.TrimSpace(cfg.KaTeXAutoRenderURL); value != "" {
		cfg.KaTeXAutoRenderURL = value
	} else {
		cfg.KaTeXAutoRenderURL = defaults.KaTeXAutoRenderURL
	}
	if value := strings.TrimSpace(cfg.MermaidJSURL); value != "" {
		cfg.MermaidJSURL = value
	} else {
		cfg.MermaidJSURL = defaults.MermaidJSURL
	}

	return cfg
}

func parseFileConfig(data []byte) (fileConfig, error) {
	var cfg fileConfig
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return fileConfig{}, err
	}

	return cfg, nil
}

func applyFileConfig(cfg model.SiteConfig, parsed fileConfig) model.SiteConfig {
	if value := strings.TrimSpace(parsed.Title); value != "" {
		cfg.Title = value
	}
	if value := strings.TrimSpace(parsed.BaseURL); value != "" {
		cfg.BaseURL = value
	}
	if value := strings.TrimSpace(parsed.Author); value != "" {
		cfg.Author = value
	}
	if value := strings.TrimSpace(parsed.Description); value != "" {
		cfg.Description = value
	}
	if value := strings.TrimSpace(parsed.Language); value != "" {
		cfg.Language = value
	}
	if value := strings.TrimSpace(parsed.DefaultImg); value != "" {
		cfg.DefaultImg = value
	}
	if parsed.DefaultPublish != nil {
		cfg.DefaultPublish = *parsed.DefaultPublish
		cfg.DefaultPublishSet = true
	}

	return cfg
}

func applyOverrides(cfg model.SiteConfig, overrides Overrides) model.SiteConfig {
	if value := strings.TrimSpace(overrides.Title); value != "" {
		cfg.Title = value
	}
	if value := strings.TrimSpace(overrides.BaseURL); value != "" {
		cfg.BaseURL = value
	}
	if value := strings.TrimSpace(overrides.Author); value != "" {
		cfg.Author = value
	}
	if value := strings.TrimSpace(overrides.Description); value != "" {
		cfg.Description = value
	}
	if value := strings.TrimSpace(overrides.Language); value != "" {
		cfg.Language = value
	}
	if value := strings.TrimSpace(overrides.DefaultImg); value != "" {
		cfg.DefaultImg = value
	}
	if overrides.DefaultPublish != nil {
		cfg.DefaultPublish = *overrides.DefaultPublish
		cfg.DefaultPublishSet = true
	}

	return cfg
}

func validate(cfg *model.SiteConfig) error {
	cfg.Title = strings.TrimSpace(cfg.Title)
	if cfg.Title == "" {
		return fmt.Errorf("title is required")
	}

	baseURL, err := normalizeBaseURL(cfg.BaseURL)
	if err != nil {
		return err
	}
	cfg.BaseURL = baseURL

	cfg.Author = strings.TrimSpace(cfg.Author)
	cfg.Description = strings.TrimSpace(cfg.Description)
	cfg.Language = strings.TrimSpace(cfg.Language)
	if cfg.Language == "" {
		cfg.Language = defaultLanguage
	}
	cfg.DefaultImg = strings.TrimSpace(cfg.DefaultImg)

	return nil
}

func normalizeBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("baseURL is required")
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("baseURL is invalid: %w", err)
	}
	if !parsed.IsAbs() || parsed.Host == "" {
		return "", fmt.Errorf("baseURL must be an absolute http or https URL")
	}

	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("baseURL must use http or https")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("baseURL must not include query or fragment")
	}

	parsed.Scheme = scheme
	cleanPath := path.Clean(parsed.Path)
	switch cleanPath {
	case ".", "/":
		parsed.Path = "/"
	default:
		parsed.Path = strings.TrimSuffix(cleanPath, "/") + "/"
	}
	parsed.RawPath = ""

	return parsed.String(), nil
}
