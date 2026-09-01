// Package fsutil provides shared filesystem inspection helpers.
package fsutil

import "errors"

// ErrUnsupportedRegularFileSource indicates that a source path is not a regular non-symlink file.
var ErrUnsupportedRegularFileSource = errors.New("source must be a regular non-symlink file")
