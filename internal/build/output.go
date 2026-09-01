package build

import (
	"fmt"
	"path"
	"sort"
	"strings"

	internalfsutil "github.com/simp-lee/obsite/internal/fsutil"
	"github.com/simp-lee/obsite/internal/model"
	"github.com/simp-lee/obsite/internal/render"
	internalslug "github.com/simp-lee/obsite/internal/slug"
)

type outputOwner string

const (
	outputOwnerMarker     outputOwner = "managed-output marker"
	outputOwnerPage       outputOwner = "page renderer"
	outputOwnerPopover    outputOwner = "popover data"
	outputOwnerStyle      outputOwner = "site stylesheet"
	outputOwnerRuntime    outputOwner = "runtime assets"
	outputOwnerSidebar    outputOwner = "Sidebar data"
	outputOwnerThemeCSS   outputOwner = "theme stylesheet"
	outputOwnerThemeAsset outputOwner = "theme assets"
	outputOwnerCustomCSS  outputOwner = "custom stylesheet"
	outputOwnerVaultAsset outputOwner = "vault assets"
	outputOwnerSitemap    outputOwner = "sitemap"
	outputOwnerRobots     outputOwner = "robots"
	outputOwnerRSS        outputOwner = "RSS"
	outputOwnerCache      outputOwner = "build cache"
)

type outputClaim struct {
	path   string
	owner  outputOwner
	source string
}

type outputDestinationNode struct {
	children map[string]*outputDestinationNode
	file     *outputClaim
	first    *outputClaim
}

type outputDestinationPlan struct {
	root outputDestinationNode
}

func newOutputDestinationPlan() *outputDestinationPlan {
	return &outputDestinationPlan{}
}

func (plan *outputDestinationPlan) claimFile(relPath string, owner outputOwner, source string) error {
	claim, key, err := normalizedOutputClaim(relPath, owner, source)
	if err != nil {
		return err
	}

	node := &plan.root
	lineage := []*outputDestinationNode{node}
	for _, segment := range strings.Split(key, "/") {
		if node.file != nil {
			return outputClaimConflict(*node.file, claim)
		}
		if node.children == nil {
			node.children = make(map[string]*outputDestinationNode)
		}
		next := node.children[segment]
		if next == nil {
			next = &outputDestinationNode{}
			node.children[segment] = next
		}
		node = next
		lineage = append(lineage, node)
	}

	if node.file != nil {
		if node.file.owner == claim.owner && node.file.path == claim.path && node.file.source == claim.source {
			return nil
		}
		return outputClaimConflict(*node.file, claim)
	}
	if node.first != nil {
		return outputClaimConflict(*node.first, claim)
	}

	stored := claim
	node.file = &stored
	for _, ancestor := range lineage {
		if ancestor.first == nil {
			ancestor.first = &stored
		}
	}
	return nil
}

func normalizedOutputClaim(relPath string, owner outputOwner, source string) (outputClaim, string, error) {
	trimmed := strings.TrimSpace(strings.ReplaceAll(relPath, `\`, "/"))
	if trimmed == "" || strings.HasPrefix(trimmed, "/") {
		return outputClaim{}, "", fmt.Errorf("invalid output destination %q for %s", relPath, owner)
	}
	cleaned := path.Clean(trimmed)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || !internalfsutil.IsPortableSitePath(cleaned) {
		return outputClaim{}, "", fmt.Errorf("invalid output destination %q for %s", relPath, owner)
	}
	key := internalslug.Canonicalize(cleaned)
	if key == "" {
		return outputClaim{}, "", fmt.Errorf("invalid output destination %q for %s", relPath, owner)
	}
	return outputClaim{path: cleaned, owner: owner, source: strings.TrimSpace(source)}, key, nil
}

func outputClaimConflict(existing outputClaim, next outputClaim) error {
	return fmt.Errorf(
		"output destination conflict: %s %q (%s) overlaps %s %q (%s)",
		existing.owner,
		existing.path,
		existing.source,
		next.owner,
		next.path,
		next.source,
	)
}

func validateOutputDestinations(cfg model.SiteConfig, idx *model.VaultIndex, folders []folderPageSpec, assets map[string]*model.Asset, theme themeInputs, writeStyleCSS bool, writeCache bool) error {
	plan := newOutputDestinationPlan()
	if err := plan.claimFile(managedOutputMarkerFilename, outputOwnerMarker, "output manager"); err != nil {
		return err
	}

	for _, route := range buildGeneratedPageRoutes(cfg, idx, folders) {
		if err := plan.claimFile(route.relPath, outputOwnerPage, route.source); err != nil {
			return err
		}
	}

	if cfg.Popover.Enabled {
		for _, note := range allPublicNotes(idx) {
			if note == nil {
				continue
			}
			slug := cleanSitePath(note.Slug)
			if slug == "" {
				continue
			}
			if err := plan.claimFile(path.Join("_popover", slug+".json"), outputOwnerPopover, note.RelPath); err != nil {
				return err
			}
		}
	}

	if writeStyleCSS {
		if err := plan.claimFile("style.css", outputOwnerStyle, "structural/theme stylesheet"); err != nil {
			return err
		}
	}
	for _, relPath := range render.RuntimeAssetOutputPaths() {
		if err := plan.claimFile(relPath, outputOwnerRuntime, relPath); err != nil {
			return err
		}
	}
	if cfg.Sidebar.Enabled {
		if err := plan.claimFile(sidebarJSONOutputPath, outputOwnerSidebar, "public navigation tree"); err != nil {
			return err
		}
	}
	if theme.stylesheet != "" {
		if err := plan.claimFile(themeCSSOutputPath, outputOwnerThemeCSS, theme.stylesheet); err != nil {
			return err
		}
	}
	for _, asset := range theme.assets {
		if err := plan.claimFile(asset.outputPath, outputOwnerThemeAsset, asset.sourcePath); err != nil {
			return err
		}
	}
	if strings.TrimSpace(cfg.CustomCSS) != "" {
		if err := plan.claimFile(customCSSOutputPath, outputOwnerCustomCSS, cfg.CustomCSS); err != nil {
			return err
		}
	}

	assetSources := make([]string, 0, len(assets))
	for source := range assets {
		assetSources = append(assetSources, source)
	}
	sort.Strings(assetSources)
	for _, source := range assetSources {
		asset := assets[source]
		if asset == nil || strings.TrimSpace(asset.DstPath) == "" {
			continue
		}
		if err := plan.claimFile(asset.DstPath, outputOwnerVaultAsset, source); err != nil {
			return err
		}
	}

	for _, fixed := range []struct {
		path   string
		owner  outputOwner
		source string
	}{
		{path: "sitemap.xml", owner: outputOwnerSitemap, source: "site routes"},
		{path: "robots.txt", owner: outputOwnerRobots, source: "site base URL"},
	} {
		if err := plan.claimFile(fixed.path, fixed.owner, fixed.source); err != nil {
			return err
		}
	}
	if cfg.RSS.Enabled {
		if err := plan.claimFile("index.xml", outputOwnerRSS, "recent notes"); err != nil {
			return err
		}
	}
	if writeCache {
		if err := plan.claimFile(cacheManifestRelPath, outputOwnerCache, "incremental manifest"); err != nil {
			return err
		}
	}
	return nil
}
