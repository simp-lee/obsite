package build

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	internalrender "github.com/simp-lee/obsite/internal/render"
	xhtml "golang.org/x/net/html"
)

func TestBuildReferencesOneContentAddressedRuntimeFromEveryPage(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	writeBuildTestFile(t, vaultPath, "notes/plain.md", "# Plain\n\nNo enhanced content.\n")
	writeBuildTestFile(t, vaultPath, "notes/rich.md", "# Rich\n\nInline $x^2$.\n\n```mermaid\ngraph TD\nA-->B\n```\n")
	if _, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard}); err != nil {
		t.Fatalf("buildWithOptions() error = %v", err)
	}

	runtimePath, err := internalrender.SharedRuntimeOutputPath()
	if err != nil {
		t.Fatal(err)
	}
	runtimeData := readBuildOutputFile(t, outputPath, runtimePath)
	hash := sha256.Sum256(runtimeData)
	if want := fmt.Sprintf("assets/obsite/runtime.%x.js", hash); runtimePath != want {
		t.Fatalf("runtime path = %q, want content address %q", runtimePath, want)
	}
	for _, marker := range [][]byte{
		[]byte("applyTheme(initialPreference)"),
		[]byte("renderMathInElement"),
		[]byte("window.mermaid.initialize"),
		[]byte("KaTeX initialization failed"),
		[]byte("Mermaid initialization failed"),
	} {
		if !bytes.Contains(runtimeData, marker) {
			t.Fatalf("runtime missing %q", marker)
		}
	}

	err = filepath.Walk(outputPath, func(current string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir() || filepath.Ext(current) != ".html" {
			return walkErr
		}
		relPath, err := filepath.Rel(outputPath, current)
		if err != nil {
			return err
		}
		relPath = filepath.ToSlash(relPath)
		html := readBuildOutputFile(t, outputPath, relPath)
		sources := scriptSources(t, html)
		var runtimeSources []string
		for _, source := range sources {
			if strings.Contains(source, "assets/obsite/runtime.") {
				runtimeSources = append(runtimeSources, source)
			}
			for _, vendor := range []string{"katex.min.js", "auto-render.min.js", "mermaid.min.js"} {
				if strings.Contains(source, vendor) {
					t.Fatalf("%s directly loads vendor script %q", relPath, source)
				}
			}
		}
		if len(runtimeSources) != 1 {
			t.Fatalf("%s runtime script sources = %#v, want exactly one", relPath, runtimeSources)
		}
		resolved := path.Clean(path.Join(path.Dir(relPath), runtimeSources[0]))
		if resolved != runtimePath {
			t.Fatalf("%s runtime source resolves to %q, want %q", relPath, resolved, runtimePath)
		}
		for _, duplicated := range [][]byte{[]byte("applyTheme(initialPreference)"), []byte("renderMathInElement"), []byte("window.mermaid.initialize")} {
			if bytes.Contains(html, duplicated) {
				t.Fatalf("%s duplicates shared runtime code %q", relPath, duplicated)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk generated HTML: %v", err)
	}

	richHTML := readBuildOutputFile(t, outputPath, "rich/index.html")
	if !bytes.Contains(richHTML, []byte("data-obsite-math")) || !bytes.Contains(richHTML, []byte("data-obsite-mermaid")) {
		t.Fatalf("rich page missing vendor feature flags\n%s", richHTML)
	}
	plainHTML := readBuildOutputFile(t, outputPath, "plain/index.html")
	if bytes.Contains(plainHTML, []byte("data-obsite-math")) || bytes.Contains(plainHTML, []byte("data-obsite-mermaid")) || bytes.Contains(plainHTML, []byte("katex.min.css")) {
		t.Fatalf("plain page executes or styles unused vendor features\n%s", plainHTML)
	}
}

func scriptSources(t *testing.T, document []byte) []string {
	t.Helper()
	root, err := xhtml.Parse(bytes.NewReader(document))
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}
	var sources []string
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node.Type == xhtml.ElementNode && node.Data == "script" {
			for _, attribute := range node.Attr {
				if attribute.Key == "src" {
					sources = append(sources, attribute.Val)
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return sources
}
