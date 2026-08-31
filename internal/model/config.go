package model

// ThemeConfig describes a named build-time theme root.
type ThemeConfig struct {
	Root string
}

// SiteConfig is the stable site-level configuration contract shared across packages.
// URL-like fields intentionally remain plain strings so html/template keeps contextual escaping.
type SiteConfig struct {
	Title           string
	BaseURL         string
	Author          string
	Description     string
	Language        string
	DefaultPublish  bool
	DefaultImg      string
	Themes          map[string]ThemeConfig
	DefaultTheme    string
	ActiveThemeName string
	ThemeRoot       string
	ThemeDir        string
	CustomCSS       string
	Pagination      PaginationConfig
	Sidebar         SidebarConfig
	Popover         PopoverConfig
	Related         RelatedConfig
	RSS             RSSConfig
	Timeline        TimelineConfig

	KaTeXCSSURL        string
	KaTeXJSURL         string
	KaTeXAutoRenderURL string
	MermaidJSURL       string
}

// PaginationConfig controls list-page pagination behavior.
type PaginationConfig struct {
	PageSize int
}

// SidebarConfig controls the optional collapsible sidebar file tree.
type SidebarConfig struct {
	Enabled bool
}

// PopoverConfig controls internal-link preview generation.
type PopoverConfig struct {
	Enabled bool
}

// RelatedConfig controls related-article generation.
type RelatedConfig struct {
	Enabled bool
	Count   int
}

// RSSConfig controls RSS feed emission.
type RSSConfig struct {
	Enabled bool
}

// TimelineConfig controls the recent-notes timeline page.
type TimelineConfig struct {
	Enabled    bool
	AsHomepage bool
	Path       string
}
