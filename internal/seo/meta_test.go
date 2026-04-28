package seo

import (
	"testing"
	"time"

	"github.com/simp-lee/obsite/internal/model"
)

func TestBuildTitleFallbacks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		page model.PageData
		note *model.Note
		want string
	}{
		{
			name: "explicit page title wins",
			page: model.PageData{
				Kind:  model.PageNote,
				Title: "Explicit Title",
				Site:  model.SiteConfig{BaseURL: "https://example.com/", Title: "Site Title"},
			},
			note: &model.Note{
				RelPath: "notes/fallback.md",
				Frontmatter: model.Frontmatter{
					Title: "Frontmatter Title",
				},
			},
			want: "Explicit Title",
		},
		{
			name: "frontmatter title is second fallback",
			page: model.PageData{
				Kind: model.PageNote,
				Site: model.SiteConfig{BaseURL: "https://example.com/", Title: "Site Title"},
			},
			note: &model.Note{
				RelPath: "notes/fallback.md",
				Frontmatter: model.Frontmatter{
					Title: "Frontmatter Title",
				},
			},
			want: "Frontmatter Title",
		},
		{
			name: "filename is final note fallback",
			page: model.PageData{
				Kind: model.PageNote,
				Site: model.SiteConfig{BaseURL: "https://example.com/", Title: "Site Title"},
			},
			note: &model.Note{RelPath: "notes/fallback name.md"},
			want: "fallback name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := Build(tt.page, tt.note)
			if got.Title != tt.want {
				t.Fatalf("Build(...).Title = %q, want %q", got.Title, tt.want)
			}
		})
	}
}

func TestBuildDescriptionFallsBackToNoteSummary(t *testing.T) {
	t.Parallel()

	got := Build(model.PageData{
		Kind: model.PageNote,
		Site: model.SiteConfig{
			BaseURL:     "https://example.com/",
			Description: "Site description",
		},
	}, &model.Note{
		RelPath: "notes/guide.md",
		Summary: "Summary from note body.",
	})

	if got.Description != "Summary from note body." {
		t.Fatalf("Build(...).Description = %q, want %q", got.Description, "Summary from note body.")
	}
}

func TestBuildNoteWithoutDescriptionOrSummaryFallsBackToTitle(t *testing.T) {
	t.Parallel()

	got := Build(model.PageData{
		Kind: model.PageNote,
		Site: model.SiteConfig{
			BaseURL:     "https://example.com/",
			Description: "Site description",
		},
		Slug: "notes/sparse",
	}, &model.Note{
		RelPath: "notes/sparse.md",
		Frontmatter: model.Frontmatter{
			Title: "Sparse",
		},
	})

	if got.Description != "Sparse" {
		t.Fatalf("Build(...).Description = %q, want %q", got.Description, "Sparse")
	}
	if got.OG.Description != "Sparse" {
		t.Fatalf("Build(...).OG.Description = %q, want %q", got.OG.Description, "Sparse")
	}
}

func TestBuildCanonicalUsesBaseURLPathPrefix(t *testing.T) {
	t.Parallel()

	got := Build(model.PageData{
		Kind: model.PageTag,
		Site: model.SiteConfig{BaseURL: "https://example.com/blog/"},
		Slug: "/tags/parent/child/",
	}, nil)

	if got.Canonical != "https://example.com/blog/tags/parent/child/" {
		t.Fatalf("Build(...).Canonical = %q, want %q", got.Canonical, "https://example.com/blog/tags/parent/child/")
	}
	if got.OG.URL != got.Canonical {
		t.Fatalf("Build(...).OG.URL = %q, want canonical %q", got.OG.URL, got.Canonical)
	}
}

func TestBuildOpenGraphURLFollowsCanonicalEvenWhenPreset(t *testing.T) {
	t.Parallel()

	got := Build(model.PageData{
		Kind: model.PageNote,
		Site: model.SiteConfig{BaseURL: "https://example.com/blog/"},
		Slug: "notes/guide",
		OG: model.OpenGraph{
			URL: "https://preview.example.net/custom",
		},
	}, nil)

	if got.Canonical != "https://example.com/blog/notes/guide/" {
		t.Fatalf("Build(...).Canonical = %q, want %q", got.Canonical, "https://example.com/blog/notes/guide/")
	}
	if got.OG.URL != got.Canonical {
		t.Fatalf("Build(...).OG.URL = %q, want canonical %q", got.OG.URL, got.Canonical)
	}
}

func TestBuildPreservesNonIndexHTMLRelPath(t *testing.T) {
	t.Parallel()

	got := Build(model.PageData{
		Kind:    model.Page404,
		Site:    model.SiteConfig{BaseURL: "https://example.com/blog/"},
		RelPath: "404.html",
	}, nil)

	if got.Canonical != "https://example.com/blog/404.html" {
		t.Fatalf("Build(...).Canonical = %q, want %q", got.Canonical, "https://example.com/blog/404.html")
	}
	if got.OG.URL != got.Canonical {
		t.Fatalf("Build(...).OG.URL = %q, want canonical %q", got.OG.URL, got.Canonical)
	}
}

func TestBuildIndexWithoutSlugUsesSiteRootCanonical(t *testing.T) {
	t.Parallel()

	got := Build(model.PageData{
		Kind: model.PageIndex,
		Site: model.SiteConfig{BaseURL: "https://example.com/blog/"},
	}, nil)

	if got.Canonical != "https://example.com/blog/" {
		t.Fatalf("Build(...).Canonical = %q, want %q", got.Canonical, "https://example.com/blog/")
	}
	if got.OG.URL != got.Canonical {
		t.Fatalf("Build(...).OG.URL = %q, want canonical %q", got.OG.URL, got.Canonical)
	}
}

func TestBuildNonIndexRootRelPathUsesSiteRootCanonical(t *testing.T) {
	t.Parallel()

	got := Build(model.PageData{
		Kind:    model.PageTimeline,
		Site:    model.SiteConfig{BaseURL: "https://example.com/blog/"},
		RelPath: "index.html",
	}, nil)

	if got.Canonical != "https://example.com/blog/" {
		t.Fatalf("Build(...).Canonical = %q, want %q", got.Canonical, "https://example.com/blog/")
	}
	if got.OG.URL != got.Canonical {
		t.Fatalf("Build(...).OG.URL = %q, want canonical %q", got.OG.URL, got.Canonical)
	}
}

func TestBuildNoteWithoutSlugDoesNotFallbackToMarkdownPath(t *testing.T) {
	t.Parallel()

	got := Build(model.PageData{
		Kind: model.PageNote,
		Site: model.SiteConfig{BaseURL: "https://example.com/blog/"},
	}, &model.Note{RelPath: "notes/post.md"})

	if got.Canonical != "" {
		t.Fatalf("Build(...).Canonical = %q, want empty string", got.Canonical)
	}
	if got.OG.URL != "" {
		t.Fatalf("Build(...).OG.URL = %q, want empty string", got.OG.URL)
	}
}

func TestBuildNonNoteWithoutSlugOrRelPathFailsClosed(t *testing.T) {
	t.Parallel()

	got := Build(model.PageData{
		Kind: model.PageTag,
		Site: model.SiteConfig{BaseURL: "https://example.com/blog/"},
	}, nil)

	if got.Canonical != "" {
		t.Fatalf("Build(...).Canonical = %q, want empty string", got.Canonical)
	}
	if got.OG.URL != "" {
		t.Fatalf("Build(...).OG.URL = %q, want empty string", got.OG.URL)
	}
}

func TestBuildOpenGraphImageFallsBackToSiteDefaultImage(t *testing.T) {
	t.Parallel()

	got := Build(model.PageData{
		Kind: model.PageIndex,
		Site: model.SiteConfig{
			BaseURL:    "https://example.com/blog/",
			DefaultImg: "images/og-default.png",
		},
	}, nil)

	if got.OG.Image != "https://example.com/blog/images/og-default.png" {
		t.Fatalf("Build(...).OG.Image = %q, want %q", got.OG.Image, "https://example.com/blog/images/og-default.png")
	}
	if got.TwitterCard != "summary_large_image" {
		t.Fatalf("Build(...).TwitterCard = %q, want %q", got.TwitterCard, "summary_large_image")
	}
}

func TestBuildOpenGraphWithoutImageUsesSummaryTwitterCard(t *testing.T) {
	t.Parallel()

	got := Build(model.PageData{
		Kind: model.PageNote,
		Site: model.SiteConfig{BaseURL: "https://example.com/blog/"},
		Slug: "notes/plain",
	}, &model.Note{
		RelPath: "notes/plain.md",
		Frontmatter: model.Frontmatter{
			Title: "Plain",
		},
	})

	if got.OG.Image != "" {
		t.Fatalf("Build(...).OG.Image = %q, want empty string", got.OG.Image)
	}
	if got.TwitterCard != "summary" {
		t.Fatalf("Build(...).TwitterCard = %q, want %q", got.TwitterCard, "summary")
	}
}

func TestBuildOpenGraphTypeDistinguishesArticleAndWebsite(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		page model.PageData
		note *model.Note
		want string
	}{
		{
			name: "note pages are articles",
			page: model.PageData{
				Kind: model.PageNote,
				Site: model.SiteConfig{BaseURL: "https://example.com/"},
			},
			note: &model.Note{RelPath: "notes/post.md"},
			want: "article",
		},
		{
			name: "non-note pages are websites",
			page: model.PageData{
				Kind: model.PageIndex,
				Site: model.SiteConfig{BaseURL: "https://example.com/"},
			},
			want: "website",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := Build(tt.page, tt.note)
			if got.OG.Type != tt.want {
				t.Fatalf("Build(...).OG.Type = %q, want %q", got.OG.Type, tt.want)
			}
		})
	}
}

func TestApplyCopiesSEOFieldsIntoPageData(t *testing.T) {
	t.Parallel()

	page := model.PageData{
		Kind: model.PageNote,
		Site: model.SiteConfig{
			BaseURL:    "https://example.com/blog/",
			Author:     "Alice Example",
			DefaultImg: "images/og-default.png",
		},
		Slug: "notes/guide",
	}

	publishedAt := time.Date(2026, 4, 6, 10, 30, 0, 0, time.UTC)
	meta, err := Apply(&page, &model.Note{
		RelPath: "notes/guide.md",
		Summary: "Summary from note body.",
		Frontmatter: model.Frontmatter{
			Date: publishedAt,
		},
	})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	if page.Title != "guide" {
		t.Fatalf("page.Title = %q, want %q", page.Title, "guide")
	}
	if page.Description != "Summary from note body." {
		t.Fatalf("page.Description = %q, want %q", page.Description, "Summary from note body.")
	}
	if page.Canonical != "https://example.com/blog/notes/guide/" {
		t.Fatalf("page.Canonical = %q, want %q", page.Canonical, "https://example.com/blog/notes/guide/")
	}
	if page.OG.Image != "https://example.com/blog/images/og-default.png" {
		t.Fatalf("page.OG.Image = %q, want %q", page.OG.Image, "https://example.com/blog/images/og-default.png")
	}
	if page.TwitterCard != meta.TwitterCard {
		t.Fatalf("page.TwitterCard = %q, want metadata value %q", page.TwitterCard, meta.TwitterCard)
	}
}

func TestApplyKeepsPageMetadataAndBreadcrumbJSONLDWhenArticleJSONLDIsIncomplete(t *testing.T) {
	t.Parallel()

	page := model.PageData{
		Kind: model.PageNote,
		Site: model.SiteConfig{
			BaseURL: "https://example.com/blog/",
		},
		Slug: "guide",
		Breadcrumbs: []model.Breadcrumb{
			{Name: "Home", URL: "../"},
			{Name: "notes", URL: "../notes/"},
			{Name: "guides", URL: "../notes/guides/"},
		},
	}

	meta, err := Apply(&page, &model.Note{
		RelPath:      "notes/guide.md",
		LastModified: time.Date(2026, 4, 6, 10, 30, 0, 0, time.UTC),
		Summary:      "Summary from note body.",
		Frontmatter: model.Frontmatter{
			Title:       "Guide",
			Description: "Guide description",
		},
	})
	assertArticleJSONLDError(t, err, "author")

	if meta.Title != "Guide" {
		t.Fatalf("meta.Title = %q, want %q", meta.Title, "Guide")
	}
	if meta.Canonical != "https://example.com/blog/guide/" {
		t.Fatalf("meta.Canonical = %q, want %q", meta.Canonical, "https://example.com/blog/guide/")
	}
	if page.Title != meta.Title {
		t.Fatalf("page.Title = %q, want metadata value %q", page.Title, meta.Title)
	}
	if page.Description != meta.Description {
		t.Fatalf("page.Description = %q, want metadata value %q", page.Description, meta.Description)
	}
	if page.Canonical != meta.Canonical {
		t.Fatalf("page.Canonical = %q, want metadata value %q", page.Canonical, meta.Canonical)
	}
	if page.OG.URL != meta.Canonical {
		t.Fatalf("page.OG.URL = %q, want canonical %q", page.OG.URL, meta.Canonical)
	}
	if page.OG.Type != "article" {
		t.Fatalf("page.OG.Type = %q, want %q", page.OG.Type, "article")
	}
	if page.TwitterCard != meta.TwitterCard {
		t.Fatalf("page.TwitterCard = %q, want metadata value %q", page.TwitterCard, meta.TwitterCard)
	}
	if page.JSONLD == "" {
		t.Fatal("page.JSONLD = empty, want preserved breadcrumb JSON-LD")
	}

	payload := decodeJSONLD(t, page.JSONLD)
	if len(payload) != 1 {
		t.Fatalf("len(page.JSONLD) = %d, want %d", len(payload), 1)
	}
	assertStructuredDataMissing(t, payload, "Article")
	breadcrumb := findStructuredData(t, payload, "BreadcrumbList")
	items := breadcrumbItems(t, breadcrumb)
	if len(items) != 4 {
		t.Fatalf("len(breadcrumb.itemListElement) = %d, want %d", len(items), 4)
	}
	assertBreadcrumbItem(t, items[0], 1, "Home", "https://example.com/blog/")
	assertBreadcrumbItem(t, items[1], 2, "notes", "https://example.com/blog/notes/")
	assertBreadcrumbItem(t, items[2], 3, "guides", "https://example.com/blog/notes/guides/")
	assertBreadcrumbItem(t, items[3], 4, "Guide", "https://example.com/blog/guide/")
}
