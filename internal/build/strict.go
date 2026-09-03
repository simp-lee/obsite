package build

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	internalasset "github.com/simp-lee/obsite/internal/asset"
	d "github.com/simp-lee/obsite/internal/diag"
	internalfsutil "github.com/simp-lee/obsite/internal/fsutil"
	"github.com/simp-lee/obsite/internal/link"
	"github.com/simp-lee/obsite/internal/model"
	"github.com/simp-lee/obsite/internal/recommend"
	"github.com/simp-lee/obsite/internal/render"
	"github.com/simp-lee/obsite/internal/siteplan"
	"github.com/simp-lee/obsite/internal/slug"
	"github.com/simp-lee/obsite/internal/social"
	"github.com/tdewolff/minify/v2"
	minhtml "github.com/tdewolff/minify/v2/html"
)

// buildStrictSite publishes the normalized section model through the same
// managed staging publisher used by the existing build foundation.
func buildStrictSite(planned *siteplan.Result, vaultPath, outputPath string, diagnosticsWriter io.Writer, strict bool) (result *BuildResult, err error) {
	result = &BuildResult{}
	if planned == nil || planned.Plan == nil {
		return result, fmt.Errorf("strict site plan is required")
	}
	plan := planned.Plan
	graph, relatedByPath, relationErr := buildStrictRelations(planned)
	if relationErr != nil {
		return result, relationErr
	}
	result.Graph = graph
	boundary, err := internalfsutil.ResolveVaultOutput(vaultPath, outputPath)
	if err != nil {
		return result, err
	}
	result.OutputPath = boundary.OutputPath
	publisher, err := prepareStagedOutputPublisher(boundary.VaultPath, boundary.OutputPath)
	if err != nil {
		return result, err
	}
	previousCache := loadStrictCacheManifest(boundary.OutputPath)
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
			if strict && err == nil {
				err = fmt.Errorf("strict build output cleanup failed: %w", publisher.cleanupErr)
			}
		}
	}()
	staging := publisher.OutputPath()
	outputs := newStrictOutputRegistry(boundary.OutputPath, previousCache)
	if err := writeManagedOutputMarker(staging); err != nil {
		return result, err
	}
	if err := writeStrictConfiguredAssets(boundary.VaultPath, staging, plan, outputs); err != nil {
		return result, err
	}
	if planned.Index == nil {
		return result, fmt.Errorf("strict site index is required")
	}
	result.Index = planned.Index
	rebindStrictPlanNotes(plan, planned.Index)
	assets, err := prepareStrictAssets(boundary.VaultPath, plan)
	if err != nil {
		return result, err
	}
	reservedAssetOutputs := strictReservedAssetOutputs(plan)
	assetCollector, err := internalasset.NewCollectorWithResourceFiles(boundary.VaultPath, assets, reservedAssetOutputs, planned.Scan.ResourceFiles)
	if err != nil {
		return result, fmt.Errorf("plan strict assets: %w", err)
	}
	applyStrictPlannedDestinations(assets, assetCollector.PlanDestinations(assets))
	applyStrictAssetURLs(plan, assets)
	if plan.Config.Sidebar.Enabled {
		payload := strictSidebarPayload{Default: strictSidebar(plan, ""), Versions: make(map[string][]model.SidebarNode)}
		for _, version := range plan.Versions {
			if version != nil && version.Root != nil && version.Root.EffectivePublish && version.Root.Route != "" {
				payload.Versions[version.ID] = strictSidebar(plan, version.ID)
			}
		}
		data, sidebarErr := json.Marshal(payload)
		if sidebarErr != nil {
			return result, sidebarErr
		}
		if writeErr := outputs.write(staging, "assets/obsite/sidebar.json", "sidebar", data); writeErr != nil {
			return result, writeErr
		}
	}
	for _, section := range plan.Sections {
		if section == nil || section.Route == "" {
			continue
		}
		data, renderErr := render.RenderStrictSection(plan, section, planned.Index, assetCollector)
		if renderErr != nil {
			return result, fmt.Errorf("render section %q: %w", section.RelPath, renderErr)
		}
		if writeErr := writeStrictHTML(outputs, staging, render.StrictRouteOutputPath(section.Route), "section:"+section.SourcePath, data); writeErr != nil {
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
		if writeErr := outputs.write(staging, card.Path, "social:"+article.RelPath, card.PNG); writeErr != nil {
			return result, writeErr
		}
		previous, next, position, total := strictReadingFlow(section, article)
		data, renderErr := render.RenderStrictArticle(plan, article, previous, next, position, total, strictBacklinks(planned.Index, graph, article), relatedByPath[article.RelPath], planned.Index, assetCollector)
		if renderErr != nil {
			return result, fmt.Errorf("render article %q: %w", article.RelPath, renderErr)
		}
		if writeErr := writeStrictHTML(outputs, staging, render.StrictRouteOutputPath(article.Route), "article:"+article.RelPath, data); writeErr != nil {
			return result, writeErr
		}
		result.NotePages++
	}
	if plan.Config.Popover.Enabled {
		if err := writeStrictPopoverPayloads(staging, planned.Index, outputs); err != nil {
			return result, err
		}
	}
	for _, tag := range strictBuildTags(planned.Index.Tags) {
		notes := make([]*model.Note, 0, len(tag.Notes))
		for _, relPath := range tag.Notes {
			if note := planned.Index.Notes[relPath]; note != nil {
				notes = append(notes, note)
			}
		}
		data, renderErr := render.RenderStrictTag(plan, tag, notes)
		if renderErr != nil {
			return result, fmt.Errorf("render tag %q: %w", tag.Name, renderErr)
		}
		if writeErr := writeStrictHTML(outputs, staging, render.StrictRouteOutputPath("/"+tag.Slug+"/"), "tag:"+tag.Name, data); writeErr != nil {
			return result, writeErr
		}
		result.TagPages++
	}
	if plan.Config.Timeline.Enabled {
		timelineRoute := "/" + slug.EncodePath(strings.Trim(plan.Config.Timeline.Path, "/")) + "/"
		data, renderErr := render.RenderStrictTimeline(plan, timelineRoute, plan.Posts)
		if renderErr != nil {
			return result, fmt.Errorf("render timeline: %w", renderErr)
		}
		if writeErr := writeStrictHTML(outputs, staging, render.StrictRouteOutputPath(timelineRoute), "timeline", data); writeErr != nil {
			return result, writeErr
		}
	}
	allAssets := assetCollector.Snapshot()
	for source, seed := range assets {
		if allAssets[source] == nil {
			clone := *seed
			allAssets[source] = &clone
		}
	}
	applyStrictPlannedDestinations(allAssets, assetCollector.PlanDestinations(allAssets))
	assetSources := make([]string, 0, len(allAssets))
	for source := range allAssets {
		assetSources = append(assetSources, source)
	}
	sort.Strings(assetSources)
	for _, source := range assetSources {
		if asset := allAssets[source]; asset != nil {
			if err := outputs.claim(asset.DstPath, "asset:"+source); err != nil {
				return result, err
			}
		}
	}
	if err := internalasset.CopyAssetsWithReservedPaths(boundary.VaultPath, staging, allAssets, nil, reservedAssetOutputs); err != nil {
		return result, fmt.Errorf("publish strict assets: %w", err)
	}
	for _, source := range assetSources {
		if asset := allAssets[source]; asset != nil && asset.DstPath != "" {
			data, err := os.ReadFile(filepath.Join(staging, filepath.FromSlash(asset.DstPath)))
			if err != nil {
				return result, fmt.Errorf("read published asset %q: %w", source, err)
			}
			outputs.record(asset.DstPath, "asset:"+source, data)
		}
	}
	applyStrictAssetURLs(plan, allAssets)
	result.Assets = allAssets
	styleData, err := render.StyleCSSData()
	if err != nil {
		return result, fmt.Errorf("read style.css: %w", err)
	}
	if err := outputs.write(staging, "style.css", "built-in CSS", styleData); err != nil {
		return result, err
	}
	runtimeAssets, err := render.RuntimeAssetData()
	if err != nil {
		return result, fmt.Errorf("read runtime assets: %w", err)
	}
	for _, runtimeAsset := range runtimeAssets {
		if err := outputs.write(staging, runtimeAsset.OutputPath, "runtime", runtimeAsset.Data); err != nil {
			return result, err
		}
	}
	if err := writeStrictMetadataOutputs(staging, plan, planned.Index, outputs); err != nil {
		return result, err
	}
	if err := writeStrictCacheManifest(staging, plan, planned.Index, allAssets, outputs); err != nil {
		return result, err
	}
	if diagnosticsWriter != nil {
		_ = diagnosticsWriter
	}
	return result, nil
}

func writeStrictHTML(outputs *strictOutputRegistry, outputRoot, relPath, owner string, data []byte) error {
	minifier := minify.New()
	minifier.AddFunc("text/html", minhtml.Minify)
	compact, err := minifier.Bytes("text/html", data)
	if err != nil {
		return fmt.Errorf("minify %s: %w", relPath, err)
	}
	return outputs.write(outputRoot, relPath, owner, compact)
}

func buildStrictRelations(planned *siteplan.Result) (*model.LinkGraph, map[string][]*model.Note, error) {
	related := make(map[string][]*model.Note)
	if planned == nil || planned.Plan == nil || planned.Index == nil {
		return &model.LinkGraph{Forward: map[string][]string{}, Backward: map[string][]string{}}, related, nil
	}
	graph := link.BuildSourceGraph(planned.Index)
	if !planned.Plan.Config.Related.Enabled {
		return graph, related, nil
	}
	groups := make(map[string][]model.RelatedSemanticDocument)
	for _, semantic := range planned.RelatedSemantic {
		versionID := ""
		if note := planned.Index.Notes[semantic.RelPath]; note != nil {
			versionID = note.VersionID
		}
		groups[versionID] = append(groups[versionID], semantic)
	}
	groupKeys := make([]string, 0, len(groups))
	for key := range groups {
		groupKeys = append(groupKeys, key)
	}
	sort.Strings(groupKeys)
	for _, key := range groupKeys {
		engine, err := recommend.BuildEngine(groups[key], planned.Index, graph, recommend.ProductionEngineParameters(planned.Plan.Config.Related.Count, 0))
		if err != nil {
			return nil, nil, fmt.Errorf("build related articles: %w", err)
		}
		for _, document := range engine.Documents {
			items := make([]*model.Note, 0, len(document.Related))
			for _, candidate := range document.Related {
				if candidate.DocID < 0 || candidate.DocID >= len(engine.Documents) {
					continue
				}
				target := planned.Index.Notes[engine.Documents[candidate.DocID].RelPath]
				if target != nil {
					items = append(items, target)
				}
			}
			related[document.RelPath] = items
		}
	}
	return graph, related, nil
}

func strictBacklinks(index *model.VaultIndex, graph *model.LinkGraph, article *model.Note) []*model.Note {
	if index == nil || graph == nil || article == nil {
		return nil
	}
	paths := graph.Backward[article.RelPath]
	result := make([]*model.Note, 0, len(paths))
	for _, source := range paths {
		if note := index.Notes[source]; note != nil {
			result = append(result, note)
		}
	}
	return result
}

type strictPopoverPayload struct {
	Title   string   `json:"title"`
	Summary string   `json:"summary"`
	Tags    []string `json:"tags"`
}

func writeStrictPopoverPayloads(outputRoot string, index *model.VaultIndex, outputs *strictOutputRegistry) error {
	if index == nil || len(index.Notes) == 0 {
		return nil
	}
	paths := make([]string, 0, len(index.Notes))
	for relPath := range index.Notes {
		paths = append(paths, relPath)
	}
	sort.Strings(paths)
	for _, relPath := range paths {
		note := index.Notes[relPath]
		if note == nil {
			continue
		}
		data, err := json.Marshal(strictPopoverPayload{Title: note.Frontmatter.Title, Tags: append([]string{}, note.Tags...)})
		if err != nil {
			return fmt.Errorf("marshal popover payload %q: %w", relPath, err)
		}
		if err := outputs.write(outputRoot, path.Join("_popover", slug.EncodePath(relPath)+".json"), "popover:"+relPath, data); err != nil {
			return err
		}
	}
	return nil
}

func strictBuildTags(tags map[string]*model.Tag) []*model.Tag {
	values := make([]*model.Tag, 0, len(tags))
	for _, tag := range tags {
		if tag != nil {
			values = append(values, tag)
		}
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Name < values[j].Name })
	return values
}

type strictCacheEntry struct {
	Owner     string `json:"owner"`
	Source    string `json:"source"`
	Route     string `json:"route,omitempty"`
	Signature string `json:"signature"`
}

type strictCacheManifest struct {
	Version int                `json:"version"`
	Entries []strictCacheEntry `json:"entries"`
}

func strictCacheRelativePath(vaultRoot, value string) string {
	if value == "" || !filepath.IsAbs(value) {
		return filepath.ToSlash(value)
	}
	relative, err := filepath.Rel(vaultRoot, value)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return ""
	}
	return filepath.ToSlash(relative)
}

func loadStrictCacheManifest(outputRoot string) *strictCacheManifest {
	if strings.TrimSpace(outputRoot) == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(outputRoot, ".obsite-cache", "manifest.json"))
	if err != nil {
		return nil
	}
	var manifest strictCacheManifest
	if err := json.Unmarshal(data, &manifest); err != nil || manifest.Version != 1 {
		return nil
	}
	return &manifest
}

func writeStrictCacheManifest(outputRoot string, plan *model.SitePlan, index *model.VaultIndex, assets map[string]*model.Asset, outputs *strictOutputRegistry) error {
	manifest := strictCacheManifest{Version: 1, Entries: make([]strictCacheEntry, 0)}
	add := func(owner, source, route string, data []byte) {
		hash := sha256.Sum256(data)
		manifest.Entries = append(manifest.Entries, strictCacheEntry{Owner: owner, Source: source, Route: route, Signature: fmt.Sprintf("%x", hash)})
	}
	if plan != nil {
		cacheConfig := plan.Config
		cacheConfig.CustomCSS = strictCacheRelativePath(plan.VaultPath, cacheConfig.CustomCSS)
		cacheConfig.ThemeDir = strictCacheRelativePath(plan.VaultPath, cacheConfig.ThemeDir)
		configData, _ := json.Marshal(struct {
			Config model.SiteConfig `json:"config"`
		}{Config: cacheConfig})
		add("site", "obsite.yaml", "", configData)
		for _, section := range plan.Sections {
			if section != nil {
				data := append([]byte(section.Title+"\x00"+section.Description+"\x00"+section.Banner+"\x00"+section.BannerAlt), section.RawContent...)
				add("section", section.SourcePath, section.Route, data)
			}
		}
		for _, article := range plan.Articles {
			if article != nil {
				meta, _ := json.Marshal(article.Frontmatter)
				data := append(meta, article.RawContent...)
				add("article", article.RelPath, article.Route, data)
				if article.SocialImage != "" {
					add("social", article.RelPath, article.SocialImage, append(meta, []byte(article.Route+"\x00"+article.SocialImage)...))
				}
			}
		}
	}
	if index != nil {
		for _, tag := range strictBuildTags(index.Tags) {
			if tag != nil {
				data, _ := json.Marshal(tag)
				add("tag", tag.Name, "/"+tag.Slug+"/", data)
			}
		}
	}
	assetSources := make([]string, 0, len(assets))
	for source := range assets {
		assetSources = append(assetSources, source)
	}
	sort.Strings(assetSources)
	for _, source := range assetSources {
		if asset := assets[source]; asset != nil {
			data, _ := json.Marshal(asset)
			add("asset", source, asset.DstPath, data)
		}
	}
	if plan != nil && plan.Config.Timeline.Enabled {
		add("timeline", "obsite.yaml", "/"+slug.EncodePath(strings.Trim(plan.Config.Timeline.Path, "/"))+"/", []byte(plan.Config.Timeline.Path))
	}
	if outputs != nil {
		manifest.Entries = append(manifest.Entries, outputs.records...)
	}
	sort.Slice(manifest.Entries, func(i, j int) bool {
		left, right := manifest.Entries[i], manifest.Entries[j]
		if left.Owner != right.Owner {
			return left.Owner < right.Owner
		}
		if left.Source != right.Source {
			return left.Source < right.Source
		}
		return left.Route < right.Route
	})
	data, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	return outputs.write(outputRoot, ".obsite-cache/manifest.json", "cache", data)
}

func writeStrictConfiguredAssets(vaultRoot, outputRoot string, plan *model.SitePlan, outputs *strictOutputRegistry) error {
	if plan == nil {
		return nil
	}
	if plan.Config.CustomCSS != "" {
		_, data, _, err := internalfsutil.ReadContainedRegularFile(vaultRoot, plan.Config.CustomCSS)
		if err != nil {
			return fmt.Errorf("read custom CSS: %w", err)
		}
		if err := outputs.write(outputRoot, customCSSOutputPath, "custom CSS", data); err != nil {
			return err
		}
	}
	if plan.Config.ThemeDir == "" {
		return nil
	}
	themeCSS := filepath.Join(plan.Config.ThemeDir, "theme.css")
	_, data, _, err := internalfsutil.ReadContainedRegularFile(vaultRoot, themeCSS)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return writeStrictThemeAssets(vaultRoot, outputRoot, plan, outputs)
		}
		return fmt.Errorf("read theme CSS: %w", err)
	}
	plan.Config.ThemeCSS = "assets/theme/theme.css"
	if err := outputs.write(outputRoot, plan.Config.ThemeCSS, "theme CSS", data); err != nil {
		return err
	}
	return writeStrictThemeAssets(vaultRoot, outputRoot, plan, outputs)
}

func writeStrictThemeAssets(vaultRoot, outputRoot string, plan *model.SitePlan, outputs *strictOutputRegistry) error {
	if plan == nil || plan.Config.ThemeDir == "" {
		return nil
	}
	return filepath.WalkDir(plan.Config.ThemeDir, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(plan.Config.ThemeDir, current)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("theme entry %q must not be a symbolic link", rel)
		}
		if entry.IsDir() {
			if rel != "assets" && !strings.HasPrefix(rel, "assets/") {
				return fmt.Errorf("unsupported theme directory %q", rel)
			}
			return nil
		}
		if rel == "theme.css" {
			return nil
		}
		if rel == "slots.html" {
			_, data, _, err := internalfsutil.ReadContainedRegularFile(vaultRoot, current)
			if err != nil {
				return fmt.Errorf("read theme slots: %w", err)
			}
			if err := render.ValidateThemeSlots(string(data)); err != nil {
				return err
			}
			plan.Config.ThemeSlots = string(data)
			return nil
		}
		if !strings.HasPrefix(rel, "assets/") {
			return fmt.Errorf("unsupported theme entry %q", rel)
		}
		_, data, _, err := internalfsutil.ReadContainedRegularFile(vaultRoot, current)
		if err != nil {
			return fmt.Errorf("read theme asset %q: %w", rel, err)
		}
		return outputs.write(outputRoot, path.Join("assets/theme", slug.EncodePath(strings.TrimPrefix(rel, "assets/"))), "theme:"+rel, data)
	})
}

func strictReservedAssetOutputs(plan *model.SitePlan) []string {
	reserved := []string{"style.css", "assets/obsite-runtime", "assets/obsite", "assets/social"}
	if plan != nil && plan.Config.CustomCSS != "" {
		reserved = append(reserved, "assets/custom.css")
	}
	if plan != nil && plan.Config.ThemeDir != "" {
		reserved = append(reserved, "assets/theme")
	}
	return reserved
}

type strictSidebarPayload struct {
	Default  []model.SidebarNode            `json:"default"`
	Versions map[string][]model.SidebarNode `json:"versions,omitempty"`
}

func strictSidebar(plan *model.SitePlan, versionID string) []model.SidebarNode {
	if plan == nil {
		return nil
	}
	root := plan.Root
	if versionID != "" {
		for _, version := range plan.Versions {
			if version != nil && version.ID == versionID {
				root = version.Root
				break
			}
		}
	}
	if root == nil {
		return nil
	}
	return strictSidebarChildren(root)
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
			result = append(result, model.SidebarNode{Name: article.Frontmatter.Title, URL: article.Route, Source: article.RelPath})
		}
	}
	return result
}

func writeStrictMetadataOutputs(outputRoot string, plan *model.SitePlan, index *model.VaultIndex, outputs *strictOutputRegistry) error {
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
	if index != nil {
		for _, tag := range strictBuildTags(index.Tags) {
			if tag != nil {
				_, _ = fmt.Fprintf(&sitemap, `<url><loc>%s</loc></url>`, strictXMLEscape(strictBuildCanonicalURL(plan.Config.BaseURL, "/"+tag.Slug+"/")))
			}
		}
	}
	if plan.Config.Timeline.Enabled {
		_, _ = fmt.Fprintf(&sitemap, `<url><loc>%s</loc></url>`, strictXMLEscape(strictBuildCanonicalURL(plan.Config.BaseURL, "/"+slug.EncodePath(strings.Trim(plan.Config.Timeline.Path, "/"))+"/")))
	}
	sitemap.WriteString(`</urlset>`)
	if err := outputs.write(outputRoot, "sitemap.xml", "sitemap", []byte(sitemap.String())); err != nil {
		return err
	}
	if err := outputs.write(outputRoot, "robots.txt", "robots", []byte(strictBuildRobots(plan.Config.BaseURL))); err != nil {
		return err
	}
	if plan.Config.RSS.Enabled {
		var rss strings.Builder
		_, _ = fmt.Fprintf(&rss, `<?xml version="1.0" encoding="UTF-8"?><rss version="2.0" xmlns:dc="http://purl.org/dc/elements/1.1/"><channel><title>%s</title><link>%s</link>`, strictXMLEscape(plan.Config.Title), strictXMLEscape(strings.TrimSuffix(plan.Config.BaseURL, "/")+"/"))
		if plan.Config.Description != "" {
			_, _ = fmt.Fprintf(&rss, `<description>%s</description>`, strictXMLEscape(plan.Config.Description))
		}
		if plan.Config.Language != "" {
			_, _ = fmt.Fprintf(&rss, `<language>%s</language>`, strictXMLEscape(plan.Config.Language))
		}
		if latest := strictLatestPostTime(plan.Posts); !latest.IsZero() {
			_, _ = fmt.Fprintf(&rss, `<lastBuildDate>%s</lastBuildDate>`, latest.UTC().Format(time.RFC1123Z))
		}
		for _, article := range plan.Posts {
			if article == nil {
				continue
			}
			link := strictBuildCanonicalURL(plan.Config.BaseURL, article.Route)
			_, _ = fmt.Fprintf(&rss, `<item><title>%s</title><link>%s</link><guid>%s</guid>`, strictXMLEscape(article.Frontmatter.Title), strictXMLEscape(link), strictXMLEscape(link))
			if article.Frontmatter.Description != "" {
				_, _ = fmt.Fprintf(&rss, `<description>%s</description>`, strictXMLEscape(article.Frontmatter.Description))
			}
			if article.Frontmatter.Author != "" {
				_, _ = fmt.Fprintf(&rss, `<dc:creator>%s</dc:creator>`, strictXMLEscape(article.Frontmatter.Author))
			}
			for _, tag := range article.Tags {
				_, _ = fmt.Fprintf(&rss, `<category>%s</category>`, strictXMLEscape(tag))
			}
			if !article.Frontmatter.Reviewed.IsZero() {
				_, _ = fmt.Fprintf(&rss, `<reviewed>%s</reviewed>`, strictXMLEscape(article.Frontmatter.Reviewed.UTC().Format(time.RFC3339)))
			}
			if article.Frontmatter.Status != "" {
				_, _ = fmt.Fprintf(&rss, `<status>%s</status>`, strictXMLEscape(article.Frontmatter.Status))
			}
			if article.Frontmatter.Audience != "" {
				_, _ = fmt.Fprintf(&rss, `<audience>%s</audience>`, strictXMLEscape(article.Frontmatter.Audience))
			}
			if article.Frontmatter.ProductVersion != "" {
				_, _ = fmt.Fprintf(&rss, `<productVersion>%s</productVersion>`, strictXMLEscape(article.Frontmatter.ProductVersion))
			}
			if article.Frontmatter.Series != "" {
				_, _ = fmt.Fprintf(&rss, `<series>%s</series>`, strictXMLEscape(article.Frontmatter.Series))
			}
			if !article.Frontmatter.Date.IsZero() {
				_, _ = fmt.Fprintf(&rss, `<pubDate>%s</pubDate>`, article.Frontmatter.Date.UTC().Format(time.RFC1123Z))
			}
			if !article.Frontmatter.Updated.IsZero() {
				_, _ = fmt.Fprintf(&rss, `<lastBuildDate>%s</lastBuildDate>`, article.Frontmatter.Updated.UTC().Format(time.RFC1123Z))
			}
			rss.WriteString(`</item>`)
		}
		rss.WriteString(`</channel></rss>`)
		if err := outputs.write(outputRoot, "index.xml", "rss", []byte(rss.String())); err != nil {
			return err
		}
	}
	notFound, err := render.RenderStrictNotFound(plan)
	if err != nil {
		return err
	}
	return writeStrictHTML(outputs, outputRoot, "404.html", "404", notFound)
}

func strictLatestPostTime(posts []*model.Note) time.Time {
	var latest time.Time
	for _, article := range posts {
		if article == nil {
			continue
		}
		value := article.Frontmatter.Updated
		if value.IsZero() {
			value = article.Frontmatter.Date
		}
		if value.After(latest) {
			latest = value
		}
	}
	return latest
}

func strictBuildRobots(baseURL string) string {
	return "User-agent: *\nAllow: /\nSitemap: " + strings.TrimSuffix(baseURL, "/") + "/sitemap.xml\n"
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
	if plan.Config.DefaultImg != "" && !plan.Config.DefaultImgExternal {
		if err := add(plan.Config.DefaultImg, "defaultImg"); err != nil {
			return nil, err
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

func applyStrictPlannedDestinations(assets map[string]*model.Asset, destinations map[string]string) {
	for source, destination := range destinations {
		if asset := assets[source]; asset != nil {
			asset.DstPath = destination
		}
	}
}

func applyStrictAssetURLs(plan *model.SitePlan, assets map[string]*model.Asset) {
	if plan != nil {
		if plan.Config.DefaultImgExternal {
			plan.Config.DefaultImgURL = plan.Config.DefaultImg
		} else if asset := assets[plan.Config.DefaultImg]; asset != nil {
			plan.Config.DefaultImgURL = asset.DstPath
		}
	}
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
	if !internalasset.IsPublishableAssetPath(raw) || strings.Contains(raw, `\`) || strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || path.Clean(raw) != raw || strings.HasPrefix(path.Clean(raw), "../") || !internalfsutil.IsPortableSitePath(raw) {
		return nil, fmt.Errorf("%s %q must be a normalized vault-relative local asset", kind, raw)
	}
	lower := strings.ToLower(raw)
	allowed := strings.HasSuffix(lower, ".png") || strings.HasSuffix(lower, ".jpg") || strings.HasSuffix(lower, ".jpeg") || strings.HasSuffix(lower, ".webp")
	if kind == "banner" || kind == "defaultImg" {
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
		if err := internalasset.ValidateLocalSVG(data); err != nil {
			return nil, fmt.Errorf("banner %q SVG: %w", raw, err)
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

func rebindStrictPlanNotes(plan *model.SitePlan, index *model.VaultIndex) {
	if plan == nil || index == nil {
		return
	}
	for _, note := range index.Notes {
		if note == nil {
			continue
		}
		original := strictPlanNote(plan, note.RelPath)
		if original == nil {
			continue
		}
		note.Route = original.Route
		note.SectionPath = original.SectionPath
		note.VersionID = original.VersionID
		note.VersionRoutes = original.VersionRoutes
		note.Slug = original.Slug
	}
	index.NoteBySlug = make(map[string]*model.Note, len(index.Notes))
	for _, note := range index.Notes {
		if note != nil && note.Slug != "" {
			index.NoteBySlug[slug.Canonicalize(note.Slug)] = note
		}
	}
	rebind := func(notes []*model.Note) []*model.Note {
		for i, note := range notes {
			if note != nil {
				if replacement := index.Notes[note.RelPath]; replacement != nil {
					notes[i] = replacement
				}
			}
		}
		return notes
	}
	plan.Articles = rebind(plan.Articles)
	plan.Documents = rebind(plan.Documents)
	plan.Posts = rebind(plan.Posts)
	plan.Pages = rebind(plan.Pages)
	for _, section := range plan.Sections {
		if section == nil {
			continue
		}
		section.Articles = rebind(section.Articles)
		section.Documents = rebind(section.Documents)
		section.Posts = rebind(section.Posts)
		section.Pages = rebind(section.Pages)
	}
}

func strictPlanNote(plan *model.SitePlan, relPath string) *model.Note {
	for _, note := range plan.Articles {
		if note != nil && note.RelPath == relPath {
			return note
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
