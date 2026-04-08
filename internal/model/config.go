package model

// SiteConfig is the stable site-level configuration contract shared across packages.
// URL-like fields intentionally remain plain strings so html/template keeps contextual escaping.
type SiteConfig struct {
	Title       string
	BaseURL     string
	Author      string
	Description string
	Language    string
	// DefaultPublish carries the explicit site-wide fallback chosen by config loading
	// when a note omits frontmatter publish. DefaultPublishSet distinguishes an
	// explicit false from an omitted zero value when callers construct SiteConfig directly.
	DefaultPublish    bool
	DefaultPublishSet bool
	DefaultImg        string
	TemplateDir       string
	CustomCSS         string
	Search            SearchConfig
	Pagination        PaginationConfig
	Sidebar           SidebarConfig
	Popover           PopoverConfig
	Related           RelatedConfig
	RSS               RSSConfig
	Timeline          TimelineConfig

	KaTeXCSSURL        string
	KaTeXJSURL         string
	KaTeXAutoRenderURL string
	MermaidJSURL       string
}

// SearchConfig configures optional Pagefind-based search output.
type SearchConfig struct {
	Enabled         bool
	PagefindPath    string
	PagefindVersion string
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
	// EnabledSet distinguishes an explicit false from an omitted zero value when
	// callers construct SiteConfig directly.
	Enabled    bool
	EnabledSet bool
}

// TimelineConfig controls the recent-notes timeline page.
type TimelineConfig struct {
	Enabled    bool
	AsHomepage bool
	Path       string
}
