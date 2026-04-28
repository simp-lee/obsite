package seo

import (
	"net/url"
	"path"
	"strings"

	"github.com/simp-lee/obsite/internal/model"
)

const (
	twitterCardSummary           = "summary"
	twitterCardSummaryLargeImage = "summary_large_image"
)

// Metadata contains the template-safe SEO fields derived for a single page.
type Metadata struct {
	Title       string
	Description string
	Canonical   string
	OG          model.OpenGraph
	TwitterCard string
}

// Build derives SEO metadata for a rendered page.
func Build(page model.PageData, note *model.Note) Metadata {
	title := pickTitle(page, note)
	description := pickDescription(page, note)
	canonicalPath, ok := pagePath(page, note)
	canonical := ""
	if ok {
		canonical = absolutePageURL(page.Site.BaseURL, canonicalPath)
	}

	metadata := Metadata{
		Title:       title,
		Description: description,
		Canonical:   canonical,
	}

	metadata.OG = model.OpenGraph{
		Title:       firstNonEmpty(page.OG.Title, metadata.Title),
		Description: firstNonEmpty(page.OG.Description, metadata.Description),
		URL:         metadata.Canonical,
		Image:       pickImage(page),
		Type:        pickOGType(page),
	}

	if page.OG.Image != "" {
		metadata.OG.Image = absoluteAssetURL(page.Site.BaseURL, page.OG.Image)
	}
	if metadata.OG.Image != "" {
		metadata.TwitterCard = twitterCardSummaryLargeImage
	} else {
		metadata.TwitterCard = twitterCardSummary
	}

	return metadata
}

// Apply derives SEO metadata and copies the fields used by templates into PageData.
//
// Page-level SEO metadata is always written into PageData. Note pages still
// return an error when their required Article JSON-LD fields are incomplete;
// any valid structured data that was serialized alongside that error remains in
// PageData.JSONLD and the rest of the metadata is still applied.
func Apply(page *model.PageData, note *model.Note) (Metadata, error) {
	if page == nil {
		return Metadata{}, nil
	}

	metadata := Build(*page, note)

	page.Title = metadata.Title
	page.Description = metadata.Description
	page.Canonical = metadata.Canonical
	page.OG = metadata.OG
	page.TwitterCard = metadata.TwitterCard
	page.JSONLD = ""

	jsonld, err := buildJSONLD(*page, note, metadata)
	page.JSONLD = jsonld
	if err != nil {
		return metadata, err
	}

	return metadata, nil
}

func pickTitle(page model.PageData, note *model.Note) string {
	if title := firstNonEmpty(page.Title); title != "" {
		return title
	}

	if note != nil {
		if title := firstNonEmpty(note.Frontmatter.Title); title != "" {
			return title
		}
		if title := noteFilename(note.RelPath); title != "" {
			return title
		}
	}

	return firstNonEmpty(page.Site.Title)
}

func pickDescription(page model.PageData, note *model.Note) string {
	if description := firstNonEmpty(page.Description); description != "" {
		return description
	}

	if page.Kind == model.PageNote {
		if note != nil {
			if description := firstNonEmpty(note.Frontmatter.Description); description != "" {
				return description
			}
			if description := firstNonEmpty(note.Summary); description != "" {
				return description
			}
		}

		return firstNonEmpty(pickTitle(page, note), page.Site.Description)
	}

	return firstNonEmpty(page.Site.Description)
}

func pickImage(page model.PageData) string {
	if image := firstNonEmpty(page.OG.Image); image != "" {
		return absoluteAssetURL(page.Site.BaseURL, image)
	}

	if image := firstNonEmpty(page.Site.DefaultImg); image != "" {
		return absoluteAssetURL(page.Site.BaseURL, image)
	}

	return ""
}

func pickOGType(page model.PageData) string {
	if ogType := firstNonEmpty(page.OG.Type); ogType != "" {
		return ogType
	}

	if page.Kind == model.PageNote {
		return "article"
	}

	return "website"
}

func pagePath(page model.PageData, note *model.Note) (string, bool) {
	if rel, ok := paginatedRelPath(page); ok {
		return rel, true
	}

	if slug := cleanPathValue(firstNonEmpty(page.Slug)); slug != "" {
		return slug + "/", true
	}

	if note != nil {
		if slug := cleanPathValue(firstNonEmpty(note.Slug)); slug != "" {
			return slug + "/", true
		}
		if page.Kind == model.PageNote {
			return "", false
		}
		if rel := relPathToCleanPath(note.RelPath); rel != "" {
			return rel, true
		}
	}

	if page.Kind == model.PageNote {
		return "", false
	}

	if rel := relPathToCleanPath(page.RelPath); rel != "" {
		return rel, true
	}
	if isRootIndexRelPath(page.RelPath) {
		return "", true
	}

	if page.Kind == model.PageIndex {
		return "", true
	}

	return "", false
}

func paginatedRelPath(page model.PageData) (string, bool) {
	if page.Pagination == nil || page.Pagination.CurrentPage <= 1 {
		return "", false
	}

	if rel := relPathToCleanPath(page.RelPath); rel != "" {
		return rel, true
	}
	if isRootIndexRelPath(page.RelPath) {
		return "", true
	}

	return "", false
}

func relPathToCleanPath(relPath string) string {
	trimmed := strings.TrimSpace(strings.ReplaceAll(relPath, `\`, "/"))
	if trimmed == "" {
		return ""
	}

	clean := path.Clean(trimmed)
	if clean == "." || clean == "index.html" {
		return ""
	}

	clean = strings.TrimPrefix(clean, "./")
	clean = strings.TrimPrefix(clean, "/")
	clean = strings.TrimSuffix(clean, "/index.html")
	clean = strings.Trim(clean, "/")
	if clean == "" {
		return ""
	}
	if strings.HasSuffix(clean, ".html") {
		return clean
	}

	return clean + "/"
}

func isRootIndexRelPath(relPath string) bool {
	trimmed := strings.TrimSpace(strings.ReplaceAll(relPath, `\`, "/"))
	if trimmed == "" {
		return false
	}

	return path.Clean(trimmed) == "index.html"
}

func noteFilename(relPath string) string {
	trimmed := strings.TrimSpace(strings.ReplaceAll(relPath, `\`, "/"))
	if trimmed == "" {
		return ""
	}

	base := path.Base(trimmed)
	if base == "." || base == "/" || base == "" {
		return ""
	}

	return strings.TrimSuffix(base, path.Ext(base))
}

func absolutePageURL(baseURL string, pagePath string) string {
	cleanPath := strings.TrimSpace(pagePath)
	if cleanPath == "" {
		return resolveAbsoluteURL(baseURL, nil)
	}

	parsedPath, err := url.Parse(cleanPath)
	if err == nil && parsedPath.IsAbs() && parsedPath.Host != "" {
		return parsedPath.String()
	}

	hasTrailingSlash := strings.HasSuffix(cleanPath, "/")
	cleanPath = cleanPathValue(cleanPath)
	if cleanPath == "" {
		return resolveAbsoluteURL(baseURL, nil)
	}
	if hasTrailingSlash {
		cleanPath += "/"
	}

	return resolveAbsoluteURL(baseURL, &url.URL{Path: cleanPath})
}

func absoluteAssetURL(baseURL string, assetPath string) string {
	trimmedAsset := strings.TrimSpace(assetPath)
	if trimmedAsset == "" {
		return ""
	}

	parsedAsset, err := url.Parse(trimmedAsset)
	if err == nil && parsedAsset.IsAbs() && parsedAsset.Host != "" {
		return parsedAsset.String()
	}

	cleanAsset := cleanPathValue(trimmedAsset)
	if cleanAsset == "" {
		return ""
	}

	return resolveAbsoluteURL(baseURL, &url.URL{Path: cleanAsset})
}

func resolveAbsoluteURL(baseURL string, reference *url.URL) string {
	trimmedBase := strings.TrimSpace(baseURL)
	if trimmedBase == "" {
		return ""
	}

	parsedBase, err := url.Parse(trimmedBase)
	if err != nil {
		return ""
	}
	if parsedBase.Path == "" {
		parsedBase.Path = "/"
	} else if !strings.HasSuffix(parsedBase.Path, "/") {
		parsedBase.Path += "/"
	}
	if reference == nil || (reference.Path == "" && reference.RawQuery == "" && reference.Fragment == "") {
		return parsedBase.String()
	}

	return parsedBase.ResolveReference(reference).String()
}

func cleanPathValue(value string) string {
	trimmed := strings.TrimSpace(strings.ReplaceAll(value, `\`, "/"))
	trimmed = strings.Trim(trimmed, "/")
	if trimmed == "" {
		return ""
	}

	clean := path.Clean("/" + trimmed)
	clean = strings.TrimPrefix(clean, "/")
	if clean == "." {
		return ""
	}

	return clean
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}

	return ""
}
