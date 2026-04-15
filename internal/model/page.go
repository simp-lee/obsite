package model

import (
	"html/template"
	"time"
)

// PageKind identifies the template variant represented by PageData.
type PageKind string

const (
	PageNote     PageKind = "note"
	PageTag      PageKind = "tag"
	PageIndex    PageKind = "index"
	Page404      PageKind = "404"
	PageFolder   PageKind = "folder"
	PageTimeline PageKind = "timeline"
)

// PageData is the shared template contract for all rendered pages.
type PageData struct {
	Kind        PageKind
	Site        SiteConfig
	SiteRootRel string

	Title       string
	TitleID     string
	Description string
	Slug        string
	Canonical   string
	RelPath     string

	Content         template.HTML
	TOC             []TOCEntry
	Date            time.Time
	LastModified    time.Time
	ReadingTime     string
	WordCount       int
	Tags            []TagLink
	Backlinks       []BacklinkEntry
	RelatedArticles []RelatedArticle
	HasMath         bool
	HasMermaid      bool
	HasSearch       bool
	HasCustomCSS    bool
	HasRSS          bool

	TagName        string
	TagNotes       []NoteSummary
	ChildTags      []TagLink
	FolderPath     string
	FolderChildren []NoteSummary

	RecentNotes   []NoteSummary
	TimelineNotes []NoteSummary
	Pagination    *PaginationData
	SidebarTree   []SidebarNode

	OG          OpenGraph
	TwitterCard string
	JSONLD      template.JS

	Breadcrumbs []Breadcrumb
}

// TagLink represents a tag link emitted into templates.
type TagLink struct {
	Name string
	Slug string
	URL  string
}

// BacklinkEntry represents a backlink listed on a note page.
type BacklinkEntry struct {
	Title string
	URL   string
}

// NoteSummary is the compact note representation used in list pages.
type NoteSummary struct {
	Title        string
	Summary      string
	URL          string
	Date         time.Time
	LastModified time.Time
	Tags         []TagLink
}

// TOCEntry represents a nested table-of-contents item for a note page.
type TOCEntry struct {
	Text     string
	ID       string
	Children []TOCEntry
}

// PageLink represents a numbered pagination destination.
type PageLink struct {
	Number int
	URL    string
}

// PaginationData contains page-navigation metadata for list pages.
type PaginationData struct {
	CurrentPage int
	TotalPages  int
	PrevURL     string
	NextURL     string
	Pages       []PageLink
}

// SidebarNode represents one node in the rendered sidebar file tree.
type SidebarNode struct {
	Name     string        `json:"name"`
	URL      string        `json:"url"`
	IsDir    bool          `json:"isDir"`
	IsActive bool          `json:"isActive"`
	Children []SidebarNode `json:"children,omitempty"`
}

// RelatedArticle contains precomputed related-content metadata for a note page.
type RelatedArticle struct {
	Title   string
	URL     string
	Summary string
	Score   float64
	Tags    []TagLink
}

// Breadcrumb represents one navigation breadcrumb item.
type Breadcrumb struct {
	Name string
	URL  string
}

// OpenGraph contains page-level Open Graph metadata.
type OpenGraph struct {
	Title       string
	Description string
	URL         string
	Image       string
	Type        string
}
