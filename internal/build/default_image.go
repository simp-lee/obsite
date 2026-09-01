package build

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	internalfsutil "github.com/simp-lee/obsite/internal/fsutil"
	"github.com/simp-lee/obsite/internal/model"
	internalslug "github.com/simp-lee/obsite/internal/slug"
	"github.com/simp-lee/obsite/internal/vault"
)

func seedDefaultImageCollisionPeers(idx *model.VaultIndex, source string, resourceFiles []string) []string {
	if idx == nil || source == "" {
		return nil
	}
	key := internalslug.Canonicalize(path.Base(source))
	var added []string
	for _, candidate := range resourceFiles {
		candidate = strings.TrimSpace(strings.ReplaceAll(candidate, `\`, "/"))
		if candidate == "" || candidate == source || internalslug.Canonicalize(path.Base(candidate)) != key || idx.Assets[candidate] != nil {
			continue
		}
		idx.Assets[candidate] = &model.Asset{SrcPath: candidate}
		added = append(added, candidate)
	}
	return added
}

func resolveDefaultImageSource(vaultRoot string, cfg model.SiteConfig, scan vault.ScanResult) (string, error) {
	value := strings.TrimSpace(cfg.DefaultImg)
	if value == "" || cfg.DefaultImgExternal {
		return "", nil
	}

	exactCandidate := filepath.Join(vaultRoot, filepath.FromSlash(value))
	if _, _, err := internalfsutil.InspectContainedRegularFile(vaultRoot, exactCandidate); err != nil {
		if errors.Is(err, internalfsutil.ErrSymlinkPath) {
			return "", fmt.Errorf("defaultImg %q must not be a symbolic link", value)
		}
		if errors.Is(err, internalfsutil.ErrUnsupportedRegularFileSource) {
			return "", fmt.Errorf("defaultImg %q must identify a regular file", value)
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("inspect defaultImg %q: %w", value, err)
		}
	}

	lookup := scan.LookupResourcePath(value)
	if len(lookup.Ambiguous) > 0 {
		return "", fmt.Errorf("defaultImg %q is ambiguous; candidates: %s", value, strings.Join(lookup.Ambiguous, ", "))
	}
	if strings.TrimSpace(lookup.Path) == "" {
		return "", fmt.Errorf("defaultImg %q does not exist as a publishable vault resource", value)
	}
	resolved, _, err := internalfsutil.InspectContainedRegularFile(vaultRoot, filepath.Join(vaultRoot, filepath.FromSlash(lookup.Path)))
	if err != nil {
		return "", fmt.Errorf("inspect resolved defaultImg %q: %w", lookup.Path, err)
	}
	relative, err := filepath.Rel(vaultRoot, resolved)
	if err != nil {
		return "", fmt.Errorf("resolve defaultImg %q: %w", value, err)
	}
	return filepath.ToSlash(relative), nil
}
