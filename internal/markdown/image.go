package markdown

import (
	"bytes"
	"fmt"
	"html"
	"path"
	"path/filepath"
	"strings"

	"github.com/gohugoio/hugo-goldmark-extensions/passthrough"
	"github.com/simp-lee/obsite/internal/diag"
	"github.com/simp-lee/obsite/internal/markdown/callout"
	"github.com/simp-lee/obsite/internal/model"
	"github.com/simp-lee/obsite/internal/resourcepath"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	gast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/renderer"
	gmhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/util"
)

type imageExtender struct {
	sourceNote *model.Note
	outputNote *model.Note
	index      *model.VaultIndex
	assetSink  AssetSink
	imageCount *int
}

func newImageExtender(sourceNote *model.Note, outputNote *model.Note, index *model.VaultIndex, assetSink AssetSink, imageCount *int) goldmark.Extender {
	if outputNote == nil {
		outputNote = sourceNote
	}

	return &imageExtender{
		sourceNote: sourceNote,
		outputNote: outputNote,
		index:      index,
		assetSink:  assetSink,
		imageCount: imageCount,
	}
}

func (e *imageExtender) Extend(md goldmark.Markdown) {
	md.Renderer().AddOptions(renderer.WithNodeRenderers(
		util.Prioritized(newImageHTMLRenderer(e.sourceNote, e.outputNote, e.index, e.assetSink, e.imageCount), 500),
	))
}

type imageHTMLRenderer struct {
	gmhtml.Config
	sourceNote *model.Note
	outputNote *model.Note
	index      *model.VaultIndex
	assetSink  AssetSink
	imageCount *int
}

func newImageHTMLRenderer(sourceNote *model.Note, outputNote *model.Note, index *model.VaultIndex, assetSink AssetSink, imageCount *int) *imageHTMLRenderer {
	if outputNote == nil {
		outputNote = sourceNote
	}

	return &imageHTMLRenderer{
		Config:     gmhtml.NewConfig(),
		sourceNote: sourceNote,
		outputNote: outputNote,
		index:      index,
		assetSink:  assetSink,
		imageCount: imageCount,
	}
}

func (r *imageHTMLRenderer) SetOption(name renderer.OptionName, value any) {
	r.Config.SetOption(name, value)
}

func (r *imageHTMLRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(gast.KindImage, r.renderImage)
}

func (r *imageHTMLRenderer) renderImage(
	w util.BufWriter,
	source []byte,
	node gast.Node,
	entering bool,
) (gast.WalkStatus, error) {
	if !entering {
		return gast.WalkContinue, nil
	}

	n := node.(*gast.Image)
	imageIndex := r.nextImageIndex()
	rewritten := r.rewriteDestination(string(n.Destination))

	_, _ = w.WriteString("<img src=\"")
	escapedDestination := escapeDestination(rewritten)
	if r.Unsafe || !gmhtml.IsDangerousURL([]byte(escapedDestination)) {
		_, _ = w.Write(util.EscapeHTML([]byte(escapedDestination)))
	}
	_, _ = w.WriteString(`" alt="`)
	r.writeAltText(w, source, n)
	_ = w.WriteByte('"')

	if len(n.Title) > 0 {
		_, _ = w.WriteString(` title="`)
		r.Writer.Write(w, n.Title)
		_ = w.WriteByte('"')
	}

	if imageIndex > 1 {
		if _, ok := n.Attribute([]byte("loading")); !ok {
			_, _ = w.WriteString(` loading="lazy"`)
		}
	}

	if n.Attributes() != nil {
		gmhtml.RenderAttributes(w, n, gmhtml.ImageAttributeFilter)
	}

	if r.XHTML {
		_, _ = w.WriteString(" />")
	} else {
		_, _ = w.WriteString(">")
	}

	return gast.WalkSkipChildren, nil
}

func (r *imageHTMLRenderer) nextImageIndex() int {
	if r.imageCount == nil {
		return 1
	}

	(*r.imageCount)++
	return *r.imageCount
}

func (r *imageHTMLRenderer) writeAltText(w util.BufWriter, source []byte, node gast.Node) {
	_, _ = w.WriteString(html.EscapeString(plainAltText(source, node)))
}

func plainAltText(source []byte, node gast.Node) string {
	if node == nil {
		return ""
	}

	var builder strings.Builder
	appendPlainAltText(&builder, source, node)
	return strings.Join(strings.Fields(builder.String()), " ")
}

func appendPlainAltText(builder *strings.Builder, source []byte, node gast.Node) {
	if builder == nil || node == nil {
		return
	}

	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		switch current := child.(type) {
		case *gast.Text:
			_, _ = builder.Write(bytes.TrimRight(current.Value(source), "\r\n"))
			if current.SoftLineBreak() || current.HardLineBreak() {
				_ = builder.WriteByte(' ')
			}
		case *gast.String:
			_, _ = builder.Write(current.Value)
		default:
			appendPlainAltText(builder, source, child)
		}
	}
}

func (r *imageHTMLRenderer) rewriteDestination(rawDestination string) string {
	trimmed := strings.TrimSpace(rawDestination)
	if trimmed == "" {
		return trimmed
	}

	baseDestination, suffix := splitDestinationSuffix(trimmed)
	vaultRelPath := r.resolveIndexedAssetPath(baseDestination)
	if vaultRelPath == "" {
		return trimmed
	}

	siteRelPath := vaultRelPath
	if r.assetSink != nil {
		if registered := normalizeSitePath(r.assetSink.Register(vaultRelPath)); registered != "" {
			siteRelPath = registered
		}
	}

	return relativeToNoteOutput(r.outputNote, siteRelPath) + suffix
}

func (r *imageHTMLRenderer) resolveIndexedAssetPath(rawDestination string) string {
	if r == nil {
		return ""
	}

	resource := resourcepath.ResolveIndexedAssetPath(r.sourceNote, r.index, rawDestination)
	if !resourcepath.IsResourceAllowedForNote(r.index, r.sourceNote, resource) {
		return ""
	}
	return resource
}

type codeBlockExtender struct {
	note *model.Note
	diag *diag.Collector
}

type mathTrackingExtender struct {
	note *model.Note
}

func newMathTrackingExtender(note *model.Note) goldmark.Extender {
	return &mathTrackingExtender{note: note}
}

func (e *mathTrackingExtender) Extend(md goldmark.Markdown) {
	md.Renderer().AddOptions(renderer.WithNodeRenderers(
		util.Prioritized(newMathTrackingHTMLRenderer(e.note), 50),
	))
}

type mathTrackingHTMLRenderer struct{ note *model.Note }

func newMathTrackingHTMLRenderer(note *model.Note) *mathTrackingHTMLRenderer {
	return &mathTrackingHTMLRenderer{note: note}
}

func (r *mathTrackingHTMLRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(passthrough.KindPassthroughInline, r.renderInlineMath)
	reg.Register(passthrough.KindPassthroughBlock, r.renderDisplayMath)
}

func (r *mathTrackingHTMLRenderer) renderInlineMath(w util.BufWriter, source []byte, node gast.Node, entering bool) (gast.WalkStatus, error) {
	if !entering {
		return gast.WalkContinue, nil
	}
	if r.note != nil {
		r.note.HasMath = true
	}
	mathNode := node.(*passthrough.PassthroughInline)
	_, _ = w.WriteString(`<span data-obsite-math-source="inline">`)
	_, _ = w.WriteString(html.EscapeString(string(mathNode.Segment.Value(source))))
	_, _ = w.WriteString(`</span>`)
	return gast.WalkContinue, nil
}

func (r *mathTrackingHTMLRenderer) renderDisplayMath(w util.BufWriter, source []byte, node gast.Node, entering bool) (gast.WalkStatus, error) {
	if !entering {
		return gast.WalkContinue, nil
	}
	if r.note != nil {
		r.note.HasMath = true
	}
	_, _ = w.WriteString(`<div data-obsite-math-source="display">`)
	inCallout := node.Parent() != nil && node.Parent().Kind() == callout.KindCallout
	for i := 0; i < node.Lines().Len(); i++ {
		segment := node.Lines().At(i)
		line := string(segment.Value(source))
		if inCallout {
			line = stripCalloutQuoteMarkers(line)
		}
		_, _ = w.WriteString(html.EscapeString(line))
	}
	_, _ = w.WriteString("\n</div>")
	return gast.WalkSkipChildren, nil
}

func stripCalloutQuoteMarkers(source string) string {
	lines := strings.SplitAfter(source, "\n")
	for i := range lines {
		if i == 0 {
			continue
		}
		lines[i] = strings.TrimPrefix(lines[i], ">")
		lines[i] = strings.TrimPrefix(lines[i], " ")
	}
	return strings.Join(lines, "")
}

func newCodeBlockExtender(note *model.Note, diagCollector *diag.Collector) goldmark.Extender {
	return &codeBlockExtender{note: note, diag: diagCollector}
}

func (e *codeBlockExtender) Extend(md goldmark.Markdown) {
	md.Renderer().AddOptions(renderer.WithNodeRenderers(
		util.Prioritized(newCodeBlockHTMLRenderer(e.note, e.diag), 150),
	))
}

type codeBlockHTMLRenderer struct {
	gmhtml.Config
	note         *model.Note
	diag         *diag.Collector
	fallback     renderer.SetOptioner
	fallbackFunc renderer.NodeRendererFunc
}

func newCodeBlockHTMLRenderer(note *model.Note, diagCollector *diag.Collector) *codeBlockHTMLRenderer {
	fallbackRenderer := highlighting.NewHTMLRenderer(highlighting.WithStyle("github"))
	fallbackRegisterer := newNodeRendererFuncRegisterer()
	fallbackRenderer.RegisterFuncs(fallbackRegisterer)
	setOptioner, _ := fallbackRenderer.(renderer.SetOptioner)

	return &codeBlockHTMLRenderer{
		Config:       gmhtml.NewConfig(),
		note:         note,
		diag:         diagCollector,
		fallback:     setOptioner,
		fallbackFunc: fallbackRegisterer.funcFor(gast.KindFencedCodeBlock),
	}
}

func (r *codeBlockHTMLRenderer) SetOption(name renderer.OptionName, value any) {
	r.Config.SetOption(name, value)
	if r.fallback != nil {
		r.fallback.SetOption(name, value)
	}
}

func (r *codeBlockHTMLRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(gast.KindFencedCodeBlock, r.renderFencedCodeBlock)
}

func (r *codeBlockHTMLRenderer) renderFencedCodeBlock(
	w util.BufWriter,
	source []byte,
	node gast.Node,
	entering bool,
) (gast.WalkStatus, error) {
	n := node.(*gast.FencedCodeBlock)
	if isMermaidFence(n.Language(source)) {
		if !entering {
			return gast.WalkContinue, nil
		}

		if r.note != nil {
			r.note.HasMermaid = true
		}

		_, _ = w.WriteString(`<pre class="mermaid">`)
		r.writeCodeLines(w, source, n)
		_, _ = w.WriteString("</pre>\n")
		return gast.WalkSkipChildren, nil
	}

	if language, ok := unsupportedFenceLanguage(n.Language(source)); ok {
		if !entering {
			return gast.WalkContinue, nil
		}

		r.recordUnsupportedFence(source, n, language)
		_, _ = w.WriteString(`<pre class="unsupported-syntax unsupported-`)
		_, _ = w.WriteString(language)
		_, _ = w.WriteString(`">`)
		r.writeCodeLines(w, source, n)
		_, _ = w.WriteString("</pre>\n")
		return gast.WalkSkipChildren, nil
	}

	if r.fallbackFunc == nil {
		return gast.WalkContinue, nil
	}
	return r.fallbackFunc(w, source, node, entering)
}

func (r *codeBlockHTMLRenderer) writeCodeLines(w util.BufWriter, source []byte, node gast.Node) {
	for i := 0; i < node.Lines().Len(); i++ {
		line := node.Lines().At(i)
		_, _ = w.Write(util.EscapeHTML(line.Value(source)))
	}
}

func (r *codeBlockHTMLRenderer) recordUnsupportedFence(source []byte, node *gast.FencedCodeBlock, language string) {
	if r == nil || r.diag == nil {
		return
	}

	location := diag.Location{}
	if r.note != nil {
		location.Path = r.note.RelPath
	}
	location.Line = fencedCodeBlockLine(r.note, source, node)
	r.diag.Add(diag.Diagnostic{Severity: diag.SeverityWarning, Kind: diag.KindUnsupportedSyntax, Location: location, Field: "code-fence", Target: language, Message: fmt.Sprintf("%s fenced code block is not supported; rendering as plain preformatted text", language)})
}

func unsupportedFenceLanguage(language []byte) (string, bool) {
	switch normalized := strings.ToLower(strings.TrimSpace(string(language))); normalized {
	case "dataview", "dataviewjs":
		return normalized, true
	default:
		return "", false
	}
}

func fencedCodeBlockLine(note *model.Note, source []byte, node *gast.FencedCodeBlock) int {
	if node == nil || node.Lines().Len() == 0 {
		return 0
	}

	start := node.Lines().At(0).Start
	if start < 0 {
		start = 0
	}
	if start > len(source) {
		start = len(source)
	}

	line := 1 + bytes.Count(source[:start], []byte("\n"))
	if note != nil && note.BodyStartLine > 1 {
		line += note.BodyStartLine - 1
	}
	return line
}

func isMermaidFence(language []byte) bool {
	return strings.EqualFold(strings.TrimSpace(string(language)), "mermaid")
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

	output := note.Route
	if output == "" {
		output = note.Slug
	}
	output = strings.Trim(strings.ReplaceAll(output, "\\", "/"), "/")
	if output == "" {
		return "."
	}

	return path.Clean(output)
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

func splitDestinationSuffix(value string) (string, string) {
	index := strings.IndexAny(value, "?#")
	if index < 0 {
		return value, ""
	}

	return value[:index], value[index:]
}

type nodeRendererFuncRegisterer struct {
	funcs map[gast.NodeKind]renderer.NodeRendererFunc
}

func newNodeRendererFuncRegisterer() *nodeRendererFuncRegisterer {
	return &nodeRendererFuncRegisterer{funcs: make(map[gast.NodeKind]renderer.NodeRendererFunc)}
}

func (r *nodeRendererFuncRegisterer) Register(kind gast.NodeKind, fn renderer.NodeRendererFunc) {
	r.funcs[kind] = fn
}

func (r *nodeRendererFuncRegisterer) funcFor(kind gast.NodeKind) renderer.NodeRendererFunc {
	return r.funcs[kind]
}
