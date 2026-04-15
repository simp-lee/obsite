package model

import "time"

// Note describes a Markdown note discovered in a vault.
type Note struct {
	RelPath      string
	Frontmatter  Frontmatter
	LastModified time.Time

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

// Frontmatter holds the supported YAML frontmatter fields plus unknown extras.
type Frontmatter struct {
	Title       string
	Description string
	Date        time.Time
	Updated     time.Time
	Tags        []string
	Aliases     []string
	Publish     *bool
	Slug        string
	Extra       map[string]any
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
