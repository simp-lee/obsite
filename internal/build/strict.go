package build

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"image"
	"io"
	"net/url"
	"path"
	"strings"
	"time"

	internalasset "github.com/simp-lee/obsite/internal/asset"
	d "github.com/simp-lee/obsite/internal/diag"
	internalfsutil "github.com/simp-lee/obsite/internal/fsutil"
	"github.com/simp-lee/obsite/internal/model"
	"github.com/simp-lee/obsite/internal/render"
	"github.com/simp-lee/obsite/internal/seo"
	"github.com/simp-lee/obsite/internal/social"
)

// buildStrictSite publishes the normalized section model through the same
// managed staging publisher used by the existing build foundation.
func buildStrictSite(plan *model.SitePlan, vaultPath, outputPath string, diagnosticsWriter io.Writer) (result *BuildResult, err error) {
	result = &BuildResult{}
	if plan == nil {
		return result, fmt.Errorf("strict site plan is required")
	}
	boundary, err := internalfsutil.ResolveVaultOutput(vaultPath, outputPath)
	if err != nil {
		return result, err
	}
	result.OutputPath = boundary.OutputPath
	publisher, err := prepareStagedOutputPublisher(boundary.VaultPath, boundary.OutputPath)
	if err != nil {
		return result, err
	}
	defer func() {
		if finalizeErr := publisher.Finalize(err == nil); finalizeErr != nil {
			if err == nil {
				err = finalizeErr
			} else {
				err = fmt.Errorf("publish strict site: %w; cleanup: %v", err, finalizeErr)
			}
		}
		if publisher.cleanupErr != nil {
			warning := d.Diagnostic{Severity: d.SeverityWarning, Kind: d.KindOutputCleanup, Message: publisher.cleanupErr.Error()}
			result.Diagnostics = append(result.Diagnostics, warning)
			result.WarningCount++
			if diagnosticsWriter != nil {
				_, _ = fmt.Fprintf(diagnosticsWriter, "%s %s: %s\n", warning.Severity, warning.Kind, warning.Message)
			}
		}
	}()
	staging := publisher.OutputPath()
	if err := writeManagedOutputMarker(staging); err != nil {
		return result, err
	}
	if plan.Config.Sidebar.Enabled {
		data, sidebarErr := json.Marshal(strictSidebar(plan))
		if sidebarErr != nil {
			return result, sidebarErr
		}
		if writeErr := writeOutputFile(staging, "assets/obsite/sidebar.json", data); writeErr != nil {
			return result, writeErr
		}
	}
	assets, err := prepareStrictAssets(boundary.VaultPath, plan)
	if err != nil {
		return result, err
	}
	if err := internalasset.CopyAssetsWithReservedPaths(boundary.VaultPath, staging, assets, nil, []string{"style.css", "assets/obsite-runtime", "assets/obsite"}); err != nil {
		return result, fmt.Errorf("publish strict assets: %w", err)
	}
	applyStrictAssetURLs(plan, assets)
	result.Assets = assets
	for _, section := range plan.Sections {
		if section == nil || section.Route == "" {
			continue
		}
		data, renderErr := render.RenderStrictSection(plan, section)
		if renderErr != nil {
			return result, fmt.Errorf("render section %q: %w", section.RelPath, renderErr)
		}
		if writeErr := writeOutputFile(staging, render.StrictRouteOutputPath(section.Route), data); writeErr != nil {
			return result, writeErr
		}
	}
	for _, article := range plan.Articles {
		if article == nil || article.Route == "" {
			continue
		}
		section := strictArticleSection(plan, article)
		cover, coverErr := strictCoverBytes(boundary.VaultPath, article.Frontmatter.Cover)
		if coverErr != nil {
			return result, fmt.Errorf("read cover for %q: %w", article.RelPath, coverErr)
		}
		context := ""
		if section != nil {
			context = section.Title
		}
		if article.VersionID != "" {
			for _, version := range plan.Versions {
				if version != nil && version.ID == article.VersionID {
					if context != "" {
						context += " / "
					}
					context += version.Label
				}
			}
		}
		card, cardErr := social.Generate(social.Input{CanonicalURL: strictBuildCanonicalURL(plan.Config.BaseURL, article.Route), SiteTitle: plan.Config.Title, Title: article.Frontmatter.Title, Context: context, Author: article.Frontmatter.Author, Date: strictCardDate(article.Frontmatter.Date), Status: article.Frontmatter.Status, Cover: cover})
		if cardErr != nil {
			return result, fmt.Errorf("generate social card for %q: %w", article.RelPath, cardErr)
		}
		article.SocialImage = card.Path
		if writeErr := writeOutputFile(staging, card.Path, card.PNG); writeErr != nil {
			return result, writeErr
		}
		previous, next, position, total := strictReadingFlow(section, article)
		data, renderErr := render.RenderStrictArticle(plan, article, previous, next, position, total)
		if renderErr != nil {
			return result, fmt.Errorf("render article %q: %w", article.RelPath, renderErr)
		}
		if writeErr := writeOutputFile(staging, render.StrictRouteOutputPath(article.Route), data); writeErr != nil {
			return result, writeErr
		}
		result.NotePages++
	}
	if _, err := render.EmitStyleCSS(staging); err != nil {
		return result, fmt.Errorf("emit style.css: %w", err)
	}
	if err := render.EmitRuntimeAssets(staging); err != nil {
		return result, fmt.Errorf("emit runtime assets: %w", err)
	}
	if err := writeStrictMetadataOutputs(staging, plan); err != nil {
		return result, err
	}
	if diagnosticsWriter != nil {
		_ = diagnosticsWriter
	}
	return result, nil
}

func strictSidebar(plan *model.SitePlan) []model.SidebarNode {
	if plan == nil || plan.Root == nil {
		return nil
	}
	return strictSidebarChildren(plan.Root)
}
func strictSidebarChildren(section *model.Section) []model.SidebarNode {
	if section == nil {
		return nil
	}
	result := make([]model.SidebarNode, 0, len(section.Children)+len(section.Articles))
	for _, child := range section.Children {
		if child != nil && child.EffectivePublish {
			result = append(result, model.SidebarNode{Name: child.Title, URL: child.Route, IsDir: true, Children: strictSidebarChildren(child)})
		}
	}
	for _, article := range section.Articles {
		if article != nil {
			result = append(result, model.SidebarNode{Name: article.Frontmatter.Title, URL: article.Route})
		}
	}
	return result
}

func writeStrictMetadataOutputs(outputRoot string, plan *model.SitePlan) error {
	var sitemap strings.Builder
	sitemap.WriteString(`<?xml version="1.0" encoding="UTF-8"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`)
	for _, section := range plan.Sections {
		if section != nil && section.Route != "" {
			_, _ = fmt.Fprintf(&sitemap, `<url><loc>%s</loc>%s</url>`, strictXMLEscape(strictBuildCanonicalURL(plan.Config.BaseURL, section.Route)), strictLastMod(section.LastModified))
		}
	}
	for _, article := range plan.Articles {
		if article != nil && article.Route != "" {
			_, _ = fmt.Fprintf(&sitemap, `<url><loc>%s</loc>%s</url>`, strictXMLEscape(strictBuildCanonicalURL(plan.Config.BaseURL, article.Route)), strictLastMod(article.LastModified))
		}
	}
	sitemap.WriteString(`</urlset>`)
	if err := writeOutputFile(outputRoot, "sitemap.xml", []byte(sitemap.String())); err != nil {
		return err
	}
	if err := writeOutputFile(outputRoot, "robots.txt", []byte(seo.BuildRobots(plan.Config.BaseURL))); err != nil {
		return err
	}
	if plan.Config.RSS.Enabled {
		var rss strings.Builder
		_, _ = fmt.Fprintf(&rss, `<?xml version="1.0" encoding="UTF-8"?><rss version="2.0"><channel><title>%s</title>`, strictXMLEscape(plan.Config.Title))
		for _, article := range plan.Posts {
			if article == nil {
				continue
			}
			link := strictBuildCanonicalURL(plan.Config.BaseURL, article.Route)
			_, _ = fmt.Fprintf(&rss, `<item><title>%s</title><link>%s</link><guid>%s</guid>`, strictXMLEscape(article.Frontmatter.Title), strictXMLEscape(link), strictXMLEscape(link))
			if !article.Frontmatter.Date.IsZero() {
				_, _ = fmt.Fprintf(&rss, `<pubDate>%s</pubDate>`, article.Frontmatter.Date.UTC().Format(time.RFC1123Z))
			}
			rss.WriteString(`</item>`)
		}
		rss.WriteString(`</channel></rss>`)
		if err := writeOutputFile(outputRoot, "index.xml", []byte(rss.String())); err != nil {
			return err
		}
	}
	notFound := fmt.Sprintf(`<!doctype html><html><head><meta charset="utf-8"><title>Not found | %s</title></head><body><main><h1>Not found</h1><a href="%s">Home</a></main></body></html>`, plan.Config.Title, strictBuildLocalPath(plan.Config.BaseURL, "/"))
	return writeOutputFile(outputRoot, "404.html", []byte(notFound))
}

func strictLastMod(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return `<lastmod>` + value.UTC().Format(time.RFC3339) + `</lastmod>`
}
func strictXMLEscape(value string) string {
	var output bytes.Buffer
	_ = xml.EscapeText(&output, []byte(value))
	return output.String()
}

func prepareStrictAssets(vaultRoot string, plan *model.SitePlan) (map[string]*model.Asset, error) {
	assets := make(map[string]*model.Asset)
	add := func(source, kind string) error {
		if strings.TrimSpace(source) == "" {
			return nil
		}
		data, err := validateStrictAsset(vaultRoot, source, kind)
		if err != nil {
			return err
		}
		_ = data
		if assets[source] == nil {
			assets[source] = &model.Asset{SrcPath: source}
		}
		return nil
	}
	for _, section := range plan.Sections {
		if section != nil && section.Banner != "" {
			if err := add(section.Banner, "banner"); err != nil {
				return nil, err
			}
		}
	}
	for _, article := range plan.Articles {
		if article != nil {
			if err := add(article.Frontmatter.Banner, "banner"); err != nil {
				return nil, err
			}
			if err := add(article.Frontmatter.Cover, "cover"); err != nil {
				return nil, err
			}
		}
	}
	if len(assets) == 0 {
		return assets, nil
	}
	return assets, nil
}

func applyStrictAssetURLs(plan *model.SitePlan, assets map[string]*model.Asset) {
	for _, section := range plan.Sections {
		if section != nil && section.Banner != "" && assets[section.Banner] != nil {
			section.BannerURL = assets[section.Banner].DstPath
		}
	}
	for _, article := range plan.Articles {
		if article != nil {
			if asset := assets[article.Frontmatter.Banner]; asset != nil {
				article.BannerURL = asset.DstPath
			}
			if asset := assets[article.Frontmatter.Cover]; asset != nil {
				article.CoverURL = asset.DstPath
			}
		}
	}
}

func validateStrictAsset(vaultRoot, raw, kind string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if strings.Contains(raw, `\`) || strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || path.Clean(raw) != raw || strings.HasPrefix(path.Clean(raw), "../") || !internalfsutil.IsPortableSitePath(raw) {
		return nil, fmt.Errorf("%s %q must be a normalized vault-relative local asset", kind, raw)
	}
	lower := strings.ToLower(raw)
	allowed := strings.HasSuffix(lower, ".png") || strings.HasSuffix(lower, ".jpg") || strings.HasSuffix(lower, ".jpeg") || strings.HasSuffix(lower, ".webp")
	if kind == "banner" {
		allowed = allowed || strings.HasSuffix(lower, ".svg")
	}
	if !allowed {
		return nil, fmt.Errorf("%s %q has an unsupported format", kind, raw)
	}
	_, data, _, err := internalfsutil.ReadContainedRegularFile(vaultRoot, raw)
	if err != nil {
		return nil, fmt.Errorf("%s %q: %w", kind, raw, err)
	}
	if strings.HasSuffix(lower, ".svg") {
		text := strings.ToLower(string(data))
		if strings.Contains(text, "http://") || strings.Contains(text, "https://") || strings.Contains(text, "//") || strings.Contains(text, "@import") {
			return nil, fmt.Errorf("banner %q contains an external SVG reference", raw)
		}
		return data, nil
	}
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return nil, fmt.Errorf("%s %q cannot be decoded: %w", kind, raw, err)
	}
	return data, nil
}

func strictCoverBytes(vaultRoot, raw string) ([]byte, error) {
	return validateStrictAsset(vaultRoot, raw, "cover")
}

func strictBuildLocalPath(baseURL, route string) string {
	parsed, err := url.Parse(baseURL)
	prefix := ""
	if err == nil {
		prefix = strings.TrimSuffix(parsed.EscapedPath(), "/")
	}
	if prefix == "" {
		return route
	}
	return prefix + "/" + strings.TrimPrefix(route, "/")
}
func strictBuildCanonicalURL(baseURL, route string) string {
	return strings.TrimSuffix(baseURL, "/") + route
}
func strictCardDate(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func strictArticleSection(plan *model.SitePlan, article *model.Note) *model.Section {
	if plan == nil || article == nil {
		return nil
	}
	for _, section := range plan.Sections {
		if section != nil && section.RelPath == article.SectionPath && section.VersionID == article.VersionID {
			return section
		}
	}
	return nil
}

func strictReadingFlow(section *model.Section, article *model.Note) (previous, next *model.Note, position, total int) {
	if section == nil || article == nil || article.Frontmatter.Type != "doc" {
		return nil, nil, 0, 0
	}
	total = len(section.Documents)
	for index, candidate := range section.Documents {
		if candidate == article {
			position = index + 1
			if index > 0 {
				previous = section.Documents[index-1]
			}
			if index+1 < total {
				next = section.Documents[index+1]
			}
			break
		}
	}
	return previous, next, position, total
}
