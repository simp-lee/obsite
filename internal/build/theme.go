package build

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	internalconfig "github.com/simp-lee/obsite/internal/config"
	internalfsutil "github.com/simp-lee/obsite/internal/fsutil"
)

const themeCSSOutputPath = "assets/theme/theme.css"

type themeAssetInput struct {
	sourcePath string
	outputPath string
}

type themeInputs struct {
	directory  string
	stylesheet string
	slots      string
	assets     []themeAssetInput
}

func resolveThemeInputs(vaultRoot string, configuredDir string) (themeInputs, error) {
	fixedDir := filepath.Join(vaultRoot, filepath.FromSlash(internalconfig.ThemeDirRelPath))
	configuredDir = strings.TrimSpace(configuredDir)
	if configuredDir != "" {
		configuredAbs := configuredDir
		if !filepath.IsAbs(configuredAbs) {
			configuredAbs = filepath.Join(vaultRoot, configuredAbs)
		}
		configuredAbs, err := filepath.Abs(configuredAbs)
		if err != nil {
			return themeInputs{}, fmt.Errorf("resolve theme directory %q: %w", configuredDir, err)
		}
		if filepath.Clean(configuredAbs) != filepath.Clean(fixedDir) {
			return themeInputs{}, fmt.Errorf("theme directory %q must be the fixed vault input %q", configuredDir, fixedDir)
		}
	}

	resolvedDir, _, err := internalfsutil.InspectContainedDirectory(vaultRoot, fixedDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if configuredDir != "" {
				return themeInputs{}, fmt.Errorf("theme directory %q does not exist", fixedDir)
			}
			return themeInputs{}, nil
		}
		if errors.Is(err, internalfsutil.ErrSymlinkPath) || errors.Is(err, internalfsutil.ErrUnsupportedRegularFileSource) {
			return themeInputs{}, fmt.Errorf("theme directory %q must be a non-symlink directory inside the vault", fixedDir)
		}
		return themeInputs{}, fmt.Errorf("inspect theme directory %q: %w", fixedDir, err)
	}

	inputs := themeInputs{directory: resolvedDir}
	entries, err := os.ReadDir(resolvedDir)
	if err != nil {
		return themeInputs{}, fmt.Errorf("read theme directory %q: %w", resolvedDir, err)
	}
	for _, entry := range entries {
		candidate := filepath.Join(resolvedDir, entry.Name())
		switch entry.Name() {
		case "theme.css":
			resolved, _, inspectErr := internalfsutil.InspectContainedRegularFile(vaultRoot, candidate)
			if inspectErr != nil {
				return themeInputs{}, invalidThemeEntry(candidate, inspectErr)
			}
			inputs.stylesheet = resolved
		case "slots.html":
			resolved, _, inspectErr := internalfsutil.InspectContainedRegularFile(vaultRoot, candidate)
			if inspectErr != nil {
				return themeInputs{}, invalidThemeEntry(candidate, inspectErr)
			}
			inputs.slots = resolved
		case "assets":
			resolved, _, inspectErr := internalfsutil.InspectContainedDirectory(vaultRoot, candidate)
			if inspectErr != nil {
				return themeInputs{}, invalidThemeEntry(candidate, inspectErr)
			}
			assets, collectErr := collectThemeAssets(vaultRoot, resolved)
			if collectErr != nil {
				return themeInputs{}, collectErr
			}
			inputs.assets = assets
		default:
			return themeInputs{}, fmt.Errorf("unsupported theme entry %q: only theme.css, slots.html, and assets/** are allowed", candidate)
		}
	}
	return inputs, nil
}

func invalidThemeEntry(candidate string, err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("theme entry %q disappeared while it was being inspected", candidate)
	}
	if errors.Is(err, internalfsutil.ErrSymlinkPath) || errors.Is(err, internalfsutil.ErrUnsupportedRegularFileSource) {
		return fmt.Errorf("theme entry %q must be a regular non-symlink file or directory inside the vault", candidate)
	}
	return fmt.Errorf("inspect theme entry %q: %w", candidate, err)
}

func collectThemeAssets(vaultRoot string, assetsRoot string) ([]themeAssetInput, error) {
	assets := make([]themeAssetInput, 0)
	err := filepath.WalkDir(assetsRoot, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if current == assetsRoot {
			return nil
		}
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("theme asset %q must not be a symbolic link", current)
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("theme asset %q must be a regular file", current)
		}
		resolved, _, err := internalfsutil.InspectContainedRegularFile(vaultRoot, current)
		if err != nil {
			return invalidThemeEntry(current, err)
		}
		relative, err := filepath.Rel(assetsRoot, resolved)
		if err != nil {
			return fmt.Errorf("resolve theme asset path %q: %w", resolved, err)
		}
		relative = filepath.ToSlash(relative)
		if strings.Contains(relative, `\`) {
			return fmt.Errorf("theme asset %q must use portable path characters", resolved)
		}
		relative = path.Clean(relative)
		if relative == "." || relative == ".." || strings.HasPrefix(relative, "../") {
			return fmt.Errorf("theme asset %q must stay below the theme assets directory", resolved)
		}
		outputPath := path.Join("assets/theme", relative)
		if !strings.HasPrefix(outputPath, "assets/theme/") {
			return fmt.Errorf("theme asset %q must publish below assets/theme", resolved)
		}
		assets = append(assets, themeAssetInput{sourcePath: resolved, outputPath: outputPath})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("inspect theme assets %q: %w", assetsRoot, err)
	}
	sort.Slice(assets, func(i, j int) bool {
		if assets[i].outputPath == assets[j].outputPath {
			return assets[i].sourcePath < assets[j].sourcePath
		}
		return assets[i].outputPath < assets[j].outputPath
	})
	return assets, nil
}

func copyThemeInputs(vaultRoot string, inputs themeInputs, outputRoot string) error {
	if inputs.stylesheet != "" {
		_, data, _, err := internalfsutil.ReadContainedRegularFile(vaultRoot, inputs.stylesheet)
		if err != nil {
			return fmt.Errorf("read theme stylesheet %q: %w", inputs.stylesheet, err)
		}
		if err := writeOutputFile(outputRoot, themeCSSOutputPath, data); err != nil {
			return fmt.Errorf("write theme stylesheet %q: %w", inputs.stylesheet, err)
		}
	}
	for _, asset := range inputs.assets {
		_, data, _, err := internalfsutil.ReadContainedRegularFile(vaultRoot, asset.sourcePath)
		if err != nil {
			return fmt.Errorf("read theme asset %q: %w", asset.sourcePath, err)
		}
		if err := writeOutputFile(outputRoot, asset.outputPath, data); err != nil {
			return fmt.Errorf("write theme asset %q: %w", asset.sourcePath, err)
		}
	}
	return nil
}
