package render

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/simp-lee/obsite/internal/model"
	"github.com/yuin/goldmark"
	"golang.org/x/text/unicode/norm"
)

// RenderStrictSection renders the fixed shell for a normalized section page.
// It consumes only the immutable model and keeps navigation server-rendered.
func RenderStrictSection(plan *model.SitePlan, section *model.Section) ([]byte, error) {
	if plan == nil || section == nil {
		return nil, fmt.Errorf("section page requires a plan and section")
	}
	content, err := strictMarkdown(section.RawContent)
	if err != nil {
		return nil, err
	}
	var body strings.Builder
	_, _ = fmt.Fprintf(&body, `<section class="section-landing"><h1>%s</h1>`, esc(section.Title))
	if section.Description != "" {
		_, _ = fmt.Fprintf(&body, `<p class="section-description">%s</p>`, esc(section.Description))
	}
	if section.Banner != "" {
		bannerURL := section.BannerURL
		if bannerURL == "" {
			bannerURL = section.Banner
		}
		_, _ = fmt.Fprintf(&body, `<img class="page-banner" src="%s" alt="%s" />`, esc(strictSitePath(plan, "/"+strings.TrimPrefix(bannerURL, "/"))), esc(section.BannerAlt))
	}
	_, _ = fmt.Fprintf(&body, `<div class="section-content">%s</div>`, content)
	if len(section.Children) > 0 {
		body.WriteString(`<h2>Sections</h2><ul class="section-children">`)
		for _, child := range section.Children {
			if child != nil && child.EffectivePublish {
				_, _ = fmt.Fprintf(&body, `<li><a href="%s">%s</a></li>`, esc(strictSitePath(plan, child.Route)), esc(child.Title))
			}
		}
		body.WriteString(`</ul>`)
	}
	if len(section.Articles) > 0 {
		body.WriteString(`<h2>Articles</h2><ul class="section-articles">`)
		for _, article := range section.Articles {
			if article != nil {
				_, _ = fmt.Fprintf(&body, `<li><a href="%s">%s</a></li>`, esc(strictSitePath(plan, article.Route)), esc(article.Frontmatter.Title))
			}
		}
		body.WriteString(`</ul>`)
	}
	return strictDocument(plan, section.Route, section.Title, section.Description, section.Breadcrumbs, section.VersionID, section.VersionRoutes, "", nil, section.SourcePath, body.String()), nil
}

// RenderStrictArticle renders a normalized article page and its single shared
// document reading-flow metadata.
func RenderStrictArticle(plan *model.SitePlan, article *model.Note, previous, next *model.Note, position, total int) ([]byte, error) {
	if plan == nil || article == nil {
		return nil, fmt.Errorf("article page requires a plan and article")
	}
	content, err := strictMarkdown(article.RawContent)
	if err != nil {
		return nil, err
	}
	section := findStrictSection(plan, article.SectionPath, article.VersionID)
	breadcrumbs := []model.Breadcrumb(nil)
	if section != nil {
		breadcrumbs = section.Breadcrumbs
	}
	var body strings.Builder
	_, _ = fmt.Fprintf(&body, `<article class="article-page"><header><h1>%s</h1>`, esc(article.Frontmatter.Title))
	if article.Frontmatter.Description != "" {
		_, _ = fmt.Fprintf(&body, `<p class="article-description">%s</p>`, esc(article.Frontmatter.Description))
	}
	if article.Frontmatter.Banner != "" {
		bannerURL := article.BannerURL
		if bannerURL == "" {
			bannerURL = article.Frontmatter.Banner
		}
		_, _ = fmt.Fprintf(&body, `<img class="page-banner" src="%s" alt="%s" />`, esc(strictSitePath(plan, "/"+strings.TrimPrefix(bannerURL, "/"))), esc(article.Frontmatter.BannerAlt))
	}
	_, _ = fmt.Fprintf(&body, `</header><div class="article-content">%s</div>`, content)
	if article.Frontmatter.Status == "deprecated" {
		body.WriteString(`<aside class="deprecated-notice" role="note">This page is deprecated.</aside>`)
	}
	if article.Frontmatter.Type == "doc" {
		_, _ = fmt.Fprintf(&body, `<nav class="reading-flow" aria-label="Document navigation"><span class="position">%d of %d</span>`, position, total)
		if previous != nil {
			_, _ = fmt.Fprintf(&body, `<a class="previous" rel="prev" href="%s">Previous</a>`, esc(strictSitePath(plan, previous.Route)))
		}
		if next != nil {
			_, _ = fmt.Fprintf(&body, `<a class="next" rel="next" href="%s">Next</a>`, esc(strictSitePath(plan, next.Route)))
		}
		body.WriteString(`</nav>`)
	}
	return strictDocument(plan, article.Route, article.Frontmatter.Title, article.Frontmatter.Description, breadcrumbs, article.VersionID, article.VersionRoutes, article.SocialImage, article, article.RelPath, body.String()), nil
}

func strictDocument(plan *model.SitePlan, currentRoute, title, description string, breadcrumbs []model.Breadcrumb, versionID string, versionRoutes map[string]string, socialImage string, metadata *model.Note, sourcePath, body string) []byte {
	var output strings.Builder
	_, _ = fmt.Fprintf(&output, `<!doctype html><html lang="%s"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>%s | %s</title>`, esc(plan.Config.Language), esc(title), esc(plan.Config.Title))
	if description != "" {
		_, _ = fmt.Fprintf(&output, `<meta name="description" content="%s">`, esc(description))
	}
	if socialImage != "" {
		imageURL := strictAbsoluteURL(plan, "/"+strings.TrimPrefix(socialImage, "/"))
		_, _ = fmt.Fprintf(&output, `<meta property="og:image" content="%s"><meta name="twitter:card" content="summary_large_image"><meta name="twitter:image" content="%s">`, esc(imageURL), esc(imageURL))
	}
	if metadata != nil {
		_, _ = fmt.Fprintf(&output, `<meta property="og:type" content="article"><meta property="og:title" content="%s">`, esc(metadata.Frontmatter.Title))
		if metadata.Frontmatter.Author != "" {
			_, _ = fmt.Fprintf(&output, `<meta name="author" content="%s">`, esc(metadata.Frontmatter.Author))
		}
		if !metadata.Frontmatter.Date.IsZero() {
			_, _ = fmt.Fprintf(&output, `<meta property="article:published_time" content="%s">`, esc(metadata.Frontmatter.Date.UTC().Format(time.RFC3339)))
		}
		if !metadata.Frontmatter.Updated.IsZero() {
			_, _ = fmt.Fprintf(&output, `<meta property="article:modified_time" content="%s">`, esc(metadata.Frontmatter.Updated.UTC().Format(time.RFC3339)))
		}
		jsonLD, _ := json.Marshal(map[string]any{"@context": "https://schema.org", "@type": "Article", "headline": metadata.Frontmatter.Title, "url": strictAbsoluteURL(plan, currentRoute)})
		_, _ = fmt.Fprintf(&output, `<script type="application/ld+json">%s</script>`, jsonLD)
	}
	_, _ = fmt.Fprintf(&output, `<link rel="stylesheet" href="%s"><link rel="canonical" href="%s"></head><body data-obsite-root data-obsite-kind="strict"><header class="site-header"><a class="site-title" href="%s">%s</a><nav aria-label="Global navigation">`, esc(strictSitePath(plan, "/style.css")), esc(strictAbsoluteURL(plan, currentRoute)), esc(strictSitePath(plan, "/")), esc(plan.Config.Title))
	for _, item := range plan.Config.Navigation {
		href, active := item.URL, false
		ariaValue := ""
		if item.Section != "" {
			for _, section := range plan.Sections {
				if section != nil && section.RelPath == item.Section {
					href = strictSitePath(plan, section.Route)
					if currentRoute == section.Route {
						ariaValue = "page"
					} else if strings.HasPrefix(currentRoute, strings.TrimSuffix(section.Route, "/")+"/") {
						ariaValue = "location"
					}
					active = ariaValue != ""
				}
			}
		} else {
			if strings.HasPrefix(href, "/") {
				href = strictSitePath(plan, href)
			}
			active = currentRoute == item.URL
			if active {
				ariaValue = "page"
			}
		}
		aria := ""
		if active {
			aria = ` aria-current="` + ariaValue + `"`
		}
		_, _ = fmt.Fprintf(&output, `<a href="%s"%s>%s</a>`, esc(href), aria, esc(item.Name))
	}
	output.WriteString(`</nav></header><main data-obsite-main>`)
	if plan.Config.Sidebar.Enabled {
		output.WriteString(`<aside class="sidebar" data-obsite-sidebar><nav aria-label="Sidebar">`)
		for _, section := range plan.Sections {
			if section != nil && section.EffectivePublish && section.VersionID == versionID {
				_, _ = fmt.Fprintf(&output, `<a href="%s">%s</a>`, esc(strictSitePath(plan, section.Route)), esc(section.Title))
			}
		}
		output.WriteString(`</nav></aside>`)
	}
	output.WriteString(`<nav class="breadcrumbs" aria-label="Breadcrumb">`)
	for _, crumb := range breadcrumbs {
		_, _ = fmt.Fprintf(&output, `<a href="%s">%s</a>`, esc(strictSitePath(plan, crumb.URL)), esc(crumb.Name))
	}
	_, _ = fmt.Fprintf(&output, `</nav>%s`, body)
	if versionID != "" {
		output.WriteString(`<nav class="version-selector" aria-label="Versions">`)
		for _, version := range plan.Versions {
			if version == nil {
				continue
			}
			href := version.Root.Route
			if versionRoutes != nil && versionRoutes[version.ID] != "" {
				href = versionRoutes[version.ID]
			}
			href = strictSitePath(plan, href)
			aria := ""
			if version.ID == versionID {
				aria = ` aria-current="page"`
			}
			_, _ = fmt.Fprintf(&output, `<a href="%s"%s>%s</a>`, esc(href), aria, esc(version.Label))
		}
		output.WriteString(`</nav>`)
	}
	if sourcePath != "" && (plan.Config.Source.EditURL != "" || plan.Config.Source.ViewURL != "") {
		output.WriteString(`<nav class="source-links" aria-label="Source">`)
		if plan.Config.Source.EditURL != "" {
			_, _ = fmt.Fprintf(&output, `<a href="%s">Edit this page</a>`, esc(strictSourceURL(plan.Config.Source.EditURL, sourcePath)))
		}
		if plan.Config.Source.ViewURL != "" {
			_, _ = fmt.Fprintf(&output, `<a href="%s">View source</a>`, esc(strictSourceURL(plan.Config.Source.ViewURL, sourcePath)))
		}
		output.WriteString(`</nav>`)
	}
	output.WriteString(`</main><footer><small>Generated by Obsite</small></footer></body></html>`)
	return []byte(output.String())
}

func strictMarkdown(source []byte) (string, error) {
	var output bytes.Buffer
	if err := goldmark.New().Convert(source, &output); err != nil {
		return "", err
	}
	return output.String(), nil
}
func findStrictSection(plan *model.SitePlan, relPath, versionID string) *model.Section {
	for _, section := range plan.Sections {
		if section != nil && section.RelPath == relPath && section.VersionID == versionID {
			return section
		}
	}
	return nil
}
func strictSitePath(plan *model.SitePlan, route string) string {
	if plan == nil || route == "" {
		return route
	}
	parsed, err := url.Parse(plan.Config.BaseURL)
	if err != nil {
		return route
	}
	prefix := strings.TrimSuffix(parsed.EscapedPath(), "/")
	if prefix == "" {
		return route
	}
	return prefix + "/" + strings.TrimPrefix(route, "/")
}
func strictAbsoluteURL(plan *model.SitePlan, route string) string {
	return strings.TrimSuffix(plan.Config.BaseURL, "/") + route
}
func strictSourceURL(templateURL, sourcePath string) string {
	return strings.Replace(templateURL, ":path", strictEncodePath(sourcePath), 1)
}
func strictEncodePath(value string) string {
	parts := strings.Split(value, "/")
	for i, part := range parts {
		parts[i] = strictEncodeSegment(norm.NFKC.String(part))
	}
	return strings.Join(parts, "/")
}
func strictEncodeSegment(value string) string {
	const hex = "0123456789ABCDEF"
	var builder strings.Builder
	for _, b := range []byte(value) {
		if b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '-' || b == '.' || b == '_' || b == '~' {
			builder.WriteByte(b)
		} else {
			builder.WriteByte('%')
			builder.WriteByte(hex[b>>4])
			builder.WriteByte(hex[b&15])
		}
	}
	return builder.String()
}
func esc(value string) string { return template.HTMLEscapeString(value) }
func StrictRouteOutputPath(route string) string {
	trimmed := strings.Trim(route, "/")
	if trimmed == "" {
		return "index.html"
	}
	return path.Join(trimmed, "index.html")
}
