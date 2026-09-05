package build

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/simp-lee/obsite/internal/analyze"
	"github.com/simp-lee/obsite/internal/asset"
)

func TestStrictBuildRewritesThemeCSSAndInvalidatesDependentURLs(t *testing.T) {
	vault := t.TempDir()
	writeStrictFile(t, vault, "obsite.yaml", "title: Theme\nbaseURL: https://example.test/docs/\nnavigation: []\n")
	writeStrictFile(t, vault, "_index.md", "---\ntitle: Home\npublish: true\n---\nHome\n")
	writeStrictFile(t, vault, ".obsite/theme/theme.css", `@import /* keep */ "styles/nested.css" screen;
.logo { background: url("My%20Logo.svg?v=1#mark"); }
/* url(not-a-resource) */ .label::after { content: "url(not-a-resource)"; }`)
	writeStrictFile(t, vault, ".obsite/theme/assets/styles/nested.css", `@font-face { src: url('../fonts/site.woff2') format('woff2'); }
.logo { background-image: image-set("../My Logo.svg" 1x, url(../My\ Logo.svg) 2x); }
.external { background: url(https://example.test/remote.png); mask: url(#local); }`)
	writeStrictFile(t, vault, ".obsite/theme/assets/fonts/site.woff2", "font bytes")
	writeStrictFile(t, vault, ".obsite/theme/assets/My Logo.svg", `<svg xmlns="http://www.w3.org/2000/svg"><path id="mark"/></svg>`)
	writeStrictFile(t, vault, ".obsite/theme/slots.html", `{{define "obsite-head-end"}}<link rel="stylesheet" href="{{themeAssetURL .SiteRootRel "styles/nested.css"}}">{{end}}`)
	output := filepath.Join(t.TempDir(), "site")
	first, err := BuildWithOptions(vault, output, Options{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	css := first.Assets[".obsite/theme/theme.css"]
	nested := first.Assets[".obsite/theme/assets/styles/nested.css"]
	logo := first.Assets[".obsite/theme/assets/My Logo.svg"]
	font := first.Assets[".obsite/theme/assets/fonts/site.woff2"]
	if !strings.HasSuffix(css.DstPath, ".css") || !strings.HasSuffix(logo.DstPath, ".svg") {
		t.Fatalf("theme destinations lost source extensions: CSS=%q logo=%q", css.DstPath, logo.DstPath)
	}
	rootCSS := string(readBuildOutputFile(t, output, css.DstPath))
	for _, want := range []string{path.Base(nested.DstPath), path.Base(logo.DstPath) + "?v=1#mark", `/* url(not-a-resource) */`, `content: "url(not-a-resource)"`} {
		if !strings.Contains(rootCSS, want) {
			t.Fatalf("theme CSS missing %q: %s", want, rootCSS)
		}
	}
	nestedCSS := string(readBuildOutputFile(t, output, nested.DstPath))
	for _, want := range []string{path.Base(font.DstPath), path.Base(logo.DstPath), "https://example.test/remote.png", "url(#local)"} {
		if !strings.Contains(nestedCSS, want) {
			t.Fatalf("nested CSS missing %q: %s", want, nestedCSS)
		}
	}
	// Resolve every emitted local CSS URL and verify that it names published bytes.
	for _, stylesheet := range []string{css.DstPath, nested.DstPath} {
		data := readBuildOutputFile(t, output, stylesheet)
		if !strings.Contains(stylesheet, fmt.Sprintf(".%x.", sha256.Sum256(data))) {
			t.Fatalf("CSS path does not hash emitted bytes: %s", stylesheet)
		}
		_, err := asset.RewriteCSSURLs(data, func(raw string) (string, error) {
			if strings.HasPrefix(raw, "https:") || strings.HasPrefix(raw, "#") {
				return raw, nil
			}
			resource := strings.Split(strings.Split(raw, "?")[0], "#")[0]
			readBuildOutputFile(t, output, path.Join(path.Dir(stylesheet), resource))
			return raw, nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	again, err := BuildWithOptions(vault, output, Options{Strict: true})
	if err != nil || !reflect.DeepEqual(first.Assets, again.Assets) {
		t.Fatalf("unchanged theme plan is not stable: %v", err)
	}
	writeStrictFile(t, vault, ".obsite/theme/assets/My Logo.svg", `<svg xmlns="http://www.w3.org/2000/svg"><circle id="mark"/></svg>`)
	changed, err := BuildWithOptions(vault, output, Options{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, old := range []string{css.SrcPath, nested.SrcPath, logo.SrcPath} {
		if changed.Assets[old].DstPath == first.Assets[old].DstPath {
			t.Fatalf("changed dependency did not invalidate %s", old)
		}
		if _, err := os.Stat(filepath.Join(output, filepath.FromSlash(first.Assets[old].DstPath))); !os.IsNotExist(err) {
			t.Fatalf("stale theme output retained for %s: %v", old, err)
		}
	}
	if changed.Assets[font.SrcPath].DstPath != font.DstPath {
		t.Fatal("unrelated theme font URL changed")
	}
}

func TestStrictThemeFailuresHaveSharedReadOnlyDiagnostics(t *testing.T) {
	for _, test := range []struct {
		name, slots, css, want string
	}{
		{name: "missing slot resource", slots: `{{define "obsite-footer-end"}}<img src="{{themeAssetURL .SiteRootRel "missing.svg"}}">{{end}}`, want: "missing.svg"},
		{name: "conditional slot resource", slots: `{{define "obsite-footer-end"}}{{if eq .RelPath "/guide/"}}<img src="{{themeAssetURL .SiteRootRel "missing.svg"}}">{{end}}{{end}}`, want: "missing.svg"},
		{name: "missing CSS resource", css: `.logo { background: url(missing.svg); }`, want: "missing.svg"},
		{name: "cyclic CSS", css: `@import "theme.css";`, want: "cyclic theme CSS"},
	} {
		t.Run(test.name, func(t *testing.T) {
			vault := t.TempDir()
			writeStrictFile(t, vault, "obsite.yaml", "title: Theme\nbaseURL: https://example.test/\nnavigation: []\n")
			writeStrictFile(t, vault, "_index.md", "---\ntitle: Home\npublish: true\n---\nHome\n")
			writeStrictFile(t, vault, "guide/_index.md", "---\ntitle: Guide\npublish: true\n---\nGuide\n")
			writeStrictFile(t, vault, ".obsite/theme/slots.html", test.slots)
			writeStrictFile(t, vault, ".obsite/theme/theme.css", test.css)
			output := filepath.Join(t.TempDir(), "site")
			writeStrictFile(t, output, managedOutputMarkerFilename, managedOutputMarkerContents)
			writeStrictFile(t, output, "index.html", "previous site")
			analysis, err := analyze.AnalyzeWithOutput(vault, output)
			if err == nil || !strings.Contains(fmt.Sprint(analysis.Diagnostics), test.want) {
				t.Fatalf("analyze: %v; diagnostics=%v", err, analysis.Diagnostics)
			}
			built, err := BuildWithOptions(vault, output, Options{Strict: true})
			if err == nil || !reflect.DeepEqual(analysis.Diagnostics, built.Diagnostics) {
				t.Fatalf("build did not share analysis diagnostics: %v; diagnostics=%v", err, built.Diagnostics)
			}
			if got := string(readBuildOutputFile(t, output, "index.html")); got != "previous site" {
				t.Fatalf("failed theme planning changed published site: %q", got)
			}
		})
	}
}

func TestStrictBuildPlansThemeAssets(t *testing.T) {
	for _, sameContent := range []bool{false, true} {
		t.Run(fmt.Sprintf("same-content=%t", sameContent), func(t *testing.T) {
			vault := t.TempDir()
			writeStrictFile(t, vault, "obsite.yaml", "title: Theme\nbaseURL: https://example.test/docs/\nnavigation: []\n")
			writeStrictFile(t, vault, "_index.md", "---\ntitle: Home\npublish: true\n---\nHome\n")
			writeStrictFile(t, vault, "guide/_index.md", "---\ntitle: Guide\npublish: true\n---\n![shared](../images/café.txt)\n")
			first, second := "first asset", "second asset"
			if sameContent {
				second = first
			}
			sources := map[string]string{
				".obsite/theme/assets/café.txt":       first,
				".obsite/theme/assets/cafe\u0301.txt": second,
				".obsite/theme/theme.css":             ":root { color: red; }",
				".obsite/theme/assets/theme.css":      ":root { color: blue; }",
				"images/café.txt":                     first,
			}
			for source, data := range sources {
				writeStrictFile(t, vault, source, data)
			}
			writeStrictFile(t, vault, ".obsite/theme/slots.html", `{{define "obsite-footer-end"}}<a href="{{themeAssetURL .SiteRootRel "café.txt"}}">first</a><a href="{{themeAssetURL .SiteRootRel "café.txt"}}">second</a>{{end}}`)
			output := filepath.Join(t.TempDir(), "site")
			if result, err := analyze.AnalyzeWithOutput(vault, output); err != nil || len(result.Diagnostics) != 0 {
				t.Fatalf("analyze: %v; diagnostics=%v", err, result.Diagnostics)
			}
			if _, err := os.Stat(output); !os.IsNotExist(err) {
				t.Fatalf("analysis touched output: %v", err)
			}
			result, err := BuildWithOptions(vault, output, Options{Strict: true})
			if err != nil {
				t.Fatal(err)
			}
			for source, data := range sources {
				asset := result.Assets[source]
				if asset == nil {
					t.Fatalf("theme source not in shared asset result: %q", source)
				}
				if !strings.Contains(asset.DstPath, fmt.Sprintf(".%x.", sha256.Sum256([]byte(data)))) {
					t.Fatalf("non-content-addressed destination: %q", asset.DstPath)
				}
				if got := string(readBuildOutputFile(t, output, asset.DstPath)); got != data {
					t.Fatalf("asset %q = %q, want %q", source, got, data)
				}
			}
			if result.Assets["images/café.txt"].DstPath != result.Assets[".obsite/theme/assets/café.txt"].DstPath {
				t.Fatal("theme and Markdown assets did not share allocation/deduplication")
			}
			for page, prefix := range map[string]string{"index.html": "./", "guide/index.html": "../"} {
				html := string(readBuildOutputFile(t, output, page))
				for _, source := range []string{".obsite/theme/assets/café.txt", ".obsite/theme/assets/cafe\u0301.txt"} {
					if !strings.Contains(html, prefix+result.Assets[source].DstPath) {
						t.Fatalf("%s missing planned slot URL for %s", page, source)
					}
				}
				if !strings.Contains(html, "/docs/"+result.Assets[".obsite/theme/theme.css"].DstPath) {
					t.Fatalf("%s missing planned theme CSS URL", page)
				}
			}
			if _, err := os.Stat(filepath.Join(output, "assets", "theme")); !os.IsNotExist(err) {
				t.Fatalf("legacy unhashed theme output exists: %v", err)
			}
		})
	}
}
