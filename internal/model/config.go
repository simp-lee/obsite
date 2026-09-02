package model

// SiteConfig is the stable site-level configuration contract shared across packages.
// URL-like fields intentionally remain plain strings so html/template keeps contextual escaping.
type SiteConfig struct {
	Title              string
	BaseURL            string
	Author             string
	Description        string
	Language           string
	DefaultPublish     bool
	DefaultImg         string
	DefaultImgExternal bool
	ThemeDir           string
	ThemeCSS           string
	ThemeSlots         string
	CustomCSS          string
	Navigation         []NavigationItem
	Source             SourceConfig
	Versions           *VersionsConfig
	Pagination         PaginationConfig
	Sidebar            SidebarConfig
	Popover            PopoverConfig
	Related            RelatedConfig
	RSS                RSSConfig
	Timeline           TimelineConfig

	RuntimeJSURL string
}

// NavigationItem is one server-rendered global navigation entry. Exactly one
// of URL and Section is populated after configuration normalization.
type NavigationItem struct {
	Name    string
	URL     string
	Section string
}

// SourceConfig contains optional absolute source-link templates.
type SourceConfig struct {
	EditURL string
	ViewURL string
}

// VersionsConfig describes an explicitly configured set of document versions.
type VersionsConfig struct {
	Root    string
	Default string
	Entries []VersionEntry
}

// VersionEntry identifies one version source directory.
type VersionEntry struct {
	ID     string
	Label  string
	Source string
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
