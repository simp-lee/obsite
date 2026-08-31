package render

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVendoredRuntimeHashesMatchNotices(t *testing.T) {
	notices, err := os.ReadFile(filepath.Join("..", "..", "THIRD_PARTY.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, pattern := range []string{"vendor/katex/katex.min.js", "vendor/katex/katex.min.css", "vendor/katex/contrib/auto-render.min.js", "vendor/katex/fonts/*", "vendor/mermaid/mermaid.min.js"} {
		paths, err := filepath.Glob(pattern)
		if err != nil || len(paths) == 0 {
			t.Fatalf("glob %q = %v, %v", pattern, paths, err)
		}
		for _, path := range paths {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			entry := fmt.Sprintf("%x  internal/render/%s", sha256.Sum256(data), filepath.ToSlash(path))
			if !strings.Contains(string(notices), entry) {
				t.Fatalf("THIRD_PARTY.md missing %q", entry)
			}
		}
	}
}
