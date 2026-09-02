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
	Navigation     []navigationFileItem `yaml:"navigation"`
	Source         sourceFileConfig     `yaml:"source"`
	Versions       *versionsFileConfig  `yaml:"versions"`
	Pagination     paginationFileConfig `yaml:"pagination"`
	Sidebar        enabledFileConfig    `yaml:"sidebar"`
	Popover        enabledFileConfig    `yaml:"popover"`
	Related        relatedFileConfig    `yaml:"related"`
	RSS            enabledFileConfig    `yaml:"rss"`
	Timeline       timelineFileConfig   `yaml:"timeline"`
}

type navigationFileItem struct {
	Name    string `yaml:"name"`
	URL     string `yaml:"url"`
	Section string `yaml:"section"`
}

type sourceFileConfig struct {
	EditURL string `yaml:"editURL"`
	ViewURL string `yaml:"viewURL"`
}

type versionsFileConfig struct {
	Root    string             `yaml:"root"`
	Default string             `yaml:"default"`
	Entries []versionFileEntry `yaml:"entries"`
}

type versionFileEntry struct {
	ID     string `yaml:"id"`
	Label  string `yaml:"label"`
	Source string `yaml:"source"`
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

// UsesStrictSchema reports whether the vault configuration declares the
// revised navigation field. Callers use it only to choose the strict parser;
// malformed revised files still fail that parser rather than falling back.
func UsesStrictSchema(resolvedVault string) (bool, error) {
	vaultRoot := filepath.Clean(strings.TrimSpace(resolvedVault))
	if vaultRoot == "" || !filepath.IsAbs(vaultRoot) {
		return false, fmt.Errorf("resolved vault path is required")
	}
	_, data, _, err := internalfsutil.ReadContainedRegularFile(vaultRoot, filepath.Join(vaultRoot, Filename))
	if err != nil {
		return false, err
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return false, err
	}
	if len(document.Content) == 0 || document.Content[0].Kind != yaml.MappingNode {
		return false, nil
	}
	root := document.Content[0]
	for index := 0; index+1 < len(root.Content); index += 2 {
		if root.Content[index].Value == "navigation" {
			return true, nil
		}
	}
	return false, nil
}

// LoadStrictForBuild loads the revised section-based configuration contract.
// It intentionally rejects the superseded defaultPublish setting and requires
// an explicit navigation sequence (including an explicit empty sequence).
func LoadStrictForBuild(resolvedVault string) (model.SiteConfig, error) {
	vaultRoot := filepath.Clean(strings.TrimSpace(resolvedVault))
	if vaultRoot == "" || !filepath.IsAbs(vaultRoot) {
		return model.SiteConfig{}, fmt.Errorf("resolved vault path is required")
	}
	configPath := filepath.Join(vaultRoot, Filename)
	_, data, _, err := internalfsutil.ReadContainedRegularFile(vaultRoot, configPath)
	if err != nil {
		return model.SiteConfig{}, fmt.Errorf("read config %q: %w", configPath, err)
	}
	if err := validateStrictConfigDocument(data); err != nil {
		return model.SiteConfig{}, fmt.Errorf("parse config %q: %w", configPath, err)
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

// InitialStrictYAML is the minimal revised-schema configuration used by the
// section-based initializer. The legacy initializer remains available while
// the CLI publication pipeline is migrated to the strict analyzer.
func InitialStrictYAML() string {
	return "baseURL: https://example.com/\ntitle: My Obsite Site\nnavigation: []\nsource: {}\n"
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
	if err := validateYAMLStructure(data); err != nil {
		return fileConfig{}, err
	}

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
	if len(parsed.Navigation) > 0 {
		for index, item := range parsed.Navigation {
			if strings.TrimSpace(item.Name) == "" {
				return fmt.Errorf("navigation[%d].name is required", index)
			}
			if (strings.TrimSpace(item.URL) == "") == (strings.TrimSpace(item.Section) == "") {
				return fmt.Errorf("navigation[%d] must contain exactly one of url or section", index)
			}
		}
	}
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
	cfg.Navigation = make([]model.NavigationItem, 0, len(parsed.Navigation))
	for _, item := range parsed.Navigation {
		cfg.Navigation = append(cfg.Navigation, model.NavigationItem{
			Name: strings.TrimSpace(item.Name), URL: strings.TrimSpace(item.URL), Section: strings.TrimSpace(item.Section),
		})
	}
	cfg.Source = model.SourceConfig{EditURL: strings.TrimSpace(parsed.Source.EditURL), ViewURL: strings.TrimSpace(parsed.Source.ViewURL)}
	if parsed.Versions != nil {
		versions := &model.VersionsConfig{Root: strings.TrimSpace(parsed.Versions.Root), Default: strings.TrimSpace(parsed.Versions.Default), Entries: make([]model.VersionEntry, 0, len(parsed.Versions.Entries))}
		for _, entry := range parsed.Versions.Entries {
			versions.Entries = append(versions.Entries, model.VersionEntry{ID: strings.TrimSpace(entry.ID), Label: strings.TrimSpace(entry.Label), Source: strings.TrimSpace(entry.Source)})
		}
		cfg.Versions = versions
	} else {
		cfg.Versions = nil
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

func validateYAMLStructure(data []byte) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return err
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); err != io.EOF && err != nil {
		return err
	}
	if len(document.Content) == 0 {
		return nil
	}
	return validateYAMLNode(document.Content[0])
}

func validateStrictConfigDocument(data []byte) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return err
	}
	if len(document.Content) == 0 || document.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("configuration must be a YAML mapping")
	}
	allowed := map[string]struct{}{"title": {}, "baseURL": {}, "author": {}, "description": {}, "language": {}, "defaultImg": {}, "navigation": {}, "source": {}, "versions": {}, "pagination": {}, "sidebar": {}, "popover": {}, "related": {}, "rss": {}, "timeline": {}}
	seen := make(map[string]struct{}, len(document.Content[0].Content)/2)
	root := document.Content[0]
	for index := 0; index+1 < len(root.Content); index += 2 {
		key, value := root.Content[index], root.Content[index+1]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
			return fmt.Errorf("configuration key at line %d must be a string", key.Line)
		}
		if _, exists := seen[key.Value]; exists {
			return fmt.Errorf("duplicate key %q at line %d", key.Value, key.Line)
		}
		seen[key.Value] = struct{}{}
		if value.Tag == "!!null" {
			return fmt.Errorf("field %q at line %d must not be null", key.Value, value.Line)
		}
		if key.Value == "defaultPublish" {
			return fmt.Errorf("field %q is not supported by the revised configuration", key.Value)
		}
		if _, ok := allowed[key.Value]; !ok {
			return fmt.Errorf("unknown config field %q at line %d", key.Value, key.Line)
		}
	}
	if _, ok := seen["navigation"]; !ok {
		return fmt.Errorf("navigation must be explicitly provided")
	}
	return nil
}

func validateYAMLNode(node *yaml.Node) error {
	if node == nil {
		return nil
	}
	switch node.Kind {
	case yaml.MappingNode:
		seen := make(map[string]int, len(node.Content)/2)
		for index := 0; index+1 < len(node.Content); index += 2 {
			keyNode, valueNode := node.Content[index], node.Content[index+1]
			if keyNode.Kind != yaml.ScalarNode || keyNode.Tag != "!!str" {
				return fmt.Errorf("mapping key at line %d must be a string", keyNode.Line)
			}
			if firstLine, ok := seen[keyNode.Value]; ok {
				return fmt.Errorf("duplicate key %q at line %d (first declared at line %d)", keyNode.Value, keyNode.Line, firstLine)
			}
			seen[keyNode.Value] = keyNode.Line
			if valueNode.Tag == "!!null" {
				return fmt.Errorf("field %q at line %d must not be null", keyNode.Value, valueNode.Line)
			}
			if err := validateYAMLNode(valueNode); err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		for _, child := range node.Content {
			if err := validateYAMLNode(child); err != nil {
				return err
			}
		}
	}
	return nil
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
	cfg.Navigation, err = normalizeNavigation(cfg.Navigation)
	if err != nil {
		return model.SiteConfig{}, err
	}
	cfg.Source, err = normalizeSourceConfig(cfg.Source)
	if err != nil {
		return model.SiteConfig{}, err
	}
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
	cfg.Versions, err = NormalizeVersionsConfig(cfg.Versions)
	if err != nil {
		return model.SiteConfig{}, err
	}
	return cfg, nil
}

// NormalizeVersionsConfig validates the syntax and identity rules that do not
// require touching the vault filesystem.
func NormalizeVersionsConfig(input *model.VersionsConfig) (*model.VersionsConfig, error) {
	if input == nil {
		return nil, nil
	}
	result := &model.VersionsConfig{Root: strings.TrimSpace(input.Root), Default: strings.TrimSpace(input.Default), Entries: make([]model.VersionEntry, 0, len(input.Entries))}
	if result.Root == "" || !validConfigRelativeDir(result.Root) {
		return nil, fmt.Errorf("versions.root must be a normalized vault-relative directory path")
	}
	if result.Default == "" {
		return nil, fmt.Errorf("versions.default is required")
	}
	seenIDs := make(map[string]struct{}, len(input.Entries))
	seenSources := make([]string, 0, len(input.Entries))
	for index, entry := range input.Entries {
		entry.ID, entry.Label, entry.Source = strings.TrimSpace(entry.ID), strings.TrimSpace(entry.Label), strings.TrimSpace(entry.Source)
		if !validVersionID(entry.ID) {
			return nil, fmt.Errorf("versions.entries[%d].id must be a non-empty ASCII path segment", index)
		}
		if entry.Label == "" {
			return nil, fmt.Errorf("versions.entries[%d].label is required", index)
		}
		if _, exists := seenIDs[entry.ID]; exists {
			return nil, fmt.Errorf("versions entry id %q is duplicated", entry.ID)
		}
		seenIDs[entry.ID] = struct{}{}
		if !validConfigRelativeDir(entry.Source) {
			return nil, fmt.Errorf("versions.entries[%d].source must be a normalized vault-relative directory path", index)
		}
		for _, other := range seenSources {
			if entry.Source == other || strings.HasPrefix(entry.Source, other+"/") || strings.HasPrefix(other, entry.Source+"/") {
				return nil, fmt.Errorf("version source %q overlaps %q", entry.Source, other)
			}
		}
		seenSources = append(seenSources, entry.Source)
		result.Entries = append(result.Entries, entry)
	}
	foundDefault := false
	for _, entry := range result.Entries {
		if entry.ID == result.Default {
			foundDefault = true
			break
		}
	}
	if !foundDefault {
		return nil, fmt.Errorf("versions.default %q does not identify an entry", result.Default)
	}
	if len(result.Entries) == 0 {
		return nil, fmt.Errorf("versions.entries must not be empty")
	}
	return result, nil
}

func validConfigRelativeDir(value string) bool {
	if value == "" || value == "." || strings.Contains(value, `\`) || strings.Contains(value, "//") || strings.HasPrefix(value, "/") {
		return false
	}
	cleaned := path.Clean(value)
	return cleaned == value && cleaned != "." && cleaned != ".." && !strings.HasPrefix(cleaned, "../") && internalfsutil.IsPortableSitePath(cleaned)
}
func validVersionID(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '~') {
			return false
		}
	}
	return true
}

func normalizeNavigation(items []model.NavigationItem) ([]model.NavigationItem, error) {
	if len(items) == 0 {
		return nil, nil
	}
	result := make([]model.NavigationItem, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for index, item := range items {
		item.Name = strings.TrimSpace(item.Name)
		item.URL = strings.TrimSpace(item.URL)
		item.Section = strings.TrimSpace(item.Section)
		if item.Name == "" {
			return nil, fmt.Errorf("navigation[%d].name is required", index)
		}
		if (item.URL == "") == (item.Section == "") {
			return nil, fmt.Errorf("navigation[%d] must contain exactly one of url or section", index)
		}
		key := "section:" + item.Section
		if item.URL != "" {
			if err := validateNavigationURL(item.URL); err != nil {
				return nil, fmt.Errorf("navigation[%d].url: %w", index, err)
			}
			key = "url:" + item.URL
		} else {
			section, err := normalizeSectionReference(item.Section)
			if err != nil {
				return nil, fmt.Errorf("navigation[%d].section: %w", index, err)
			}
			item.Section = section
			key = "section:" + section
		}
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("navigation[%d] duplicates target %q", index, key)
		}
		seen[key] = struct{}{}
		result = append(result, item)
	}
	return result, nil
}

func validateNavigationURL(raw string) error {
	if strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "//") {
		if strings.ContainsAny(raw, "\\\x00\r\n") {
			return fmt.Errorf("site-relative URL contains an invalid character")
		}
		return nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" || (strings.ToLower(parsed.Scheme) != "http" && strings.ToLower(parsed.Scheme) != "https") {
		return fmt.Errorf("URL must be site-relative or an absolute HTTP(S) URL")
	}
	if parsed.User != nil {
		return fmt.Errorf("URL must not include user info")
	}
	return nil
}

func normalizeSectionReference(raw string) (string, error) {
	if raw == "." {
		return raw, nil
	}
	if raw == "" || strings.ContainsAny(raw, `\\?#`) || strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || strings.Contains(raw, "//") {
		return "", fmt.Errorf("must be a vault-relative section path; root is '.'")
	}
	cleaned := path.Clean(raw)
	if cleaned != raw || cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("must be a normalized vault-relative section path")
	}
	for part := range strings.SplitSeq(cleaned, "/") {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("must not contain empty or dot path segments")
		}
	}
	return cleaned, nil
}

func normalizeSourceConfig(source model.SourceConfig) (model.SourceConfig, error) {
	var err error
	source.EditURL, err = normalizeSourceTemplate(source.EditURL)
	if err != nil {
		return model.SourceConfig{}, fmt.Errorf("source.editURL: %w", err)
	}
	source.ViewURL, err = normalizeSourceTemplate(source.ViewURL)
	if err != nil {
		return model.SourceConfig{}, fmt.Errorf("source.viewURL: %w", err)
	}
	return source, nil
}

func normalizeSourceTemplate(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if strings.Count(raw, ":path") != 1 {
		return "", fmt.Errorf("template must contain exactly one :path placeholder")
	}
	parsed, err := url.Parse(raw)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" || (strings.ToLower(parsed.Scheme) != "http" && strings.ToLower(parsed.Scheme) != "https") {
		return "", fmt.Errorf("template must be an absolute HTTP(S) URL")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("template must not include user info")
	}
	if strings.Contains(parsed.RawQuery, ":path") || strings.Contains(parsed.Fragment, ":path") || strings.Contains(parsed.Host, ":path") {
		return "", fmt.Errorf(":path must be in the URL path component")
	}
	pathText := parsed.Path
	if !strings.Contains(pathText, ":path") {
		return "", fmt.Errorf(":path must be in the URL path component")
	}
	for index := 0; index < len(raw); index++ {
		if raw[index] != ':' || index+1 >= len(raw) || !isASCIIPlaceholderLetter(raw[index+1]) {
			continue
		}
		end := index + 2
		for end < len(raw) && (isASCIIPlaceholderLetter(raw[end]) || raw[end] >= '0' && raw[end] <= '9' || raw[end] == '_' || raw[end] == '-') {
			end++
		}
		if raw[index:end] != ":path" {
			return "", fmt.Errorf("unknown template placeholder %q", raw[index:end])
		}
		index = end - 1
	}
	return raw, nil
}

func isASCIIPlaceholderLetter(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
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

func rawBaseURLPath(raw string) string {
	schemeEnd := strings.Index(raw, "://")
	if schemeEnd < 0 {
		return ""
	}
	pathStart := strings.IndexByte(raw[schemeEnd+3:], '/')
	if pathStart < 0 {
		return "/"
	}
	pathStart += schemeEnd + 3
	pathEnd := strings.IndexAny(raw[pathStart:], "?#")
	if pathEnd < 0 {
		return raw[pathStart:]
	}
	return raw[pathStart : pathStart+pathEnd]
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

	escapedPath := parsed.EscapedPath()
	if rawPath := rawBaseURLPath(raw); strings.Contains(rawPath, "%") {
		escapedPath = rawPath
	}
	for component := range strings.SplitSeq(escapedPath, "/") {
		if !strings.Contains(component, "%") {
			continue
		}
		decoded, err := url.PathUnescape(component)
		if err != nil {
			return "", fmt.Errorf("baseURL has an invalid escaped path component: %w", err)
		}
		if decoded == "." || decoded == ".." || strings.ContainsAny(decoded, `/\\`) {
			return "", fmt.Errorf("baseURL must not contain encoded separators or dot segments")
		}
	}

	cleanPath := path.Clean(parsed.Path)
	if strings.Contains(escapedPath, "%") && cleanPath != parsed.Path {
		return "", fmt.Errorf("baseURL with escaped path components cannot be normalized")
	}
	if cleanPath == "." || cleanPath == "/" {
		parsed.Path = "/"
		escapedPath = "/"
	} else {
		parsed.Path = strings.TrimSuffix(cleanPath, "/") + "/"
		escapedPath = strings.TrimSuffix(escapedPath, "/") + "/"
	}
	if escapedPath != parsed.Path {
		parsed.RawPath = escapedPath
	} else {
		parsed.RawPath = ""
	}
	return parsed.String(), nil
}
