package math

import (
	"bytes"
	"fmt"
	stdhtml "html"

	"github.com/yuin/goldmark"
	gast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// DocumentMetaHasMath marks parsed documents that contain inline or display math nodes.
const DocumentMetaHasMath = "obsite:has_math"

// InlineMath stores the raw LaTeX content found between single-dollar delimiters.
type InlineMath struct {
	gast.BaseInline
	Literal []byte
}

// KindInlineMath identifies inline math nodes.
var KindInlineMath = gast.NewNodeKind("InlineMath")

// NewInlineMath creates a new inline math node.
func NewInlineMath(literal []byte) *InlineMath {
	return &InlineMath{Literal: append([]byte(nil), literal...)}
}

// Kind implements gast.Node.
func (n *InlineMath) Kind() gast.NodeKind {
	return KindInlineMath
}

// Dump implements gast.Node.
func (n *InlineMath) Dump(source []byte, level int) {
	gast.DumpHelper(n, source, level, map[string]string{
		"Literal": fmt.Sprintf("%q", n.Literal),
	}, nil)
}

// Text implements gast.Node.
func (n *InlineMath) Text(source []byte) []byte {
	return append([]byte(nil), n.Literal...)
}

// DisplayMath stores the raw LaTeX content found between double-dollar delimiters.
type DisplayMath struct {
	gast.BaseBlock
	Literal []byte
}

// KindDisplayMath identifies display math nodes.
var KindDisplayMath = gast.NewNodeKind("DisplayMath")

// NewDisplayMath creates a new display math node.
func NewDisplayMath(literal []byte) *DisplayMath {
	return &DisplayMath{Literal: append([]byte(nil), literal...)}
}

// Kind implements gast.Node.
func (n *DisplayMath) Kind() gast.NodeKind {
	return KindDisplayMath
}

// Dump implements gast.Node.
func (n *DisplayMath) Dump(source []byte, level int) {
	gast.DumpHelper(n, source, level, map[string]string{
		"Literal": fmt.Sprintf("%q", n.Literal),
	}, nil)
}

// Text implements gast.Node.
func (n *DisplayMath) Text(source []byte) []byte {
	return append([]byte(nil), n.Literal...)
}

type inlineParser struct{}

var defaultInlineParser = &inlineParser{}

// NewParser returns the inline parser that recognizes single-dollar math.
func NewParser() parser.InlineParser {
	return defaultInlineParser
}

func (p *inlineParser) Trigger() []byte {
	return []byte{'$'}
}

func (p *inlineParser) Parse(parent gast.Node, block text.Reader, _ parser.Context) gast.Node {
	line, segment := block.PeekLine()
	if !canOpenInlineMath(line, block.PrecendingCharacter()) {
		return nil
	}

	end := findInlineMathClosing(line)
	if end < 0 {
		return nil
	}

	literal := line[1:end]
	if len(bytes.TrimSpace(literal)) == 0 {
		return nil
	}

	node := NewInlineMath(literal)
	node.SetPos(segment.Start)
	block.Advance(end + 1)
	return node
}

type paragraphTransformer struct{}

// NewParagraphTransformer rewrites paragraph-only $$...$$ content into a display math node.
func NewParagraphTransformer() parser.ParagraphTransformer {
	return &paragraphTransformer{}
}

func (t *paragraphTransformer) Transform(node *gast.Paragraph, reader text.Reader, _ parser.Context) {
	rewritten := RewriteParagraph(node, reader.Source())
	if rewritten == node {
		return
	}

	parent := node.Parent()
	if parent == nil {
		return
	}

	parent.ReplaceChild(parent, node, rewritten)
}

// RewriteParagraph applies the package display-math paragraph rewrite contract.
func RewriteParagraph(node *gast.Paragraph, source []byte) gast.Node {
	if node == nil {
		return nil
	}

	display, ok := parseDisplayMath(node, source)
	if !ok {
		return node
	}

	return display
}

type documentTransformer struct{}

// NewDocumentTransformer annotates parsed documents when any math node is present.
func NewDocumentTransformer() parser.ASTTransformer {
	return &documentTransformer{}
}

func (t *documentTransformer) Transform(node *gast.Document, reader text.Reader, pc parser.Context) {
	if HasMath(node) {
		node.AddMeta(DocumentMetaHasMath, true)
	}
}

type HTMLRenderer struct{}

// NewHTMLRenderer renders math nodes into containers whose text remains KaTeX auto-renderable.
func NewHTMLRenderer() renderer.NodeRenderer {
	return &HTMLRenderer{}
}

func (r *HTMLRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(KindInlineMath, r.renderInlineMath)
	reg.Register(KindDisplayMath, r.renderDisplayMath)
}

func (r *HTMLRenderer) renderInlineMath(
	w util.BufWriter,
	source []byte,
	n gast.Node,
	entering bool,
) (gast.WalkStatus, error) {
	math := n.(*InlineMath)

	if entering {
		_, _ = w.WriteString(`<span class="math math-inline">`)
		_, _ = w.WriteString(`$`)
		_, _ = w.WriteString(stdhtml.EscapeString(string(math.Literal)))
		return gast.WalkContinue, nil
	}

	_, _ = w.WriteString(`$</span>`)
	return gast.WalkContinue, nil
}

func (r *HTMLRenderer) renderDisplayMath(
	w util.BufWriter,
	source []byte,
	n gast.Node,
	entering bool,
) (gast.WalkStatus, error) {
	math := n.(*DisplayMath)

	if entering {
		_, _ = w.WriteString(`<div class="math math-display">`)
		_, _ = w.WriteString(`$$`)
		_, _ = w.WriteString(stdhtml.EscapeString(string(math.Literal)))
		return gast.WalkContinue, nil
	}

	_, _ = w.WriteString("$$</div>\n")
	return gast.WalkContinue, nil
}

// IsMathNode reports whether the given node is an Obsite math node.
func IsMathNode(node gast.Node) bool {
	if node == nil {
		return false
	}

	switch node.Kind() {
	case KindInlineMath, KindDisplayMath:
		return true
	default:
		return false
	}
}

// HasMath reports whether the AST contains at least one math node.
func HasMath(root gast.Node) bool {
	if root == nil {
		return false
	}

	found := false
	_ = gast.Walk(root, func(node gast.Node, entering bool) (gast.WalkStatus, error) {
		if !entering {
			return gast.WalkContinue, nil
		}
		if IsMathNode(node) {
			found = true
			return gast.WalkStop, nil
		}
		return gast.WalkContinue, nil
	})

	return found
}

type extender struct{}

// Extension wires the math parser, transformer, and renderer into goldmark.
var Extension = &extender{}

// New returns the package-level goldmark extender.
func New() goldmark.Extender {
	return Extension
}

func (e *extender) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(
		parser.WithInlineParsers(
			util.Prioritized(NewParser(), 450),
		),
		parser.WithParagraphTransformers(
			util.Prioritized(NewParagraphTransformer(), 450),
		),
		parser.WithASTTransformers(
			util.Prioritized(NewDocumentTransformer(), 450),
		),
	)
	m.Renderer().AddOptions(renderer.WithNodeRenderers(
		util.Prioritized(NewHTMLRenderer(), 500),
	))
}

func canOpenInlineMath(line []byte, before rune) bool {
	if len(line) < 3 || line[0] != '$' {
		return false
	}
	if before == '\\' || before == '$' || isASCIIDigitRune(before) {
		return false
	}

	next := line[1]
	return next != '$' && !isInlineSpace(next)
}

func findInlineMathClosing(line []byte) int {
	for i := 1; i < len(line); i++ {
		if line[i] != '$' {
			continue
		}
		if isEscapedDelimiter(line, i) {
			continue
		}
		if i+1 < len(line) && line[i+1] == '$' {
			continue
		}
		if isInlineSpace(line[i-1]) {
			continue
		}
		if i+1 < len(line) && isASCIIDigit(line[i+1]) {
			continue
		}
		return i
	}

	return -1
}

func parseDisplayMath(node *gast.Paragraph, source []byte) (*DisplayMath, bool) {
	lines := node.Lines()
	if lines.Len() == 0 {
		return nil, false
	}

	firstSegment := lines.At(0)
	first := firstSegment.Value(source)
	openAfter, ok := findDisplayOpen(first)
	if !ok {
		return nil, false
	}

	lastSegment := lines.At(lines.Len() - 1)
	last := lastSegment.Value(source)
	closeBefore, ok := findDisplayClose(last)
	if !ok {
		return nil, false
	}
	if lines.Len() == 1 && closeBefore < openAfter {
		return nil, false
	}

	var literal bytes.Buffer
	for i := 0; i < lines.Len(); i++ {
		segment := lines.At(i)
		value := segment.Value(source)
		start := 0
		stop := len(value)

		if i == 0 {
			start = openAfter
		}
		if i == lines.Len()-1 {
			stop = closeBefore
		}
		if stop < start {
			return nil, false
		}

		literal.Write(value[start:stop])
	}

	content := literal.Bytes()
	if len(bytes.TrimSpace(content)) == 0 {
		return nil, false
	}

	display := NewDisplayMath(content)
	display.SetPos(lines.At(0).Start)
	return display, true
}

func findDisplayOpen(line []byte) (int, bool) {
	start := leadingHorizontalSpace(line)
	if start+2 > len(line) {
		return 0, false
	}
	if line[start] != '$' || line[start+1] != '$' {
		return 0, false
	}
	if start+2 < len(line) && line[start+2] == '$' {
		return 0, false
	}
	return start + 2, true
}

func findDisplayClose(line []byte) (int, bool) {
	end := len(line)
	for end > 0 && (line[end-1] == '\n' || line[end-1] == '\r') {
		end--
	}
	for end > 0 && (line[end-1] == ' ' || line[end-1] == '\t') {
		end--
	}
	if end < 2 {
		return 0, false
	}
	if line[end-2] != '$' || line[end-1] != '$' {
		return 0, false
	}
	if end >= 3 && line[end-3] == '$' {
		return 0, false
	}
	return end - 2, true
}

func leadingHorizontalSpace(line []byte) int {
	index := 0
	for index < len(line) {
		if line[index] != ' ' && line[index] != '\t' {
			break
		}
		index++
	}
	return index
}

func isEscapedDelimiter(line []byte, index int) bool {
	count := 0
	for i := index - 1; i >= 0 && line[i] == '\\'; i-- {
		count++
	}
	return count%2 == 1
}

func isInlineSpace(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r':
		return true
	default:
		return false
	}
}

func isASCIIDigit(b byte) bool {
	return b >= '0' && b <= '9'
}

func isASCIIDigitRune(r rune) bool {
	return r >= '0' && r <= '9'
}
