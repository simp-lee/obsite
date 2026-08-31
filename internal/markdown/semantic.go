package markdown

import (
	"strings"

	"github.com/simp-lee/obsite/internal/markdown/math"
	gast "github.com/yuin/goldmark/ast"
	gmhashtag "go.abhg.dev/goldmark/hashtag"
	gmwikilink "go.abhg.dev/goldmark/wikilink"
)

// RelatedSemanticText extracts recommendation headings and body directly from
// a source AST. It never follows embeds or reconstructs text from rendered HTML.
func RelatedSemanticText(root gast.Node, source []byte) ([]string, string) {
	if root == nil {
		return nil, ""
	}

	headings := make([]string, 0)
	_ = gast.Walk(root, func(node gast.Node, entering bool) (gast.WalkStatus, error) {
		if !entering {
			return gast.WalkContinue, nil
		}

		heading, ok := node.(*gast.Heading)
		if !ok {
			return gast.WalkContinue, nil
		}

		collector := newHeadingTextCollector()
		for child := heading.FirstChild(); child != nil; child = child.NextSibling() {
			appendRelatedHeadingText(collector, child, source)
		}
		if value := collector.String(); value != "" {
			headings = append(headings, value)
		}
		return gast.WalkSkipChildren, nil
	})

	body := newHeadingTextCollector()
	for child := root.FirstChild(); child != nil; child = child.NextSibling() {
		appendRelatedBodyText(body, child, source)
	}
	return headings, body.String()
}

func appendRelatedHeadingText(collector *headingTextCollector, node gast.Node, source []byte) {
	if collector == nil || node == nil {
		return
	}

	switch current := node.(type) {
	case *gast.Text:
		appendRelatedSourceText(collector, current, source)
	case *gast.String:
		if current.IsCode() || current.IsRaw() {
			if !collector.inInvisibleRawHTML() {
				collector.appendText(string(current.Value))
			}
		} else {
			collector.appendSourceText(string(current.Value))
		}
	case *gast.CodeSpan:
		collector.appendCodeSpanText(current, source)
	case *gast.AutoLink:
		collector.appendSourceText(string(current.Label(source)))
	case *gmhashtag.Node:
		return
	case *gmwikilink.Node:
		if current.Embed || collector.inInvisibleRawHTML() {
			return
		}
		collector.appendText(relatedWikilinkText(current, source, true))
	case *gast.RawHTML:
		collector.applyRawHTML(string(current.Segments.Value(source)))
	case *math.InlineMath, *math.DisplayMath:
		return
	default:
		for child := node.FirstChild(); child != nil; child = child.NextSibling() {
			appendRelatedHeadingText(collector, child, source)
		}
	}
}

func appendRelatedBodyText(collector *headingTextCollector, node gast.Node, source []byte) {
	if collector == nil || node == nil {
		return
	}

	switch current := node.(type) {
	case *gast.Heading, *gast.CodeBlock, *gast.FencedCodeBlock, *gast.HTMLBlock, *gast.LinkReferenceDefinition:
		return
	case *math.InlineMath, *math.DisplayMath, *gmhashtag.Node:
		return
	case *gast.Text:
		appendRelatedSourceText(collector, current, source)
	case *gast.String:
		if current.IsCode() || current.IsRaw() {
			if !collector.inInvisibleRawHTML() {
				collector.appendText(string(current.Value))
			}
		} else {
			collector.appendSourceText(string(current.Value))
		}
	case *gast.CodeSpan:
		collector.appendCodeSpanText(current, source)
	case *gast.AutoLink:
		if !collector.inInvisibleRawHTML() {
			collector.appendSourceText(string(current.Label(source)))
		}
	case *gmwikilink.Node:
		if current.Embed {
			return
		}
		if !collector.inInvisibleRawHTML() {
			collector.appendText(relatedWikilinkText(current, source, false))
		}
	case *gast.RawHTML:
		collector.applyRawHTML(string(current.Segments.Value(source)))
	default:
		for child := node.FirstChild(); child != nil; child = child.NextSibling() {
			appendRelatedBodyText(collector, child, source)
		}
		if node.Type() == gast.TypeBlock {
			collector.space()
			collector.endBlock()
		}
	}
}

func appendRelatedSourceText(collector *headingTextCollector, node *gast.Text, source []byte) {
	if collector == nil || node == nil {
		return
	}

	if node.IsRaw() {
		if !collector.inInvisibleRawHTML() {
			collector.appendText(string(node.Value(source)))
		}
	} else {
		collector.appendSourceText(string(node.Value(source)))
	}
	if !collector.inInvisibleRawHTML() && (node.SoftLineBreak() || node.HardLineBreak()) {
		collector.space()
	}
}

func relatedWikilinkText(node *gmwikilink.Node, source []byte, heading bool) string {
	if node == nil || node.Embed {
		return ""
	}

	collector := newHeadingTextCollector()
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		if heading {
			appendRelatedHeadingText(collector, child, source)
		} else {
			appendRelatedBodyText(collector, child, source)
		}
	}
	if value := collector.String(); value != "" {
		return value
	}

	target := strings.TrimSpace(string(node.Target))
	fragment := strings.TrimSpace(string(node.Fragment))
	switch {
	case fragment == "":
		return target
	case target == "":
		return fragment
	default:
		return target + " " + fragment
	}
}
