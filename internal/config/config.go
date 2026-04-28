package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	internalfsutil "github.com/simp-lee/obsite/internal/fsutil"
	"github.com/simp-lee/obsite/internal/model"
	"github.com/simp-lee/obsite/internal/render"
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
	Theme          string
	VaultPath      string
}

// LoadedSiteConfig carries the normalized site config after config-file and
// load-path defaults have been resolved.
type LoadedSiteConfig struct {
	Config model.SiteConfig
}

type fileConfig struct {
	Title          string                     `yaml:"title"`
	BaseURL        string                     `yaml:"baseURL"`
	Author         string                     `yaml:"author"`
	Description    string                     `yaml:"description"`
	Language       string                     `yaml:"language"`
	DefaultImg     string                     `yaml:"defaultImg"`
	DefaultPublish *bool                      `yaml:"defaultPublish"`
	Themes         map[string]fileThemeConfig `yaml:"themes"`
	DefaultTheme   string                     `yaml:"defaultTheme"`
	Search         searchFileConfig           `yaml:"search"`
	Pagination     paginationFileConfig       `yaml:"pagination"`
	Sidebar        enabledFileConfig          `yaml:"sidebar"`
	Popover        enabledFileConfig          `yaml:"popover"`
	Related        relatedFileConfig          `yaml:"related"`
	RSS            enabledFileConfig          `yaml:"rss"`
	Timeline       timelineFileConfig         `yaml:"timeline"`
}

type fileThemeConfig struct {
	Root string `yaml:"root"`
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

	loaded, err := applyLoadPathDefaults(normalized, paths, overrides.Theme)
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
		paths.configDir = normalizeAbsolutePath(filepath.Dir(trimmedConfigPath))
	}
	if trimmedVaultPath := strings.TrimSpace(overrides.VaultPath); trimmedVaultPath != "" {
		paths.vaultRoot = normalizeAbsolutePath(trimmedVaultPath)
	}

	return paths
}

func normalizeAbsolutePath(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}

	cleaned := filepath.Clean(trimmed)
	absPath, err := filepath.Abs(cleaned)
	if err != nil {
		return cleaned
	}

	return filepath.Clean(absPath)
}

func parseFileConfig(data []byte) (fileConfig, error) {
	if err := prevalidateFileConfigYAML(data); err != nil {
		return fileConfig{}, err
	}

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
	for name, theme := range parsed.Themes {
		trimmedName := strings.TrimSpace(name)
		if trimmedName == "" {
			return fmt.Errorf("themes contains an empty theme name")
		}
		if strings.TrimSpace(theme.Root) == "" {
			return fmt.Errorf("themes.%s.root must not be empty", trimmedName)
		}
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
	if len(parsed.Themes) > 0 {
		cfg.Themes = make(map[string]model.ThemeConfig, len(parsed.Themes))
		for name, theme := range parsed.Themes {
			cfg.Themes[strings.TrimSpace(name)] = model.ThemeConfig{Root: strings.TrimSpace(theme.Root)}
		}
	}
	if value := strings.TrimSpace(parsed.DefaultTheme); value != "" {
		cfg.DefaultTheme = value
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
	if len(cfg.Themes) > 0 {
		normalizedThemes := make(map[string]model.ThemeConfig, len(cfg.Themes))
		for name, theme := range cfg.Themes {
			trimmedName := strings.TrimSpace(name)
			if trimmedName == "" {
				return fmt.Errorf("themes contains an empty theme name")
			}
			if _, exists := normalizedThemes[trimmedName]; exists {
				return fmt.Errorf("themes contains duplicate theme name %q", trimmedName)
			}
			trimmedRoot := strings.TrimSpace(theme.Root)
			if trimmedRoot == "" {
				return fmt.Errorf("themes.%s.root must not be empty", trimmedName)
			}
			normalizedThemes[trimmedName] = model.ThemeConfig{Root: trimmedRoot}
		}
		cfg.Themes = normalizedThemes
	}
	cfg.DefaultTheme = strings.TrimSpace(cfg.DefaultTheme)
	cfg.ActiveThemeName = strings.TrimSpace(cfg.ActiveThemeName)
	cfg.ThemeRoot = strings.TrimSpace(cfg.ThemeRoot)
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

func applyLoadPathDefaults(cfg model.SiteConfig, paths loadPaths, selectedTheme string) (LoadedSiteConfig, error) {
	baseDir := paths.configDir
	if baseDir == "" {
		baseDir = paths.vaultRoot
	}

	if len(cfg.Themes) > 0 {
		resolvedThemes := make(map[string]model.ThemeConfig, len(cfg.Themes))
		for name, theme := range cfg.Themes {
			resolvedThemes[name] = model.ThemeConfig{Root: resolveConfiguredPath(theme.Root, paths.configDir)}
		}
		cfg.Themes = resolvedThemes
	}
	cfg.Search.PagefindPath = resolvePagefindPath(cfg.Search.PagefindPath, baseDir)

	detectedCSS, err := detectCustomCSS(paths.vaultRoot)
	if err != nil {
		return LoadedSiteConfig{}, err
	}
	cfg.CustomCSS = detectedCSS

	activeThemeName, err := resolveSelectedThemeName(cfg, selectedTheme)
	if err != nil {
		return LoadedSiteConfig{}, err
	}
	cfg.ActiveThemeName = activeThemeName
	if err := validateConfiguredThemeRoots(cfg.Themes, activeThemeName); err != nil {
		return LoadedSiteConfig{}, err
	}
	if activeThemeName == "" {
		cfg.ThemeRoot = ""
		return LoadedSiteConfig{Config: cfg}, nil
	}

	selectedThemeConfig := cfg.Themes[activeThemeName]
	cfg.ThemeRoot = strings.TrimSpace(selectedThemeConfig.Root)
	if err := validateSelectedThemeRoot(activeThemeName, cfg.ThemeRoot); err != nil {
		return LoadedSiteConfig{}, err
	}

	return LoadedSiteConfig{Config: cfg}, nil
}

func resolveConfiguredPath(raw string, baseDir string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	candidate := trimmed
	if !filepath.IsAbs(trimmed) && strings.TrimSpace(baseDir) != "" {
		candidate = filepath.Join(baseDir, trimmed)
	}

	return normalizeAbsolutePath(candidate)
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

func resolveSelectedThemeName(cfg model.SiteConfig, selectedTheme string) (string, error) {
	overrideTheme := strings.TrimSpace(selectedTheme)
	if overrideTheme != "" {
		if _, ok := cfg.Themes[overrideTheme]; !ok {
			return "", fmt.Errorf("theme %q was not found in themes", overrideTheme)
		}

		return overrideTheme, nil
	}

	if cfg.DefaultTheme != "" {
		if _, ok := cfg.Themes[cfg.DefaultTheme]; !ok {
			return "", fmt.Errorf("defaultTheme %q was not found in themes", cfg.DefaultTheme)
		}

		return cfg.DefaultTheme, nil
	}

	return "", nil
}

func validateConfiguredThemeRoots(themes map[string]model.ThemeConfig, selectedThemeName string) error {
	if len(themes) == 0 {
		return nil
	}

	names := make([]string, 0, len(themes))
	for name := range themes {
		if name == selectedThemeName {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		if err := validateConfiguredThemeRoot(name, themes[name].Root); err != nil {
			return err
		}
	}

	return nil
}

func validateConfiguredThemeRoot(themeName string, themeRoot string) error {
	trimmedRoot := normalizeAbsolutePath(themeRoot)
	if trimmedRoot == "" {
		return fmt.Errorf("theme %q root must not be empty", themeName)
	}

	info, err := os.Stat(trimmedRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("theme %q root %q does not exist", themeName, trimmedRoot)
		}

		return fmt.Errorf("stat theme %q root %q: %w", themeName, trimmedRoot, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("theme %q root %q is not a directory", themeName, trimmedRoot)
	}

	return nil
}

func validateSelectedThemeRoot(themeName string, themeRoot string) error {
	resolvedRoot, err := inspectSelectedThemeRoot(themeName, themeRoot)
	if err != nil {
		return err
	}

	missingTemplates, err := missingRequiredThemeTemplates(themeName, resolvedRoot)
	if err != nil {
		return err
	}
	if len(missingTemplates) > 0 {
		return fmt.Errorf(
			"selected theme %q root %q is missing required HTML templates: %s",
			themeName,
			resolvedRoot,
			strings.Join(missingTemplates, ", "),
		)
	}
	if err := validateOptionalThemeStyleFile(themeName, resolvedRoot); err != nil {
		return err
	}
	if err := validateAdditionalThemeOwnedFiles(themeName, resolvedRoot); err != nil {
		return err
	}

	return nil
}

func inspectSelectedThemeRoot(themeName string, themeRoot string) (string, error) {
	trimmedRoot := normalizeAbsolutePath(themeRoot)
	if trimmedRoot == "" {
		return "", fmt.Errorf("selected theme %q root must not be empty", themeName)
	}

	info, err := os.Lstat(trimmedRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("selected theme %q root %q does not exist", themeName, trimmedRoot)
		}

		return "", fmt.Errorf("stat selected theme %q root %q: %w", themeName, trimmedRoot, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("selected theme %q root %q must not be a symlink", themeName, trimmedRoot)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("selected theme %q root %q is not a directory", themeName, trimmedRoot)
	}

	return trimmedRoot, nil
}

func missingRequiredThemeTemplates(themeName string, themeRoot string) ([]string, error) {
	missing := make([]string, 0, len(render.RequiredHTMLTemplateNames))
	for _, name := range render.RequiredHTMLTemplateNames {
		filePath := filepath.Join(themeRoot, filepath.FromSlash(name))
		_, _, err := internalfsutil.InspectRegularNonSymlinkFile(filePath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				missing = append(missing, name)
				continue
			}
			if errors.Is(err, internalfsutil.ErrUnsupportedRegularFileSource) {
				return nil, fmt.Errorf(
					"selected theme %q root %q contains invalid required HTML template %q: must be a regular non-symlink file",
					themeName,
					themeRoot,
					name,
				)
			}

			return nil, fmt.Errorf("stat theme template %q: %w", filePath, err)
		}
	}

	return missing, nil
}

func validateOptionalThemeStyleFile(themeName string, themeRoot string) error {
	stylePath := filepath.Join(themeRoot, "style.css")
	_, _, err := internalfsutil.InspectRegularNonSymlinkFile(stylePath)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if errors.Is(err, internalfsutil.ErrUnsupportedRegularFileSource) {
		return fmt.Errorf(
			"selected theme %q root %q contains invalid theme stylesheet %q: must be a regular non-symlink file",
			themeName,
			themeRoot,
			"style.css",
		)
	}

	return fmt.Errorf("stat theme stylesheet %q: %w", stylePath, err)
}

func validateAdditionalThemeOwnedFiles(themeName string, themeRoot string) error {
	validatedNames := make(map[string]struct{}, len(render.RequiredHTMLTemplateNames)+1)
	for _, name := range render.RequiredHTMLTemplateNames {
		validatedNames[name] = struct{}{}
	}
	validatedNames["style.css"] = struct{}{}

	return filepath.WalkDir(themeRoot, func(currentPath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk selected theme %q root %q: %w", themeName, themeRoot, walkErr)
		}
		if entry == nil || entry.IsDir() {
			return nil
		}

		relPath, err := filepath.Rel(themeRoot, currentPath)
		if err != nil {
			return fmt.Errorf("relative theme-owned path %q: %w", currentPath, err)
		}
		relPath = filepath.ToSlash(relPath)
		if _, ok := validatedNames[relPath]; ok {
			return nil
		}
		if entry.Type().IsRegular() {
			return nil
		}

		if strings.EqualFold(path.Ext(relPath), ".html") {
			return fmt.Errorf(
				"selected theme %q root %q contains invalid optional HTML template %q: must be a regular non-symlink file",
				themeName,
				themeRoot,
				relPath,
			)
		}

		return fmt.Errorf(
			"selected theme %q root %q contains invalid theme static asset %q: must be a regular non-symlink file",
			themeName,
			themeRoot,
			relPath,
		)
	})
}

func requiredThemeTemplateNames() []string {
	return append([]string(nil), render.RequiredHTMLTemplateNames...)
}

func prevalidateFileConfigYAML(data []byte) error {
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return err
	}
	if len(document.Content) == 0 {
		return nil
	}

	root := document.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil
	}

	for index := 0; index+1 < len(root.Content); index += 2 {
		keyNode := root.Content[index]
		valueNode := root.Content[index+1]
		switch strings.TrimSpace(keyNode.Value) {
		case "templateDir":
			return fmt.Errorf("templateDir is no longer supported; use themes.<name>.root with defaultTheme or --theme")
		case "customCSS":
			return fmt.Errorf("customCSS is no longer supported; use a theme style.css or vault-root custom.css")
		case "themes":
			if err := prevalidateThemesNode(valueNode); err != nil {
				return err
			}
		}
	}

	return nil
}

func prevalidateThemesNode(node *yaml.Node) error {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}

	seen := make(map[string]struct{}, len(node.Content)/2)
	for index := 0; index+1 < len(node.Content); index += 2 {
		keyNode := node.Content[index]
		valueNode := node.Content[index+1]
		themeName := strings.TrimSpace(keyNode.Value)
		if themeName == "" {
			return fmt.Errorf("themes contains an empty theme name")
		}
		if _, exists := seen[themeName]; exists {
			return fmt.Errorf("themes contains duplicate theme name %q", themeName)
		}
		seen[themeName] = struct{}{}

		rootNode, ok := mappingValueByKey(valueNode, "root")
		if ok && rootNode.Kind == yaml.ScalarNode && strings.TrimSpace(rootNode.Value) == "" {
			return fmt.Errorf("themes.%s.root must not be empty", themeName)
		}
	}

	return nil
}

func mappingValueByKey(node *yaml.Node, key string) (*yaml.Node, bool) {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil, false
	}

	for index := 0; index+1 < len(node.Content); index += 2 {
		if strings.TrimSpace(node.Content[index].Value) == key {
			return node.Content[index+1], true
		}
	}

	return nil, false
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
