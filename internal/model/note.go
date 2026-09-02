package model

import "time"

// Note describes a Markdown note discovered in a vault.
type Note struct {
	RelPath      string
	Frontmatter  Frontmatter
	LastModified time.Time

	// The planning fields are assigned once by the strict site planner and are
	// intentionally kept on the canonical note handed to later render phases.
	Route           string
	SectionPath     string
	VersionID       string
	VersionRoutes   map[string]string
	Slug            string
	Aliases         []string
	Tags            []string
	Headings        []Heading
	HeadingSections map[string]SectionRange

	RawContent    []byte
	BodyStartLine int
	HTMLContent   string
	Summary       string

	OutLinks  []LinkRef
	Embeds    []EmbedRef
	ImageRefs []ImageRef

	HasMath    bool
	HasMermaid bool
}

// PublishedAt returns the best available article timestamp for this note.
func (n *Note) PublishedAt() time.Time {
	if n == nil {
		return time.Time{}
	}
	if !n.Frontmatter.Date.IsZero() {
		return n.Frontmatter.Date
	}

	return n.LastModified
}

// LessRecentNote orders notes by published time descending, then slug ascending.
func LessRecentNote(left *Note, right *Note) bool {
	leftDate := time.Time{}
	rightDate := time.Time{}
	if left != nil {
		leftDate = left.PublishedAt()
	}
	if right != nil {
		rightDate = right.PublishedAt()
	}

	switch {
	case leftDate.IsZero() && !rightDate.IsZero():
		return false
	case !leftDate.IsZero() && rightDate.IsZero():
		return true
	case !leftDate.Equal(rightDate):
		return leftDate.After(rightDate)
	}

	leftSlug := noteSortKey(left)
	rightSlug := noteSortKey(right)
	if leftSlug != rightSlug {
		return leftSlug < rightSlug
	}

	leftPath := ""
	rightPath := ""
	if left != nil {
		leftPath = left.RelPath
	}
	if right != nil {
		rightPath = right.RelPath
	}
	return leftPath < rightPath
}

func noteSortKey(note *Note) string {
	if note == nil {
		return ""
	}
	if note.Slug != "" {
		return note.Slug
	}
	return note.RelPath
}

// SectionRange identifies a source slice within Note.RawContent.
type SectionRange struct {
	StartOffset int
	EndOffset   int
}

// Frontmatter holds article metadata. Extra remains populated by the legacy
// parser only; strict planning rejects unknown fields before constructing this
// model.
type Frontmatter struct {
	Title          string
	Description    string
	Date           time.Time
	Updated        time.Time
	Tags           []string
	Aliases        []string
	Publish        *bool
	Slug           string
	Type           string
	Order          *int
	Author         string
	Reviewed       time.Time
	Status         string
	Audience       string
	ProductVersion string
	Series         string
	Cover          string
	Banner         string
	BannerAlt      string
	Extra          map[string]any
}

// SectionFrontmatter is the deliberately smaller schema used by _index.md.
type SectionFrontmatter struct {
	Title       string
	Description string
	Publish     *bool
	Order       *int
	Banner      string
	BannerAlt   string
}

// SectionSource is a parsed _index.md source, kept separate from articles.
type SectionSource struct {
	RelPath       string
	SectionPath   string
	Frontmatter   SectionFrontmatter
	RawContent    []byte
	BodyStartLine int
	LastModified  time.Time
}

// Heading captures a heading extracted from Markdown.
type Heading struct {
	Level int
	Text  string
	ID    string
}

// LinkRef records an outbound wikilink across extraction and resolution phases.
type LinkRef struct {
	// RawTarget is captured from the source wikilink during AST extraction.
	RawTarget string
	// ResolvedRelPath is filled on render-time link copies once RawTarget matches a note.
	ResolvedRelPath string
	Display         string
	Fragment        string
	Line            int
	Offset          int
}

// EmbedRef records an embed reference discovered during parsing.
type EmbedRef struct {
	Target   string
	Fragment string
	IsImage  bool
	Width    int
	Line     int
	Offset   int
}

// ImageRef records a standard Markdown image reference discovered during parsing.
type ImageRef struct {
	RawTarget string
	Line      int
	Offset    int
}

// Tag represents a tag and the notes currently associated with it.
type Tag struct {
	Name  string
	Slug  string
	Notes []string
}

// Asset represents a non-Markdown resource referenced by notes.
type Asset struct {
	SrcPath  string
	DstPath  string
	RefCount int
}
