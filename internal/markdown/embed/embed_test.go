package embed

import (
	"bufio"
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/simp-lee/obsite/internal/diag"
	"github.com/simp-lee/obsite/internal/model"
	gast "github.com/yuin/goldmark/ast"
	gmwikilink "go.abhg.dev/goldmark/wikilink"
)

func TestImageAssetCandidatesIncludeAttachmentFolderFallback(t *testing.T) {
	t.Parallel()

	note := &model.Note{RelPath: "notes/current.md"}
	idx := &model.VaultIndex{AttachmentFolderPath: "assets/uploads"}

	got := imageAssetCandidates(note, idx, "diagram.png")
	want := []string{"notes/diagram.png", "diagram.png", "assets/uploads/diagram.png"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("imageAssetCandidates() = %#v, want %#v", got, want)
	}
}

func TestSelectEmbedSourceUsesHeadingSectionRange(t *testing.T) {
	t.Parallel()

	raw := []byte("# Top\n\n## Section\n\nWanted\n\n## Later\n\nSkip\n")
	start := strings.Index(string(raw), "## Section")
	end := strings.Index(string(raw), "## Later")
	note := &model.Note{
		RawContent: raw,
		HeadingSections: map[string]model.SectionRange{
			"section": {StartOffset: start, EndOffset: end},
		},
	}

	got := string(selectEmbedSource(note, "section"))
	want := "## Section\n\nWanted\n\n"
	if got != want {
		t.Fatalf("selectEmbedSource() = %q, want %q", got, want)
	}
}

func TestEmbedAltTextFallsBackToFilenameStem(t *testing.T) {
	t.Parallel()

	if got := embedAltText("600", "diagram.png", "assets/uploads/diagram.png"); got != "diagram" {
		t.Fatalf("embedAltText() = %q, want %q", got, "diagram")
	}
	if got := embedAltText("Shown Label", "diagram.png", "assets/uploads/diagram.png"); got != "Shown Label" {
		t.Fatalf("embedAltText() = %q, want %q", got, "Shown Label")
	}
}

func TestRenderEmbedFallsBackToPlainTextAndLinkForBlockReferences(t *testing.T) {
	t.Parallel()

	current := &model.Note{
		Slug:    "current",
		RelPath: "notes/current.md",
		Embeds:  []model.EmbedRef{{Target: "Guide", Fragment: "^block-ref", Line: 3}},
	}
	target := &model.Note{Slug: "guide", RelPath: "notes/guide.md"}
	idx := &model.VaultIndex{
		Notes: map[string]*model.Note{
			current.RelPath: current,
			target.RelPath:  target,
		},
		NoteBySlug: map[string]*model.Note{
			current.Slug: current,
			target.Slug:  target,
		},
		NoteByName: map[string][]*model.Note{
			"current": {current},
			"guide":   {target},
		},
		AliasByName: map[string][]*model.Note{},
	}
	collector := diag.NewCollector()
	renderer := &wikilinkHTMLRenderer{
		index:       idx,
		currentNote: current,
		outputNote:  current,
		diag:        collector,
	}
	node := &gmwikilink.Node{Target: []byte("Guide"), Fragment: []byte("^block-ref"), Embed: true}

	var raw bytes.Buffer
	buf := bufio.NewWriter(&raw)
	status, err := renderer.renderEmbed(buf, nil, node)
	if err != nil {
		t.Fatalf("renderEmbed() error = %v", err)
	}
	if err := buf.Flush(); err != nil {
		t.Fatalf("buf.Flush() error = %v", err)
	}
	if status != gast.WalkSkipChildren {
		t.Fatalf("renderEmbed() status = %v, want %v", status, gast.WalkSkipChildren)
	}

	html := raw.String()
	if !strings.Contains(html, `Guide#^block-ref`) {
		t.Fatalf("HTML = %q, want raw block reference text preserved", html)
	}
	if !strings.Contains(html, `<a href="../guide/">open note</a>`) {
		t.Fatalf("HTML = %q, want fallback link to the resolved note", html)
	}

	want := []diag.Diagnostic{{
		Severity: diag.SeverityWarning,
		Kind:     diag.KindUnsupportedSyntax,
		Location: diag.Location{Path: current.RelPath, Line: 3},
		Message:  `embed "Guide#^block-ref" block reference embeds are not supported; rendering as plain text with a link`,
	}}
	if got := collector.Diagnostics(); !reflect.DeepEqual(got, want) {
		t.Fatalf("collector.Diagnostics() = %#v, want %#v", got, want)
	}
}

func TestRenderEmbedFallsBackToPlainTextForUnpublishedBlockReferences(t *testing.T) {
	t.Parallel()

	current := &model.Note{
		Slug:    "current",
		RelPath: "notes/current.md",
		Embeds:  []model.EmbedRef{{Target: "Private", Fragment: "^block-ref", Line: 7}},
	}
	unpublished := &model.Note{Slug: "private/secret", RelPath: "private/secret.md"}
	idx := &model.VaultIndex{
		Notes: map[string]*model.Note{
			current.RelPath: current,
		},
		NoteBySlug: map[string]*model.Note{
			current.Slug: current,
		},
		NoteByName: map[string][]*model.Note{
			"current": {current},
		},
		AliasByName: map[string][]*model.Note{},
		Unpublished: model.UnpublishedLookup{
			Notes: map[string]*model.Note{
				unpublished.RelPath: unpublished,
			},
			NoteByName: map[string][]*model.Note{
				"private": {unpublished},
			},
			AliasByName: map[string][]*model.Note{},
		},
	}
	collector := diag.NewCollector()
	renderer := &wikilinkHTMLRenderer{
		index:       idx,
		currentNote: current,
		outputNote:  current,
		diag:        collector,
	}
	node := &gmwikilink.Node{Target: []byte("Private"), Fragment: []byte("^block-ref"), Embed: true}

	var raw bytes.Buffer
	buf := bufio.NewWriter(&raw)
	status, err := renderer.renderEmbed(buf, nil, node)
	if err != nil {
		t.Fatalf("renderEmbed() error = %v", err)
	}
	if err := buf.Flush(); err != nil {
		t.Fatalf("buf.Flush() error = %v", err)
	}
	if status != gast.WalkSkipChildren {
		t.Fatalf("renderEmbed() status = %v, want %v", status, gast.WalkSkipChildren)
	}

	html := raw.String()
	if !strings.Contains(html, `Private#^block-ref`) {
		t.Fatalf("HTML = %q, want raw block reference text preserved", html)
	}
	for _, forbidden := range []string{`href=`, `open note`, `private/secret`} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("HTML = %q, want %q omitted for unpublished block-reference fallback", html, forbidden)
		}
	}

	want := []diag.Diagnostic{{
		Severity: diag.SeverityWarning,
		Kind:     diag.KindUnsupportedSyntax,
		Location: diag.Location{Path: current.RelPath, Line: 7},
		Message:  `embed "Private#^block-ref" block reference embeds are not supported; rendering as plain text`,
	}}
	if got := collector.Diagnostics(); !reflect.DeepEqual(got, want) {
		t.Fatalf("collector.Diagnostics() = %#v, want %#v", got, want)
	}
}
