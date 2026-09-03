package vault

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	internalfsutil "github.com/simp-lee/obsite/internal/fsutil"
	"github.com/simp-lee/obsite/internal/model"
)

const (
	obsidianConfigDir  = ".obsidian"
	obsidianAppJSON    = ".obsidian/app.json"
	nodeModulesDirName = "node_modules"
	obsiteDirName      = ".obsite"
)

// ScanResult is the Step 11 handoff for later frontmatter parsing and index building.
// It captures the candidate Markdown files, candidate resources, and the normalized
// Obsidian attachment folder setting when present.
type ScanResult struct {
	VaultPath            string
	AttachmentFolderPath string
	MarkdownFiles        []string
	ResourceFiles        []string

	markdownSet       map[string]struct{}
	resourceSet       map[string]string
	resourceLookup    map[string]string
	resourceConflicts map[string][]string
}

// ScanOptions carries the already-resolved output exclusion shared with build and watch.
type ScanOptions struct {
	OutputPath string
}

// Scan walks a vault once without an output exclusion.
func Scan(vaultPath string) (ScanResult, error) {
	return ScanWithOptions(vaultPath, ScanOptions{})
}

// ScanWithOptions walks a vault once and returns the Markdown and resource
// candidates needed by later phases. Hidden entries, node_modules, the resolved
// output, all .obsidian content except the separately-read app.json, symlinks,
// and non-regular files are excluded. attachmentFolderPath is preserved as
// normalized metadata only and does not relax scan boundaries.
func ScanWithOptions(vaultPath string, options ScanOptions) (ScanResult, error) {
	absVaultPath, err := normalizeVaultPath(vaultPath)
	if err != nil {
		return ScanResult{}, err
	}

	excludedOutput := filepath.Clean(strings.TrimSpace(options.OutputPath))
	if excludedOutput != "." && !internalfsutil.PathWithinRoot(absVaultPath, excludedOutput) {
		excludedOutput = ""
	}

	attachmentFolderPath, err := readAttachmentFolderPath(absVaultPath)
	if err != nil {
		return ScanResult{}, err
	}

	result := ScanResult{
		VaultPath:            absVaultPath,
		AttachmentFolderPath: attachmentFolderPath,
		markdownSet:          make(map[string]struct{}),
	}

	err = filepath.WalkDir(absVaultPath, func(currentPath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		relPath, err := filepath.Rel(absVaultPath, currentPath)
		if err != nil {
			return fmt.Errorf("compute relative path for %q: %w", currentPath, err)
		}
		relPath = filepath.ToSlash(relPath)
		if relPath == "." {
			return nil
		}
		if excludedOutput != "" && internalfsutil.SamePath(currentPath, excludedOutput) {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		isSymlink, err := isSymlinkEntry(entry)
		if err != nil {
			return fmt.Errorf("inspect %q: %w", currentPath, err)
		}
		if isSymlink {
			return nil
		}

		name := entry.Name()
		if entry.IsDir() {
			if shouldSkipPath(relPath) {
				return fs.SkipDir
			}
			return nil
		}

		if shouldSkipPath(relPath) {
			return nil
		}

		isRegular, err := isRegularFileEntry(entry)
		if err != nil {
			return fmt.Errorf("inspect %q: %w", currentPath, err)
		}
		if !isRegular {
			return nil
		}

		if isMarkdownFile(name) {
			result.markdownSet[relPath] = struct{}{}
			result.MarkdownFiles = append(result.MarkdownFiles, relPath)
			return nil
		}

		result.ResourceFiles = append(result.ResourceFiles, relPath)
		return nil
	})
	if err != nil {
		return ScanResult{}, fmt.Errorf("scan vault %q: %w", absVaultPath, err)
	}

	sort.Strings(result.MarkdownFiles)
	sort.Strings(result.ResourceFiles)
	result.resourceSet = model.BuildExactLookupPaths(result.ResourceFiles)
	result.resourceLookup, result.resourceConflicts = model.BuildCanonicalLookupPaths(result.ResourceFiles)

	return result, nil
}

// LookupResourcePath returns the scanned vault-relative resource path that
// matches relPath after exact and canonical Unicode lookup.
func (r ScanResult) LookupResourcePath(relPath string) model.PathLookupResult {
	if exactKey := normalizeLookupPath(relPath); exactKey != "" {
		if resolved := r.resourceSet[exactKey]; resolved != "" {
			return model.PathLookupResult{Path: resolved}
		}
	}

	canonicalKey := model.CanonicalResourceLookupPath(relPath)
	if canonicalKey == "" {
		return model.PathLookupResult{}
	}
	if ambiguous := r.resourceConflicts[canonicalKey]; len(ambiguous) > 0 {
		return model.PathLookupResult{Ambiguous: append([]string(nil), ambiguous...)}
	}

	return model.PathLookupResult{Path: r.resourceLookup[canonicalKey]}
}

func normalizeVaultPath(vaultPath string) (string, error) {
	return internalfsutil.ResolveVaultPath(vaultPath)
}

func readAttachmentFolderPath(vaultPath string) (string, error) {
	configDirPath := filepath.Join(vaultPath, obsidianConfigDir)
	if _, _, err := internalfsutil.InspectContainedDirectory(vaultPath, configDirPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		if errors.Is(err, internalfsutil.ErrSymlinkPath) {
			return "", fmt.Errorf("obsidian config path %q must not be a symbolic link", configDirPath)
		}
		if errors.Is(err, internalfsutil.ErrUnsupportedRegularFileSource) {
			return "", nil
		}
		return "", err
	}

	appConfigPath := filepath.Join(vaultPath, filepath.FromSlash(obsidianAppJSON))
	_, data, _, err := internalfsutil.ReadContainedRegularFile(vaultPath, appConfigPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		if errors.Is(err, internalfsutil.ErrSymlinkPath) {
			return "", fmt.Errorf("obsidian config path %q must not be a symbolic link", appConfigPath)
		}
		if errors.Is(err, internalfsutil.ErrUnsupportedRegularFileSource) {
			return "", fmt.Errorf("obsidian config path %q must be a regular file", appConfigPath)
		}
		return "", fmt.Errorf("read %q: %w", appConfigPath, err)
	}

	var config struct {
		AttachmentFolderPath string `json:"attachmentFolderPath"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return "", fmt.Errorf("parse %q: %w", appConfigPath, err)
	}

	normalizedPath, err := normalizeAttachmentFolderPath(config.AttachmentFolderPath)
	if err != nil {
		return "", fmt.Errorf("normalize attachmentFolderPath from %q: %w", appConfigPath, err)
	}
	return normalizedPath, nil
}

func normalizeAttachmentFolderPath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}

	normalized := strings.ReplaceAll(raw, `\`, "/")
	if hasWindowsDriveAbsolutePath(normalized) || hasDoubleSlashAbsolutePath(normalized) {
		return "", fmt.Errorf("attachmentFolderPath must stay inside the vault: %q", raw)
	}

	cleaned := path.Clean(normalized)
	if cleaned == "." {
		return ".", nil
	}

	cleaned = strings.TrimPrefix(cleaned, "/")
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("attachmentFolderPath must stay inside the vault: %q", raw)
	}

	return cleaned, nil
}

func hasWindowsDriveAbsolutePath(cleaned string) bool {
	if len(cleaned) < 2 {
		return false
	}
	if !isASCIILetter(cleaned[0]) || cleaned[1] != ':' {
		return false
	}
	return len(cleaned) == 2 || cleaned[2] == '/'
}

func hasDoubleSlashAbsolutePath(cleaned string) bool {
	return strings.HasPrefix(cleaned, "//")
}

func isASCIILetter(value byte) bool {
	return (value >= 'a' && value <= 'z') || (value >= 'A' && value <= 'Z')
}

func shouldSkipPath(relPath string) bool {
	normalizedRelPath := normalizeLookupPath(relPath)
	if normalizedRelPath == "" || normalizedRelPath == "." {
		return false
	}
	return hasSkippedPathSegment(normalizedRelPath)
}

func isSymlinkEntry(entry fs.DirEntry) (bool, error) {
	if entry.Type()&fs.ModeSymlink != 0 {
		return true, nil
	}
	if entry.Type().IsRegular() || entry.IsDir() {
		return false, nil
	}

	info, err := entry.Info()
	if err != nil {
		return false, err
	}
	return info.Mode()&fs.ModeSymlink != 0, nil
}

func isRegularFileEntry(entry fs.DirEntry) (bool, error) {
	if entry.Type().IsRegular() {
		return true, nil
	}
	if entry.Type()&fs.ModeSymlink != 0 || entry.IsDir() {
		return false, nil
	}

	info, err := entry.Info()
	if err != nil {
		return false, err
	}
	return info.Mode().IsRegular(), nil
}

func hasSkippedPathSegment(relPath string) bool {
	for _, segment := range strings.Split(relPath, "/") {
		if shouldSkipPathSegment(segment) {
			return true
		}
	}
	return false
}

func shouldSkipPathSegment(name string) bool {
	return name == obsidianConfigDir || name == nodeModulesDirName || name == obsiteDirName
}

func isMarkdownFile(name string) bool {
	return strings.EqualFold(filepath.Ext(name), ".md")
}

func normalizeLookupPath(relPath string) string {
	trimmed := strings.TrimSpace(relPath)
	if trimmed == "" {
		return ""
	}

	normalized := strings.ReplaceAll(trimmed, `\`, "/")
	normalized = strings.TrimPrefix(normalized, "/")
	if normalized == "" {
		return ""
	}

	normalized = path.Clean(normalized)
	if normalized == "." || normalized == ".." || strings.HasPrefix(normalized, "../") {
		return ""
	}

	return normalized
}
