package markdown

import (
	"html"
	"io"
	"strconv"
	"strings"
	"unicode"

	"github.com/simp-lee/obsite/internal/markdown/math"
	"github.com/simp-lee/obsite/internal/model"
	gast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	gmhashtag "go.abhg.dev/goldmark/hashtag"
	gmwikilink "go.abhg.dev/goldmark/wikilink"
	xhtml "golang.org/x/net/html"
)

type visibleHeadingIDTransformer struct {
	prefix   string
	headings []model.Heading
}

func newVisibleHeadingIDTransformer(note *model.Note, prefix string) visibleHeadingIDTransformer {
	transformer := visibleHeadingIDTransformer{prefix: prefix}
	if note != nil && len(note.Headings) > 0 {
		transformer.headings = append([]model.Heading(nil), note.Headings...)
	}
	return transformer
}

func (t visibleHeadingIDTransformer) Transform(doc *gast.Document, reader text.Reader, _ parser.Context) {
	used := make(map[string]struct{}, len(t.headings))
	source := reader.Source()
	headingIndex := 0

	_ = gast.Walk(doc, func(node gast.Node, entering bool) (gast.WalkStatus, error) {
		if !entering {
			return gast.WalkContinue, nil
		}

		heading, ok := node.(*gast.Heading)
		if !ok {
			return gast.WalkContinue, nil
		}

		heading.SetAttributeString("id", []byte(t.headingID(headingIndex, VisibleHeadingText(heading, source), used)))
		headingIndex++
		return gast.WalkContinue, nil
	})
}

func (t visibleHeadingIDTransformer) headingID(index int, visibleText string, used map[string]struct{}) string {
	if index < len(t.headings) {
		if base := strings.TrimSpace(t.headings[index].ID); base != "" {
			used[base] = struct{}{}
			if t.prefix == "" {
				return base
			}
			return t.prefix + base
		}
	}

	return renderHeadingID(visibleText, t.prefix, used)
}

func renderHeadingID(visibleText string, prefix string, used map[string]struct{}) string {
	base := uniqueHeadingID(normalizeHeadingID(visibleText), used)
	if prefix == "" {
		return base
	}
	return prefix + base
}

// VisibleHeadingText returns the user-visible text of a parsed heading.
func VisibleHeadingText(heading *gast.Heading, source []byte) string {
	if heading == nil {
		return ""
	}

	collector := newHeadingTextCollector()
	for child := heading.FirstChild(); child != nil; child = child.NextSibling() {
		appendVisibleHeadingInlineText(collector, child, source)
	}

	return collector.String()
}

func appendVisibleHeadingInlineText(collector *headingTextCollector, node gast.Node, source []byte) {
	if collector == nil || node == nil {
		return
	}

	switch current := node.(type) {
	case *gast.Text:
		if current.IsRaw() {
			collector.appendText(string(current.Value(source)))
		} else {
			collector.appendSourceText(string(current.Value(source)))
		}
		if !collector.inInvisibleRawHTML() && (current.SoftLineBreak() || current.HardLineBreak()) {
			collector.space()
		}
	case *gast.String:
		if current.IsCode() || current.IsRaw() {
			collector.appendText(string(current.Value))
		} else {
			collector.appendSourceText(string(current.Value))
		}
	case *gast.CodeSpan:
		collector.appendCodeSpanText(current, source)
	case *gmhashtag.Node:
		collector.appendText("#" + string(current.Tag))
	case *gmwikilink.Node:
		collector.appendText(visibleHeadingWikilinkText(current, source))
	case *gast.RawHTML:
		collector.applyRawHTML(string(current.Segments.Value(source)))
	case *math.InlineMath:
		collector.appendText(string(current.Literal))
	case *math.DisplayMath:
		collector.appendText(string(current.Literal))
		collector.space()
	default:
		for child := node.FirstChild(); child != nil; child = child.NextSibling() {
			appendVisibleHeadingInlineText(collector, child, source)
		}
	}
}

func visibleHeadingWikilinkText(node *gmwikilink.Node, source []byte) string {
	if node == nil {
		return ""
	}

	collector := newHeadingTextCollector()
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		appendVisibleHeadingInlineText(collector, child, source)
	}
	if textValue := collector.String(); textValue != "" {
		return textValue
	}

	return normalizeHeadingWhitespace(composeWikilinkTarget(string(node.Target), string(node.Fragment)))
}

func composeWikilinkTarget(target string, fragment string) string {
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

func normalizeHeadingID(value string) string {
	value = normalizeHeadingWhitespace(value)
	if value == "" {
		return "heading"
	}

	var builder strings.Builder
	lastHyphen := false

	for _, r := range strings.ToLower(value) {
		switch {
		case isHeadingASCIIControl(r):
			continue
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			builder.WriteRune(r)
			lastHyphen = false
		case unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) || r == '_' || r == '-':
			if lastHyphen || builder.Len() == 0 {
				continue
			}
			builder.WriteByte('-')
			lastHyphen = true
		}
	}

	normalized := strings.Trim(builder.String(), "-")
	if normalized == "" {
		return "heading"
	}
	return normalized
}

func uniqueHeadingID(base string, used map[string]struct{}) string {
	if strings.TrimSpace(base) == "" {
		base = "heading"
	}
	if _, ok := used[base]; !ok {
		used[base] = struct{}{}
		return base
	}

	for index := 1; ; index++ {
		candidate := base + "-" + strconv.Itoa(index)
		if _, ok := used[candidate]; ok {
			continue
		}
		used[candidate] = struct{}{}
		return candidate
	}
}

func normalizeHeadingWhitespace(value string) string {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return ""
	}
	return strings.Join(fields, " ")
}

func isHeadingASCIIControl(r rune) bool {
	return (r >= 0 && r < 0x20) || r == 0x7f
}

type headingTextCollector struct {
	builder            strings.Builder
	pendingSpace       bool
	htmlStack          []headingHTMLTagState
	invisibleHTMLDepth int
}

func newHeadingTextCollector() *headingTextCollector {
	return &headingTextCollector{}
}

func (c *headingTextCollector) String() string {
	return normalizeHeadingWhitespace(c.builder.String())
}

func (c *headingTextCollector) space() {
	if c == nil || c.builder.Len() == 0 {
		return
	}
	c.pendingSpace = true
}

func (c *headingTextCollector) appendText(value string) {
	if c == nil || value == "" {
		return
	}
	if c.pendingSpace && c.builder.Len() > 0 {
		c.builder.WriteByte(' ')
	}
	c.pendingSpace = false
	c.builder.WriteString(value)
}

func (c *headingTextCollector) appendSourceText(value string) {
	if c == nil || value == "" || c.inInvisibleRawHTML() {
		return
	}
	c.appendText(html.UnescapeString(value))
}

func (c *headingTextCollector) appendCodeSpanText(node *gast.CodeSpan, source []byte) {
	if c == nil || node == nil || c.inInvisibleRawHTML() {
		return
	}

	var builder strings.Builder
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		textNode, ok := child.(*gast.Text)
		if !ok {
			continue
		}

		value := textNode.Segment.Value(source)
		if len(value) > 0 && value[len(value)-1] == '\n' {
			_, _ = builder.Write(value[:len(value)-1])
			_ = builder.WriteByte(' ')
			continue
		}
		_, _ = builder.Write(value)
	}

	c.appendText(builder.String())
}

func (c *headingTextCollector) inInvisibleRawHTML() bool {
	return c != nil && c.invisibleHTMLDepth > 0
}

func (c *headingTextCollector) applyRawHTML(fragment string) {
	if c == nil || fragment == "" {
		return
	}

	tokenizer := xhtml.NewTokenizer(strings.NewReader(fragment))
	for {
		switch tokenType := tokenizer.Next(); tokenType {
		case xhtml.ErrorToken:
			if err := tokenizer.Err(); err != nil && err != io.EOF {
				return
			}
			return
		case xhtml.TextToken:
			if !c.inInvisibleRawHTML() {
				c.appendText(string(tokenizer.Text()))
			}
		case xhtml.StartTagToken, xhtml.SelfClosingTagToken:
			token := tokenizer.Token()
			tag := headingHTMLTagToken{name: strings.ToLower(token.Data), selfClosing: tokenType == xhtml.SelfClosingTagToken}
			if tag.name == "" {
				continue
			}

			invisible := c.inInvisibleRawHTML() || headingHTMLTagHidesText(tag.name, token.Attr)
			if !invisible && headingHTMLTagBreaksText(tag) {
				c.space()
			}
			if headingHTMLTagIsVoid(tag.name) {
				continue
			}
			if tag.selfClosing {
				continue
			}

			c.htmlStack = append(c.htmlStack, headingHTMLTagState{name: tag.name, invisible: invisible})
			if invisible {
				c.invisibleHTMLDepth++
			}
		case xhtml.EndTagToken:
			token := tokenizer.Token()
			tag := headingHTMLTagToken{name: strings.ToLower(token.Data), closing: true}
			if tag.name == "" {
				continue
			}

			if !c.inInvisibleRawHTML() && headingHTMLTagBreaksText(tag) {
				c.space()
			}

			var removedInvisible int
			c.htmlStack, removedInvisible = closeHeadingHTMLTagState(c.htmlStack, tag.name)
			c.invisibleHTMLDepth -= removedInvisible
			if c.invisibleHTMLDepth < 0 {
				c.invisibleHTMLDepth = 0
			}
		}
	}
}

type headingHTMLTagToken struct {
	name        string
	closing     bool
	selfClosing bool
}

type headingHTMLTagState struct {
	name      string
	invisible bool
}

var headingTextBoundaryTags = map[string]struct{}{
	"address":    {},
	"article":    {},
	"aside":      {},
	"blockquote": {},
	"br":         {},
	"caption":    {},
	"dd":         {},
	"div":        {},
	"dl":         {},
	"dt":         {},
	"figcaption": {},
	"figure":     {},
	"footer":     {},
	"form":       {},
	"h1":         {},
	"h2":         {},
	"h3":         {},
	"h4":         {},
	"h5":         {},
	"h6":         {},
	"header":     {},
	"hr":         {},
	"li":         {},
	"main":       {},
	"nav":        {},
	"ol":         {},
	"p":          {},
	"pre":        {},
	"section":    {},
	"table":      {},
	"tbody":      {},
	"td":         {},
	"tfoot":      {},
	"th":         {},
	"thead":      {},
	"tr":         {},
	"ul":         {},
}

var headingInvisibleTextTags = map[string]struct{}{
	"script":   {},
	"style":    {},
	"template": {},
}

var headingHTMLVoidTags = map[string]struct{}{
	"area":   {},
	"base":   {},
	"br":     {},
	"col":    {},
	"embed":  {},
	"hr":     {},
	"img":    {},
	"input":  {},
	"link":   {},
	"meta":   {},
	"param":  {},
	"source": {},
	"track":  {},
	"wbr":    {},
}

func headingHTMLTagBreaksText(token headingHTMLTagToken) bool {
	_, ok := headingTextBoundaryTags[token.name]
	return ok
}

func headingHTMLTagIsVoid(name string) bool {
	_, ok := headingHTMLVoidTags[name]
	return ok
}

func headingHTMLTagHidesText(name string, attrs []xhtml.Attribute) bool {
	if _, ok := headingInvisibleTextTags[name]; ok {
		return true
	}

	for _, attr := range attrs {
		if strings.EqualFold(attr.Key, "hidden") {
			return true
		}
	}

	return false
}

func closeHeadingHTMLTagState(stack []headingHTMLTagState, name string) ([]headingHTMLTagState, int) {
	if len(stack) == 0 || name == "" {
		return stack, 0
	}

	for index := len(stack) - 1; index >= 0; index-- {
		if stack[index].name != name {
			continue
		}

		removedInvisible := 0
		for _, state := range stack[index:] {
			if state.invisible {
				removedInvisible++
			}
		}

		return stack[:index], removedInvisible
	}

	return stack, 0
}
