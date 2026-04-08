package asset

import (
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
	scanInventoryHook  func()
}

// NewCollector creates a collector for render-time asset registrations.
// Register reuses the pass-1 destination plan when available, keeps unique
// render-only assets on plain assets/<basename> paths, and falls back to
// deterministic content-hashed paths for unresolved or colliding registrations.
func NewCollector(vaultRoot string, indexed map[string]*model.Asset) *AssetCollector {
	return NewCollectorWithReservedPaths(vaultRoot, indexed, nil)
}

// NewCollectorWithReservedPaths creates a collector that keeps reserved output paths unavailable to regular assets.
func NewCollectorWithReservedPaths(vaultRoot string, indexed map[string]*model.Asset, reservedOutputPaths []string) *AssetCollector {
	return newCollectorWithReservedPaths(vaultRoot, indexed, reservedOutputPaths, nil)
}

func newCollector(vaultRoot string, indexed map[string]*model.Asset, scanInventoryHook func()) *AssetCollector {
	return newCollectorWithReservedPaths(vaultRoot, indexed, nil, scanInventoryHook)
}

func newCollectorWithReservedPaths(vaultRoot string, indexed map[string]*model.Asset, reservedOutputPaths []string, scanInventoryHook func()) *AssetCollector {
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
	collector.inventoryByGroup = collector.scanVaultInventory()

	return collector
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
	sources := make(map[string]struct{}, 1+len(c.seededByGroup[groupKey])+len(inventory))
	if srcPath != "" {
		sources[srcPath] = struct{}{}
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

func (c *AssetCollector) scanVaultInventory() map[string][]string {
	if c == nil || strings.TrimSpace(c.vaultRoot) == "" {
		return nil
	}
	if c.scanInventoryHook != nil {
		c.scanInventoryHook()
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
		return nil
	}

	for groupKey := range groups {
		sort.Strings(groups[groupKey])
	}

	return groups
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
