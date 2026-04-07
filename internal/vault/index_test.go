package vault

import (
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/simp-lee/obsite/internal/diag"
	"github.com/simp-lee/obsite/internal/model"
)

func TestBuildIndexRejectsSlugConflicts(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	writeVaultFile(t, vaultPath, "notes/alpha.md", "---\nslug: same\n---\nalpha\n")
	writeVaultFile(t, vaultPath, "notes/beta.md", "---\nslug: same\n---\nbeta\n")

	scanResult, frontmatterResult := prepareIndexInputs(t, vaultPath)
	diagCollector := diag.NewCollector()

	idx, err := BuildIndex(scanResult, frontmatterResult, diagCollector)
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

	idx, err := BuildIndex(scanResult, frontmatterResult, diagCollector)
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

func TestBuildIndexExtractsVisibleHeadingText(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	writeVaultFile(t, vaultPath, "notes/headings.md", `# Intro #tag [[Target Page|Shown Label]]

## Raw <span>HTML</span> [[Target Page#Section|Section Label]]
`)

	scanResult, frontmatterResult := prepareIndexInputs(t, vaultPath)
	idx, err := BuildIndex(scanResult, frontmatterResult, diag.NewCollector())
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
	if got := note.Headings[1]; got.Level != 2 || got.Text != "Raw HTML Section Label" {
		t.Fatalf("note.Headings[1] = %#v, want visible text with raw HTML stripped", got)
	}
}

func TestBuildIndexExtractsHeadingIDsFromVisibleText(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	writeVaultFile(t, vaultPath, "notes/headings.md", `# Intro *Bold*

## [[Target Page|Shown Label]]

### 中文 标题
`)
	writeVaultFile(t, vaultPath, "notes/target page.md", "# Target Page\n")

	scanResult, frontmatterResult := prepareIndexInputs(t, vaultPath)
	idx, err := BuildIndex(scanResult, frontmatterResult, diag.NewCollector())
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
	idx, err := BuildIndex(scanResult, frontmatterResult, diag.NewCollector())
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
	idx, err := BuildIndex(scanResult, frontmatterResult, diag.NewCollector())
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
	idx, err := BuildIndex(scanResult, frontmatterResult, diag.NewCollector())
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

func TestBuildIndexRegistersExtendedObsidianImageEmbedExtensions(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	writeVaultFile(t, vaultPath, "notes/gallery.md", "![[photo.apng]]\n![[camera.jfif]]\n")
	writeVaultFile(t, vaultPath, "notes/photo.apng", "apng")
	writeVaultFile(t, vaultPath, "notes/camera.jfif", "jfif")

	scanResult, frontmatterResult := prepareIndexInputs(t, vaultPath)
	idx, err := BuildIndex(scanResult, frontmatterResult, diag.NewCollector())
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

func TestBuildIndexRecordsUnresolvedDiagnosticsForMissingAndSkippedMarkdownImages(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	writeVaultFile(t, vaultPath, "notes/post.md", "![Missing](../images/missing.png)\n![Hidden](../.hidden/secret.png)\n![External](https://example.com/photo.png)\n![Fragment](#hero)\n")
	writeVaultFile(t, vaultPath, ".hidden/secret.png", "secret")

	scanResult, frontmatterResult := prepareIndexInputs(t, vaultPath)
	diagCollector := diag.NewCollector()
	idx, err := BuildIndex(scanResult, frontmatterResult, diagCollector)
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
	idx, err := BuildIndex(scanResult, frontmatterResult, diagCollector)
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

func TestBuildIndexBuildsSummaryFromVisibleTextAtWordBoundary(t *testing.T) {
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
	idx, err := BuildIndex(scanResult, frontmatterResult, diag.NewCollector())
	if err != nil {
		t.Fatalf("BuildIndex() error = %v", err)
	}

	note := idx.Notes["notes/summary.md"]
	if note == nil {
		t.Fatal("idx.Notes[notes/summary.md] = nil, want note")
	}

	want := "Alpha Beta Gamma Delta One two three four five six seven eight nine ten eleven twelve thirteen fourteen fifteen sixteen seventeen eighteen nineteen"
	if note.Summary != want {
		t.Fatalf("note.Summary = %q, want %q", note.Summary, want)
	}
	if strings.Contains(note.Summary, "AlphaBeta") || strings.Contains(note.Summary, "GammaDelta") {
		t.Fatalf("note.Summary = %q, want HTML block and br boundaries to preserve word spacing", note.Summary)
	}
	if strings.Contains(note.Summary, "omit me entirely") {
		t.Fatalf("note.Summary = %q, want script contents removed", note.Summary)
	}
	if strings.Contains(note.Summary, "display:none") {
		t.Fatalf("note.Summary = %q, want style contents removed", note.Summary)
	}
	if strings.Contains(note.Summary, "Supercalifragilistic") {
		t.Fatalf("note.Summary = %q, want truncation at previous word boundary", note.Summary)
	}
	if got := utf8.RuneCountInString(note.Summary); got > 150 {
		t.Fatalf("utf8.RuneCountInString(note.Summary) = %d, want <= 150", got)
	}
}

func TestBuildIndexSummaryKeepsLongLeadingWordIntact(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	longWord := strings.Repeat("A", summaryRuneLimit+20)
	writeVaultFile(t, vaultPath, "notes/summary-long-word.md", longWord+" trailing words that should be omitted\n")

	scanResult, frontmatterResult := prepareIndexInputs(t, vaultPath)
	idx, err := BuildIndex(scanResult, frontmatterResult, diag.NewCollector())
	if err != nil {
		t.Fatalf("BuildIndex() error = %v", err)
	}

	note := idx.Notes["notes/summary-long-word.md"]
	if note == nil {
		t.Fatal("idx.Notes[notes/summary-long-word.md] = nil, want note")
	}
	if note.Summary != longWord {
		t.Fatalf("note.Summary = %q, want full leading word without mid-word truncation", note.Summary)
	}
	if got := utf8.RuneCountInString(note.Summary); got != summaryRuneLimit+20 {
		t.Fatalf("utf8.RuneCountInString(note.Summary) = %d, want %d", got, summaryRuneLimit+20)
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
