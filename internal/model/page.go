package model

import (
	"html/template"
	"time"
)

// PageKind identifies the template variant represented by PageData.
type PageKind string

const (
	PageNote  PageKind = "note"
	PageTag   PageKind = "tag"
	PageIndex PageKind = "index"
	Page404   PageKind = "404"
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

	Content      template.HTML
	Date         time.Time
	LastModified time.Time
	Tags         []TagLink
	Backlinks    []BacklinkEntry
	HasMath      bool
	HasMermaid   bool

	TagName   string
	TagNotes  []NoteSummary
	ChildTags []TagLink

	RecentNotes []NoteSummary

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
	Title string
	URL   string
	Date  time.Time
	Tags  []TagLink
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
