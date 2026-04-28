package asset

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/simp-lee/obsite/internal/model"
)

// AssetCollector records pass-2 asset registrations and returns the site-relative
// output path that renderers should use in generated HTML.
type AssetCollector struct {
	mu                 sync.Mutex
	vaultRoot          string
	assets             map[string]*model.Asset
	planned            map[string]string
	reservedOutputKeys map[string]struct{}
	seededByGroup      map[string][]string
	inventoryByGroup   map[string][]string
	scanInventoryHook  func() error
}

// NewCollectorWithResourceFiles creates a collector that optionally reuses a
// caller-provided emitted-resource inventory instead of walking the vault.
// Passing nil keeps collision planning scoped to indexed assets plus later
// pass-2 registrations.
func NewCollectorWithResourceFiles(vaultRoot string, indexed map[string]*model.Asset, reservedOutputPaths []string, resourceFiles []string) (*AssetCollector, error) {
	return newCollectorWithReservedPaths(vaultRoot, indexed, reservedOutputPaths, resourceInventoryGroups(resourceFiles), nil)
}

func newCollectorWithReservedPaths(vaultRoot string, indexed map[string]*model.Asset, reservedOutputPaths []string, inventoryByGroup map[string][]string, scanInventoryHook func() error) (*AssetCollector, error) {
	collector := &AssetCollector{
		vaultRoot:          vaultRoot,
		assets:             make(map[string]*model.Asset),
		planned:            make(map[string]string),
		reservedOutputKeys: normalizeReservedOutputKeys(reservedOutputPaths),
		seededByGroup:      make(map[string][]string),
		scanInventoryHook:  scanInventoryHook,
	}

	for srcPath, dstPath := range planAssetDestinations(vaultRoot, indexed, collector.reservedOutputKeys) {
		if srcPath == "" || dstPath == "" {
			continue
		}
		collector.planned[srcPath] = dstPath
		groupKey := plainAssetKey(srcPath)
		if groupKey != "" {
			collector.seededByGroup[groupKey] = append(collector.seededByGroup[groupKey], srcPath)
		}
	}

	for groupKey := range collector.seededByGroup {
		sort.Strings(collector.seededByGroup[groupKey])
	}
	if inventoryByGroup != nil {
		collector.inventoryByGroup = cloneInventoryByGroup(inventoryByGroup)
	} else {
		inventoryByGroup, err := collector.scanVaultInventory()
		if err != nil {
			return nil, fmt.Errorf("scan vault asset inventory: %w", err)
		}
		collector.inventoryByGroup = inventoryByGroup
	}

	return collector, nil
}

// Register records a vault-relative asset reference and returns the site-relative
// output path under assets/.
func (c *AssetCollector) Register(vaultRelPath string) string {
	if c == nil {
		return ""
	}

	srcPath := normalizePublishableAssetPath(vaultRelPath)
	if srcPath == "" {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	asset := c.assets[srcPath]
	if asset == nil {
		asset = &model.Asset{SrcPath: srcPath}
		c.assets[srcPath] = asset
	}
	asset.RefCount++
	if asset.DstPath != "" {
		return asset.DstPath
	}
	asset.DstPath = c.registerSitePathLocked(srcPath)

	return asset.DstPath
}

// Snapshot returns a stable copy of the pass-2 registrations.
func (c *AssetCollector) Snapshot() map[string]*model.Asset {
	if c == nil {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	snapshot := make(map[string]*model.Asset, len(c.assets))
	for srcPath, asset := range c.assets {
		if asset == nil {
			continue
		}
		cloned := *asset
		snapshot[srcPath] = &cloned
	}

	return snapshot
}

// PlanDestinations expands requested sources through the collector's
// inventory-aware basename groups, updates the collector's cached plan, and
// returns destinations for the requested sources.
func (c *AssetCollector) PlanDestinations(assets map[string]*model.Asset) map[string]string {
	if c == nil || len(assets) == 0 {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	expanded := make(map[string]*model.Asset)
	requested := make(map[string]struct{}, len(assets))
	for key, asset := range assets {
		srcPath := normalizeAssetSource(key, asset)
		if srcPath == "" {
			continue
		}

		requested[srcPath] = struct{}{}
		for _, candidate := range c.groupSourcesLocked(plainAssetKey(srcPath), srcPath) {
			existing := expanded[candidate]
			if existing == nil {
				existing = &model.Asset{SrcPath: candidate}
				if dstPath := c.planned[candidate]; dstPath != "" {
					existing.DstPath = dstPath
				}
				expanded[candidate] = existing
			}
		}

		existing := expanded[srcPath]
		if existing == nil {
			existing = &model.Asset{SrcPath: srcPath}
			expanded[srcPath] = existing
		}
		if asset != nil {
			existing.RefCount += asset.RefCount
			if dstPath := outputSitePath(asset.DstPath); dstPath != "" {
				existing.DstPath = dstPath
			}
		}
	}
	if len(expanded) == 0 {
		return nil
	}

	planned := planAssetDestinations(c.vaultRoot, expanded, c.reservedOutputKeys)
	filtered := make(map[string]string, len(requested))
	for srcPath, dstPath := range planned {
		if srcPath == "" || dstPath == "" {
			continue
		}
		c.planned[srcPath] = dstPath
		if asset := c.assets[srcPath]; asset != nil {
			asset.DstPath = dstPath
		}
		if _, ok := requested[srcPath]; ok {
			filtered[srcPath] = dstPath
		}
	}

	return filtered
}

func (c *AssetCollector) registerSitePathLocked(srcPath string) string {
	if asset := c.assets[srcPath]; asset != nil && asset.DstPath != "" {
		return asset.DstPath
	}
	if dstPath := c.planned[srcPath]; dstPath != "" {
		if outputSiteKey(dstPath) == plainAssetKey(srcPath) {
			c.planGroupLocked(srcPath)
			if replanned := c.planned[srcPath]; replanned != "" {
				return replanned
			}
		}
		return dstPath
	}
	if !c.sourceExists(srcPath) {
		dstPath := hashedAssetPath(srcPath, missingAssetHash(srcPath))
		c.planned[srcPath] = dstPath
		return dstPath
	}

	c.planGroupLocked(srcPath)
	if dstPath := c.planned[srcPath]; dstPath != "" {
		return dstPath
	}

	hashValue, err := assetHash(c.vaultRoot, srcPath)
	if err != nil {
		hashValue = missingAssetHash(srcPath)
	}

	dstPath := hashedAssetPath(srcPath, hashValue)
	c.planned[srcPath] = dstPath
	return dstPath
}

func (c *AssetCollector) planGroupLocked(srcPath string) {
	groupKey := plainAssetKey(srcPath)
	if groupKey == "" {
		return
	}

	sources := c.groupSourcesLocked(groupKey, srcPath)
	if len(sources) == 0 {
		return
	}

	assets := make(map[string]*model.Asset, len(sources))
	for _, candidate := range sources {
		asset := &model.Asset{SrcPath: candidate}
		if dstPath := c.planned[candidate]; dstPath != "" {
			asset.DstPath = dstPath
		}
		assets[candidate] = asset
	}

	for candidate, dstPath := range planAssetDestinations(c.vaultRoot, assets, c.reservedOutputKeys) {
		if candidate == "" || dstPath == "" {
			continue
		}
		c.planned[candidate] = dstPath
		if asset := c.assets[candidate]; asset != nil {
			asset.DstPath = dstPath
		}
	}
}

func (c *AssetCollector) groupSourcesLocked(groupKey string, srcPath string) []string {
	inventory := c.inventoryByGroup[groupKey]
	sources := make(map[string]struct{}, 1+len(c.assets)+len(c.seededByGroup[groupKey])+len(inventory))
	if srcPath != "" {
		sources[srcPath] = struct{}{}
	}
	for candidate := range c.assets {
		if plainAssetKey(candidate) == groupKey {
			sources[candidate] = struct{}{}
		}
	}
	for _, candidate := range c.seededByGroup[groupKey] {
		sources[candidate] = struct{}{}
	}
	for _, candidate := range inventory {
		sources[candidate] = struct{}{}
	}

	ordered := make([]string, 0, len(sources))
	for candidate := range sources {
		ordered = append(ordered, candidate)
	}
	sort.Strings(ordered)
	return ordered
}

func (c *AssetCollector) scanVaultInventory() (map[string][]string, error) {
	if c == nil || strings.TrimSpace(c.vaultRoot) == "" {
		return nil, nil
	}
	if c.scanInventoryHook != nil {
		if err := c.scanInventoryHook(); err != nil {
			return nil, err
		}
	}

	groups := make(map[string][]string)
	seen := make(map[string]struct{})
	err := filepath.WalkDir(c.vaultRoot, func(currentPath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		relPath, err := filepath.Rel(c.vaultRoot, currentPath)
		if err != nil {
			return err
		}
		relPath = filepath.ToSlash(relPath)
		if relPath == "." {
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if entry.IsDir() {
			if shouldSkipPublishableAssetPath(relPath) {
				return fs.SkipDir
			}
			return nil
		}
		if shouldSkipPublishableAssetPath(relPath) {
			return nil
		}
		if regular, err := isRegularInventoryEntry(entry); err != nil {
			return err
		} else if !regular {
			return nil
		}

		normalized := normalizePublishableAssetPath(relPath)
		if normalized == "" {
			return nil
		}
		if _, ok := seen[normalized]; ok {
			return nil
		}

		seen[normalized] = struct{}{}
		groupKey := plainAssetKey(normalized)
		if groupKey == "" {
			return nil
		}
		groups[groupKey] = append(groups[groupKey], normalized)
		return nil
	})
	if err != nil {
		return nil, err
	}

	for groupKey := range groups {
		sort.Strings(groups[groupKey])
	}

	return groups, nil
}

func isRegularInventoryEntry(entry fs.DirEntry) (bool, error) {
	if entry == nil {
		return false, nil
	}
	if entry.Type().IsRegular() {
		return true, nil
	}

	info, err := entry.Info()
	if err != nil {
		return false, err
	}

	return info.Mode().IsRegular(), nil
}

func (c *AssetCollector) sourceExists(srcPath string) bool {
	if c == nil || strings.TrimSpace(c.vaultRoot) == "" || srcPath == "" {
		return false
	}

	_, _, err := assetSourceInfo(c.vaultRoot, srcPath)
	return err == nil
}

func resourceInventoryGroups(resourceFiles []string) map[string][]string {
	groups := make(map[string][]string)
	if resourceFiles == nil {
		return groups
	}

	seen := make(map[string]struct{}, len(resourceFiles))
	for _, resourceFile := range resourceFiles {
		normalized := normalizePublishableAssetPath(resourceFile)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}

		groupKey := plainAssetKey(normalized)
		if groupKey == "" {
			continue
		}
		groups[groupKey] = append(groups[groupKey], normalized)
	}

	for groupKey := range groups {
		sort.Strings(groups[groupKey])
	}

	return groups
}

func cloneInventoryByGroup(groups map[string][]string) map[string][]string {
	cloned := make(map[string][]string, len(groups))
	for groupKey, paths := range groups {
		cloned[groupKey] = append([]string(nil), paths...)
	}
	return cloned
}
