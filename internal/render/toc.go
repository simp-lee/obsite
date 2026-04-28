package render

import (
	"strings"

	"github.com/simp-lee/obsite/internal/model"
	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

type tocStackEntry struct {
	level int
	entry *model.TOCEntry
}

func buildTOC(headings []model.Heading, pageTitle string, titleID string, omitLeadingTitle bool) []model.TOCEntry {
	usable := make([]model.Heading, 0, len(headings))
	normalizedTitle := normalizeHeadingText(pageTitle)
	trimmedTitleID := strings.TrimSpace(titleID)

	for index, heading := range headings {
		text := strings.TrimSpace(heading.Text)
		id := strings.TrimSpace(heading.ID)
		if heading.Level <= 0 || text == "" || id == "" {
			continue
		}

		if omitLeadingTitle && index == 0 && heading.Level == 1 && tocHeadingMatchesPromotedTitle(text, id, normalizedTitle, trimmedTitleID) {
			continue
		}

		usable = append(usable, model.Heading{
			Level: heading.Level,
			Text:  text,
			ID:    id,
		})
	}

	if len(usable) == 0 {
		return nil
	}

	root := make([]model.TOCEntry, 0, len(usable))
	stack := make([]tocStackEntry, 0, len(usable))
	for _, heading := range usable {
		entry := model.TOCEntry{Text: heading.Text, ID: heading.ID}

		for len(stack) > 0 && heading.Level <= stack[len(stack)-1].level {
			stack = stack[:len(stack)-1]
		}

		if len(stack) == 0 {
			root = append(root, entry)
			stack = append(stack, tocStackEntry{level: heading.Level, entry: &root[len(root)-1]})
			continue
		}

		parent := stack[len(stack)-1].entry
		parent.Children = append(parent.Children, entry)
		stack = append(stack, tocStackEntry{level: heading.Level, entry: &parent.Children[len(parent.Children)-1]})
	}

	return root
}

func tocHeadingsFromHTML(content string) []model.Heading {
	if strings.TrimSpace(content) == "" {
		return nil
	}

	nodes, err := parseHTMLFragment(content)
	if err != nil {
		return nil
	}

	headings := make([]model.Heading, 0, len(nodes))
	for _, node := range nodes {
		collectTOCHeadings(node, &headings)
	}
	if len(headings) == 0 {
		return nil
	}

	return headings
}

func collectTOCHeadings(node *xhtml.Node, headings *[]model.Heading) {
	if node == nil {
		return
	}

	if node.Type == xhtml.ElementNode {
		if shouldSkipVisibleTextNode(node) {
			return
		}
		if htmlNodeMatchesTag(node, atom.Details, "details") && !htmlNodeHasAttr(node, "open") {
			collectClosedDetailsTOCHeadings(node, headings)
			return
		}

		if level, ok := htmlHeadingLevel(node); ok {
			text := visibleHeadingTextFromHTMLNode(node)
			id := strings.TrimSpace(htmlNodeAttrValue(node, "id"))
			if text != "" && id != "" {
				*headings = append(*headings, model.Heading{Level: level, Text: text, ID: id})
			}
		}
	}

	for child := node.FirstChild; child != nil; child = child.NextSibling {
		collectTOCHeadings(child, headings)
	}
}

func collectClosedDetailsTOCHeadings(node *xhtml.Node, headings *[]model.Heading) {
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == xhtml.ElementNode && htmlNodeMatchesTag(child, atom.Summary, "summary") {
			collectTOCHeadings(child, headings)
		}
	}
}

func visibleHeadingTextFromHTMLNode(node *xhtml.Node) string {
	if node == nil {
		return ""
	}

	extractor := visibleTextExtractor{}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		extractor.walk(child)
	}

	return strings.Join(strings.Fields(extractor.String()), " ")
}

func htmlHeadingLevel(node *xhtml.Node) (int, bool) {
	if node == nil || node.Type != xhtml.ElementNode {
		return 0, false
	}

	switch {
	case htmlNodeMatchesTag(node, atom.H1, "h1"):
		return 1, true
	case htmlNodeMatchesTag(node, atom.H2, "h2"):
		return 2, true
	case htmlNodeMatchesTag(node, atom.H3, "h3"):
		return 3, true
	case htmlNodeMatchesTag(node, atom.H4, "h4"):
		return 4, true
	case htmlNodeMatchesTag(node, atom.H5, "h5"):
		return 5, true
	case htmlNodeMatchesTag(node, atom.H6, "h6"):
		return 6, true
	default:
		return 0, false
	}
}

func htmlNodeAttrValue(node *xhtml.Node, key string) string {
	if node == nil {
		return ""
	}

	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, key) {
			return attr.Val
		}
	}

	return ""
}

func tocHeadingMatchesPromotedTitle(text string, id string, normalizedTitle string, titleID string) bool {
	if titleID != "" && strings.TrimSpace(id) == titleID {
		return true
	}

	if normalizedTitle == "" {
		return false
	}

	return strings.EqualFold(normalizeHeadingText(text), normalizedTitle)
}
