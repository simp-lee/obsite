package model

import "time"

// Section is a normalized section node. SourcePath always names its
// _index.md; an _index.md is never represented as a Note.
type Section struct {
	RelPath          string
	SourcePath       string
	Route            string
	Title            string
	Description      string
	Publish          bool
	EffectivePublish bool
	Order            *int
	Banner           string
	BannerURL        string
	BannerAlt        string
	RawContent       []byte
	BodyStartLine    int
	LastModified     time.Time
	VersionID        string
	VersionRoutes    map[string]string
	HiddenBy         string
	Parent           *Section
	Children         []*Section
	Articles         []*Note
	Documents        []*Note
	Posts            []*Note
	Pages            []*Note
	Breadcrumbs      []Breadcrumb
}

// Version is one independently planned version tree.
type Version struct {
	ID       string
	Label    string
	Source   string
	Root     *Section
	Sections []*Section
}

// SitePlan is the immutable normalized handoff from vault analysis to later
// validation, asset planning, and rendering. Slices are sorted by the planner;
// consumers must treat the pointed-to values as read-only.
type SitePlan struct {
	VaultPath      string
	Config         SiteConfig
	Root           *Section
	Sections       []*Section
	Articles       []*Note
	Documents      []*Note
	Posts          []*Note
	Pages          []*Note
	Versions       []*Version
	Routes         map[string]string
	ReservedRoutes map[string]struct{}
}

// PlannedSource keeps the strict parse result for either an article or a
// section source. It is useful to diagnostics before publication filtering.
type PlannedSource struct {
	RelPath string
	Article *Note
	Section *SectionSource
	Publish bool
}
