package model

// SiteConfig is the stable site-level configuration contract shared across packages.
// URL-like fields intentionally remain plain strings so html/template keeps contextual escaping.
// DefaultPublishSet and RSS.EnabledSet disambiguate explicit false from shared
// product defaults, which resolve to true when unset.
type SiteConfig struct {
	Title             string
	BaseURL           string
	Author            string
	Description       string
	Language          string
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
	Enabled    bool
	EnabledSet bool
}

// TimelineConfig controls the recent-notes timeline page.
type TimelineConfig struct {
	Enabled    bool
	AsHomepage bool
	Path       string
}

// EffectiveDefaultPublish returns the resolved publish policy for notes that do
// not set frontmatter publish.
func (cfg SiteConfig) EffectiveDefaultPublish() bool {
	if cfg.DefaultPublishSet {
		return cfg.DefaultPublish
	}

	return true
}

// EffectiveRSSEnabled returns the resolved RSS policy for the site.
func (cfg SiteConfig) EffectiveRSSEnabled() bool {
	if cfg.RSS.EnabledSet {
		return cfg.RSS.Enabled
	}

	return true
}
