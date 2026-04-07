package asset

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/simp-lee/obsite/internal/diag"
	"github.com/simp-lee/obsite/internal/model"
)

const (
	outputDirPrefix  = "assets"
	minHashSuffixLen = 12
)

var errUnsupportedAssetSource = errors.New("asset source must be a regular non-symlink file inside the vault")

// MergeAssets combines pass-1 indexed assets with pass-2 collector assets.
// Assets are deduped by vault-relative source path and their reference counts
// are added together.
func MergeAssets(vaultRoot string, indexed map[string]*model.Asset, collector *AssetCollector) map[string]*model.Asset {
	merged := make(map[string]*model.Asset, len(indexed))
	mergeAssetMap(merged, indexed)
	if collector != nil {
		mergeAssetMap(merged, collector.Snapshot())
	}

	assigned := planAssetDestinations(vaultRoot, merged)
	for srcPath, asset := range merged {
		if asset == nil {
			continue
		}
		if dstPath := assigned[srcPath]; dstPath != "" {
			asset.DstPath = dstPath
		}
	}

	return merged
}

// CopyAssets copies merged assets from the vault into the output directory.
// Missing or unsupported asset sources are downgraded to unresolved-asset diagnostics so the build can continue.
func CopyAssets(vaultRoot string, outputRoot string, assets map[string]*model.Asset, diagCollector *diag.Collector) error {
	if len(assets) == 0 {
		return nil
	}

	assigned := planAssetDestinations(vaultRoot, assets)
	lookup := make(map[string]*model.Asset, len(assets))
	ordered := make([]string, 0, len(assets))
	for key, asset := range assets {
		srcPath := normalizeAssetSource(key, asset)
		if srcPath == "" {
			continue
		}
		lookup[srcPath] = asset
		ordered = append(ordered, srcPath)
	}
	sort.Strings(ordered)

	written := make(map[string]string, len(ordered))
	for _, srcPath := range ordered {
		asset := lookup[srcPath]
		if asset == nil {
			continue
		}

		dstPath := outputSitePath(asset.DstPath)
		if dstPath == "" {
			dstPath = assigned[srcPath]
			asset.DstPath = dstPath
		}
		if dstPath == "" {
			continue
		}
		dstKey := outputSiteKey(dstPath)
		if dstKey == "" {
			continue
		}

		srcAbsPath, info, err := assetSourceInfo(vaultRoot, srcPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) || errors.Is(err, errUnsupportedAssetSource) {
				recordUnavailableAsset(diagCollector, srcPath, dstPath)
				continue
			}
			return fmt.Errorf("inspect asset %q: %w", srcPath, err)
		}

		hashValue, err := fileHashHex(srcAbsPath)
		if err != nil {
			return fmt.Errorf("hash asset %q: %w", srcPath, err)
		}
		if existingHash, ok := written[dstKey]; ok {
			if existingHash == hashValue {
				continue
			}
			return fmt.Errorf("asset destination conflict for %q", dstPath)
		}

		dstAbsPath := filepath.Join(outputRoot, filepath.FromSlash(dstPath))
		if err := os.MkdirAll(filepath.Dir(dstAbsPath), 0o755); err != nil {
			return fmt.Errorf("mkdir asset destination for %q: %w", dstPath, err)
		}
		if err := copyFile(srcAbsPath, dstAbsPath, info.Mode().Perm()); err != nil {
			return fmt.Errorf("copy asset %q -> %q: %w", srcPath, dstPath, err)
		}

		written[dstKey] = hashValue
		asset.SrcPath = srcPath
		asset.DstPath = dstPath
	}

	return nil
}

func mergeAssetMap(dst map[string]*model.Asset, src map[string]*model.Asset) {
	if len(src) == 0 {
		return
	}

	for key, asset := range src {
		srcPath := normalizeAssetSource(key, asset)
		if srcPath == "" {
			continue
		}

		existing := dst[srcPath]
		if existing == nil {
			existing = &model.Asset{SrcPath: srcPath}
			dst[srcPath] = existing
		}

		if asset != nil {
			existing.RefCount += asset.RefCount
			if dstPath := outputSitePath(asset.DstPath); dstPath != "" {
				existing.DstPath = dstPath
			}
		}
	}
}

func planAssetDestinations(vaultRoot string, assets map[string]*model.Asset) map[string]string {
	assigned := make(map[string]string, len(assets))
	grouped := make(map[string][]string)

	for key, asset := range assets {
		srcPath := normalizeAssetSource(key, asset)
		if srcPath == "" {
			continue
		}
		groupKey := plainAssetKey(srcPath)
		grouped[groupKey] = append(grouped[groupKey], srcPath)
		if asset != nil {
			if dstPath := outputSitePath(asset.DstPath); dstPath != "" {
				assigned[srcPath] = dstPath
			}
		}
	}

	planned := make(map[string]string, len(assets))
	groupKeys := make([]string, 0, len(grouped))
	for groupKey := range grouped {
		groupKeys = append(groupKeys, groupKey)
	}
	sort.Strings(groupKeys)

	for _, groupKey := range groupKeys {
		sources := grouped[groupKey]
		sort.Strings(sources)

		if len(sources) == 1 {
			srcPath := sources[0]
			if dstPath := assigned[srcPath]; dstPath != "" {
				planned[srcPath] = dstPath
			} else {
				planned[srcPath] = plainAssetPath(srcPath)
			}
			continue
		}

		hasMultipleAvailableSources := groupHasMultipleAvailableSources(vaultRoot, sources)
		plainAssignedSrc := ""
		assignedCounts := make(map[string]int, len(sources))
		for _, srcPath := range sources {
			if dstPath := assigned[srcPath]; dstPath != "" {
				assignedCounts[outputSiteKey(dstPath)]++
			}
			if !hasMultipleAvailableSources && assigned[srcPath] == plainAssetPath(srcPath) {
				plainAssignedSrc = srcPath
				break
			}
		}

		hashed := hashCollisionPaths(vaultRoot, groupKey, sources)

		for _, srcPath := range sources {
			if dstPath := assigned[srcPath]; dstPath != "" {
				dstKey := outputSiteKey(dstPath)
				switch {
				case !hasMultipleAvailableSources && plainAssignedSrc != "" && srcPath == plainAssignedSrc:
					planned[srcPath] = dstPath
					continue
				case dstKey != "" && dstKey != groupKey && assignedCounts[dstKey] == 1:
					planned[srcPath] = dstPath
					continue
				}
			}
			planned[srcPath] = hashed[srcPath]
		}

		byKey := make(map[string][]string, len(sources))
		for _, srcPath := range sources {
			dstKey := outputSiteKey(planned[srcPath])
			if dstKey == "" {
				continue
			}
			byKey[dstKey] = append(byKey[dstKey], srcPath)
		}
		for _, members := range byKey {
			if len(members) <= 1 {
				continue
			}

			canonicalPath := planned[members[0]]
			identical := true
			for _, srcPath := range members[1:] {
				if planned[srcPath] != canonicalPath {
					identical = false
					break
				}
			}
			if identical {
				continue
			}

			for _, srcPath := range members {
				planned[srcPath] = hashed[srcPath]
			}
		}
	}

	return planned
}

func groupHasMultipleAvailableSources(vaultRoot string, sources []string) bool {
	if len(sources) <= 1 {
		return false
	}
	if strings.TrimSpace(vaultRoot) == "" {
		return true
	}

	available := 0
	for _, srcPath := range sources {
		if _, _, err := assetSourceInfo(vaultRoot, srcPath); err != nil {
			continue
		}
		available++
		if available > 1 {
			return true
		}
	}

	return false
}

func hashCollisionPaths(vaultRoot string, groupKey string, sources []string) map[string]string {
	hashes := make(map[string]string, len(sources))
	for _, srcPath := range sources {
		hashValue, err := assetHash(vaultRoot, srcPath)
		if err != nil {
			hashValue = missingAssetHash(srcPath)
		}
		hashes[srcPath] = hashValue
	}

	prefixLen := minimumUniqueHashPrefix(hashes)
	planned := make(map[string]string, len(sources))
	baseName := path.Base(groupKey)
	for _, srcPath := range sources {
		planned[srcPath] = hashedAssetPathForBase(baseName, hashes[srcPath][:prefixLen])
	}

	return planned
}

func minimumUniqueHashPrefix(hashes map[string]string) int {
	distinct := make([]string, 0, len(hashes))
	seenDistinct := make(map[string]struct{}, len(hashes))
	for _, hashValue := range hashes {
		if _, ok := seenDistinct[hashValue]; ok {
			continue
		}
		seenDistinct[hashValue] = struct{}{}
		distinct = append(distinct, hashValue)
	}
	if len(distinct) <= 1 {
		return minHashSuffixLen
	}

	for length := minHashSuffixLen; length <= len(distinct[0]); length++ {
		prefixes := make(map[string]struct{}, len(distinct))
		unique := true
		for _, hashValue := range distinct {
			prefix := hashValue[:length]
			if _, ok := prefixes[prefix]; ok {
				unique = false
				break
			}
			prefixes[prefix] = struct{}{}
		}
		if unique {
			return length
		}
	}

	return len(distinct[0])
}

func plainAssetPath(srcPath string) string {
	return path.Join(outputDirPrefix, path.Base(srcPath))
}

func hashedAssetPath(srcPath string, suffix string) string {
	return hashedAssetPathForBase(path.Base(srcPath), suffix)
}

func hashedAssetPathForBase(baseName string, suffix string) string {
	baseName = strings.ToLower(path.Base(strings.TrimSpace(strings.ReplaceAll(baseName, "\\", "/"))))
	if baseName == "" || baseName == "." || baseName == "/" {
		baseName = "asset"
	}
	ext := path.Ext(baseName)
	stem := strings.TrimSuffix(baseName, ext)
	if stem == "" {
		stem = baseName
	}

	return path.Join(outputDirPrefix, stem+"."+suffix+ext)
}

func normalizeAssetSource(key string, asset *model.Asset) string {
	if asset != nil && strings.TrimSpace(asset.SrcPath) != "" {
		if srcPath := normalizePublishableAssetPath(asset.SrcPath); srcPath != "" {
			return srcPath
		}
	}

	return normalizePublishableAssetPath(key)
}

func normalizeAssetPath(value string) string {
	if !isVaultRelativeAssetInput(value) {
		return ""
	}

	normalized := normalizePath(value)
	if normalized == "" || isOutsideVaultPath(normalized) {
		return ""
	}

	return normalized
}

func normalizePublishableAssetPath(value string) string {
	normalized := normalizeAssetPath(value)
	if normalized == "" || shouldSkipPublishableAssetPath(normalized) {
		return ""
	}

	return normalized
}

func outputSitePath(value string) string {
	normalized := normalizePath(value)
	if normalized == "" || isOutsideVaultPath(normalized) {
		return ""
	}
	if normalized == outputDirPrefix || !strings.HasPrefix(normalized, outputDirPrefix+"/") {
		return ""
	}

	return normalized
}

func outputSiteKey(value string) string {
	normalized := outputSitePath(value)
	if normalized == "" {
		return ""
	}

	return strings.ToLower(normalized)
}

func plainAssetKey(srcPath string) string {
	return outputSiteKey(plainAssetPath(srcPath))
}

func assetHash(vaultRoot string, srcPath string) (string, error) {
	if vaultRoot == "" {
		return missingAssetHash(srcPath), nil
	}

	srcAbsPath, _, err := assetSourceInfo(vaultRoot, srcPath)
	if err != nil {
		return "", err
	}

	return fileHashHex(srcAbsPath)
}

func isVaultRelativeAssetInput(value string) bool {
	cleaned := strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if cleaned == "" {
		return false
	}
	if shouldKeepDestination(cleaned) {
		return false
	}
	if strings.HasPrefix(cleaned, "//") {
		return false
	}
	if len(cleaned) >= 2 && isASCIIAlpha(cleaned[0]) && cleaned[1] == ':' {
		return false
	}

	return true
}

func isASCIIAlpha(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}

func shouldSkipPublishableAssetPath(relPath string) bool {
	normalized := normalizePath(relPath)
	if normalized == "" || normalized == "." {
		return false
	}

	for _, segment := range strings.Split(normalized, "/") {
		switch {
		case segment == "":
			continue
		case segment == ".obsidian", segment == "node_modules":
			return true
		case strings.HasPrefix(segment, "."):
			return true
		}
	}

	return false
}

func assetSourceInfo(vaultRoot string, srcPath string) (string, os.FileInfo, error) {
	vaultRoot = strings.TrimSpace(vaultRoot)
	if vaultRoot == "" || srcPath == "" {
		return "", nil, os.ErrNotExist
	}

	rootAbs, err := filepath.Abs(vaultRoot)
	if err != nil {
		return "", nil, err
	}

	currentPath := rootAbs
	parts := strings.Split(filepath.FromSlash(srcPath), string(os.PathSeparator))
	for index, part := range parts {
		if part == "" || part == "." {
			continue
		}

		currentPath = filepath.Join(currentPath, part)
		info, err := os.Lstat(currentPath)
		if err != nil {
			return "", nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", nil, errUnsupportedAssetSource
		}

		isLast := index == len(parts)-1
		if !isLast {
			if !info.IsDir() {
				return "", nil, errUnsupportedAssetSource
			}
			continue
		}

		if info.IsDir() || !info.Mode().IsRegular() {
			return "", nil, errUnsupportedAssetSource
		}

		return currentPath, info, nil
	}

	return "", nil, errUnsupportedAssetSource
}

func missingAssetHash(srcPath string) string {
	sum := sha256.Sum256([]byte("missing:" + srcPath))
	return hex.EncodeToString(sum[:])
}

func fileHashHex(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func copyFile(srcPath string, dstPath string, perm os.FileMode) error {
	if perm == 0 {
		perm = 0o644
	}

	srcFile, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return err
	}

	return nil
}

func recordUnavailableAsset(diagCollector *diag.Collector, srcPath string, dstPath string) {
	if diagCollector == nil {
		return
	}

	diagCollector.Warningf(
		diag.KindUnresolvedAsset,
		diag.Location{Path: srcPath},
		"asset %q could not be copied to %q because the source file is missing or not a regular vault file",
		srcPath,
		dstPath,
	)
}
