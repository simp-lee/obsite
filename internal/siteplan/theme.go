package siteplan

import (
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/simp-lee/obsite/internal/asset"
	"github.com/simp-lee/obsite/internal/diag"
	"github.com/simp-lee/obsite/internal/model"
	"github.com/simp-lee/obsite/internal/render"
)

func validateThemeSlotAssets(plan *model.SitePlan, index *model.VaultIndex, collector *diag.Collector) {
	if strings.TrimSpace(plan.Config.ThemeSlots) == "" {
		return
	}
	check := func(route, title string, note *model.Note, source string) {
		if _, err := render.RenderStrictThemeSlots(plan, route, title, note, source); err != nil {
			record(collector, diag.KindMetadata, filepath.Join(plan.Config.ThemeDir, "slots.html"), "theme slots for %q: %v", route, err)
		}
	}
	for _, section := range plan.Sections {
		if section != nil && section.Route != "" {
			check(section.Route, section.Title, nil, section.SourcePath)
		}
	}
	for _, article := range plan.Articles {
		if article != nil && article.Route != "" {
			check(article.Route, article.Frontmatter.Title, article, article.RelPath)
		}
	}
	if index != nil {
		for _, tag := range sortedStrictTags(index.Tags) {
			check("/"+encodePath(tag.Slug)+"/", "Tag: "+tag.Name, nil, "")
		}
	}
	if plan.Config.Timeline.Enabled {
		for _, route := range timelineRoutes(plan.Config.Timeline.Path, plan.Config.Pagination.PageSize, len(plan.Posts)) {
			check(route, "Recent articles", nil, "")
		}
	}
	check("/404.html", "Not found", nil, "")
}

// Theme inputs are admitted explicitly, not via Markdown's resource discovery.
// Keep their exact physical identities (including NFC/NFD distinctions), and
// allocate their emitted bytes through the shared content-addressed planner.
func planThemeAssets(plan *model.SitePlan, inputs map[string][]byte, assetSources map[string]string, cssSource string, collector *diag.Collector) {
	plan.ThemeAssets = make(map[string]*model.PlannedAsset, len(inputs))
	plan.ThemeAssetURLs = make(map[string]string, len(assetSources))
	plan.Config.ThemeCSS = ""
	cssDirs := make(map[string]string, len(assetSources))
	for name, source := range assetSources {
		cssDirs[source] = path.Dir(name)
	}
	// Previously theme.css and the contents of assets/ shared one directory.
	cssDirs[cssSource] = "."
	visiting := make(map[string]bool)
	var resolve func(string) (*model.PlannedAsset, error)
	resolve = func(source string) (*model.PlannedAsset, error) {
		if planned := plan.ThemeAssets[source]; planned != nil {
			return planned, nil
		}
		if visiting[source] {
			return nil, fmt.Errorf("cyclic theme CSS dependency at %q", source)
		}
		visiting[source] = true
		defer delete(visiting, source)
		data := inputs[source]
		if strings.EqualFold(path.Ext(source), ".css") {
			var err error
			data, err = asset.RewriteCSSURLs(data, func(raw string) (string, error) {
				u, err := url.Parse(raw)
				if err != nil {
					return "", fmt.Errorf("theme CSS URL %q: %w", raw, err)
				}
				if u.IsAbs() || u.Host != "" || strings.HasPrefix(raw, "/") || u.Path == "" {
					return raw, nil
				}
				name := path.Join(cssDirs[source], u.Path)
				target := assetSources[name]
				if target == "" && name == "theme.css" {
					target = cssSource
				}
				if target == "" {
					return "", fmt.Errorf("theme CSS asset %q was not found", raw)
				}
				dependency, err := resolve(target)
				if err != nil {
					return "", err
				}
				suffix := ""
				if i := strings.IndexAny(raw, "?#"); i >= 0 {
					suffix = raw[i:]
				}
				return path.Base(dependency.DstPath) + suffix, nil
			})
			if err != nil {
				return nil, err
			}
		}
		planned := asset.PlanData(source, data)
		plan.ThemeAssets[source] = planned
		return planned, nil
	}
	ordered := make([]string, 0, len(inputs))
	for source := range inputs {
		ordered = append(ordered, source)
	}
	sort.Strings(ordered)
	for _, source := range ordered {
		if _, err := resolve(source); err != nil {
			record(collector, diag.KindMetadata, source, "theme asset: %v", err)
		}
	}
	for name, source := range assetSources {
		if planned := plan.ThemeAssets[source]; planned != nil {
			plan.ThemeAssetURLs[name] = planned.DstPath
		}
	}
	if planned := plan.ThemeAssets[cssSource]; planned != nil {
		plan.Config.ThemeCSS = planned.DstPath
	}
}
