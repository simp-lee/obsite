package model

import "testing"

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
