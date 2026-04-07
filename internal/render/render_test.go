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
	if len(got.Page.Breadcrumbs) != 2 {
		t.Fatalf("len(RenderNote().Page.Breadcrumbs) = %d, want %d", len(got.Page.Breadcrumbs), 2)
	}
	if got.Page.Breadcrumbs[0].Name != "Home" || got.Page.Breadcrumbs[0].URL != "../" {
		t.Fatalf("RenderNote().Page.Breadcrumbs[0] = %#v, want Home breadcrumb to site root", got.Page.Breadcrumbs[0])
	}
	if got.Page.Breadcrumbs[1].Name != "Guide" || got.Page.Breadcrumbs[1].URL != "" {
		t.Fatalf("RenderNote().Page.Breadcrumbs[1] = %#v, want current page breadcrumb", got.Page.Breadcrumbs[1])
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
	if !bytes.Contains(got.HTML, []byte("<script type=\"application/ld+json\">")) {
		t.Fatalf("RenderNote() HTML missing JSON-LD\n%s", got.HTML)
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

func TestRenderTagPageComputesOutputPathAndDefaultsBreadcrumbs(t *testing.T) {
	t.Parallel()

	got, err := RenderTagPage(TagPageInput{
		Site: testSiteConfig(),
		Tag:  &model.Tag{Name: "systems", Slug: "tags/systems/distributed"},
		ChildTags: []model.TagLink{{
			Name: "systems/distributed/edge",
			Slug: "systems/distributed/edge",
			URL:  "edge/",
		}},
		Notes: []model.NoteSummary{{
			Title: "Guide",
			URL:   "../../../guide/",
			Date:  time.Date(2026, 4, 6, 9, 30, 0, 0, time.UTC),
		}},
		LastModified: time.Date(2026, 4, 6, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("RenderTagPage() error = %v", err)
	}

	if got.Page.RelPath != "tags/systems/distributed/index.html" {
		t.Fatalf("RenderTagPage().Page.RelPath = %q, want %q", got.Page.RelPath, "tags/systems/distributed/index.html")
	}
	if got.Page.SiteRootRel != "../../../" {
		t.Fatalf("RenderTagPage().Page.SiteRootRel = %q, want %q", got.Page.SiteRootRel, "../../../")
	}
	if got.Page.Canonical != "https://example.com/blog/tags/systems/distributed/" {
		t.Fatalf("RenderTagPage().Page.Canonical = %q, want %q", got.Page.Canonical, "https://example.com/blog/tags/systems/distributed/")
	}
	if got.Page.Description != testSiteConfig().Description {
		t.Fatalf("RenderTagPage().Page.Description = %q, want site description fallback %q", got.Page.Description, testSiteConfig().Description)
	}
	if len(got.Page.Breadcrumbs) != 2 {
		t.Fatalf("len(RenderTagPage().Page.Breadcrumbs) = %d, want %d", len(got.Page.Breadcrumbs), 2)
	}
	if got.Page.Breadcrumbs[0].URL != "../../../" {
		t.Fatalf("RenderTagPage().Page.Breadcrumbs[0] = %#v, want home breadcrumb to site root", got.Page.Breadcrumbs[0])
	}
	if got.Page.Breadcrumbs[1].Name != "systems" || got.Page.Breadcrumbs[1].URL != "" {
		t.Fatalf("RenderTagPage().Page.Breadcrumbs[1] = %#v, want current tag without dead /tags/ link", got.Page.Breadcrumbs[1])
	}
	if !bytes.Contains(got.HTML, []byte("<a class=\"tag-pill\" href=\"edge/\">#systems/distributed/edge</a>")) {
		t.Fatalf("RenderTagPage() HTML missing child tag link\n%s", got.HTML)
	}
	if !bytes.Contains(got.HTML, []byte("<a href=\"../../../guide/\">Guide</a>")) {
		t.Fatalf("RenderTagPage() HTML missing note summary link\n%s", got.HTML)
	}
	if bytes.Contains(got.HTML, []byte("<a href=\"../../\">Tags</a>")) {
		t.Fatalf("RenderTagPage() HTML unexpectedly links to an ungenerated tags landing page\n%s", got.HTML)
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
	if err := EmitStyleCSS(outputDir); err != nil {
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
