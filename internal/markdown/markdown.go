package markdown

import (
	"io"
	"strconv"
	"strings"
	"sync"

	figure "github.com/mangoumbrella/goldmark-figure"
	"github.com/simp-lee/obsite/internal/diag"
	"github.com/simp-lee/obsite/internal/markdown/callout"
	internalembed "github.com/simp-lee/obsite/internal/markdown/embed"
	internalhighlight "github.com/simp-lee/obsite/internal/markdown/highlight"
	"github.com/simp-lee/obsite/internal/markdown/math"
	internalwikilink "github.com/simp-lee/obsite/internal/markdown/wikilink"
	"github.com/simp-lee/obsite/internal/model"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/util"
	gmhashtag "go.abhg.dev/goldmark/hashtag"
	gmwikilink "go.abhg.dev/goldmark/wikilink"
)

// AssetSink is the render-time dependency boundary for discovered assets.
// Implementations return the site-relative output path for the registered asset.
type AssetSink interface {
	Register(vaultRelPath string) string
}

// RenderResult exposes the render-local note state produced during pass 2.
type RenderResult struct {
	note     *model.Note
	resolver *internalwikilink.VaultResolver
	embedded []model.LinkRef
}

// HasMath reports whether the rendered page content contains math.
func (r *RenderResult) HasMath() bool {
	return r != nil && r.note != nil && r.note.HasMath
}

// HasMermaid reports whether the rendered page content contains Mermaid blocks.
func (r *RenderResult) HasMermaid() bool {
	return r != nil && r.note != nil && r.note.HasMermaid
}

// OutLinks returns the render-local resolved outlinks for the source note.
func (r *RenderResult) OutLinks() []model.LinkRef {
	if r == nil {
		return nil
	}

	base := []model.LinkRef(nil)
	if r.resolver != nil {
		base = r.resolver.OutLinks()
	}
	if len(r.embedded) == 0 {
		return base
	}

	merged := make([]model.LinkRef, 0, len(base)+len(r.embedded))
	merged = append(merged, base...)
	merged = append(merged, r.embedded...)
	return merged
}

func (r *RenderResult) appendEmbeddedOutLinks(links []model.LinkRef) {
	if r == nil || len(links) == 0 {
		return
	}

	r.embedded = append(r.embedded, links...)
}

var (
	sharedParserOnce sync.Once
	sharedParser     goldmark.Markdown
)

// NewParser returns the shared AST-only goldmark instance used by pass 1.
func NewParser(_ *diag.Collector) goldmark.Markdown {
	sharedParserOnce.Do(func() {
		sharedParser = goldmark.New(
			goldmark.WithParserOptions(
				newParserOptions(nil, "")...,
			),
			goldmark.WithExtensions(newCoreExtensions(noopWikilinkResolver{}, nil)...),
		)
	})

	return sharedParser
}

// NewMarkdown returns a per-note goldmark instance plus its render-local result state.
func NewMarkdown(idx *model.VaultIndex, note *model.Note, assetSink AssetSink, diagCollector *diag.Collector) (goldmark.Markdown, *RenderResult) {
	imageCount := new(int)
	headingPrefixCount := new(int)
	visited := make(map[string]struct{}, 1)
	renderNote := prepareRenderNote(cloneRenderNote(note))
	if renderNote != nil && renderNote.RelPath != "" {
		visited[renderNote.RelPath] = struct{}{}
	}
	result := &RenderResult{}

	return newMarkdownWithState(idx, renderNote, renderNote, assetSink, diagCollector, imageCount, headingPrefixCount, "", visited, 0, result), result
}

func newMarkdownWithState(
	idx *model.VaultIndex,
	sourceNote *model.Note,
	outputNote *model.Note,
	assetSink AssetSink,
	diagCollector *diag.Collector,
	imageCount *int,
	headingPrefixCount *int,
	headingIDPrefix string,
	visited map[string]struct{},
	depth int,
	result *RenderResult,
) goldmark.Markdown {
	if outputNote == nil {
		outputNote = sourceNote
	}

	resolver, resolverState := newRenderWikilinkResolver(idx, sourceNote, outputNote, diagCollector, headingIDPrefix)
	if result != nil {
		result.note = outputNote
		result.resolver = resolverState
	}
	hashtagResolver := newRenderHashtagResolver(idx, outputNote)
	extensions := append(
		newCoreExtensions(resolver, hashtagResolver),
		figure.Figure,
		newMathTrackingExtender(sourceNote),
		newCodeBlockExtender(sourceNote, diagCollector),
		newRawHTMLExtender(),
		newImageExtender(sourceNote, outputNote, idx, assetSink, imageCount),
	)

	var md goldmark.Markdown
	extensions = append(extensions, internalembed.New(
		idx,
		sourceNote,
		outputNote,
		diagCollector,
		assetSink,
		resolver,
		func(source []byte, writer io.Writer) error {
			return md.Convert(source, writer)
		},
		func(target *model.Note, source []byte, writer io.Writer, childVisited map[string]struct{}, childDepth int) error {
			childNote := prepareRenderNote(cloneRenderNote(target))
			childResult := &RenderResult{}
			if err := newMarkdownWithState(
				idx,
				childNote,
				outputNote,
				assetSink,
				diagCollector,
				imageCount,
				headingPrefixCount,
				nextEmbeddedHeadingIDPrefix(headingPrefixCount),
				childVisited,
				childDepth,
				childResult,
			).Convert(source, writer); err != nil {
				return err
			}

			if result != nil {
				result.appendEmbeddedOutLinks(rewriteEmbeddedOutLinks(childNote, outputNote, childResult.OutLinks()))
				if childNote != nil && strings.TrimSpace(childNote.RelPath) != "" {
					result.appendEmbeddedOutLinks([]model.LinkRef{{ResolvedRelPath: childNote.RelPath}})
				}
			}

			if outputNote != nil && childNote != nil {
				outputNote.HasMath = outputNote.HasMath || childNote.HasMath
				outputNote.HasMermaid = outputNote.HasMermaid || childNote.HasMermaid
			}

			return nil
		},
		imageCount,
		headingIDPrefix,
		visited,
		depth,
	))

	md = goldmark.New(
		goldmark.WithParserOptions(
			newParserOptions(sourceNote, headingIDPrefix)...,
		),
		goldmark.WithExtensions(extensions...),
	)

	return md
}

func newParserOptions(note *model.Note, headingIDPrefix string) []parser.Option {
	return []parser.Option{
		parser.WithASTTransformers(
			util.Prioritized(newVisibleHeadingIDTransformer(note, headingIDPrefix), 1000),
		),
	}
}

func newCoreExtensions(wikilinkResolver gmwikilink.Resolver, hashtagResolver gmhashtag.Resolver) []goldmark.Extender {
	return []goldmark.Extender{
		extension.GFM,
		extension.Footnote,
		&gmhashtag.Extender{Variant: gmhashtag.ObsidianVariant, Resolver: hashtagResolver},
		&gmwikilink.Extender{Resolver: wikilinkResolver},
		callout.New(),
		internalhighlight.New(),
		math.New(),
	}
}

func newRenderWikilinkResolver(idx *model.VaultIndex, sourceNote *model.Note, outputNote *model.Note, diagCollector *diag.Collector, headingIDPrefix string) (gmwikilink.Resolver, *internalwikilink.VaultResolver) {
	delegate := internalwikilink.NewRenderVaultResolver(idx, sourceNote, outputNote, headingIDPrefix, diagCollector)
	return renderWikilinkResolver{delegate: delegate}, delegate
}

type renderWikilinkResolver struct {
	delegate gmwikilink.Resolver
}

func (r renderWikilinkResolver) ResolveWikilink(node *gmwikilink.Node) ([]byte, error) {
	if node == nil || node.Embed || r.delegate == nil {
		return nil, nil
	}

	return r.delegate.ResolveWikilink(node)
}

type noopWikilinkResolver struct{}

func (noopWikilinkResolver) ResolveWikilink(*gmwikilink.Node) ([]byte, error) {
	return nil, nil
}

func cloneRenderNote(note *model.Note) *model.Note {
	if note == nil {
		return nil
	}

	cloned := *note
	cloned.Aliases = append([]string(nil), note.Aliases...)
	cloned.Tags = append([]string(nil), note.Tags...)
	cloned.Headings = append([]model.Heading(nil), note.Headings...)
	cloned.RawContent = append([]byte(nil), note.RawContent...)
	cloned.OutLinks = append([]model.LinkRef(nil), note.OutLinks...)
	cloned.Embeds = append([]model.EmbedRef(nil), note.Embeds...)

	if len(note.HeadingSections) > 0 {
		cloned.HeadingSections = make(map[string]model.SectionRange, len(note.HeadingSections))
		for id, section := range note.HeadingSections {
			cloned.HeadingSections[id] = section
		}
	}

	return &cloned
}

func prepareRenderNote(note *model.Note) *model.Note {
	if note == nil {
		return nil
	}

	note.HasMath = false
	note.HasMermaid = false
	return note
}

func nextEmbeddedHeadingIDPrefix(counter *int) string {
	if counter == nil {
		return "embed-"
	}

	(*counter)++
	return "embed-" + strconv.Itoa(*counter) + "-"
}

func rewriteEmbeddedOutLinks(sourceNote *model.Note, outputNote *model.Note, links []model.LinkRef) []model.LinkRef {
	if len(links) == 0 {
		return nil
	}

	rewritten := append([]model.LinkRef(nil), links...)
	if sourceNote == nil || sourceNote.RelPath == "" || outputNote == nil || outputNote.RelPath == "" {
		return rewritten
	}

	for i := range rewritten {
		if rewritten[i].ResolvedRelPath != sourceNote.RelPath {
			continue
		}
		if !linkRefHasFragment(rewritten[i]) {
			continue
		}
		rewritten[i].ResolvedRelPath = outputNote.RelPath
	}

	return rewritten
}

func linkRefHasFragment(ref model.LinkRef) bool {
	if strings.TrimSpace(ref.Fragment) != "" {
		return true
	}

	rawTarget := strings.TrimSpace(ref.RawTarget)
	return strings.Contains(rawTarget, "#")
}
