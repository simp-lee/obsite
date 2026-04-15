package render

import (
	"bytes"
	"html/template"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/simp-lee/obsite/internal/config"
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
			},
			want: []string{
				"<link rel=\"stylesheet\" href=\"../style.css\">",
				"<div class=\"entry-content\" data-page-content>",
			},
			wantAbsent: []string{
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
	loadedSite, err := config.LoadForBuild("", config.Overrides{
		Title:   "Field Notes",
		BaseURL: "https://example.com/",
	})
	if err != nil {
		t.Fatalf("config.LoadForBuild() error = %v", err)
	}
	site := loadedSite.Config
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

func TestTemplateDirOverridesMatchingTemplatesAndFallsBackToDefaults(t *testing.T) {
	t.Parallel()

	templateDir := t.TempDir()
	override := `{{define "content-note"}}<section data-custom-note>{{.Title}}</section>{{end}}`
	if err := os.WriteFile(filepath.Join(templateDir, "note.html"), []byte(override), 0o644); err != nil {
		t.Fatalf("os.WriteFile(note.html) error = %v", err)
	}

	site := testSiteConfig()
	site.TemplateDir = templateDir

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
	assertContains(t, string(note.HTML), "<link rel=\"stylesheet\" href=\"../style.css\">")

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

func TestTemplateDirCachesParsedTemplateSetByNormalizedDir(t *testing.T) {
	t.Parallel()

	templateDir := t.TempDir()
	t.Cleanup(func() {
		clearTemplateSetCacheEntriesForDir(t, templateDir)
	})

	overridePath := filepath.Join(templateDir, "base.html")
	override := `{{define "base"}}<!doctype html><html><body data-cache-base="true">{{template "content" .}}</body></html>{{end}}`
	if err := os.WriteFile(overridePath, []byte(override), 0o644); err != nil {
		t.Fatalf("os.WriteFile(base.html) error = %v", err)
	}

	site := testSiteConfig()
	site.TemplateDir = filepath.Join(templateDir, ".")

	first, err := loadTemplateSet(site)
	if err != nil {
		t.Fatalf("loadTemplateSet() error = %v", err)
	}

	site.TemplateDir = templateDir
	second, err := loadTemplateSet(site)
	if err != nil {
		t.Fatalf("loadTemplateSet() error = %v", err)
	}
	if first != second {
		t.Fatal("loadTemplateSet() returned different template set instances for the same normalized directory and unchanged overrides")
	}

	if got := countTemplateSetCacheEntriesForDir(t, templateDir); got != 1 {
		t.Fatalf("countTemplateSetCacheEntriesForDir(%q) = %d, want %d", templateDir, got, 1)
	}
}

func TestTemplateDirReusesCachedOverrideSnapshotUntilFilesChange(t *testing.T) {
	templateDir := t.TempDir()
	t.Cleanup(func() {
		clearTemplateSetCacheEntriesForDir(t, templateDir)
	})

	overridePath := filepath.Join(templateDir, "base.html")
	baseV1 := `{{define "base"}}<!doctype html><html><body data-snapshot-cache="v1">{{template "content" .}}</body></html>{{end}}`
	if err := os.WriteFile(overridePath, []byte(baseV1), 0o644); err != nil {
		t.Fatalf("os.WriteFile(base.html v1) error = %v", err)
	}

	readCount := trackTemplateOverrideFileReadsForDir(t, templateDir)
	site := testSiteConfig()
	site.TemplateDir = filepath.Join(templateDir, ".")

	first, err := loadTemplateSet(site)
	if err != nil {
		t.Fatalf("loadTemplateSet() error = %v", err)
	}
	if got := readCount(); got != 1 {
		t.Fatalf("template override reads after first load = %d, want %d", got, 1)
	}

	second, err := loadTemplateSet(site)
	if err != nil {
		t.Fatalf("loadTemplateSet() second error = %v", err)
	}
	if first != second {
		t.Fatal("loadTemplateSet() returned different template set instances for unchanged override snapshot")
	}
	if got := readCount(); got != 2 {
		t.Fatalf("template override reads after unchanged reload = %d, want %d", got, 2)
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
		t.Fatal("loadTemplateSet() returned cached template set after override file changed")
	}
	if got := readCount(); got != 3 {
		t.Fatalf("template override reads after changed reload = %d, want %d", got, 3)
	}
}

func TestCachedTemplateOverrideSnapshotReloadsWhenContentsChangeUnderSameState(t *testing.T) {
	templateDir := t.TempDir()
	overridePath := filepath.Join(templateDir, "base.html")
	baseV1 := `{{define "base"}}<!doctype html><html><body data-same-state="v1">{{template "content" .}}</body></html>{{end}}`
	if err := os.WriteFile(overridePath, []byte(baseV1), 0o644); err != nil {
		t.Fatalf("os.WriteFile(base.html v1) error = %v", err)
	}

	state, err := scanTemplateOverrideState(templateDir)
	if err != nil {
		t.Fatalf("scanTemplateOverrideState(%q) error = %v", templateDir, err)
	}

	var cached cachedTemplateOverrideSnapshot
	first, err := cached.load(state)
	if err != nil {
		t.Fatalf("cached.load(state) first error = %v", err)
	}
	if len(first.files) != 1 {
		t.Fatalf("len(first.files) = %d, want %d", len(first.files), 1)
	}
	if !strings.Contains(first.files[0].contents, `data-same-state="v1"`) {
		t.Fatalf("first snapshot contents = %q, want v1 template contents", first.files[0].contents)
	}

	baseV2 := strings.Replace(baseV1, "v1", "v2", 1)
	if len(baseV2) != len(baseV1) {
		t.Fatalf("len(baseV2) = %d, want same length as len(baseV1) = %d", len(baseV2), len(baseV1))
	}
	if err := os.WriteFile(overridePath, []byte(baseV2), 0o644); err != nil {
		t.Fatalf("os.WriteFile(base.html v2) error = %v", err)
	}

	second, err := cached.load(state)
	if err != nil {
		t.Fatalf("cached.load(state) second error = %v", err)
	}
	if second.signature == first.signature {
		t.Fatalf("second.signature = %q, want a new signature after same-state content edit", second.signature)
	}
	if len(second.files) != 1 {
		t.Fatalf("len(second.files) = %d, want %d", len(second.files), 1)
	}
	if !strings.Contains(second.files[0].contents, `data-same-state="v2"`) {
		t.Fatalf("second snapshot contents = %q, want v2 template contents", second.files[0].contents)
	}
	if strings.Contains(second.files[0].contents, `data-same-state="v1"`) {
		t.Fatalf("second snapshot contents = %q, want stale v1 contents to be replaced", second.files[0].contents)
	}
}

func TestTemplateDirReloadsTemplatesWhenOverridesChangeWithinSameProcess(t *testing.T) {
	t.Parallel()

	templateDir := t.TempDir()
	t.Cleanup(func() {
		clearTemplateSetCacheEntriesForDir(t, templateDir)
	})

	basePath := filepath.Join(templateDir, "base.html")
	baseV1 := `{{define "base"}}<!doctype html><html><body data-live-base="v1">{{template "content" .}}</body></html>{{end}}`
	if err := os.WriteFile(basePath, []byte(baseV1), 0o644); err != nil {
		t.Fatalf("os.WriteFile(base.html v1) error = %v", err)
	}

	site := testSiteConfig()
	site.TemplateDir = filepath.Join(templateDir, ".")

	noteHTML := renderTemplateDirNoteHTML(t, site)
	assertContains(t, noteHTML, `data-live-base="v1"`)

	baseV2 := `{{define "base"}}<!doctype html><html><body data-live-base="v2">{{template "content" .}}</body></html>{{end}}`
	if err := os.WriteFile(basePath, []byte(baseV2), 0o644); err != nil {
		t.Fatalf("os.WriteFile(base.html v2) error = %v", err)
	}

	notFoundHTML := renderTemplateDir404HTML(t, site)
	assertContains(t, notFoundHTML, `data-live-base="v2"`)
	assertNotContains(t, notFoundHTML, `data-live-base="v1"`)

	if err := os.Remove(basePath); err != nil {
		t.Fatalf("os.Remove(base.html) error = %v", err)
	}

	indexHTML := renderTemplateDirIndexHTML(t, site)
	assertNotContains(t, indexHTML, `data-live-base=`)
	assertContains(t, indexHTML, `<h2 id="recent-notes-heading">Recent notes</h2>`)

	notFoundOverride := `{{define "content-404"}}<section data-live-404>{{.Title}}</section>{{end}}`
	if err := os.WriteFile(filepath.Join(templateDir, "404.html"), []byte(notFoundOverride), 0o644); err != nil {
		t.Fatalf("os.WriteFile(404.html) error = %v", err)
	}

	notFoundHTML = renderTemplateDir404HTML(t, site)
	assertContains(t, notFoundHTML, `<section data-live-404>Not found</section>`)

	timelineHTML := renderTemplateDirTimelineHTML(t, site)
	assertContains(t, timelineHTML, `<h2 id="timeline-notes-heading">Recent notes</h2>`)
	assertNotContains(t, timelineHTML, `data-live-404`)
}

func TestTemplateDirRepeatedEditsDoNotGrowCacheCardinality(t *testing.T) {
	t.Parallel()

	templateDir := t.TempDir()
	t.Cleanup(func() {
		clearTemplateSetCacheEntriesForDir(t, templateDir)
	})

	site := testSiteConfig()
	site.TemplateDir = filepath.Join(templateDir, ".")

	basePath := filepath.Join(templateDir, "base.html")
	baseV1 := `{{define "base"}}<!doctype html><html><body data-cache-cardinality="v1">{{template "content" .}}</body></html>{{end}}`
	if err := os.WriteFile(basePath, []byte(baseV1), 0o644); err != nil {
		t.Fatalf("os.WriteFile(base.html v1) error = %v", err)
	}

	noteHTML := renderTemplateDirNoteHTML(t, site)
	assertContains(t, noteHTML, `data-cache-cardinality="v1"`)
	if got := countTemplateSetCacheEntriesForDir(t, templateDir); got != 1 {
		t.Fatalf("countTemplateSetCacheEntriesForDir(%q) after initial load = %d, want %d", templateDir, got, 1)
	}

	baseV2 := `{{define "base"}}<!doctype html><html><body data-cache-cardinality="v2">{{template "content" .}}</body></html>{{end}}`
	if err := os.WriteFile(basePath, []byte(baseV2), 0o644); err != nil {
		t.Fatalf("os.WriteFile(base.html v2) error = %v", err)
	}

	notFoundHTML := renderTemplateDir404HTML(t, site)
	assertContains(t, notFoundHTML, `data-cache-cardinality="v2"`)
	assertNotContains(t, notFoundHTML, `data-cache-cardinality="v1"`)
	if got := countTemplateSetCacheEntriesForDir(t, templateDir); got != 1 {
		t.Fatalf("countTemplateSetCacheEntriesForDir(%q) after base update = %d, want %d", templateDir, got, 1)
	}

	if err := os.Remove(basePath); err != nil {
		t.Fatalf("os.Remove(base.html) error = %v", err)
	}

	indexHTML := renderTemplateDirIndexHTML(t, site)
	assertContains(t, indexHTML, `<h2 id="recent-notes-heading">Recent notes</h2>`)
	assertNotContains(t, indexHTML, `data-cache-cardinality=`)
	if got := countTemplateSetCacheEntriesForDir(t, templateDir); got != 1 {
		t.Fatalf("countTemplateSetCacheEntriesForDir(%q) after base removal = %d, want %d", templateDir, got, 1)
	}

	notFoundOverride := `{{define "content-404"}}<section data-cache-cardinality-404>{{.Title}}</section>{{end}}`
	if err := os.WriteFile(filepath.Join(templateDir, "404.html"), []byte(notFoundOverride), 0o644); err != nil {
		t.Fatalf("os.WriteFile(404.html) error = %v", err)
	}

	notFoundHTML = renderTemplateDir404HTML(t, site)
	assertContains(t, notFoundHTML, `<section data-cache-cardinality-404>Not found</section>`)
	if got := countTemplateSetCacheEntriesForDir(t, templateDir); got != 1 {
		t.Fatalf("countTemplateSetCacheEntriesForDir(%q) after 404 addition = %d, want %d", templateDir, got, 1)
	}
}

func TestTemplateDirOverridesBaseAndTagTemplatesAndFallsBackToDefaults(t *testing.T) {
	t.Parallel()

	templateDir := t.TempDir()
	baseOverride := `{{define "base"}}<!doctype html><html><head><title>{{.Title}}</title></head><body data-custom-base="true">{{template "content" .}}</body></html>{{end}}`
	tagOverride := `{{define "content-tag"}}<section data-custom-tag>#{{.TagName}}</section>{{end}}`
	if err := os.WriteFile(filepath.Join(templateDir, "base.html"), []byte(baseOverride), 0o644); err != nil {
		t.Fatalf("os.WriteFile(base.html) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(templateDir, "tag.html"), []byte(tagOverride), 0o644); err != nil {
		t.Fatalf("os.WriteFile(tag.html) error = %v", err)
	}

	site := testSiteConfig()
	site.TemplateDir = templateDir

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

func TestTemplateDirOverridesAllowedPageTemplatesIndividuallyAndFallsBackElsewhere(t *testing.T) {
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
			renderTarget:       renderTemplateDirIndexHTML,
			wantTarget:         `<section data-custom-index>Field Notes</section>`,
			renderFallback:     renderTemplateDir404HTML,
			wantFallback:       `Return to the homepage`,
			wantFallbackAbsent: `data-custom-index`,
		},
		{
			name:               "404 template override",
			overrideFile:       "404.html",
			overrideContents:   `{{define "content-404"}}<section data-custom-404>{{.Title}}</section>{{end}}`,
			renderTarget:       renderTemplateDir404HTML,
			wantTarget:         `<section data-custom-404>Not found</section>`,
			renderFallback:     renderTemplateDirIndexHTML,
			wantFallback:       `<h2 id="recent-notes-heading">Recent notes</h2>`,
			wantFallbackAbsent: `data-custom-404`,
		},
		{
			name:               "folder template override",
			overrideFile:       "folder.html",
			overrideContents:   `{{define "content-folder"}}<section data-custom-folder>{{.FolderPath}}</section>{{end}}`,
			renderTarget:       renderTemplateDirFolderHTML,
			wantTarget:         `<section data-custom-folder>journal</section>`,
			renderFallback:     renderTemplateDirTimelineHTML,
			wantFallback:       `<h2 id="timeline-notes-heading">Recent notes</h2>`,
			wantFallbackAbsent: `data-custom-folder`,
		},
		{
			name:               "timeline template override",
			overrideFile:       "timeline.html",
			overrideContents:   `{{define "content-timeline"}}<section data-custom-timeline>{{.Title}}</section>{{end}}`,
			renderTarget:       renderTemplateDirTimelineHTML,
			wantTarget:         `<section data-custom-timeline>Recent notes</section>`,
			renderFallback:     renderTemplateDirFolderHTML,
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
				clearTemplateSetCacheEntriesForDir(t, templateDir)
			})

			if err := os.WriteFile(filepath.Join(templateDir, tt.overrideFile), []byte(tt.overrideContents), 0o644); err != nil {
				t.Fatalf("os.WriteFile(%s) error = %v", tt.overrideFile, err)
			}

			site := testSiteConfig()
			site.TemplateDir = filepath.Join(templateDir, ".")

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

func renderTemplateDirNoteHTML(t *testing.T, site model.SiteConfig) string {
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

func renderTemplateDirIndexHTML(t *testing.T, site model.SiteConfig) string {
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

func renderTemplateDir404HTML(t *testing.T, site model.SiteConfig) string {
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

func renderTemplateDirFolderHTML(t *testing.T, site model.SiteConfig) string {
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

func renderTemplateDirTimelineHTML(t *testing.T, site model.SiteConfig) string {
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

func countTemplateSetCacheEntriesForDir(t *testing.T, templateDir string) int {
	t.Helper()

	normalizedDir, err := normalizeTemplateDir(templateDir)
	if err != nil {
		t.Fatalf("normalizeTemplateDir(%q) error = %v", templateDir, err)
	}

	count := 0
	templateSetCache.Range(func(key, _ any) bool {
		if templateSetCacheKeyMatchesDir(key, normalizedDir) {
			count++
		}
		return true
	})

	return count
}

func clearTemplateSetCacheEntriesForDir(t *testing.T, templateDir string) {
	t.Helper()

	normalizedDir, err := normalizeTemplateDir(templateDir)
	if err != nil {
		t.Fatalf("normalizeTemplateDir(%q) error = %v", templateDir, err)
	}

	keys := make([]any, 0, 2)
	templateSetCache.Range(func(key, _ any) bool {
		if templateSetCacheKeyMatchesDir(key, normalizedDir) {
			keys = append(keys, key)
		}
		return true
	})
	for _, key := range keys {
		templateSetCache.Delete(key)
	}
	templateOverrideSnapshotCache.Delete(normalizedDir)
}

func trackTemplateOverrideFileReadsForDir(t *testing.T, templateDir string) func() int {
	t.Helper()

	normalizedDir, err := normalizeTemplateDir(templateDir)
	if err != nil {
		t.Fatalf("normalizeTemplateDir(%q) error = %v", templateDir, err)
	}

	var reads atomic.Int64
	templateOverrideFileReader.mu.Lock()
	previous := templateOverrideFileReader.read
	templateOverrideFileReader.read = func(filePath string) ([]byte, error) {
		data, err := previous(filePath)
		if strings.HasPrefix(filePath, normalizedDir+string(filepath.Separator)) {
			reads.Add(1)
		}
		return data, err
	}
	templateOverrideFileReader.mu.Unlock()

	t.Cleanup(func() {
		templateOverrideFileReader.mu.Lock()
		templateOverrideFileReader.read = previous
		templateOverrideFileReader.mu.Unlock()
	})

	return func() int {
		return int(reads.Load())
	}
}

func templateSetCacheKeyMatchesDir(key any, templateDir string) bool {
	switch typed := key.(type) {
	case string:
		return typed == templateDir
	}

	value := reflect.ValueOf(key)
	if value.Kind() != reflect.Struct {
		return false
	}

	field := value.FieldByName("templateDir")
	return field.IsValid() && field.Kind() == reflect.String && field.String() == templateDir
}
