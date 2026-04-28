package render

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/simp-lee/obsite/internal/diag"
	internalfsutil "github.com/simp-lee/obsite/internal/fsutil"
	"github.com/simp-lee/obsite/internal/model"
	"github.com/simp-lee/obsite/internal/seo"
	templateassets "github.com/simp-lee/obsite/templates"
)

const (
	baseTemplateName     = "base"
	indexOutputPath      = "index.html"
	notFoundOutputPath   = "404.html"
	notFoundTitle        = "Not found"
	notFoundDescription  = "The requested page could not be found."
	summaryRuneLimit     = 150
	tagsRootOutputPrefix = "tags"
)

var embeddedTemplateAssetNames = []string{
	"base.html",
	"note.html",
	"index.html",
	"tag.html",
	"folder.html",
	"timeline.html",
	"404.html",
	"style.css",
	"katex.min.css",
	"katex.min.js",
	"auto-render.min.js",
	"mermaid.esm.min.mjs",
}

var embeddedHTMLTemplateNames = htmlTemplateAssetNames(embeddedTemplateAssetNames)

// RequiredHTMLTemplateNames lists the HTML template files every external theme must provide.
var RequiredHTMLTemplateNames = []string{
	"base.html",
	"note.html",
	"index.html",
	"tag.html",
	"folder.html",
	"timeline.html",
	"404.html",
}

type embeddedOutputAsset struct {
	name       string
	outputPath string
}

var runtimeTemplateAssets = []embeddedOutputAsset{
	{name: "katex.min.css", outputPath: "assets/obsite-runtime/katex.min.css"},
	{name: "katex.min.js", outputPath: "assets/obsite-runtime/katex.min.js"},
	{name: "auto-render.min.js", outputPath: "assets/obsite-runtime/auto-render.min.js"},
	{name: "mermaid.esm.min.mjs", outputPath: "assets/obsite-runtime/mermaid.esm.min.mjs"},
}

var parseDefaultTemplates = sync.OnceValues(func() (*template.Template, error) {
	return parseEmbeddedTemplates()
})

var (
	templateSetCache           sync.Map
	themeTemplateSnapshotCache sync.Map
)

var themeTemplateFileReader = struct {
	mu   sync.RWMutex
	read func(string) ([]byte, error)
}{
	read: os.ReadFile,
}

type themeIdentity struct {
	activeThemeName string
	themeRoot       string
}

type templateSetCacheKey struct {
	activeThemeName string
	themeRoot       string
	signature       string
}

type themeTemplateFile struct {
	path     string
	contents string
}

type themeTemplateSnapshot struct {
	identity  themeIdentity
	files     []themeTemplateFile
	signature string
}

type themeTemplateFileState struct {
	name            string
	path            string
	exists          bool
	size            int64
	modTimeUnixNano int64
	changeToken     string
}

type themeTemplateState struct {
	identity themeIdentity
	files    []themeTemplateFileState
}

type cachedThemeTemplateSnapshot struct {
	mu       sync.Mutex
	state    themeTemplateState
	snapshot themeTemplateSnapshot
	ready    bool
}

func parseEmbeddedTemplates() (*template.Template, error) {
	return template.New(baseTemplateName).Funcs(template.FuncMap{
		"toJSON":       templateJSON,
		"pageAssetURL": pageAssetURL,
		"siteBasePath": siteBasePath,
	}).ParseFS(templateassets.FS, embeddedHTMLTemplateNames...)
}

// RenderedPage is the rendered HTML plus the PageData used to execute it.
type RenderedPage struct {
	Page        model.PageData
	HTML        []byte
	Diagnostics []diag.Diagnostic
}

// NotePageInput supplies the data needed to render a note page.
type NotePageInput struct {
	Site            model.SiteConfig
	Note            *model.Note
	Tags            []model.TagLink
	Backlinks       []model.BacklinkEntry
	RelatedArticles []model.RelatedArticle
	Breadcrumbs     []model.Breadcrumb
	SidebarTree     []model.SidebarNode
	HasSearch       bool
}

// TagPageInput supplies the data needed to render a tag archive page.
type TagPageInput struct {
	Site         model.SiteConfig
	Tag          *model.Tag
	ChildTags    []model.TagLink
	Notes        []model.NoteSummary
	Breadcrumbs  []model.Breadcrumb
	LastModified time.Time
	RelPath      string
	Pagination   *model.PaginationData
	SidebarTree  []model.SidebarNode
	HasSearch    bool
}

// FolderPageInput supplies the data needed to render a folder listing page.
type FolderPageInput struct {
	Site         model.SiteConfig
	FolderPath   string
	Children     []model.NoteSummary
	Breadcrumbs  []model.Breadcrumb
	LastModified time.Time
	RelPath      string
	Pagination   *model.PaginationData
	SidebarTree  []model.SidebarNode
	HasSearch    bool
}

// TimelinePageInput supplies the data needed to render a recent-notes timeline page.
type TimelinePageInput struct {
	Site         model.SiteConfig
	TimelinePath string
	Notes        []model.NoteSummary
	LastModified time.Time
	AsHomepage   bool
	RelPath      string
	Pagination   *model.PaginationData
	SidebarTree  []model.SidebarNode
	HasSearch    bool
}

// IndexPageInput supplies the data needed to render the index page.
type IndexPageInput struct {
	Site         model.SiteConfig
	RecentNotes  []model.NoteSummary
	LastModified time.Time
	RelPath      string
	Pagination   *model.PaginationData
	SidebarTree  []model.SidebarNode
	HasSearch    bool
}

// NotFoundPageInput supplies the data needed to render the 404 page.
type NotFoundPageInput struct {
	Site         model.SiteConfig
	RecentNotes  []model.NoteSummary
	LastModified time.Time
	SidebarTree  []model.SidebarNode
	HasSearch    bool
}

// RenderNote renders a note page to HTML using the embedded default templates.
func RenderNote(input NotePageInput) (RenderedPage, error) {
	if input.Note == nil {
		return RenderedPage{}, errors.New("render note: note is required")
	}

	slug := normalizeSlug(input.Note.Slug)
	if slug == "" {
		return RenderedPage{}, errors.New("render note: note slug is required")
	}

	content, titleID := normalizeNoteContent(input.Note)
	summary := strings.TrimSpace(input.Note.Summary)
	if summary == "" {
		summary = VisibleSummary(input.Note)
	}
	latinWords, cjkChars := CountWords(content)
	relPath := cleanURLPath(slug + "/index.html")
	displayTitle := noteDisplayTitle(input.Note)
	tocHeadings := tocHeadingsFromHTML(content)
	omitLeadingTitle := false
	if len(tocHeadings) == 0 {
		tocHeadings = append([]model.Heading(nil), input.Note.Headings...)
		omitLeadingTitle = content != input.Note.HTMLContent
	}
	page := model.PageData{
		Kind:            model.PageNote,
		Site:            input.Site,
		SiteRootRel:     siteRootRel(relPath),
		Title:           strings.TrimSpace(input.Note.Frontmatter.Title),
		TitleID:         titleID,
		Description:     strings.TrimSpace(input.Note.Frontmatter.Description),
		Slug:            slug,
		RelPath:         relPath,
		Content:         template.HTML(content),
		Date:            input.Note.Frontmatter.Date,
		LastModified:    input.Note.LastModified,
		ReadingTime:     FormatReadingTime(latinWords, cjkChars),
		WordCount:       latinWords + cjkChars,
		TOC:             buildTOC(tocHeadings, displayTitle, titleID, omitLeadingTitle),
		Tags:            cloneTagLinks(input.Tags),
		Backlinks:       cloneBacklinks(input.Backlinks),
		RelatedArticles: cloneRelatedArticles(input.RelatedArticles),
		HasMath:         input.Note.HasMath,
		HasMermaid:      input.Note.HasMermaid,
		HasSearch:       input.HasSearch,
		HasCustomCSS:    hasCustomCSS(input.Site),
		Breadcrumbs:     defaultNoteBreadcrumbs(input.Breadcrumbs, relPath, input.Note, displayTitle),
		SidebarTree:     cloneSidebarTree(input.SidebarTree),
	}
	if page.Description == "" {
		page.Description = summary
	}

	return renderPage(page, input.Note)
}

// CountWords approximates reading units from visible rendered HTML content.
// Latin text is counted by whitespace-delimited words and CJK text by characters.
func CountWords(htmlContent string) (latinWords, cjkChars int) {
	text := visibleTextFromHTML(htmlContent)
	if text == "" {
		return 0, 0
	}

	for _, token := range strings.Fields(text) {
		hasLatinWord := false
		for _, r := range token {
			switch {
			case isCJKRune(r):
				cjkChars++
			case unicode.IsLetter(r) || unicode.IsDigit(r):
				hasLatinWord = true
			}
		}
		if hasLatinWord {
			latinWords++
		}
	}

	return latinWords, cjkChars
}

// VisibleSummary derives a fallback summary from a note's normalized visible HTML content.
func VisibleSummary(note *model.Note) string {
	if note == nil {
		return ""
	}

	content, _ := normalizeNoteContent(note)
	return visibleSummaryFromHTML(content)
}

func visibleSummaryFromHTML(htmlContent string) string {
	text := visibleTextFromHTMLWithOptions(htmlContent, visibleTextOptions{skipBlockCode: true})
	if text == "" {
		return ""
	}

	return truncateVisibleSummary(text, summaryRuneLimit)
}

// FormatReadingTime renders a human-readable reading-time label.
func FormatReadingTime(latinWords, cjkChars int) string {
	weightedUnits := (latinWords * 2) + cjkChars
	if weightedUnits <= 0 {
		return ""
	}

	minutes := weightedUnits / 400
	if weightedUnits%400 != 0 {
		minutes++
	}

	return fmt.Sprintf("%d min read", minutes)
}

func truncateVisibleSummary(value string, limit int) string {
	value = strings.TrimSpace(value)
	if value == "" || limit <= 0 {
		return ""
	}
	if utf8.RuneCountInString(value) <= limit {
		return value
	}

	lastBoundary := 0
	firstBoundaryAfterLimit := 0
	runeCount := 0
	for index, r := range value {
		runeCount++
		end := index + utf8.RuneLen(r)
		if isVisibleSummaryBoundaryRune(r) {
			if runeCount <= limit {
				lastBoundary = end
			} else {
				firstBoundaryAfterLimit = end
				break
			}
		}

		if runeCount == limit && lastBoundary > 0 {
			return strings.TrimSpace(value[:lastBoundary])
		}
	}

	if lastBoundary > 0 {
		return strings.TrimSpace(value[:lastBoundary])
	}
	if firstBoundaryAfterLimit > 0 {
		return strings.TrimSpace(value[:firstBoundaryAfterLimit])
	}

	return value
}

func isVisibleSummaryBoundaryRune(r rune) bool {
	if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
		return true
	}
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
}

func visibleTextFromHTML(htmlContent string) string {
	return visibleTextFromHTMLWithOptions(htmlContent, visibleTextOptions{})
}

type visibleTextOptions struct {
	skipBlockCode bool
}

func visibleTextFromHTMLWithOptions(htmlContent string, options visibleTextOptions) string {
	if strings.TrimSpace(htmlContent) == "" {
		return ""
	}

	nodes, err := parseHTMLFragment(htmlContent)
	if err != nil {
		return ""
	}

	extractor := visibleTextExtractor{options: options}
	for _, node := range nodes {
		extractor.walk(node)
	}

	return strings.Join(strings.Fields(extractor.String()), " ")
}

type visibleTextExtractor struct {
	options visibleTextOptions
	builder strings.Builder
}

func (e *visibleTextExtractor) String() string {
	return e.builder.String()
}

func (e *visibleTextExtractor) walk(node *xhtml.Node) {
	if node == nil {
		return
	}

	switch node.Type {
	case xhtml.TextNode:
		e.builder.WriteString(node.Data)
		return
	case xhtml.ElementNode:
		if shouldSkipVisibleTextNode(node) {
			return
		}
		if e.options.skipBlockCode && shouldSkipVisibleSummaryNode(node) {
			return
		}

		separator := isVisibleTextSeparator(node.Data)
		if separator {
			e.builder.WriteByte(' ')
		}

		if node.Data == "details" && !htmlNodeHasAttr(node, "open") {
			e.walkClosedDetailsSummary(node)
			if separator {
				e.builder.WriteByte(' ')
			}
			return
		}

		if node.Data == "br" || node.Data == "hr" {
			return
		}

		for child := node.FirstChild; child != nil; child = child.NextSibling {
			e.walk(child)
		}

		if separator {
			e.builder.WriteByte(' ')
		}
		return
	}

	for child := node.FirstChild; child != nil; child = child.NextSibling {
		e.walk(child)
	}
}

func (e *visibleTextExtractor) walkClosedDetailsSummary(node *xhtml.Node) {
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == xhtml.ElementNode && child.Data == "summary" {
			e.walk(child)
		}
	}
}

func parseHTMLFragment(htmlContent string) ([]*xhtml.Node, error) {
	context := &xhtml.Node{Type: xhtml.ElementNode, DataAtom: atom.Div, Data: "div"}
	return xhtml.ParseFragment(strings.NewReader(htmlContent), context)
}

func shouldSkipVisibleTextNode(node *xhtml.Node) bool {
	return isInvisibleHTMLContent(node.Data, node.DataAtom, htmlNodeHasAttr(node, "hidden"))
}

func shouldSkipVisibleSummaryNode(node *xhtml.Node) bool {
	if node == nil || node.Type != xhtml.ElementNode {
		return false
	}

	if htmlNodeMatchesTag(node, atom.Div, "div") && htmlNodeHasClass(node, "chroma") {
		return true
	}

	if !htmlNodeMatchesTag(node, atom.Pre, "pre") {
		return false
	}

	if htmlNodeHasClass(node, "mermaid") ||
		htmlNodeHasClass(node, "unsupported-syntax") ||
		htmlNodeHasClassPrefix(node, "language-") ||
		htmlNodeHasAttr(node, "tabindex") {
		return true
	}

	return htmlNodeHasDescendantTag(node, atom.Code, "code")
}

func htmlNodeMatchesTag(node *xhtml.Node, dataAtom atom.Atom, tag string) bool {
	if node == nil {
		return false
	}

	if node.DataAtom == dataAtom {
		return true
	}

	return strings.EqualFold(strings.TrimSpace(node.Data), tag)
}

func htmlNodeHasClass(node *xhtml.Node, className string) bool {
	return htmlNodeHasClassMatch(node, className, true)
}

func htmlNodeHasClassPrefix(node *xhtml.Node, prefix string) bool {
	return htmlNodeHasClassMatch(node, prefix, false)
}

func htmlNodeHasClassMatch(node *xhtml.Node, prefix string, exact bool) bool {
	if node == nil || strings.TrimSpace(prefix) == "" {
		return false
	}

	for _, attr := range node.Attr {
		if !strings.EqualFold(attr.Key, "class") {
			continue
		}
		for _, value := range strings.Fields(attr.Val) {
			if exact {
				if value == prefix {
					return true
				}
				continue
			}
			if strings.HasPrefix(value, prefix) {
				return true
			}
		}
	}

	return false
}

func htmlNodeHasDescendantTag(node *xhtml.Node, dataAtom atom.Atom, tag string) bool {
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == xhtml.ElementNode && htmlNodeMatchesTag(child, dataAtom, tag) {
			return true
		}
		if htmlNodeHasDescendantTag(child, dataAtom, tag) {
			return true
		}
	}

	return false
}

func htmlNodeHasAttr(node *xhtml.Node, key string) bool {
	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, key) {
			return true
		}
	}

	return false
}

func isVisibleTextSeparator(tag string) bool {
	switch tag {
	case "address", "article", "aside", "blockquote", "body", "br", "caption", "dd", "details", "div", "dl", "dt", "figcaption", "figure", "footer", "form", "h1", "h2", "h3", "h4", "h5", "h6", "header", "hr", "legend", "li", "main", "nav", "ol", "p", "pre", "section", "summary", "table", "tbody", "td", "tfoot", "th", "thead", "tr", "ul":
		return true
	default:
		return false
	}
}

func normalizeNoteContent(note *model.Note) (string, string) {
	if note == nil {
		return "", ""
	}

	content := note.HTMLContent
	firstHeading, ok := redundantLeadingHeading(note)
	if !ok {
		return content, ""
	}

	normalizedContent, ok := stripPromotedLeadingH1(content, firstHeading.ID)
	if !ok {
		return content, ""
	}

	return normalizedContent, strings.TrimSpace(firstHeading.ID)
}

func stripPromotedLeadingH1(content string, headingID string) (string, bool) {
	tokenizer := xhtml.NewTokenizer(strings.NewReader(content))
	offset := 0
	preservedPrefixStart := -1
	preservedPrefixEnd := -1
	headingStart := -1
	headingDepth := 0
	invisiblePreludeStack := make([]string, 0, 2)

	markPreservedPrefix := func(start int, end int) {
		if preservedPrefixStart < 0 {
			preservedPrefixStart = start
		}
		preservedPrefixEnd = end
	}

	for {
		tokenType := tokenizer.Next()
		start := offset
		offset += len(tokenizer.Raw())
		end := offset

		switch tokenType {
		case xhtml.ErrorToken:
			return "", false
		case xhtml.TextToken:
			if headingStart >= 0 {
				continue
			}
			if len(invisiblePreludeStack) > 0 {
				markPreservedPrefix(start, end)
				continue
			}
			if len(bytes.TrimSpace(tokenizer.Text())) == 0 {
				if preservedPrefixStart >= 0 {
					preservedPrefixEnd = end
				}
				continue
			}
			return "", false
		case xhtml.CommentToken, xhtml.DoctypeToken:
			if headingStart >= 0 {
				continue
			}
			markPreservedPrefix(start, end)
		case xhtml.StartTagToken, xhtml.SelfClosingTagToken:
			token := tokenizer.Token()
			if headingStart >= 0 {
				if token.DataAtom == atom.H1 {
					headingDepth++
				}
				continue
			}
			if len(invisiblePreludeStack) > 0 {
				markPreservedPrefix(start, end)
				if tokenType == xhtml.StartTagToken && !isHTMLVoidElement(token.Data) {
					invisiblePreludeStack = append(invisiblePreludeStack, token.Data)
				}
				continue
			}
			if isInvisibleLeadingPreludeToken(token) {
				markPreservedPrefix(start, end)
				if tokenType == xhtml.StartTagToken && !isHTMLVoidElement(token.Data) {
					invisiblePreludeStack = append(invisiblePreludeStack, token.Data)
				}
				continue
			}
			if token.DataAtom != atom.H1 {
				return "", false
			}
			if !tokenHasAttributeValue(token, "id", headingID) {
				return "", false
			}
			headingStart = start
			headingDepth = 1
		case xhtml.EndTagToken:
			token := tokenizer.Token()
			if headingStart < 0 {
				if len(invisiblePreludeStack) > 0 {
					markPreservedPrefix(start, end)
					if strings.EqualFold(invisiblePreludeStack[len(invisiblePreludeStack)-1], token.Data) {
						invisiblePreludeStack = invisiblePreludeStack[:len(invisiblePreludeStack)-1]
					}
					continue
				}
				return "", false
			}
			if token.DataAtom != atom.H1 {
				continue
			}
			headingDepth--
			if headingDepth != 0 {
				continue
			}

			prefix := ""
			if preservedPrefixStart >= 0 && preservedPrefixEnd >= preservedPrefixStart {
				prefix = content[preservedPrefixStart:preservedPrefixEnd]
			}

			return prefix + strings.TrimLeftFunc(content[end:], unicode.IsSpace), true
		default:
			if headingStart < 0 {
				if len(invisiblePreludeStack) > 0 {
					markPreservedPrefix(start, end)
					continue
				}
				return "", false
			}
		}
	}
}

func isInvisibleLeadingPreludeToken(token xhtml.Token) bool {
	return isInvisibleHTMLContent(token.Data, token.DataAtom, tokenHasAttribute(token, "hidden"))
}

func isInvisibleHTMLContent(name string, dataAtom atom.Atom, hidden bool) bool {
	if hidden {
		return true
	}

	switch dataAtom {
	case atom.Style, atom.Script, atom.Template, atom.Meta, atom.Link:
		return true
	}

	switch strings.ToLower(strings.TrimSpace(name)) {
	case "style", "script", "template", "meta", "link":
		return true
	default:
		return false
	}
}

func tokenHasAttribute(token xhtml.Token, key string) bool {
	for _, attr := range token.Attr {
		if strings.EqualFold(attr.Key, key) {
			return true
		}
	}

	return false
}

func tokenHasAttributeValue(token xhtml.Token, key string, value string) bool {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return true
	}

	for _, attr := range token.Attr {
		if strings.EqualFold(attr.Key, key) && strings.TrimSpace(attr.Val) == trimmedValue {
			return true
		}
	}

	return false
}

func isHTMLVoidElement(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta", "param", "source", "track", "wbr":
		return true
	default:
		return false
	}
}

func redundantLeadingHeading(note *model.Note) (model.Heading, bool) {
	if note == nil || len(note.Headings) == 0 {
		return model.Heading{}, false
	}

	first := note.Headings[0]
	if first.Level != 1 {
		return model.Heading{}, false
	}

	displayTitle := strings.TrimSpace(noteDisplayTitle(note))
	if displayTitle == "" {
		return model.Heading{}, false
	}

	headingText := strings.TrimSpace(first.Text)
	if headingText == "" {
		return model.Heading{}, false
	}

	if leadingHeadingMatchesDisplayTitle(displayTitle, headingText, strings.TrimSpace(note.Frontmatter.Title) == "") {
		return first, true
	}

	return model.Heading{}, false
}

func leadingHeadingMatchesDisplayTitle(displayTitle string, headingText string, filenameFallback bool) bool {
	normalizedTitle := normalizeHeadingText(displayTitle)
	normalizedHeading := normalizeHeadingText(headingText)
	if normalizedTitle == "" || normalizedHeading == "" {
		return false
	}

	if strings.EqualFold(normalizedHeading, normalizedTitle) {
		return true
	}
	if !filenameFallback {
		return false
	}

	return strings.EqualFold(normalizeFilenameFallbackTitle(displayTitle), normalizeFilenameFallbackTitle(headingText))
}

func normalizeFilenameFallbackTitle(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}

	var builder strings.Builder
	pendingSpace := false
	for _, r := range trimmed {
		switch {
		case isFilenameFallbackSeparator(r):
			if builder.Len() == 0 {
				continue
			}
			pendingSpace = true
		default:
			if pendingSpace && builder.Len() > 0 {
				builder.WriteByte(' ')
			}
			builder.WriteRune(unicode.ToLower(r))
			pendingSpace = false
		}
	}

	return strings.TrimSpace(builder.String())
}

func isFilenameFallbackSeparator(r rune) bool {
	return unicode.IsSpace(r) || r == '-' || r == '_' || r == '.'
}

func normalizeHeadingText(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func isCJKRune(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
}

// RenderTagPage renders a tag archive page to HTML using the embedded default templates.
func RenderTagPage(input TagPageInput) (RenderedPage, error) {
	if input.Tag == nil {
		return RenderedPage{}, errors.New("render tag page: tag is required")
	}

	tagSlug := normalizeTagPageSlug(input.Tag.Slug)
	if tagSlug == "" {
		return RenderedPage{}, errors.New("render tag page: tag slug is required")
	}

	pageSlug := tagSlug
	relPath := resolvePageRelPath(input.RelPath, cleanURLPath(pageSlug+"/index.html"))
	page := model.PageData{
		Kind:         model.PageTag,
		Site:         input.Site,
		SiteRootRel:  siteRootRel(relPath),
		Title:        strings.TrimSpace(input.Tag.Name),
		Slug:         pageSlug,
		RelPath:      relPath,
		TagName:      strings.TrimSpace(input.Tag.Name),
		TagNotes:     cloneNoteSummaries(input.Notes),
		ChildTags:    cloneTagLinks(input.ChildTags),
		LastModified: input.LastModified,
		Pagination:   clonePagination(input.Pagination),
		HasSearch:    input.HasSearch,
		HasCustomCSS: hasCustomCSS(input.Site),
		Breadcrumbs:  defaultTagBreadcrumbs(input.Breadcrumbs, relPath, input.Tag),
		SidebarTree:  cloneSidebarTree(input.SidebarTree),
	}

	return renderPage(page, nil)
}

// RenderFolderPage renders a folder listing page to HTML using the embedded default templates.
func RenderFolderPage(input FolderPageInput) (RenderedPage, error) {
	folderPath := cleanURLPath(input.FolderPath)
	if folderPath == "" {
		return RenderedPage{}, errors.New("render folder page: folder path is required")
	}

	title := folderDisplayTitle(folderPath)
	if title == "" {
		return RenderedPage{}, errors.New("render folder page: folder title is required")
	}

	relPath := resolvePageRelPath(input.RelPath, cleanURLPath(folderPath+"/index.html"))
	page := model.PageData{
		Kind:           model.PageFolder,
		Site:           input.Site,
		SiteRootRel:    siteRootRel(relPath),
		Title:          title,
		Slug:           folderPath,
		RelPath:        relPath,
		FolderPath:     folderPath,
		FolderChildren: cloneNoteSummaries(input.Children),
		LastModified:   input.LastModified,
		Pagination:     clonePagination(input.Pagination),
		HasSearch:      input.HasSearch,
		HasCustomCSS:   hasCustomCSS(input.Site),
		Breadcrumbs:    defaultFolderBreadcrumbs(input.Breadcrumbs, relPath, folderPath, title),
		SidebarTree:    cloneSidebarTree(input.SidebarTree),
	}

	return renderPage(page, nil)
}

// RenderTimelinePage renders the recent-notes timeline page to HTML using the embedded default templates.
func RenderTimelinePage(input TimelinePageInput) (RenderedPage, error) {
	defaultRelPath := indexOutputPath
	slug := ""
	if !input.AsHomepage {
		slug = cleanURLPath(input.TimelinePath)
		if slug == "" {
			return RenderedPage{}, errors.New("render timeline page: timeline path is required")
		}
		defaultRelPath = cleanURLPath(slug + "/index.html")
	}
	relPath := resolvePageRelPath(input.RelPath, defaultRelPath)

	page := model.PageData{
		Kind:          model.PageTimeline,
		Site:          input.Site,
		SiteRootRel:   siteRootRel(relPath),
		Title:         "Recent notes",
		Slug:          slug,
		RelPath:       relPath,
		TimelineNotes: cloneNoteSummaries(input.Notes),
		LastModified:  input.LastModified,
		Pagination:    clonePagination(input.Pagination),
		HasSearch:     input.HasSearch,
		HasCustomCSS:  hasCustomCSS(input.Site),
		Breadcrumbs:   defaultTimelineBreadcrumbs(relPath, input.AsHomepage),
		SidebarTree:   cloneSidebarTree(input.SidebarTree),
	}

	return renderPage(page, nil)
}

// RenderIndex renders the site index page to HTML using the embedded default templates.
func RenderIndex(input IndexPageInput) (RenderedPage, error) {
	relPath := resolvePageRelPath(input.RelPath, indexOutputPath)
	page := model.PageData{
		Kind:         model.PageIndex,
		Site:         input.Site,
		SiteRootRel:  siteRootRel(relPath),
		Title:        strings.TrimSpace(input.Site.Title),
		Description:  strings.TrimSpace(input.Site.Description),
		RelPath:      relPath,
		RecentNotes:  cloneNoteSummaries(input.RecentNotes),
		LastModified: input.LastModified,
		Pagination:   clonePagination(input.Pagination),
		HasSearch:    input.HasSearch,
		HasCustomCSS: hasCustomCSS(input.Site),
		SidebarTree:  cloneSidebarTree(input.SidebarTree),
	}

	return renderPage(page, nil)
}

// Render404 renders the default 404 page to HTML using the embedded default templates.
func Render404(input NotFoundPageInput) (RenderedPage, error) {
	page := model.PageData{
		Kind:         model.Page404,
		Site:         input.Site,
		SiteRootRel:  siteRootRel(notFoundOutputPath),
		Title:        notFoundTitle,
		Description:  notFoundDescription,
		RelPath:      notFoundOutputPath,
		RecentNotes:  cloneNoteSummaries(input.RecentNotes),
		LastModified: input.LastModified,
		HasSearch:    input.HasSearch,
		HasCustomCSS: hasCustomCSS(input.Site),
		SidebarTree:  cloneSidebarTree(input.SidebarTree),
	}

	return renderPage(page, nil)
}

// ThemeStaticAsset describes one regular file owned by the selected theme root.
type ThemeStaticAsset struct {
	SourcePath        string
	ThemeRelativePath string
	OutputPath        string
}

// EmitStyleCSS writes the configured stylesheet into the output root.
// It returns whether style.css was actually written.
func EmitStyleCSS(outputRoot string, site model.SiteConfig) (bool, error) {
	if strings.TrimSpace(outputRoot) == "" {
		return false, errors.New("emit style.css: output root is required")
	}

	data, found, err := readStyleAsset(site)
	if err != nil {
		return false, fmt.Errorf("emit style.css: %w", err)
	}
	if !found {
		return false, nil
	}
	if err := os.MkdirAll(outputRoot, 0o755); err != nil {
		return false, fmt.Errorf("emit style.css: mkdir %q: %w", outputRoot, err)
	}
	if err := os.WriteFile(filepath.Join(outputRoot, "style.css"), data, 0o644); err != nil {
		return false, fmt.Errorf("emit style.css: write style.css: %w", err)
	}

	return true, nil
}

// ListThemeStaticAssets returns the stable inventory of regular, non-HTML files
// owned by the selected theme root. Theme-owned HTML is always treated as
// template input and is never emitted as a static asset.
func ListThemeStaticAssets(themeRoot string) ([]ThemeStaticAsset, error) {
	normalizedRoot, err := normalizeThemeRoot(themeRoot)
	if err != nil {
		return nil, err
	}
	if normalizedRoot == "" {
		return nil, nil
	}

	assets := make([]ThemeStaticAsset, 0)
	err = filepath.WalkDir(normalizedRoot, func(currentPath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk theme root %q: %w", normalizedRoot, walkErr)
		}
		if entry == nil || entry.IsDir() {
			return nil
		}

		relPath, err := themeOwnedRelativePath(normalizedRoot, currentPath)
		if err != nil {
			return fmt.Errorf("relative theme asset path %q: %w", currentPath, err)
		}
		if relPath == "style.css" {
			return nil
		}
		if isThemeHTMLTemplatePath(relPath) {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("theme static asset %q must be a regular non-symlink file", currentPath)
		}

		assets = append(assets, ThemeStaticAsset{
			SourcePath:        currentPath,
			ThemeRelativePath: relPath,
			OutputPath:        path.Join("assets", "theme", relPath),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	return assets, nil
}

// EmitThemeStaticAssets copies the provided theme-owned files into the output root.
func EmitThemeStaticAssets(outputRoot string, assets []ThemeStaticAsset) error {
	if strings.TrimSpace(outputRoot) == "" {
		return errors.New("emit theme static assets: output root is required")
	}

	for _, asset := range assets {
		data, err := os.ReadFile(asset.SourcePath)
		if err != nil {
			return fmt.Errorf("emit theme static assets: read %q: %w", asset.SourcePath, err)
		}

		assetPath := filepath.Join(outputRoot, filepath.FromSlash(asset.OutputPath))
		if err := os.MkdirAll(filepath.Dir(assetPath), 0o755); err != nil {
			return fmt.Errorf("emit theme static assets: mkdir %q: %w", filepath.Dir(assetPath), err)
		}
		if err := os.WriteFile(assetPath, data, 0o644); err != nil {
			return fmt.Errorf("emit theme static assets: write %q: %w", asset.OutputPath, err)
		}
	}

	return nil
}

func isThemeHTMLTemplatePath(relPath string) bool {
	return strings.EqualFold(path.Ext(relPath), ".html")
}

func themeOwnedRelativePath(themeRoot string, currentPath string) (string, error) {
	relPath, err := filepath.Rel(themeRoot, currentPath)
	if err != nil {
		return "", err
	}

	return filepath.ToSlash(relPath), nil
}

// EmitRuntimeAssets writes the embedded offline math and Mermaid runtime files into the output root.
func EmitRuntimeAssets(outputRoot string) error {
	if strings.TrimSpace(outputRoot) == "" {
		return errors.New("emit runtime assets: output root is required")
	}

	for _, asset := range runtimeTemplateAssets {
		data, err := readEmbeddedAsset(asset.name)
		if err != nil {
			return fmt.Errorf("emit runtime assets: %w", err)
		}

		assetPath := filepath.Join(outputRoot, filepath.FromSlash(asset.outputPath))
		if err := os.MkdirAll(filepath.Dir(assetPath), 0o755); err != nil {
			return fmt.Errorf("emit runtime assets: mkdir %q: %w", filepath.Dir(assetPath), err)
		}
		if err := os.WriteFile(assetPath, data, 0o644); err != nil {
			return fmt.Errorf("emit runtime assets: write %q: %w", asset.outputPath, err)
		}
	}

	return nil
}

// RuntimeAssetOutputPaths returns the output paths reserved for the embedded offline runtime assets.
func RuntimeAssetOutputPaths() []string {
	paths := make([]string, 0, len(runtimeTemplateAssets))
	for _, asset := range runtimeTemplateAssets {
		paths = append(paths, asset.outputPath)
	}

	return paths
}

func renderPage(page model.PageData, note *model.Note) (RenderedPage, error) {
	page.HasRSS = page.Site.EffectiveRSSEnabled()

	_, seoErr := seo.Apply(&page, note)
	pageDiagnostics, handledSEOWarning := nonFatalSEODiagnostics(note, seoErr)
	if handledSEOWarning {
		seoErr = nil
	}
	html, renderErr := executeTemplate(page)

	return RenderedPage{Page: page, HTML: html, Diagnostics: pageDiagnostics}, errors.Join(seoErr, renderErr)
}

func nonFatalSEODiagnostics(note *model.Note, err error) ([]diag.Diagnostic, bool) {
	var articleErr *seo.ArticleJSONLDError
	if !errors.As(err, &articleErr) {
		return nil, false
	}

	location := diag.Location{}
	if note != nil {
		location.Path = note.RelPath
	}

	return []diag.Diagnostic{{
		Severity: diag.SeverityWarning,
		Kind:     diag.KindStructuredData,
		Location: location,
		Message:  articleJSONLDWarningMessage(articleErr),
	}}, true
}

func articleJSONLDWarningMessage(articleErr *seo.ArticleJSONLDError) string {
	if articleErr == nil || len(articleErr.MissingFields) == 0 {
		return "article JSON-LD omitted because required fields are missing"
	}

	return fmt.Sprintf(
		"article JSON-LD omitted because required fields are missing: %s",
		strings.Join(articleErr.MissingFields, ", "),
	)
}

func executeTemplate(page model.PageData) ([]byte, error) {
	tmpl, err := loadTemplateSet(page.Site)
	if err != nil {
		return nil, fmt.Errorf("load templates: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, baseTemplateName, page); err != nil {
		return nil, fmt.Errorf("execute template %q: %w", baseTemplateName, err)
	}

	return buf.Bytes(), nil
}

func loadTemplateSet(site model.SiteConfig) (*template.Template, error) {
	identity, err := resolveThemeIdentity(site)
	if err != nil {
		return nil, err
	}
	if identity.themeRoot == "" {
		tmpl, err := parseDefaultTemplates()
		if err != nil {
			return nil, fmt.Errorf("parse default templates: %w", err)
		}

		return tmpl, nil
	}

	snapshot, err := loadThemeTemplateSnapshot(identity)
	if err != nil {
		return nil, err
	}

	key := snapshot.cacheKey()
	loader := cachedTemplateSetLoader(snapshot)
	tmpl, err := loader()
	if err != nil {
		templateSetCache.Delete(key)
		return nil, err
	}
	pruneTemplateSetCacheEntries(key)

	return tmpl, nil
}

func resolveThemeIdentity(site model.SiteConfig) (themeIdentity, error) {
	themeRoot, err := normalizeThemeRoot(site.ThemeRoot)
	if err != nil {
		return themeIdentity{}, err
	}

	return themeIdentity{
		activeThemeName: strings.TrimSpace(site.ActiveThemeName),
		themeRoot:       themeRoot,
	}, nil
}

func loadThemeTemplateSnapshot(identity themeIdentity) (themeTemplateSnapshot, error) {
	state, err := scanThemeTemplateState(identity)
	if err != nil {
		return themeTemplateSnapshot{}, err
	}

	entry, _ := themeTemplateSnapshotCache.LoadOrStore(identity, &cachedThemeTemplateSnapshot{})
	return entry.(*cachedThemeTemplateSnapshot).load(state)
}

func (cached *cachedThemeTemplateSnapshot) load(state themeTemplateState) (themeTemplateSnapshot, error) {
	cached.mu.Lock()
	defer cached.mu.Unlock()

	if cached.ready && themeTemplateStateMatches(cached.state, state) {
		return cached.snapshot, nil
	}

	snapshot, err := scanThemeTemplateSnapshotFromState(state)
	if err != nil {
		return themeTemplateSnapshot{}, err
	}

	cached.state = state
	cached.snapshot = snapshot
	cached.ready = true

	return snapshot, nil
}

func themeTemplateStateMatches(left themeTemplateState, right themeTemplateState) bool {
	if left.identity != right.identity || len(left.files) != len(right.files) {
		return false
	}

	for index := range left.files {
		if left.files[index] != right.files[index] {
			return false
		}
	}

	return true
}

func (snapshot themeTemplateSnapshot) cacheKey() templateSetCacheKey {
	return templateSetCacheKey{
		activeThemeName: snapshot.identity.activeThemeName,
		themeRoot:       snapshot.identity.themeRoot,
		signature:       snapshot.signature,
	}
}

func cachedTemplateSetLoader(snapshot themeTemplateSnapshot) func() (*template.Template, error) {
	key := snapshot.cacheKey()
	loader, _ := templateSetCache.LoadOrStore(key, sync.OnceValues(func() (*template.Template, error) {
		return parseTemplateSet(snapshot)
	}))

	return loader.(func() (*template.Template, error))
}

func pruneTemplateSetCacheEntries(activeKey templateSetCacheKey) {
	keys := make([]templateSetCacheKey, 0, 1)
	templateSetCache.Range(func(key, _ any) bool {
		cachedKey, ok := key.(templateSetCacheKey)
		if !ok {
			return true
		}
		if cachedKey.activeThemeName == activeKey.activeThemeName && cachedKey.themeRoot == activeKey.themeRoot && cachedKey.signature != activeKey.signature {
			keys = append(keys, cachedKey)
		}
		return true
	})
	for _, key := range keys {
		templateSetCache.Delete(key)
	}
}

func scanThemeTemplateState(identity themeIdentity) (themeTemplateState, error) {
	if err := validateRequiredThemeTemplateFiles(identity.themeRoot); err != nil {
		return themeTemplateState{}, err
	}

	files, err := listThemeTemplateFileStatesInRoot(identity.themeRoot)
	if err != nil {
		return themeTemplateState{}, err
	}

	return themeTemplateState{
		identity: identity,
		files:    files,
	}, nil
}

func validateRequiredThemeTemplateFiles(themeRoot string) error {
	missing := make([]string, 0, len(RequiredHTMLTemplateNames))
	for _, name := range RequiredHTMLTemplateNames {
		fileState, err := resolveThemeTemplateFileStateInRoot(themeRoot, name)
		if err != nil {
			return err
		}
		if !fileState.exists {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required theme templates in %q: %s", themeRoot, strings.Join(missing, ", "))
	}

	return nil
}

func listThemeTemplateFileStatesInRoot(themeRoot string) ([]themeTemplateFileState, error) {
	if themeRoot == "" {
		return nil, nil
	}

	files := make([]themeTemplateFileState, 0, len(RequiredHTMLTemplateNames))
	err := filepath.WalkDir(themeRoot, func(currentPath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk theme root %q: %w", themeRoot, walkErr)
		}
		if entry == nil || entry.IsDir() {
			return nil
		}

		relPath, err := themeOwnedRelativePath(themeRoot, currentPath)
		if err != nil {
			return fmt.Errorf("relative theme template path %q: %w", currentPath, err)
		}
		if !isThemeHTMLTemplatePath(relPath) {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("theme HTML template %q must be a regular non-symlink file", currentPath)
		}

		fileState, err := resolveThemeTemplateFileStateInRoot(themeRoot, relPath)
		if err != nil {
			return err
		}
		if !fileState.exists {
			return nil
		}

		files = append(files, fileState)
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(files, func(i int, j int) bool {
		return files[i].name < files[j].name
	})

	return files, nil
}

func scanThemeTemplateSnapshotFromState(state themeTemplateState) (themeTemplateSnapshot, error) {
	hasher := sha256.New()
	files := make([]themeTemplateFile, 0, len(state.files))
	_, _ = hasher.Write([]byte(state.identity.activeThemeName))
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write([]byte(state.identity.themeRoot))
	_, _ = hasher.Write([]byte{0})
	for _, fileState := range state.files {
		name := fileState.name

		_, _ = hasher.Write([]byte(name))
		if !fileState.exists {
			return themeTemplateSnapshot{}, fmt.Errorf("missing required theme template %q in %q", name, state.identity.themeRoot)
		}

		data, err := readThemeTemplateContents(fileState.path)
		if err != nil {
			return themeTemplateSnapshot{}, fmt.Errorf("read theme template %q: %w", fileState.path, err)
		}
		_, _ = hasher.Write([]byte{0, 1})
		_, _ = hasher.Write(data)
		_, _ = hasher.Write([]byte{0})

		files = append(files, themeTemplateFile{
			path:     fileState.path,
			contents: string(data),
		})
	}

	return themeTemplateSnapshot{
		identity:  state.identity,
		files:     files,
		signature: hex.EncodeToString(hasher.Sum(nil)),
	}, nil
}

func readThemeTemplateContents(filePath string) ([]byte, error) {
	themeTemplateFileReader.mu.RLock()
	reader := themeTemplateFileReader.read
	themeTemplateFileReader.mu.RUnlock()

	return reader(filePath)
}

func parseTemplateSet(snapshot themeTemplateSnapshot) (*template.Template, error) {
	if snapshot.identity.themeRoot == "" {
		tmpl, err := parseDefaultTemplates()
		if err != nil {
			return nil, fmt.Errorf("parse default templates: %w", err)
		}

		return tmpl, nil
	}

	tmpl := template.New(baseTemplateName).Funcs(template.FuncMap{
		"toJSON":       templateJSON,
		"pageAssetURL": pageAssetURL,
		"siteBasePath": siteBasePath,
	})
	for _, file := range snapshot.files {
		if _, err := tmpl.Parse(file.contents); err != nil {
			return nil, fmt.Errorf("parse theme template %q: %w", file.path, err)
		}
	}

	return tmpl, nil
}

func readStyleAsset(site model.SiteConfig) ([]byte, bool, error) {
	themeRoot, err := normalizeThemeRoot(site.ThemeRoot)
	if err != nil {
		return nil, false, err
	}
	if themeRoot == "" {
		data, err := readEmbeddedAsset("style.css")
		if err != nil {
			return nil, false, err
		}

		return data, true, nil
	}

	stylePath, ok, err := resolveOptionalThemeFilePathInRoot(themeRoot, "style.css")
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, nil
	}

	data, err := os.ReadFile(stylePath)
	if err != nil {
		return nil, false, fmt.Errorf("read theme style %q: %w", stylePath, err)
	}

	return data, true, nil
}

func resolveOptionalThemeFilePathInRoot(themeRoot string, name string) (string, bool, error) {
	fileState, err := resolveThemeTemplateFileStateInRoot(themeRoot, name)
	if err != nil {
		return "", false, err
	}

	return fileState.path, fileState.exists, nil
}

func resolveThemeTemplateFileStateInRoot(themeRoot string, name string) (themeTemplateFileState, error) {
	state := themeTemplateFileState{name: name}
	if themeRoot == "" {
		return state, nil
	}

	themePath := filepath.Join(themeRoot, filepath.FromSlash(name))
	resolvedPath, info, err := internalfsutil.InspectRegularNonSymlinkFile(themePath)
	if err != nil {
		if os.IsNotExist(err) {
			return state, nil
		}
		if errors.Is(err, internalfsutil.ErrUnsupportedRegularFileSource) {
			return themeTemplateFileState{}, fmt.Errorf("theme file %q must be a regular non-symlink file", themePath)
		}

		return themeTemplateFileState{}, fmt.Errorf("stat theme file %q: %w", themePath, err)
	}

	state.path = resolvedPath
	state.exists = true
	state.size = info.Size()
	state.modTimeUnixNano = info.ModTime().UnixNano()
	state.changeToken = themeTemplateFileChangeToken(info)

	return state, nil
}

func themeTemplateFileChangeToken(info os.FileInfo) string {
	if info == nil {
		return ""
	}

	token := fmt.Sprintf("%d:%d:%v", info.Size(), info.ModTime().UnixNano(), info.Mode())
	value := reflect.ValueOf(info.Sys())
	if !value.IsValid() {
		return token
	}
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return token
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return token
	}

	parts := []string{
		themeTemplateStatFieldString(value, "Dev"),
		themeTemplateStatFieldString(value, "Ino"),
		themeTemplateStatFieldString(value, "Ctim"),
		themeTemplateStatFieldString(value, "Ctimespec"),
		themeTemplateStatFieldString(value, "Mtim"),
		themeTemplateStatFieldString(value, "Mtimespec"),
	}

	return token + ":" + strings.Join(parts, ":")
}

func themeTemplateStatFieldString(value reflect.Value, fieldName string) string {
	field := value.FieldByName(fieldName)
	if !field.IsValid() {
		return ""
	}

	return fmt.Sprint(field.Interface())
}

func normalizeThemeRoot(themeRoot string) (string, error) {
	trimmedRoot := strings.TrimSpace(themeRoot)
	if trimmedRoot == "" {
		return "", nil
	}

	normalizedRoot, err := filepath.Abs(filepath.Clean(trimmedRoot))
	if err != nil {
		return "", fmt.Errorf("normalize theme root %q: %w", trimmedRoot, err)
	}
	if err := validateThemeRoot(normalizedRoot); err != nil {
		return "", err
	}

	return normalizedRoot, nil
}

func validateThemeRoot(themeRoot string) error {
	info, err := os.Lstat(themeRoot)
	if err != nil {
		return fmt.Errorf("stat theme root %q: %w", themeRoot, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("theme root %q must not be a symlink", themeRoot)
	}
	if !info.IsDir() {
		return fmt.Errorf("theme root %q is not a directory", themeRoot)
	}

	return nil
}

func readEmbeddedAsset(name string) ([]byte, error) {
	data, err := templateassets.FS.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("read embedded asset %q: %w", name, err)
	}

	return data, nil
}

// EmbeddedTemplateAssetNames returns the embedded template asset inventory in stable order.
func EmbeddedTemplateAssetNames() []string {
	return append([]string(nil), embeddedTemplateAssetNames...)
}

// ReadEmbeddedTemplateAsset returns an embedded default template asset.
func ReadEmbeddedTemplateAsset(name string) ([]byte, error) {
	return readEmbeddedAsset(name)
}

func htmlTemplateAssetNames(names []string) []string {
	filtered := make([]string, 0, len(names))
	for _, name := range names {
		if strings.EqualFold(path.Ext(strings.TrimSpace(name)), ".html") {
			filtered = append(filtered, name)
		}
	}
	return filtered
}

func templateJSON(value any) (template.JS, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}

	return template.JS(data), nil
}

func defaultNoteBreadcrumbs(existing []model.Breadcrumb, relPath string, note *model.Note, title string) []model.Breadcrumb {
	if len(existing) > 0 {
		return cloneBreadcrumbs(existing)
	}

	breadcrumbs := []model.Breadcrumb{{Name: "Home", URL: siteRootRel(relPath)}}
	breadcrumbs = appendFolderHierarchyBreadcrumbs(breadcrumbs, relPath, noteFolderPath(note), false, "")
	if strings.TrimSpace(title) != "" {
		breadcrumbs = append(breadcrumbs, model.Breadcrumb{Name: title})
	}

	return breadcrumbs
}

func defaultTagBreadcrumbs(existing []model.Breadcrumb, relPath string, tag *model.Tag) []model.Breadcrumb {
	if len(existing) > 0 {
		return cloneBreadcrumbs(existing)
	}

	breadcrumbs := []model.Breadcrumb{{Name: "Home", URL: siteRootRel(relPath)}}
	tagNames := tagBreadcrumbNames(tag)
	for index, name := range tagNames {
		breadcrumb := model.Breadcrumb{Name: name}
		if index < len(tagNames)-1 {
			breadcrumb.URL = relativeDirPath(relPath, path.Join(tagsRootOutputPrefix, name))
		}
		breadcrumbs = append(breadcrumbs, breadcrumb)
	}

	return breadcrumbs
}

func defaultFolderBreadcrumbs(existing []model.Breadcrumb, relPath string, folderPath string, title string) []model.Breadcrumb {
	if len(existing) > 0 {
		return cloneBreadcrumbs(existing)
	}

	breadcrumbs := []model.Breadcrumb{{Name: "Home", URL: siteRootRel(relPath)}}
	breadcrumbs = appendFolderHierarchyBreadcrumbs(breadcrumbs, relPath, folderPath, true, title)

	return breadcrumbs
}

func defaultTimelineBreadcrumbs(relPath string, asHomepage bool) []model.Breadcrumb {
	if asHomepage {
		return nil
	}

	return []model.Breadcrumb{{Name: "Home", URL: siteRootRel(relPath)}, {Name: "Notes"}}
}

func appendFolderHierarchyBreadcrumbs(breadcrumbs []model.Breadcrumb, relPath string, folderPath string, lastIsCurrent bool, currentName string) []model.Breadcrumb {
	segments := pathSegments(folderPath)
	for index, segment := range segments {
		name := segment
		if index == len(segments)-1 && strings.TrimSpace(currentName) != "" {
			name = strings.TrimSpace(currentName)
		}

		breadcrumb := model.Breadcrumb{Name: name}
		if !lastIsCurrent || index < len(segments)-1 {
			breadcrumb.URL = relativeDirPath(relPath, path.Join(segments[:index+1]...))
		}
		breadcrumbs = append(breadcrumbs, breadcrumb)
	}

	return breadcrumbs
}

func noteFolderPath(note *model.Note) string {
	if note == nil {
		return ""
	}

	trimmed := strings.TrimSpace(strings.ReplaceAll(note.RelPath, `\`, "/"))
	if trimmed == "" {
		return ""
	}

	dir := path.Dir(trimmed)
	if dir == "." || dir == "/" {
		return ""
	}

	return cleanURLPath(dir)
}

func tagBreadcrumbNames(tag *model.Tag) []string {
	fullName := ""
	if tag != nil {
		fullName = strings.TrimSpace(tag.Name)
		if fullName == "" {
			fullName = tagNameFromSlug(tag.Slug)
		}
	}

	segments := pathSegments(fullName)
	if len(segments) == 0 {
		return nil
	}

	names := make([]string, 0, len(segments))
	for index := range segments {
		names = append(names, strings.Join(segments[:index+1], "/"))
	}

	return names
}

func tagNameFromSlug(raw string) string {
	clean := normalizeTagPageSlug(raw)
	if clean == "" {
		return ""
	}

	return strings.TrimPrefix(clean, tagsRootOutputPrefix+"/")
}

func pathSegments(raw string) []string {
	clean := cleanURLPath(raw)
	if clean == "" {
		return nil
	}

	segments := strings.Split(clean, "/")
	filtered := segments[:0]
	for _, segment := range segments {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			continue
		}
		filtered = append(filtered, segment)
	}

	return filtered
}

func hasCustomCSS(site model.SiteConfig) bool {
	return strings.TrimSpace(site.CustomCSS) != ""
}

func resolvePageRelPath(override string, defaultRelPath string) string {
	if clean := cleanURLPath(override); clean != "" {
		return clean
	}

	return cleanURLPath(defaultRelPath)
}

func siteRootRel(relPath string) string {
	clean := cleanURLPath(relPath)
	if clean == "" || clean == "." {
		return "./"
	}

	dir := path.Dir(clean)
	if dir == "." || dir == "" {
		return "./"
	}

	segments := strings.Split(strings.Trim(dir, "/"), "/")
	depth := 0
	for _, segment := range segments {
		if segment != "" && segment != "." {
			depth++
		}
	}
	if depth == 0 {
		return "./"
	}

	return strings.Repeat("../", depth)
}

func siteBasePath(rawBaseURL string) string {
	trimmed := strings.TrimSpace(rawBaseURL)
	if trimmed == "" {
		return "/"
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "/"
	}

	clean := path.Clean(parsed.Path)
	switch clean {
	case "", ".", "/":
		return "/"
	default:
		return strings.TrimSuffix(clean, "/") + "/"
	}
}

func pageAssetURL(siteRootRel string, raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}

	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "http://") ||
		strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "//") ||
		strings.HasPrefix(lower, "data:") ||
		strings.HasPrefix(lower, "mailto:") ||
		strings.HasPrefix(lower, "javascript:") ||
		strings.HasPrefix(trimmed, "/") ||
		strings.HasPrefix(trimmed, "#") ||
		strings.HasPrefix(trimmed, "?") ||
		strings.HasPrefix(trimmed, "./") ||
		strings.HasPrefix(trimmed, "../") {
		return trimmed
	}

	base := strings.TrimSpace(siteRootRel)
	if base == "" {
		base = "./"
	}

	return base + trimmed
}

func relativeDirPath(fromFile string, toDir string) string {
	fromDir := path.Dir(cleanURLPath(fromFile))
	if fromDir == "." || fromDir == "" {
		fromDir = "."
	}

	target := cleanURLPath(toDir)
	if target == "" {
		target = "."
	}

	rel, err := filepath.Rel(filepath.FromSlash(fromDir), filepath.FromSlash(target))
	if err != nil {
		return ""
	}

	clean := filepath.ToSlash(rel)
	if clean == "." || clean == "" {
		return "./"
	}

	clean = strings.TrimSuffix(clean, "/")
	return clean + "/"
}

func normalizeSlug(raw string) string {
	clean := cleanURLPath(raw)
	clean = strings.TrimSuffix(clean, "/index.html")
	clean = strings.TrimSuffix(clean, ".html")
	clean = strings.Trim(clean, "/")
	if clean == "." {
		return ""
	}

	return clean
}

func normalizeTagPageSlug(raw string) string {
	clean := normalizeSlug(raw)
	if clean == "" {
		return ""
	}
	if clean == tagsRootOutputPrefix || strings.HasPrefix(clean, tagsRootOutputPrefix+"/") {
		return clean
	}
	return cleanURLPath(tagsRootOutputPrefix + "/" + clean)
}

func cleanURLPath(raw string) string {
	replaced := strings.TrimSpace(strings.ReplaceAll(raw, `\`, "/"))
	if replaced == "" {
		return ""
	}

	clean := path.Clean(replaced)
	clean = strings.TrimPrefix(clean, "./")
	clean = strings.TrimPrefix(clean, "/")
	if clean == "." {
		return ""
	}

	return clean
}

func noteDisplayTitle(note *model.Note) string {
	if note == nil {
		return ""
	}
	if title := strings.TrimSpace(note.Frontmatter.Title); title != "" {
		return title
	}

	base := path.Base(strings.ReplaceAll(note.RelPath, `\`, "/"))
	if base == "." || base == "" || base == "/" {
		return ""
	}

	return strings.TrimSuffix(base, path.Ext(base))
}

func folderDisplayTitle(folderPath string) string {
	clean := cleanURLPath(folderPath)
	if clean == "" {
		return ""
	}

	base := path.Base(clean)
	if base == "." || base == "" || base == "/" {
		return clean
	}

	return base
}

func cloneTagLinks(src []model.TagLink) []model.TagLink {
	if len(src) == 0 {
		return nil
	}

	dst := make([]model.TagLink, len(src))
	copy(dst, src)
	return dst
}

func cloneBacklinks(src []model.BacklinkEntry) []model.BacklinkEntry {
	if len(src) == 0 {
		return nil
	}

	dst := make([]model.BacklinkEntry, len(src))
	copy(dst, src)
	return dst
}

func cloneRelatedArticles(src []model.RelatedArticle) []model.RelatedArticle {
	if len(src) == 0 {
		return nil
	}

	dst := make([]model.RelatedArticle, len(src))
	for idx := range src {
		dst[idx] = src[idx]
		dst[idx].Tags = cloneTagLinks(src[idx].Tags)
	}

	return dst
}

func cloneNoteSummaries(src []model.NoteSummary) []model.NoteSummary {
	if len(src) == 0 {
		return nil
	}

	dst := make([]model.NoteSummary, len(src))
	for idx := range src {
		dst[idx] = src[idx]
		dst[idx].Tags = cloneTagLinks(src[idx].Tags)
	}

	return dst
}

func cloneBreadcrumbs(src []model.Breadcrumb) []model.Breadcrumb {
	if len(src) == 0 {
		return nil
	}

	dst := make([]model.Breadcrumb, len(src))
	copy(dst, src)
	return dst
}

func clonePagination(src *model.PaginationData) *model.PaginationData {
	if src == nil {
		return nil
	}

	dst := *src
	if len(src.Pages) > 0 {
		dst.Pages = make([]model.PageLink, len(src.Pages))
		copy(dst.Pages, src.Pages)
	}

	return &dst
}

func cloneSidebarTree(src []model.SidebarNode) []model.SidebarNode {
	if len(src) == 0 {
		return nil
	}

	dst := make([]model.SidebarNode, len(src))
	for index := range src {
		dst[index] = src[index]
		dst[index].Children = cloneSidebarTree(src[index].Children)
	}

	return dst
}
