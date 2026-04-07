package callout

import (
	stdhtml "html"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	gast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

var calloutHeaderPattern = regexp.MustCompile(`^\[!([A-Za-z0-9_-]+)\]([+-])?(.*)$`)

type FoldMode uint8

const (
	FoldNone FoldMode = iota
	FoldOpen
	FoldClosed
)

type Callout struct {
	gast.BaseBlock
	CalloutType string
	Title       string
	Fold        FoldMode
}

var KindCallout = gast.NewNodeKind("Callout")

func NewCallout(calloutType, title string, fold FoldMode) *Callout {
	return &Callout{
		CalloutType: calloutType,
		Title:       title,
		Fold:        fold,
	}
}

func (n *Callout) Kind() gast.NodeKind {
	return KindCallout
}

func (n *Callout) Dump(source []byte, level int) {
	gast.DumpHelper(n, source, level, nil, nil)
}

type paragraphTransformer struct{}

func NewParagraphTransformer() parser.ParagraphTransformer {
	return &paragraphTransformer{}
}

func (t *paragraphTransformer) Transform(node *gast.Paragraph, reader text.Reader, pc parser.Context) {
	parent := node.Parent()
	if parent == nil || parent.Kind() != gast.KindBlockquote || parent.FirstChild() != node {
		return
	}

	lines := node.Lines()
	if lines.Len() == 0 {
		return
	}

	firstLineSegment := lines.At(0)
	firstLine := strings.TrimRight(string(firstLineSegment.Value(reader.Source())), "\r\n")
	calloutType, title, fold, ok := parseCalloutHeader(firstLine)
	if !ok {
		return
	}

	callout := NewCallout(calloutType, title, fold)
	if lines.Len() > 1 {
		content := gast.NewParagraph()
		for i := 1; i < lines.Len(); i++ {
			segment := lines.At(i)
			if i == lines.Len()-1 {
				segment = trimTrailingLineBreak(segment, reader.Source())
			}
			content.Lines().Append(segment)
		}
		callout.AppendChild(callout, content)
	}

	parent.ReplaceChild(parent, node, callout)
}

type astTransformer struct{}

func NewASTTransformer() parser.ASTTransformer {
	return &astTransformer{}
}

func (t *astTransformer) Transform(node *gast.Document, reader text.Reader, pc parser.Context) {
	var rewrite func(parent gast.Node)
	rewrite = func(parent gast.Node) {
		for current := parent.FirstChild(); current != nil; {
			next := current.NextSibling()

			blockquote, ok := current.(*gast.Blockquote)
			if !ok {
				rewrite(current)
				current = next
				continue
			}

			callout, ok := blockquote.FirstChild().(*Callout)
			if !ok {
				rewrite(blockquote)
				current = next
				continue
			}

			for child := callout.NextSibling(); child != nil; {
				childNext := child.NextSibling()
				blockquote.RemoveChild(blockquote, child)
				callout.AppendChild(callout, child)
				child = childNext
			}

			blockquote.RemoveChild(blockquote, callout)
			parent.ReplaceChild(parent, blockquote, callout)
			rewrite(callout)
			current = next
		}
	}

	rewrite(node)
}

type HTMLRenderer struct{}

func NewHTMLRenderer() renderer.NodeRenderer {
	return &HTMLRenderer{}
}

func (r *HTMLRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(KindCallout, r.renderCallout)
}

func (r *HTMLRenderer) renderCallout(
	w util.BufWriter,
	source []byte,
	n gast.Node,
	entering bool,
) (gast.WalkStatus, error) {
	callout := n.(*Callout)

	if entering {
		if callout.Fold == FoldNone {
			_, _ = w.WriteString(`<div class="callout callout-`)
			_, _ = w.WriteString(stdhtml.EscapeString(callout.CalloutType))
			_, _ = w.WriteString(`">` + "\n")
			_, _ = w.WriteString(`<div class="callout-title">`)
			_, _ = w.WriteString(stdhtml.EscapeString(callout.displayTitle()))
			_, _ = w.WriteString(`</div>` + "\n")
			return gast.WalkContinue, nil
		}

		_, _ = w.WriteString(`<details class="callout callout-`)
		_, _ = w.WriteString(stdhtml.EscapeString(callout.CalloutType))
		if callout.Fold == FoldOpen {
			_, _ = w.WriteString(`" open>` + "\n")
		} else {
			_, _ = w.WriteString(`">` + "\n")
		}
		_, _ = w.WriteString(`<summary class="callout-title">`)
		_, _ = w.WriteString(stdhtml.EscapeString(callout.displayTitle()))
		_, _ = w.WriteString(`</summary>` + "\n")
		return gast.WalkContinue, nil
	}

	if callout.Fold == FoldNone {
		_, _ = w.WriteString("</div>\n")
	} else {
		_, _ = w.WriteString("</details>\n")
	}
	return gast.WalkContinue, nil
}

func (n *Callout) displayTitle() string {
	if n.Title != "" {
		return n.Title
	}

	normalized := strings.NewReplacer("-", " ", "_", " ").Replace(n.CalloutType)
	words := strings.Fields(normalized)
	if len(words) == 0 {
		return "Callout"
	}

	for i, word := range words {
		words[i] = strings.ToUpper(word[:1]) + strings.ToLower(word[1:])
	}

	return strings.Join(words, " ")
}

type extender struct{}

// Extension wires the local callout parser, AST rewrite, and renderer.
// Evaluated VojtaStruhar/goldmark-obsidian-callout on 2026-04-05: upstream
// README still lists default callout titles as unsupported, and its tests
// render untitled callouts with an empty <summary>. Step 8 needs a stable,
// visible .callout-title fallback and keeps non-folded callouts as <div>, so
// this package retains a local AST transformer instead of adding that dependency.
var Extension = &extender{}

func New() goldmark.Extender {
	return Extension
}

func (e *extender) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(
		parser.WithParagraphTransformers(
			util.Prioritized(NewParagraphTransformer(), 500),
		),
		parser.WithASTTransformers(
			util.Prioritized(NewASTTransformer(), 0),
		),
	)
	m.Renderer().AddOptions(renderer.WithNodeRenderers(
		util.Prioritized(NewHTMLRenderer(), 500),
	))
}

func parseCalloutHeader(line string) (string, string, FoldMode, bool) {
	trimmed := strings.TrimLeft(line, " \t")
	matches := calloutHeaderPattern.FindStringSubmatch(trimmed)
	if matches == nil {
		return "", "", FoldNone, false
	}

	fold := FoldNone
	switch matches[2] {
	case "+":
		fold = FoldOpen
	case "-":
		fold = FoldClosed
	}

	return strings.ToLower(matches[1]), strings.TrimSpace(matches[3]), fold, true
}

func trimTrailingLineBreak(segment text.Segment, source []byte) text.Segment {
	trimmed := segment
	for trimmed.Stop > trimmed.Start {
		switch source[trimmed.Stop-1] {
		case '\n', '\r':
			trimmed.Stop--
		default:
			return trimmed
		}
	}
	return trimmed
}
