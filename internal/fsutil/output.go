package fsutil

import (
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"strings"
)

// ResolveOutputURLPath maps one URL-escaped, relative output path to the
// decoded filesystem path that a conventional static file server will use.
func ResolveOutputURLPath(outputRoot string, outputURLPath string) (cleanURLPath string, absoluteFilePath string, err error) {
	cleanURLPath = strings.TrimSpace(strings.ReplaceAll(outputURLPath, `\`, "/"))
	if cleanURLPath == "" || strings.HasPrefix(cleanURLPath, "/") {
		return "", "", fmt.Errorf("output path %q must be relative", outputURLPath)
	}
	cleanURLPath = path.Clean(cleanURLPath)
	if cleanURLPath == "." || cleanURLPath == ".." || strings.HasPrefix(cleanURLPath, "../") {
		return "", "", fmt.Errorf("output path %q must stay within output root", outputURLPath)
	}

	filePath, err := url.PathUnescape(cleanURLPath)
	if err != nil {
		return "", "", fmt.Errorf("output path %q has invalid URL escaping: %w", outputURLPath, err)
	}
	if strings.ContainsRune(filePath, 0) || strings.HasPrefix(filePath, "/") {
		return "", "", fmt.Errorf("output path %q must stay within output root", outputURLPath)
	}
	filePath = path.Clean(filePath)
	if filePath == "." || filePath == ".." || strings.HasPrefix(filePath, "../") {
		return "", "", fmt.Errorf("output path %q must stay within output root", outputURLPath)
	}

	localizedFilePath, err := filepath.Localize(filePath)
	if err != nil {
		return "", "", fmt.Errorf("output path %q cannot be represented on this filesystem: %w", outputURLPath, err)
	}
	absoluteRoot, err := filepath.Abs(outputRoot)
	if err != nil {
		return "", "", fmt.Errorf("resolve output root %q: %w", outputRoot, err)
	}
	absoluteFilePath = filepath.Join(absoluteRoot, localizedFilePath)
	relativeToRoot, err := filepath.Rel(absoluteRoot, absoluteFilePath)
	if err != nil {
		return "", "", fmt.Errorf("resolve output path %q: %w", cleanURLPath, err)
	}
	if relativeToRoot == ".." || strings.HasPrefix(relativeToRoot, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("output path %q must stay within output root", cleanURLPath)
	}
	return cleanURLPath, absoluteFilePath, nil
}
