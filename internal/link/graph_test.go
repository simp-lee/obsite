package link

import (
	"bytes"
	"path"
	"reflect"
	"strings"
	"testing"

	"github.com/simp-lee/obsite/internal/diag"
	internalmarkdown "github.com/simp-lee/obsite/internal/markdown"
	"github.com/simp-lee/obsite/internal/model"
)

func TestBuildGraphBuildsForwardAndBackwardMaps(t *testing.T) {
	t.Parallel()

	current := testNote("notes/current.md", "notes/current")
	guide := testNote("notes/guide.md", "notes/guide")
	reference := testNote(
		"docs/reference.md",
		"docs/reference",
		withHeadings(model.Heading{Level: 2, Text: "Intro", ID: "intro"}),
	)
	entry := testNote("posts/entry.md", "posts/entry")

	graph := BuildGraph(buildIndex([]*model.Note{current, guide, reference, entry}, nil), map[string][]model.LinkRef{
		current.RelPath: {
			{RawTarget: "Guide", ResolvedRelPath: guide.RelPath, Line: 3},
			{RawTarget: "Reference#Intro", ResolvedRelPath: reference.RelPath, Fragment: "Intro", Line: 4},
		},
		entry.RelPath: {
			{RawTarget: "Guide", ResolvedRelPath: guide.RelPath, Line: 8},
		},
	})

	wantForward := map[string][]string{
		current.RelPath:   {reference.RelPath, guide.RelPath},
		guide.RelPath:     {},
		entry.RelPath:     {guide.RelPath},
		reference.RelPath: {},
	}
	if !reflect.DeepEqual(graph.Forward, wantForward) {
		t.Fatalf("graph.Forward = %#v, want %#v", graph.Forward, wantForward)
	}

	wantBackward := map[string][]string{
		current.RelPath:   {},
		guide.RelPath:     {current.RelPath, entry.RelPath},
		entry.RelPath:     {},
		reference.RelPath: {current.RelPath},
	}
	if !reflect.DeepEqual(graph.Backward, wantBackward) {
		t.Fatalf("graph.Backward = %#v, want %#v", graph.Backward, wantBackward)
	}
}

func TestBuildGraphUsesResolvedRenderOutLinks(t *testing.T) {
	t.Parallel()

	current := testNote("notes/current.md", "notes/current")
	exact := testNote("projects/deep/guide.md", "projects/deep/guide")
	near := testNote("notes/guide.md", "notes/guide")
	alias := testNote("docs/handbook.md", "docs/handbook", withAliases("Team Docs"))

	graph := BuildGraph(buildIndex([]*model.Note{current, exact, near, alias}, nil), map[string][]model.LinkRef{
		current.RelPath: {
			{RawTarget: "projects/deep/guide", ResolvedRelPath: exact.RelPath, Line: 3},
			{RawTarget: "Team Docs", ResolvedRelPath: alias.RelPath, Line: 4},
			{RawTarget: "Guide", ResolvedRelPath: near.RelPath, Line: 5},
		},
	})

	wantTargets := []string{alias.RelPath, near.RelPath, exact.RelPath}
	if !reflect.DeepEqual(graph.Forward[current.RelPath], wantTargets) {
		t.Fatalf("graph.Forward[%q] = %#v, want %#v", current.RelPath, graph.Forward[current.RelPath], wantTargets)
	}
	if got := graph.Backward[alias.RelPath]; !reflect.DeepEqual(got, []string{current.RelPath}) {
		t.Fatalf("graph.Backward[%q] = %#v, want host backlink from resolved render outlink", alias.RelPath, got)
	}
}

func TestBuildGraphDeduplicatesOutgoingLinksPerTarget(t *testing.T) {
	t.Parallel()

	current := testNote("notes/current.md", "notes/current")
	target := testNote(
		"guides/guide.md",
		"guides/guide",
		withHeadings(model.Heading{Level: 2, Text: "Section Title", ID: "section-title"}),
	)

	graph := BuildGraph(buildIndex([]*model.Note{current, target}, nil), map[string][]model.LinkRef{
		current.RelPath: {
			{RawTarget: "Guide", ResolvedRelPath: target.RelPath, Line: 3},
			{RawTarget: "Guide", ResolvedRelPath: target.RelPath, Line: 4},
			{RawTarget: "Guide#Section Title", ResolvedRelPath: target.RelPath, Fragment: "Section Title", Line: 5},
		},
	})

	if got := graph.Forward[current.RelPath]; !reflect.DeepEqual(got, []string{target.RelPath}) {
		t.Fatalf("graph.Forward[%q] = %#v, want one deduplicated target", current.RelPath, got)
	}
	if got := graph.Backward[target.RelPath]; !reflect.DeepEqual(got, []string{current.RelPath}) {
		t.Fatalf("graph.Backward[%q] = %#v, want one deduplicated source", target.RelPath, got)
	}
}

func TestBuildGraphIgnoresUnresolvedRenderOutLinks(t *testing.T) {
	t.Parallel()

	current := testNote("notes/current.md", "notes/current")
	target := testNote("guides/guide.md", "guides/guide")

	graph := BuildGraph(buildIndex([]*model.Note{current, target}, nil), map[string][]model.LinkRef{
		current.RelPath: {
			{RawTarget: "Missing", Line: 12},
			{RawTarget: "Guide#Missing Heading", Fragment: "Missing Heading", Line: 14},
			{RawTarget: "Guide", ResolvedRelPath: target.RelPath, Line: 16},
		},
	})

	if got := graph.Forward[current.RelPath]; !reflect.DeepEqual(got, []string{target.RelPath}) {
		t.Fatalf("graph.Forward[%q] = %#v, want only resolved edges", current.RelPath, got)
	}
}

// AC-R035: FR-9、FR-17 graph 层不得重复产出 resolver/render 已经负责的 deadlink、ambiguous、unpublished 诊断
func TestBuildGraphDoesNotDuplicateResolutionDiagnostics(t *testing.T) {
	t.Parallel()

	// REG-035
	// Ledger Key: link.graph / duplicates-resolution-diagnostics-owned-by-render
	current := testNote("notes/current.md", "notes/current")
	current.OutLinks = []model.LinkRef{
		{RawTarget: "Missing", Line: 1},
		{RawTarget: "Docs", Line: 2},
		{RawTarget: "Private", Line: 3},
	}
	alpha := testNote("alpha/docs.md", "alpha/docs", withAliases("Docs"))
	beta := testNote("beta/docs.md", "beta/docs", withAliases("Docs"))
	private := testNote("private/secret.md", "private/secret", withAliases("Private"))

	idx := buildIndex([]*model.Note{current, alpha, beta}, []*model.Note{private})
	collector := diag.NewCollector()
	md, renderResult := internalmarkdown.NewMarkdown(idx, current, nil, collector)

	var buf bytes.Buffer
	if err := md.Convert([]byte("[[Missing]]\n[[Docs]]\n[[Private]]\n"), &buf); err != nil {
		t.Fatalf("Convert() error = %v", err)
	}

	gotOutLinks := renderResult.OutLinks()
	if len(gotOutLinks) != 3 {
		t.Fatalf("len(renderResult.OutLinks()) = %d, want 3", len(gotOutLinks))
	}
	if gotOutLinks[0].ResolvedRelPath != "" {
		t.Fatalf("renderResult.OutLinks()[0].ResolvedRelPath = %q, want empty for deadlink", gotOutLinks[0].ResolvedRelPath)
	}
	if gotOutLinks[1].ResolvedRelPath != alpha.RelPath {
		t.Fatalf("renderResult.OutLinks()[1].ResolvedRelPath = %q, want %q", gotOutLinks[1].ResolvedRelPath, alpha.RelPath)
	}
	if gotOutLinks[2].ResolvedRelPath != "" {
		t.Fatalf("renderResult.OutLinks()[2].ResolvedRelPath = %q, want empty for unpublished link", gotOutLinks[2].ResolvedRelPath)
	}

	wantDiagnostics := []diag.Diagnostic{
		{
			Severity: diag.SeverityWarning,
			Kind:     diag.KindDeadLink,
			Location: diag.Location{Path: current.RelPath, Line: 1},
			Message:  `wikilink "Missing" could not be resolved`,
		},
		{
			Severity: diag.SeverityWarning,
			Kind:     diag.Kind("ambiguous_wikilink"),
			Location: diag.Location{Path: current.RelPath, Line: 2},
			Message:  `wikilink "Docs" matched multiple notes at the same path distance (alpha/docs.md, beta/docs.md); choosing "alpha/docs.md"`,
		},
		{
			Severity: diag.SeverityWarning,
			Kind:     diag.Kind("unpublished_wikilink"),
			Location: diag.Location{Path: current.RelPath, Line: 3},
			Message:  `wikilink "Private" points to unpublished note "private/secret.md"; rendering as plain text`,
		},
	}
	if got := collector.Diagnostics(); !reflect.DeepEqual(got, wantDiagnostics) {
		t.Fatalf("collector.Diagnostics() = %#v, want %#v", got, wantDiagnostics)
	}

	graph := BuildGraph(idx, map[string][]model.LinkRef{current.RelPath: gotOutLinks})
	if got := collector.Diagnostics(); !reflect.DeepEqual(got, wantDiagnostics) {
		t.Fatalf("collector.Diagnostics() after BuildGraph = %#v, want %#v", got, wantDiagnostics)
	}
	if got := graph.Forward[current.RelPath]; !reflect.DeepEqual(got, []string{alpha.RelPath}) {
		t.Fatalf("graph.Forward[%q] = %#v, want only the resolved ambiguous target", current.RelPath, got)
	}
	if got := graph.Backward[alpha.RelPath]; !reflect.DeepEqual(got, []string{current.RelPath}) {
		t.Fatalf("graph.Backward[%q] = %#v, want backlink from current note", alpha.RelPath, got)
	}
	if got := graph.Backward[beta.RelPath]; !reflect.DeepEqual(got, []string{}) {
		t.Fatalf("graph.Backward[%q] = %#v, want no backlink for unchosen ambiguous target", beta.RelPath, got)
	}
}

func TestBuildGraphIgnoresTargetsOutsidePublicGraph(t *testing.T) {
	t.Parallel()

	current := testNote("notes/current.md", "notes/current")
	public := testNote("notes/public.md", "notes/public")
	private := testNote("private/secret.md", "private/secret", withAliases("Private"))

	graph := BuildGraph(buildIndex([]*model.Note{current, public}, []*model.Note{private}), map[string][]model.LinkRef{
		current.RelPath: {
			{RawTarget: "Private", ResolvedRelPath: private.RelPath, Line: 9},
			{RawTarget: "Public", ResolvedRelPath: public.RelPath, Line: 10},
		},
	})

	if got := graph.Forward[current.RelPath]; !reflect.DeepEqual(got, []string{public.RelPath}) {
		t.Fatalf("graph.Forward[%q] = %#v, want only public targets retained", current.RelPath, got)
	}
	if _, ok := graph.Backward[private.RelPath]; ok {
		t.Fatalf("graph.Backward unexpectedly contains unpublished target %q", private.RelPath)
	}
	if got := graph.Backward[public.RelPath]; !reflect.DeepEqual(got, []string{current.RelPath}) {
		t.Fatalf("graph.Backward[%q] = %#v, want backlink from public target only", public.RelPath, got)
	}
}

func TestBuildGraphSkipsSelfLinksInForwardAndBackwardMaps(t *testing.T) {
	t.Parallel()

	current := testNote(
		"notes/current.md",
		"notes/current",
		withHeadings(model.Heading{Level: 2, Text: "Intro", ID: "intro"}),
	)

	graph := BuildGraph(buildIndex([]*model.Note{current}, nil), map[string][]model.LinkRef{
		current.RelPath: {
			{RawTarget: "#Intro", ResolvedRelPath: current.RelPath, Fragment: "Intro", Line: 7},
		},
	})

	if got := graph.Forward[current.RelPath]; !reflect.DeepEqual(got, []string{}) {
		t.Fatalf("graph.Forward[%q] = %#v, want self-link excluded from note graph", current.RelPath, got)
	}
	if got := graph.Backward[current.RelPath]; !reflect.DeepEqual(got, []string{}) {
		t.Fatalf("graph.Backward[%q] = %#v, want self backlink excluded", current.RelPath, got)
	}
}

func TestBuildGraphIncludesEmbedMergedHostLinks(t *testing.T) {
	t.Parallel()

	host := testNote("notes/host.md", "notes/host")
	guide := testNote("notes/guide.md", "notes/guide")
	reference := testNote("docs/reference.md", "docs/reference")

	graph := BuildGraph(buildIndex([]*model.Note{host, guide, reference}, nil), map[string][]model.LinkRef{
		host.RelPath: {
			{RawTarget: "Guide", ResolvedRelPath: guide.RelPath, Line: 5},
			{RawTarget: "Reference", ResolvedRelPath: reference.RelPath, Line: 8},
		},
	})

	if got := graph.Forward[host.RelPath]; !reflect.DeepEqual(got, []string{reference.RelPath, guide.RelPath}) {
		t.Fatalf("graph.Forward[%q] = %#v, want embedded links merged onto host page", host.RelPath, got)
	}
	if got := graph.Backward[reference.RelPath]; !reflect.DeepEqual(got, []string{host.RelPath}) {
		t.Fatalf("graph.Backward[%q] = %#v, want host backlink for embedded outlink", reference.RelPath, got)
	}
}

type noteOption func(*model.Note)

func testNote(relPath string, slug string, options ...noteOption) *model.Note {
	note := &model.Note{RelPath: relPath, Slug: slug}
	for _, option := range options {
		option(note)
	}
	return note
}

func withAliases(aliases ...string) noteOption {
	return func(note *model.Note) {
		note.Aliases = append([]string(nil), aliases...)
	}
}

func withHeadings(headings ...model.Heading) noteOption {
	return func(note *model.Note) {
		note.Headings = append([]model.Heading(nil), headings...)
	}
}

func buildIndex(public []*model.Note, unpublished []*model.Note) *model.VaultIndex {
	idx := &model.VaultIndex{
		Notes:       make(map[string]*model.Note, len(public)),
		NoteBySlug:  make(map[string]*model.Note, len(public)),
		NoteByName:  make(map[string][]*model.Note),
		AliasByName: make(map[string][]*model.Note),
		Unpublished: model.UnpublishedLookup{
			Notes:       make(map[string]*model.Note, len(unpublished)),
			NoteByName:  make(map[string][]*model.Note),
			AliasByName: make(map[string][]*model.Note),
		},
	}

	for _, note := range public {
		if note == nil {
			continue
		}
		idx.Notes[note.RelPath] = note
		idx.NoteBySlug[note.Slug] = note
		idx.NoteByName[noteLookupKey(note.RelPath)] = append(idx.NoteByName[noteLookupKey(note.RelPath)], note)
		for _, alias := range note.Aliases {
			idx.AliasByName[aliasLookupKey(alias)] = append(idx.AliasByName[aliasLookupKey(alias)], note)
		}
	}

	for _, note := range unpublished {
		if note == nil {
			continue
		}
		idx.Unpublished.Notes[note.RelPath] = note
		idx.Unpublished.NoteByName[noteLookupKey(note.RelPath)] = append(idx.Unpublished.NoteByName[noteLookupKey(note.RelPath)], note)
		for _, alias := range note.Aliases {
			idx.Unpublished.AliasByName[aliasLookupKey(alias)] = append(idx.Unpublished.AliasByName[aliasLookupKey(alias)], note)
		}
	}

	return idx
}

func noteLookupKey(relPath string) string {
	base := path.Base(strings.TrimSpace(relPath))
	if strings.EqualFold(path.Ext(base), ".md") {
		base = base[:len(base)-len(path.Ext(base))]
	}
	return strings.ToLower(base)
}

func aliasLookupKey(alias string) string {
	return strings.ToLower(strings.TrimSpace(alias))
}
