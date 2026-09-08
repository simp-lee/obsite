package fsutil

import (
	"path/filepath"
	"testing"
)

func TestResolveOutputURLPathUsesDecodedFilesystemNames(t *testing.T) {
	root := t.TempDir()

	tests := []struct {
		name     string
		urlPath  string
		wantPath string
		wantErr  bool
	}{
		{name: "space and Unicode", urlPath: "docs/Caf%C3%A9%20Guide/index.html", wantPath: "docs/Café Guide/index.html"},
		{name: "exactly one decoding pass", urlPath: "assets/name%2520part.txt", wantPath: "assets/name%20part.txt"},
		{name: "encoded parent traversal", urlPath: "%2E%2E/escape.txt", wantErr: true},
		{name: "encoded absolute path", urlPath: "%2Fescape.txt", wantErr: true},
		{name: "malformed escape", urlPath: "assets/bad%name.txt", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleaned, got, err := ResolveOutputURLPath(root, tt.urlPath)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ResolveOutputURLPath(%q) error = nil", tt.urlPath)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveOutputURLPath(%q) error = %v", tt.urlPath, err)
			}
			if cleaned != tt.urlPath {
				t.Fatalf("clean URL path = %q, want %q", cleaned, tt.urlPath)
			}
			if want := filepath.Join(root, filepath.FromSlash(tt.wantPath)); got != want {
				t.Fatalf("filesystem path = %q, want %q", got, want)
			}
		})
	}
}
