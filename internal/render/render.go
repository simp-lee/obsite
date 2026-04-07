package render

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/simp-lee/obsite/internal/diag"
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
	tagsRootOutputPrefix = "tags"
)

var parseDefaultTemplates = sync.OnceValues(func() (*template.Template, error) {
	return template.ParseFS(templateassets.FS,
		"base.html",
		"note.html",
		"index.html",
		"tag.html",
		"404.html",
	)
})

var leadingHeadingPattern = regexp.MustCompile(`(?is)^\s*<h1\b([^>]*)>.*?</h1>\s*`)

// RenderedPage is the rendered HTML plus the PageData used to execute it.
type RenderedPage struct {
	Page        model.PageData
	HTML        []byte
	Diagnostics []diag.Diagnostic
}

// NotePageInput supplies the data needed to render a note page.
type NotePageInput struct {
	Site        model.SiteConfig
	Note        *model.Note
	Tags        []model.TagLink
	Backlinks   []model.BacklinkEntry
	Breadcrumbs []model.Breadcrumb
}

// TagPageInput supplies the data needed to render a tag archive page.
type TagPageInput struct {
	Site         model.SiteConfig
	Tag          *model.Tag
	ChildTags    []model.TagLink
	Notes        []model.NoteSummary
	Breadcrumbs  []model.Breadcrumb
	LastModified time.Time
}

// IndexPageInput supplies the data needed to render the index page.
type IndexPageInput struct {
	Site         model.SiteConfig
	RecentNotes  []model.NoteSummary
	LastModified time.Time
}

// NotFoundPageInput supplies the data needed to render the 404 page.
type NotFoundPageInput struct {
	Site         model.SiteConfig
	RecentNotes  []model.NoteSummary
	LastModified time.Time
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
	relPath := cleanURLPath(slug + "/index.html")
	page := model.PageData{
		Kind:         model.PageNote,
		Site:         input.Site,
		SiteRootRel:  siteRootRel(relPath),
		Title:        strings.TrimSpace(input.Note.Frontmatter.Title),
		TitleID:      titleID,
		Description:  strings.TrimSpace(input.Note.Frontmatter.Description),
		Slug:         slug,
		RelPath:      relPath,
		Content:      template.HTML(content),
		Date:         input.Note.Frontmatter.Date,
		LastModified: input.Note.LastModified,
		Tags:         cloneTagLinks(input.Tags),
		Backlinks:    cloneBacklinks(input.Backlinks),
		HasMath:      input.Note.HasMath,
		HasMermaid:   input.Note.HasMermaid,
		Breadcrumbs:  defaultNoteBreadcrumbs(input.Breadcrumbs, siteRootRel(relPath), noteDisplayTitle(input.Note)),
	}

	return renderPage(page, input.Note)
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

	matches := leadingHeadingPattern.FindStringSubmatchIndex(content)
	if matches == nil {
		return content, ""
	}

	openTag := content[matches[2]:matches[3]]
	if !headingOpenTagHasID(openTag, firstHeading.ID) {
		return content, ""
	}

	return strings.TrimLeftFunc(content[matches[1]:], unicode.IsSpace), strings.TrimSpace(firstHeading.ID)
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
	if headingText != "" && strings.EqualFold(normalizeHeadingText(headingText), normalizeHeadingText(displayTitle)) {
		return first, true
	}

	headingID := strings.TrimSpace(first.ID)
	if headingID == "" {
		return model.Heading{}, false
	}

	if headingID == normalizeHeadingID(displayTitle) {
		return first, true
	}

	return model.Heading{}, false
}

func headingOpenTagHasID(openTag string, id string) bool {
	trimmedID := strings.TrimSpace(id)
	if trimmedID == "" {
		return true
	}

	return strings.Contains(openTag, `id="`+trimmedID+`"`) || strings.Contains(openTag, `id='`+trimmedID+`'`)
}

func normalizeHeadingText(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func normalizeHeadingID(value string) string {
	value = normalizeHeadingText(value)
	if value == "" {
		return "heading"
	}

	var builder strings.Builder
	lastHyphen := false

	for _, r := range strings.ToLower(value) {
		switch {
		case isASCIIControl(r):
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

func isASCIIControl(r rune) bool {
	return (r >= 0 && r < 0x20) || r == 0x7f
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
	relPath := cleanURLPath(pageSlug + "/index.html")
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
		Breadcrumbs:  defaultTagBreadcrumbs(input.Breadcrumbs, relPath, strings.TrimSpace(input.Tag.Name)),
	}

	return renderPage(page, nil)
}

// RenderIndex renders the site index page to HTML using the embedded default templates.
func RenderIndex(input IndexPageInput) (RenderedPage, error) {
	page := model.PageData{
		Kind:         model.PageIndex,
		Site:         input.Site,
		SiteRootRel:  siteRootRel(indexOutputPath),
		Title:        strings.TrimSpace(input.Site.Title),
		Description:  strings.TrimSpace(input.Site.Description),
		RelPath:      indexOutputPath,
		RecentNotes:  cloneNoteSummaries(input.RecentNotes),
		LastModified: input.LastModified,
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
	}

	return renderPage(page, nil)
}

// EmitStyleCSS writes the embedded default stylesheet into the output root.
func EmitStyleCSS(outputRoot string) error {
	if strings.TrimSpace(outputRoot) == "" {
		return errors.New("emit style.css: output root is required")
	}

	data, err := readEmbeddedAsset("style.css")
	if err != nil {
		return fmt.Errorf("emit style.css: %w", err)
	}
	if err := os.MkdirAll(outputRoot, 0o755); err != nil {
		return fmt.Errorf("emit style.css: mkdir %q: %w", outputRoot, err)
	}
	if err := os.WriteFile(filepath.Join(outputRoot, "style.css"), data, 0o644); err != nil {
		return fmt.Errorf("emit style.css: write style.css: %w", err)
	}

	return nil
}

func renderPage(page model.PageData, note *model.Note) (RenderedPage, error) {
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
	tmpl, err := parseDefaultTemplates()
	if err != nil {
		return nil, fmt.Errorf("parse default templates: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, baseTemplateName, page); err != nil {
		return nil, fmt.Errorf("execute template %q: %w", baseTemplateName, err)
	}

	return buf.Bytes(), nil
}

func readEmbeddedAsset(name string) ([]byte, error) {
	data, err := templateassets.FS.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("read embedded asset %q: %w", name, err)
	}

	return data, nil
}

func defaultNoteBreadcrumbs(existing []model.Breadcrumb, root string, title string) []model.Breadcrumb {
	if len(existing) > 0 {
		return cloneBreadcrumbs(existing)
	}

	breadcrumbs := []model.Breadcrumb{{Name: "Home", URL: root}}
	if strings.TrimSpace(title) != "" {
		breadcrumbs = append(breadcrumbs, model.Breadcrumb{Name: title})
	}

	return breadcrumbs
}

func defaultTagBreadcrumbs(existing []model.Breadcrumb, relPath string, tagName string) []model.Breadcrumb {
	if len(existing) > 0 {
		return cloneBreadcrumbs(existing)
	}

	breadcrumbs := []model.Breadcrumb{{Name: "Home", URL: siteRootRel(relPath)}}
	if strings.TrimSpace(tagName) != "" {
		breadcrumbs = append(breadcrumbs, model.Breadcrumb{Name: tagName})
	}

	return breadcrumbs
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
