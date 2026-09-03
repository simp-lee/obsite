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

	"github.com/simp-lee/obsite/internal/diag"
	"github.com/simp-lee/obsite/internal/markdown"
	"github.com/simp-lee/obsite/internal/model"
	"github.com/simp-lee/obsite/internal/slug"
	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// RenderStrictSection renders the fixed shell for a normalized section page.
// It consumes only the immutable model and keeps navigation server-rendered.
func RenderStrictSection(plan *model.SitePlan, section *model.Section, index *model.VaultIndex, assets markdown.AssetSink) ([]byte, error) {
	if plan == nil || section == nil {
		return nil, fmt.Errorf("section page requires a plan and section")
	}
	sectionNote := &model.Note{
		RelPath: section.SourcePath, Route: section.Route, VersionID: section.VersionID, BasePath: strictBasePath(plan), Slug: strings.Trim(section.Route, "/"),
		RawContent: section.RawContent, Headings: section.Headings, HeadingSections: section.HeadingSections,
		OutLinks: section.OutLinks, Embeds: section.Embeds, ImageRefs: section.ImageRefs,
		HasMath: section.HasMath, HasMermaid: section.HasMermaid,
	}
	content, err := strictMarkdown(index, sectionNote, assets)
	if err != nil {
		return nil, err
	}
	var body strings.Builder
	_, _ = fmt.Fprintf(&body, `<section class="section-landing"><header><h1>%s</h1>`, esc(section.Title))
	if section.Description != "" {
		_, _ = fmt.Fprintf(&body, `<p class="section-description">%s</p>`, esc(section.Description))
	}
	if section.Banner != "" {
		if section.BannerURL == "" {
			return nil, fmt.Errorf("section banner %q has no planned asset destination", section.Banner)
		}
		_, _ = fmt.Fprintf(&body, `<img class="page-banner" src="%s" alt="%s" />`, esc(strictSitePath(plan, "/"+strings.TrimPrefix(section.BannerURL, "/"))), esc(section.BannerAlt))
	}
	body.WriteString(`</header><div class="entry-content section-content" data-page-content>`)
	body.WriteString(content)
	body.WriteString(`</div>`)
	visibleChildren := publishedSectionChildren(section)
	if len(visibleChildren) > 0 {
		body.WriteString(`<h2>Sections</h2><ul class="section-children">`)
		for _, child := range visibleChildren {
			_, _ = fmt.Fprintf(&body, `<li><a href="%s">%s</a></li>`, esc(strictSitePath(plan, child.Route)), esc(child.Title))
		}
		body.WriteString(`</ul>`)
	}
	if versions := strictChildVersions(plan, section); len(versions) > 0 {
		body.WriteString(`<h2>Versions</h2><ul class="section-versions">`)
		for _, version := range versions {
			if version != nil && version.Root != nil {
				_, _ = fmt.Fprintf(&body, `<li><a href="%s">%s</a></li>`, esc(strictSitePath(plan, version.Root.Route)), esc(version.Label))
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
	body.WriteString(`</section>`)
	return strictDocument(plan, section.Route, section.Title, section.Description, section.Breadcrumbs, section.VersionID, section.VersionRoutes, "", nil, section.SourcePath, body.String())
}

// RenderStrictArticle renders a normalized article page and its single shared
// document reading-flow metadata.
func RenderStrictArticle(plan *model.SitePlan, article *model.Note, previous, next *model.Note, position, total int, backlinks, related []*model.Note, index *model.VaultIndex, assets markdown.AssetSink) ([]byte, error) {
	if plan == nil || article == nil {
		return nil, fmt.Errorf("article page requires a plan and article")
	}
	renderArticle := *article
	renderArticle.BasePath = strictBasePath(plan)
	content, err := strictMarkdown(index, &renderArticle, assets)
	if err != nil {
		return nil, err
	}
	if plan.Config.Popover.Enabled {
		content, err = annotateStrictPopovers(content, article, index)
		if err != nil {
			return nil, err
		}
	}
	section := findStrictSection(plan, article.SectionPath, article.VersionID)
	breadcrumbs := []model.Breadcrumb(nil)
	if section != nil {
		breadcrumbs = section.Breadcrumbs
	}
	var body strings.Builder
	_, _ = fmt.Fprintf(&body, `<article class="article-page article-sheet"><header><h1>%s</h1>`, esc(article.Frontmatter.Title))
	if article.Frontmatter.Description != "" {
		_, _ = fmt.Fprintf(&body, `<p class="article-description">%s</p>`, esc(article.Frontmatter.Description))
	}
	if article.Frontmatter.Banner != "" {
		if article.BannerURL == "" {
			return nil, fmt.Errorf("article banner %q has no planned asset destination", article.Frontmatter.Banner)
		}
		_, _ = fmt.Fprintf(&body, `<img class="page-banner" src="%s" alt="%s" />`, esc(strictSitePath(plan, "/"+strings.TrimPrefix(article.BannerURL, "/"))), esc(article.Frontmatter.BannerAlt))
	}
	_, _ = fmt.Fprintf(&body, `</header>`)
	writeStrictArticleMetadata(&body, article)
	writeStrictTOC(&body, article)
	_, _ = fmt.Fprintf(&body, `<div class="entry-content article-content" data-page-content>%s</div>`, content)
	if len(article.Tags) > 0 {
		body.WriteString(`<ul class="article-tags" aria-label="Tags">`)
		for _, tag := range article.Tags {
			_, _ = fmt.Fprintf(&body, `<li><a href="%s">%s</a></li>`, esc(strictSitePath(plan, "/tags/"+strictEncodePath(tag)+"/")), esc(tag))
		}
		body.WriteString(`</ul>`)
	}
	if article.Frontmatter.Status == "deprecated" {
		body.WriteString(`<aside class="deprecated-notice" role="note">This page is deprecated.</aside>`)
	}
	if len(related) > 0 {
		body.WriteString(`<section class="related-articles"><h2>Related articles</h2><ul>`)
		for _, note := range related {
			if note != nil {
				_, _ = fmt.Fprintf(&body, `<li><a href="%s">%s</a></li>`, esc(strictSitePath(plan, note.Route)), esc(note.Frontmatter.Title))
			}
		}
		body.WriteString(`</ul></section>`)
	}
	if len(backlinks) > 0 {
		body.WriteString(`<section class="backlinks"><h2>Backlinks</h2><ul>`)
		for _, note := range backlinks {
			if note != nil {
				_, _ = fmt.Fprintf(&body, `<li><a href="%s">%s</a></li>`, esc(strictSitePath(plan, note.Route)), esc(note.Frontmatter.Title))
			}
		}
		body.WriteString(`</ul></section>`)
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
	body.WriteString(`</article>`)
	return strictDocument(plan, article.Route, article.Frontmatter.Title, article.Frontmatter.Description, breadcrumbs, article.VersionID, article.VersionRoutes, article.SocialImage, article, article.RelPath, body.String())
}

// RenderStrictNotFound renders the fixed static 404 page.
func RenderStrictNotFound(plan *model.SitePlan) ([]byte, error) {
	if plan == nil {
		return nil, fmt.Errorf("404 page requires a plan")
	}
	body := `<section class="not-found-page"><h1>Not found</h1><p>The requested page could not be found.</p><a href="` + esc(strictSitePath(plan, "/")) + `">Home</a></section>`
	return strictDocument(plan, "/404.html", "Not found", "", nil, "", nil, "", nil, "", body)
}

// RenderStrictTag renders a deterministic tag archive from the normalized index.
func RenderStrictTag(plan *model.SitePlan, tag *model.Tag, notes []*model.Note) ([]byte, error) {
	if plan == nil || tag == nil {
		return nil, fmt.Errorf("tag page requires a plan and tag")
	}
	var body strings.Builder
	_, _ = fmt.Fprintf(&body, `<section class="tag-page"><h1>Tag: %s</h1><ul class="tag-articles">`, esc(tag.Name))
	for _, note := range notes {
		if note != nil {
			_, _ = fmt.Fprintf(&body, `<li><a href="%s">%s</a></li>`, esc(strictSitePath(plan, note.Route)), esc(note.Frontmatter.Title))
		}
	}
	body.WriteString(`</ul></section>`)
	route := "/" + slug.EncodePath(tag.Slug) + "/"
	return strictDocument(plan, route, "Tag: "+tag.Name, "", nil, "", nil, "", nil, "", body.String())
}

// RenderStrictTimeline renders the optional recent-article archive.
func RenderStrictTimeline(plan *model.SitePlan, route string, notes []*model.Note) ([]byte, error) {
	if plan == nil || strings.Trim(route, "/") == "" {
		return nil, fmt.Errorf("timeline page requires a plan and route")
	}
	var body strings.Builder
	_, _ = fmt.Fprintf(&body, `<section class="timeline-page"><h1>Recent articles</h1><ul>`)
	for _, note := range notes {
		if note != nil {
			_, _ = fmt.Fprintf(&body, `<li><a href="%s">%s</a></li>`, esc(strictSitePath(plan, note.Route)), esc(note.Frontmatter.Title))
		}
	}
	body.WriteString(`</ul></section>`)
	return strictDocument(plan, "/"+strings.Trim(route, "/")+"/", "Recent articles", "", nil, "", nil, "", nil, "", body.String())
}

func annotateStrictPopovers(content string, article *model.Note, index *model.VaultIndex) (string, error) {
	if strings.TrimSpace(content) == "" || article == nil || index == nil {
		return content, nil
	}
	context := &xhtml.Node{Type: xhtml.ElementNode, DataAtom: atom.Div, Data: "div"}
	nodes, err := xhtml.ParseFragment(strings.NewReader(content), context)
	if err != nil {
		return "", fmt.Errorf("parse article HTML for popovers: %w", err)
	}
	base, _ := url.Parse("https://obsite.invalid" + article.Route)
	var visit func(*xhtml.Node)
	visit = func(node *xhtml.Node) {
		if node == nil {
			return
		}
		if node.Type == xhtml.ElementNode && node.Data == "a" {
			for _, attribute := range node.Attr {
				if strings.EqualFold(attribute.Key, "href") && attribute.Val != "" && !strings.HasPrefix(attribute.Val, "#") {
					if target := strictPopoverTarget(base, attribute.Val, index); target != nil {
						node.Attr = append(node.Attr, xhtml.Attribute{Key: "data-popover-path", Val: target.RelPath})
					}
					break
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	for _, node := range nodes {
		visit(node)
	}
	var output bytes.Buffer
	for _, node := range nodes {
		if err := xhtml.Render(&output, node); err != nil {
			return "", err
		}
	}
	return output.String(), nil
}

func strictPopoverTarget(base *url.URL, href string, index *model.VaultIndex) *model.Note {
	if base == nil || index == nil {
		return nil
	}
	targetURL, err := url.Parse(href)
	if err != nil || targetURL.IsAbs() || targetURL.Host != "" {
		return nil
	}
	resolved := base.ResolveReference(targetURL)
	cleaned := strings.TrimSuffix(resolved.EscapedPath(), "/index.html")
	if cleaned == "" {
		cleaned = "/"
	}
	if !strings.HasSuffix(cleaned, "/") {
		cleaned += "/"
	}
	for _, note := range index.Notes {
		if note != nil && note.Route == cleaned {
			return note
		}
	}
	return nil
}

func writeStrictTOC(body *strings.Builder, article *model.Note) {
	if body == nil || article == nil || len(article.Headings) == 0 {
		return
	}
	items := 0
	for _, heading := range article.Headings {
		if heading.ID != "" && strings.TrimSpace(heading.Text) != "" {
			items++
		}
	}
	if items == 0 {
		return
	}
	body.WriteString(`<nav class="table-of-contents" aria-label="Table of contents"><ol>`)
	for _, heading := range article.Headings {
		if heading.ID != "" && strings.TrimSpace(heading.Text) != "" {
			_, _ = fmt.Fprintf(body, `<li class="toc-level-%d"><a href="#%s">%s</a></li>`, heading.Level, esc(heading.ID), esc(heading.Text))
		}
	}
	body.WriteString(`</ol></nav>`)
}

func writeStrictArticleMetadata(body *strings.Builder, article *model.Note) {
	if body == nil || article == nil {
		return
	}
	values := []struct{ name, value string }{
		{"Author", article.Frontmatter.Author},
		{"Published", strictMetadataTime(article.Frontmatter.Date)},
		{"Updated", strictMetadataTime(article.Frontmatter.Updated)},
		{"Reviewed", strictMetadataTime(article.Frontmatter.Reviewed)},
		{"Status", article.Frontmatter.Status},
		{"Audience", article.Frontmatter.Audience},
		{"Product version", article.Frontmatter.ProductVersion},
		{"Series", article.Frontmatter.Series},
	}
	written := false
	for _, item := range values {
		if item.value != "" {
			if !written {
				body.WriteString(`<dl class="article-metadata">`)
				written = true
			}
			_, _ = fmt.Fprintf(body, `<dt>%s</dt><dd>%s</dd>`, esc(item.name), esc(item.value))
		}
	}
	if written {
		body.WriteString(`</dl>`)
	}
}

func strictMetadataTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func strictDocument(plan *model.SitePlan, currentRoute, title, description string, breadcrumbs []model.Breadcrumb, versionID string, versionRoutes map[string]string, socialImage string, metadata *model.Note, sourcePath, body string) ([]byte, error) {
	canonical := strictAbsoluteURL(plan, currentRoute)
	runtimePath, err := SharedRuntimeOutputPath()
	if err != nil {
		return nil, fmt.Errorf("resolve shared runtime: %w", err)
	}
	slots, err := RenderThemeSlots(plan.Config.ThemeSlots, SlotData{
		Kind: strictSlotKind(currentRoute, metadata, sourcePath), Title: title, Canonical: canonical,
		RelPath: currentRoute, SiteRootRel: strictSlotRootRel(currentRoute),
		Site: SlotSiteData{Title: plan.Config.Title, BaseURL: plan.Config.BaseURL, Author: plan.Config.Author, Description: plan.Config.Description, Language: plan.Config.Language},
	})
	if err != nil {
		return nil, err
	}
	var output strings.Builder
	rootAttrs := `data-obsite-base-path="` + esc(strictBasePath(plan)) + `" data-obsite-kind="strict"`
	if plan.Config.Sidebar.Enabled && hasPublishedSidebarEntries(strictSidebarRoot(plan, versionID)) {
		rootAttrs += ` data-obsite-sidebar`
	}
	if versionID != "" {
		rootAttrs += ` data-obsite-version="` + esc(versionID) + `"`
	}
	if plan.Config.Popover.Enabled {
		rootAttrs += ` data-obsite-popover`
	}
	if strings.Contains(body, "data-obsite-math-source") {
		rootAttrs += ` data-obsite-math`
	}
	if strings.Contains(body, `class="mermaid"`) {
		rootAttrs += ` data-obsite-mermaid`
	}
	_, _ = fmt.Fprintf(&output, `<!doctype html><html lang="%s" %s><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>%s | %s</title>`, esc(plan.Config.Language), rootAttrs, esc(title), esc(plan.Config.Title))
	if description != "" {
		_, _ = fmt.Fprintf(&output, `<meta name="description" content="%s">`, esc(description))
	}
	_, _ = fmt.Fprintf(&output, `<meta property="og:url" content="%s"><meta property="og:title" content="%s"><meta name="twitter:title" content="%s">`, esc(canonical), esc(title), esc(title))
	if description != "" {
		_, _ = fmt.Fprintf(&output, `<meta property="og:description" content="%s"><meta name="twitter:description" content="%s">`, esc(description), esc(description))
	}
	socialImageURL := ""
	if socialImage != "" {
		socialImageURL = strictAbsoluteURL(plan, "/"+strings.TrimPrefix(socialImage, "/"))
		_, _ = fmt.Fprintf(&output, `<meta property="og:image" content="%s"><meta name="twitter:card" content="summary_large_image"><meta name="twitter:image" content="%s">`, esc(socialImageURL), esc(socialImageURL))
	} else if plan.Config.DefaultImgURL != "" {
		imageURL := plan.Config.DefaultImgURL
		if !plan.Config.DefaultImgExternal {
			imageURL = strictAbsoluteURL(plan, "/"+strings.TrimPrefix(imageURL, "/"))
		}
		_, _ = fmt.Fprintf(&output, `<meta property="og:image" content="%s">`, esc(imageURL))
	}
	if metadata != nil {
		output.WriteString(`<meta property="og:type" content="article">`)
		if metadata.Frontmatter.Author != "" {
			_, _ = fmt.Fprintf(&output, `<meta name="author" content="%s"><meta property="article:author" content="%s">`, esc(metadata.Frontmatter.Author), esc(metadata.Frontmatter.Author))
		}
		if metadata.Frontmatter.Audience != "" {
			_, _ = fmt.Fprintf(&output, `<meta name="audience" content="%s">`, esc(metadata.Frontmatter.Audience))
		}
		if metadata.Frontmatter.ProductVersion != "" {
			_, _ = fmt.Fprintf(&output, `<meta name="product-version" content="%s">`, esc(metadata.Frontmatter.ProductVersion))
		}
		if metadata.Frontmatter.Series != "" {
			_, _ = fmt.Fprintf(&output, `<meta name="series" content="%s">`, esc(metadata.Frontmatter.Series))
		}
		if metadata.Frontmatter.Status != "" {
			_, _ = fmt.Fprintf(&output, `<meta name="status" content="%s">`, esc(metadata.Frontmatter.Status))
		}
		if !metadata.Frontmatter.Date.IsZero() {
			_, _ = fmt.Fprintf(&output, `<meta property="article:published_time" content="%s">`, esc(metadata.Frontmatter.Date.UTC().Format(time.RFC3339)))
		}
		if !metadata.Frontmatter.Updated.IsZero() {
			_, _ = fmt.Fprintf(&output, `<meta property="article:modified_time" content="%s">`, esc(metadata.Frontmatter.Updated.UTC().Format(time.RFC3339)))
		}
		jsonData := map[string]any{"@context": "https://schema.org", "@type": "Article", "headline": metadata.Frontmatter.Title, "url": canonical}
		if socialImageURL != "" {
			jsonData["image"] = socialImageURL
		}
		if description != "" {
			jsonData["description"] = description
		}
		if metadata.Frontmatter.Author != "" {
			jsonData["author"] = map[string]string{"@type": "Person", "name": metadata.Frontmatter.Author}
		}
		if !metadata.Frontmatter.Date.IsZero() {
			jsonData["datePublished"] = metadata.Frontmatter.Date.UTC().Format(time.RFC3339)
		}
		if !metadata.Frontmatter.Updated.IsZero() {
			jsonData["dateModified"] = metadata.Frontmatter.Updated.UTC().Format(time.RFC3339)
		}
		if !metadata.Frontmatter.Reviewed.IsZero() {
			jsonData["lastReviewed"] = metadata.Frontmatter.Reviewed.UTC().Format(time.RFC3339)
		}
		if metadata.Frontmatter.Status != "" {
			jsonData["creativeWorkStatus"] = metadata.Frontmatter.Status
		}
		if metadata.Frontmatter.Audience != "" {
			jsonData["audience"] = map[string]string{"@type": "Audience", "audienceType": metadata.Frontmatter.Audience}
		}
		if metadata.Frontmatter.ProductVersion != "" {
			jsonData["version"] = metadata.Frontmatter.ProductVersion
		}
		if metadata.Frontmatter.Series != "" {
			jsonData["isPartOf"] = metadata.Frontmatter.Series
		}
		if len(metadata.Tags) > 0 {
			jsonData["keywords"] = metadata.Tags
		}
		jsonLD, _ := json.Marshal(jsonData)
		_, _ = fmt.Fprintf(&output, `<script type="application/ld+json">%s</script>`, jsonLD)
	}
	_, _ = fmt.Fprintf(&output, `<script src="%s"></script><link rel="stylesheet" href="%s"><link rel="canonical" href="%s">`, esc(strictSitePath(plan, "/"+runtimePath)), esc(strictSitePath(plan, "/style.css")), esc(canonical))
	if plan.Config.CustomCSS != "" {
		_, _ = fmt.Fprintf(&output, `<link rel="stylesheet" href="%s">`, esc(strictSitePath(plan, "/assets/custom.css")))
	}
	if plan.Config.ThemeCSS != "" {
		_, _ = fmt.Fprintf(&output, `<link rel="stylesheet" href="%s">`, esc(strictSitePath(plan, "/assets/theme/theme.css")))
	}
	if strings.Contains(body, "data-obsite-math-source") {
		_, _ = fmt.Fprintf(&output, `<link rel="stylesheet" href="%s">`, esc(strictSitePath(plan, "/"+katexCSSOutputPath)))
	}
	output.WriteString(slots["obsite-head-end"])
	output.WriteString(`</head><body class="site-body" data-site-body><div class="site-frame"><header class="site-masthead site-header"><div class="masthead-band"><a class="site-mark site-title" href="` + esc(strictSitePath(plan, "/")) + `">` + esc(plan.Config.Title) + `</a></div><div class="masthead-copy"><div class="masthead-actions"><button type="button" class="theme-toggle" data-theme-toggle hidden aria-pressed="false"><span class="theme-toggle-caption">Theme</span><span class="theme-toggle-value" data-theme-toggle-value></span><span data-theme-toggle-state class="sr-only"></span><span data-theme-toggle-source class="sr-only"></span></button></div><nav aria-label="Global navigation">`)
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
			active = currentRoute == strictNavigationMatch(item.URL)
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
	output.WriteString(`</nav></div>`)
	output.WriteString(slots["obsite-header-end"])
	output.WriteString(`</header><main class="site-main" data-obsite-main>`)
	if plan.Config.Sidebar.Enabled && hasPublishedSidebarEntries(strictSidebarRoot(plan, versionID)) {
		output.WriteString(`<button type="button" class="sidebar-toggle-mobile sidebar-launch" data-sidebar-toggle hidden aria-expanded="false"><span class="sidebar-launch-icon" aria-hidden="true"></span>Open navigation</button><aside class="sidebar-shell" data-sidebar-shell><div class="sidebar-panel-head"><strong>Navigation</strong><button type="button" class="sidebar-close" data-sidebar-close>Close navigation</button></div><nav class="sidebar" aria-label="Sidebar" data-sidebar-root>`)
		output.WriteString(`<ul class="sidebar-list sidebar-list-root">`)
		strictWriteSidebarHTML(&output, plan, strictSidebarRoot(plan, versionID), currentRoute)
		output.WriteString(`</ul></nav></aside><button type="button" class="sidebar-overlay" data-sidebar-overlay hidden aria-label="Close navigation"></button>`)
	}
	output.WriteString(`<div class="site-content"><nav class="breadcrumbs" aria-label="Breadcrumb"><ol>`)
	for _, crumb := range breadcrumbs {
		_, _ = fmt.Fprintf(&output, `<li><a href="%s">%s</a></li>`, esc(strictSitePath(plan, crumb.URL)), esc(crumb.Name))
	}
	_, _ = fmt.Fprintf(&output, `</ol></nav>%s`, body)
	if versionID != "" {
		output.WriteString(`<nav class="version-selector" aria-label="Versions">`)
		for _, version := range strictPublicVersions(plan) {
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
	if plan.Config.Popover.Enabled {
		output.WriteString(`<div id="obsite-popover-card" data-popover-card hidden aria-hidden="true"></div>`)
	}
	output.WriteString(slots["obsite-main-end"])
	output.WriteString(`</div></main><footer class="site-footer"><small>Generated by Obsite</small>`)
	output.WriteString(slots["obsite-footer-end"])
	output.WriteString(`</footer></div></body></html>`)
	return []byte(output.String()), nil
}

func strictBasePath(plan *model.SitePlan) string {
	if plan == nil {
		return "/"
	}
	parsed, err := url.Parse(plan.Config.BaseURL)
	if err != nil || parsed.EscapedPath() == "" {
		return "/"
	}
	return parsed.EscapedPath()
}

func hasPublishedSidebarEntries(section *model.Section) bool {
	if section == nil {
		return false
	}
	for _, child := range section.Children {
		if child != nil && child.EffectivePublish && child.Route != "" {
			return true
		}
	}
	for _, article := range section.Articles {
		if article != nil && article.Route != "" {
			return true
		}
	}
	return false
}

func strictSidebarRoot(plan *model.SitePlan, versionID string) *model.Section {
	if plan == nil {
		return nil
	}
	if versionID != "" {
		for _, version := range plan.Versions {
			if version != nil && version.ID == versionID {
				return version.Root
			}
		}
	}
	return plan.Root
}

func strictWriteSidebarHTML(output *strings.Builder, plan *model.SitePlan, section *model.Section, currentRoute string) {
	if output == nil || section == nil {
		return
	}
	for _, child := range section.Children {
		if child == nil || !child.EffectivePublish {
			continue
		}
		_, _ = fmt.Fprintf(output, `<li><a class="sidebar-link sidebar-link-dir" href="%s"%s>%s</a>`, esc(strictSitePath(plan, child.Route)), strictCurrentARIA(child.Route, currentRoute), esc(child.Title))
		if len(child.Children) > 0 || len(child.Articles) > 0 {
			output.WriteString(`<ul class="sidebar-list">`)
			strictWriteSidebarHTML(output, plan, child, currentRoute)
			output.WriteString(`</ul>`)
		}
		output.WriteString(`</li>`)
	}
	for _, article := range section.Articles {
		if article == nil {
			continue
		}
		_, _ = fmt.Fprintf(output, `<li><a class="sidebar-link" href="%s"%s>%s</a></li>`, esc(strictSitePath(plan, article.Route)), strictCurrentARIA(article.Route, currentRoute), esc(article.Frontmatter.Title))
	}
}

func strictCurrentARIA(route, current string) string {
	if route == current {
		return ` aria-current="page"`
	}
	return ""
}

func strictSlotKind(route string, metadata *model.Note, sourcePath string) string {
	if metadata != nil {
		return "article"
	}
	if route == "/404.html" {
		return "404"
	}
	if strings.HasPrefix(route, "/tags/") {
		return "tag"
	}
	if sourcePath != "" {
		return "section"
	}
	return "timeline"
}

func strictSlotRootRel(route string) string {
	cleaned := strings.Trim(route, "/")
	if cleaned == "" {
		return "./"
	}
	segments := strings.Split(cleaned, "/")
	if len(segments) > 0 && segments[len(segments)-1] == "404.html" {
		return "./"
	}
	return strings.Repeat("../", len(segments))
}

func strictMarkdown(index *model.VaultIndex, note *model.Note, assets markdown.AssetSink) (string, error) {
	var output bytes.Buffer
	collector := diag.NewCollector()
	md, _ := markdown.NewMarkdown(index, note, assets, collector)
	if err := md.Convert(note.RawContent, &output); err != nil {
		return "", err
	}
	return output.String(), nil
}

func strictPublicVersions(plan *model.SitePlan) []*model.Version {
	if plan == nil {
		return nil
	}
	result := make([]*model.Version, 0, len(plan.Versions))
	for _, version := range plan.Versions {
		if version != nil && version.Root != nil && version.Root.EffectivePublish && version.Root.Route != "" {
			result = append(result, version)
		}
	}
	return result
}

func publishedSectionChildren(section *model.Section) []*model.Section {
	if section == nil {
		return nil
	}
	result := make([]*model.Section, 0, len(section.Children))
	for _, child := range section.Children {
		if child != nil && child.EffectivePublish && child.Route != "" {
			result = append(result, child)
		}
	}
	return result
}

func strictChildVersions(plan *model.SitePlan, section *model.Section) []*model.Version {
	if plan == nil || section == nil {
		return nil
	}
	result := make([]*model.Version, 0)
	for _, version := range strictPublicVersions(plan) {
		if path.Dir(version.Root.RelPath) == section.RelPath {
			result = append(result, version)
		}
	}
	return result
}

func findStrictSection(plan *model.SitePlan, relPath, versionID string) *model.Section {
	for _, section := range plan.Sections {
		if section != nil && section.RelPath == relPath && section.VersionID == versionID {
			return section
		}
	}
	return nil
}

func strictNavigationMatch(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || !strings.HasPrefix(raw, "/") {
		return raw
	}
	if parsed.Path == "/" || parsed.Path == "" {
		return "/"
	}
	return "/" + slug.EncodePath(strings.Trim(parsed.Path, "/")) + "/"
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
	return slug.EncodePath(value)
}
func esc(value string) string { return template.HTMLEscapeString(value) }
func StrictRouteOutputPath(route string) string {
	trimmed := strings.Trim(route, "/")
	if trimmed == "" {
		return "index.html"
	}
	return path.Join(trimmed, "index.html")
}
