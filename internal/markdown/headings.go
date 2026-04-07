package markdown

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/simp-lee/obsite/internal/markdown/math"
	"github.com/simp-lee/obsite/internal/model"
	gast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	gmhashtag "go.abhg.dev/goldmark/hashtag"
	gmwikilink "go.abhg.dev/goldmark/wikilink"
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
		collector.appendText(string(current.Value(source)))
		if current.SoftLineBreak() || current.HardLineBreak() {
			collector.space()
		}
	case *gast.String:
		collector.appendText(string(current.Value))
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
	builder      strings.Builder
	pendingSpace bool
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

func (c *headingTextCollector) applyRawHTML(fragment string) {
	if c == nil || fragment == "" {
		return
	}
	for _, token := range parseHeadingHTMLTokens(fragment) {
		if headingHTMLTagBreaksText(token) {
			c.space()
		}
	}
}

type headingHTMLTagToken struct {
	name        string
	closing     bool
	selfClosing bool
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

func headingHTMLTagBreaksText(token headingHTMLTagToken) bool {
	_, ok := headingTextBoundaryTags[token.name]
	return ok
}

func parseHeadingHTMLTokens(fragment string) []headingHTMLTagToken {
	tokens := make([]headingHTMLTagToken, 0)
	for index := 0; index < len(fragment); {
		open := strings.IndexByte(fragment[index:], '<')
		if open < 0 {
			break
		}
		open += index
		next, token, ok := nextHeadingHTMLTagToken(fragment, open)
		if !ok {
			index = open + 1
			continue
		}
		tokens = append(tokens, token)
		index = next
	}
	return tokens
}

func nextHeadingHTMLTagToken(fragment string, start int) (int, headingHTMLTagToken, bool) {
	if start < 0 || start >= len(fragment) || fragment[start] != '<' {
		return 0, headingHTMLTagToken{}, false
	}
	if strings.HasPrefix(fragment[start:], "<!--") {
		end := strings.Index(fragment[start+4:], "-->")
		if end < 0 {
			return len(fragment), headingHTMLTagToken{}, true
		}
		return start + 4 + end + 3, headingHTMLTagToken{}, true
	}

	end, ok := findHeadingTagEnd(fragment, start+1)
	if !ok {
		return 0, headingHTMLTagToken{}, false
	}

	inner := strings.TrimSpace(fragment[start+1 : end])
	if inner == "" || inner[0] == '!' || inner[0] == '?' {
		return end + 1, headingHTMLTagToken{}, true
	}

	token := headingHTMLTagToken{}
	if inner[0] == '/' {
		token.closing = true
		inner = strings.TrimSpace(inner[1:])
	}
	if strings.HasSuffix(inner, "/") {
		token.selfClosing = true
		inner = strings.TrimSpace(inner[:len(inner)-1])
	}
	if inner == "" {
		return end + 1, token, true
	}

	nameEnd := 0
	for nameEnd < len(inner) {
		r, size := utf8.DecodeRuneInString(inner[nameEnd:])
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == ':' || r == '-' || r == '_') {
			break
		}
		nameEnd += size
	}
	if nameEnd == 0 {
		return end + 1, token, true
	}

	token.name = strings.ToLower(inner[:nameEnd])
	return end + 1, token, true
}

func findHeadingTagEnd(fragment string, start int) (int, bool) {
	quote := rune(0)
	for index := start; index < len(fragment); index++ {
		current := rune(fragment[index])
		switch {
		case quote != 0 && current == quote:
			quote = 0
		case quote == 0 && (current == '\'' || current == '"'):
			quote = current
		case quote == 0 && current == '>':
			return index, true
		}
	}
	return 0, false
}
