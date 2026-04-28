package vault

import (
	"bytes"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/simp-lee/obsite/internal/diag"
	internalmarkdown "github.com/simp-lee/obsite/internal/markdown"
	"github.com/simp-lee/obsite/internal/model"
)

func TestBuildIndexRejectsSlugConflicts(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	writeVaultFile(t, vaultPath, "notes/alpha.md", "---\nslug: same\n---\nalpha\n")
	writeVaultFile(t, vaultPath, "notes/beta.md", "---\nslug: same\n---\nbeta\n")

	scanResult, frontmatterResult := prepareIndexInputs(t, vaultPath)
	diagCollector := diag.NewCollector()

	idx, err := buildIndexForTest(scanResult, frontmatterResult, diagCollector)
	if err == nil {
		t.Fatal("BuildIndex() error = nil, want slug conflict")
	}
	if idx != nil {
		t.Fatalf("BuildIndex() index = %#v, want nil on slug conflict", idx)
	}
	if !diagCollector.HasErrors() {
		t.Fatal("diagCollector.HasErrors() = false, want true")
	}

	var found bool
	for _, diagnostic := range diagCollector.Errors() {
		if diagnostic.Kind == diag.KindSlugConflict {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("diagnostics = %#v, want slug_conflict error", diagCollector.Errors())
	}
}

func TestBuildIndexDoesNotReportFalseSlugConflictForUnicodeNormalization(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	writeVaultFile(t, vaultPath, "notes/Cafe\u0301 Notes.md", "# Accent\n")
	writeVaultFile(t, vaultPath, "notes/Cafe Notes.md", "# Plain\n")

	scanResult, frontmatterResult := prepareIndexInputs(t, vaultPath)
	diagCollector := diag.NewCollector()

	idx, err := buildIndexForTest(scanResult, frontmatterResult, diagCollector)
	if err != nil {
		t.Fatalf("BuildIndex() error = %v, want no slug conflict", err)
	}
	if diagCollector.HasErrors() {
		t.Fatalf("diagCollector.Errors() = %#v, want no errors", diagCollector.Errors())
	}

	accented := idx.Notes["notes/Cafe\u0301 Notes.md"]
	if accented == nil {
		t.Fatal("idx.Notes[notes/Cafe\u0301 Notes.md] = nil, want note")
	}
	if accented.Slug != "café-notes" {
		t.Fatalf("accented.Slug = %q, want %q", accented.Slug, "café-notes")
	}
	if got := idx.NoteBySlug["café-notes"]; got != accented {
		t.Fatalf("idx.NoteBySlug[café-notes] = %p, want %p", got, accented)
	}

	plain := idx.Notes["notes/Cafe Notes.md"]
	if plain == nil {
		t.Fatal("idx.Notes[notes/Cafe Notes.md] = nil, want note")
	}
	if plain.Slug != "cafe-notes" {
		t.Fatalf("plain.Slug = %q, want %q", plain.Slug, "cafe-notes")
	}
	if got := idx.NoteBySlug["cafe-notes"]; got != plain {
		t.Fatalf("idx.NoteBySlug[cafe-notes] = %p, want %p", got, plain)
	}
}

func TestBuildIndexExtractsMetadataAndSeparatesUnpublishedLookups(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	writeVaultFile(t, vaultPath, "notes/guide.md", `---
title: Guide
tags:
  - frontmatter
aliases:
  - Docs
---
# Intro Heading

%% [[Ignore Me]] ![[ignore.png]] #ignored %%
Lead <span>alpha</span> text with #inline/tag and [[Target Page#Section|Doc Link]].
![[assets/diagram.png|600]]
![[Target Page#Section]]
![Chart](../images/chart.png?raw=1#frag)

## Intro Heading

$E=mc^2$

`+"```"+`mermaid
graph TD
A-->B
`+"```"+`
`)
	writeVaultFile(t, vaultPath, "notes/target page.md", "# Section\n\nBody\n")
	writeVaultFile(t, vaultPath, "notes/private.md", "---\naliases:\n  - Docs\npublish: false\n---\nsecret\n")
	writeVaultFile(t, vaultPath, "assets/diagram.png", "png")
	writeVaultFile(t, vaultPath, "images/chart.png", "png")

	scanResult, frontmatterResult := prepareIndexInputs(t, vaultPath)
	diagCollector := diag.NewCollector()

	idx, err := buildIndexForTest(scanResult, frontmatterResult, diagCollector)
	if err != nil {
		t.Fatalf("BuildIndex() error = %v", err)
	}
	if diagCollector.HasErrors() {
		t.Fatalf("diagCollector.Errors() = %#v, want no errors", diagCollector.Errors())
	}

	if len(idx.Notes) != 2 {
		t.Fatalf("len(idx.Notes) = %d, want 2", len(idx.Notes))
	}
	if _, ok := idx.Notes["notes/private.md"]; ok {
		t.Fatal("idx.Notes unexpectedly contains unpublished note")
	}

	guide := idx.Notes["notes/guide.md"]
	if guide == nil {
		t.Fatal("idx.Notes[notes/guide.md] = nil, want note")
	}
	if guide.Slug != "guide" {
		t.Fatalf("guide.Slug = %q, want %q", guide.Slug, "guide")
	}
	if got := idx.NoteBySlug["guide"]; got != guide {
		t.Fatalf("idx.NoteBySlug[guide] = %p, want %p", got, guide)
	}
	if got := idx.NoteByName["guide"]; len(got) != 1 || got[0] != guide {
		t.Fatalf("idx.NoteByName[guide] = %#v, want [%p]", got, guide)
	}
	if got := idx.AliasByName["docs"]; len(got) != 1 || got[0] != guide {
		t.Fatalf("idx.AliasByName[docs] = %#v, want [%p]", got, guide)
	}
	if _, ok := idx.NoteByName["private"]; ok {
		t.Fatal("idx.NoteByName unexpectedly contains unpublished note name")
	}
	if got := idx.Unpublished.AliasByName["docs"]; len(got) != 1 || got[0].RelPath != "notes/private.md" {
		t.Fatalf("idx.Unpublished.AliasByName[docs] = %#v, want unpublished private note", got)
	}

	if strings.Contains(string(guide.RawContent), "Ignore Me") || strings.Contains(string(guide.RawContent), "%%") {
		t.Fatalf("guide.RawContent = %q, want stripped comments", string(guide.RawContent))
	}

	wantHeadings := []model.Heading{
		{Level: 1, Text: "Intro Heading", ID: "intro-heading"},
		{Level: 2, Text: "Intro Heading", ID: "intro-heading-1"},
	}
	if !reflect.DeepEqual(guide.Headings, wantHeadings) {
		t.Fatalf("guide.Headings = %#v, want %#v", guide.Headings, wantHeadings)
	}

	wantTags := []string{"frontmatter", "inline/tag"}
	if !reflect.DeepEqual(guide.Tags, wantTags) {
		t.Fatalf("guide.Tags = %#v, want %#v", guide.Tags, wantTags)
	}
	if _, ok := idx.Tags["ignored"]; ok {
		t.Fatal("idx.Tags unexpectedly contains tag stripped by comment preprocessing")
	}
	if tag := idx.Tags["frontmatter"]; tag == nil || !reflect.DeepEqual(tag.Notes, []string{"notes/guide.md"}) {
		t.Fatalf("idx.Tags[frontmatter] = %#v, want note membership", tag)
	}
	if tag := idx.Tags["inline/tag"]; tag == nil || !reflect.DeepEqual(tag.Notes, []string{"notes/guide.md"}) {
		t.Fatalf("idx.Tags[inline/tag] = %#v, want note membership", tag)
	}

	if !guide.HasMath {
		t.Fatal("guide.HasMath = false, want true")
	}
	if !guide.HasMermaid {
		t.Fatal("guide.HasMermaid = false, want true")
	}

	if len(guide.OutLinks) != 1 {
		t.Fatalf("len(guide.OutLinks) = %d, want 1", len(guide.OutLinks))
	}
	if got := guide.OutLinks[0]; got.RawTarget != "Target Page#Section" || got.Display != "Doc Link" || got.Fragment != "Section" || got.Line != 11 {
		t.Fatalf("guide.OutLinks[0] = %#v, want target/display/fragment/line metadata", got)
	}
	if wantOffset := strings.Index(string(guide.RawContent), "[[Target Page#Section|Doc Link]]"); guide.OutLinks[0].Offset != wantOffset {
		t.Fatalf("guide.OutLinks[0].Offset = %d, want %d", guide.OutLinks[0].Offset, wantOffset)
	}

	if len(guide.Embeds) != 2 {
		t.Fatalf("len(guide.Embeds) = %d, want 2", len(guide.Embeds))
	}
	if got := guide.Embeds[0]; got.Target != "assets/diagram.png" || got.Fragment != "" || !got.IsImage || got.Width != 600 || got.Line != 12 {
		t.Fatalf("guide.Embeds[0] = %#v, want image embed metadata", got)
	}
	if got := guide.Embeds[1]; got.Target != "Target Page" || got.Fragment != "Section" || got.IsImage || got.Width != 0 || got.Line != 13 {
		t.Fatalf("guide.Embeds[1] = %#v, want note embed metadata", got)
	}
	if len(guide.ImageRefs) != 1 {
		t.Fatalf("len(guide.ImageRefs) = %d, want 1", len(guide.ImageRefs))
	}
	if got := guide.ImageRefs[0]; got.RawTarget != "../images/chart.png?raw=1#frag" || got.Line != 14 {
		t.Fatalf("guide.ImageRefs[0] = %#v, want markdown image metadata", got)
	}
	if wantOffset := strings.Index(string(guide.RawContent), "![Chart](../images/chart.png?raw=1#frag)"); guide.ImageRefs[0].Offset != wantOffset {
		t.Fatalf("guide.ImageRefs[0].Offset = %d, want %d", guide.ImageRefs[0].Offset, wantOffset)
	}
	if section, ok := guide.HeadingSections["intro-heading"]; !ok || section.StartOffset >= section.EndOffset {
		t.Fatalf("guide.HeadingSections[intro-heading] = %#v, want non-empty section range", section)
	}

	if len(idx.Assets) != 2 {
		t.Fatalf("len(idx.Assets) = %d, want 2 assets including image embeds", len(idx.Assets))
	}
	if asset := idx.Assets["assets/diagram.png"]; asset == nil || asset.SrcPath != "assets/diagram.png" || asset.RefCount != 1 {
		t.Fatalf("idx.Assets[assets/diagram.png] = %#v, want image embed asset registration", asset)
	}
	asset := idx.Assets["images/chart.png"]
	if asset == nil {
		t.Fatal("idx.Assets[images/chart.png] = nil, want asset")
	}
	if asset.SrcPath != "images/chart.png" || asset.RefCount != 1 {
		t.Fatalf("asset = %#v, want SrcPath and RefCount populated", asset)
	}
}

func TestBuildIndexMarksLeadingCalloutDisplayMathAndNormalizesSlashEdgeHashtags(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	writeVaultFile(t, vaultPath, "notes/math.md", `> [!note] Math
> $$
> x^2
> $$

#parent//child
`)

	scanResult, frontmatterResult := prepareIndexInputs(t, vaultPath)
	idx, err := buildIndexForTest(scanResult, frontmatterResult, diag.NewCollector())
	if err != nil {
		t.Fatalf("BuildIndex() error = %v", err)
	}

	note := idx.Notes["notes/math.md"]
	if note == nil {
		t.Fatal("idx.Notes[notes/math.md] = nil, want note")
	}
	if !note.HasMath {
		t.Fatal("note.HasMath = false, want true for leading callout display math")
	}
	wantTags := []string{"parent/child"}
	if !reflect.DeepEqual(note.Tags, wantTags) {
		t.Fatalf("note.Tags = %#v, want %#v", note.Tags, wantTags)
	}
	tag := idx.Tags["parent/child"]
	if tag == nil {
		t.Fatal("idx.Tags[parent/child] = nil, want canonicalized slash-edge tag")
	}
	if tag.Slug != "tags/parent/child" {
		t.Fatalf("tag.Slug = %q, want %q", tag.Slug, "tags/parent/child")
	}
	if !reflect.DeepEqual(tag.Notes, []string{"notes/math.md"}) {
		t.Fatalf("tag.Notes = %#v, want canonical tag membership", tag.Notes)
	}
}

func TestBuildIndexExtractsVisibleHeadingText(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	writeVaultFile(t, vaultPath, "notes/headings.md", `# Intro #tag [[Target Page|Shown Label]]

## RFC <sup>2</sup> and <span>Alpha</span> [[Target Page#Section|Section Label]]
`)

	scanResult, frontmatterResult := prepareIndexInputs(t, vaultPath)
	if len(frontmatterResult.PublicNotes) != 1 {
		t.Fatalf("len(frontmatterResult.PublicNotes) = %d, want 1", len(frontmatterResult.PublicNotes))
	}
	frontmatterResult.PublicNotes[0].Summary = "stale summary"
	frontmatterResult.PublicNotes[0].HTMLContent = "<p>stale html</p>"

	idx, err := buildIndexForTest(scanResult, frontmatterResult, diag.NewCollector())
	if err != nil {
		t.Fatalf("BuildIndex() error = %v", err)
	}

	note := idx.Notes["notes/headings.md"]
	if note == nil {
		t.Fatal("idx.Notes[notes/headings.md] = nil, want note")
	}
	if len(note.Headings) != 2 {
		t.Fatalf("len(note.Headings) = %d, want 2", len(note.Headings))
	}

	if got := note.Headings[0]; got.Level != 1 || got.Text != "Intro #tag Shown Label" {
		t.Fatalf("note.Headings[0] = %#v, want visible hashtag and wikilink label text", got)
	}
	if got := note.Headings[1]; got.Level != 2 || got.Text != "RFC 2 and Alpha Section Label" {
		t.Fatalf("note.Headings[1] = %#v, want visible text with raw HTML inner text preserved", got)
	}
}

func TestBuildIndexExtractsRawHTMLHeadingTextByNodeSemantics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   model.Heading
	}{
		{
			name:   "entity-before-raw-html",
			source: "# &amp;lt; <span>Alpha</span>\n",
			want:   model.Heading{Level: 1, Text: "&lt; Alpha", ID: "lt-alpha"},
		},
		{
			name:   "code-span-after-raw-html",
			source: "# <span>Alpha</span> `&amp;lt;`\n",
			want:   model.Heading{Level: 1, Text: "Alpha &amp;lt;", ID: "alpha-amp-lt"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			vaultPath := t.TempDir()
			writeVaultFile(t, vaultPath, "notes/headings.md", tt.source)

			scanResult, frontmatterResult := prepareIndexInputs(t, vaultPath)
			idx, err := buildIndexForTest(scanResult, frontmatterResult, diag.NewCollector())
			if err != nil {
				t.Fatalf("BuildIndex() error = %v", err)
			}

			note := idx.Notes["notes/headings.md"]
			if note == nil {
				t.Fatal("idx.Notes[notes/headings.md] = nil, want note")
			}
			if len(note.Headings) != 1 {
				t.Fatalf("len(note.Headings) = %d, want 1", len(note.Headings))
			}

			if got := note.Headings[0]; got != tt.want {
				t.Fatalf("note.Headings[0] = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestBuildIndexExtractsHeadingIDsFromVisibleText(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	writeVaultFile(t, vaultPath, "notes/headings.md", `# Intro *Bold*

## [[Target Page|Shown Label]]

### 中文 标题

#### RFC <sup>2</sup> and <span>Alpha</span> [[Target Page|Shown Label]]
`)
	writeVaultFile(t, vaultPath, "notes/target page.md", "# Target Page\n")

	scanResult, frontmatterResult := prepareIndexInputs(t, vaultPath)
	idx, err := buildIndexForTest(scanResult, frontmatterResult, diag.NewCollector())
	if err != nil {
		t.Fatalf("BuildIndex() error = %v", err)
	}

	note := idx.Notes["notes/headings.md"]
	if note == nil {
		t.Fatal("idx.Notes[notes/headings.md] = nil, want note")
	}

	want := []model.Heading{
		{Level: 1, Text: "Intro Bold", ID: "intro-bold"},
		{Level: 2, Text: "Shown Label", ID: "shown-label"},
		{Level: 3, Text: "中文 标题", ID: "中文-标题"},
		{Level: 4, Text: "RFC 2 and Alpha Shown Label", ID: "rfc-2-and-alpha-shown-label"},
	}
	if !reflect.DeepEqual(note.Headings, want) {
		t.Fatalf("note.Headings = %#v, want %#v", note.Headings, want)
	}
}

func TestBuildIndexAliasLookupDeduplicatesNormalizedAliasesPerNote(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	writeVaultFile(t, vaultPath, "notes/alpha.md", `---
aliases:
  - Docs
  - docs
---
alpha
`)
	writeVaultFile(t, vaultPath, "notes/beta.md", `---
aliases:
  - DOCS
---
beta
`)

	scanResult, frontmatterResult := prepareIndexInputs(t, vaultPath)
	idx, err := buildIndexForTest(scanResult, frontmatterResult, diag.NewCollector())
	if err != nil {
		t.Fatalf("BuildIndex() error = %v", err)
	}

	got := idx.AliasByName["docs"]
	if len(got) != 2 {
		t.Fatalf("len(idx.AliasByName[docs]) = %d, want 2 distinct candidate notes", len(got))
	}

	paths := []string{got[0].RelPath, got[1].RelPath}
	sort.Strings(paths)
	want := []string{"notes/alpha.md", "notes/beta.md"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("idx.AliasByName[docs] paths = %#v, want %#v", paths, want)
	}
}

func TestBuildIndexKeepsImageLikeNoteEmbedsAsNotes(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	writeVaultFile(t, vaultPath, "notes/gallery.md", "![[photo.png]]\n")
	writeVaultFile(t, vaultPath, "notes/photo.png.md", "# Photo Note\n")

	scanResult, frontmatterResult := prepareIndexInputs(t, vaultPath)
	idx, err := buildIndexForTest(scanResult, frontmatterResult, diag.NewCollector())
	if err != nil {
		t.Fatalf("BuildIndex() error = %v", err)
	}

	note := idx.Notes["notes/gallery.md"]
	if note == nil {
		t.Fatal("idx.Notes[notes/gallery.md] = nil, want note")
	}
	if len(note.Embeds) != 1 {
		t.Fatalf("len(note.Embeds) = %d, want 1", len(note.Embeds))
	}

	if got := note.Embeds[0]; got.Target != "photo.png" || got.Fragment != "" || got.IsImage || got.Width != 0 || got.Line != 1 {
		t.Fatalf("note.Embeds[0] = %#v, want note embed metadata without image classification", got)
	}
}

func TestBuildIndexRecognizesAttachmentFolderEmbedsAndDecodedMarkdownImages(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	writeVaultFile(t, vaultPath, ".obsidian/app.json", `{"attachmentFolderPath":"assets/uploads"}`)
	writeVaultFile(t, vaultPath, "notes/gallery.md", "![[diagram.png|640]]\n![Chart](../images/My%20Chart.png?raw=1#frag)\n")
	writeVaultFile(t, vaultPath, "assets/uploads/diagram.png", "png")
	writeVaultFile(t, vaultPath, "images/My Chart.png", "png")

	scanResult, frontmatterResult := prepareIndexInputs(t, vaultPath)
	idx, err := buildIndexForTest(scanResult, frontmatterResult, diag.NewCollector())
	if err != nil {
		t.Fatalf("BuildIndex() error = %v", err)
	}

	note := idx.Notes["notes/gallery.md"]
	if note == nil {
		t.Fatal("idx.Notes[notes/gallery.md] = nil, want note")
	}
	if len(note.Embeds) != 1 {
		t.Fatalf("len(note.Embeds) = %d, want 1", len(note.Embeds))
	}
	if got := note.Embeds[0]; got.Target != "diagram.png" || got.Fragment != "" || !got.IsImage || got.Width != 640 || got.Line != 1 {
		t.Fatalf("note.Embeds[0] = %#v, want attachment-folder image embed metadata", got)
	}

	if len(idx.Assets) != 2 {
		t.Fatalf("len(idx.Assets) = %d, want 2 assets including attachment-folder image embeds", len(idx.Assets))
	}
	if asset := idx.Assets["assets/uploads/diagram.png"]; asset == nil || asset.SrcPath != "assets/uploads/diagram.png" || asset.RefCount != 1 {
		t.Fatalf("idx.Assets[assets/uploads/diagram.png] = %#v, want attachment-folder image embed asset registration", asset)
	}
	asset := idx.Assets["images/My Chart.png"]
	if asset == nil {
		t.Fatal("idx.Assets[images/My Chart.png] = nil, want asset")
	}
	if asset.SrcPath != "images/My Chart.png" || asset.RefCount != 1 {
		t.Fatalf("asset = %#v, want decoded SrcPath and RefCount populated", asset)
	}
}

func TestBuildIndexRecognizesSlashPathImageEmbedsAsAssets(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	writeVaultFile(t, vaultPath, "notes/gallery.md", "![[assets/diagram.png|600]]\n")
	writeVaultFile(t, vaultPath, "assets/diagram.png", "png")

	scanResult, frontmatterResult := prepareIndexInputs(t, vaultPath)
	idx, err := buildIndexForTest(scanResult, frontmatterResult, diag.NewCollector())
	if err != nil {
		t.Fatalf("BuildIndex() error = %v", err)
	}

	note := idx.Notes["notes/gallery.md"]
	if note == nil {
		t.Fatal("idx.Notes[notes/gallery.md] = nil, want note")
	}
	if len(note.Embeds) != 1 {
		t.Fatalf("len(note.Embeds) = %d, want 1", len(note.Embeds))
	}
	if got := note.Embeds[0]; got.Target != "assets/diagram.png" || got.Fragment != "" || !got.IsImage || got.Width != 600 || got.Line != 1 {
		t.Fatalf("note.Embeds[0] = %#v, want slash-path image embed metadata", got)
	}
	if asset := idx.Assets["assets/diagram.png"]; asset == nil || asset.SrcPath != "assets/diagram.png" || asset.RefCount != 1 {
		t.Fatalf("idx.Assets[assets/diagram.png] = %#v, want slash-path image embed asset registration", asset)
	}
}

func TestBuildIndexCanonicalizesUnicodeSlashPathImageEmbeds(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	writeVaultFile(t, vaultPath, "notes/gallery.md", "![[assets/Café Diagram.png|600]]\n")
	writeVaultFile(t, vaultPath, "assets/Cafe\u0301 Diagram.png", "png")

	scanResult, frontmatterResult := prepareIndexInputs(t, vaultPath)
	idx, err := buildIndexForTest(scanResult, frontmatterResult, diag.NewCollector())
	if err != nil {
		t.Fatalf("BuildIndex() error = %v", err)
	}

	note := idx.Notes["notes/gallery.md"]
	if note == nil {
		t.Fatal("idx.Notes[notes/gallery.md] = nil, want note")
	}
	if len(note.Embeds) != 1 {
		t.Fatalf("len(note.Embeds) = %d, want 1", len(note.Embeds))
	}
	if got := note.Embeds[0]; got.Target != "assets/Café Diagram.png" || !got.IsImage || got.Width != 600 {
		t.Fatalf("note.Embeds[0] = %#v, want canonicalized slash-path image embed metadata", got)
	}
	if asset := idx.Assets["assets/Cafe\u0301 Diagram.png"]; asset == nil || asset.RefCount != 1 {
		t.Fatalf("idx.Assets[assets/Cafe\\u0301 Diagram.png] = %#v, want canonicalized slash-path image embed asset", asset)
	}
}

func TestBuildIndexKeepsLeadingNonFrontmatterDelimiterBlocksVisibleToPass1(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	writeVaultFile(t, vaultPath, "notes/rule.md", "---\n# Heading\n\nLead #topic [[Target]]\n\n---\n\nBody\n")
	writeVaultFile(t, vaultPath, "notes/target.md", "# Target\n")

	scanResult, frontmatterResult := prepareIndexInputs(t, vaultPath)
	idx, err := buildIndexForTest(scanResult, frontmatterResult, diag.NewCollector())
	if err != nil {
		t.Fatalf("BuildIndex() error = %v", err)
	}

	note := idx.Notes["notes/rule.md"]
	if note == nil {
		t.Fatal("idx.Notes[notes/rule.md] = nil, want note")
	}

	wantHeadings := []model.Heading{{Level: 1, Text: "Heading", ID: "heading"}}
	if !reflect.DeepEqual(note.Headings, wantHeadings) {
		t.Fatalf("note.Headings = %#v, want %#v", note.Headings, wantHeadings)
	}
	if !reflect.DeepEqual(note.Tags, []string{"topic"}) {
		t.Fatalf("note.Tags = %#v, want %#v", note.Tags, []string{"topic"})
	}
	if len(note.OutLinks) != 1 {
		t.Fatalf("len(note.OutLinks) = %d, want 1", len(note.OutLinks))
	}
	if got := note.OutLinks[0]; got.RawTarget != "Target" || got.Display != "Target" {
		t.Fatalf("note.OutLinks[0] = %#v, want visible outlink extracted from leading delimiter block", got)
	}
}

func TestBuildIndexRegistersExtendedObsidianImageEmbedExtensions(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	writeVaultFile(t, vaultPath, "notes/gallery.md", "![[photo.apng]]\n![[camera.jfif]]\n")
	writeVaultFile(t, vaultPath, "notes/photo.apng", "apng")
	writeVaultFile(t, vaultPath, "notes/camera.jfif", "jfif")

	scanResult, frontmatterResult := prepareIndexInputs(t, vaultPath)
	idx, err := buildIndexForTest(scanResult, frontmatterResult, diag.NewCollector())
	if err != nil {
		t.Fatalf("BuildIndex() error = %v", err)
	}

	note := idx.Notes["notes/gallery.md"]
	if note == nil {
		t.Fatal("idx.Notes[notes/gallery.md] = nil, want note")
	}
	if len(note.Embeds) != 2 {
		t.Fatalf("len(note.Embeds) = %d, want 2", len(note.Embeds))
	}
	if got := note.Embeds[0]; got.Target != "photo.apng" || !got.IsImage {
		t.Fatalf("note.Embeds[0] = %#v, want .apng embed classified as image", got)
	}
	if got := note.Embeds[1]; got.Target != "camera.jfif" || !got.IsImage {
		t.Fatalf("note.Embeds[1] = %#v, want .jfif embed classified as image", got)
	}
	if asset := idx.Assets["notes/photo.apng"]; asset == nil || asset.RefCount != 1 {
		t.Fatalf("idx.Assets[notes/photo.apng] = %#v, want registered asset", asset)
	}
	if asset := idx.Assets["notes/camera.jfif"]; asset == nil || asset.RefCount != 1 {
		t.Fatalf("idx.Assets[notes/camera.jfif] = %#v, want registered asset", asset)
	}
}

// AC-R026: FR-5、FR-12 扩展图片 embed 在索引与渲染阶段必须共享同一图片后缀白名单
func TestExtendedImageEmbedsShareWhitelistAcrossIndexAndRender(t *testing.T) {
	t.Parallel()

	// REG-026
	// Ledger Key: asset.image / pass1-pass2-extension-whitelists-drift
	vaultPath := t.TempDir()
	writeVaultFile(t, vaultPath, ".obsidian/app.json", `{"attachmentFolderPath":"attachments"}`)
	writeVaultFile(t, vaultPath, "notes/gallery.md", "![[photo.apng]]\n![[camera.jfif|320]]\n")
	writeVaultFile(t, vaultPath, "notes/photo.apng", "apng")
	writeVaultFile(t, vaultPath, "attachments/camera.jfif", "jfif")

	scanResult, frontmatterResult := prepareIndexInputs(t, vaultPath)
	idx, err := buildIndexForTest(scanResult, frontmatterResult, diag.NewCollector())
	if err != nil {
		t.Fatalf("BuildIndex() error = %v", err)
	}

	note := idx.Notes["notes/gallery.md"]
	if note == nil {
		t.Fatal("idx.Notes[notes/gallery.md] = nil, want note")
	}
	if len(note.Embeds) != 2 {
		t.Fatalf("len(note.Embeds) = %d, want 2", len(note.Embeds))
	}
	if got := note.Embeds[0]; got.Target != "photo.apng" || !got.IsImage {
		t.Fatalf("note.Embeds[0] = %#v, want .apng embed classified as image", got)
	}
	if got := note.Embeds[1]; got.Target != "camera.jfif" || !got.IsImage || got.Width != 320 {
		t.Fatalf("note.Embeds[1] = %#v, want .jfif attachment-folder embed classified as image", got)
	}
	if asset := idx.Assets["notes/photo.apng"]; asset == nil || asset.RefCount != 1 {
		t.Fatalf("idx.Assets[notes/photo.apng] = %#v, want registered asset", asset)
	}
	if asset := idx.Assets["attachments/camera.jfif"]; asset == nil || asset.RefCount != 1 {
		t.Fatalf("idx.Assets[attachments/camera.jfif] = %#v, want registered attachment-folder asset", asset)
	}

	sink := &indexTestAssetSink{paths: map[string]string{
		"notes/photo.apng":        "assets/photo.apng",
		"attachments/camera.jfif": "assets/camera.jfif",
	}}
	collector := diag.NewCollector()
	md, _ := internalmarkdown.NewMarkdown(idx, note, sink, collector)

	var buf bytes.Buffer
	if err := md.Convert([]byte("![[photo.apng]]\n\n![[camera.jfif|320]]\n"), &buf); err != nil {
		t.Fatalf("Convert() error = %v", err)
	}

	html := buf.String()
	if got := strings.Count(html, "<img "); got != 2 {
		t.Fatalf("image tag count = %d, want 2 extended image embeds rendered as images\n%s", got, html)
	}
	if !strings.Contains(html, `<img src="../assets/photo.apng" alt="photo">`) {
		t.Fatalf("HTML = %q, want .apng embed rendered as image", html)
	}
	if !strings.Contains(html, `<img src="../assets/camera.jfif" alt="camera" width="320" loading="lazy">`) {
		t.Fatalf("HTML = %q, want .jfif attachment-folder embed rendered as image", html)
	}
	if !reflect.DeepEqual(sink.registered, []string{"notes/photo.apng", "attachments/camera.jfif"}) {
		t.Fatalf("registered = %#v, want both extended image assets registered once", sink.registered)
	}
	if got := collector.Diagnostics(); len(got) != 0 {
		t.Fatalf("collector.Diagnostics() = %#v, want no degradation diagnostics for supported extended image embeds", got)
	}
}

func TestBuildIndexCanonicalizesUnicodeMarkdownImagePathsAndImageEmbeds(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	writeVaultFile(t, vaultPath, ".obsidian/app.json", `{"attachmentFolderPath":"assets/uploads"}`)
	writeVaultFile(t, vaultPath, "notes/gallery.md", "![[Café-Poster.png|640]]\n![Chart](../images/Café-Chart.png?raw=1#frag)\n")
	writeVaultFile(t, vaultPath, "assets/uploads/Cafe\u0301-Poster.png", "png")
	writeVaultFile(t, vaultPath, "images/Cafe\u0301-Chart.png", "png")

	scanResult, frontmatterResult := prepareIndexInputs(t, vaultPath)
	idx, err := buildIndexForTest(scanResult, frontmatterResult, diag.NewCollector())
	if err != nil {
		t.Fatalf("BuildIndex() error = %v", err)
	}

	note := idx.Notes["notes/gallery.md"]
	if note == nil {
		t.Fatal("idx.Notes[notes/gallery.md] = nil, want note")
	}
	if len(note.Embeds) != 1 {
		t.Fatalf("len(note.Embeds) = %d, want 1", len(note.Embeds))
	}
	if got := note.Embeds[0]; got.Target != "Café-Poster.png" || !got.IsImage || got.Width != 640 {
		t.Fatalf("note.Embeds[0] = %#v, want composed image embed metadata", got)
	}
	if len(note.ImageRefs) != 1 {
		t.Fatalf("len(note.ImageRefs) = %d, want 1", len(note.ImageRefs))
	}
	if got := note.ImageRefs[0]; got.RawTarget != "../images/Café-Chart.png?raw=1#frag" {
		t.Fatalf("note.ImageRefs[0] = %#v, want composed markdown image metadata", got)
	}

	if asset := idx.Assets["assets/uploads/Cafe\u0301-Poster.png"]; asset == nil || asset.RefCount != 1 {
		t.Fatalf("idx.Assets[assets/uploads/Cafe\\u0301-Poster.png] = %#v, want canonicalized image embed asset", asset)
	}
	if asset := idx.Assets["images/Cafe\u0301-Chart.png"]; asset == nil || asset.RefCount != 1 {
		t.Fatalf("idx.Assets[images/Cafe\\u0301-Chart.png] = %#v, want canonicalized markdown image asset", asset)
	}
	if got := idx.ResolveAssetPath("assets/uploads/Café-Poster.png"); got != "assets/uploads/Cafe\u0301-Poster.png" {
		t.Fatalf("idx.ResolveAssetPath(embed) = %q, want %q", got, "assets/uploads/Cafe\u0301-Poster.png")
	}
	if got := idx.ResolveAssetPath("images/Café-Chart.png"); got != "images/Cafe\u0301-Chart.png" {
		t.Fatalf("idx.ResolveAssetPath(markdown image) = %q, want %q", got, "images/Cafe\u0301-Chart.png")
	}
	if got := idx.ResolveResourcePath("assets/uploads/Café-Poster.png"); got != "assets/uploads/Cafe\u0301-Poster.png" {
		t.Fatalf("idx.ResolveResourcePath(embed) = %q, want %q", got, "assets/uploads/Cafe\u0301-Poster.png")
	}
	if got := idx.ResolveResourceBaseName("Café-Poster.png"); got != "assets/uploads/Cafe\u0301-Poster.png" {
		t.Fatalf("idx.ResolveResourceBaseName() = %q, want %q", got, "assets/uploads/Cafe\u0301-Poster.png")
	}
}

func TestBuildIndexRecordsUnresolvedDiagnosticsForMissingAndSkippedMarkdownImages(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	writeVaultFile(t, vaultPath, "notes/post.md", "![Missing](../images/missing.png)\n![Hidden](../.hidden/secret.png)\n![External](https://example.com/photo.png)\n![Fragment](#hero)\n")
	writeVaultFile(t, vaultPath, ".hidden/secret.png", "secret")

	scanResult, frontmatterResult := prepareIndexInputs(t, vaultPath)
	diagCollector := diag.NewCollector()
	idx, err := buildIndexForTest(scanResult, frontmatterResult, diagCollector)
	if err != nil {
		t.Fatalf("BuildIndex() error = %v", err)
	}

	if idx == nil {
		t.Fatal("BuildIndex() index = nil, want index")
	}
	if len(idx.Assets) != 0 {
		t.Fatalf("len(idx.Assets) = %d, want 0 unresolved markdown images excluded from assets", len(idx.Assets))
	}

	got := diagCollector.Diagnostics()
	if len(got) != 2 {
		t.Fatalf("len(diagCollector.Diagnostics()) = %d, want 2 unresolved_asset warnings", len(got))
	}

	wantLines := []int{1, 2}
	for index, diagnostic := range got {
		if diagnostic.Kind != diag.KindUnresolvedAsset {
			t.Fatalf("diagnostics[%d] = %#v, want unresolved_asset warning", index, diagnostic)
		}
		if diagnostic.Location.Path != "notes/post.md" {
			t.Fatalf("diagnostics[%d].Location.Path = %q, want %q", index, diagnostic.Location.Path, "notes/post.md")
		}
		if diagnostic.Location.Line != wantLines[index] {
			t.Fatalf("diagnostics[%d].Location.Line = %d, want %d", index, diagnostic.Location.Line, wantLines[index])
		}
	}
	if !strings.Contains(got[0].Message, "../images/missing.png") {
		t.Fatalf("diagnostics[0].Message = %q, want missing markdown image target", got[0].Message)
	}
	if !strings.Contains(got[1].Message, "../.hidden/secret.png") {
		t.Fatalf("diagnostics[1].Message = %q, want skipped markdown image target", got[1].Message)
	}
}

func TestBuildIndexRecordsUnresolvedDiagnosticsForSymlinkMarkdownImages(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outsidePath := t.TempDir()
	writeVaultFile(t, vaultPath, "notes/post.md", "![Linked](../images/link.png)\n")
	writeVaultFile(t, outsidePath, "link.png", "png")
	writeVaultSymlink(t, filepath.Join(outsidePath, "link.png"), filepath.Join(vaultPath, "images", "link.png"))

	scanResult, frontmatterResult := prepareIndexInputs(t, vaultPath)
	diagCollector := diag.NewCollector()
	idx, err := buildIndexForTest(scanResult, frontmatterResult, diagCollector)
	if err != nil {
		t.Fatalf("BuildIndex() error = %v", err)
	}

	if idx == nil {
		t.Fatal("BuildIndex() index = nil, want index")
	}
	if len(idx.Assets) != 0 {
		t.Fatalf("len(idx.Assets) = %d, want 0 unresolved markdown images excluded from assets", len(idx.Assets))
	}

	got := diagCollector.Diagnostics()
	if len(got) != 1 {
		t.Fatalf("len(diagCollector.Diagnostics()) = %d, want 1 unresolved_asset warning", len(got))
	}
	if got[0].Kind != diag.KindUnresolvedAsset {
		t.Fatalf("diagnostics[0] = %#v, want unresolved_asset warning", got[0])
	}
	if got[0].Location.Path != "notes/post.md" || got[0].Location.Line != 1 {
		t.Fatalf("diagnostics[0].Location = %#v, want notes/post.md:1", got[0].Location)
	}
	if !strings.Contains(got[0].Message, "../images/link.png") {
		t.Fatalf("diagnostics[0].Message = %q, want symlink markdown image target", got[0].Message)
	}
}

func TestBuildIndexLeavesSummaryForRenderedContentNormalization(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	writeVaultFile(t, vaultPath, "notes/summary.md", `<p>Alpha</p><p>Beta</p>

<div>Gamma<br>Delta</div>

<script>
omit me entirely
</script>

<style>
.secret { display:none }
</style>

One two three four five six seven eight nine ten eleven twelve thirteen fourteen fifteen sixteen seventeen eighteen nineteen Supercalifragilistic.
`)

	scanResult, frontmatterResult := prepareIndexInputs(t, vaultPath)
	idx, err := buildIndexForTest(scanResult, frontmatterResult, diag.NewCollector())
	if err != nil {
		t.Fatalf("BuildIndex() error = %v", err)
	}

	note := idx.Notes["notes/summary.md"]
	if note == nil {
		t.Fatal("idx.Notes[notes/summary.md] = nil, want note")
	}
	if note.Summary != "" {
		t.Fatalf("note.Summary = %q, want empty until pass-2 rendered HTML is available", note.Summary)
	}
	if note.HTMLContent != "" {
		t.Fatalf("note.HTMLContent = %q, want empty until pass-2 rendered HTML is available", note.HTMLContent)
	}
}

func TestBuildIndexWithOptionsHonorsBoundedPassOneConcurrency(t *testing.T) {
	vaultPath := t.TempDir()
	for _, relPath := range []string{"notes/alpha.md", "notes/beta.md", "notes/gamma.md", "notes/delta.md"} {
		writeVaultFile(t, vaultPath, relPath, "# Title\n\nBody\n")
	}

	scanResult, frontmatterResult := prepareIndexInputs(t, vaultPath)
	started := make(chan string, len(frontmatterResult.PublicNotes))
	release := make(chan struct{})
	done := make(chan struct{})

	var current atomic.Int32
	var maxSeen atomic.Int32

	var (
		idx *model.VaultIndex
		err error
	)

	go func() {
		idx, err = buildIndexWithOptions(scanResult, frontmatterResult, diag.NewCollector(), indexBuildOptions{
			concurrency: 2,
			onNoteStart: func(note *model.Note) {
				active := current.Add(1)
				for {
					seen := maxSeen.Load()
					if active <= seen || maxSeen.CompareAndSwap(seen, active) {
						break
					}
				}
				started <- note.RelPath
				<-release
			},
			onNoteDone: func(*model.Note) {
				current.Add(-1)
			},
		})
		close(done)
	}()

	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("buildIndexWithOptions() did not start pass-1 work up to the configured concurrency")
		}
	}
	if maxSeen.Load() != 2 {
		t.Fatalf("max pass-1 workers = %d, want %d", maxSeen.Load(), 2)
	}

	select {
	case relPath := <-started:
		t.Fatalf("pass 1 started %q before a worker slot was released", relPath)
	case <-time.After(150 * time.Millisecond):
	}

	close(release)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("buildIndexWithOptions() did not complete")
	}

	if err != nil {
		t.Fatalf("buildIndexWithOptions() error = %v", err)
	}
	if idx == nil {
		t.Fatal("buildIndexWithOptions() index = nil, want index")
	}
	if len(idx.Notes) != 4 {
		t.Fatalf("len(idx.Notes) = %d, want %d", len(idx.Notes), 4)
	}
	if maxSeen.Load() > 2 {
		t.Fatalf("max pass-1 workers = %d, want <= %d", maxSeen.Load(), 2)
	}
}

func buildIndexForTest(scanResult ScanResult, frontmatterResult FrontmatterResult, diagCollector *diag.Collector) (*model.VaultIndex, error) {
	return BuildIndexWithConcurrency(scanResult, frontmatterResult, diagCollector, 1)
}

func prepareIndexInputs(t *testing.T, vaultPath string) (ScanResult, FrontmatterResult) {
	t.Helper()

	scanResult, err := Scan(vaultPath)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	frontmatterResult, err := ParseFrontmatter(scanResult, model.SiteConfig{DefaultPublish: true})
	if err != nil {
		t.Fatalf("ParseFrontmatter() error = %v", err)
	}

	return scanResult, frontmatterResult
}

type indexTestAssetSink struct {
	paths      map[string]string
	registered []string
}

func (s *indexTestAssetSink) Register(vaultRelPath string) string {
	s.registered = append(s.registered, vaultRelPath)
	if s.paths == nil {
		return ""
	}
	return s.paths[vaultRelPath]
}
