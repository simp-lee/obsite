package markdown

import (
	"bytes"
	"net/url"
	"path"
	"path/filepath"
	"strings"

	figureast "github.com/mangoumbrella/goldmark-figure/ast"
	internalasset "github.com/simp-lee/obsite/internal/asset"
	"github.com/simp-lee/obsite/internal/diag"
	"github.com/simp-lee/obsite/internal/markdown/math"
	"github.com/simp-lee/obsite/internal/model"
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
	if embed, ok := videoEmbedDestination(string(n.Destination)); ok && canRenderVideoEmbed(node) {
		r.renderVideoEmbed(w, source, n, embed)
		return gast.WalkSkipChildren, nil
	}

	imageIndex := r.nextImageIndex()
	rewritten := r.rewriteDestination(string(n.Destination))

	_, _ = w.WriteString("<img src=\"")
	escapedDestination := util.URLEscape([]byte(rewritten), true)
	if r.Unsafe || !gmhtml.IsDangerousURL(escapedDestination) {
		_, _ = w.Write(util.EscapeHTML(escapedDestination))
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

func (r *imageHTMLRenderer) renderVideoEmbed(w util.BufWriter, source []byte, node *gast.Image, embed videoEmbed) {
	_, _ = w.WriteString(`<div class="video-embed"><iframe src="`)
	_, _ = w.Write(util.EscapeHTML([]byte(embed.src)))
	_, _ = w.WriteString(`" title="`)
	_, _ = w.Write(util.EscapeHTML([]byte(r.videoEmbedTitle(source, node, embed.defaultTitle))))
	_, _ = w.WriteString(`" loading="lazy" allowfullscreen></iframe></div>`)
}

func canRenderVideoEmbed(node gast.Node) bool {
	for parent := node.Parent(); parent != nil; parent = parent.Parent() {
		if parent.Kind() == figureast.KindFigureImage {
			return true
		}
	}

	return false
}

func (r *imageHTMLRenderer) nextImageIndex() int {
	if r.imageCount == nil {
		return 1
	}

	(*r.imageCount)++
	return *r.imageCount
}

func (r *imageHTMLRenderer) videoEmbedTitle(source []byte, node *gast.Image, fallback string) string {
	if node != nil {
		if title := strings.TrimSpace(string(node.Title)); title != "" {
			return title
		}
		if alt := strings.TrimSpace(plainAltText(source, node)); alt != "" {
			return alt
		}
	}

	return fallback
}

func (r *imageHTMLRenderer) writeAltText(w util.BufWriter, source []byte, node gast.Node) {
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		switch current := child.(type) {
		case *gast.Text:
			value := bytes.TrimRight(current.Value(source), "\r\n")
			if current.IsRaw() {
				r.Writer.RawWrite(w, value)
			} else {
				r.Writer.Write(w, value)
			}
			if current.SoftLineBreak() || current.HardLineBreak() {
				_ = w.WriteByte(' ')
			}
		case *gast.String:
			if current.IsRaw() || current.IsCode() {
				r.Writer.RawWrite(w, current.Value)
			} else {
				r.Writer.Write(w, current.Value)
			}
		default:
			r.writeAltText(w, source, child)
		}
	}
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
	attachmentFolderPath := ""
	if r != nil && r.index != nil {
		attachmentFolderPath = r.index.AttachmentFolderPath
	}

	return internalasset.ResolvePath(r.sourceNote, attachmentFolderPath, rawDestination, func(candidate string) bool {
		return r != nil && r.index != nil && r.index.Assets[candidate] != nil
	})
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
		util.Prioritized(newMathTrackingHTMLRenderer(e.note), 499),
	))
}

type mathTrackingHTMLRenderer struct {
	note        *model.Note
	inlineFunc  renderer.NodeRendererFunc
	displayFunc renderer.NodeRendererFunc
}

func newMathTrackingHTMLRenderer(note *model.Note) *mathTrackingHTMLRenderer {
	fallback := math.NewHTMLRenderer()
	fallbackRegisterer := newNodeRendererFuncRegisterer()
	fallback.RegisterFuncs(fallbackRegisterer)

	return &mathTrackingHTMLRenderer{
		note:        note,
		inlineFunc:  fallbackRegisterer.funcFor(math.KindInlineMath),
		displayFunc: fallbackRegisterer.funcFor(math.KindDisplayMath),
	}
}

func (r *mathTrackingHTMLRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(math.KindInlineMath, r.renderInlineMath)
	reg.Register(math.KindDisplayMath, r.renderDisplayMath)
}

func (r *mathTrackingHTMLRenderer) renderInlineMath(
	w util.BufWriter,
	source []byte,
	node gast.Node,
	entering bool,
) (gast.WalkStatus, error) {
	if entering && r.note != nil {
		r.note.HasMath = true
	}
	if r.inlineFunc == nil {
		return gast.WalkContinue, nil
	}
	return r.inlineFunc(w, source, node, entering)
}

func (r *mathTrackingHTMLRenderer) renderDisplayMath(
	w util.BufWriter,
	source []byte,
	node gast.Node,
	entering bool,
) (gast.WalkStatus, error) {
	if entering && r.note != nil {
		r.note.HasMath = true
	}
	if r.displayFunc == nil {
		return gast.WalkContinue, nil
	}
	return r.displayFunc(w, source, node, entering)
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
	r.diag.Warningf(diag.KindUnsupportedSyntax, location, "%s fenced code block is not supported; rendering as plain preformatted text", language)
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

func isOutsideVaultPath(value string) bool {
	return value == ".." || strings.HasPrefix(value, "../")
}

func splitDestinationSuffix(value string) (string, string) {
	index := strings.IndexAny(value, "?#")
	if index < 0 {
		return value, ""
	}

	return value[:index], value[index:]
}

type videoEmbed struct {
	src          string
	defaultTitle string
}

const youtubeCanonicalVideoIDLength = 11

func videoEmbedDestination(rawDestination string) (videoEmbed, bool) {
	trimmed := strings.TrimSpace(rawDestination)
	if trimmed == "" {
		return videoEmbed{}, false
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return videoEmbed{}, false
	}
	if parsed == nil || !isVideoURLScheme(parsed.Scheme) {
		return videoEmbed{}, false
	}

	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	switch {
	case matchesVideoHost(host, "youtube.com"):
		return youtubeWatchEmbed(parsed)
	case matchesVideoHost(host, "youtu.be"):
		return youtubeShortEmbed(parsed)
	case matchesVideoHost(host, "vimeo.com"):
		return vimeoEmbed(parsed)
	default:
		return videoEmbed{}, false
	}
}

func isVideoURLScheme(scheme string) bool {
	switch strings.ToLower(strings.TrimSpace(scheme)) {
	case "http", "https":
		return true
	default:
		return false
	}
}

func matchesVideoHost(host string, want string) bool {
	host = strings.TrimSpace(strings.ToLower(host))
	want = strings.TrimSpace(strings.ToLower(want))
	if host == "" || want == "" {
		return false
	}

	return host == want || strings.HasSuffix(host, "."+want)
}

func youtubeWatchEmbed(parsed *url.URL) (videoEmbed, bool) {
	if parsed == nil || strings.Trim(strings.ToLower(parsed.Path), "/") != "watch" {
		return videoEmbed{}, false
	}

	videoID, ok := canonicalYouTubeVideoID(parsed.Query().Get("v"))
	if !ok {
		return videoEmbed{}, false
	}

	return videoEmbed{
		src:          "https://www.youtube.com/embed/" + videoID,
		defaultTitle: "YouTube video",
	}, true
}

func youtubeShortEmbed(parsed *url.URL) (videoEmbed, bool) {
	if parsed == nil {
		return videoEmbed{}, false
	}

	segments := trimmedPathSegments(parsed.Path)
	if len(segments) != 1 {
		return videoEmbed{}, false
	}

	videoID, ok := canonicalYouTubeVideoID(segments[0])
	if !ok {
		return videoEmbed{}, false
	}

	return videoEmbed{
		src:          "https://www.youtube.com/embed/" + videoID,
		defaultTitle: "YouTube video",
	}, true
}

func canonicalYouTubeVideoID(value string) (string, bool) {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) != youtubeCanonicalVideoIDLength {
		return "", false
	}

	for _, r := range trimmed {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-', r == '_':
		default:
			return "", false
		}
	}

	return trimmed, true
}

func vimeoEmbed(parsed *url.URL) (videoEmbed, bool) {
	if parsed == nil {
		return videoEmbed{}, false
	}

	videoID, ok := vimeoVideoID(parsed)
	if !ok {
		return videoEmbed{}, false
	}

	return videoEmbed{
		src:          "https://player.vimeo.com/video/" + videoID,
		defaultTitle: "Vimeo video",
	}, true
}

func vimeoVideoID(parsed *url.URL) (string, bool) {
	if parsed == nil {
		return "", false
	}

	host := strings.TrimSpace(strings.ToLower(parsed.Hostname()))
	segments := trimmedPathSegments(parsed.Path)
	switch host {
	case "vimeo.com", "www.vimeo.com":
		if len(segments) != 1 || !isDigitsOnly(segments[0]) {
			return "", false
		}
		return segments[0], true
	case "player.vimeo.com":
		if len(segments) != 2 || !strings.EqualFold(segments[0], "video") || !isDigitsOnly(segments[1]) {
			return "", false
		}
		return segments[1], true
	default:
		return "", false
	}
}

func trimmedPathSegments(value string) []string {
	trimmed := strings.Trim(strings.TrimSpace(value), "/")
	if trimmed == "" {
		return nil
	}

	parts := strings.Split(trimmed, "/")
	segments := make([]string, 0, len(parts))
	for _, part := range parts {
		cleaned := strings.TrimSpace(part)
		if cleaned != "" {
			segments = append(segments, cleaned)
		}
	}

	return segments
}

func isDigitsOnly(value string) bool {
	if value == "" {
		return false
	}

	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}

	return true
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
