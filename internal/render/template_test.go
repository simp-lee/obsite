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
				Breadcrumbs: []model.Breadcrumb{{Name: "Home", URL: "../../"}, {Name: "Tags", URL: "../"}, {Name: "systems"}},
			},
			want: []string{
				"<link rel=\"stylesheet\" href=\"../../style.css\">",
				"<h2 id=\"child-tags-heading\">Child tags</h2>",
				"<a class=\"tag-pill\" href=\"distributed/\">#systems/distributed</a>",
				"<h2 id=\"tag-notes-heading\">Notes</h2>",
				"<a href=\"../../composable-systems/\">Composable Systems</a>",
			},
		},
		{
			name: "404 page",
			data: model.PageData{
				Kind:        model.Page404,
				SiteRootRel: "./",
				Site: model.SiteConfig{
					Title:       "Field Notes",
					Description: "An editorial notebook.",
					Language:    "en",
				},
				Title:       "Not found",
				Description: "The requested page could not be found.",
				RecentNotes: []model.NoteSummary{{Title: "Composable Systems", URL: "composable-systems/"}},
			},
			want: []string{
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

func TestDefaultTemplatesUseModuleSafeMermaidLoader(t *testing.T) {
	t.Parallel()

	tmpl := parseDefaultTemplateSet(t)
	site, err := config.Load("", config.Overrides{
		Title:   "Field Notes",
		BaseURL: "https://example.com/",
	})
	if err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}
	if !strings.HasSuffix(site.MermaidJSURL, "/dist/mermaid.esm.min.mjs") {
		t.Fatalf("default MermaidJSURL = %q, want ESM .mjs asset contract", site.MermaidJSURL)
	}
	escapedMermaidURL := strings.ReplaceAll(site.MermaidJSURL, "/", `\/`)

	got := renderTemplate(t, tmpl, model.PageData{
		Kind:        model.PageNote,
		SiteRootRel: "../",
		Site:        site,
		Title:       "Mermaid Note",
		Content:     template.HTML("<pre class=\"mermaid\">graph TD;A-->B</pre>"),
		HasMermaid:  true,
	})

	assertContains(t, got, "<script type=\"module\">")
	assertContains(t, got, "import mermaid from \""+escapedMermaidURL+"\";")
	assertContains(t, got, "mermaid.initialize({")
	assertContains(t, got, "startOnLoad: true,")
	assertContains(t, got, "theme: \"neutral\"")
	assertContains(t, got, "securityLevel: \"loose\"")
	assertNotContains(t, got, "window.mermaid")
	assertNotContains(t, got, "<script defer src=\""+site.MermaidJSURL+"\"></script>")
}

func TestDefaultStylesProvideMobileTableFallback(t *testing.T) {
	t.Parallel()

	css := readTemplateAsset(t, "style.css")
	pattern := regexp.MustCompile(`(?s)@media\s*\(max-width:\s*56rem\)\s*\{.*?\.entry-content table\s*\{.*?display:\s*block;.*?width:\s*max-content;.*?min-width:\s*100%;.*?overflow-x:\s*auto;.*?-webkit-overflow-scrolling:\s*touch;`)
	if !pattern.MatchString(css) {
		t.Fatalf("style.css missing mobile table overflow fallback for narrow screens")
	}
}

func parseDefaultTemplateSet(t *testing.T) *template.Template {
	t.Helper()

	root := repoRoot(t)
	tmpl, err := template.ParseFiles(
		filepath.Join(root, "templates", "base.html"),
		filepath.Join(root, "templates", "note.html"),
		filepath.Join(root, "templates", "index.html"),
		filepath.Join(root, "templates", "tag.html"),
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
