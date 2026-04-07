package highlight

import (
	"github.com/yuin/goldmark"
	gast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

type markDelimiterProcessor struct{}

func (p *markDelimiterProcessor) IsDelimiter(b byte) bool {
	return b == '='
}

func (p *markDelimiterProcessor) CanOpenCloser(opener, closer *parser.Delimiter) bool {
	return opener.Char == closer.Char && opener.NextSibling() != closer
}

func (p *markDelimiterProcessor) OnMatch(consumes int) gast.Node {
	return NewMark()
}

var defaultMarkDelimiterProcessor = &markDelimiterProcessor{}

type inlineParser struct{}

var defaultInlineParser = &inlineParser{}

func NewParser() parser.InlineParser {
	return defaultInlineParser
}

func (p *inlineParser) Trigger() []byte {
	return []byte{'='}
}

func (p *inlineParser) Parse(parent gast.Node, block text.Reader, pc parser.Context) gast.Node {
	before := block.PrecendingCharacter()
	line, segment := block.PeekLine()
	node := parser.ScanDelimiter(line, before, 2, defaultMarkDelimiterProcessor)
	if node == nil || node.OriginalLength != 2 || before == '=' {
		return nil
	}

	node.Segment = segment.WithStop(segment.Start + node.OriginalLength)
	block.Advance(node.OriginalLength)
	pc.PushDelimiter(node)
	return node
}

type HTMLRenderer struct {
	html.Config
}

func NewHTMLRenderer(opts ...html.Option) renderer.NodeRenderer {
	r := &HTMLRenderer{Config: html.NewConfig()}
	for _, opt := range opts {
		opt.SetHTMLOption(&r.Config)
	}
	return r
}

func (r *HTMLRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(KindMark, r.renderMark)
}

var markAttributeFilter = html.GlobalAttributeFilter

func (r *HTMLRenderer) renderMark(
	w util.BufWriter,
	source []byte,
	n gast.Node,
	entering bool,
) (gast.WalkStatus, error) {
	if entering {
		if n.Attributes() != nil {
			_, _ = w.WriteString("<mark")
			html.RenderAttributes(w, n, markAttributeFilter)
			_ = w.WriteByte('>')
		} else {
			_, _ = w.WriteString("<mark>")
		}
		return gast.WalkContinue, nil
	}

	_, _ = w.WriteString("</mark>")
	return gast.WalkContinue, nil
}

type extender struct{}

var Extension = &extender{}

func New() goldmark.Extender {
	return Extension
}

func (e *extender) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(parser.WithInlineParsers(
		util.Prioritized(NewParser(), 500),
	))
	m.Renderer().AddOptions(renderer.WithNodeRenderers(
		util.Prioritized(NewHTMLRenderer(), 500),
	))
}

type Mark struct {
	gast.BaseInline
}

var KindMark = gast.NewNodeKind("Mark")

func (n *Mark) Dump(source []byte, level int) {
	gast.DumpHelper(n, source, level, nil, nil)
}

func (n *Mark) Kind() gast.NodeKind {
	return KindMark
}

func NewMark() *Mark {
	return &Mark{}
}
