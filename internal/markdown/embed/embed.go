package embed

import (
	"bytes"
	"io"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	figureast "github.com/mangoumbrella/goldmark-figure/ast"
	"github.com/simp-lee/obsite/internal/diag"
	internalwikilink "github.com/simp-lee/obsite/internal/markdown/wikilink"
	"github.com/simp-lee/obsite/internal/model"
	"github.com/simp-lee/obsite/internal/resourcepath"
	"github.com/yuin/goldmark"
	gast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
	gmwikilink "go.abhg.dev/goldmark/wikilink"
)

const (
	maxDepth                       = 10
	kindAmbiguousEmbed   diag.Kind = "ambiguous_embed"
	kindUnpublishedEmbed diag.Kind = "unpublished_embed"
)

// AssetSink mirrors markdown.AssetSink without importing the parent package.
type AssetSink interface {
	Register(vaultRelPath string) string
}

// RenderEmbeddedFunc renders a note or section embed in a child note context.
type RenderEmbeddedFunc func(note *model.Note, source []byte, writer io.Writer, visited map[string]struct{}, depth int) error

type extender struct {
	renderer *wikilinkHTMLRenderer
}

// New installs embed-aware wikilink rendering for pass 2.
func New(
	idx *model.VaultIndex,
	note *model.Note,
	outputNote *model.Note,
	diagCollector *diag.Collector,
	assetSink AssetSink,
	fallbackResolver gmwikilink.Resolver,
	convert func([]byte, io.Writer) error,
	renderEmbedded RenderEmbeddedFunc,
	imageCount *int,
	headingIDPrefix string,
	visited map[string]struct{},
	depth int,
) goldmark.Extender {
	clonedVisited := cloneVisited(visited)
	if key := visitKey(note, ""); key != "" {
		clonedVisited[key] = struct{}{}
	}
	if outputNote == nil {
		outputNote = note
	}
	if fallbackResolver == nil {
		fallbackResolver = internalwikilink.NewRenderVaultResolver(idx, note, outputNote, headingIDPrefix, diagCollector)
	}

	return &extender{renderer: &wikilinkHTMLRenderer{
		fallback:         &gmwikilink.Renderer{Resolver: fallbackResolver},
		fallbackResolver: fallbackResolver,
		index:            idx,
		currentNote:      note,
		outputNote:       outputNote,
		diag:             diagCollector,
		assetSink:        assetSink,
		convert:          convert,
		renderEmbedded:   renderEmbedded,
		imageCount:       imageCount,
		visited:          clonedVisited,
		depth:            depth,
	}}
}

// MaxRenderDepth returns the embed recursion limit enforced during rendering.
func MaxRenderDepth() int {
	return maxDepth
}

// ScopeNoteToFragment returns the note view used to render a heading-scoped embed.
func ScopeNoteToFragment(note *model.Note, fragmentID string) *model.Note {
	return scopeNoteToSectionEmbeds(note, fragmentID)
}

func (e *extender) Extend(md goldmark.Markdown) {
	md.Parser().AddOptions(parser.WithParagraphTransformers(
		util.Prioritized(newImageEmbedFigureParagraphTransformer(e.renderer.currentNote, e.renderer.index), 119),
	))
	md.Renderer().AddOptions(renderer.WithNodeRenderers(
		util.Prioritized(e.renderer, 198),
	))
}

type imageEmbedFigureParagraphTransformer struct {
	currentNote *model.Note
	index       *model.VaultIndex
}

func newImageEmbedFigureParagraphTransformer(currentNote *model.Note, index *model.VaultIndex) parser.ParagraphTransformer {
	return &imageEmbedFigureParagraphTransformer{currentNote: currentNote, index: index}
}

func (t *imageEmbedFigureParagraphTransformer) Transform(node *gast.Paragraph, reader text.Reader, _ parser.Context) {
	lines := node.Lines()
	if lines.Len() == 0 {
		return
	}

	source := reader.Source()
	firstLine := lines.At(0)
	if !t.isImageEmbedFigureLine(firstLine.Value(source)) {
		return
	}

	parent := node.Parent()
	if parent == nil {
		return
	}

	figure := figureast.NewFigure()
	parent.ReplaceChild(parent, node, figure)

	currentLine := 0
	for currentLine < lines.Len() {
		segment := lines.At(currentLine)
		if !t.isImageEmbedFigureLine(segment.Value(source)) {
			break
		}

		figureImage := figureast.NewFigureImage()
		figureImage.Lines().Append(segment)
		figure.AppendChild(figure, figureImage)
		currentLine++
	}

	if currentLine >= lines.Len() {
		return
	}

	figureCaption := figureast.NewFigureCaption()
	for i := currentLine; i < lines.Len(); i++ {
		segment := lines.At(i)
		if i == lines.Len()-1 && segment.Stop > segment.Start && source[segment.Stop-1] == '\n' {
			segment.Stop--
		}
		figureCaption.Lines().Append(segment)
	}
	figure.AppendChild(figure, figureCaption)
}

func (t *imageEmbedFigureParagraphTransformer) isImageEmbedFigureLine(line []byte) bool {
	target, ok := parseImageEmbedFigureTarget(line)
	if !ok {
		return false
	}

	return resolveImageAssetPath(t.currentNote, t.index, target) != ""
}

type wikilinkHTMLRenderer struct {
	fallback         *gmwikilink.Renderer
	fallbackResolver gmwikilink.Resolver
	index            *model.VaultIndex
	currentNote      *model.Note
	outputNote       *model.Note
	diag             *diag.Collector
	assetSink        AssetSink
	convert          func([]byte, io.Writer) error
	renderEmbedded   RenderEmbeddedFunc
	imageCount       *int
	visited          map[string]struct{}
	depth            int
	nextEmbed        int
}

func (r *wikilinkHTMLRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(gmwikilink.Kind, r.renderWikilink)
}

func (r *wikilinkHTMLRenderer) renderWikilink(
	w util.BufWriter,
	source []byte,
	node gast.Node,
	entering bool,
) (gast.WalkStatus, error) {
	wikilinkNode, ok := node.(*gmwikilink.Node)
	if !ok {
		return gast.WalkStop, nil
	}

	if !wikilinkNode.Embed {
		if r.fallback == nil {
			return gast.WalkContinue, nil
		}
		return r.fallback.Render(w, source, node, entering)
	}

	if !entering {
		return gast.WalkContinue, nil
	}

	return r.renderEmbed(w, source, wikilinkNode)
}

func (r *wikilinkHTMLRenderer) renderEmbed(
	w util.BufWriter,
	source []byte,
	node *gmwikilink.Node,
) (gast.WalkStatus, error) {
	target := string(node.Target)
	rawTarget := composeRawTarget(target, string(node.Fragment))
	ref := r.consumeEmbed(rawTarget)
	fragment := strings.TrimSpace(string(node.Fragment))

	if strings.HasPrefix(fragment, "^") {
		lookup := internalwikilink.LookupTarget(r.index, r.currentNote, target, "")
		if len(lookup.Ambiguous) > 1 {
			r.recordAmbiguous(ref, rawTarget, lookup.Note, lookup.Ambiguous)
		}
		fallback := "plain text with a link"
		targetNote := lookup.Note
		if lookup.Unpublished {
			fallback = "plain text"
			targetNote = nil
		}
		r.recordUnsupportedWithFallback(ref, rawTarget, "block reference embeds are not supported", fallback)
		r.renderBlockReferenceFallback(w, ref, rawTarget, targetNote)
		return gast.WalkSkipChildren, nil
	}

	if assetPath := r.resolveImageAssetPath(target); assetPath != "" {
		r.renderImageEmbed(w, source, node, assetPath)
		return gast.WalkSkipChildren, nil
	}

	lookup := internalwikilink.LookupTarget(r.index, r.currentNote, target, fragment)
	if len(lookup.Ambiguous) > 1 {
		r.recordAmbiguous(ref, rawTarget, lookup.Note, lookup.Ambiguous)
	}

	switch {
	case lookup.Note == nil:
		if lookup.CanvasResource {
			if len(lookup.Ambiguous) > 0 {
				r.recordAmbiguousCanvas(ref, rawTarget, lookup.Ambiguous)
			} else {
				r.recordUnsupportedCanvas(ref, rawTarget)
			}
			return r.renderPlainTextEmbedFallback(w, ref, rawTarget)
		}
		if looksLikeImageTarget(target) || (ref != nil && ref.IsImage) {
			r.recordUnresolvedAsset(ref, rawTarget)
			r.renderEmbedFallbackText(w, ref, rawTarget)
			return gast.WalkSkipChildren, nil
		}
		r.recordDeadEmbed(ref, rawTarget)
		return r.renderPlainTextEmbedFallback(w, ref, rawTarget)
	case lookup.Unpublished:
		r.recordUnpublished(ref, rawTarget, lookup.Note)
		return r.renderPlainTextEmbedFallback(w, ref, rawTarget)
	case lookup.MissingFragment:
		r.recordMissingFragment(ref, rawTarget, lookup.Note, fragment)
		return r.renderPlainTextEmbedFallback(w, ref, rawTarget)
	case r.depth >= maxDepth:
		r.recordUnsupported(ref, rawTarget, "maximum embed depth of 10 exceeded")
		return r.renderPlainTextEmbedFallback(w, ref, rawTarget)
	case r.isVisited(lookup.Note, lookup.FragmentID):
		r.recordCycle(ref, rawTarget, lookup.Note, lookup.FragmentID)
		return r.renderPlainTextEmbedFallback(w, ref, rawTarget)
	default:
		embeddedSource := selectEmbedSource(lookup.Note, lookup.FragmentID)
		if len(embeddedSource) == 0 {
			r.recordMissingFragment(ref, rawTarget, lookup.Note, fragment)
			return r.renderPlainTextEmbedFallback(w, ref, rawTarget)
		}
		renderNote := scopeNoteToSectionEmbeds(lookup.Note, lookup.FragmentID)

		childVisited := cloneVisited(r.visited)
		if key := visitKey(lookup.Note, lookup.FragmentID); key != "" {
			childVisited[key] = struct{}{}
		}

		if r.renderEmbedded != nil {
			if err := r.renderEmbedded(renderNote, embeddedSource, w, childVisited, r.depth+1); err != nil {
				return gast.WalkStop, err
			}
			return gast.WalkSkipChildren, nil
		}
		if r.convert != nil {
			if err := r.convert(embeddedSource, w); err != nil {
				return gast.WalkStop, err
			}
			return gast.WalkSkipChildren, nil
		}

		return gast.WalkContinue, nil
	}
}

func (r *wikilinkHTMLRenderer) renderPlainTextEmbedFallback(w util.BufWriter, ref *model.EmbedRef, rawTarget string) (gast.WalkStatus, error) {
	r.renderEmbedFallbackText(w, ref, rawTarget)
	return gast.WalkSkipChildren, nil
}

func (r *wikilinkHTMLRenderer) renderImageEmbed(
	w util.BufWriter,
	source []byte,
	node *gmwikilink.Node,
	assetPath string,
) {
	siteRelPath := assetPath
	if r.assetSink != nil {
		if registered := normalizeSitePath(r.assetSink.Register(assetPath)); registered != "" {
			siteRelPath = registered
		}
	}

	imageIndex := r.nextImageIndex()
	label := normalizeInlineText(wikilinkNodeText(source, node))
	width := imageWidth(label)
	alt := embedAltText(label, composeRawTarget(string(node.Target), string(node.Fragment)), assetPath)

	_, _ = w.WriteString(`<img src="`)
	_, _ = w.Write(util.EscapeHTML(util.URLEscape([]byte(relativeToNoteOutput(r.outputNote, siteRelPath)), true)))
	_, _ = w.WriteString(`" alt="`)
	_, _ = w.Write(util.EscapeHTML([]byte(alt)))
	_ = w.WriteByte('"')

	if width > 0 {
		_, _ = w.WriteString(` width="`)
		_, _ = w.WriteString(strconv.Itoa(width))
		_ = w.WriteByte('"')
	}
	if imageIndex > 1 {
		_, _ = w.WriteString(` loading="lazy"`)
	}

	_, _ = w.WriteString(`>`)
}

func (r *wikilinkHTMLRenderer) renderEmbedFallbackText(w util.BufWriter, ref *model.EmbedRef, rawTarget string) {
	fallback := strings.TrimSpace(rawTarget)
	if fallback == "" && ref != nil {
		fallback = strings.TrimSpace(composeRawTarget(ref.Target, ref.Fragment))
	}
	if fallback == "" {
		return
	}

	_, _ = w.Write(util.EscapeHTML([]byte(fallback)))
}

func (r *wikilinkHTMLRenderer) renderBlockReferenceFallback(w util.BufWriter, ref *model.EmbedRef, rawTarget string, targetNote *model.Note) {
	r.renderEmbedFallbackText(w, ref, rawTarget)
	if targetNote == nil {
		return
	}

	href := internalwikilink.BuildNoteHref(r.outputNote, r.currentNote, targetNote, "", "")
	if strings.TrimSpace(href) == "" {
		return
	}

	_, _ = w.WriteString(` (<a href="`)
	_, _ = w.Write(util.EscapeHTML(util.URLEscape([]byte(href), true)))
	_, _ = w.WriteString(`">open note</a>)`)
}

func (r *wikilinkHTMLRenderer) nextImageIndex() int {
	if r.imageCount == nil {
		return 1
	}

	(*r.imageCount)++
	return *r.imageCount
}

func (r *wikilinkHTMLRenderer) consumeEmbed(rawTarget string) *model.EmbedRef {
	if r == nil || r.currentNote == nil {
		return nil
	}

	normalized := strings.TrimSpace(rawTarget)
	for i := r.nextEmbed; i < len(r.currentNote.Embeds); i++ {
		candidate := composeRawTarget(r.currentNote.Embeds[i].Target, r.currentNote.Embeds[i].Fragment)
		if !strings.EqualFold(strings.TrimSpace(candidate), normalized) {
			continue
		}

		r.nextEmbed = i + 1
		return &r.currentNote.Embeds[i]
	}

	return nil
}

func (r *wikilinkHTMLRenderer) resolveImageAssetPath(target string) string {
	return resolveImageAssetPath(r.currentNote, r.index, target)
}

func resolveImageAssetPath(note *model.Note, idx *model.VaultIndex, target string) string {
	return resourcepath.ResolveIndexedAssetPath(note, idx, target)
}

func (r *wikilinkHTMLRenderer) isVisited(note *model.Note, fragmentID string) bool {
	key := visitKey(note, fragmentID)
	if key == "" {
		return false
	}
	_, ok := r.visited[key]
	return ok
}

func (r *wikilinkHTMLRenderer) recordDeadEmbed(ref *model.EmbedRef, rawTarget string) {
	if r == nil || r.diag == nil {
		return
	}

	r.diag.Warningf(diag.KindDeadLink, r.location(ref), "note embed %q could not be resolved; rendering as plain text", rawTarget)
}

func (r *wikilinkHTMLRenderer) recordUnresolvedAsset(ref *model.EmbedRef, rawTarget string) {
	if r == nil || r.diag == nil {
		return
	}

	r.diag.Warningf(diag.KindUnresolvedAsset, r.location(ref), "image embed %q could not be resolved to a vault asset; rendering as plain text", rawTarget)
}

func (r *wikilinkHTMLRenderer) recordUnpublished(ref *model.EmbedRef, rawTarget string, note *model.Note) {
	if r == nil || r.diag == nil || note == nil {
		return
	}

	r.diag.Warningf(kindUnpublishedEmbed, r.location(ref), "note embed %q points to unpublished note %q; rendering as plain text", rawTarget, note.RelPath)
}

func (r *wikilinkHTMLRenderer) recordMissingFragment(ref *model.EmbedRef, rawTarget string, note *model.Note, fragment string) {
	if r == nil || r.diag == nil || note == nil {
		return
	}

	missing := normalizeInlineText(fragment)
	r.diag.Warningf(diag.KindDeadLink, r.location(ref), "note embed %q points to missing heading %q in %q; rendering as plain text", rawTarget, missing, note.RelPath)
}

func (r *wikilinkHTMLRenderer) recordAmbiguous(ref *model.EmbedRef, rawTarget string, chosen *model.Note, candidates []string) {
	if r == nil || r.diag == nil || chosen == nil || len(candidates) == 0 {
		return
	}

	r.diag.Warningf(kindAmbiguousEmbed, r.location(ref), "note embed %q matched multiple notes at the same path distance (%s); choosing %q", rawTarget, strings.Join(candidates, ", "), chosen.RelPath)
}

func (r *wikilinkHTMLRenderer) recordCycle(ref *model.EmbedRef, rawTarget string, note *model.Note, fragmentID string) {
	if r == nil || r.diag == nil || note == nil {
		return
	}

	cycle := make([]string, 0, len(r.visited)+1)
	for key := range r.visited {
		cycle = append(cycle, key)
	}
	if key := visitKey(note, fragmentID); key != "" {
		cycle = append(cycle, key)
	}
	sort.Strings(cycle)

	r.diag.Warningf(diag.KindUnsupportedSyntax, r.location(ref), "note embed %q would create a transclusion cycle (%s); rendering as plain text", rawTarget, strings.Join(cycle, " -> "))
}

func (r *wikilinkHTMLRenderer) recordUnsupported(ref *model.EmbedRef, rawTarget string, message string) {
	r.recordUnsupportedWithFallback(ref, rawTarget, message, "plain text")
}

func (r *wikilinkHTMLRenderer) recordUnsupportedCanvas(ref *model.EmbedRef, rawTarget string) {
	r.recordUnsupportedWithFallback(ref, rawTarget, "targets unsupported canvas content", "plain text")
}

func (r *wikilinkHTMLRenderer) recordAmbiguousCanvas(ref *model.EmbedRef, rawTarget string, candidates []string) {
	if r == nil || r.diag == nil || len(candidates) == 0 {
		return
	}

	r.diag.Warningf(diag.KindUnsupportedSyntax, r.location(ref), "embed %q matched multiple canvas resources after canonical lookup (%s); refusing canonical fallback and rendering as plain text", rawTarget, strings.Join(candidates, ", "))
}

func (r *wikilinkHTMLRenderer) recordUnsupportedWithFallback(ref *model.EmbedRef, rawTarget string, message string, fallback string) {
	if r == nil || r.diag == nil {
		return
	}

	r.diag.Warningf(diag.KindUnsupportedSyntax, r.location(ref), "embed %q %s; rendering as %s", rawTarget, message, fallback)
}

func (r *wikilinkHTMLRenderer) location(ref *model.EmbedRef) diag.Location {
	location := diag.Location{}
	if r != nil && r.currentNote != nil {
		location.Path = r.currentNote.RelPath
	}
	if ref != nil {
		location.Line = ref.Line
	}
	return location
}

func selectEmbedSource(note *model.Note, fragmentID string) []byte {
	if note == nil {
		return nil
	}
	if fragmentID == "" {
		return note.RawContent
	}

	section, ok := note.HeadingSections[fragmentID]
	if !ok {
		return nil
	}

	start := section.StartOffset
	end := section.EndOffset
	if start < 0 {
		start = 0
	}
	if end > len(note.RawContent) {
		end = len(note.RawContent)
	}
	if end < start {
		end = start
	}

	return note.RawContent[start:end]
}

func scopeNoteToSectionEmbeds(note *model.Note, fragmentID string) *model.Note {
	if note == nil || fragmentID == "" {
		return note
	}

	section, ok := note.HeadingSections[fragmentID]
	if !ok {
		return note
	}
	section = boundedSection(note, section)

	scoped := *note
	scoped.RawContent = note.RawContent[section.StartOffset:section.EndOffset]
	scoped.BodyStartLine = sectionBodyStartLine(note, section)
	scoped.Headings = headingsInSection(note.Headings, note.HeadingSections, section)
	scoped.HeadingSections = headingSectionsFor(scoped.Headings, note.HeadingSections, section)
	scoped.OutLinks = outLinksInSection(note.OutLinks, section)
	scoped.Embeds = embedsInSection(note.Embeds, section)
	scoped.ImageRefs = imageRefsInSection(note.ImageRefs, section)
	return &scoped
}

func boundedSection(note *model.Note, section model.SectionRange) model.SectionRange {
	if note == nil {
		return section
	}

	if section.StartOffset < 0 {
		section.StartOffset = 0
	}
	if section.StartOffset > len(note.RawContent) {
		section.StartOffset = len(note.RawContent)
	}
	if section.EndOffset > len(note.RawContent) {
		section.EndOffset = len(note.RawContent)
	}
	if section.EndOffset < section.StartOffset {
		section.EndOffset = section.StartOffset
	}

	return section
}

func sectionBodyStartLine(note *model.Note, section model.SectionRange) int {
	if note == nil {
		return 0
	}

	start := section.StartOffset
	if start < 0 {
		start = 0
	}
	if start > len(note.RawContent) {
		start = len(note.RawContent)
	}

	line := note.BodyStartLine
	if line < 1 {
		line = 1
	}

	return line + bytes.Count(note.RawContent[:start], []byte("\n"))
}

func headingsInSection(headings []model.Heading, sections map[string]model.SectionRange, section model.SectionRange) []model.Heading {
	if len(headings) == 0 || len(sections) == 0 {
		return nil
	}

	filtered := make([]model.Heading, 0, len(headings))
	for _, heading := range headings {
		headingSection, ok := sections[heading.ID]
		if !ok || !offsetInSection(headingSection.StartOffset, section) {
			continue
		}
		filtered = append(filtered, heading)
	}

	return filtered
}

func headingSectionsFor(headings []model.Heading, sections map[string]model.SectionRange, parent model.SectionRange) map[string]model.SectionRange {
	if len(headings) == 0 || len(sections) == 0 {
		return nil
	}

	filtered := make(map[string]model.SectionRange, len(headings))
	for _, heading := range headings {
		section, ok := sections[heading.ID]
		if !ok {
			continue
		}
		filtered[heading.ID] = rebaseSectionRange(section, parent)
	}

	if len(filtered) == 0 {
		return nil
	}

	return filtered
}

func rebaseSectionRange(section model.SectionRange, parent model.SectionRange) model.SectionRange {
	if section.StartOffset < parent.StartOffset {
		section.StartOffset = parent.StartOffset
	}
	if section.EndOffset > parent.EndOffset {
		section.EndOffset = parent.EndOffset
	}
	if section.EndOffset < section.StartOffset {
		section.EndOffset = section.StartOffset
	}

	return model.SectionRange{
		StartOffset: section.StartOffset - parent.StartOffset,
		EndOffset:   section.EndOffset - parent.StartOffset,
	}
}

func outLinksInSection(refs []model.LinkRef, section model.SectionRange) []model.LinkRef {
	if len(refs) == 0 {
		return nil
	}

	filtered := make([]model.LinkRef, 0, len(refs))
	for _, ref := range refs {
		if !offsetInSection(ref.Offset, section) {
			continue
		}
		ref.Offset -= section.StartOffset
		filtered = append(filtered, ref)
	}

	return filtered
}

func embedsInSection(refs []model.EmbedRef, section model.SectionRange) []model.EmbedRef {
	if len(refs) == 0 {
		return nil
	}

	filtered := make([]model.EmbedRef, 0, len(refs))
	for _, ref := range refs {
		if !offsetInSection(ref.Offset, section) {
			continue
		}
		ref.Offset -= section.StartOffset
		filtered = append(filtered, ref)
	}

	return filtered
}

func imageRefsInSection(refs []model.ImageRef, section model.SectionRange) []model.ImageRef {
	if len(refs) == 0 {
		return nil
	}

	filtered := make([]model.ImageRef, 0, len(refs))
	for _, ref := range refs {
		if !offsetInSection(ref.Offset, section) {
			continue
		}
		ref.Offset -= section.StartOffset
		filtered = append(filtered, ref)
	}

	return filtered
}

func offsetInSection(offset int, section model.SectionRange) bool {
	if offset < section.StartOffset {
		return false
	}
	if section.EndOffset > section.StartOffset && offset >= section.EndOffset {
		return false
	}
	return true
}

func relativeToNoteOutput(note *model.Note, siteRelPath string) string {
	normalized := normalizeSitePath(siteRelPath)
	if normalized == "" {
		return ""
	}

	relativePath, err := filepath.Rel(noteOutputDir(note), normalized)
	if err != nil {
		return normalized
	}

	return filepath.ToSlash(relativePath)
}

func noteOutputDir(note *model.Note) string {
	if note == nil {
		return "."
	}

	slug := strings.Trim(strings.ReplaceAll(note.Slug, "\\", "/"), "/")
	if slug == "" {
		return "."
	}

	return path.Clean(slug)
}

func normalizeSitePath(value string) string {
	cleaned := strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	cleaned = strings.TrimPrefix(cleaned, "/")
	if cleaned == "" {
		return ""
	}

	cleaned = path.Clean(cleaned)
	if cleaned == "." {
		return ""
	}

	return cleaned
}

func parseImageEmbedFigureTarget(line []byte) (string, bool) {
	trimmed := strings.TrimSpace(string(line))
	if !strings.HasPrefix(trimmed, "![[") || !strings.HasSuffix(trimmed, "]]") {
		return "", false
	}

	inner := strings.TrimSpace(trimmed[len("![[") : len(trimmed)-len("]]")])
	if inner == "" {
		return "", false
	}

	target, _, _ := strings.Cut(inner, "|")
	target = strings.TrimSpace(target)
	if target == "" {
		return "", false
	}

	targetPath, _, _ := strings.Cut(target, "#")
	targetPath = strings.TrimSpace(targetPath)
	if targetPath == "" {
		return "", false
	}

	if !resourcepath.LooksLikeImage(targetPath) {
		return "", false
	}

	return targetPath, true
}

func looksLikeImageTarget(value string) bool {
	return resourcepath.LooksLikeImage(value)
}

func imageWidth(label string) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(label))
	if err != nil || parsed <= 0 {
		return 0
	}
	return parsed
}

func embedAltText(label string, rawTarget string, assetPath string) string {
	label = normalizeInlineText(label)
	rawTarget = normalizeInlineText(rawTarget)

	if label != "" && imageWidth(label) == 0 && !strings.EqualFold(label, rawTarget) {
		return label
	}

	fallback := strings.TrimSpace(assetPath)
	if fallback == "" {
		fallback = rawTarget
	}

	name := path.Base(strings.ReplaceAll(fallback, "\\", "/"))
	ext := path.Ext(name)
	if ext != "" {
		name = strings.TrimSuffix(name, ext)
	}
	name = strings.ReplaceAll(name, "_", " ")
	name = strings.ReplaceAll(name, "-", " ")
	name = normalizeInlineText(name)
	if name == "" {
		return "image"
	}

	return name
}

func normalizeInlineText(value string) string {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return ""
	}
	return strings.Join(fields, " ")
}

func wikilinkNodeText(source []byte, node gast.Node) string {
	var builder strings.Builder
	appendWikilinkNodeText(&builder, source, node)
	return builder.String()
}

func appendWikilinkNodeText(builder *strings.Builder, source []byte, node gast.Node) {
	if builder == nil || node == nil {
		return
	}

	switch current := node.(type) {
	case *gast.Text:
		_, _ = builder.Write(current.Value(source))
		if current.SoftLineBreak() || current.HardLineBreak() {
			_ = builder.WriteByte('\n')
		}
	case *gast.String:
		_, _ = builder.Write(current.Value)
	case *gast.RawHTML:
		_, _ = builder.Write(current.Segments.Value(source))
	case *gast.AutoLink:
		_, _ = builder.Write(current.Label(source))
	default:
		for child := node.FirstChild(); child != nil; child = child.NextSibling() {
			appendWikilinkNodeText(builder, source, child)
		}
	}
}

func composeRawTarget(target string, fragment string) string {
	target = strings.TrimSpace(target)
	fragment = strings.TrimSpace(fragment)
	if fragment == "" {
		return target
	}
	if target == "" {
		return "#" + fragment
	}
	return target + "#" + fragment
}

func visitKey(note *model.Note, fragmentID string) string {
	if note == nil {
		return ""
	}

	relPath := strings.TrimSpace(note.RelPath)
	if relPath == "" {
		return ""
	}

	fragmentID = strings.TrimSpace(fragmentID)
	if fragmentID == "" {
		return relPath
	}

	return relPath + "#" + fragmentID
}

func cloneVisited(visited map[string]struct{}) map[string]struct{} {
	cloned := make(map[string]struct{}, len(visited))
	for relPath := range visited {
		cloned[relPath] = struct{}{}
	}
	return cloned
}
