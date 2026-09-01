package render

import (
	"bytes"
	"html/template"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
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
					Title:        "Field Notes",
					Description:  "An editorial notebook.",
					Author:       "Alice Example",
					Language:     "en",
					RuntimeJSURL: "assets/obsite/runtime.test.js",
					DefaultImg:   "images/default-og.png",
					BaseURL:      "https://example.com/",
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
				`data-obsite-base-path="/" data-obsite-math data-obsite-mermaid`,
				"<script src=\"../assets/obsite/runtime.test.js\"></script>",
				"<link rel=\"stylesheet\" href=\"../style.css\">",
				"<link rel=\"stylesheet\" href=\"../assets/obsite-runtime/katex.min.css\">",
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
				"<article class=\"page-shell article-page\" data-page-content>",
			},
			wantAbsent: []string{
				"<meta name=\"twitter:image\"",
				"cdn.jsdelivr.net",
				"renderMathInElement",
				"window.mermaid",
				"runtime.test.js",
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

func TestDefaultTemplatesExposeStableObsiteLandmarksForEveryPageKind(t *testing.T) {
	t.Parallel()
	tmpl := parseDefaultTemplateSet(t)
	for _, kind := range []model.PageKind{model.PageNote, model.PageIndex, model.PageTag, model.PageFolder, model.PageTimeline, model.Page404} {
		kind := kind
		t.Run(string(kind), func(t *testing.T) {
			t.Parallel()
			got := renderTemplate(t, tmpl, model.PageData{Kind: kind, Site: model.SiteConfig{Title: "Garden"}, Title: "Page", Content: template.HTML("<p>body</p>")})
			for _, marker := range []string{"data-obsite-root", `data-obsite-kind="` + string(kind) + `"`, "data-obsite-header", "data-obsite-main", "data-page-content", "data-obsite-footer"} {
				assertContains(t, got, marker)
			}
			if count := strings.Count(got, "data-page-content"); count != 1 {
				t.Fatalf("data-page-content count = %d, want 1\noutput:\n%s", count, got)
			}
		})
	}
}

func TestDefaultTemplatesOrderStructuralRuntimeThemeAndCustomStyles(t *testing.T) {
	t.Parallel()
	tmpl := parseDefaultTemplateSet(t)
	site := model.SiteConfig{
		Title:     "Field Notes",
		Language:  "en",
		ThemeCSS:  "/vault/.obsite/theme/theme.css",
		CustomCSS: "/vault/custom.css",
	}
	got := renderTemplate(t, tmpl, model.PageData{Kind: model.PageNote, SiteRootRel: "../../", Site: site, Title: "Math", Content: template.HTML(`<p>$x$</p>`), HasMath: true, HasThemeCSS: true, HasCustomCSS: true})
	ordered := []string{
		`href="../../style.css"`,
		`href="../../assets/obsite-runtime/katex.min.css"`,
		`href="../../assets/theme/theme.css"`,
		`href="../../assets/custom.css"`,
	}
	previous := -1
	for _, marker := range ordered {
		index := strings.Index(got, marker)
		if index < 0 {
			t.Fatalf("rendered page missing %q\n%s", marker, got)
		}
		if index <= previous {
			t.Fatalf("stylesheet %q is out of cascade order\n%s", marker, got)
		}
		previous = index
	}
}

func TestDefaultTemplatesMarkMermaidForSharedRuntime(t *testing.T) {
	t.Parallel()
	tmpl := parseDefaultTemplateSet(t)
	site := model.SiteConfig{Title: "Field Notes", BaseURL: "https://example.com/", Language: "en", RuntimeJSURL: "assets/obsite/runtime.test.js"}
	got := renderTemplate(t, tmpl, model.PageData{Kind: model.PageNote, SiteRootRel: "../", Site: site, Title: "Mermaid Note", Content: template.HTML(`<pre class="mermaid">graph TD;A-->B</pre>`), HasMermaid: true})
	assertContains(t, got, `data-obsite-mermaid`)
	assertContains(t, got, `<script src="../assets/obsite/runtime.test.js"></script>`)
	assertNotContains(t, got, `assets/obsite-runtime/mermaid.min.js`)
	assertNotContains(t, got, "window.mermaid.initialize")
	assertNotContains(t, got, "cdn.jsdelivr.net")
}

func TestDefaultTemplatesIncludeThemeToggleAndSharedRuntime(t *testing.T) {
	t.Parallel()

	tmpl := parseDefaultTemplateSet(t)
	got := renderTemplate(t, tmpl, model.PageData{
		Kind:        model.PageIndex,
		SiteRootRel: "./",
		Site: model.SiteConfig{
			Title:        "Field Notes",
			BaseURL:      "https://example.com/blog/",
			Description:  "An editorial notebook.",
			Language:     "en",
			RuntimeJSURL: "assets/obsite/runtime.test.js",
		},
		Title: "Field Notes",
	})

	assertContains(t, got, "<meta name=\"color-scheme\" content=\"light dark\">")
	assertContains(t, got, `data-obsite-base-path="/blog/"`)
	assertContains(t, got, `<script src="./assets/obsite/runtime.test.js"></script>`)
	assertContains(t, got, "data-theme-toggle")
	assertContains(t, got, "aria-labelledby=\"theme-toggle-name\"")
	assertContains(t, got, "aria-describedby=\"theme-toggle-state theme-toggle-source\"")
	assertContains(t, got, "aria-pressed=\"false\"")
	assertContains(t, got, "hidden>")
	assertContains(t, got, "data-theme-toggle-value")
	assertContains(t, got, "data-theme-toggle-state")
	assertContains(t, got, "data-theme-toggle-source")
	for _, forbidden := range []string{"var storageKey", "migrateStoredTheme", "renderMathInElement", "window.mermaid.initialize", "__obsiteInitThemeToggle"} {
		assertNotContains(t, got, forbidden)
	}
}

func TestSharedRuntimeAppliesColorBeforeDOMReadyInitialization(t *testing.T) {
	t.Parallel()

	runtimeJS := readTemplateAsset(t, "runtime.js")
	assertOrderedStrings(t, runtimeJS, "var initialPreference = readStoredTheme();", "applyTheme(initialPreference);", "onReady(function ()")
	for _, want := range []string{
		`var storageKey = "obsite.theme.v1:" + basePath`,
		`prefers-color-scheme: dark`,
		`root.setAttribute("data-theme", preference)`,
		`window.localStorage.setItem(storageKey, value)`,
		`toggle.hidden = false`,
	} {
		assertContains(t, runtimeJS, want)
	}

	tmpl := parseDefaultTemplateSet(t)
	got := renderTemplate(t, tmpl, model.PageData{Kind: model.PageNote, SiteRootRel: "../", Site: model.SiteConfig{Title: "Field Notes", Language: "en", RuntimeJSURL: "assets/obsite/runtime.test.js"}, Title: "Runtime", HasMath: true, HasMermaid: true})
	assertContains(t, got, `data-obsite-math data-obsite-mermaid`)
	assertOrderedStrings(t, got, `<script src="../assets/obsite/runtime.test.js"></script>`, `<link rel="stylesheet" href="../style.css">`)
	assertNotContains(t, got, `<script defer src="../assets/obsite/runtime.test.js"></script>`)
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

func TestDefaultTemplatesConditionallyRenderSidebarMounts(t *testing.T) {
	t.Parallel()

	tmpl := parseDefaultTemplateSet(t)
	enabled := renderTemplate(t, tmpl, model.PageData{
		Kind:        model.PageNote,
		SiteRootRel: "../",
		Site: model.SiteConfig{
			Title:        "Field Notes",
			BaseURL:      "https://example.com/blog/",
			Language:     "en",
			RuntimeJSURL: "assets/obsite/runtime.test.js",
			Sidebar:      model.SidebarConfig{Enabled: true},
		},
		Title:   "Guide",
		Content: template.HTML("<p>Body.</p>"),
	})

	assertContains(t, enabled, `data-obsite-sidebar`)
	assertContains(t, enabled, `class="sidebar-launch"`)
	assertContains(t, enabled, `id="sidebar-panel"`)
	assertContains(t, enabled, `data-site-root-rel="../"`)
	assertContains(t, enabled, `data-sidebar-overlay`)
	assertNotContains(t, enabled, `sidebar-data`)
	assertNotContains(t, enabled, `"isActive"`)
	assertNotContains(t, enabled, `obsite.sidebar.expanded.v1`)

	runtimeJS := readTemplateAsset(t, "runtime.js")
	for _, want := range []string{`new URL("sidebar.json", runtimeScript.src)`, `cache: "no-cache"`, `data-sidebar-ready`, `aria-current`, `Sidebar initialization failed.`} {
		assertContains(t, runtimeJS, want)
	}

	disabled := renderTemplate(t, tmpl, model.PageData{
		Kind:        model.PageNote,
		SiteRootRel: "../",
		Site:        model.SiteConfig{Title: "Field Notes", BaseURL: "https://example.com/blog/", Language: "en", RuntimeJSURL: "assets/obsite/runtime.test.js"},
		Title:       "Guide",
		Content:     template.HTML("<p>Body.</p>"),
	})

	assertNotContains(t, disabled, `data-obsite-sidebar`)
	assertNotContains(t, disabled, `data-sidebar-toggle`)
	assertNotContains(t, disabled, `data-sidebar-shell`)
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

func TestDefaultStylesDefinePublicLightDarkAndSystemVariables(t *testing.T) {
	t.Parallel()

	css := readTemplateAsset(t, "style.css")
	for _, want := range []string{
		"@layer obsite",
		":root[data-theme=\"light\"]",
		":root[data-theme=\"dark\"]",
		"@media (prefers-color-scheme: dark)",
		"--theme-toggle-bg",
		"--page-background: var(--obsite-background)",
		"--paper: var(--obsite-surface)",
		"--page-surface: var(--obsite-surface)",
		"--ink: var(--obsite-text)",
		"--muted: var(--obsite-muted)",
		"--accent: var(--obsite-accent)",
		"--line: var(--obsite-border)",
		".sr-only",
	} {
		if !strings.Contains(css, want) {
			t.Fatalf("style.css missing %q", want)
		}
	}
	for _, variable := range []string{
		"--obsite-background",
		"--obsite-surface",
		"--obsite-text",
		"--obsite-muted",
		"--obsite-accent",
		"--obsite-border",
		"--obsite-font-body",
		"--obsite-font-display",
		"--obsite-font-mono",
		"--obsite-content-width",
		"--obsite-sidebar-width",
		"--obsite-radius",
		"--obsite-shadow",
	} {
		if !strings.Contains(css, variable+":") {
			t.Fatalf("style.css missing public variable %q", variable)
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
		"pageAssetURL":  pageAssetURL,
		"siteBasePath":  siteBasePath,
		"slotData":      projectSlotData,
		"themeAssetURL": themeAssetURL,
	}).ParseFiles(
		filepath.Join(root, "internal", "render", "site", "base.html"),
		filepath.Join(root, "internal", "render", "site", "note.html"),
		filepath.Join(root, "internal", "render", "site", "index.html"),
		filepath.Join(root, "internal", "render", "site", "tag.html"),
		filepath.Join(root, "internal", "render", "site", "folder.html"),
		filepath.Join(root, "internal", "render", "site", "timeline.html"),
		filepath.Join(root, "internal", "render", "site", "404.html"),
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
	data, err := os.ReadFile(filepath.Join(root, "internal", "render", "site", name))
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
