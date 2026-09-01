package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
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
	// Filename is the only supported site configuration filename.
	Filename = "obsite.yaml"
	// CustomCSSFilename and ThemeDirRelPath are the fixed optional vault inputs.
	CustomCSSFilename = "custom.css"
	ThemeDirRelPath   = ".obsite/theme"

	defaultLanguage           = "en"
	defaultPaginationPageSize = 20
	defaultRelatedCount       = 5
	defaultTimelinePath       = "notes"
)

type fileConfig struct {
	Title          string               `yaml:"title"`
	BaseURL        string               `yaml:"baseURL"`
	Author         string               `yaml:"author"`
	Description    string               `yaml:"description"`
	Language       string               `yaml:"language"`
	DefaultImg     string               `yaml:"defaultImg"`
	DefaultPublish *bool                `yaml:"defaultPublish"`
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

// Defaults returns the single canonical product default set.
func Defaults() model.SiteConfig {
	return model.SiteConfig{
		Language:       defaultLanguage,
		DefaultPublish: true,
		Pagination: model.PaginationConfig{
			PageSize: defaultPaginationPageSize,
		},
		Related: model.RelatedConfig{
			Count: defaultRelatedCount,
		},
		RSS: model.RSSConfig{
			Enabled: true,
		},
		Timeline: model.TimelineConfig{
			Path: defaultTimelinePath,
		},
	}
}

// InitialYAML renders the init file from the canonical defaults. The required
// title and baseURL use explicit starter placeholders.
func InitialYAML() string {
	defaults := Defaults()
	return fmt.Sprintf(`# Obsite site configuration.
# Replace baseURL with the real public URL before publishing.
baseURL: https://example.com/
title: My Obsite Site
author: ""
description: ""
language: %s
defaultPublish: %t
defaultImg: ""
pagination:
  pageSize: %d
sidebar:
  enabled: %t
popover:
  enabled: %t
related:
  enabled: %t
  count: %d
rss:
  enabled: %t
timeline:
  enabled: %t
  asHomepage: %t
  path: %s
`,
		defaults.Language,
		defaults.DefaultPublish,
		defaults.Pagination.PageSize,
		defaults.Sidebar.Enabled,
		defaults.Popover.Enabled,
		defaults.Related.Enabled,
		defaults.Related.Count,
		defaults.RSS.Enabled,
		defaults.Timeline.Enabled,
		defaults.Timeline.AsHomepage,
		defaults.Timeline.Path,
	)
}

// LoadForBuild reads exactly <resolvedVault>/obsite.yaml and discovers only the
// fixed optional vault inputs.
func LoadForBuild(resolvedVault string) (model.SiteConfig, error) {
	vaultRoot := filepath.Clean(strings.TrimSpace(resolvedVault))
	if vaultRoot == "" || !filepath.IsAbs(vaultRoot) {
		return model.SiteConfig{}, fmt.Errorf("resolved vault path is required")
	}

	configPath := filepath.Join(vaultRoot, Filename)
	_, data, _, err := internalfsutil.ReadContainedRegularFile(vaultRoot, configPath)
	if err != nil {
		return model.SiteConfig{}, fmt.Errorf("read config %q: %w", configPath, err)
	}

	parsed, err := parseFileConfig(data)
	if err != nil {
		return model.SiteConfig{}, fmt.Errorf("parse config %q: %w", configPath, err)
	}
	if err := validateParsedFileConfig(parsed); err != nil {
		return model.SiteConfig{}, fmt.Errorf("validate config %q: %w", configPath, err)
	}

	cfg := applyFileConfig(Defaults(), parsed)
	cfg, err = normalizeAndValidate(cfg)
	if err != nil {
		return model.SiteConfig{}, fmt.Errorf("validate config %q: %w", configPath, err)
	}

	cfg.CustomCSS, err = discoverOptionalRegularFile(vaultRoot, CustomCSSFilename, "custom CSS")
	if err != nil {
		return model.SiteConfig{}, err
	}
	cfg.ThemeDir, err = discoverOptionalDirectory(vaultRoot, ThemeDirRelPath, "theme directory")
	if err != nil {
		return model.SiteConfig{}, err
	}
	return cfg, nil
}

// NormalizeSiteConfig normalizes an already-constructed internal config. The
// vault YAML loader, not this function, owns user-visible boolean defaults.
func NormalizeSiteConfig(cfg model.SiteConfig) (model.SiteConfig, error) {
	defaults := Defaults()
	if strings.TrimSpace(cfg.Language) == "" {
		cfg.Language = defaults.Language
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
	return normalizeAndValidate(cfg)
}

func parseFileConfig(data []byte) (fileConfig, error) {
	var cfg fileConfig
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return fileConfig{}, err
	}
	var trailing fileConfig
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err != nil {
			return fileConfig{}, err
		}
		return fileConfig{}, fmt.Errorf("multiple YAML documents are not supported")
	}
	return cfg, nil
}

func validateParsedFileConfig(parsed fileConfig) error {
	if parsed.Pagination.PageSize != nil && *parsed.Pagination.PageSize <= 0 {
		return fmt.Errorf("pagination.pageSize must be greater than 0")
	}
	if parsed.Related.Count != nil && (*parsed.Related.Count < 1 || *parsed.Related.Count > 20) {
		return fmt.Errorf("related.count must be between 1 and 20")
	}
	return nil
}

func applyFileConfig(cfg model.SiteConfig, parsed fileConfig) model.SiteConfig {
	cfg.Title = strings.TrimSpace(parsed.Title)
	cfg.BaseURL = strings.TrimSpace(parsed.BaseURL)
	cfg.Author = strings.TrimSpace(parsed.Author)
	cfg.Description = strings.TrimSpace(parsed.Description)
	if value := strings.TrimSpace(parsed.Language); value != "" {
		cfg.Language = value
	}
	cfg.DefaultImg = strings.TrimSpace(parsed.DefaultImg)
	if parsed.DefaultPublish != nil {
		cfg.DefaultPublish = *parsed.DefaultPublish
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

func normalizeAndValidate(cfg model.SiteConfig) (model.SiteConfig, error) {
	cfg.Title = strings.TrimSpace(cfg.Title)
	if cfg.Title == "" {
		return model.SiteConfig{}, fmt.Errorf("title is required")
	}
	baseURL, err := normalizeBaseURL(cfg.BaseURL)
	if err != nil {
		return model.SiteConfig{}, err
	}
	cfg.BaseURL = baseURL
	cfg.Author = strings.TrimSpace(cfg.Author)
	cfg.Description = strings.TrimSpace(cfg.Description)
	cfg.Language = strings.TrimSpace(cfg.Language)
	defaultImg, externalDefaultImg, err := normalizeDefaultImg(cfg.DefaultImg)
	if err != nil {
		return model.SiteConfig{}, err
	}
	cfg.DefaultImg = defaultImg
	cfg.DefaultImgExternal = externalDefaultImg
	cfg.ThemeDir = strings.TrimSpace(cfg.ThemeDir)
	cfg.ThemeCSS = strings.TrimSpace(cfg.ThemeCSS)
	cfg.CustomCSS = strings.TrimSpace(cfg.CustomCSS)
	if cfg.Pagination.PageSize <= 0 {
		return model.SiteConfig{}, fmt.Errorf("pagination.pageSize must be greater than 0")
	}
	if cfg.Related.Count < 1 || cfg.Related.Count > 20 {
		return model.SiteConfig{}, fmt.Errorf("related.count must be between 1 and 20")
	}
	timelinePath, err := normalizeTimelinePath(cfg.Timeline.Path)
	if err != nil {
		return model.SiteConfig{}, err
	}
	cfg.Timeline.Path = timelinePath
	return cfg, nil
}

func discoverOptionalRegularFile(vaultRoot string, relPath string, label string) (string, error) {
	candidate := filepath.Join(vaultRoot, filepath.FromSlash(relPath))
	resolved, _, err := internalfsutil.InspectContainedRegularFile(vaultRoot, candidate)
	if err == nil {
		return resolved, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if errors.Is(err, internalfsutil.ErrSymlinkPath) || errors.Is(err, internalfsutil.ErrUnsupportedRegularFileSource) {
		return "", fmt.Errorf("%s %q must be a regular non-symlink file inside the vault", label, candidate)
	}
	return "", fmt.Errorf("inspect %s %q: %w", label, candidate, err)
}

func discoverOptionalDirectory(vaultRoot string, relPath string, label string) (string, error) {
	candidate := filepath.Join(vaultRoot, filepath.FromSlash(relPath))
	resolved, _, err := internalfsutil.InspectContainedDirectory(vaultRoot, candidate)
	if err == nil {
		return resolved, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if errors.Is(err, internalfsutil.ErrSymlinkPath) || errors.Is(err, internalfsutil.ErrUnsupportedRegularFileSource) {
		return "", fmt.Errorf("%s %q must be a non-symlink directory inside the vault", label, candidate)
	}
	return "", fmt.Errorf("inspect %s %q: %w", label, candidate, err)
}

func normalizeDefaultImg(raw string) (string, bool, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false, nil
	}
	parsed, err := url.Parse(trimmed)
	if err == nil && parsed.IsAbs() && parsed.Host != "" && (strings.EqualFold(parsed.Scheme, "http") || strings.EqualFold(parsed.Scheme, "https")) {
		return trimmed, true, nil
	}
	if strings.Contains(trimmed, `\`) || strings.ContainsAny(trimmed, "?#:") || strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, "//") || hasWindowsDrivePrefix(trimmed) {
		return "", false, fmt.Errorf("defaultImg must be an absolute hosted http(s) URL or a vault-root-relative resource path using '/' separators")
	}
	cleaned := path.Clean(trimmed)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", false, fmt.Errorf("defaultImg must stay inside the vault")
	}
	return cleaned, false, nil
}

func normalizeTimelinePath(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return defaultTimelinePath, nil
	}
	if strings.Contains(trimmed, `\`) || strings.ContainsAny(trimmed, "?#") || strings.HasPrefix(trimmed, "/") || hasWindowsDrivePrefix(trimmed) || strings.HasPrefix(trimmed, "//") {
		return "", fmt.Errorf("timeline.path must be a site-relative URL path")
	}
	cleaned := path.Clean(trimmed)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("timeline.path must stay within the generated site")
	}
	if !internalfsutil.IsPortableSitePath(cleaned) {
		return "", fmt.Errorf("timeline.path must use portable site path components")
	}
	return cleaned, nil
}

func hasWindowsDrivePrefix(raw string) bool {
	return len(raw) >= 2 && ((raw[0] >= 'a' && raw[0] <= 'z') || (raw[0] >= 'A' && raw[0] <= 'Z')) && raw[1] == ':'
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
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("baseURL must use http or https")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("baseURL must not include user info")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("baseURL must not include query or fragment")
	}
	cleanPath := path.Clean(parsed.Path)
	if cleanPath == "." || cleanPath == "/" {
		parsed.Path = "/"
	} else {
		parsed.Path = strings.TrimSuffix(cleanPath, "/") + "/"
	}
	parsed.RawPath = ""
	return parsed.String(), nil
}
