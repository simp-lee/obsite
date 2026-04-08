package render

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/simp-lee/obsite/internal/diag"
	"github.com/simp-lee/obsite/internal/model"
)

func TestSiteRootRel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		relPath string
		want    string
	}{
		{name: "index root", relPath: "index.html", want: "./"},
		{name: "404 root", relPath: "404.html", want: "./"},
		{name: "note page", relPath: "guide/index.html", want: "../"},
		{name: "tag page", relPath: "tags/systems/index.html", want: "../../"},
		{name: "nested tag page", relPath: "tags/systems/distributed/index.html", want: "../../../"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := siteRootRel(tt.relPath)
			if got != tt.want {
				t.Fatalf("siteRootRel(%q) = %q, want %q", tt.relPath, got, tt.want)
			}
		})
	}
}

func TestCountWords(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		html      string
		wantLatin int
		wantCJK   int
	}{
		{
			name:      "latin html",
			html:      "<p>Hello <strong>world</strong> from obsite.</p>",
			wantLatin: 4,
			wantCJK:   0,
		},
		{
			name:      "cjk html",
			html:      "<p>你好世界</p>",
			wantLatin: 0,
			wantCJK:   4,
		},
		{
			name:      "mixed html",
			html:      "<p>Hello世界 from obsite</p>",
			wantLatin: 3,
			wantCJK:   2,
		},
		{
			name:      "empty html",
			html:      "",
			wantLatin: 0,
			wantCJK:   0,
		},
		{
			name:      "collapsed raw details keep summary only",
			html:      "<p>Visible intro.</p><details><summary>Expand</summary><p>Hidden body words.</p></details><p>Visible outro.</p>",
			wantLatin: 5,
			wantCJK:   0,
		},
		{
			name:      "collapsed callout details keep summary only",
			html:      "<p>Before.</p><details class=\"callout callout-tip\"><summary class=\"callout-title\">Heads up</summary><p>Hidden callout text.</p></details><p>After.</p>",
			wantLatin: 4,
			wantCJK:   0,
		},
		{
			name:      "open details remain visible",
			html:      "<details open><summary>Expand</summary><p>Visible body text.</p></details>",
			wantLatin: 4,
			wantCJK:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotLatin, gotCJK := CountWords(tt.html)
			if gotLatin != tt.wantLatin || gotCJK != tt.wantCJK {
				t.Fatalf("CountWords(%q) = (%d, %d), want (%d, %d)", tt.html, gotLatin, gotCJK, tt.wantLatin, tt.wantCJK)
			}
		})
	}
}

func TestFormatReadingTime(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		latinWords int
		cjkChars   int
		want       string
	}{
		{name: "empty", latinWords: 0, cjkChars: 0, want: ""},
		{name: "latin rounded up", latinWords: 201, cjkChars: 0, want: "2 min read"},
		{name: "cjk rounded up", latinWords: 0, cjkChars: 401, want: "2 min read"},
		{name: "mixed content", latinWords: 150, cjkChars: 150, want: "2 min read"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := FormatReadingTime(tt.latinWords, tt.cjkChars)
			if got != tt.want {
				t.Fatalf("FormatReadingTime(%d, %d) = %q, want %q", tt.latinWords, tt.cjkChars, got, tt.want)
			}
		})
	}
}

func TestRenderNoteComputesOutputPathAndAppliesSEO(t *testing.T) {
	t.Parallel()

	publishedAt := time.Date(2026, 4, 6, 9, 30, 0, 0, time.UTC)
	updatedAt := time.Date(2026, 4, 6, 12, 15, 0, 0, time.UTC)

	got, err := RenderNote(NotePageInput{
		Site: testSiteConfig(),
		Note: &model.Note{
			RelPath:      "notes/guide.md",
			Slug:         "guide",
			LastModified: updatedAt,
			HTMLContent:  "<p>Rendered note body.</p>",
			Summary:      "Summary from note body.",
			HasMath:      true,
			HasMermaid:   true,
			Frontmatter: model.Frontmatter{
				Title: "Guide",
				Date:  publishedAt,
			},
		},
		Tags:      []model.TagLink{{Name: "systems", Slug: "systems", URL: "../tags/systems/"}},
		Backlinks: []model.BacklinkEntry{{Title: "Index", URL: "../"}},
	})
	if err != nil {
		t.Fatalf("RenderNote() error = %v", err)
	}

	if got.Page.RelPath != "guide/index.html" {
		t.Fatalf("RenderNote().Page.RelPath = %q, want %q", got.Page.RelPath, "guide/index.html")
	}
	if got.Page.SiteRootRel != "../" {
		t.Fatalf("RenderNote().Page.SiteRootRel = %q, want %q", got.Page.SiteRootRel, "../")
	}
	if got.Page.Description != "Summary from note body." {
		t.Fatalf("RenderNote().Page.Description = %q, want %q", got.Page.Description, "Summary from note body.")
	}
	if got.Page.Canonical != "https://example.com/blog/guide/" {
		t.Fatalf("RenderNote().Page.Canonical = %q, want %q", got.Page.Canonical, "https://example.com/blog/guide/")
	}
	if got.Page.OG.Type != "article" {
		t.Fatalf("RenderNote().Page.OG.Type = %q, want %q", got.Page.OG.Type, "article")
	}
	if len(got.Page.Breadcrumbs) != 3 {
		t.Fatalf("len(RenderNote().Page.Breadcrumbs) = %d, want %d", len(got.Page.Breadcrumbs), 3)
	}
	if got.Page.Breadcrumbs[0].Name != "Home" || got.Page.Breadcrumbs[0].URL != "../" {
		t.Fatalf("RenderNote().Page.Breadcrumbs[0] = %#v, want Home breadcrumb to site root", got.Page.Breadcrumbs[0])
	}
	if got.Page.Breadcrumbs[1].Name != "notes" || got.Page.Breadcrumbs[1].URL != "../notes/" {
		t.Fatalf("RenderNote().Page.Breadcrumbs[1] = %#v, want containing folder breadcrumb", got.Page.Breadcrumbs[1])
	}
	if got.Page.Breadcrumbs[2].Name != "Guide" || got.Page.Breadcrumbs[2].URL != "" {
		t.Fatalf("RenderNote().Page.Breadcrumbs[2] = %#v, want current page breadcrumb", got.Page.Breadcrumbs[2])
	}
	if len(got.Page.Tags) != 1 || got.Page.Tags[0].URL != "../tags/systems/" {
		t.Fatalf("RenderNote().Page.Tags = %#v, want preserved note tags", got.Page.Tags)
	}
	if len(got.Page.Backlinks) != 1 || got.Page.Backlinks[0].URL != "../" {
		t.Fatalf("RenderNote().Page.Backlinks = %#v, want preserved backlinks", got.Page.Backlinks)
	}
	if !bytes.Contains(got.HTML, []byte("<h1 class=\"page-title\">Guide</h1>")) {
		t.Fatalf("RenderNote() HTML missing note title\n%s", got.HTML)
	}
	if !bytes.Contains(got.HTML, []byte("<a class=\"tag-pill\" href=\"../tags/systems/\">#systems</a>")) {
		t.Fatalf("RenderNote() HTML missing tag link\n%s", got.HTML)
	}
	if !bytes.Contains(got.HTML, []byte("<li><a href=\"../\">Index</a></li>")) {
		t.Fatalf("RenderNote() HTML missing backlink\n%s", got.HTML)
	}
	if !bytes.Contains(got.HTML, []byte("<a href=\"../notes/\">notes</a>")) {
		t.Fatalf("RenderNote() HTML missing note folder breadcrumb\n%s", got.HTML)
	}
	if !bytes.Contains(got.HTML, []byte("<script type=\"application/ld+json\">")) {
		t.Fatalf("RenderNote() HTML missing JSON-LD\n%s", got.HTML)
	}
}

func TestRenderNoteInjectsReadingTimeAndWordCount(t *testing.T) {
	t.Parallel()

	got, err := RenderNote(NotePageInput{
		Site: testSiteConfig(),
		Note: &model.Note{
			RelPath:     "notes/guide.md",
			Slug:        "guide",
			HTMLContent: "<h1 id=\"guide\">Guide</h1><p>Hello world.</p>",
			Headings: []model.Heading{{
				Level: 1,
				Text:  "Guide",
				ID:    "guide",
			}},
			Frontmatter: model.Frontmatter{
				Title: "Guide",
			},
		},
	})
	if err != nil {
		t.Fatalf("RenderNote() error = %v", err)
	}

	if got.Page.WordCount != 2 {
		t.Fatalf("RenderNote().Page.WordCount = %d, want %d", got.Page.WordCount, 2)
	}
	if got.Page.ReadingTime != "1 min read" {
		t.Fatalf("RenderNote().Page.ReadingTime = %q, want %q", got.Page.ReadingTime, "1 min read")
	}
	if !bytes.Contains(got.HTML, []byte("1 min read")) {
		t.Fatalf("RenderNote() HTML missing reading time\n%s", got.HTML)
	}
	if !bytes.Contains(got.HTML, []byte("<span class=\"meta-label\">Words / chars</span>")) {
		t.Fatalf("RenderNote() HTML missing word-count label\n%s", got.HTML)
	}
	if !bytes.Contains(got.HTML, []byte("<span>2</span>")) {
		t.Fatalf("RenderNote() HTML missing rendered word count\n%s", got.HTML)
	}
}

func TestRenderNoteClonesSidebarTree(t *testing.T) {
	t.Parallel()

	sidebarTree := []model.SidebarNode{{
		Name:  "notes",
		URL:   "notes/",
		IsDir: true,
		Children: []model.SidebarNode{{
			Name:     "Guide",
			URL:      "guide/",
			IsActive: true,
		}},
	}}

	got, err := RenderNote(NotePageInput{
		Site: testSiteConfig(),
		Note: &model.Note{
			RelPath:     "notes/guide.md",
			Slug:        "guide",
			HTMLContent: "<p>Rendered note body.</p>",
			Frontmatter: model.Frontmatter{Title: "Guide"},
		},
		SidebarTree: sidebarTree,
	})
	if err != nil {
		t.Fatalf("RenderNote() error = %v", err)
	}

	if len(got.Page.SidebarTree) != 1 {
		t.Fatalf("len(RenderNote().Page.SidebarTree) = %d, want %d", len(got.Page.SidebarTree), 1)
	}
	if got.Page.SidebarTree[0].Name != "notes" || !got.Page.SidebarTree[0].IsDir {
		t.Fatalf("RenderNote().Page.SidebarTree[0] = %#v, want directory sidebar node", got.Page.SidebarTree[0])
	}
	if len(got.Page.SidebarTree[0].Children) != 1 || !got.Page.SidebarTree[0].Children[0].IsActive {
		t.Fatalf("RenderNote().Page.SidebarTree children = %#v, want active nested note", got.Page.SidebarTree[0].Children)
	}

	sidebarTree[0].Name = "changed"
	sidebarTree[0].Children[0].Name = "changed child"
	if got.Page.SidebarTree[0].Name != "notes" {
		t.Fatalf("RenderNote().Page.SidebarTree[0].Name = %q after input mutation, want cloned value", got.Page.SidebarTree[0].Name)
	}
	if got.Page.SidebarTree[0].Children[0].Name != "Guide" {
		t.Fatalf("RenderNote().Page.SidebarTree[0].Children[0].Name = %q after input mutation, want cloned value", got.Page.SidebarTree[0].Children[0].Name)
	}
}

func TestRenderNoteCountsCollapsedCalloutSummaryOnly(t *testing.T) {
	t.Parallel()

	got, err := RenderNote(NotePageInput{
		Site: testSiteConfig(),
		Note: &model.Note{
			RelPath:     "notes/guide.md",
			Slug:        "guide",
			HTMLContent: "<h1 id=\"guide\">Guide</h1><p>Before.</p><details class=\"callout callout-tip\"><summary class=\"callout-title\">Heads up</summary><p>Hidden callout text.</p></details><p>After.</p>",
			Headings: []model.Heading{{
				Level: 1,
				Text:  "Guide",
				ID:    "guide",
			}},
			Frontmatter: model.Frontmatter{
				Title: "Guide",
			},
		},
	})
	if err != nil {
		t.Fatalf("RenderNote() error = %v", err)
	}

	if got.Page.WordCount != 4 {
		t.Fatalf("RenderNote().Page.WordCount = %d, want %d", got.Page.WordCount, 4)
	}
	if !bytes.Contains(got.HTML, []byte("<span>4</span>")) {
		t.Fatalf("RenderNote() HTML missing collapsed-callout word count\n%s", got.HTML)
	}
}

func TestRenderNoteBuildsNestedTOCFromHeadings(t *testing.T) {
	t.Parallel()

	got, err := RenderNote(NotePageInput{
		Site: testSiteConfig(),
		Note: &model.Note{
			RelPath: "notes/guide.md",
			Slug:    "guide",
			HTMLContent: `<h2 id="overview">Overview</h2>
<p>Intro.</p>
<h3 id="details">Details</h3>
<h4 id="deep-dive">Deep dive</h4>
<h2 id="appendix">Appendix</h2>`,
			Headings: []model.Heading{
				{Level: 2, Text: "Overview", ID: "overview"},
				{Level: 3, Text: "Details", ID: "details"},
				{Level: 4, Text: "Deep dive", ID: "deep-dive"},
				{Level: 2, Text: "Appendix", ID: "appendix"},
			},
			Frontmatter: model.Frontmatter{Title: "Guide"},
		},
	})
	if err != nil {
		t.Fatalf("RenderNote() error = %v", err)
	}

	if len(got.Page.TOC) != 2 {
		t.Fatalf("len(RenderNote().Page.TOC) = %d, want %d", len(got.Page.TOC), 2)
	}
	if got.Page.TOC[0].ID != "overview" || got.Page.TOC[0].Text != "Overview" {
		t.Fatalf("RenderNote().Page.TOC[0] = %#v, want overview root entry", got.Page.TOC[0])
	}
	if len(got.Page.TOC[0].Children) != 1 {
		t.Fatalf("len(RenderNote().Page.TOC[0].Children) = %d, want %d", len(got.Page.TOC[0].Children), 1)
	}
	if got.Page.TOC[0].Children[0].ID != "details" || got.Page.TOC[0].Children[0].Text != "Details" {
		t.Fatalf("RenderNote().Page.TOC[0].Children[0] = %#v, want nested details entry", got.Page.TOC[0].Children[0])
	}
	if len(got.Page.TOC[0].Children[0].Children) != 1 {
		t.Fatalf("len(RenderNote().Page.TOC[0].Children[0].Children) = %d, want %d", len(got.Page.TOC[0].Children[0].Children), 1)
	}
	if got.Page.TOC[0].Children[0].Children[0].ID != "deep-dive" {
		t.Fatalf("RenderNote().Page.TOC[0].Children[0].Children[0] = %#v, want deep-dive nested entry", got.Page.TOC[0].Children[0].Children[0])
	}
	if got.Page.TOC[1].ID != "appendix" || got.Page.TOC[1].Text != "Appendix" {
		t.Fatalf("RenderNote().Page.TOC[1] = %#v, want appendix root entry", got.Page.TOC[1])
	}
	if !bytes.Contains(got.HTML, []byte(`<nav class="toc-nav" aria-label="Contents">`)) {
		t.Fatalf("RenderNote() HTML missing ToC nav\n%s", got.HTML)
	}
	if bytes.Contains(got.HTML, []byte(`aria-labelledby="toc-heading"`)) {
		t.Fatalf("RenderNote() HTML unexpectedly references a fixed ToC heading id\n%s", got.HTML)
	}
	if !bytes.Contains(got.HTML, []byte(`<h2>Contents</h2>`)) {
		t.Fatalf("RenderNote() HTML missing visible ToC heading\n%s", got.HTML)
	}
	for _, want := range []string{`href="#overview"`, `href="#details"`, `href="#deep-dive"`, `href="#appendix"`} {
		if !bytes.Contains(got.HTML, []byte(want)) {
			t.Fatalf("RenderNote() HTML missing ToC link %q\n%s", want, got.HTML)
		}
	}
	if bytes.Count(got.HTML, []byte(`<ol class="toc-list">`)) != 3 {
		t.Fatalf("RenderNote() HTML ToC nesting depth unexpected\n%s", got.HTML)
	}
}

func TestRenderNoteTOCNavDoesNotCollideWithHeadingIDNamedTOCHeading(t *testing.T) {
	t.Parallel()

	got, err := RenderNote(NotePageInput{
		Site: testSiteConfig(),
		Note: &model.Note{
			RelPath: "notes/guide.md",
			Slug:    "guide",
			HTMLContent: `<h2 id="toc-heading">TOC Heading</h2>
<p>Intro.</p>
<h3 id="details">Details</h3>`,
			Headings: []model.Heading{
				{Level: 2, Text: "TOC Heading", ID: "toc-heading"},
				{Level: 3, Text: "Details", ID: "details"},
			},
			Frontmatter: model.Frontmatter{Title: "Guide"},
		},
	})
	if err != nil {
		t.Fatalf("RenderNote() error = %v", err)
	}

	if !bytes.Contains(got.HTML, []byte(`<nav class="toc-nav" aria-label="Contents">`)) {
		t.Fatalf("RenderNote() HTML missing accessible ToC nav label\n%s", got.HTML)
	}
	if bytes.Contains(got.HTML, []byte(`aria-labelledby="toc-heading"`)) {
		t.Fatalf("RenderNote() HTML unexpectedly reuses toc-heading for ToC chrome\n%s", got.HTML)
	}
	if bytes.Count(got.HTML, []byte(`id="toc-heading"`)) != 1 {
		t.Fatalf("RenderNote() HTML should expose exactly one toc-heading fragment target\n%s", got.HTML)
	}
	if !bytes.Contains(got.HTML, []byte(`href="#toc-heading"`)) {
		t.Fatalf("RenderNote() HTML missing real heading fragment link\n%s", got.HTML)
	}
	if !bytes.Contains([]byte(got.Page.Content), []byte(`<h2 id="toc-heading">TOC Heading</h2>`)) {
		t.Fatalf("RenderNote().Page.Content = %q, want preserved article heading target", got.Page.Content)
	}
}

func TestRenderNoteTOCOmitsPromotedDuplicateLeadingTitleHeading(t *testing.T) {
	t.Parallel()

	got, err := RenderNote(NotePageInput{
		Site: testSiteConfig(),
		Note: &model.Note{
			RelPath: "notes/guide.md",
			Slug:    "guide",
			HTMLContent: `<h1 id="guide">Guide</h1>
<h2 id="overview">Overview</h2>
<h3 id="details">Details</h3>`,
			Headings: []model.Heading{
				{Level: 1, Text: "Guide", ID: "guide"},
				{Level: 2, Text: "Overview", ID: "overview"},
				{Level: 3, Text: "Details", ID: "details"},
			},
			Frontmatter: model.Frontmatter{Title: "Guide"},
		},
	})
	if err != nil {
		t.Fatalf("RenderNote() error = %v", err)
	}

	if got.Page.TitleID != "guide" {
		t.Fatalf("RenderNote().Page.TitleID = %q, want %q", got.Page.TitleID, "guide")
	}
	if len(got.Page.TOC) != 1 {
		t.Fatalf("len(RenderNote().Page.TOC) = %d, want %d", len(got.Page.TOC), 1)
	}
	if got.Page.TOC[0].ID != "overview" {
		t.Fatalf("RenderNote().Page.TOC[0] = %#v, want overview root entry after duplicate title omission", got.Page.TOC[0])
	}
	if len(got.Page.TOC[0].Children) != 1 || got.Page.TOC[0].Children[0].ID != "details" {
		t.Fatalf("RenderNote().Page.TOC[0].Children = %#v, want nested details entry", got.Page.TOC[0].Children)
	}
	if bytes.Contains(got.HTML, []byte(`href="#guide"`)) {
		t.Fatalf("RenderNote() HTML unexpectedly links duplicate promoted title heading in ToC\n%s", got.HTML)
	}
	for _, want := range []string{`href="#overview"`, `href="#details"`} {
		if !bytes.Contains(got.HTML, []byte(want)) {
			t.Fatalf("RenderNote() HTML missing ToC link %q\n%s", want, got.HTML)
		}
	}
}

func TestRenderNoteKeepsDistinctLeadingH1WhenFrontmatterTitleIDCollides(t *testing.T) {
	t.Parallel()

	got, err := RenderNote(NotePageInput{
		Site: testSiteConfig(),
		Note: &model.Note{
			RelPath: "notes/c-sharp.md",
			Slug:    "c-sharp",
			HTMLContent: `<h1 id="c">C</h1>
<h2 id="pointers">Pointers</h2>`,
			Headings: []model.Heading{
				{Level: 1, Text: "C", ID: "c"},
				{Level: 2, Text: "Pointers", ID: "pointers"},
			},
			Frontmatter: model.Frontmatter{Title: "C#"},
		},
	})
	if err != nil {
		t.Fatalf("RenderNote() error = %v", err)
	}

	if got.Page.TitleID != "" {
		t.Fatalf("RenderNote().Page.TitleID = %q, want empty when body H1 is not a true title duplicate", got.Page.TitleID)
	}
	if len(got.Page.TOC) != 1 {
		t.Fatalf("len(RenderNote().Page.TOC) = %d, want %d", len(got.Page.TOC), 1)
	}
	if got.Page.TOC[0].ID != "c" || got.Page.TOC[0].Text != "C" {
		t.Fatalf("RenderNote().Page.TOC[0] = %#v, want preserved leading H1 entry", got.Page.TOC[0])
	}
	if len(got.Page.TOC[0].Children) != 1 || got.Page.TOC[0].Children[0].ID != "pointers" {
		t.Fatalf("RenderNote().Page.TOC[0].Children = %#v, want nested pointers entry", got.Page.TOC[0].Children)
	}
	if !bytes.Contains(got.HTML, []byte(`<h1 class="page-title">C#</h1>`)) {
		t.Fatalf("RenderNote() HTML missing frontmatter page title\n%s", got.HTML)
	}
	if bytes.Contains(got.HTML, []byte(`<h1 class="page-title" id="c">C#</h1>`)) {
		t.Fatalf("RenderNote() HTML unexpectedly promoted colliding body H1 id onto page title\n%s", got.HTML)
	}
	if !bytes.Contains([]byte(got.Page.Content), []byte(`<h1 id="c">C</h1>`)) {
		t.Fatalf("RenderNote().Page.Content = %q, want preserved body H1", got.Page.Content)
	}
	for _, want := range []string{`href="#c"`, `href="#pointers"`} {
		if !bytes.Contains(got.HTML, []byte(want)) {
			t.Fatalf("RenderNote() HTML missing preserved ToC link %q\n%s", want, got.HTML)
		}
	}
}

func TestRenderNoteKeepsDistinctLeadingH1WhenFilenameFallbackIDCollides(t *testing.T) {
	t.Parallel()

	got, err := RenderNote(NotePageInput{
		Site: testSiteConfig(),
		Note: &model.Note{
			RelPath: "notes/c.md",
			Slug:    "c",
			HTMLContent: `<h1 id="c">C++</h1>
<h2 id="operators">Operators</h2>`,
			Headings: []model.Heading{
				{Level: 1, Text: "C++", ID: "c"},
				{Level: 2, Text: "Operators", ID: "operators"},
			},
		},
	})
	if err != nil {
		t.Fatalf("RenderNote() error = %v", err)
	}

	if got.Page.TitleID != "" {
		t.Fatalf("RenderNote().Page.TitleID = %q, want empty when filename fallback and body H1 text are not equivalent", got.Page.TitleID)
	}
	if len(got.Page.TOC) != 1 {
		t.Fatalf("len(RenderNote().Page.TOC) = %d, want %d", len(got.Page.TOC), 1)
	}
	if got.Page.TOC[0].ID != "c" || got.Page.TOC[0].Text != "C++" {
		t.Fatalf("RenderNote().Page.TOC[0] = %#v, want preserved leading H1 entry", got.Page.TOC[0])
	}
	if len(got.Page.TOC[0].Children) != 1 || got.Page.TOC[0].Children[0].ID != "operators" {
		t.Fatalf("RenderNote().Page.TOC[0].Children = %#v, want nested operators entry", got.Page.TOC[0].Children)
	}
	if bytes.Contains(got.HTML, []byte(`<h1 class="page-title" id="c">`)) {
		t.Fatalf("RenderNote() HTML unexpectedly promoted colliding body H1 id onto filename fallback page title\n%s", got.HTML)
	}
	if !bytes.Contains([]byte(got.Page.Content), []byte(`<h1 id="c">C++</h1>`)) {
		t.Fatalf("RenderNote().Page.Content = %q, want preserved body H1", got.Page.Content)
	}
	for _, want := range []string{`href="#c"`, `href="#operators"`} {
		if !bytes.Contains(got.HTML, []byte(want)) {
			t.Fatalf("RenderNote() HTML missing preserved ToC link %q\n%s", want, got.HTML)
		}
	}
}

func TestRenderNotePromotesFilenameFallbackLeadingH1WhenDotsOnlySeparateWords(t *testing.T) {
	t.Parallel()

	got, err := RenderNote(NotePageInput{
		Site: testSiteConfig(),
		Note: &model.Note{
			RelPath: "notes/release.v2.md",
			Slug:    "release-v2",
			HTMLContent: `<h1 id="release-v2">Release v2</h1>
<h2 id="overview">Overview</h2>
<h3 id="details">Details</h3>`,
			Headings: []model.Heading{
				{Level: 1, Text: "Release v2", ID: "release-v2"},
				{Level: 2, Text: "Overview", ID: "overview"},
				{Level: 3, Text: "Details", ID: "details"},
			},
		},
	})
	if err != nil {
		t.Fatalf("RenderNote() error = %v", err)
	}

	if got.Page.TitleID != "release-v2" {
		t.Fatalf("RenderNote().Page.TitleID = %q, want %q", got.Page.TitleID, "release-v2")
	}
	if len(got.Page.TOC) != 1 {
		t.Fatalf("len(RenderNote().Page.TOC) = %d, want %d", len(got.Page.TOC), 1)
	}
	if got.Page.TOC[0].ID != "overview" {
		t.Fatalf("RenderNote().Page.TOC[0] = %#v, want overview root entry after dotted filename fallback title omission", got.Page.TOC[0])
	}
	if len(got.Page.TOC[0].Children) != 1 || got.Page.TOC[0].Children[0].ID != "details" {
		t.Fatalf("RenderNote().Page.TOC[0].Children = %#v, want nested details entry", got.Page.TOC[0].Children)
	}
	if !bytes.Contains(got.HTML, []byte(`<h1 class="page-title" id="release-v2">release.v2</h1>`)) {
		t.Fatalf("RenderNote() HTML missing promoted dotted filename fallback title id\n%s", got.HTML)
	}
	if bytes.Contains([]byte(got.Page.Content), []byte(`<h1 id="release-v2">Release v2</h1>`)) {
		t.Fatalf("RenderNote().Page.Content = %q, want duplicate dotted filename fallback H1 omitted", got.Page.Content)
	}
	if bytes.Contains(got.HTML, []byte(`href="#release-v2"`)) {
		t.Fatalf("RenderNote() HTML unexpectedly links dotted filename fallback duplicate title heading in ToC\n%s", got.HTML)
	}
	for _, want := range []string{`href="#overview"`, `href="#details"`} {
		if !bytes.Contains(got.HTML, []byte(want)) {
			t.Fatalf("RenderNote() HTML missing preserved ToC link %q\n%s", want, got.HTML)
		}
	}
}

func TestRenderNoteHidesTOCWhenFewerThanTwoUsableHeadings(t *testing.T) {
	t.Parallel()

	got, err := RenderNote(NotePageInput{
		Site: testSiteConfig(),
		Note: &model.Note{
			RelPath: "notes/guide.md",
			Slug:    "guide",
			HTMLContent: `<h1 id="guide">Guide</h1>
<p>Intro.</p>
<h2 id="overview">Overview</h2>`,
			Headings: []model.Heading{
				{Level: 1, Text: "Guide", ID: "guide"},
				{Level: 2, Text: "Overview", ID: "overview"},
			},
			Frontmatter: model.Frontmatter{Title: "Guide"},
		},
	})
	if err != nil {
		t.Fatalf("RenderNote() error = %v", err)
	}

	if len(got.Page.TOC) != 0 {
		t.Fatalf("RenderNote().Page.TOC = %#v, want empty ToC when fewer than two usable headings remain", got.Page.TOC)
	}
	if bytes.Contains(got.HTML, []byte(`class="toc-nav"`)) {
		t.Fatalf("RenderNote() HTML unexpectedly rendered ToC for too-few headings\n%s", got.HTML)
	}
}

func TestRenderNoteDegradesIncompleteArticleJSONLD(t *testing.T) {
	t.Parallel()

	got, err := RenderNote(NotePageInput{
		Site: testSiteConfig(),
		Note: &model.Note{
			RelPath:     "notes/sparse.md",
			Slug:        "sparse",
			HTMLContent: "<p></p>",
			Frontmatter: model.Frontmatter{
				Title: "Sparse",
			},
		},
	})
	if err != nil {
		t.Fatalf("RenderNote() error = %v, want incomplete Article JSON-LD to degrade", err)
	}
	if got.Page.Canonical != "https://example.com/blog/sparse/" {
		t.Fatalf("RenderNote().Page.Canonical = %q, want %q", got.Page.Canonical, "https://example.com/blog/sparse/")
	}
	if got.Page.JSONLD == "" {
		t.Fatal("RenderNote().Page.JSONLD = empty, want preserved partial JSON-LD")
	}
	if len(got.Diagnostics) != 1 {
		t.Fatalf("len(RenderNote().Diagnostics) = %d, want 1 warning", len(got.Diagnostics))
	}
	if got.Diagnostics[0].Severity != diag.SeverityWarning {
		t.Fatalf("RenderNote().Diagnostics[0].Severity = %q, want %q", got.Diagnostics[0].Severity, diag.SeverityWarning)
	}
	if got.Diagnostics[0].Kind != diag.KindStructuredData {
		t.Fatalf("RenderNote().Diagnostics[0].Kind = %q, want %q", got.Diagnostics[0].Kind, diag.KindStructuredData)
	}
	if got.Diagnostics[0].Location.Path != "notes/sparse.md" {
		t.Fatalf("RenderNote().Diagnostics[0].Location.Path = %q, want %q", got.Diagnostics[0].Location.Path, "notes/sparse.md")
	}
	if !bytes.Contains([]byte(got.Diagnostics[0].Message), []byte("article JSON-LD omitted")) {
		t.Fatalf("RenderNote().Diagnostics[0].Message = %q, want structured-data warning message", got.Diagnostics[0].Message)
	}
	if !bytes.Contains([]byte(got.Page.JSONLD), []byte(`"@type":"BreadcrumbList"`)) {
		t.Fatalf("RenderNote().Page.JSONLD = %s, want breadcrumb fallback", got.Page.JSONLD)
	}
	if bytes.Contains([]byte(got.Page.JSONLD), []byte(`"@type":"Article"`)) {
		t.Fatalf("RenderNote().Page.JSONLD = %s, want incomplete Article schema omitted", got.Page.JSONLD)
	}
	if !bytes.Contains(got.HTML, []byte("<h1 class=\"page-title\">Sparse</h1>")) {
		t.Fatalf("RenderNote() HTML missing note title after SEO degradation\n%s", got.HTML)
	}
}

func TestRenderNotePromotesDuplicateLeadingHeadingIDToPageTitle(t *testing.T) {
	t.Parallel()

	got, err := RenderNote(NotePageInput{
		Site: testSiteConfig(),
		Note: &model.Note{
			RelPath:     "notes/body-html.md",
			Slug:        "body-html",
			HTMLContent: "<h1 id=\"body-html\">Body HTML</h1>\n<p>Rendered note body.</p>",
			Headings: []model.Heading{{
				Level: 1,
				Text:  "Body HTML",
				ID:    "body-html",
			}},
		},
	})
	if err != nil {
		t.Fatalf("RenderNote() error = %v", err)
	}

	if !bytes.Contains(got.HTML, []byte("<h1 class=\"page-title\" id=\"body-html\">body-html</h1>")) {
		t.Fatalf("RenderNote() HTML missing promoted page-title id\n%s", got.HTML)
	}
	if bytes.Contains(got.HTML, []byte("<div class=\"entry-content\" data-page-content><h1")) {
		t.Fatalf("RenderNote() HTML still contains duplicate body h1\n%s", got.HTML)
	}
	if !bytes.Contains(got.HTML, []byte("<p>Rendered note body.</p>")) {
		t.Fatalf("RenderNote() HTML missing preserved body content\n%s", got.HTML)
	}
	if got.Page.TitleID != "body-html" {
		t.Fatalf("RenderNote().Page.TitleID = %q, want %q", got.Page.TitleID, "body-html")
	}
	if !bytes.Contains([]byte(got.Page.Content), []byte("Rendered note body.")) {
		t.Fatalf("RenderNote().Page.Content = %q, want body content without duplicate heading", got.Page.Content)
	}
}

func TestRenderNotePromotesDuplicateLeadingHeadingAfterLeadingHTMLComment(t *testing.T) {
	t.Parallel()

	got, err := RenderNote(NotePageInput{
		Site: testSiteConfig(),
		Note: &model.Note{
			RelPath: "notes/body-html.md",
			Slug:    "body-html",
			HTMLContent: `<!-- keep me -->
<h1 id="body-html">Body HTML</h1>
<h2 id="overview">Overview</h2>
<h3 id="details">Details</h3>
<p>Rendered note body.</p>`,
			Headings: []model.Heading{
				{Level: 1, Text: "Body HTML", ID: "body-html"},
				{Level: 2, Text: "Overview", ID: "overview"},
				{Level: 3, Text: "Details", ID: "details"},
			},
		},
	})
	if err != nil {
		t.Fatalf("RenderNote() error = %v", err)
	}

	if got.Page.TitleID != "body-html" {
		t.Fatalf("RenderNote().Page.TitleID = %q, want %q", got.Page.TitleID, "body-html")
	}
	if len(got.Page.TOC) != 1 {
		t.Fatalf("len(RenderNote().Page.TOC) = %d, want %d", len(got.Page.TOC), 1)
	}
	if got.Page.TOC[0].ID != "overview" {
		t.Fatalf("RenderNote().Page.TOC[0] = %#v, want overview root entry after duplicate title omission", got.Page.TOC[0])
	}
	if len(got.Page.TOC[0].Children) != 1 || got.Page.TOC[0].Children[0].ID != "details" {
		t.Fatalf("RenderNote().Page.TOC[0].Children = %#v, want nested details entry", got.Page.TOC[0].Children)
	}
	if !bytes.Contains(got.HTML, []byte(`<h1 class="page-title" id="body-html">body-html</h1>`)) {
		t.Fatalf("RenderNote() HTML missing promoted page-title id\n%s", got.HTML)
	}
	if !bytes.Contains([]byte(got.Page.Content), []byte(`<!-- keep me -->`)) {
		t.Fatalf("RenderNote().Page.Content = %q, want preserved leading HTML comment", got.Page.Content)
	}
	if bytes.Contains([]byte(got.Page.Content), []byte(`<h1 id="body-html">Body HTML</h1>`)) {
		t.Fatalf("RenderNote().Page.Content = %q, want duplicate body H1 omitted despite leading comment", got.Page.Content)
	}
	if bytes.Contains(got.HTML, []byte(`href="#body-html"`)) {
		t.Fatalf("RenderNote() HTML unexpectedly links duplicate promoted title heading in ToC\n%s", got.HTML)
	}
	for _, want := range []string{`href="#overview"`, `href="#details"`} {
		if !bytes.Contains(got.HTML, []byte(want)) {
			t.Fatalf("RenderNote() HTML missing ToC link %q\n%s", want, got.HTML)
		}
	}
}

func TestRenderNotePromotesDuplicateLeadingHeadingAfterLeadingInvisibleRawHTML(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		prefix string
	}{
		{name: "style", prefix: `<style>h1 { letter-spacing: 0.04em; }</style>`},
		{name: "script", prefix: `<script type="application/json">{"title":"Body HTML"}</script>`},
		{name: "template", prefix: `<template><div><strong>Hidden</strong> helper</div></template>`},
		{name: "meta", prefix: `<meta name="description" content="hidden prelude">`},
		{name: "link", prefix: `<link rel="preload" href="/style.css" as="style">`},
		{name: "hidden div", prefix: `<div hidden><div>Hidden helper</div><p>Still hidden</p></div>`},
		{name: "hidden span", prefix: `<span hidden><strong>Hidden</strong> inline helper</span>`},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got, err := RenderNote(NotePageInput{
				Site: testSiteConfig(),
				Note: &model.Note{
					RelPath: "notes/body-html.md",
					Slug:    "body-html",
					HTMLContent: tt.prefix + `
<h1 id="body-html">Body HTML</h1>
<h2 id="overview">Overview</h2>
<h3 id="details">Details</h3>
<p>Rendered note body.</p>`,
					Headings: []model.Heading{
						{Level: 1, Text: "Body HTML", ID: "body-html"},
						{Level: 2, Text: "Overview", ID: "overview"},
						{Level: 3, Text: "Details", ID: "details"},
					},
				},
			})
			if err != nil {
				t.Fatalf("RenderNote() error = %v", err)
			}

			if got.Page.TitleID != "body-html" {
				t.Fatalf("RenderNote().Page.TitleID = %q, want %q", got.Page.TitleID, "body-html")
			}
			if len(got.Page.TOC) != 1 {
				t.Fatalf("len(RenderNote().Page.TOC) = %d, want %d", len(got.Page.TOC), 1)
			}
			if got.Page.TOC[0].ID != "overview" {
				t.Fatalf("RenderNote().Page.TOC[0] = %#v, want overview root entry after duplicate title omission", got.Page.TOC[0])
			}
			if len(got.Page.TOC[0].Children) != 1 || got.Page.TOC[0].Children[0].ID != "details" {
				t.Fatalf("RenderNote().Page.TOC[0].Children = %#v, want nested details entry", got.Page.TOC[0].Children)
			}
			if !bytes.Contains(got.HTML, []byte(`<h1 class="page-title" id="body-html">body-html</h1>`)) {
				t.Fatalf("RenderNote() HTML missing promoted page-title id\n%s", got.HTML)
			}
			if !bytes.Contains([]byte(got.Page.Content), []byte(tt.prefix)) {
				t.Fatalf("RenderNote().Page.Content = %q, want preserved invisible raw HTML prefix", got.Page.Content)
			}
			if bytes.Contains([]byte(got.Page.Content), []byte(`<h1 id="body-html">Body HTML</h1>`)) {
				t.Fatalf("RenderNote().Page.Content = %q, want duplicate body H1 omitted despite leading invisible raw HTML", got.Page.Content)
			}
			if bytes.Contains(got.HTML, []byte(`href="#body-html"`)) {
				t.Fatalf("RenderNote() HTML unexpectedly links duplicate promoted title heading in ToC\n%s", got.HTML)
			}
			for _, want := range []string{`href="#overview"`, `href="#details"`} {
				if !bytes.Contains(got.HTML, []byte(want)) {
					t.Fatalf("RenderNote() HTML missing ToC link %q\n%s", want, got.HTML)
				}
			}
		})
	}
}

func TestRenderNoteDoesNotPromoteDuplicateLeadingHeadingAfterLeadingVisibleRawHTML(t *testing.T) {
	t.Parallel()

	got, err := RenderNote(NotePageInput{
		Site: testSiteConfig(),
		Note: &model.Note{
			RelPath: "notes/body-html.md",
			Slug:    "body-html",
			HTMLContent: `<div><p>Visible helper</p></div>
<h1 id="body-html">Body HTML</h1>
<h2 id="overview">Overview</h2>
<h3 id="details">Details</h3>
<p>Rendered note body.</p>`,
			Headings: []model.Heading{
				{Level: 1, Text: "Body HTML", ID: "body-html"},
				{Level: 2, Text: "Overview", ID: "overview"},
				{Level: 3, Text: "Details", ID: "details"},
			},
		},
	})
	if err != nil {
		t.Fatalf("RenderNote() error = %v", err)
	}

	if got.Page.TitleID != "" {
		t.Fatalf("RenderNote().Page.TitleID = %q, want empty when visible raw HTML precedes the duplicate-looking H1", got.Page.TitleID)
	}
	if len(got.Page.TOC) != 1 {
		t.Fatalf("len(RenderNote().Page.TOC) = %d, want %d", len(got.Page.TOC), 1)
	}
	if got.Page.TOC[0].ID != "body-html" || got.Page.TOC[0].Text != "Body HTML" {
		t.Fatalf("RenderNote().Page.TOC[0] = %#v, want preserved leading H1 entry", got.Page.TOC[0])
	}
	if len(got.Page.TOC[0].Children) != 1 || got.Page.TOC[0].Children[0].ID != "overview" {
		t.Fatalf("RenderNote().Page.TOC[0].Children = %#v, want nested overview entry", got.Page.TOC[0].Children)
	}
	if len(got.Page.TOC[0].Children[0].Children) != 1 || got.Page.TOC[0].Children[0].Children[0].ID != "details" {
		t.Fatalf("RenderNote().Page.TOC[0].Children[0].Children = %#v, want nested details entry", got.Page.TOC[0].Children[0].Children)
	}
	if !bytes.Contains([]byte(got.Page.Content), []byte(`<div><p>Visible helper</p></div>`)) {
		t.Fatalf("RenderNote().Page.Content = %q, want preserved visible raw HTML prefix", got.Page.Content)
	}
	if !bytes.Contains([]byte(got.Page.Content), []byte(`<h1 id="body-html">Body HTML</h1>`)) {
		t.Fatalf("RenderNote().Page.Content = %q, want preserved body H1 when visible raw HTML precedes it", got.Page.Content)
	}
	for _, want := range []string{`href="#body-html"`, `href="#overview"`, `href="#details"`} {
		if !bytes.Contains(got.HTML, []byte(want)) {
			t.Fatalf("RenderNote() HTML missing preserved ToC link %q\n%s", want, got.HTML)
		}
	}
}

func TestRenderTagPageComputesOutputPathAndDefaultsBreadcrumbs(t *testing.T) {
	t.Parallel()

	got, err := RenderTagPage(TagPageInput{
		Site: testSiteConfig(),
		Tag:  &model.Tag{Name: "systems/distributed/edge", Slug: "tags/systems/distributed/edge"},
		ChildTags: []model.TagLink{{
			Name: "systems/distributed/edge/runtime",
			Slug: "systems/distributed/edge/runtime",
			URL:  "runtime/",
		}},
		Notes: []model.NoteSummary{{
			Title: "Guide",
			URL:   "../../../../guide/",
			Date:  time.Date(2026, 4, 6, 9, 30, 0, 0, time.UTC),
		}},
		LastModified: time.Date(2026, 4, 6, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("RenderTagPage() error = %v", err)
	}

	if got.Page.RelPath != "tags/systems/distributed/edge/index.html" {
		t.Fatalf("RenderTagPage().Page.RelPath = %q, want %q", got.Page.RelPath, "tags/systems/distributed/edge/index.html")
	}
	if got.Page.SiteRootRel != "../../../../" {
		t.Fatalf("RenderTagPage().Page.SiteRootRel = %q, want %q", got.Page.SiteRootRel, "../../../../")
	}
	if got.Page.Canonical != "https://example.com/blog/tags/systems/distributed/edge/" {
		t.Fatalf("RenderTagPage().Page.Canonical = %q, want %q", got.Page.Canonical, "https://example.com/blog/tags/systems/distributed/edge/")
	}
	if got.Page.Description != testSiteConfig().Description {
		t.Fatalf("RenderTagPage().Page.Description = %q, want site description fallback %q", got.Page.Description, testSiteConfig().Description)
	}
	if len(got.Page.Breadcrumbs) != 4 {
		t.Fatalf("len(RenderTagPage().Page.Breadcrumbs) = %d, want %d", len(got.Page.Breadcrumbs), 4)
	}
	if got.Page.Breadcrumbs[0].URL != "../../../../" {
		t.Fatalf("RenderTagPage().Page.Breadcrumbs[0] = %#v, want home breadcrumb to site root", got.Page.Breadcrumbs[0])
	}
	if got.Page.Breadcrumbs[1].Name != "systems" || got.Page.Breadcrumbs[1].URL != "../../" {
		t.Fatalf("RenderTagPage().Page.Breadcrumbs[1] = %#v, want first ancestor tag breadcrumb", got.Page.Breadcrumbs[1])
	}
	if got.Page.Breadcrumbs[2].Name != "systems/distributed" || got.Page.Breadcrumbs[2].URL != "../" {
		t.Fatalf("RenderTagPage().Page.Breadcrumbs[2] = %#v, want second ancestor tag breadcrumb", got.Page.Breadcrumbs[2])
	}
	if got.Page.Breadcrumbs[3].Name != "systems/distributed/edge" || got.Page.Breadcrumbs[3].URL != "" {
		t.Fatalf("RenderTagPage().Page.Breadcrumbs[3] = %#v, want current tag without dead /tags/ link", got.Page.Breadcrumbs[3])
	}
	if !bytes.Contains(got.HTML, []byte("<a class=\"tag-pill\" href=\"runtime/\">#systems/distributed/edge/runtime</a>")) {
		t.Fatalf("RenderTagPage() HTML missing child tag link\n%s", got.HTML)
	}
	if !bytes.Contains(got.HTML, []byte("<a href=\"../../../../guide/\">Guide</a>")) {
		t.Fatalf("RenderTagPage() HTML missing note summary link\n%s", got.HTML)
	}
	if !bytes.Contains(got.HTML, []byte("<a href=\"../../\">systems</a>")) {
		t.Fatalf("RenderTagPage() HTML missing ancestor tag breadcrumb\n%s", got.HTML)
	}
	if !bytes.Contains(got.HTML, []byte("<a href=\"../\">systems/distributed</a>")) {
		t.Fatalf("RenderTagPage() HTML missing nested ancestor tag breadcrumb\n%s", got.HTML)
	}
	if bytes.Contains(got.HTML, []byte("<a href=\"../../\">Tags</a>")) {
		t.Fatalf("RenderTagPage() HTML unexpectedly links to an ungenerated tags landing page\n%s", got.HTML)
	}
}

func TestRenderFolderPageComputesOutputPathAndDefaultsBreadcrumbs(t *testing.T) {
	t.Parallel()

	got, err := RenderFolderPage(FolderPageInput{
		Site:       testSiteConfig(),
		FolderPath: "alpha/beta",
		Children: []model.NoteSummary{{
			Title: "Guide",
			URL:   "../../guide/",
			Date:  time.Date(2026, 4, 6, 9, 30, 0, 0, time.UTC),
			Tags:  []model.TagLink{{Name: "systems", Slug: "tags/systems", URL: "../../tags/systems/"}},
		}},
		LastModified: time.Date(2026, 4, 6, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("RenderFolderPage() error = %v", err)
	}

	if got.Page.Kind != model.PageFolder {
		t.Fatalf("RenderFolderPage().Page.Kind = %q, want %q", got.Page.Kind, model.PageFolder)
	}
	if got.Page.RelPath != "alpha/beta/index.html" {
		t.Fatalf("RenderFolderPage().Page.RelPath = %q, want %q", got.Page.RelPath, "alpha/beta/index.html")
	}
	if got.Page.SiteRootRel != "../../" {
		t.Fatalf("RenderFolderPage().Page.SiteRootRel = %q, want %q", got.Page.SiteRootRel, "../../")
	}
	if got.Page.Canonical != "https://example.com/blog/alpha/beta/" {
		t.Fatalf("RenderFolderPage().Page.Canonical = %q, want %q", got.Page.Canonical, "https://example.com/blog/alpha/beta/")
	}
	if got.Page.Title != "beta" {
		t.Fatalf("RenderFolderPage().Page.Title = %q, want %q", got.Page.Title, "beta")
	}
	if got.Page.FolderPath != "alpha/beta" {
		t.Fatalf("RenderFolderPage().Page.FolderPath = %q, want %q", got.Page.FolderPath, "alpha/beta")
	}
	if got.Page.Description != testSiteConfig().Description {
		t.Fatalf("RenderFolderPage().Page.Description = %q, want site description fallback %q", got.Page.Description, testSiteConfig().Description)
	}
	if len(got.Page.Breadcrumbs) != 3 {
		t.Fatalf("len(RenderFolderPage().Page.Breadcrumbs) = %d, want %d", len(got.Page.Breadcrumbs), 3)
	}
	if got.Page.Breadcrumbs[0].Name != "Home" || got.Page.Breadcrumbs[0].URL != "../../" {
		t.Fatalf("RenderFolderPage().Page.Breadcrumbs[0] = %#v, want home breadcrumb to site root", got.Page.Breadcrumbs[0])
	}
	if got.Page.Breadcrumbs[1].Name != "alpha" || got.Page.Breadcrumbs[1].URL != "../" {
		t.Fatalf("RenderFolderPage().Page.Breadcrumbs[1] = %#v, want parent folder breadcrumb", got.Page.Breadcrumbs[1])
	}
	if got.Page.Breadcrumbs[2].Name != "beta" || got.Page.Breadcrumbs[2].URL != "" {
		t.Fatalf("RenderFolderPage().Page.Breadcrumbs[2] = %#v, want current folder breadcrumb", got.Page.Breadcrumbs[2])
	}
	if !bytes.Contains(got.HTML, []byte("<h1 class=\"page-title\">beta</h1>")) {
		t.Fatalf("RenderFolderPage() HTML missing folder title\n%s", got.HTML)
	}
	if !bytes.Contains(got.HTML, []byte("<a href=\"../\">alpha</a>")) {
		t.Fatalf("RenderFolderPage() HTML missing parent folder breadcrumb\n%s", got.HTML)
	}
	if !bytes.Contains(got.HTML, []byte("<a href=\"../../guide/\">Guide</a>")) {
		t.Fatalf("RenderFolderPage() HTML missing note link\n%s", got.HTML)
	}
	if !bytes.Contains(got.HTML, []byte("<a class=\"tag-pill\" href=\"../../tags/systems/\">#systems</a>")) {
		t.Fatalf("RenderFolderPage() HTML missing tag link\n%s", got.HTML)
	}
}

func TestRenderTimelinePageComputesOutputPath(t *testing.T) {
	t.Parallel()

	got, err := RenderTimelinePage(TimelinePageInput{
		Site:         testSiteConfig(),
		TimelinePath: "timeline",
		Notes: []model.NoteSummary{{
			Title:   "Guide",
			Summary: "Guide summary.",
			URL:     "../guide/",
			Date:    time.Date(2026, 4, 6, 9, 30, 0, 0, time.UTC),
			Tags:    []model.TagLink{{Name: "systems", Slug: "tags/systems", URL: "../tags/systems/"}},
		}},
		LastModified: time.Date(2026, 4, 6, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("RenderTimelinePage() error = %v", err)
	}

	if got.Page.Kind != model.PageTimeline {
		t.Fatalf("RenderTimelinePage().Page.Kind = %q, want %q", got.Page.Kind, model.PageTimeline)
	}
	if got.Page.RelPath != "timeline/index.html" {
		t.Fatalf("RenderTimelinePage().Page.RelPath = %q, want %q", got.Page.RelPath, "timeline/index.html")
	}
	if got.Page.SiteRootRel != "../" {
		t.Fatalf("RenderTimelinePage().Page.SiteRootRel = %q, want %q", got.Page.SiteRootRel, "../")
	}
	if got.Page.Canonical != "https://example.com/blog/timeline/" {
		t.Fatalf("RenderTimelinePage().Page.Canonical = %q, want %q", got.Page.Canonical, "https://example.com/blog/timeline/")
	}
	if got.Page.Title != "Recent notes" {
		t.Fatalf("RenderTimelinePage().Page.Title = %q, want %q", got.Page.Title, "Recent notes")
	}
	if got.Page.Description != testSiteConfig().Description {
		t.Fatalf("RenderTimelinePage().Page.Description = %q, want site description fallback %q", got.Page.Description, testSiteConfig().Description)
	}
	if len(got.Page.Breadcrumbs) != 2 {
		t.Fatalf("len(RenderTimelinePage().Page.Breadcrumbs) = %d, want %d", len(got.Page.Breadcrumbs), 2)
	}
	if got.Page.Breadcrumbs[0].Name != "Home" || got.Page.Breadcrumbs[0].URL != "../" {
		t.Fatalf("RenderTimelinePage().Page.Breadcrumbs[0] = %#v, want home breadcrumb", got.Page.Breadcrumbs[0])
	}
	if got.Page.Breadcrumbs[1].Name != "Notes" || got.Page.Breadcrumbs[1].URL != "" {
		t.Fatalf("RenderTimelinePage().Page.Breadcrumbs[1] = %#v, want current timeline breadcrumb", got.Page.Breadcrumbs[1])
	}
	if !bytes.Contains(got.HTML, []byte("<p class=\"page-eyebrow\">Timeline</p>")) {
		t.Fatalf("RenderTimelinePage() HTML missing timeline eyebrow\n%s", got.HTML)
	}
	if !bytes.Contains(got.HTML, []byte("<span aria-current=\"page\">Notes</span>")) {
		t.Fatalf("RenderTimelinePage() HTML missing timeline breadcrumb current marker\n%s", got.HTML)
	}
	if !bytes.Contains(got.HTML, []byte("<a href=\"../guide/\">Guide</a>")) {
		t.Fatalf("RenderTimelinePage() HTML missing note link\n%s", got.HTML)
	}
	if !bytes.Contains(got.HTML, []byte("Guide summary.")) {
		t.Fatalf("RenderTimelinePage() HTML missing summary\n%s", got.HTML)
	}
	if !bytes.Contains(got.HTML, []byte("<a class=\"tag-pill\" href=\"../tags/systems/\">#systems</a>")) {
		t.Fatalf("RenderTimelinePage() HTML missing tag link\n%s", got.HTML)
	}
}

func TestRenderTimelinePageAsHomepageUsesRootCanonical(t *testing.T) {
	t.Parallel()

	got, err := RenderTimelinePage(TimelinePageInput{
		Site: testSiteConfig(),
		Notes: []model.NoteSummary{{
			Title: "Guide",
			URL:   "guide/",
		}},
		LastModified: time.Date(2026, 4, 6, 12, 0, 0, 0, time.UTC),
		AsHomepage:   true,
	})
	if err != nil {
		t.Fatalf("RenderTimelinePage() error = %v", err)
	}

	if got.Page.RelPath != "index.html" {
		t.Fatalf("RenderTimelinePage().Page.RelPath = %q, want %q", got.Page.RelPath, "index.html")
	}
	if got.Page.SiteRootRel != "./" {
		t.Fatalf("RenderTimelinePage().Page.SiteRootRel = %q, want %q", got.Page.SiteRootRel, "./")
	}
	if got.Page.Canonical != "https://example.com/blog/" {
		t.Fatalf("RenderTimelinePage().Page.Canonical = %q, want %q", got.Page.Canonical, "https://example.com/blog/")
	}
	if got.Page.Slug != "" {
		t.Fatalf("RenderTimelinePage().Page.Slug = %q, want empty root slug", got.Page.Slug)
	}
	if len(got.Page.Breadcrumbs) != 0 {
		t.Fatalf("len(RenderTimelinePage().Page.Breadcrumbs) = %d, want 0 in homepage mode", len(got.Page.Breadcrumbs))
	}
	if !bytes.Contains(got.HTML, []byte("<a href=\"guide/\">Guide</a>")) {
		t.Fatalf("RenderTimelinePage() HTML missing root-relative note link\n%s", got.HTML)
	}
}

func TestRenderIndexSupportsPaginatedRelPathAndPagination(t *testing.T) {
	t.Parallel()

	pagination := &model.PaginationData{
		CurrentPage: 2,
		TotalPages:  3,
		PrevURL:     "../../",
		NextURL:     "../3/",
		Pages: []model.PageLink{
			{Number: 1, URL: "../../"},
			{Number: 2, URL: "./"},
			{Number: 3, URL: "../3/"},
		},
	}

	got, err := RenderIndex(IndexPageInput{
		Site: testSiteConfig(),
		RecentNotes: []model.NoteSummary{{
			Title: "Guide",
			URL:   "../../guide/",
		}},
		LastModified: time.Date(2026, 4, 6, 12, 0, 0, 0, time.UTC),
		RelPath:      "page/2/index.html",
		Pagination:   pagination,
	})
	if err != nil {
		t.Fatalf("RenderIndex() error = %v", err)
	}

	if got.Page.RelPath != "page/2/index.html" {
		t.Fatalf("RenderIndex().Page.RelPath = %q, want %q", got.Page.RelPath, "page/2/index.html")
	}
	if got.Page.SiteRootRel != "../../" {
		t.Fatalf("RenderIndex().Page.SiteRootRel = %q, want %q", got.Page.SiteRootRel, "../../")
	}
	if got.Page.Canonical != "https://example.com/blog/page/2/" {
		t.Fatalf("RenderIndex().Page.Canonical = %q, want %q", got.Page.Canonical, "https://example.com/blog/page/2/")
	}
	if got.Page.Pagination == nil || got.Page.Pagination.CurrentPage != 2 || got.Page.Pagination.NextURL != "../3/" {
		t.Fatalf("RenderIndex().Page.Pagination = %#v, want cloned pagination data", got.Page.Pagination)
	}
	if !bytes.Contains(got.HTML, []byte(`<link rel="prev" href="../../">`)) {
		t.Fatalf("RenderIndex() HTML missing prev head link\n%s", got.HTML)
	}
	if !bytes.Contains(got.HTML, []byte(`<link rel="next" href="../3/">`)) {
		t.Fatalf("RenderIndex() HTML missing next head link\n%s", got.HTML)
	}
	if !bytes.Contains(got.HTML, []byte(`<a class="pagination-page" href="../../">1</a>`)) {
		t.Fatalf("RenderIndex() HTML missing numbered pagination link\n%s", got.HTML)
	}
	if !bytes.Contains(got.HTML, []byte(`<span class="pagination-page" aria-current="page">2</span>`)) {
		t.Fatalf("RenderIndex() HTML missing current-page marker\n%s", got.HTML)
	}
}

func TestRenderTagPageSupportsPaginatedRelPathAndPagination(t *testing.T) {
	t.Parallel()

	got, err := RenderTagPage(TagPageInput{
		Site: testSiteConfig(),
		Tag:  &model.Tag{Name: "systems", Slug: "tags/systems"},
		Notes: []model.NoteSummary{{
			Title: "Guide",
			URL:   "../../../../guide/",
		}},
		LastModified: time.Date(2026, 4, 6, 12, 0, 0, 0, time.UTC),
		RelPath:      "tags/systems/page/2/index.html",
		Pagination: &model.PaginationData{
			CurrentPage: 2,
			TotalPages:  2,
			PrevURL:     "../../",
			Pages:       []model.PageLink{{Number: 1, URL: "../../"}, {Number: 2, URL: "./"}},
		},
	})
	if err != nil {
		t.Fatalf("RenderTagPage() error = %v", err)
	}

	if got.Page.RelPath != "tags/systems/page/2/index.html" {
		t.Fatalf("RenderTagPage().Page.RelPath = %q, want %q", got.Page.RelPath, "tags/systems/page/2/index.html")
	}
	if got.Page.SiteRootRel != "../../../../" {
		t.Fatalf("RenderTagPage().Page.SiteRootRel = %q, want %q", got.Page.SiteRootRel, "../../../../")
	}
	if got.Page.Canonical != "https://example.com/blog/tags/systems/page/2/" {
		t.Fatalf("RenderTagPage().Page.Canonical = %q, want %q", got.Page.Canonical, "https://example.com/blog/tags/systems/page/2/")
	}
	if len(got.Page.Breadcrumbs) != 2 || got.Page.Breadcrumbs[0].URL != "../../../../" {
		t.Fatalf("RenderTagPage().Page.Breadcrumbs = %#v, want home breadcrumb rooted from paginated tag page", got.Page.Breadcrumbs)
	}
	if !bytes.Contains(got.HTML, []byte(`<link rel="prev" href="../../">`)) {
		t.Fatalf("RenderTagPage() HTML missing prev head link\n%s", got.HTML)
	}
	if !bytes.Contains(got.HTML, []byte(`<a class="pagination-link pagination-link-prev" href="../../" rel="prev">Previous</a>`)) {
		t.Fatalf("RenderTagPage() HTML missing prev navigation link\n%s", got.HTML)
	}
}

func TestRenderFolderPageSupportsPaginatedRelPathAndPagination(t *testing.T) {
	t.Parallel()

	got, err := RenderFolderPage(FolderPageInput{
		Site:       testSiteConfig(),
		FolderPath: "alpha/beta",
		Children: []model.NoteSummary{{
			Title: "Guide",
			URL:   "../../../../guide/",
		}},
		LastModified: time.Date(2026, 4, 6, 12, 0, 0, 0, time.UTC),
		RelPath:      "alpha/beta/page/2/index.html",
		Pagination: &model.PaginationData{
			CurrentPage: 2,
			TotalPages:  2,
			PrevURL:     "../../",
			Pages:       []model.PageLink{{Number: 1, URL: "../../"}, {Number: 2, URL: "./"}},
		},
	})
	if err != nil {
		t.Fatalf("RenderFolderPage() error = %v", err)
	}

	if got.Page.RelPath != "alpha/beta/page/2/index.html" {
		t.Fatalf("RenderFolderPage().Page.RelPath = %q, want %q", got.Page.RelPath, "alpha/beta/page/2/index.html")
	}
	if got.Page.SiteRootRel != "../../../../" {
		t.Fatalf("RenderFolderPage().Page.SiteRootRel = %q, want %q", got.Page.SiteRootRel, "../../../../")
	}
	if got.Page.Canonical != "https://example.com/blog/alpha/beta/page/2/" {
		t.Fatalf("RenderFolderPage().Page.Canonical = %q, want %q", got.Page.Canonical, "https://example.com/blog/alpha/beta/page/2/")
	}
	if len(got.Page.Breadcrumbs) != 3 {
		t.Fatalf("len(RenderFolderPage().Page.Breadcrumbs) = %d, want %d", len(got.Page.Breadcrumbs), 3)
	}
	if got.Page.Breadcrumbs[0].URL != "../../../../" {
		t.Fatalf("RenderFolderPage().Page.Breadcrumbs[0] = %#v, want home breadcrumb rooted from paginated folder page", got.Page.Breadcrumbs[0])
	}
	if got.Page.Breadcrumbs[1].Name != "alpha" || got.Page.Breadcrumbs[1].URL != "../../../" {
		t.Fatalf("RenderFolderPage().Page.Breadcrumbs[1] = %#v, want parent folder breadcrumb rooted from paginated folder page", got.Page.Breadcrumbs[1])
	}
	if got.Page.Breadcrumbs[2].Name != "beta" || got.Page.Breadcrumbs[2].URL != "" {
		t.Fatalf("RenderFolderPage().Page.Breadcrumbs[2] = %#v, want current folder breadcrumb on paginated page", got.Page.Breadcrumbs[2])
	}
	if !bytes.Contains(got.HTML, []byte(`<link rel="prev" href="../../">`)) {
		t.Fatalf("RenderFolderPage() HTML missing prev head link\n%s", got.HTML)
	}
	if !bytes.Contains(got.HTML, []byte(`<a class="pagination-link pagination-link-prev" href="../../" rel="prev">Previous</a>`)) {
		t.Fatalf("RenderFolderPage() HTML missing prev navigation link\n%s", got.HTML)
	}
}

func TestRenderTimelinePageSupportsPaginatedRelPathAndPagination(t *testing.T) {
	t.Parallel()

	got, err := RenderTimelinePage(TimelinePageInput{
		Site:         testSiteConfig(),
		TimelinePath: "notes",
		Notes: []model.NoteSummary{{
			Title: "Guide",
			URL:   "../../../guide/",
		}},
		LastModified: time.Date(2026, 4, 6, 12, 0, 0, 0, time.UTC),
		RelPath:      "notes/page/2/index.html",
		Pagination: &model.PaginationData{
			CurrentPage: 2,
			TotalPages:  2,
			PrevURL:     "../../",
			Pages:       []model.PageLink{{Number: 1, URL: "../../"}, {Number: 2, URL: "./"}},
		},
	})
	if err != nil {
		t.Fatalf("RenderTimelinePage() error = %v", err)
	}

	if got.Page.RelPath != "notes/page/2/index.html" {
		t.Fatalf("RenderTimelinePage().Page.RelPath = %q, want %q", got.Page.RelPath, "notes/page/2/index.html")
	}
	if got.Page.SiteRootRel != "../../../" {
		t.Fatalf("RenderTimelinePage().Page.SiteRootRel = %q, want %q", got.Page.SiteRootRel, "../../../")
	}
	if got.Page.Canonical != "https://example.com/blog/notes/page/2/" {
		t.Fatalf("RenderTimelinePage().Page.Canonical = %q, want %q", got.Page.Canonical, "https://example.com/blog/notes/page/2/")
	}
	if len(got.Page.Breadcrumbs) != 2 {
		t.Fatalf("len(RenderTimelinePage().Page.Breadcrumbs) = %d, want %d", len(got.Page.Breadcrumbs), 2)
	}
	if got.Page.Breadcrumbs[0].Name != "Home" || got.Page.Breadcrumbs[0].URL != "../../../" {
		t.Fatalf("RenderTimelinePage().Page.Breadcrumbs[0] = %#v, want home breadcrumb rooted from paginated timeline page", got.Page.Breadcrumbs[0])
	}
	if got.Page.Breadcrumbs[1].Name != "Notes" || got.Page.Breadcrumbs[1].URL != "" {
		t.Fatalf("RenderTimelinePage().Page.Breadcrumbs[1] = %#v, want current timeline breadcrumb on paginated page", got.Page.Breadcrumbs[1])
	}
	if !bytes.Contains(got.HTML, []byte(`<link rel="prev" href="../../">`)) {
		t.Fatalf("RenderTimelinePage() HTML missing prev head link\n%s", got.HTML)
	}
	if !bytes.Contains(got.HTML, []byte(`<a class="pagination-link pagination-link-prev" href="../../" rel="prev">Previous</a>`)) {
		t.Fatalf("RenderTimelinePage() HTML missing prev navigation link\n%s", got.HTML)
	}
}

func TestRenderIndexAnd404UseEmbeddedTemplates(t *testing.T) {
	t.Parallel()

	index, err := RenderIndex(IndexPageInput{
		Site: testSiteConfig(),
		RecentNotes: []model.NoteSummary{{
			Title: "Guide",
			URL:   "guide/",
			Date:  time.Date(2026, 4, 6, 9, 30, 0, 0, time.UTC),
			Tags:  []model.TagLink{{Name: "systems", Slug: "systems", URL: "tags/systems/"}},
		}},
		LastModified: time.Date(2026, 4, 6, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("RenderIndex() error = %v", err)
	}

	if index.Page.RelPath != "index.html" {
		t.Fatalf("RenderIndex().Page.RelPath = %q, want %q", index.Page.RelPath, "index.html")
	}
	if index.Page.SiteRootRel != "./" {
		t.Fatalf("RenderIndex().Page.SiteRootRel = %q, want %q", index.Page.SiteRootRel, "./")
	}
	if index.Page.Canonical != "https://example.com/blog/" {
		t.Fatalf("RenderIndex().Page.Canonical = %q, want %q", index.Page.Canonical, "https://example.com/blog/")
	}
	if !bytes.Contains(index.HTML, []byte("<link rel=\"stylesheet\" href=\"./style.css\">")) {
		t.Fatalf("RenderIndex() HTML missing embedded template stylesheet link\n%s", index.HTML)
	}
	if !bytes.Contains(index.HTML, []byte("<a href=\"guide/\">Guide</a>")) {
		t.Fatalf("RenderIndex() HTML missing recent note link\n%s", index.HTML)
	}

	notFound, err := Render404(NotFoundPageInput{
		Site: testSiteConfig(),
		RecentNotes: []model.NoteSummary{{
			Title: "Guide",
			URL:   "guide/",
		}},
		LastModified: time.Date(2026, 4, 6, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Render404() error = %v", err)
	}

	if notFound.Page.RelPath != "404.html" {
		t.Fatalf("Render404().Page.RelPath = %q, want %q", notFound.Page.RelPath, "404.html")
	}
	if notFound.Page.SiteRootRel != "./" {
		t.Fatalf("Render404().Page.SiteRootRel = %q, want %q", notFound.Page.SiteRootRel, "./")
	}
	if notFound.Page.Canonical != "https://example.com/blog/404.html" {
		t.Fatalf("Render404().Page.Canonical = %q, want %q", notFound.Page.Canonical, "https://example.com/blog/404.html")
	}
	if notFound.Page.Description != "The requested page could not be found." {
		t.Fatalf("Render404().Page.Description = %q, want %q", notFound.Page.Description, "The requested page could not be found.")
	}
	if !bytes.Contains(notFound.HTML, []byte("<a class=\"action-link\" href=\"./\">Return to the homepage</a>")) {
		t.Fatalf("Render404() HTML missing home action link\n%s", notFound.HTML)
	}
	if !bytes.Contains(notFound.HTML, []byte("<li><a href=\"guide/\">Guide</a></li>")) {
		t.Fatalf("Render404() HTML missing recent note suggestion\n%s", notFound.HTML)
	}
}

func TestEmitStyleCSSWritesEmbeddedStylesheet(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	if err := EmitStyleCSS(outputDir, model.SiteConfig{}); err != nil {
		t.Fatalf("EmitStyleCSS() error = %v", err)
	}

	got, err := os.ReadFile(filepath.Join(outputDir, "style.css"))
	if err != nil {
		t.Fatalf("os.ReadFile(style.css) error = %v", err)
	}

	want, err := readEmbeddedAsset("style.css")
	if err != nil {
		t.Fatalf("readEmbeddedAsset(style.css) error = %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("EmitStyleCSS() wrote unexpected content")
	}
	if len(got) == 0 {
		t.Fatal("EmitStyleCSS() wrote empty stylesheet")
	}
}

func TestEmitStyleCSSUsesTemplateDirOverride(t *testing.T) {
	t.Parallel()

	templateDir := t.TempDir()
	want := []byte("body{font-size:1rem}")
	if err := os.WriteFile(filepath.Join(templateDir, "style.css"), want, 0o644); err != nil {
		t.Fatalf("os.WriteFile(style.css) error = %v", err)
	}

	outputDir := t.TempDir()
	if err := EmitStyleCSS(outputDir, model.SiteConfig{TemplateDir: templateDir}); err != nil {
		t.Fatalf("EmitStyleCSS() error = %v", err)
	}

	got, err := os.ReadFile(filepath.Join(outputDir, "style.css"))
	if err != nil {
		t.Fatalf("os.ReadFile(style.css) error = %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("EmitStyleCSS() wrote %q, want %q", got, want)
	}
}

func testSiteConfig() model.SiteConfig {
	return model.SiteConfig{
		Title:              "Field Notes",
		BaseURL:            "https://example.com/blog/",
		Author:             "Alice Example",
		Description:        "An editorial notebook.",
		Language:           "en",
		DefaultImg:         "images/default-og.png",
		KaTeXCSSURL:        "https://cdn.example.test/katex.css",
		KaTeXJSURL:         "https://cdn.example.test/katex.js",
		KaTeXAutoRenderURL: "https://cdn.example.test/auto-render.js",
		MermaidJSURL:       "https://cdn.example.test/mermaid.esm.min.mjs",
	}
}
