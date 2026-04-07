package seo

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/simp-lee/obsite/internal/model"
)

const schemaOrgContext = "https://schema.org"

type articleJSONLD struct {
	Context       string             `json:"@context"`
	Type          string             `json:"@type"`
	Headline      string             `json:"headline,omitempty"`
	DatePublished string             `json:"datePublished,omitempty"`
	Author        *namedEntityJSONLD `json:"author,omitempty"`
	Description   string             `json:"description,omitempty"`
	URL           string             `json:"url,omitempty"`
}

type namedEntityJSONLD struct {
	Type string `json:"@type"`
	Name string `json:"name"`
}

type breadcrumbListJSONLD struct {
	Context         string                 `json:"@context"`
	Type            string                 `json:"@type"`
	ItemListElement []breadcrumbItemJSONLD `json:"itemListElement,omitempty"`
}

type breadcrumbItemJSONLD struct {
	Type     string `json:"@type"`
	Position int    `json:"position"`
	Name     string `json:"name"`
	Item     string `json:"item,omitempty"`
}

type normalizedBreadcrumb struct {
	Name string
	URL  string
}

// ArticleJSONLDError reports which required Article JSON-LD fields are missing.
type ArticleJSONLDError struct {
	MissingFields []string
}

func (e *ArticleJSONLDError) Error() string {
	if e == nil || len(e.MissingFields) == 0 {
		return "article JSON-LD is incomplete"
	}

	return fmt.Sprintf("article JSON-LD requires %s", strings.Join(e.MissingFields, ", "))
}

// BuildJSONLD pre-serializes page JSON-LD for templates.
//
// Note pages still return an explicit error when the required Article schema
// fields are incomplete, but any valid BreadcrumbList JSON-LD is preserved in
// the serialized payload.
func BuildJSONLD(page model.PageData, note *model.Note) (template.JS, error) {
	return buildJSONLD(page, note, Build(page, note))
}

func buildJSONLD(page model.PageData, note *model.Note, metadata Metadata) (template.JS, error) {
	if page.Kind != model.PageNote {
		return "", nil
	}

	payload := make([]any, 0, 2)
	article, articleErr := buildArticleJSONLD(page, note, metadata)
	if article != nil {
		payload = append(payload, article)
	}
	if breadcrumb := buildBreadcrumbListJSONLD(page, metadata); breadcrumb != nil {
		payload = append(payload, breadcrumb)
	}
	if len(payload) == 0 {
		return "", articleErr
	}

	serialized, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	return template.JS(serialized), articleErr
}

func buildArticleJSONLD(page model.PageData, note *model.Note, metadata Metadata) (*articleJSONLD, error) {
	headline := strings.TrimSpace(metadata.Title)
	description := strings.TrimSpace(metadata.Description)
	canonical := strings.TrimSpace(metadata.Canonical)
	author := articleAuthor(page.Site)
	publishedAt := publishedAt(page, note)
	missingFields := missingArticleJSONLDFields(headline, description, canonical, author, publishedAt)
	if len(missingFields) > 0 {
		return nil, &ArticleJSONLDError{MissingFields: missingFields}
	}

	return &articleJSONLD{
		Context:       schemaOrgContext,
		Type:          "Article",
		Headline:      headline,
		DatePublished: publishedAt.UTC().Format(time.RFC3339),
		Author:        author,
		Description:   description,
		URL:           canonical,
	}, nil
}

func articleAuthor(site model.SiteConfig) *namedEntityJSONLD {
	if name := strings.TrimSpace(site.Author); name != "" {
		return &namedEntityJSONLD{Type: "Person", Name: name}
	}
	if name := strings.TrimSpace(site.Title); name != "" {
		return &namedEntityJSONLD{Type: "Organization", Name: name}
	}

	return nil
}

func missingArticleJSONLDFields(headline string, description string, canonical string, author *namedEntityJSONLD, publishedAt time.Time) []string {
	missing := make([]string, 0, 5)
	if headline == "" {
		missing = append(missing, "headline")
	}
	if description == "" {
		missing = append(missing, "description")
	}
	if canonical == "" {
		missing = append(missing, "url")
	}
	if author == nil {
		missing = append(missing, "author")
	}
	if publishedAt.IsZero() {
		missing = append(missing, "datePublished")
	}

	return missing
}

func buildBreadcrumbListJSONLD(page model.PageData, metadata Metadata) *breadcrumbListJSONLD {
	breadcrumbs := normalizeBreadcrumbs(page, metadata)
	if len(breadcrumbs) == 0 {
		return nil
	}

	items := make([]breadcrumbItemJSONLD, 0, len(breadcrumbs))
	for _, breadcrumb := range breadcrumbs {
		if breadcrumb.Name == "" {
			continue
		}

		item := breadcrumbItemJSONLD{
			Type:     "ListItem",
			Position: len(items) + 1,
			Name:     breadcrumb.Name,
			Item:     breadcrumb.URL,
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		return nil
	}

	return &breadcrumbListJSONLD{
		Context:         schemaOrgContext,
		Type:            "BreadcrumbList",
		ItemListElement: items,
	}
}

func normalizeBreadcrumbs(page model.PageData, metadata Metadata) []normalizedBreadcrumb {
	normalized := make([]normalizedBreadcrumb, 0, len(page.Breadcrumbs)+1)
	for _, breadcrumb := range page.Breadcrumbs {
		name := strings.TrimSpace(breadcrumb.Name)
		if name == "" {
			continue
		}

		normalized = append(normalized, normalizedBreadcrumb{
			Name: name,
			URL:  absolutizeBreadcrumbURL(page.Site.BaseURL, breadcrumb.URL),
		})
	}

	canonical := strings.TrimSpace(metadata.Canonical)
	title := strings.TrimSpace(metadata.Title)
	if canonical == "" || title == "" {
		return normalized
	}
	if len(normalized) > 0 {
		last := &normalized[len(normalized)-1]
		if last.URL == "" && last.Name == title {
			last.URL = canonical
		}
		if sameAbsoluteLocation(last.URL, canonical) {
			return normalized
		}
	}

	return append(normalized, normalizedBreadcrumb{Name: title, URL: canonical})
}

func sameAbsoluteLocation(left string, right string) bool {
	normalizedLeft := comparableAbsoluteURL(left)
	normalizedRight := comparableAbsoluteURL(right)
	return normalizedLeft != "" && normalizedLeft == normalizedRight
}

func comparableAbsoluteURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return trimmed
	}

	cleanPath := path.Clean(parsed.Path)
	switch cleanPath {
	case ".", "":
		parsed.Path = "/"
	default:
		if cleanPath != "/" {
			cleanPath = strings.TrimRight(cleanPath, "/")
		}
		if cleanPath == "" {
			cleanPath = "/"
		}
		parsed.Path = cleanPath
	}
	parsed.RawPath = ""

	return parsed.String()
}

func absolutizeBreadcrumbURL(baseURL string, raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}

	return absolutePageURL(baseURL, raw)
}

func publishedAt(page model.PageData, note *model.Note) time.Time {
	if note != nil {
		if publishedAt := note.PublishedAt(); !publishedAt.IsZero() {
			return publishedAt
		}
	}
	if !page.Date.IsZero() {
		return page.Date.UTC()
	}
	if !page.LastModified.IsZero() {
		return page.LastModified.UTC()
	}

	return time.Time{}
}
