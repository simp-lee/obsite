package markdown

import (
	"github.com/yuin/goldmark"
	gast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/renderer"
	gmhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/util"
)

type rawHTMLExtender struct{}

func newRawHTMLExtender() goldmark.Extender {
	return &rawHTMLExtender{}
}

func (e *rawHTMLExtender) Extend(md goldmark.Markdown) {
	md.Renderer().AddOptions(renderer.WithNodeRenderers(
		util.Prioritized(newRawHTMLRenderer(), 501),
	))
}

type rawHTMLRenderer struct {
	gmhtml.Config
}

func newRawHTMLRenderer() *rawHTMLRenderer {
	return &rawHTMLRenderer{Config: gmhtml.NewConfig()}
}

func (r *rawHTMLRenderer) SetOption(name renderer.OptionName, value any) {
	r.Config.SetOption(name, value)
}

func (r *rawHTMLRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(gast.KindHTMLBlock, r.renderHTMLBlock)
	reg.Register(gast.KindRawHTML, r.renderRawHTML)
}

func (r *rawHTMLRenderer) renderHTMLBlock(
	w util.BufWriter,
	source []byte,
	node gast.Node,
	entering bool,
) (gast.WalkStatus, error) {
	n := node.(*gast.HTMLBlock)
	if entering {
		for i := 0; i < n.Lines().Len(); i++ {
			line := n.Lines().At(i)
			r.Writer.SecureWrite(w, line.Value(source))
		}
	} else if n.HasClosure() {
		r.Writer.SecureWrite(w, n.ClosureLine.Value(source))
	}

	return gast.WalkContinue, nil
}

func (r *rawHTMLRenderer) renderRawHTML(
	w util.BufWriter,
	source []byte,
	node gast.Node,
	entering bool,
) (gast.WalkStatus, error) {
	if !entering {
		return gast.WalkSkipChildren, nil
	}

	n := node.(*gast.RawHTML)
	for i := 0; i < n.Segments.Len(); i++ {
		segment := n.Segments.At(i)
		_, _ = w.Write(segment.Value(source))
	}

	return gast.WalkSkipChildren, nil
}
