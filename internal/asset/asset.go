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
	internalfsutil "github.com/simp-lee/obsite/internal/fsutil"
	"github.com/simp-lee/obsite/internal/model"
	internalslug "github.com/simp-lee/obsite/internal/slug"
)

const (
	outputDirPrefix  = "assets"
	minHashSuffixLen = 12
)

var errUnsupportedAssetSource = errors.New("asset source must be a regular non-symlink file inside the vault")

// CopyAssetsWithReservedPaths copies merged assets while preserving reserved output destinations for non-asset writers.
func CopyAssetsWithReservedPaths(vaultRoot string, outputRoot string, assets map[string]*model.Asset, diagCollector *diag.Collector, reservedOutputPaths []string) error {
	if len(assets) == 0 {
		return nil
	}

	reservedOutputKeys := normalizeReservedOutputKeys(reservedOutputPaths)
	assigned := planAssetDestinations(vaultRoot, assets, reservedOutputKeys)
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
		if isReservedOutputKey(outputSiteKey(dstPath), reservedOutputKeys) {
			dstPath = ""
		}
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

		existingHash, destinationExists := written[dstKey]
		hashValue, duplicate, err := copyContainedAsset(vaultRoot, srcPath, outputRoot, dstPath, existingHash, destinationExists)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) || errors.Is(err, errUnsupportedAssetSource) {
				recordUnavailableAsset(diagCollector, srcPath, dstPath)
				continue
			}
			return fmt.Errorf("copy asset %q -> %q: %w", srcPath, dstPath, err)
		}
		if duplicate {
			continue
		}

		written[dstKey] = hashValue
		asset.SrcPath = srcPath
		asset.DstPath = dstPath
	}

	return nil
}

func planAssetDestinations(vaultRoot string, assets map[string]*model.Asset, reservedOutputKeys map[string]struct{}) map[string]string {
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
		reservedPlainPath := isReservedOutputKey(groupKey, reservedOutputKeys)
		plainPreferredSrc := ""
		if !reservedPlainPath {
			plainPreferredSrc = uniqueAvailableSource(vaultRoot, sources)
		}
		hashed := hashCollisionPaths(vaultRoot, groupKey, sources)

		if len(sources) == 1 {
			srcPath := sources[0]
			if srcPath == plainPreferredSrc {
				planned[srcPath] = plainAssetPath(srcPath)
			} else if hasAssignedHashedDestination(assigned[srcPath], groupKey, reservedOutputKeys) {
				planned[srcPath] = hashed[srcPath]
			} else if reservedPlainPath {
				planned[srcPath] = hashed[srcPath]
			} else {
				planned[srcPath] = plainAssetPath(srcPath)
			}
			continue
		}

		for _, srcPath := range sources {
			if srcPath == plainPreferredSrc {
				planned[srcPath] = plainAssetPath(srcPath)
				continue
			}
			planned[srcPath] = hashed[srcPath]
		}
	}

	return planned
}

func hasAssignedHashedDestination(dstPath string, groupKey string, reservedOutputKeys map[string]struct{}) bool {
	dstKey := outputSiteKey(dstPath)
	return dstKey != "" && !isReservedOutputKey(dstKey, reservedOutputKeys) && dstKey != groupKey
}

func uniqueAvailableSource(vaultRoot string, sources []string) string {
	if len(sources) == 0 {
		return ""
	}
	if strings.TrimSpace(vaultRoot) == "" {
		if len(sources) == 1 {
			return sources[0]
		}
		return ""
	}

	availableSource := ""
	for _, srcPath := range sources {
		if _, _, err := assetSourceInfo(vaultRoot, srcPath); err != nil {
			continue
		}
		if availableSource != "" {
			return ""
		}
		availableSource = srcPath
	}

	return availableSource
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
	return path.Join(outputDirPrefix, encodeAssetSegment(path.Base(srcPath)))
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

	return path.Join(outputDirPrefix, encodeAssetSegment(stem)+"."+suffix+ext)
}

func encodeAssetSegment(value string) string {
	const hex = "0123456789ABCDEF"
	var result strings.Builder
	for _, b := range []byte(value) {
		if b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '-' || b == '.' || b == '_' || b == '~' {
			result.WriteByte(b)
		} else {
			result.WriteByte('%')
			result.WriteByte(hex[b>>4])
			result.WriteByte(hex[b&0x0f])
		}
	}
	return result.String()
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

// IsPublishableAssetPath reports whether value is a vault-relative path that the
// asset pipeline accepts as a publishable input.
func IsPublishableAssetPath(value string) bool {
	return normalizePublishableAssetPath(value) != ""
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

	return internalslug.Canonicalize(normalized)
}

func normalizeReservedOutputKeys(reservedOutputPaths []string) map[string]struct{} {
	if len(reservedOutputPaths) == 0 {
		return nil
	}

	reserved := make(map[string]struct{}, len(reservedOutputPaths))
	for _, reservedOutputPath := range reservedOutputPaths {
		if key := outputSiteKey(reservedOutputPath); key != "" {
			reserved[key] = struct{}{}
		}
	}
	if len(reserved) == 0 {
		return nil
	}

	return reserved
}

func isReservedOutputKey(outputKey string, reservedOutputKeys map[string]struct{}) bool {
	if outputKey == "" || len(reservedOutputKeys) == 0 {
		return false
	}

	_, ok := reservedOutputKeys[outputKey]
	return ok
}

func plainAssetKey(srcPath string) string {
	return outputSiteKey(plainAssetPath(srcPath))
}

func assetHash(vaultRoot string, srcPath string) (hashHex string, err error) {
	if vaultRoot == "" {
		return missingAssetHash(srcPath), nil
	}

	_, file, _, err := openAssetSource(vaultRoot, srcPath)
	if err != nil {
		return "", err
	}
	defer func() {
		err = errors.Join(err, file.Close())
	}()
	return fileHashHex(file)
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

	resolvedPath, info, err := internalfsutil.InspectContainedRegularFile(vaultRoot, filepath.FromSlash(srcPath))
	if err != nil {
		if errors.Is(err, internalfsutil.ErrUnsupportedRegularFileSource) ||
			errors.Is(err, internalfsutil.ErrPathOutsideRoot) ||
			errors.Is(err, internalfsutil.ErrSymlinkPath) {
			return "", nil, errUnsupportedAssetSource
		}
		return "", nil, err
	}
	return resolvedPath, info, nil
}

func missingAssetHash(srcPath string) string {
	sum := sha256.Sum256([]byte("missing:" + srcPath))
	return hex.EncodeToString(sum[:])
}

func openAssetSource(vaultRoot string, srcPath string) (string, *os.File, os.FileInfo, error) {
	resolvedPath, file, info, err := internalfsutil.OpenContainedRegularFile(vaultRoot, filepath.FromSlash(srcPath))
	if err != nil {
		if errors.Is(err, internalfsutil.ErrUnsupportedRegularFileSource) ||
			errors.Is(err, internalfsutil.ErrPathOutsideRoot) ||
			errors.Is(err, internalfsutil.ErrSymlinkPath) {
			return "", nil, nil, errUnsupportedAssetSource
		}
		return "", nil, nil, err
	}
	return resolvedPath, file, info, nil
}

func copyContainedAsset(vaultRoot string, srcPath string, outputRoot string, dstPath string, existingHash string, destinationExists bool) (hashHex string, duplicate bool, err error) {
	_, source, info, err := openAssetSource(vaultRoot, srcPath)
	if err != nil {
		return "", false, err
	}
	defer func() {
		err = errors.Join(err, source.Close())
	}()

	hashHex, err = fileHashHex(source)
	if err != nil {
		return "", false, fmt.Errorf("hash source: %w", err)
	}
	if destinationExists {
		if existingHash == hashHex {
			return hashHex, true, nil
		}
		return "", false, fmt.Errorf("asset destination conflict for %q", dstPath)
	}

	dstAbsPath := filepath.Join(outputRoot, filepath.FromSlash(dstPath))
	if err := os.MkdirAll(filepath.Dir(dstAbsPath), 0o755); err != nil {
		return "", false, fmt.Errorf("mkdir destination: %w", err)
	}
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return "", false, fmt.Errorf("rewind source: %w", err)
	}
	if err := copyFile(source, dstAbsPath, info.Mode().Perm()); err != nil {
		return "", false, err
	}
	return hashHex, false, nil
}

func fileHashHex(reader io.Reader) (string, error) {
	hasher := sha256.New()
	if _, err := io.Copy(hasher, reader); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func copyFile(source io.Reader, dstPath string, perm os.FileMode) (err error) {
	if perm == 0 {
		perm = 0o644
	}

	dstFile, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, dstFile.Close())
	}()

	_, err = io.Copy(dstFile, source)
	return err
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
