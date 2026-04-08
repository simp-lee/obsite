package model

import (
	"testing"
	"time"
)

func TestLinkRefRetainsRawAndResolvedTargets(t *testing.T) {
	ref := LinkRef{
		RawTarget:       "garden/guide",
		ResolvedRelPath: "notes/guide.md",
		Display:         "Guide",
		Fragment:        "overview",
		Line:            12,
	}

	if ref.RawTarget != "garden/guide" {
		t.Fatalf("RawTarget = %q, want %q", ref.RawTarget, "garden/guide")
	}

	if ref.ResolvedRelPath != "notes/guide.md" {
		t.Fatalf("ResolvedRelPath = %q, want %q", ref.ResolvedRelPath, "notes/guide.md")
	}

	if ref.Fragment != "overview" {
		t.Fatalf("Fragment = %q, want %q", ref.Fragment, "overview")
	}

	if ref.Line != 12 {
		t.Fatalf("Line = %d, want %d", ref.Line, 12)
	}

	if ref.Display != "Guide" {
		t.Fatalf("Display = %q, want %q", ref.Display, "Guide")
	}
}

func TestSiteConfigDefaultPublishStoresExplicitPolicy(t *testing.T) {
	cfg := SiteConfig{DefaultPublish: true}
	if !cfg.DefaultPublish {
		t.Fatal("explicit DefaultPublish=true should be preserved")
	}

	cfg.DefaultPublish = false
	if cfg.DefaultPublish {
		t.Fatal("explicit DefaultPublish=false should be preserved")
	}
}

func TestVaultIndexUnpublishedLookupSupportsResolverKeys(t *testing.T) {
	note := &Note{RelPath: "notes/guide.md"}

	idx := VaultIndex{
		Unpublished: UnpublishedLookup{
			Notes: map[string]*Note{
				note.RelPath: note,
			},
			NoteByName: map[string][]*Note{
				"guide": {note},
			},
			AliasByName: map[string][]*Note{
				"docs": {note},
			},
		},
	}

	if got := idx.Unpublished.Notes[note.RelPath]; got != note {
		t.Fatalf("unpublished path lookup = %p, want %p", got, note)
	}

	if got := idx.Unpublished.NoteByName["guide"]; len(got) != 1 || got[0] != note {
		t.Fatalf("unpublished name lookup = %#v, want [%p]", got, note)
	}

	if got := idx.Unpublished.AliasByName["docs"]; len(got) != 1 || got[0] != note {
		t.Fatalf("unpublished alias lookup = %#v, want [%p]", got, note)
	}
}

func TestPageDataSupportsExtendedFeatureContracts(t *testing.T) {
	page := PageData{
		Kind:        PageTimeline,
		TOC:         []TOCEntry{{Text: "Overview", ID: "overview", Children: []TOCEntry{{Text: "Details", ID: "details"}}}},
		ReadingTime: "4 min read",
		WordCount:   880,
		RelatedArticles: []RelatedArticle{{
			Title:   "Systems Thinking",
			URL:     "../systems-thinking/",
			Summary: "A related note.",
			Score:   1.25,
			Tags:    []TagLink{{Name: "go", Slug: "go", URL: "../tags/go/"}},
		}},
		Pagination: &PaginationData{
			CurrentPage: 2,
			TotalPages:  3,
			PrevURL:     "../",
			NextURL:     "../page/3/",
			Pages:       []PageLink{{Number: 1, URL: "../"}, {Number: 2, URL: "./"}, {Number: 3, URL: "../page/3/"}},
		},
		SidebarTree: []SidebarNode{{
			Name:  "notes",
			IsDir: true,
			Children: []SidebarNode{{
				Name:     "guide",
				URL:      "../guide/",
				IsActive: true,
			}},
		}},
		HasSearch:    true,
		HasCustomCSS: true,
		HasRSS:       true,
		FolderPath:   "notes/backend",
		FolderChildren: []NoteSummary{{
			Title:   "Guide",
			Summary: "Folder summary",
			URL:     "../guide/",
		}},
		TimelineNotes: []NoteSummary{{
			Title:   "Launch",
			Summary: "Timeline summary",
			URL:     "../launch/",
		}},
	}

	if PageFolder != "folder" {
		t.Fatalf("PageFolder = %q, want %q", PageFolder, "folder")
	}
	if page.Kind != PageTimeline {
		t.Fatalf("Kind = %q, want %q", page.Kind, PageTimeline)
	}
	if len(page.TOC) != 1 || len(page.TOC[0].Children) != 1 {
		t.Fatalf("TOC = %#v, want nested entries", page.TOC)
	}
	if page.WordCount != 880 {
		t.Fatalf("WordCount = %d, want %d", page.WordCount, 880)
	}
	if page.Pagination == nil || page.Pagination.Pages[1].Number != 2 {
		t.Fatalf("Pagination = %#v, want numbered page links", page.Pagination)
	}
	if len(page.SidebarTree) != 1 || !page.SidebarTree[0].IsDir || !page.SidebarTree[0].Children[0].IsActive {
		t.Fatalf("SidebarTree = %#v, want active nested directory tree", page.SidebarTree)
	}
	if got := page.RelatedArticles[0].Tags[0].Name; got != "go" {
		t.Fatalf("RelatedArticles[0].Tags[0].Name = %q, want %q", got, "go")
	}
	if got := page.FolderChildren[0].Summary; got != "Folder summary" {
		t.Fatalf("FolderChildren[0].Summary = %q, want %q", got, "Folder summary")
	}
	if got := page.TimelineNotes[0].Summary; got != "Timeline summary" {
		t.Fatalf("TimelineNotes[0].Summary = %q, want %q", got, "Timeline summary")
	}
	if !page.HasSearch || !page.HasCustomCSS || !page.HasRSS {
		t.Fatalf("feature flags = (%t, %t, %t), want all true", page.HasSearch, page.HasCustomCSS, page.HasRSS)
	}
}

func TestSiteConfigAndFrontmatterSupportExtendedFeatureFields(t *testing.T) {
	updated := time.Date(2026, 4, 7, 15, 4, 0, 0, time.UTC)
	cfg := SiteConfig{
		TemplateDir: "templates/custom",
		CustomCSS:   "assets/custom.css",
		Search: SearchConfig{
			Enabled:         true,
			PagefindPath:    "pagefind_extended",
			PagefindVersion: "1.4.0",
		},
		Pagination: PaginationConfig{PageSize: 30},
		Sidebar:    SidebarConfig{Enabled: true},
		Popover:    PopoverConfig{Enabled: true},
		Related:    RelatedConfig{Enabled: true, Count: 6},
		RSS:        RSSConfig{Enabled: true},
		Timeline: TimelineConfig{
			Enabled:    true,
			AsHomepage: true,
			Path:       "notes",
		},
	}
	frontmatter := Frontmatter{Updated: updated}
	summary := NoteSummary{Summary: "Used by RSS and list pages."}

	if cfg.TemplateDir != "templates/custom" {
		t.Fatalf("TemplateDir = %q, want %q", cfg.TemplateDir, "templates/custom")
	}
	if !cfg.Search.Enabled || cfg.Search.PagefindPath != "pagefind_extended" {
		t.Fatalf("Search = %#v, want enabled pagefind config", cfg.Search)
	}
	if cfg.Pagination.PageSize != 30 {
		t.Fatalf("Pagination.PageSize = %d, want %d", cfg.Pagination.PageSize, 30)
	}
	if !cfg.Related.Enabled || cfg.Related.Count != 6 {
		t.Fatalf("Related = %#v, want enabled count=6", cfg.Related)
	}
	if !cfg.Timeline.Enabled || !cfg.Timeline.AsHomepage || cfg.Timeline.Path != "notes" {
		t.Fatalf("Timeline = %#v, want enabled homepage path", cfg.Timeline)
	}
	if !frontmatter.Updated.Equal(updated) {
		t.Fatalf("Frontmatter.Updated = %v, want %v", frontmatter.Updated, updated)
	}
	if summary.Summary != "Used by RSS and list pages." {
		t.Fatalf("NoteSummary.Summary = %q, want preserved value", summary.Summary)
	}
}
