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

	KaTeXCSSURL        string
	KaTeXJSURL         string
	KaTeXAutoRenderURL string
	MermaidJSURL       string
}
