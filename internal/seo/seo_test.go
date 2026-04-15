package seo

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"html/template"
	"strings"
	"testing"
	"time"

	"github.com/simp-lee/obsite/internal/model"
)

func buildJSONLDForTest(page model.PageData, note *model.Note) (template.JS, error) {
	return buildJSONLD(page, note, Build(page, note))
}

func TestBuildSitemapSortsByRecencyThenSlugOrPathAndRequiresLastMod(t *testing.T) {
	t.Parallel()

	noteDate := time.Date(2026, 4, 7, 0, 0, 0, 0, time.UTC)
	sharedLastMod := time.Date(2026, 4, 6, 9, 0, 0, 0, time.UTC)
	pages := []model.PageData{
		{
			Canonical:    "https://example.com/blog/tags/go/advanced/",
			Slug:         "tags/go/advanced",
			RelPath:      "tags/go/advanced/index.html",
			LastModified: sharedLastMod,
		},
		{
			Canonical:    "https://example.com/blog/beta/",
			Slug:         "beta",
			RelPath:      "beta/index.html",
			Date:         noteDate,
			LastModified: sharedLastMod,
		},
		{
			Canonical:    "https://example.com/blog/",
			RelPath:      "index.html",
			LastModified: sharedLastMod,
		},
		{
			Canonical:    "https://example.com/blog/alpha/",
			Slug:         "alpha",
			RelPath:      "alpha/index.html",
			Date:         noteDate,
			LastModified: sharedLastMod,
		},
		{
			Canonical:    "https://example.com/blog/tags/go/",
			Slug:         "tags/go",
			RelPath:      "tags/go/index.html",
			LastModified: sharedLastMod,
		},
		{
			Canonical:    "https://example.com/blog/misc/",
			LastModified: sharedLastMod,
		},
	}

	got, err := BuildSitemap(pages)
	if err != nil {
		t.Fatalf("BuildSitemap() error = %v", err)
	}

	var parsed struct {
		URLs []struct {
			Loc     string `xml:"loc"`
			LastMod string `xml:"lastmod"`
		} `xml:"url"`
	}
	if err := xml.Unmarshal(got, &parsed); err != nil {
		t.Fatalf("xml.Unmarshal() error = %v", err)
	}

	if len(parsed.URLs) != 6 {
		t.Fatalf("len(urls) = %d, want %d", len(parsed.URLs), 6)
	}

	wantLocs := []string{
		"https://example.com/blog/alpha/",
		"https://example.com/blog/beta/",
		"https://example.com/blog/",
		"https://example.com/blog/tags/go/",
		"https://example.com/blog/tags/go/advanced/",
		"https://example.com/blog/misc/",
	}
	for index, wantLoc := range wantLocs {
		if parsed.URLs[index].Loc != wantLoc {
			t.Fatalf("urls[%d].loc = %q, want %q", index, parsed.URLs[index].Loc, wantLoc)
		}
	}
	if parsed.URLs[0].LastMod != sharedLastMod.Format(time.RFC3339) {
		t.Fatalf("urls[0].lastmod = %q, want %q", parsed.URLs[0].LastMod, sharedLastMod.Format(time.RFC3339))
	}
	if parsed.URLs[len(parsed.URLs)-1].LastMod != sharedLastMod.Format(time.RFC3339) {
		t.Fatalf(
			"urls[%d].lastmod = %q, want %q",
			len(parsed.URLs)-1,
			parsed.URLs[len(parsed.URLs)-1].LastMod,
			sharedLastMod.Format(time.RFC3339),
		)
	}
}

func TestBuildSitemapFailsWhenLastModMissing(t *testing.T) {
	t.Parallel()

	_, err := BuildSitemap([]model.PageData{{Canonical: "https://example.com/blog/notes/guide/"}})
	if err == nil {
		t.Fatal("BuildSitemap() error = nil, want missing lastmod error")
	}
	if !strings.Contains(err.Error(), "missing lastmod") {
		t.Fatalf("BuildSitemap() error = %q, want message containing %q", err.Error(), "missing lastmod")
	}
}

func TestBuildSitemapFailsWhenURLMissing(t *testing.T) {
	t.Parallel()

	_, err := BuildSitemap([]model.PageData{{
		Canonical:    "   ",
		LastModified: time.Date(2026, 4, 6, 10, 30, 0, 0, time.UTC),
	}})
	if err == nil {
		t.Fatal("BuildSitemap() error = nil, want missing URL error")
	}
	if !strings.Contains(err.Error(), "missing canonical URL") {
		t.Fatalf("BuildSitemap() error = %q, want message containing %q", err.Error(), "missing canonical URL")
	}
}

func TestBuildRobotsUsesAbsoluteSitemapURL(t *testing.T) {
	t.Parallel()

	got := BuildRobots("https://example.com/blog")
	want := "User-agent: *\nAllow: /\nSitemap: https://example.com/blog/sitemap.xml\n"
	if got != want {
		t.Fatalf("BuildRobots() = %q, want %q", got, want)
	}
}

func TestBuildJSONLDIncludesArticleAndBreadcrumbListForNotePages(t *testing.T) {
	t.Parallel()

	publishedAt := time.Date(2026, 4, 6, 10, 30, 0, 0, time.UTC)
	page := model.PageData{
		Kind: model.PageNote,
		Site: model.SiteConfig{
			BaseURL: "https://example.com/blog/",
			Author:  "Alice Example",
		},
		Slug: "notes/guide",
		Breadcrumbs: []model.Breadcrumb{
			{Name: "Home", URL: "/"},
			{Name: "Notes", URL: "notes/"},
		},
	}
	note := &model.Note{
		RelPath: "notes/guide.md",
		Frontmatter: model.Frontmatter{
			Title:       "Guide",
			Description: "Guide description",
			Date:        publishedAt,
		},
		Summary: "Summary fallback",
	}

	jsonld, err := buildJSONLDForTest(page, note)
	if err != nil {
		t.Fatalf("BuildJSONLD() error = %v", err)
	}

	payload := decodeJSONLD(t, jsonld)
	if len(payload) != 2 {
		t.Fatalf("len(BuildJSONLD()) = %d, want %d", len(payload), 2)
	}

	article := findStructuredData(t, payload, "Article")
	if got := article["headline"]; got != "Guide" {
		t.Fatalf("article.headline = %#v, want %q", got, "Guide")
	}
	if got := article["description"]; got != "Guide description" {
		t.Fatalf("article.description = %#v, want %q", got, "Guide description")
	}
	if got := article["url"]; got != "https://example.com/blog/notes/guide/" {
		t.Fatalf("article.url = %#v, want %q", got, "https://example.com/blog/notes/guide/")
	}
	if got := article["datePublished"]; got != publishedAt.Format(time.RFC3339) {
		t.Fatalf("article.datePublished = %#v, want %q", got, publishedAt.Format(time.RFC3339))
	}
	author, ok := article["author"].(map[string]any)
	if !ok {
		t.Fatalf("article.author = %#v, want object", article["author"])
	}
	if got := author["@type"]; got != "Person" {
		t.Fatalf("article.author.@type = %#v, want %q", got, "Person")
	}
	if got := author["name"]; got != "Alice Example" {
		t.Fatalf("article.author.name = %#v, want %q", got, "Alice Example")
	}

	breadcrumb := findStructuredData(t, payload, "BreadcrumbList")
	items := breadcrumbItems(t, breadcrumb)
	if len(items) != 3 {
		t.Fatalf("len(breadcrumb.itemListElement) = %d, want %d", len(items), 3)
	}

	assertBreadcrumbItem(t, items[0], 1, "Home", "https://example.com/blog/")
	assertBreadcrumbItem(t, items[1], 2, "Notes", "https://example.com/blog/notes/")
	assertBreadcrumbItem(t, items[2], 3, "Guide", "https://example.com/blog/notes/guide/")
}

func TestBuildJSONLDFallsBackToOrganizationAuthorWhenSiteAuthorMissing(t *testing.T) {
	t.Parallel()

	publishedAt := time.Date(2026, 4, 6, 10, 30, 0, 0, time.UTC)
	page := model.PageData{
		Kind: model.PageNote,
		Site: model.SiteConfig{
			BaseURL: "https://example.com/blog/",
			Title:   "Example Blog",
		},
		Slug: "notes/guide",
	}
	note := &model.Note{
		RelPath: "notes/guide.md",
		Frontmatter: model.Frontmatter{
			Title:       "Guide",
			Description: "Guide description",
			Date:        publishedAt,
		},
	}

	jsonld, err := buildJSONLDForTest(page, note)
	if err != nil {
		t.Fatalf("BuildJSONLD() error = %v", err)
	}

	article := findStructuredData(t, decodeJSONLD(t, jsonld), "Article")
	author, ok := article["author"].(map[string]any)
	if !ok {
		t.Fatalf("article.author = %#v, want object", article["author"])
	}
	if got := author["@type"]; got != "Organization" {
		t.Fatalf("article.author.@type = %#v, want %q", got, "Organization")
	}
	if got := author["name"]; got != "Example Blog" {
		t.Fatalf("article.author.name = %#v, want %q", got, "Example Blog")
	}
}

func TestBuildJSONLDFallsBackToNoteLastModifiedWhenDateMissing(t *testing.T) {
	t.Parallel()

	publishedAt := time.Date(2026, 4, 6, 10, 30, 0, 0, time.UTC)
	page := model.PageData{
		Kind: model.PageNote,
		Site: model.SiteConfig{
			BaseURL: "https://example.com/blog/",
			Title:   "Example Blog",
		},
		Slug: "notes/guide",
		Breadcrumbs: []model.Breadcrumb{
			{Name: "Home", URL: "/"},
			{Name: "Notes", URL: "notes/"},
		},
	}
	note := &model.Note{
		RelPath:      "notes/guide.md",
		LastModified: publishedAt,
		Frontmatter: model.Frontmatter{
			Title:       "Guide",
			Description: "Guide description",
		},
	}

	jsonld, err := buildJSONLDForTest(page, note)
	if err != nil {
		t.Fatalf("BuildJSONLD() error = %v", err)
	}

	payload := decodeJSONLD(t, jsonld)
	if len(payload) != 2 {
		t.Fatalf("len(BuildJSONLD()) = %d, want %d", len(payload), 2)
	}
	article := findStructuredData(t, payload, "Article")
	if got := article["datePublished"]; got != publishedAt.Format(time.RFC3339) {
		t.Fatalf("article.datePublished = %#v, want %q", got, publishedAt.Format(time.RFC3339))
	}

	breadcrumb := findStructuredData(t, payload, "BreadcrumbList")
	items := breadcrumbItems(t, breadcrumb)
	if len(items) != 3 {
		t.Fatalf("len(breadcrumb.itemListElement) = %d, want %d", len(items), 3)
	}
	assertBreadcrumbItem(t, items[0], 1, "Home", "https://example.com/blog/")
	assertBreadcrumbItem(t, items[1], 2, "Notes", "https://example.com/blog/notes/")
	assertBreadcrumbItem(t, items[2], 3, "Guide", "https://example.com/blog/notes/guide/")
}

func TestBuildJSONLDReturnsBreadcrumbListWhenAuthorMissing(t *testing.T) {
	t.Parallel()

	page := model.PageData{
		Kind: model.PageNote,
		Site: model.SiteConfig{
			BaseURL: "https://example.com/blog/",
		},
		Slug: "notes/guide",
		Breadcrumbs: []model.Breadcrumb{
			{Name: "Home", URL: "/"},
			{Name: "Notes", URL: "notes/"},
		},
	}
	note := &model.Note{
		RelPath:      "notes/guide.md",
		LastModified: time.Date(2026, 4, 6, 10, 30, 0, 0, time.UTC),
		Frontmatter: model.Frontmatter{
			Title:       "Guide",
			Description: "Guide description",
		},
	}

	jsonld, err := buildJSONLDForTest(page, note)
	assertArticleJSONLDError(t, err, "author")

	payload := decodeJSONLD(t, jsonld)
	if len(payload) != 1 {
		t.Fatalf("len(BuildJSONLD()) = %d, want %d", len(payload), 1)
	}
	assertStructuredDataMissing(t, payload, "Article")

	breadcrumb := findStructuredData(t, payload, "BreadcrumbList")
	items := breadcrumbItems(t, breadcrumb)
	if len(items) != 3 {
		t.Fatalf("len(breadcrumb.itemListElement) = %d, want %d", len(items), 3)
	}
	assertBreadcrumbItem(t, items[0], 1, "Home", "https://example.com/blog/")
	assertBreadcrumbItem(t, items[1], 2, "Notes", "https://example.com/blog/notes/")
	assertBreadcrumbItem(t, items[2], 3, "Guide", "https://example.com/blog/notes/guide/")
}

func TestApplyCopiesPreSerializedJSONLDIntoPageData(t *testing.T) {
	t.Parallel()

	page := model.PageData{
		Kind: model.PageNote,
		Site: model.SiteConfig{
			BaseURL: "https://example.com/blog/",
			Author:  "Alice Example",
		},
		Slug:        "notes/guide",
		Breadcrumbs: []model.Breadcrumb{{Name: "Home", URL: "/"}},
	}
	publishedAt := time.Date(2026, 4, 6, 10, 30, 0, 0, time.UTC)
	note := &model.Note{
		RelPath: "notes/guide.md",
		Frontmatter: model.Frontmatter{
			Title:       "Guide",
			Description: "Guide description",
			Date:        publishedAt,
		},
	}

	want, err := buildJSONLDForTest(page, note)
	if err != nil {
		t.Fatalf("BuildJSONLD() error = %v", err)
	}

	if _, err := Apply(&page, note); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	if page.JSONLD == "" {
		t.Fatal("page.JSONLD = empty, want pre-serialized JSON-LD")
	}
	if page.JSONLD != want {
		t.Fatalf("page.JSONLD = %s, want %s", page.JSONLD, want)
	}
	if !json.Valid([]byte(page.JSONLD)) {
		t.Fatalf("page.JSONLD = %s, want valid JSON", page.JSONLD)
	}
}

func TestBuildJSONLDIncludesBreadcrumbListForNonNotePagesWithBreadcrumbs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		page model.PageData
		want []model.Breadcrumb
	}{
		{
			name: "tag page",
			page: model.PageData{
				Kind:  model.PageTag,
				Site:  model.SiteConfig{BaseURL: "https://example.com/blog/"},
				Title: "field",
				Slug:  "tags/field",
				Breadcrumbs: []model.Breadcrumb{
					{Name: "Home", URL: "../../"},
					{Name: "field"},
				},
			},
			want: []model.Breadcrumb{
				{Name: "Home", URL: "https://example.com/blog/"},
				{Name: "field", URL: "https://example.com/blog/tags/field/"},
			},
		},
		{
			name: "paginated folder page",
			page: model.PageData{
				Kind:       model.PageFolder,
				Site:       model.SiteConfig{BaseURL: "https://example.com/blog/"},
				Title:      "garden",
				RelPath:    "notes/garden/page/2/index.html",
				Pagination: &model.PaginationData{CurrentPage: 2},
				Breadcrumbs: []model.Breadcrumb{
					{Name: "Home", URL: "../../../../"},
					{Name: "notes", URL: "../../../"},
					{Name: "garden"},
				},
			},
			want: []model.Breadcrumb{
				{Name: "Home", URL: "https://example.com/blog/"},
				{Name: "notes", URL: "https://example.com/blog/notes/"},
				{Name: "garden", URL: "https://example.com/blog/notes/garden/page/2/"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jsonld, err := buildJSONLDForTest(tt.page, nil)
			if err != nil {
				t.Fatalf("BuildJSONLD() error = %v", err)
			}

			payload := decodeJSONLD(t, jsonld)
			if len(payload) != 1 {
				t.Fatalf("len(BuildJSONLD()) = %d, want %d", len(payload), 1)
			}
			assertStructuredDataMissing(t, payload, "Article")
			breadcrumb := findStructuredData(t, payload, "BreadcrumbList")
			items := breadcrumbItems(t, breadcrumb)
			if len(items) != len(tt.want) {
				t.Fatalf("len(breadcrumb.itemListElement) = %d, want %d", len(items), len(tt.want))
			}
			for index, want := range tt.want {
				assertBreadcrumbItem(t, items[index], index+1, want.Name, want.URL)
			}
		})
	}
}

func TestBuildJSONLDOmitsPagesWithoutStructuredData(t *testing.T) {
	t.Parallel()

	got, err := buildJSONLDForTest(model.PageData{
		Kind: model.PageIndex,
		Site: model.SiteConfig{BaseURL: "https://example.com/blog/"},
	}, nil)
	if err != nil {
		t.Fatalf("BuildJSONLD() error = %v", err)
	}

	if got != "" {
		t.Fatalf("BuildJSONLD() = %s, want empty string", got)
	}
}

func TestBuildJSONLDOmitsBreadcrumbListWhenRenderLayerDoesNotProvideBreadcrumbs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		page model.PageData
	}{
		{
			name: "index page",
			page: model.PageData{
				Kind:    model.PageIndex,
				Site:    model.SiteConfig{BaseURL: "https://example.com/blog/", Title: "Field Notes"},
				Title:   "Field Notes",
				RelPath: "index.html",
			},
		},
		{
			name: "404 page",
			page: model.PageData{
				Kind:    model.Page404,
				Site:    model.SiteConfig{BaseURL: "https://example.com/blog/"},
				Title:   "Not found",
				RelPath: "404.html",
			},
		},
		{
			name: "timeline homepage",
			page: model.PageData{
				Kind:    model.PageTimeline,
				Site:    model.SiteConfig{BaseURL: "https://example.com/blog/"},
				Title:   "Recent notes",
				RelPath: "index.html",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildJSONLDForTest(tt.page, nil)
			if err != nil {
				t.Fatalf("BuildJSONLD() error = %v", err)
			}
			if got != "" {
				t.Fatalf("BuildJSONLD() = %s, want empty string when breadcrumbs are not provided", got)
			}
		})
	}
}

func TestBuildJSONLDAvoidsDuplicateCanonicalBreadcrumb(t *testing.T) {
	t.Parallel()

	page := model.PageData{
		Kind: model.PageNote,
		Site: model.SiteConfig{
			BaseURL: "https://example.com/blog/",
			Author:  "Alice Example",
		},
		Slug: "notes/guide",
		Breadcrumbs: []model.Breadcrumb{
			{Name: "Home", URL: "/"},
			{Name: "Guide", URL: "/notes/guide/"},
		},
	}
	publishedAt := time.Date(2026, 4, 6, 10, 30, 0, 0, time.UTC)
	note := &model.Note{
		RelPath: "notes/guide.md",
		Frontmatter: model.Frontmatter{
			Title:       "Guide",
			Description: "Guide description",
			Date:        publishedAt,
		},
	}

	jsonld, err := buildJSONLDForTest(page, note)
	if err != nil {
		t.Fatalf("BuildJSONLD() error = %v", err)
	}

	breadcrumb := findStructuredData(t, decodeJSONLD(t, jsonld), "BreadcrumbList")
	items := breadcrumbItems(t, breadcrumb)
	if len(items) != 2 {
		t.Fatalf("len(breadcrumb.itemListElement) = %d, want %d", len(items), 2)
	}
	assertBreadcrumbItem(t, items[1], 2, "Guide", "https://example.com/blog/notes/guide/")
}

func TestBuildJSONLDNormalizesEmptyCurrentPageBreadcrumbURL(t *testing.T) {
	t.Parallel()

	page := model.PageData{
		Kind: model.PageNote,
		Site: model.SiteConfig{
			BaseURL: "https://example.com/blog/",
			Author:  "Alice Example",
		},
		Slug: "notes/guide",
		Breadcrumbs: []model.Breadcrumb{
			{Name: "Home", URL: "/"},
			{Name: "Notes", URL: "/notes/"},
			{Name: "Guide", URL: ""},
		},
	}
	publishedAt := time.Date(2026, 4, 6, 10, 30, 0, 0, time.UTC)
	note := &model.Note{
		RelPath: "notes/guide.md",
		Frontmatter: model.Frontmatter{
			Title:       "Guide",
			Description: "Guide description",
			Date:        publishedAt,
		},
	}

	jsonld, err := buildJSONLDForTest(page, note)
	if err != nil {
		t.Fatalf("BuildJSONLD() error = %v", err)
	}

	breadcrumb := findStructuredData(t, decodeJSONLD(t, jsonld), "BreadcrumbList")
	items := breadcrumbItems(t, breadcrumb)
	if len(items) != 3 {
		t.Fatalf("len(breadcrumb.itemListElement) = %d, want %d", len(items), 3)
	}
	assertBreadcrumbItem(t, items[0], 1, "Home", "https://example.com/blog/")
	assertBreadcrumbItem(t, items[1], 2, "Notes", "https://example.com/blog/notes/")
	assertBreadcrumbItem(t, items[2], 3, "Guide", "https://example.com/blog/notes/guide/")
}

func decodeJSONLD(t *testing.T, input template.JS) []map[string]any {
	t.Helper()

	var payload []map[string]any
	if err := json.Unmarshal([]byte(input), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	return payload
}

func findStructuredData(t *testing.T, payload []map[string]any, wantType string) map[string]any {
	t.Helper()

	for _, item := range payload {
		if item["@type"] == wantType {
			return item
		}
	}

	t.Fatalf("structured data missing type %q", wantType)
	return nil
}

func assertStructuredDataMissing(t *testing.T, payload []map[string]any, wantType string) {
	t.Helper()

	for _, item := range payload {
		if item["@type"] == wantType {
			t.Fatalf("structured data unexpectedly included type %q", wantType)
		}
	}
}

func assertArticleJSONLDError(t *testing.T, err error, wantMissingFields ...string) {
	t.Helper()

	if err == nil {
		t.Fatal("error = nil, want ArticleJSONLDError")
	}

	var articleErr *ArticleJSONLDError
	if !errors.As(err, &articleErr) {
		t.Fatalf("error = %T (%v), want *ArticleJSONLDError", err, err)
	}
	if len(articleErr.MissingFields) != len(wantMissingFields) {
		t.Fatalf("len(error.MissingFields) = %d, want %d (%v)", len(articleErr.MissingFields), len(wantMissingFields), wantMissingFields)
	}
	for i, wantField := range wantMissingFields {
		if articleErr.MissingFields[i] != wantField {
			t.Fatalf("error.MissingFields[%d] = %q, want %q", i, articleErr.MissingFields[i], wantField)
		}
	}
}

func breadcrumbItems(t *testing.T, payload map[string]any) []map[string]any {
	t.Helper()

	rawItems, ok := payload["itemListElement"].([]any)
	if !ok {
		t.Fatalf("breadcrumb.itemListElement = %#v, want array", payload["itemListElement"])
	}

	items := make([]map[string]any, 0, len(rawItems))
	for _, rawItem := range rawItems {
		item, ok := rawItem.(map[string]any)
		if !ok {
			t.Fatalf("breadcrumb item = %#v, want object", rawItem)
		}
		items = append(items, item)
	}

	return items
}

func assertBreadcrumbItem(t *testing.T, item map[string]any, wantPosition int, wantName string, wantURL string) {
	t.Helper()

	if got := item["position"]; got != float64(wantPosition) {
		t.Fatalf("breadcrumb.position = %#v, want %d", got, wantPosition)
	}
	if got := item["name"]; got != wantName {
		t.Fatalf("breadcrumb.name = %#v, want %q", got, wantName)
	}
	if got := item["item"]; got != wantURL {
		t.Fatalf("breadcrumb.item = %#v, want %q", got, wantURL)
	}
}
