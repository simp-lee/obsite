package vault

import (
	"reflect"
	"testing"
	"time"

	"github.com/simp-lee/obsite/internal/diag"
	"github.com/simp-lee/obsite/internal/model"
)

func TestMergeNoteTagsNormalizesNestedTagsAndPreservesFirstOccurrenceOrder(t *testing.T) {
	t.Parallel()

	existing := []string{" Parent / Child ", "parent", "Topic   Name"}
	inline := []string{"#parent/child", " #topic   name ", "#topic/sub"}

	got := mergeNoteTags(existing, inline)
	want := []string{"parent/child", "parent", "topic-name", "topic/sub"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mergeNoteTags() = %#v, want %#v", got, want)
	}
}

func TestMergeNoteTagsCoalescesSlugEquivalentNestedTags(t *testing.T) {
	t.Parallel()

	existing := []string{"Parent/Child_Name"}
	inline := []string{"#parent/child-name", "#parent/child name"}

	got := mergeNoteTags(existing, inline)
	want := []string{"parent/child-name"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mergeNoteTags() = %#v, want %#v", got, want)
	}
}

func TestBuildTagIndexBuildsNestedAggregatesWithStableOrderingAndSlugPaths(t *testing.T) {
	t.Parallel()

	newer := time.Date(2026, time.April, 5, 0, 0, 0, 0, time.UTC)
	older := time.Date(2026, time.April, 4, 0, 0, 0, 0, time.UTC)

	notes := []*model.Note{
		{
			RelPath: "notes/older.md",
			Slug:    "zeta",
			Tags:    []string{"Parent Space/Child_Name", "parent space"},
			Frontmatter: model.Frontmatter{
				Date: older,
			},
		},
		{
			RelPath: "notes/newer.md",
			Slug:    "alpha",
			Tags:    []string{"parent space/child_name/grand"},
			Frontmatter: model.Frontmatter{
				Date: newer,
			},
		},
		{
			RelPath: "notes/same-date.md",
			Slug:    "beta",
			Tags:    []string{"parent space"},
			Frontmatter: model.Frontmatter{
				Date: newer,
			},
		},
	}

	tags := buildTagIndex(notes)

	parent := tags["parent-space"]
	if parent == nil {
		t.Fatal("tags[parent-space] = nil, want aggregate tag")
	}
	if parent.Slug != "tags/parent-space" {
		t.Fatalf("parent.Slug = %q, want %q", parent.Slug, "tags/parent-space")
	}
	if !reflect.DeepEqual(parent.Notes, []string{"notes/newer.md", "notes/same-date.md", "notes/older.md"}) {
		t.Fatalf("parent.Notes = %#v, want date-desc then slug-asc ordering with dedupe", parent.Notes)
	}

	child := tags["parent-space/child-name"]
	if child == nil {
		t.Fatal("tags[parent-space/child-name] = nil, want child tag")
	}
	if child.Slug != "tags/parent-space/child-name" {
		t.Fatalf("child.Slug = %q, want %q", child.Slug, "tags/parent-space/child-name")
	}
	if !reflect.DeepEqual(child.Notes, []string{"notes/newer.md", "notes/older.md"}) {
		t.Fatalf("child.Notes = %#v, want parent-child aggregate ordering", child.Notes)
	}

	grand := tags["parent-space/child-name/grand"]
	if grand == nil {
		t.Fatal("tags[parent-space/child-name/grand] = nil, want grandchild tag")
	}
	if grand.Slug != "tags/parent-space/child-name/grand" {
		t.Fatalf("grand.Slug = %q, want %q", grand.Slug, "tags/parent-space/child-name/grand")
	}
	if !reflect.DeepEqual(grand.Notes, []string{"notes/newer.md"}) {
		t.Fatalf("grand.Notes = %#v, want only direct note membership", grand.Notes)
	}
}

func TestBuildTagIndexCoalescesSlugEquivalentChildren(t *testing.T) {
	t.Parallel()

	notes := []*model.Note{
		{
			RelPath: "notes/one.md",
			Tags:    []string{"parent/child_name"},
		},
		{
			RelPath: "notes/two.md",
			Tags:    []string{"parent/child-name"},
		},
	}

	tags := buildTagIndex(notes)

	if _, ok := tags["parent/child_name"]; ok {
		t.Fatal("tags[parent/child_name] unexpectedly kept pre-slug equivalent key")
	}

	child := tags["parent/child-name"]
	if child == nil {
		t.Fatal("tags[parent/child-name] = nil, want canonical child tag")
	}
	if child.Slug != "tags/parent/child-name" {
		t.Fatalf("child.Slug = %q, want %q", child.Slug, "tags/parent/child-name")
	}
	if !reflect.DeepEqual(child.Notes, []string{"notes/one.md", "notes/two.md"}) {
		t.Fatalf("child.Notes = %#v, want merged membership for slug-equivalent tags", child.Notes)
	}
}

func TestBuildTagIndexFallsBackToLastModifiedWhenDateIsMissing(t *testing.T) {
	t.Parallel()

	older := time.Date(2026, time.April, 4, 9, 0, 0, 0, time.UTC)
	newer := time.Date(2026, time.April, 5, 9, 0, 0, 0, time.UTC)

	notes := []*model.Note{
		{
			RelPath:      "notes/alpha.md",
			Slug:         "alpha",
			Tags:         []string{"topic"},
			LastModified: older,
		},
		{
			RelPath:      "notes/zeta.md",
			Slug:         "zeta",
			Tags:         []string{"topic"},
			LastModified: newer,
		},
	}

	tags := buildTagIndex(notes)
	topic := tags["topic"]
	if topic == nil {
		t.Fatal("tags[topic] = nil, want topic tag")
	}
	if !reflect.DeepEqual(topic.Notes, []string{"notes/zeta.md", "notes/alpha.md"}) {
		t.Fatalf("topic.Notes = %#v, want LastModified-desc ordering when date is missing", topic.Notes)
	}
}

func TestBuildIndexBuildsNestedTagAggregatesFromMergedNoteTags(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	writeVaultFile(t, vaultPath, "notes/alpha.md", `---
date: 2026-04-05
tags:
  - parent/child
  - parent
---
# Alpha

#parent/child/grand #extra/topic
`)
	writeVaultFile(t, vaultPath, "notes/beta.md", `---
date: 2026-04-06
tags:
  - parent/child
---
# Beta
`)

	scanResult, frontmatterResult := prepareIndexInputs(t, vaultPath)
	idx, err := buildIndexForTest(scanResult, frontmatterResult, diag.NewCollector())
	if err != nil {
		t.Fatalf("BuildIndex() error = %v", err)
	}

	alpha := idx.Notes["notes/alpha.md"]
	if alpha == nil {
		t.Fatal("idx.Notes[notes/alpha.md] = nil, want note")
	}
	wantNoteTags := []string{"parent/child", "parent", "parent/child/grand", "extra/topic"}
	if !reflect.DeepEqual(alpha.Tags, wantNoteTags) {
		t.Fatalf("alpha.Tags = %#v, want %#v", alpha.Tags, wantNoteTags)
	}

	if parent := idx.Tags["parent"]; parent == nil {
		t.Fatal("idx.Tags[parent] = nil, want parent aggregate")
	} else {
		if parent.Slug != "tags/parent" {
			t.Fatalf("idx.Tags[parent].Slug = %q, want %q", parent.Slug, "tags/parent")
		}
		if !reflect.DeepEqual(parent.Notes, []string{"notes/beta.md", "notes/alpha.md"}) {
			t.Fatalf("idx.Tags[parent].Notes = %#v, want sorted aggregate membership without duplicates", parent.Notes)
		}
	}

	if child := idx.Tags["parent/child"]; child == nil {
		t.Fatal("idx.Tags[parent/child] = nil, want child aggregate")
	} else if !reflect.DeepEqual(child.Notes, []string{"notes/beta.md", "notes/alpha.md"}) {
		t.Fatalf("idx.Tags[parent/child].Notes = %#v, want child aggregate membership", child.Notes)
	}

	if grand := idx.Tags["parent/child/grand"]; grand == nil {
		t.Fatal("idx.Tags[parent/child/grand] = nil, want grandchild tag")
	} else if !reflect.DeepEqual(grand.Notes, []string{"notes/alpha.md"}) {
		t.Fatalf("idx.Tags[parent/child/grand].Notes = %#v, want direct grandchild membership", grand.Notes)
	}

	if extra := idx.Tags["extra"]; extra == nil {
		t.Fatal("idx.Tags[extra] = nil, want inline parent aggregate")
	} else if extra.Slug != "tags/extra" {
		t.Fatalf("idx.Tags[extra].Slug = %q, want %q", extra.Slug, "tags/extra")
	}
	if extraTopic := idx.Tags["extra/topic"]; extraTopic == nil {
		t.Fatal("idx.Tags[extra/topic] = nil, want inline child tag")
	} else if extraTopic.Slug != "tags/extra/topic" {
		t.Fatalf("idx.Tags[extra/topic].Slug = %q, want %q", extraTopic.Slug, "tags/extra/topic")
	}
}

func TestBuildIndexDropsFrontmatterTagsWithoutValidSlugSegments(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	writeVaultFile(t, vaultPath, "notes/symbols.md", `---
tags:
  - "!!!"
  - parent/child
---
# Symbols
`)

	scanResult, frontmatterResult := prepareIndexInputs(t, vaultPath)
	idx, err := buildIndexForTest(scanResult, frontmatterResult, diag.NewCollector())
	if err != nil {
		t.Fatalf("BuildIndex() error = %v", err)
	}

	note := idx.Notes["notes/symbols.md"]
	if note == nil {
		t.Fatal("idx.Notes[notes/symbols.md] = nil, want note")
	}
	if !reflect.DeepEqual(note.Tags, []string{"parent/child"}) {
		t.Fatalf("note.Tags = %#v, want only valid canonical tags", note.Tags)
	}
	if _, ok := idx.Tags["!!!"]; ok {
		t.Fatal("idx.Tags[!!!] unexpectedly contains symbols-only tag")
	}
	if child := idx.Tags["parent/child"]; child == nil || !reflect.DeepEqual(child.Notes, []string{"notes/symbols.md"}) {
		t.Fatalf("idx.Tags[parent/child] = %#v, want valid tag membership", child)
	}
}
