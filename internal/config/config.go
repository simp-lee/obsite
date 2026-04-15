package config

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	internalfsutil "github.com/simp-lee/obsite/internal/fsutil"
	"github.com/simp-lee/obsite/internal/model"
	"gopkg.in/yaml.v3"
)

const (
	defaultLanguage = "en"

	defaultPagefindPath       = "tools/pagefind_extended"
	defaultPagefindVersion    = "1.5.2"
	defaultPaginationPageSize = 20
	defaultRelatedCount       = 5
	defaultTimelinePath       = "notes"
	defaultCustomCSSName      = "custom.css"

	defaultKaTeXCSSURL        = "assets/obsite-runtime/katex.min.css"
	defaultKaTeXJSURL         = "assets/obsite-runtime/katex.min.js"
	defaultKaTeXAutoRenderURL = "assets/obsite-runtime/auto-render.min.js"
	defaultMermaidJSURL       = "assets/obsite-runtime/mermaid.esm.min.mjs"
)

// Overrides carries caller-provided values. Empty strings and nil booleans mean
// the field was not provided.
type Overrides struct {
	Title          string
	BaseURL        string
	Author         string
	Description    string
	Language       string
	DefaultImg     string
	DefaultPublish *bool
	VaultPath      string
}

// LoadedSiteConfig carries the normalized site config plus build-only policy
// that should not leak into the shared model.SiteConfig contract.
type LoadedSiteConfig struct {
	Config                model.SiteConfig
	AllowMissingCustomCSS bool
}

type fileConfig struct {
	Title          string               `yaml:"title"`
	BaseURL        string               `yaml:"baseURL"`
	Author         string               `yaml:"author"`
	Description    string               `yaml:"description"`
	Language       string               `yaml:"language"`
	DefaultImg     string               `yaml:"defaultImg"`
	DefaultPublish *bool                `yaml:"defaultPublish"`
	TemplateDir    string               `yaml:"templateDir"`
	CustomCSS      string               `yaml:"customCSS"`
	Search         searchFileConfig     `yaml:"search"`
	Pagination     paginationFileConfig `yaml:"pagination"`
	Sidebar        enabledFileConfig    `yaml:"sidebar"`
	Popover        enabledFileConfig    `yaml:"popover"`
	Related        relatedFileConfig    `yaml:"related"`
	RSS            enabledFileConfig    `yaml:"rss"`
	Timeline       timelineFileConfig   `yaml:"timeline"`
}

type enabledFileConfig struct {
	Enabled *bool `yaml:"enabled"`
}

type searchFileConfig struct {
	Enabled         *bool  `yaml:"enabled"`
	PagefindPath    string `yaml:"pagefindPath"`
	PagefindVersion string `yaml:"pagefindVersion"`
}

type paginationFileConfig struct {
	PageSize *int `yaml:"pageSize"`
}

type relatedFileConfig struct {
	Enabled *bool `yaml:"enabled"`
	Count   *int  `yaml:"count"`
}

type timelineFileConfig struct {
	Enabled    *bool  `yaml:"enabled"`
	AsHomepage *bool  `yaml:"asHomepage"`
	Path       string `yaml:"path"`
}

type loadPaths struct {
	configDir string
	vaultRoot string
}

// LoadForBuild reads obsite.yaml and returns the normalized site config plus
// build-only policy derived during loading.
func LoadForBuild(path string, overrides Overrides) (LoadedSiteConfig, error) {
	cfg := defaultSiteConfig()
	paths := resolveLoadPaths(path, overrides)

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return LoadedSiteConfig{}, fmt.Errorf("read config %q: %w", path, err)
		}

		parsed, err := parseFileConfig(data)
		if err != nil {
			return LoadedSiteConfig{}, fmt.Errorf("parse config %q: %w", path, err)
		}

		if err := validateParsedFileConfig(parsed); err != nil {
			return LoadedSiteConfig{}, fmt.Errorf("validate config %q: %w", path, err)
		}

		cfg = applyFileConfig(cfg, parsed)
	}

	cfg = applyOverrides(cfg, overrides)

	normalized, err := ValidateLoadedSiteConfig(cfg)
	if err != nil {
		if path == "" {
			return LoadedSiteConfig{}, fmt.Errorf("validate config: %w", err)
		}

		return LoadedSiteConfig{}, fmt.Errorf("validate config %q: %w", path, err)
	}

	loaded, err := applyLoadPathDefaults(normalized, paths)
	if err != nil {
		if path == "" {
			return LoadedSiteConfig{}, fmt.Errorf("resolve config paths: %w", err)
		}

		return LoadedSiteConfig{}, fmt.Errorf("resolve config paths for %q: %w", path, err)
	}

	return loaded, nil
}

// NormalizeSiteConfig applies the documented shared product defaults to a
// caller-provided site config, then validates and normalizes it.
//
// Callers that already resolved explicit policy booleans through config loading
// should use ValidateLoadedSiteConfig to avoid re-defaulting false values for
// defaultPublish and rss.enabled.
func NormalizeSiteConfig(cfg model.SiteConfig) (model.SiteConfig, error) {
	cfg = applySharedDefaults(cfg)

	return ValidateLoadedSiteConfig(cfg)
}

// ValidateLoadedSiteConfig validates and normalizes a config that already has
// loader or caller-resolved boolean policy. Unlike NormalizeSiteConfig, it
// preserves explicit false values for defaultPublish and rss.enabled.
func ValidateLoadedSiteConfig(cfg model.SiteConfig) (model.SiteConfig, error) {
	cfg = applyRuntimeDefaults(cfg)
	if err := validate(&cfg); err != nil {
		return model.SiteConfig{}, err
	}

	return cfg, nil
}

func applySharedDefaults(cfg model.SiteConfig) model.SiteConfig {
	defaults := defaultSiteConfig()

	if !cfg.DefaultPublishSet {
		cfg.DefaultPublish = defaults.EffectiveDefaultPublish()
		cfg.DefaultPublishSet = true
	}
	if !cfg.RSS.EnabledSet {
		cfg.RSS.Enabled = defaults.EffectiveRSSEnabled()
		cfg.RSS.EnabledSet = true
	}

	return cfg
}

func defaultSiteConfig() model.SiteConfig {
	return model.SiteConfig{
		Language:          defaultLanguage,
		DefaultPublish:    true,
		DefaultPublishSet: true,
		Search: model.SearchConfig{
			PagefindPath:    defaultPagefindPath,
			PagefindVersion: defaultPagefindVersion,
		},
		Pagination: model.PaginationConfig{PageSize: defaultPaginationPageSize},
		Related:    model.RelatedConfig{Count: defaultRelatedCount},
		RSS: model.RSSConfig{
			Enabled:    true,
			EnabledSet: true,
		},
		Timeline: model.TimelineConfig{
			Path: defaultTimelinePath,
		},
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

	if value := strings.TrimSpace(cfg.Search.PagefindPath); value != "" {
		cfg.Search.PagefindPath = value
	} else {
		cfg.Search.PagefindPath = defaults.Search.PagefindPath
	}
	if value := strings.TrimSpace(cfg.Search.PagefindVersion); value != "" {
		cfg.Search.PagefindVersion = value
	} else {
		cfg.Search.PagefindVersion = defaults.Search.PagefindVersion
	}
	if cfg.Pagination.PageSize == 0 {
		cfg.Pagination.PageSize = defaults.Pagination.PageSize
	}
	if cfg.Related.Count == 0 {
		cfg.Related.Count = defaults.Related.Count
	}
	if strings.TrimSpace(cfg.Timeline.Path) == "" {
		cfg.Timeline.Path = defaults.Timeline.Path
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

func resolveLoadPaths(configPath string, overrides Overrides) loadPaths {
	paths := loadPaths{}

	if trimmedConfigPath := strings.TrimSpace(configPath); trimmedConfigPath != "" {
		paths.configDir = filepath.Clean(filepath.Dir(trimmedConfigPath))
	}
	if trimmedVaultPath := strings.TrimSpace(overrides.VaultPath); trimmedVaultPath != "" {
		paths.vaultRoot = filepath.Clean(trimmedVaultPath)
	}

	return paths
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

func validateParsedFileConfig(parsed fileConfig) error {
	if parsed.Pagination.PageSize != nil && *parsed.Pagination.PageSize <= 0 {
		return fmt.Errorf("pagination.pageSize must be greater than 0")
	}
	if parsed.Related.Count != nil && *parsed.Related.Count <= 0 {
		return fmt.Errorf("related.count must be greater than 0")
	}

	return nil
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
	if value := strings.TrimSpace(parsed.TemplateDir); value != "" {
		cfg.TemplateDir = value
	}
	if value := strings.TrimSpace(parsed.CustomCSS); value != "" {
		cfg.CustomCSS = value
	}
	if parsed.Search.Enabled != nil {
		cfg.Search.Enabled = *parsed.Search.Enabled
	}
	if value := strings.TrimSpace(parsed.Search.PagefindPath); value != "" {
		cfg.Search.PagefindPath = value
	}
	if value := strings.TrimSpace(parsed.Search.PagefindVersion); value != "" {
		cfg.Search.PagefindVersion = value
	}
	if parsed.Pagination.PageSize != nil {
		cfg.Pagination.PageSize = *parsed.Pagination.PageSize
	}
	if parsed.Sidebar.Enabled != nil {
		cfg.Sidebar.Enabled = *parsed.Sidebar.Enabled
	}
	if parsed.Popover.Enabled != nil {
		cfg.Popover.Enabled = *parsed.Popover.Enabled
	}
	if parsed.Related.Enabled != nil {
		cfg.Related.Enabled = *parsed.Related.Enabled
	}
	if parsed.Related.Count != nil {
		cfg.Related.Count = *parsed.Related.Count
	}
	if parsed.RSS.Enabled != nil {
		cfg.RSS.Enabled = *parsed.RSS.Enabled
		cfg.RSS.EnabledSet = true
	}
	if parsed.Timeline.Enabled != nil {
		cfg.Timeline.Enabled = *parsed.Timeline.Enabled
	}
	if parsed.Timeline.AsHomepage != nil {
		cfg.Timeline.AsHomepage = *parsed.Timeline.AsHomepage
	}
	if value := strings.TrimSpace(parsed.Timeline.Path); value != "" {
		cfg.Timeline.Path = value
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
	cfg.TemplateDir = strings.TrimSpace(cfg.TemplateDir)
	cfg.CustomCSS = strings.TrimSpace(cfg.CustomCSS)
	cfg.Search.PagefindPath = strings.TrimSpace(cfg.Search.PagefindPath)
	cfg.Search.PagefindVersion = strings.TrimSpace(cfg.Search.PagefindVersion)
	if cfg.Pagination.PageSize < 0 {
		return fmt.Errorf("pagination.pageSize must be greater than 0")
	}
	if cfg.Related.Count < 0 {
		return fmt.Errorf("related.count must be greater than 0")
	}

	timelinePath, err := normalizeTimelinePath(cfg.Timeline.Path)
	if err != nil {
		return err
	}
	cfg.Timeline.Path = timelinePath

	return nil
}

func applyLoadPathDefaults(cfg model.SiteConfig, paths loadPaths) (LoadedSiteConfig, error) {
	baseDir := paths.configDir
	if baseDir == "" {
		baseDir = paths.vaultRoot
	}

	cfg.TemplateDir = resolveConfiguredPath(cfg.TemplateDir, baseDir)
	cfg.CustomCSS = resolveConfiguredPath(cfg.CustomCSS, baseDir)
	cfg.Search.PagefindPath = resolvePagefindPath(cfg.Search.PagefindPath, baseDir)
	if cfg.CustomCSS != "" {
		return LoadedSiteConfig{Config: cfg}, nil
	}

	detectedCSS, err := detectCustomCSS(paths.configDir, paths.vaultRoot)
	if err != nil {
		return LoadedSiteConfig{}, err
	}
	cfg.CustomCSS = detectedCSS

	return LoadedSiteConfig{Config: cfg, AllowMissingCustomCSS: detectedCSS != ""}, nil
}

func resolveConfiguredPath(raw string, baseDir string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if filepath.IsAbs(trimmed) || strings.TrimSpace(baseDir) == "" {
		return filepath.Clean(trimmed)
	}

	return filepath.Clean(filepath.Join(baseDir, trimmed))
}

func resolvePagefindPath(raw string, baseDir string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if filepath.IsAbs(trimmed) {
		return filepath.Clean(trimmed)
	}
	if baseDir == "" || !looksLikeFilesystemPath(trimmed) {
		return trimmed
	}

	return filepath.Clean(filepath.Join(baseDir, trimmed))
}

func looksLikeFilesystemPath(raw string) bool {
	return strings.Contains(raw, "/") || strings.Contains(raw, "\\")
}

func detectCustomCSS(roots ...string) (string, error) {
	seen := make(map[string]struct{}, len(roots))
	for _, root := range roots {
		trimmedRoot := strings.TrimSpace(root)
		if trimmedRoot == "" {
			continue
		}

		cleanRoot := filepath.Clean(trimmedRoot)
		if _, ok := seen[cleanRoot]; ok {
			continue
		}
		seen[cleanRoot] = struct{}{}

		candidate := filepath.Join(cleanRoot, defaultCustomCSSName)
		resolvedPath, _, err := internalfsutil.InspectRegularNonSymlinkFile(candidate)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if errors.Is(err, internalfsutil.ErrUnsupportedRegularFileSource) {
				return "", fmt.Errorf("auto-detected custom CSS %q must be a regular non-symlink file", candidate)
			}

			return "", fmt.Errorf("inspect auto-detected custom CSS %q: %w", candidate, err)
		}

		return resolvedPath, nil
	}

	return "", nil
}

func normalizeTimelinePath(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return defaultTimelinePath, nil
	}

	normalizedSlashes := strings.ReplaceAll(trimmed, `\`, "/")
	if strings.ContainsAny(normalizedSlashes, "?#") {
		return "", fmt.Errorf("timeline.path must not include query or fragment")
	}
	if strings.HasPrefix(normalizedSlashes, "//") || hasWindowsDrivePrefix(normalizedSlashes) {
		return "", fmt.Errorf("timeline.path must be a site-relative path")
	}

	cleaned := path.Clean(normalizedSlashes)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("timeline.path must stay within the site root")
	}

	normalized := strings.Trim(cleaned, "/")
	if normalized == "" || normalized == "." {
		return "", fmt.Errorf("timeline.path must not be empty")
	}

	return normalized, nil
}

func hasWindowsDrivePrefix(raw string) bool {
	if len(raw) < 3 || raw[1] != ':' {
		return false
	}

	first := raw[0]
	if (first < 'A' || first > 'Z') && (first < 'a' || first > 'z') {
		return false
	}

	return raw[2] == '/'
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
	if parsed.User != nil {
		return "", fmt.Errorf("baseURL must not include user info")
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
