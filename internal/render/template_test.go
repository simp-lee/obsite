package render

import (
	"bytes"
	"html/template"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/simp-lee/obsite/internal/model"
)

func TestDefaultTemplatesRenderExpectedHTML(t *testing.T) {
	t.Parallel()

	tmpl := parseDefaultTemplateSet(t)

	tests := []struct {
		name       string
		data       model.PageData
		want       []string
		wantAbsent []string
	}{
		{
			name: "note page",
			data: model.PageData{
				Kind:        model.PageNote,
				SiteRootRel: "../",
				Site: model.SiteConfig{
					Title:              "Field Notes",
					Description:        "An editorial notebook.",
					Author:             "Alice Example",
					Language:           "en",
					KaTeXCSSURL:        "https://cdn.example.test/katex.css",
					KaTeXJSURL:         "https://cdn.example.test/katex.js",
					MermaidJSURL:       "https://cdn.example.test/mermaid.esm.min.mjs",
					DefaultImg:         "images/default-og.png",
					BaseURL:            "https://example.com/",
					KaTeXAutoRenderURL: "https://cdn.example.test/auto-render.js",
				},
				Title:        "Composable Systems",
				Description:  "A note about how small parts fit together.",
				Canonical:    "https://example.com/composable-systems/",
				Content:      template.HTML("<p>Rendered note body.</p><div class=\"math-display\">$$a^2+b^2=c^2$$</div>"),
				Date:         time.Date(2026, 4, 6, 9, 0, 0, 0, time.UTC),
				LastModified: time.Date(2026, 4, 6, 12, 0, 0, 0, time.UTC),
				Tags: []model.TagLink{
					{Name: "systems", URL: "../tags/systems/"},
				},
				Backlinks: []model.BacklinkEntry{
					{Title: "Related essay", URL: "../related-essay/"},
				},
				HasMath:    true,
				HasMermaid: true,
				OG: model.OpenGraph{
					Title:       "Composable Systems",
					Description: "A note about how small parts fit together.",
					URL:         "https://example.com/composable-systems/",
					Image:       "https://example.com/images/default-og.png",
					Type:        "article",
				},
				TwitterCard: "summary_large_image",
				JSONLD:      template.JS(`[{"@context":"https://schema.org","@type":"Article"}]`),
				Breadcrumbs: []model.Breadcrumb{
					{Name: "Home", URL: "../"},
					{Name: "notes", URL: "../notes/"},
					{Name: "Composable Systems"},
				},
			},
			want: []string{
				"<title>Composable Systems · Field Notes</title>",
				"<meta name=\"description\" content=\"A note about how small parts fit together.\">",
				"<meta name=\"author\" content=\"Alice Example\">",
				"<link rel=\"canonical\" href=\"https://example.com/composable-systems/\">",
				"<meta property=\"og:title\" content=\"Composable Systems\">",
				"<meta property=\"og:type\" content=\"article\">",
				"<meta property=\"og:description\" content=\"A note about how small parts fit together.\">",
				"<meta property=\"og:url\" content=\"https://example.com/composable-systems/\">",
				"<meta property=\"og:image\" content=\"https://example.com/images/default-og.png\">",
				"<meta property=\"og:site_name\" content=\"Field Notes\">",
				"<meta name=\"twitter:card\" content=\"summary_large_image\">",
				"<meta name=\"twitter:title\" content=\"Composable Systems\">",
				"<meta name=\"twitter:description\" content=\"A note about how small parts fit together.\">",
				"<meta name=\"twitter:image\" content=\"https://example.com/images/default-og.png\">",
				"<link rel=\"stylesheet\" href=\"../style.css\">",
				"<link rel=\"stylesheet\" href=\"https://cdn.example.test/katex.css\">",
				"<script defer src=\"https://cdn.example.test/katex.js\"></script>",
				"<script defer src=\"https://cdn.example.test/auto-render.js\"></script>",
				"<script type=\"module\">",
				"import mermaid from \"https:\\/\\/cdn.example.test\\/mermaid.esm.min.mjs\";",
				"mermaid.initialize({",
				"startOnLoad: true,",
				"theme: \"neutral\"",
				"securityLevel: \"loose\"",
				"<script type=\"application/ld+json\">[{\"@context\":\"https://schema.org\",\"@type\":\"Article\"}]</script>",
				"<nav class=\"breadcrumbs\" aria-label=\"Breadcrumb\">",
				"<a href=\"../notes/\">notes</a>",
				"<span aria-current=\"page\">Composable Systems</span>",
				"<a class=\"tag-pill\" href=\"../tags/systems/\">#systems</a>",
				"<h2 id=\"backlinks-heading\">Backlinks</h2>",
				"<li><a href=\"../related-essay/\">Related essay</a></li>",
			},
		},
		{
			name: "note page omits unresolved asset blocks",
			data: model.PageData{
				Kind:        model.PageNote,
				SiteRootRel: "../",
				Site: model.SiteConfig{
					Title:       "Field Notes",
					Description: "An editorial notebook.",
					Author:      "Alice Example",
					Language:    "en",
				},
				Title:       "Plain Note",
				Description: "A note without asset URLs.",
				Content:     template.HTML("<p>Rendered note body.</p>"),
				HasMath:     true,
				HasMermaid:  true,
				OG: model.OpenGraph{
					Title:       "Plain Note",
					Description: "A note without asset URLs.",
					Type:        "article",
				},
				TwitterCard: "summary",
			},
			want: []string{
				"<link rel=\"stylesheet\" href=\"../style.css\">",
				"<meta name=\"twitter:card\" content=\"summary\">",
				"<meta name=\"twitter:title\" content=\"Plain Note\">",
				"<meta name=\"twitter:description\" content=\"A note without asset URLs.\">",
				"<div class=\"entry-content\" data-page-content>",
			},
			wantAbsent: []string{
				"<meta name=\"twitter:image\"",
				"cdn.jsdelivr.net",
				"renderMathInElement",
				"window.mermaid",
				"pagefind-ui.css",
				"pagefind-ui.js",
				`data-obsite-search-ui="true"`,
				`<div id="obsite-search-root"></div>`,
			},
		},
		{
			name: "note page with search ready",
			data: model.PageData{
				Kind:        model.PageNote,
				SiteRootRel: "../../",
				Site: model.SiteConfig{
					Title:    "Field Notes",
					Language: "en",
					Search: model.SearchConfig{
						Enabled: true,
					},
				},
				Title:     "Searchable Note",
				Content:   template.HTML("<p>Rendered note body.</p>"),
				HasSearch: true,
			},
			want: []string{
				`<link rel="stylesheet" href="../../_pagefind/pagefind-ui.css" data-obsite-search-ui="true">`,
				`<div class="site-search" data-obsite-search-ui="true">`,
				`<div id="obsite-search-root"></div>`,
				`<script src="../../_pagefind/pagefind-ui.js" data-obsite-search-ui="true"></script>`,
				`<script data-obsite-search-ui="true">`,
				`new PagefindUI({ element: "#obsite-search-root" });`,
			},
			wantAbsent: []string{
				`showSubResults`,
				`DOMContentLoaded`,
			},
		},
		{
			name: "index page",
			data: model.PageData{
				Kind:        model.PageIndex,
				SiteRootRel: "./",
				Site: model.SiteConfig{
					Title:       "Field Notes",
					Description: "An editorial notebook.",
					Author:      "Alice Example",
					Language:    "en",
				},
				Title:       "Field Notes",
				Description: "An editorial notebook.",
				RecentNotes: []model.NoteSummary{
					{
						Title: "Composable Systems",
						URL:   "composable-systems/",
						Date:  time.Date(2026, 4, 6, 9, 0, 0, 0, time.UTC),
						Tags:  []model.TagLink{{Name: "systems", URL: "tags/systems/"}},
					},
				},
			},
			want: []string{
				"<link rel=\"stylesheet\" href=\"./style.css\">",
				"<h2 id=\"recent-notes-heading\">Recent notes</h2>",
				"<a href=\"composable-systems/\">Composable Systems</a>",
				"<a class=\"tag-pill\" href=\"tags/systems/\">#systems</a>",
			},
		},
		{
			name: "timeline page",
			data: model.PageData{
				Kind:        model.PageTimeline,
				SiteRootRel: "../",
				Site: model.SiteConfig{
					Title:    "Field Notes",
					Language: "en",
				},
				Title:       "Recent notes",
				Breadcrumbs: []model.Breadcrumb{{Name: "Home", URL: "../"}, {Name: "Notes"}},
				TimelineNotes: []model.NoteSummary{
					{
						Title:   "Composable Systems",
						Summary: "Freshly updated notes in reverse chronological order.",
						URL:     "../composable-systems/",
						Date:    time.Date(2026, 4, 6, 9, 0, 0, 0, time.UTC),
						Tags:    []model.TagLink{{Name: "systems", URL: "../tags/systems/"}},
					},
				},
			},
			want: []string{
				"<link rel=\"stylesheet\" href=\"../style.css\">",
				"<nav class=\"breadcrumbs\" aria-label=\"Breadcrumb\">",
				"<a href=\"../\">Home</a>",
				"<span aria-current=\"page\">Notes</span>",
				"<h1 class=\"page-title\">Recent notes</h1>",
				"<h2 id=\"timeline-notes-heading\">Recent notes</h2>",
				"<a href=\"../composable-systems/\">Composable Systems</a>",
				"Freshly updated notes in reverse chronological order.",
				"<a class=\"tag-pill\" href=\"../tags/systems/\">#systems</a>",
			},
		},
		{
			name: "tag page",
			data: model.PageData{
				Kind:        model.PageTag,
				SiteRootRel: "../../",
				Site: model.SiteConfig{
					Title:    "Field Notes",
					Language: "en",
				},
				Title:       "systems",
				TagName:     "systems",
				Canonical:   "https://example.com/tags/systems/",
				ChildTags:   []model.TagLink{{Name: "systems/distributed", URL: "distributed/"}},
				TagNotes:    []model.NoteSummary{{Title: "Composable Systems", URL: "../../composable-systems/", Date: time.Date(2026, 4, 6, 9, 0, 0, 0, time.UTC)}},
				Breadcrumbs: []model.Breadcrumb{{Name: "Home", URL: "../../../"}, {Name: "systems", URL: "../"}, {Name: "systems/distributed"}},
			},
			want: []string{
				"<link rel=\"stylesheet\" href=\"../../style.css\">",
				"<nav class=\"breadcrumbs\" aria-label=\"Breadcrumb\">",
				"<a href=\"../\">systems</a>",
				"<span aria-current=\"page\">systems/distributed</span>",
				"<h2 id=\"child-tags-heading\">Child tags</h2>",
				"<a class=\"tag-pill\" href=\"distributed/\">#systems/distributed</a>",
				"<h2 id=\"tag-notes-heading\">Notes</h2>",
				"<a href=\"../../composable-systems/\">Composable Systems</a>",
			},
			wantAbsent: []string{
				">Tags</a>",
			},
		},
		{
			name: "404 page",
			data: model.PageData{
				Kind:        model.Page404,
				SiteRootRel: "./",
				Site: model.SiteConfig{
					BaseURL:     "https://example.com/blog/",
					Title:       "Field Notes",
					Description: "An editorial notebook.",
					Language:    "en",
				},
				Title:       "Not found",
				Description: "The requested page could not be found.",
				RecentNotes: []model.NoteSummary{{Title: "Composable Systems", URL: "composable-systems/"}},
			},
			want: []string{
				`<base href="/blog/">`,
				"<link rel=\"stylesheet\" href=\"./style.css\">",
				"<a class=\"action-link\" href=\"./\">Return to the homepage</a>",
				"<li><a href=\"composable-systems/\">Composable Systems</a></li>",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := renderTemplate(t, tmpl, tt.data)
			for _, want := range tt.want {
				assertContains(t, got, want)
			}
			for _, wantAbsent := range tt.wantAbsent {
				assertNotContains(t, got, wantAbsent)
			}
		})
	}
}

func TestRenderIndexKeepsSearchDisabledUntilReady(t *testing.T) {
	t.Parallel()

	got, err := RenderIndex(IndexPageInput{
		Site: model.SiteConfig{
			Title: "Field Notes",
			Search: model.SearchConfig{
				Enabled: true,
			},
		},
		RecentNotes: []model.NoteSummary{{
			Title: "Guide",
			URL:   "guide/",
		}},
	})
	if err != nil {
		t.Fatalf("RenderIndex() error = %v", err)
	}
	if got.Page.HasSearch {
		t.Fatalf("RenderIndex().Page.HasSearch = %t, want false before Pagefind succeeds", got.Page.HasSearch)
	}
	for _, forbidden := range [][]byte{
		[]byte(`pagefind-ui.css`),
		[]byte(`pagefind-ui.js`),
		[]byte(`data-obsite-search-ui`),
		[]byte(`id="obsite-search-root"`),
		[]byte(`PagefindUI`),
	} {
		if bytes.Contains(got.HTML, forbidden) {
			t.Fatalf("RenderIndex() HTML unexpectedly exposes search before readiness\n%s", got.HTML)
		}
	}
}

func TestDefaultTemplatesUseModuleSafeMermaidLoader(t *testing.T) {
	t.Parallel()

	tmpl := parseDefaultTemplateSet(t)
	site := model.SiteConfig{
		Title:              "Field Notes",
		BaseURL:            "https://example.com/",
		Language:           "en",
		KaTeXCSSURL:        "assets/obsite-runtime/katex.min.css",
		KaTeXJSURL:         "assets/obsite-runtime/katex.min.js",
		KaTeXAutoRenderURL: "assets/obsite-runtime/auto-render.min.js",
		MermaidJSURL:       "assets/obsite-runtime/mermaid.esm.min.mjs",
	}
	if !strings.HasSuffix(site.MermaidJSURL, "mermaid.esm.min.mjs") {
		t.Fatalf("default MermaidJSURL = %q, want ESM .mjs asset contract", site.MermaidJSURL)
	}
	escapedMermaidURL := strings.ReplaceAll("../"+site.MermaidJSURL, "/", `\/`)

	got := renderTemplate(t, tmpl, model.PageData{
		Kind:        model.PageNote,
		SiteRootRel: "../",
		Site:        site,
		Title:       "Mermaid Note",
		Content:     template.HTML("<div class=\"math math-display\">$$E = mc^2$$</div><pre class=\"mermaid\">graph TD;A-->B</pre>"),
		HasMath:     true,
		HasMermaid:  true,
	})

	assertContains(t, got, "<script type=\"module\">")
	assertContains(t, got, "<link rel=\"stylesheet\" href=\"../"+site.KaTeXCSSURL+"\">")
	assertContains(t, got, "<script defer src=\"../"+site.KaTeXJSURL+"\"></script>")
	assertContains(t, got, "<script defer src=\"../"+site.KaTeXAutoRenderURL+"\"></script>")
	assertContains(t, got, "import mermaid from \""+escapedMermaidURL+"\";")
	assertContains(t, got, "mermaid.initialize({")
	assertContains(t, got, "startOnLoad: true,")
	assertContains(t, got, "theme: \"neutral\"")
	assertContains(t, got, "securityLevel: \"loose\"")
	assertNotContains(t, got, "cdn.jsdelivr.net")
	assertNotContains(t, got, "window.mermaid")
	assertNotContains(t, got, "<script defer src=\""+site.MermaidJSURL+"\"></script>")
}

func TestThemeRootLoadsCompleteTemplateSet(t *testing.T) {
	t.Parallel()

	themeRoot := t.TempDir()
	writeCompleteThemeRoot(t, themeRoot, map[string]string{
		"note.html": `{{define "content-note"}}<section data-custom-note>{{.Title}}</section>{{end}}`,
	})

	site := testSiteConfig()
	site.ActiveThemeName = "feature"
	site.ThemeRoot = themeRoot

	note, err := RenderNote(NotePageInput{
		Site: site,
		Note: &model.Note{
			RelPath:     "notes/guide.md",
			Slug:        "guide",
			HTMLContent: "<p>Rendered note body.</p>",
			Frontmatter: model.Frontmatter{Title: "Guide"},
		},
	})
	if err != nil {
		t.Fatalf("RenderNote() error = %v", err)
	}
	assertContains(t, string(note.HTML), "<section data-custom-note>Guide</section>")

	index, err := RenderIndex(IndexPageInput{
		Site: site,
		RecentNotes: []model.NoteSummary{{
			Title: "Guide",
			URL:   "guide/",
		}},
	})
	if err != nil {
		t.Fatalf("RenderIndex() error = %v", err)
	}
	assertContains(t, string(index.HTML), "<h2 id=\"recent-notes-heading\">Recent notes</h2>")
	assertContains(t, string(index.HTML), "<a href=\"guide/\">Guide</a>")
}

func TestThemeRootLoadsNestedPartialTemplatesAndReloadsThemWithinSameProcess(t *testing.T) {
	t.Parallel()

	themeRoot := t.TempDir()
	t.Cleanup(func() {
		clearTemplateSetCacheEntriesForThemeRoot(t, themeRoot)
	})

	partialPath := filepath.Join(themeRoot, "partials", "badge.html")
	writeCompleteThemeRoot(t, themeRoot, map[string]string{
		"note.html":           `{{define "content-note"}}<article>{{template "theme-badge" .}}</article>{{end}}`,
		"partials/badge.html": `{{define "theme-badge"}}<span data-theme-badge="v1">{{.Title}}</span>{{end}}`,
	})

	state, err := scanThemeTemplateState(themeIdentity{activeThemeName: "feature", themeRoot: themeRoot})
	if err != nil {
		t.Fatalf("scanThemeTemplateState() error = %v", err)
	}
	if len(state.files) != len(RequiredHTMLTemplateNames)+1 {
		t.Fatalf("len(state.files) = %d, want %d", len(state.files), len(RequiredHTMLTemplateNames)+1)
	}

	snapshot, err := scanThemeTemplateSnapshotFromState(state)
	if err != nil {
		t.Fatalf("scanThemeTemplateSnapshotFromState() error = %v", err)
	}
	if len(snapshot.files) != len(RequiredHTMLTemplateNames)+1 {
		t.Fatalf("len(snapshot.files) = %d, want %d", len(snapshot.files), len(RequiredHTMLTemplateNames)+1)
	}
	partialFile := findThemeTemplateFile(t, snapshot.files, "badge.html")
	if !strings.Contains(partialFile.contents, `data-theme-badge="v1"`) {
		t.Fatalf("partial snapshot contents = %q, want v1 partial contents", partialFile.contents)
	}

	site := testSiteConfig()
	site.ActiveThemeName = "feature"
	site.ThemeRoot = filepath.Join(themeRoot, ".")

	noteHTML := renderThemeRootNoteHTML(t, site)
	assertContains(t, noteHTML, `<span data-theme-badge="v1">Guide</span>`)

	if err := os.WriteFile(partialPath, []byte(`{{define "theme-badge"}}<span data-theme-badge="v2-updated">{{.Title}}</span>{{end}}`), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", partialPath, err)
	}

	noteHTML = renderThemeRootNoteHTML(t, site)
	assertContains(t, noteHTML, `<span data-theme-badge="v2-updated">Guide</span>`)
	assertNotContains(t, noteHTML, `data-theme-badge="v1"`)
}

func TestRequiredHTMLTemplateNamesExcludeEmbeddedOnlyHTMLPartials(t *testing.T) {
	want := []string{"base.html", "note.html", "index.html", "tag.html", "folder.html", "timeline.html", "404.html"}
	if !equalTemplateNameSlices(RequiredHTMLTemplateNames, want) {
		t.Fatalf("RequiredHTMLTemplateNames = %#v, want %#v", RequiredHTMLTemplateNames, want)
	}

	if containsTemplateName(RequiredHTMLTemplateNames, "partials/helper.html") {
		t.Fatalf("RequiredHTMLTemplateNames unexpectedly contains embedded-only helper: %#v", RequiredHTMLTemplateNames)
	}
	if !containsTemplateName(embeddedHTMLTemplateNames, "base.html") {
		t.Fatalf("embeddedHTMLTemplateNames = %#v, want base.html in the embedded HTML inventory", embeddedHTMLTemplateNames)
	}
}

func TestThemeRootRejectsSymlinkedOptionalHTMLFiles(t *testing.T) {
	t.Parallel()

	themeRoot := t.TempDir()
	t.Cleanup(func() {
		clearTemplateSetCacheEntriesForThemeRoot(t, themeRoot)
	})

	writeCompleteThemeRoot(t, themeRoot, map[string]string{
		"note.html":           `{{define "content-note"}}<article>{{template "theme-badge" .}}</article>{{end}}`,
		"partials/badge.html": `{{define "theme-badge"}}<span data-theme-badge="regular">{{.Title}}</span>{{end}}`,
	})

	overridePath := filepath.Join(t.TempDir(), "badge-override.html")
	if err := os.WriteFile(overridePath, []byte(`{{define "theme-badge"}}<span data-theme-badge="symlink">{{.Title}}</span>{{end}}`), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", overridePath, err)
	}

	linkPath := filepath.Join(themeRoot, "partials", "zz-badge.html")
	if err := os.MkdirAll(filepath.Dir(linkPath), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v", filepath.Dir(linkPath), err)
	}
	if err := os.Symlink(overridePath, linkPath); err != nil {
		t.Skipf("os.Symlink(%q, %q) unsupported: %v", overridePath, linkPath, err)
	}

	_, err := scanThemeTemplateState(themeIdentity{activeThemeName: "feature", themeRoot: themeRoot})
	if err == nil {
		t.Fatal("scanThemeTemplateState() error = nil, want symlinked optional HTML template failure")
	}
	for _, want := range []string{"zz-badge.html", "regular non-symlink file"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("scanThemeTemplateState() error = %q, want substring %q", err.Error(), want)
		}
	}

	site := testSiteConfig()
	site.ActiveThemeName = "feature"
	site.ThemeRoot = themeRoot

	_, err = RenderNote(NotePageInput{
		Site: site,
		Note: &model.Note{
			RelPath:     "notes/guide.md",
			Slug:        "guide",
			HTMLContent: "<p>Rendered note body.</p>",
			Frontmatter: model.Frontmatter{Title: "Guide"},
		},
	})
	if err == nil {
		t.Fatal("RenderNote() error = nil, want symlinked optional HTML template failure")
	}
	for _, want := range []string{"load templates", "zz-badge.html", "regular non-symlink file"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("RenderNote() error = %q, want substring %q", err.Error(), want)
		}
	}
}

func TestThemeRootRejectsSymlinkedRequiredTemplate(t *testing.T) {
	t.Parallel()

	themeRoot := t.TempDir()
	t.Cleanup(func() {
		clearTemplateSetCacheEntriesForThemeRoot(t, themeRoot)
	})

	writeCompleteThemeRoot(t, themeRoot, nil)

	targetPath := filepath.Join(t.TempDir(), "tag.html")
	if err := os.WriteFile(targetPath, []byte(`{{define "content-tag"}}tag{{end}}`), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", targetPath, err)
	}
	linkPath := filepath.Join(themeRoot, "tag.html")
	if err := os.Remove(linkPath); err != nil {
		t.Fatalf("os.Remove(%q) error = %v", linkPath, err)
	}
	if err := os.Symlink(targetPath, linkPath); err != nil {
		t.Skipf("os.Symlink(%q, %q) unsupported: %v", targetPath, linkPath, err)
	}

	site := testSiteConfig()
	site.ActiveThemeName = "feature"
	site.ThemeRoot = themeRoot

	_, err := RenderNote(NotePageInput{
		Site: site,
		Note: &model.Note{
			RelPath:     "notes/guide.md",
			Slug:        "guide",
			HTMLContent: "<p>Rendered note body.</p>",
			Frontmatter: model.Frontmatter{Title: "Guide"},
		},
	})
	if err == nil {
		t.Fatal("RenderNote() error = nil, want symlinked required template failure")
	}
	for _, want := range []string{"load templates", "tag.html", "regular non-symlink file"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("RenderNote() error = %q, want substring %q", err.Error(), want)
		}
	}
}

func TestEmitStyleCSSRejectsSymlinkedThemeStyle(t *testing.T) {
	t.Parallel()

	themeRoot := t.TempDir()
	writeCompleteThemeRoot(t, themeRoot, nil)

	targetPath := filepath.Join(t.TempDir(), "style.css")
	if err := os.WriteFile(targetPath, []byte("body { color: tomato; }\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", targetPath, err)
	}
	linkPath := filepath.Join(themeRoot, "style.css")
	if err := os.Symlink(targetPath, linkPath); err != nil {
		t.Skipf("os.Symlink(%q, %q) unsupported: %v", targetPath, linkPath, err)
	}

	wrote, err := EmitStyleCSS(t.TempDir(), model.SiteConfig{ActiveThemeName: "feature", ThemeRoot: themeRoot})
	if err == nil {
		t.Fatal("EmitStyleCSS() error = nil, want symlinked theme style failure")
	}
	if wrote {
		t.Fatal("EmitStyleCSS() wrote = true, want false when theme style.css is a symlink")
	}
	for _, want := range []string{"emit style.css", "style.css", "regular non-symlink file"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("EmitStyleCSS() error = %q, want substring %q", err.Error(), want)
		}
	}
}

func TestThemeRootRequiresCompleteTemplateSet(t *testing.T) {
	t.Parallel()

	themeRoot := t.TempDir()
	override := `{{define "content-note"}}<section data-custom-note>{{.Title}}</section>{{end}}`
	if err := os.WriteFile(filepath.Join(themeRoot, "note.html"), []byte(override), 0o644); err != nil {
		t.Fatalf("os.WriteFile(note.html) error = %v", err)
	}

	site := testSiteConfig()
	site.ActiveThemeName = "feature"
	site.ThemeRoot = themeRoot

	_, err := RenderNote(NotePageInput{
		Site: site,
		Note: &model.Note{
			RelPath:     "notes/guide.md",
			Slug:        "guide",
			HTMLContent: "<p>Rendered note body.</p>",
			Frontmatter: model.Frontmatter{Title: "Guide"},
		},
	})
	if err == nil {
		t.Fatal("RenderNote() error = nil, want missing required theme templates")
	}
	if !strings.Contains(err.Error(), "missing required theme templates") {
		t.Fatalf("RenderNote() error = %v, want missing required theme templates", err)
	}
}

func TestThemeRootCachesParsedTemplateSetByNormalizedIdentity(t *testing.T) {
	t.Parallel()

	themeRoot := t.TempDir()
	t.Cleanup(func() {
		clearTemplateSetCacheEntriesForThemeRoot(t, themeRoot)
	})

	writeCompleteThemeRoot(t, themeRoot, map[string]string{
		"base.html": themeBaseWithBodyAttribute(t, `data-cache-base="true"`),
	})

	site := testSiteConfig()
	site.ActiveThemeName = "feature"
	site.ThemeRoot = filepath.Join(themeRoot, ".")

	first, err := loadTemplateSet(site)
	if err != nil {
		t.Fatalf("loadTemplateSet() error = %v", err)
	}

	site.ThemeRoot = themeRoot
	second, err := loadTemplateSet(site)
	if err != nil {
		t.Fatalf("loadTemplateSet() error = %v", err)
	}
	if first != second {
		t.Fatal("loadTemplateSet() returned different template set instances for the same theme identity and unchanged files")
	}

	if got := countTemplateSetCacheEntriesForThemeIdentity(t, "feature", themeRoot); got != 1 {
		t.Fatalf("countTemplateSetCacheEntriesForThemeIdentity(%q, %q) = %d, want %d", "feature", themeRoot, got, 1)
	}
}

func TestThemeRootDoesNotReuseTemplateSetAcrossThemeNames(t *testing.T) {
	t.Parallel()

	themeRoot := t.TempDir()
	t.Cleanup(func() {
		clearTemplateSetCacheEntriesForThemeRoot(t, themeRoot)
	})

	writeCompleteThemeRoot(t, themeRoot, map[string]string{
		"base.html": `{{define "base"}}<!doctype html><html><body data-theme-name-cache="true">{{template "content" .}}</body></html>{{end}}`,
	})

	alpha := testSiteConfig()
	alpha.ActiveThemeName = "alpha"
	alpha.ThemeRoot = themeRoot
	beta := alpha
	beta.ActiveThemeName = "beta"

	first, err := loadTemplateSet(alpha)
	if err != nil {
		t.Fatalf("loadTemplateSet(alpha) error = %v", err)
	}
	second, err := loadTemplateSet(beta)
	if err != nil {
		t.Fatalf("loadTemplateSet(beta) error = %v", err)
	}
	if first == second {
		t.Fatal("loadTemplateSet() reused a cached template set across distinct theme names")
	}
	if got := countTemplateSetCacheEntriesForThemeRoot(t, themeRoot); got != 2 {
		t.Fatalf("countTemplateSetCacheEntriesForThemeRoot(%q) = %d, want %d", themeRoot, got, 2)
	}
}

func TestThemeRootReusesCachedSnapshotUntilFilesChange(t *testing.T) {
	templateDir := t.TempDir()
	t.Cleanup(func() {
		clearTemplateSetCacheEntriesForThemeRoot(t, templateDir)
	})

	overridePath := filepath.Join(templateDir, "base.html")
	baseV1 := themeBaseWithBodyAttribute(t, `data-snapshot-cache="v1"`)
	writeCompleteThemeRoot(t, templateDir, map[string]string{"base.html": baseV1})

	wantReadsPerSnapshot := len(RequiredHTMLTemplateNames)
	readCount := trackThemeTemplateFileReadsForRoot(t, templateDir)
	site := testSiteConfig()
	site.ActiveThemeName = "feature"
	site.ThemeRoot = filepath.Join(templateDir, ".")

	first, err := loadTemplateSet(site)
	if err != nil {
		t.Fatalf("loadTemplateSet() error = %v", err)
	}
	if got := readCount(); got != wantReadsPerSnapshot {
		t.Fatalf("theme template reads after first load = %d, want %d", got, wantReadsPerSnapshot)
	}

	second, err := loadTemplateSet(site)
	if err != nil {
		t.Fatalf("loadTemplateSet() second error = %v", err)
	}
	if first != second {
		t.Fatal("loadTemplateSet() returned different template set instances for unchanged theme snapshot")
	}
	if got := readCount(); got != wantReadsPerSnapshot {
		t.Fatalf("theme template reads after unchanged reload = %d, want %d", got, wantReadsPerSnapshot)
	}

	baseV2 := baseV1 + "\n"
	if err := os.WriteFile(overridePath, []byte(baseV2), 0o644); err != nil {
		t.Fatalf("os.WriteFile(base.html v2) error = %v", err)
	}

	third, err := loadTemplateSet(site)
	if err != nil {
		t.Fatalf("loadTemplateSet() third error = %v", err)
	}
	if third == second {
		t.Fatal("loadTemplateSet() returned cached template set after a theme template changed")
	}
	if got := readCount(); got != wantReadsPerSnapshot*2 {
		t.Fatalf("theme template reads after changed reload = %d, want %d", got, wantReadsPerSnapshot*2)
	}
}

func TestCachedThemeSnapshotReusesCachedSnapshotWhenStateMatches(t *testing.T) {
	templateDir := t.TempDir()
	baseV1 := themeBaseWithBodyAttribute(t, `data-same-state="v1"`)
	writeCompleteThemeRoot(t, templateDir, map[string]string{"base.html": baseV1})

	state, err := scanThemeTemplateState(themeIdentity{activeThemeName: "feature", themeRoot: templateDir})
	if err != nil {
		t.Fatalf("scanThemeTemplateState(%q) error = %v", templateDir, err)
	}

	var cached cachedThemeTemplateSnapshot
	first, err := cached.load(state)
	if err != nil {
		t.Fatalf("cached.load(state) first error = %v", err)
	}
	if len(first.files) != len(RequiredHTMLTemplateNames) {
		t.Fatalf("len(first.files) = %d, want %d", len(first.files), len(RequiredHTMLTemplateNames))
	}
	baseFile := findThemeTemplateFile(t, first.files, "base.html")
	if !strings.Contains(baseFile.contents, `data-same-state="v1"`) {
		t.Fatalf("base snapshot contents = %q, want v1 template contents", baseFile.contents)
	}

	second, err := cached.load(state)
	if err != nil {
		t.Fatalf("cached.load(state) second error = %v", err)
	}
	if second.signature != first.signature {
		t.Fatalf("second.signature = %q, want cached signature %q when file state is unchanged", second.signature, first.signature)
	}
	if len(second.files) != len(RequiredHTMLTemplateNames) {
		t.Fatalf("len(second.files) = %d, want %d", len(second.files), len(RequiredHTMLTemplateNames))
	}
	baseFile = findThemeTemplateFile(t, second.files, "base.html")
	if !strings.Contains(baseFile.contents, `data-same-state="v1"`) {
		t.Fatalf("base snapshot contents = %q, want cached v1 template contents", baseFile.contents)
	}
}

func TestThemeRootReloadsTemplatesWhenThemeFilesChangeWithinSameProcess(t *testing.T) {
	t.Parallel()

	templateDir := t.TempDir()
	t.Cleanup(func() {
		clearTemplateSetCacheEntriesForThemeRoot(t, templateDir)
	})

	basePath := filepath.Join(templateDir, "base.html")
	baseV1 := themeBaseWithBodyAttribute(t, `data-live-base="v1"`)
	writeCompleteThemeRoot(t, templateDir, map[string]string{"base.html": baseV1})

	site := testSiteConfig()
	site.ActiveThemeName = "feature"
	site.ThemeRoot = filepath.Join(templateDir, ".")

	noteHTML := renderThemeRootNoteHTML(t, site)
	assertContains(t, noteHTML, `data-live-base="v1"`)

	baseV2 := themeBaseWithBodyAttribute(t, `data-live-base="v2"`)
	if err := os.WriteFile(basePath, []byte(baseV2), 0o644); err != nil {
		t.Fatalf("os.WriteFile(base.html v2) error = %v", err)
	}

	notFoundHTML := renderThemeRoot404HTML(t, site)
	assertContains(t, notFoundHTML, `data-live-base="v2"`)
	assertNotContains(t, notFoundHTML, `data-live-base="v1"`)

	notFoundOverride := `{{define "content-404"}}<section data-live-404>{{.Title}}</section>{{end}}`
	if err := os.WriteFile(filepath.Join(templateDir, "404.html"), []byte(notFoundOverride), 0o644); err != nil {
		t.Fatalf("os.WriteFile(404.html) error = %v", err)
	}

	notFoundHTML = renderThemeRoot404HTML(t, site)
	assertContains(t, notFoundHTML, `<section data-live-404>Not found</section>`)

	timelineHTML := renderThemeRootTimelineHTML(t, site)
	assertContains(t, timelineHTML, `<h2 id="timeline-notes-heading">Recent notes</h2>`)
	assertNotContains(t, timelineHTML, `data-live-404`)
}

func TestThemeRootRepeatedEditsDoNotGrowCacheCardinality(t *testing.T) {
	t.Parallel()

	templateDir := t.TempDir()
	t.Cleanup(func() {
		clearTemplateSetCacheEntriesForThemeRoot(t, templateDir)
	})

	site := testSiteConfig()
	site.ActiveThemeName = "feature"
	site.ThemeRoot = filepath.Join(templateDir, ".")

	basePath := filepath.Join(templateDir, "base.html")
	baseV1 := themeBaseWithBodyAttribute(t, `data-cache-cardinality="v1"`)
	writeCompleteThemeRoot(t, templateDir, map[string]string{"base.html": baseV1})

	noteHTML := renderThemeRootNoteHTML(t, site)
	assertContains(t, noteHTML, `data-cache-cardinality="v1"`)
	if got := countTemplateSetCacheEntriesForThemeIdentity(t, "feature", templateDir); got != 1 {
		t.Fatalf("countTemplateSetCacheEntriesForThemeIdentity(%q, %q) after initial load = %d, want %d", "feature", templateDir, got, 1)
	}

	baseV2 := themeBaseWithBodyAttribute(t, `data-cache-cardinality="v2"`)
	if err := os.WriteFile(basePath, []byte(baseV2), 0o644); err != nil {
		t.Fatalf("os.WriteFile(base.html v2) error = %v", err)
	}

	notFoundHTML := renderThemeRoot404HTML(t, site)
	assertContains(t, notFoundHTML, `data-cache-cardinality="v2"`)
	assertNotContains(t, notFoundHTML, `data-cache-cardinality="v1"`)
	if got := countTemplateSetCacheEntriesForThemeIdentity(t, "feature", templateDir); got != 1 {
		t.Fatalf("countTemplateSetCacheEntriesForThemeIdentity(%q, %q) after base update = %d, want %d", "feature", templateDir, got, 1)
	}

	notFoundOverride := `{{define "content-404"}}<section data-cache-cardinality-404>{{.Title}}</section>{{end}}`
	if err := os.WriteFile(filepath.Join(templateDir, "404.html"), []byte(notFoundOverride), 0o644); err != nil {
		t.Fatalf("os.WriteFile(404.html) error = %v", err)
	}

	notFoundHTML = renderThemeRoot404HTML(t, site)
	assertContains(t, notFoundHTML, `<section data-cache-cardinality-404>Not found</section>`)
	if got := countTemplateSetCacheEntriesForThemeIdentity(t, "feature", templateDir); got != 1 {
		t.Fatalf("countTemplateSetCacheEntriesForThemeIdentity(%q, %q) after 404 addition = %d, want %d", "feature", templateDir, got, 1)
	}
}

func TestThemeRootLoadsOverriddenBaseAndTagTemplatesFromCompleteTheme(t *testing.T) {
	t.Parallel()

	templateDir := t.TempDir()
	baseOverride := themeBaseWithBodyAttribute(t, `data-custom-base="true"`)
	tagOverride := `{{define "content-tag"}}<section data-custom-tag>#{{.TagName}}</section>{{end}}`
	writeCompleteThemeRoot(t, templateDir, map[string]string{
		"base.html": baseOverride,
		"tag.html":  tagOverride,
	})

	site := testSiteConfig()
	site.ActiveThemeName = "feature"
	site.ThemeRoot = templateDir

	tagPage, err := RenderTagPage(TagPageInput{
		Site: site,
		Tag: &model.Tag{
			Name: "systems",
			Slug: "systems",
		},
		Notes: []model.NoteSummary{{
			Title: "Guide",
			URL:   "../../guide/",
		}},
		RelPath: "tags/systems/index.html",
	})
	if err != nil {
		t.Fatalf("RenderTagPage() error = %v", err)
	}
	assertContains(t, string(tagPage.HTML), `data-custom-base="true"`)
	assertContains(t, string(tagPage.HTML), `<section data-custom-tag>#systems</section>`)

	note, err := RenderNote(NotePageInput{
		Site: site,
		Note: &model.Note{
			RelPath:     "notes/guide.md",
			Slug:        "guide",
			HTMLContent: "<p>Rendered note body.</p>",
			Frontmatter: model.Frontmatter{Title: "Guide"},
		},
	})
	if err != nil {
		t.Fatalf("RenderNote() error = %v", err)
	}
	assertContains(t, string(note.HTML), `data-custom-base="true"`)
	assertContains(t, string(note.HTML), `<article class="page-shell article-page">`)

	index, err := RenderIndex(IndexPageInput{
		Site: site,
		RecentNotes: []model.NoteSummary{{
			Title: "Guide",
			URL:   "guide/",
		}},
	})
	if err != nil {
		t.Fatalf("RenderIndex() error = %v", err)
	}
	assertContains(t, string(index.HTML), `data-custom-base="true"`)
	assertContains(t, string(index.HTML), `<h2 id="recent-notes-heading">Recent notes</h2>`)
}

func TestThemeRootLoadsAllowedPageTemplatesIndividuallyFromCompleteTheme(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		overrideFile       string
		overrideContents   string
		renderTarget       func(t *testing.T, site model.SiteConfig) string
		wantTarget         string
		renderFallback     func(t *testing.T, site model.SiteConfig) string
		wantFallback       string
		wantFallbackAbsent string
	}{
		{
			name:               "index template override",
			overrideFile:       "index.html",
			overrideContents:   `{{define "content-index"}}<section data-custom-index>{{.Title}}</section>{{end}}`,
			renderTarget:       renderThemeRootIndexHTML,
			wantTarget:         `<section data-custom-index>Field Notes</section>`,
			renderFallback:     renderThemeRoot404HTML,
			wantFallback:       `Return to the homepage`,
			wantFallbackAbsent: `data-custom-index`,
		},
		{
			name:               "404 template override",
			overrideFile:       "404.html",
			overrideContents:   `{{define "content-404"}}<section data-custom-404>{{.Title}}</section>{{end}}`,
			renderTarget:       renderThemeRoot404HTML,
			wantTarget:         `<section data-custom-404>Not found</section>`,
			renderFallback:     renderThemeRootIndexHTML,
			wantFallback:       `<h2 id="recent-notes-heading">Recent notes</h2>`,
			wantFallbackAbsent: `data-custom-404`,
		},
		{
			name:               "folder template override",
			overrideFile:       "folder.html",
			overrideContents:   `{{define "content-folder"}}<section data-custom-folder>{{.FolderPath}}</section>{{end}}`,
			renderTarget:       renderThemeRootFolderHTML,
			wantTarget:         `<section data-custom-folder>journal</section>`,
			renderFallback:     renderThemeRootTimelineHTML,
			wantFallback:       `<h2 id="timeline-notes-heading">Recent notes</h2>`,
			wantFallbackAbsent: `data-custom-folder`,
		},
		{
			name:               "timeline template override",
			overrideFile:       "timeline.html",
			overrideContents:   `{{define "content-timeline"}}<section data-custom-timeline>{{.Title}}</section>{{end}}`,
			renderTarget:       renderThemeRootTimelineHTML,
			wantTarget:         `<section data-custom-timeline>Recent notes</section>`,
			renderFallback:     renderThemeRootFolderHTML,
			wantFallback:       `<h2 id="folder-notes-heading">Notes</h2>`,
			wantFallbackAbsent: `data-custom-timeline`,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			templateDir := t.TempDir()
			t.Cleanup(func() {
				clearTemplateSetCacheEntriesForThemeRoot(t, templateDir)
			})

			writeCompleteThemeRoot(t, templateDir, map[string]string{tt.overrideFile: tt.overrideContents})

			site := testSiteConfig()
			site.ActiveThemeName = "feature"
			site.ThemeRoot = filepath.Join(templateDir, ".")

			targetHTML := tt.renderTarget(t, site)
			assertContains(t, targetHTML, tt.wantTarget)

			fallbackHTML := tt.renderFallback(t, site)
			assertContains(t, fallbackHTML, tt.wantFallback)
			assertNotContains(t, fallbackHTML, tt.wantFallbackAbsent)
		})
	}
}

func TestDefaultTemplatesIncludeThemeToggleAndThemeScript(t *testing.T) {
	t.Parallel()

	tmpl := parseDefaultTemplateSet(t)
	got := renderTemplate(t, tmpl, model.PageData{
		Kind:        model.PageIndex,
		SiteRootRel: "./",
		Site: model.SiteConfig{
			Title:       "Field Notes",
			BaseURL:     "https://example.com/blog/",
			Description: "An editorial notebook.",
			Language:    "en",
		},
		Title: "Field Notes",
	})

	assertContains(t, got, "<meta name=\"color-scheme\" content=\"light dark\">")
	assertContains(t, got, "data-theme-toggle")
	assertContains(t, got, "aria-labelledby=\"theme-toggle-name\"")
	assertContains(t, got, "aria-describedby=\"theme-toggle-state theme-toggle-source\"")
	assertContains(t, got, "aria-pressed=\"false\"")
	assertContains(t, got, "hidden>")
	assertContains(t, got, "data-theme-toggle-value")
	assertContains(t, got, "<span class=\"theme-toggle-value\" aria-hidden=\"true\" data-theme-toggle-value>Mode</span>")
	assertContains(t, got, "data-theme-toggle-state")
	assertContains(t, got, "data-theme-toggle-source")
	assertContains(t, got, `var storageKey = "obsite.theme.v1:\/blog\/"`)
	assertContains(t, got, `var legacyStorageKey = "theme"`)
	assertContains(t, got, "function migrateStoredTheme(value)")
	assertContains(t, got, "localStorage.removeItem(legacyStorageKey)")
	assertContains(t, got, "localStorage.getItem(storageKey)")
	assertContains(t, got, "localStorage.setItem(storageKey, nextTheme)")
	assertContains(t, got, "prefers-color-scheme: dark")
	assertContains(t, got, "root.setAttribute(\"data-theme\", preference)")
	assertContains(t, got, "Current mode ")
	assertContains(t, got, "Following system preference.")
	assertContains(t, got, "Theme locked to ")
	assertContains(t, got, "toggle.hidden = false")
	assertNotContains(t, got, "toggle.setAttribute(\"aria-label\"")
	assertNotContains(t, got, "toggle.setAttribute(\"title\"")
	assertNotContains(t, got, "Switch to dark theme")
	assertNotContains(t, got, "Switch to light theme")
}

func TestDefaultTemplatesInitializeThemeToggleWithoutDOMContentLoaded(t *testing.T) {
	t.Parallel()

	tmpl := parseDefaultTemplateSet(t)
	got := renderTemplate(t, tmpl, model.PageData{
		Kind:        model.PageNote,
		SiteRootRel: "../",
		Site: model.SiteConfig{
			Title:              "Field Notes",
			Language:           "en",
			KaTeXCSSURL:        "https://cdn.example.test/katex.css",
			KaTeXJSURL:         "https://cdn.example.test/katex.js",
			KaTeXAutoRenderURL: "https://cdn.example.test/auto-render.js",
			MermaidJSURL:       "https://cdn.example.test/mermaid.esm.min.mjs",
		},
		Title:      "Sequenced Theme Toggle",
		Content:    template.HTML("<p>Rendered note body.</p>"),
		HasMath:    true,
		HasMermaid: true,
	})

	assertContains(t, got, "window.__obsiteInitThemeToggle = initThemeToggle")

	readyPattern := regexp.MustCompile(`(?s)<button class="theme-toggle".*?data-theme-toggle.*?</button>\s*<script>\s*if \(typeof window\.__obsiteInitThemeToggle === "function"\) \{\s*window\.__obsiteInitThemeToggle\(\);\s*\}\s*</script>`)
	if !readyPattern.MatchString(got) {
		t.Fatalf("theme toggle initializer script should run immediately after the toggle markup\noutput:\n%s", got)
	}

	blockedPattern := regexp.MustCompile(`(?s)document\.addEventListener\("DOMContentLoaded", function \(\) \{\s*var toggle = document\.querySelector\("\[data-theme-toggle\]"\)`)
	if blockedPattern.MatchString(got) {
		t.Fatalf("theme toggle initialization still depends on DOMContentLoaded\noutput:\n%s", got)
	}
}

func TestDefaultTemplatesConditionallyRenderRSSAutoDiscoveryLink(t *testing.T) {
	t.Parallel()

	tmpl := parseDefaultTemplateSet(t)
	withRSS := renderTemplate(t, tmpl, model.PageData{
		Kind:        model.PageNote,
		SiteRootRel: "../",
		Site: model.SiteConfig{
			Title:    "Field Notes",
			Language: "en",
		},
		Title:   "Guide",
		Content: template.HTML("<p>Body.</p>"),
		HasRSS:  true,
	})
	assertContains(t, withRSS, `<link rel="alternate" type="application/rss+xml" title="Field Notes RSS" href="../index.xml">`)

	withoutRSS := renderTemplate(t, tmpl, model.PageData{
		Kind:        model.PageNote,
		SiteRootRel: "../",
		Site: model.SiteConfig{
			Title:    "Field Notes",
			Language: "en",
		},
		Title:   "Guide",
		Content: template.HTML("<p>Body.</p>"),
	})
	assertNotContains(t, withoutRSS, `type="application/rss+xml"`)
}

func TestDefaultTemplatesRenderPaginationHeadLinksAndNavigation(t *testing.T) {
	t.Parallel()

	tmpl := parseDefaultTemplateSet(t)
	got := renderTemplate(t, tmpl, model.PageData{
		Kind:        model.PageTimeline,
		SiteRootRel: "../../",
		Site: model.SiteConfig{
			Title:    "Field Notes",
			Language: "en",
		},
		Title: "Recent notes",
		TimelineNotes: []model.NoteSummary{{
			Title: "Guide",
			URL:   "../../guide/",
		}},
		Pagination: &model.PaginationData{
			CurrentPage: 2,
			TotalPages:  3,
			PrevURL:     "../../",
			NextURL:     "../3/",
			Pages: []model.PageLink{
				{Number: 1, URL: "../../"},
				{Number: 2, URL: "./"},
				{Number: 3, URL: "../3/"},
			},
		},
	})

	assertContains(t, got, `<link rel="prev" href="../../">`)
	assertContains(t, got, `<link rel="next" href="../3/">`)
	assertContains(t, got, `<nav class="pagination-nav" aria-label="Pagination">`)
	assertContains(t, got, `<a class="pagination-link pagination-link-prev" href="../../" rel="prev">Previous</a>`)
	assertContains(t, got, `<span class="pagination-page" aria-current="page">2</span>`)
	assertContains(t, got, `<a class="pagination-page" href="../../">1</a>`)
	assertContains(t, got, `<a class="pagination-page" href="../3/">3</a>`)
	assertContains(t, got, `<a class="pagination-link pagination-link-next" href="../3/" rel="next">Next</a>`)
}

func TestDefaultTemplatesConditionallyRenderSidebarNavigation(t *testing.T) {
	t.Parallel()

	tmpl := parseDefaultTemplateSet(t)
	enabled := renderTemplate(t, tmpl, model.PageData{
		Kind:        model.PageNote,
		SiteRootRel: "../",
		Site: model.SiteConfig{
			Title:    "Field Notes",
			BaseURL:  "https://example.com/blog/",
			Language: "en",
			Sidebar:  model.SidebarConfig{Enabled: true},
		},
		Title:   "Guide",
		Content: template.HTML("<p>Body.</p>"),
		SidebarTree: []model.SidebarNode{{
			Name:  "notes",
			URL:   "notes/",
			IsDir: true,
			Children: []model.SidebarNode{{
				Name:     "Guide",
				URL:      "guide/",
				IsActive: true,
			}},
		}},
	})

	assertContains(t, enabled, `class="sidebar-launch"`)
	assertContains(t, enabled, `id="sidebar-panel"`)
	assertContains(t, enabled, `data-site-root-rel="../"`)
	assertContains(t, enabled, `data-sidebar-overlay`)
	assertContains(t, enabled, `id="sidebar-data" type="application/json">[{"name":"notes","url":"notes/","isDir":true,"isActive":false,"children":[{"name":"Guide","url":"guide/","isDir":false,"isActive":true}]}]</script>`)
	assertContains(t, enabled, `obsite.sidebar.expanded.v1:\/blog\/`)
	assertContains(t, enabled, `var legacyStorageKey = "obsite.sidebar.expanded.v1"`)
	assertContains(t, enabled, `localStorage.removeItem(legacyStorageKey)`)
	assertContains(t, enabled, `JSON.parse(dataNode.textContent || "[]")`)
	assertContains(t, enabled, `data-sidebar-ready`)

	disabled := renderTemplate(t, tmpl, model.PageData{
		Kind:        model.PageNote,
		SiteRootRel: "../",
		Site: model.SiteConfig{
			Title:    "Field Notes",
			BaseURL:  "https://example.com/blog/",
			Language: "en",
		},
		Title:   "Guide",
		Content: template.HTML("<p>Body.</p>"),
	})

	assertNotContains(t, disabled, `sidebar-data`)
	assertNotContains(t, disabled, `data-sidebar-toggle`)
	assertNotContains(t, disabled, `obsite.sidebar.expanded.v1:\/blog\/`)
}

func TestDefaultTemplatesConditionallyRenderRelatedArticlesSection(t *testing.T) {
	t.Parallel()

	tmpl := parseDefaultTemplateSet(t)
	withRelated := renderTemplate(t, tmpl, model.PageData{
		Kind:        model.PageNote,
		SiteRootRel: "../",
		Site: model.SiteConfig{
			Title:    "Field Notes",
			Language: "en",
		},
		Title:   "Guide",
		Content: template.HTML("<p>Body.</p>"),
		RelatedArticles: []model.RelatedArticle{{
			Title: "Beta",
			URL:   "../beta/",
		}},
	})
	assertContains(t, withRelated, `<h2 id="related-articles-heading">Related Articles</h2>`)
	assertContains(t, withRelated, `<li><a href="../beta/">Beta</a></li>`)

	withoutRelated := renderTemplate(t, tmpl, model.PageData{
		Kind:        model.PageNote,
		SiteRootRel: "../",
		Site: model.SiteConfig{
			Title:    "Field Notes",
			Language: "en",
		},
		Title:   "Guide",
		Content: template.HTML("<p>Body.</p>"),
	})
	assertNotContains(t, withoutRelated, `related-articles-heading`)
	assertNotContains(t, withoutRelated, `Related Articles`)
}

func TestDefaultTemplatesArchiveHeadersDescribeCurrentPageSlice(t *testing.T) {
	t.Parallel()

	tmpl := parseDefaultTemplateSet(t)

	tests := []struct {
		name       string
		data       model.PageData
		want       string
		wantAbsent string
	}{
		{
			name: "tag archive uses page-slice copy",
			data: model.PageData{
				Kind:        model.PageTag,
				SiteRootRel: "../../",
				Site: model.SiteConfig{
					Title:    "Field Notes",
					Language: "en",
				},
				TagName:   "systems",
				TagNotes:  []model.NoteSummary{{Title: "Guide", URL: "../../guide/"}},
				ChildTags: []model.TagLink{{Name: "systems/distributed", URL: "distributed/"}},
				Pagination: &model.PaginationData{
					CurrentPage: 2,
					TotalPages:  2,
					PrevURL:     "../../",
					Pages:       []model.PageLink{{Number: 1, URL: "../../"}, {Number: 2, URL: "./"}},
				},
			},
			want:       `<p class="page-deck">Browse the notes collected under this topic on this page, with 1 nested tags to explore next.</p>`,
			wantAbsent: `Browse 1 notes collected under this topic`,
		},
		{
			name: "folder archive uses page-slice copy",
			data: model.PageData{
				Kind:        model.PageFolder,
				SiteRootRel: "../../",
				Site: model.SiteConfig{
					Title:    "Field Notes",
					Language: "en",
				},
				Title:          "Alpha",
				FolderPath:     "alpha",
				FolderChildren: []model.NoteSummary{{Title: "Guide", URL: "../../guide/"}},
				Pagination: &model.PaginationData{
					CurrentPage: 2,
					TotalPages:  2,
					PrevURL:     "../../",
					Pages:       []model.PageLink{{Number: 1, URL: "../../"}, {Number: 2, URL: "./"}},
				},
			},
			want:       `<p class="page-deck">Browse the published notes on this page filed under alpha.</p>`,
			wantAbsent: `Browse 1 published notes filed under alpha.`,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := renderTemplate(t, tmpl, tt.data)
			assertContains(t, got, tt.want)
			assertNotContains(t, got, tt.wantAbsent)
		})
	}
}

func TestDefaultStylesProvideMobileTableFallback(t *testing.T) {
	t.Parallel()

	css := readTemplateAsset(t, "style.css")
	pattern := regexp.MustCompile(`(?s)@media\s*\(max-width:\s*56rem\)\s*\{.*?\.entry-content table\s*\{.*?display:\s*block;.*?width:\s*max-content;.*?min-width:\s*100%;.*?overflow-x:\s*auto;.*?-webkit-overflow-scrolling:\s*touch;`)
	if !pattern.MatchString(css) {
		t.Fatalf("style.css missing mobile table overflow fallback for narrow screens")
	}
}

func TestDefaultStylesProvideResponsiveSidebarNavigation(t *testing.T) {
	t.Parallel()

	css := readTemplateAsset(t, "style.css")
	assertContains(t, css, `.site-body[data-sidebar-ready="true"] {`)
	assertContains(t, css, `grid-template-columns: minmax(15rem, var(--sidebar-width)) minmax(0, 1fr);`)
	assertContains(t, css, `.sidebar-shell {`)
	assertContains(t, css, `position: sticky;`)

	mobilePattern := regexp.MustCompile(`(?s)@media\s*\(max-width:\s*56rem\)\s*\{.*?\.sidebar-launch\s*\{.*?display:\s*inline-flex;.*?\.sidebar-shell\s*\{.*?position:\s*fixed;.*?transform:\s*translateX\(-105%\);.*?body\[data-sidebar-open="true"\]\s*\.sidebar-shell\s*\{.*?transform:\s*translateX\(0\);`)
	if !mobilePattern.MatchString(css) {
		t.Fatalf("style.css missing responsive mobile sidebar drawer rules")
	}
}

func TestDefaultStylesDefineDarkThemeOverrides(t *testing.T) {
	t.Parallel()

	css := readTemplateAsset(t, "style.css")
	for _, want := range []string{
		":root[data-theme=\"light\"]",
		":root[data-theme=\"dark\"]",
		"@media (prefers-color-scheme: dark)",
		"--theme-toggle-bg",
		"--page-background",
		".sr-only",
	} {
		if !strings.Contains(css, want) {
			t.Fatalf("style.css missing %q", want)
		}
	}
}

func TestDefaultStylesExposeHeadingAnchorAffordance(t *testing.T) {
	t.Parallel()

	css := readTemplateAsset(t, "style.css")
	for _, want := range []string{
		".page-title[id]",
		".entry-content h1[id]",
		"scroll-margin-top: 1.35rem;",
		"content: \"#\";",
		".page-title[id]:hover::before",
		".entry-content h2[id]:target::before",
	} {
		if !strings.Contains(css, want) {
			t.Fatalf("style.css missing %q", want)
		}
	}
}

func TestDefaultStylesStylePaginationNavigation(t *testing.T) {
	t.Parallel()

	css := readTemplateAsset(t, "style.css")
	navPattern := regexp.MustCompile(`(?s)\.pagination-nav\s*\{.*?display:\s*flex;.*?flex-wrap:\s*wrap;`)
	if !navPattern.MatchString(css) {
		t.Fatalf("style.css missing wrapped pagination nav layout")
	}

	pagesPattern := regexp.MustCompile(`(?s)\.pagination-pages\s*\{.*?display:\s*flex;.*?list-style:\s*none;`)
	if !pagesPattern.MatchString(css) {
		t.Fatalf("style.css missing inline pagination pages styling")
	}

	currentPattern := regexp.MustCompile(`(?s)\.pagination-page\[aria-current="page"\]\s*\{.*?background:\s*var\(--tag-bg\);`)
	if !currentPattern.MatchString(css) {
		t.Fatalf("style.css missing current-page pagination emphasis")
	}

	if !strings.Contains(css, `.pagination-link-prev {`) || !strings.Contains(css, `margin-right: auto;`) {
		t.Fatalf("style.css missing previous-link pagination alignment")
	}
	if !strings.Contains(css, `.pagination-link-next {`) || !strings.Contains(css, `margin-left: auto;`) {
		t.Fatalf("style.css missing next-link pagination alignment")
	}
	if !strings.Contains(css, `flex-basis: 100%;`) || !strings.Contains(css, `justify-content: flex-start;`) {
		t.Fatalf("style.css missing narrow-screen pagination wrapping rules")
	}
}

func parseDefaultTemplateSet(t *testing.T) *template.Template {
	t.Helper()

	root := repoRoot(t)
	tmpl, err := template.New("base").Funcs(template.FuncMap{
		"toJSON":       templateJSON,
		"pageAssetURL": pageAssetURL,
		"siteBasePath": siteBasePath,
	}).ParseFiles(
		filepath.Join(root, "templates", "base.html"),
		filepath.Join(root, "templates", "note.html"),
		filepath.Join(root, "templates", "index.html"),
		filepath.Join(root, "templates", "tag.html"),
		filepath.Join(root, "templates", "folder.html"),
		filepath.Join(root, "templates", "timeline.html"),
		filepath.Join(root, "templates", "404.html"),
	)
	if err != nil {
		t.Fatalf("template.ParseFiles() error = %v", err)
	}

	return tmpl
}

func renderTemplate(t *testing.T, tmpl *template.Template, data model.PageData) string {
	t.Helper()

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "base", data); err != nil {
		t.Fatalf("ExecuteTemplate(base) error = %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("ExecuteTemplate(base) wrote empty output")
	}

	return buf.String()
}

func readTemplateAsset(t *testing.T, name string) string {
	t.Helper()

	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "templates", name))
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error = %v", name, err)
	}

	return string(data)
}

func assertContains(t *testing.T, got string, want string) {
	t.Helper()

	if !strings.Contains(got, want) {
		t.Fatalf("rendered output missing %q\noutput:\n%s", want, got)
	}
}

func assertNotContains(t *testing.T, got string, wantAbsent string) {
	t.Helper()

	if strings.Contains(got, wantAbsent) {
		t.Fatalf("rendered output unexpectedly contained %q\noutput:\n%s", wantAbsent, got)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}

	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}

func themeBaseWithBodyAttribute(t *testing.T, bodyAttribute string) string {
	t.Helper()

	base, err := readEmbeddedAsset("base.html")
	if err != nil {
		t.Fatalf("readEmbeddedAsset(base.html) error = %v", err)
	}
	bodyAttribute = strings.TrimSpace(bodyAttribute)
	if bodyAttribute == "" {
		return string(base)
	}

	marker := `<body class="kind-{{.Kind}}">`
	replacement := `<body class="kind-{{.Kind}}" ` + bodyAttribute + `>`
	updated := strings.Replace(string(base), marker, replacement, 1)
	if updated == string(base) {
		t.Fatal("themeBaseWithBodyAttribute() did not find body marker in embedded base template")
	}

	return updated
}

func writeCompleteThemeRoot(t *testing.T, themeRoot string, overrides map[string]string) {
	t.Helper()

	files := make(map[string]string, len(RequiredHTMLTemplateNames)+len(overrides))
	for _, name := range RequiredHTMLTemplateNames {
		data, err := readEmbeddedAsset(name)
		if err != nil {
			t.Fatalf("readEmbeddedAsset(%q) error = %v", name, err)
		}
		files[name] = string(data)
	}
	for path, contents := range overrides {
		files[path] = contents
	}

	writeThemeRootFiles(t, themeRoot, files)
}

func writeThemeRootFiles(t *testing.T, themeRoot string, files map[string]string) {
	t.Helper()

	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	for _, relPath := range paths {
		absPath := filepath.Join(themeRoot, filepath.FromSlash(relPath))
		if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
			t.Fatalf("os.MkdirAll(%q) error = %v", filepath.Dir(absPath), err)
		}
		if err := os.WriteFile(absPath, []byte(files[relPath]), 0o644); err != nil {
			t.Fatalf("os.WriteFile(%q) error = %v", relPath, err)
		}
	}
}

func renderThemeRootNoteHTML(t *testing.T, site model.SiteConfig) string {
	t.Helper()

	note, err := RenderNote(NotePageInput{
		Site: site,
		Note: &model.Note{
			RelPath:     "notes/guide.md",
			Slug:        "guide",
			HTMLContent: "<p>Rendered note body.</p>",
			Frontmatter: model.Frontmatter{Title: "Guide"},
		},
	})
	if err != nil {
		t.Fatalf("RenderNote() error = %v", err)
	}

	return string(note.HTML)
}

func renderThemeRootIndexHTML(t *testing.T, site model.SiteConfig) string {
	t.Helper()

	page, err := RenderIndex(IndexPageInput{
		Site: site,
		RecentNotes: []model.NoteSummary{{
			Title: "Guide",
			URL:   "guide/",
		}},
	})
	if err != nil {
		t.Fatalf("RenderIndex() error = %v", err)
	}

	return string(page.HTML)
}

func renderThemeRoot404HTML(t *testing.T, site model.SiteConfig) string {
	t.Helper()

	page, err := Render404(NotFoundPageInput{
		Site: site,
		RecentNotes: []model.NoteSummary{{
			Title: "Guide",
			URL:   "guide/",
		}},
	})
	if err != nil {
		t.Fatalf("Render404() error = %v", err)
	}

	return string(page.HTML)
}

func renderThemeRootFolderHTML(t *testing.T, site model.SiteConfig) string {
	t.Helper()

	page, err := RenderFolderPage(FolderPageInput{
		Site:       site,
		FolderPath: "journal",
		Children: []model.NoteSummary{{
			Title: "Guide",
			URL:   "../guide/",
		}},
	})
	if err != nil {
		t.Fatalf("RenderFolderPage() error = %v", err)
	}

	return string(page.HTML)
}

func renderThemeRootTimelineHTML(t *testing.T, site model.SiteConfig) string {
	t.Helper()

	page, err := RenderTimelinePage(TimelinePageInput{
		Site:         site,
		TimelinePath: "notes",
		Notes: []model.NoteSummary{{
			Title: "Guide",
			URL:   "../guide/",
		}},
	})
	if err != nil {
		t.Fatalf("RenderTimelinePage() error = %v", err)
	}

	return string(page.HTML)
}

func countTemplateSetCacheEntriesForThemeRoot(t *testing.T, themeRoot string) int {
	t.Helper()

	normalizedRoot, err := normalizeThemeRoot(themeRoot)
	if err != nil {
		t.Fatalf("normalizeThemeRoot(%q) error = %v", themeRoot, err)
	}

	count := 0
	templateSetCache.Range(func(key, _ any) bool {
		if templateSetCacheKeyMatchesThemeRoot(key, normalizedRoot) {
			count++
		}
		return true
	})

	return count
}

func countTemplateSetCacheEntriesForThemeIdentity(t *testing.T, themeName string, themeRoot string) int {
	t.Helper()

	normalizedRoot, err := normalizeThemeRoot(themeRoot)
	if err != nil {
		t.Fatalf("normalizeThemeRoot(%q) error = %v", themeRoot, err)
	}

	count := 0
	templateSetCache.Range(func(key, _ any) bool {
		if templateSetCacheKeyMatchesThemeIdentity(key, themeName, normalizedRoot) {
			count++
		}
		return true
	})

	return count
}

func clearTemplateSetCacheEntriesForThemeRoot(t *testing.T, themeRoot string) {
	t.Helper()

	normalizedRoot, err := normalizeThemeRoot(themeRoot)
	if err != nil {
		t.Fatalf("normalizeThemeRoot(%q) error = %v", themeRoot, err)
	}

	keys := make([]any, 0, 2)
	templateSetCache.Range(func(key, _ any) bool {
		if templateSetCacheKeyMatchesThemeRoot(key, normalizedRoot) {
			keys = append(keys, key)
		}
		return true
	})
	for _, key := range keys {
		templateSetCache.Delete(key)
	}
	themeTemplateSnapshotCache.Range(func(key, _ any) bool {
		if identity, ok := key.(themeIdentity); ok && identity.themeRoot == normalizedRoot {
			themeTemplateSnapshotCache.Delete(key)
		}
		return true
	})
}

func trackThemeTemplateFileReadsForRoot(t *testing.T, themeRoot string) func() int {
	t.Helper()

	normalizedRoot, err := normalizeThemeRoot(themeRoot)
	if err != nil {
		t.Fatalf("normalizeThemeRoot(%q) error = %v", themeRoot, err)
	}

	var reads atomic.Int64
	themeTemplateFileReader.mu.Lock()
	previous := themeTemplateFileReader.read
	themeTemplateFileReader.read = func(filePath string) ([]byte, error) {
		data, err := previous(filePath)
		if strings.HasPrefix(filePath, normalizedRoot+string(filepath.Separator)) {
			reads.Add(1)
		}
		return data, err
	}
	themeTemplateFileReader.mu.Unlock()

	t.Cleanup(func() {
		themeTemplateFileReader.mu.Lock()
		themeTemplateFileReader.read = previous
		themeTemplateFileReader.mu.Unlock()
	})

	return func() int {
		return int(reads.Load())
	}
}

func templateSetCacheKeyMatchesThemeRoot(key any, themeRoot string) bool {
	typed, ok := key.(templateSetCacheKey)
	return ok && typed.themeRoot == themeRoot
}

func templateSetCacheKeyMatchesThemeIdentity(key any, themeName string, themeRoot string) bool {
	typed, ok := key.(templateSetCacheKey)
	return ok && typed.activeThemeName == themeName && typed.themeRoot == themeRoot
}

func findThemeTemplateFile(t *testing.T, files []themeTemplateFile, name string) themeTemplateFile {
	t.Helper()

	for _, file := range files {
		if filepath.Base(file.path) == name {
			return file
		}
	}
	t.Fatalf("theme template snapshot missing %q", name)
	return themeTemplateFile{}
}

func containsTemplateName(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}

	return false
}

func equalTemplateNameSlices(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}

	return true
}
