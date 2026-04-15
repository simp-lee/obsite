// Package fsutil provides shared filesystem inspection helpers.
package fsutil

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// ErrUnsupportedRegularFileSource indicates that a source path is not a regular non-symlink file.
var ErrUnsupportedRegularFileSource = errors.New("source must be a regular non-symlink file")

// InspectRegularNonSymlinkFile resolves filePath to an absolute path and ensures the final entry is a regular non-symlink file.
func InspectRegularNonSymlinkFile(filePath string) (string, os.FileInfo, error) {
	trimmedPath := strings.TrimSpace(filePath)
	if trimmedPath == "" {
		return "", nil, os.ErrNotExist
	}

	absPath, err := filepath.Abs(trimmedPath)
	if err != nil {
		return "", nil, err
	}

	info, err := os.Lstat(absPath)
	if err != nil {
		return "", nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", nil, ErrUnsupportedRegularFileSource
	}

	return absPath, info, nil
}
