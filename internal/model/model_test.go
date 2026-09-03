package model

import (
	"reflect"
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

func TestNoteFrontmatterPublishStoresSinglePublishPolicy(t *testing.T) {
	published := true
	hidden := false

	note := Note{Frontmatter: Frontmatter{Publish: &published}}
	if note.Frontmatter.Publish == nil || !*note.Frontmatter.Publish {
		t.Fatalf("Frontmatter.Publish = %v, want true", note.Frontmatter.Publish)
	}

	note = Note{Frontmatter: Frontmatter{Publish: &hidden}}
	if note.Frontmatter.Publish == nil || *note.Frontmatter.Publish {
		t.Fatalf("Frontmatter.Publish = %v, want false", note.Frontmatter.Publish)
	}

	note = Note{}
	if note.Frontmatter.Publish != nil {
		t.Fatalf("Frontmatter.Publish = %v, want nil", note.Frontmatter.Publish)
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

func TestVaultIndexResourceLookupInitializesLazilyFromExactPaths(t *testing.T) {
	idx := &VaultIndex{
		Resources: map[string]string{
			"boards/Cafe\u0301.canvas": "boards/Cafe\u0301.canvas",
			"archive/Café.canvas":      "archive/Café.canvas",
		},
	}

	if got := idx.ResolveResourcePath("boards/Cafe\u0301.canvas"); got != "boards/Cafe\u0301.canvas" {
		t.Fatalf("ResolveResourcePath(exact) = %q, want %q", got, "boards/Cafe\u0301.canvas")
	}

	lookup := idx.LookupResourceBaseName("CAFÉ.canvas")
	if lookup.Path != "" {
		t.Fatalf("LookupResourceBaseName().Path = %q, want empty for ambiguous basename fallback", lookup.Path)
	}
	want := []string{"archive/Café.canvas", "boards/Cafe\u0301.canvas"}
	if !reflect.DeepEqual(lookup.Ambiguous, want) {
		t.Fatalf("LookupResourceBaseName().Ambiguous = %#v, want %#v", lookup.Ambiguous, want)
	}
}

func TestSiteConfigAndFrontmatterSupportExtendedFeatureFields(t *testing.T) {
	updated := time.Date(2026, 4, 7, 15, 4, 0, 0, time.UTC)
	cfg := SiteConfig{
		CustomCSS:  "assets/custom.css",
		Pagination: PaginationConfig{PageSize: 30},
		Sidebar:    SidebarConfig{Enabled: true},
		Popover:    PopoverConfig{Enabled: true},
		Related:    RelatedConfig{Enabled: true, Count: 6},
		RSS:        RSSConfig{Enabled: true},
		Timeline: TimelineConfig{
			Enabled: true,
			Path:    "notes",
		},
	}
	frontmatter := Frontmatter{Updated: updated}

	if cfg.Pagination.PageSize != 30 {
		t.Fatalf("Pagination.PageSize = %d, want %d", cfg.Pagination.PageSize, 30)
	}
	if !cfg.Related.Enabled || cfg.Related.Count != 6 {
		t.Fatalf("Related = %#v, want enabled count=6", cfg.Related)
	}
	if !cfg.Timeline.Enabled || cfg.Timeline.Path != "notes" {
		t.Fatalf("Timeline = %#v, want enabled path", cfg.Timeline)
	}
	if !frontmatter.Updated.Equal(updated) {
		t.Fatalf("Frontmatter.Updated = %v, want %v", frontmatter.Updated, updated)
	}
}
