package build

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	internalasset "github.com/simp-lee/obsite/internal/asset"
	internalconfig "github.com/simp-lee/obsite/internal/config"
	"github.com/simp-lee/obsite/internal/diag"
	"github.com/simp-lee/obsite/internal/model"
	"github.com/simp-lee/obsite/internal/vault"
)

func TestBuildEmitsSiteWithDeterministicOrderingAndCleanURLs(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, ".obsidian/app.json", `{"attachmentFolderPath":"attachments"}`)
	writeBuildTestFile(t, vaultPath, "images/hero.png", "hero-image")
	writeBuildTestFile(t, vaultPath, "notes/alpha.md", `---
title: Alpha
date: 2026-04-06
tags:
  - Topic/Child
---
# Alpha

Alpha note.

![Hero](../images/hero.png)

[[Beta]]
`)
	writeBuildTestFile(t, vaultPath, "notes/beta.md", `---
title: Beta
date: 2026-04-06
tags:
  - Topic
---
# Beta

Beta note links back to [[Alpha]].
`)
	writeBuildTestFile(t, vaultPath, "notes/gamma.md", `---
title: Gamma
date: 2026-04-05
---
# Gamma

Gamma note.
`)

	var diagnostics bytes.Buffer
	result, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{
		concurrency:       2,
		diagnosticsWriter: &diagnostics,
	})
	if err != nil {
		t.Fatalf("buildWithOptions() error = %v", err)
	}
	if result == nil {
		t.Fatal("buildWithOptions() = nil result, want build result")
	}
	if result.NotePages != 3 {
		t.Fatalf("result.NotePages = %d, want %d", result.NotePages, 3)
	}
	if result.TagPages != 2 {
		t.Fatalf("result.TagPages = %d, want %d", result.TagPages, 2)
	}
	if result.Graph == nil {
		t.Fatal("result.Graph = nil, want link graph")
	}
	if got := result.Graph.Backward["notes/alpha.md"]; !reflect.DeepEqual(got, []string{"notes/beta.md"}) {
		t.Fatalf("result.Graph.Backward[notes/alpha.md] = %#v, want %#v", got, []string{"notes/beta.md"})
	}
	if len(result.Diagnostics) != 0 {
		t.Fatalf("len(result.Diagnostics) = %d, want 0", len(result.Diagnostics))
	}
	if got := diagnostics.String(); strings.TrimSpace(got) != "" {
		t.Fatalf("diagnostics summary = %q, want empty summary for clean build", got)
	}

	alphaHTML := readBuildOutputFile(t, outputPath, "alpha/index.html")
	if !containsAny(alphaHTML, `href="../beta/">Beta</a>`, `href=../beta/>Beta</a>`) {
		t.Fatalf("alpha page missing clean beta link\n%s", alphaHTML)
	}
	if !containsAny(alphaHTML, `src="../assets/hero.png"`, `src=../assets/hero.png`) {
		t.Fatalf("alpha page missing rewritten asset link\n%s", alphaHTML)
	}
	if !containsAny(alphaHTML, `href="../tags/topic/child/">#topic/child</a>`, `href=../tags/topic/child/>#topic/child</a>`) {
		t.Fatalf("alpha page missing tag clean URL\n%s", alphaHTML)
	}
	if !containsAny(alphaHTML, `href="../beta/">Beta</a>`, `href=../beta/>Beta</a>`) {
		t.Fatalf("alpha page missing backlink entry for beta\n%s", alphaHTML)
	}

	tagHTML := readBuildOutputFile(t, outputPath, "tags/topic/index.html")
	if !containsAny(tagHTML, `href="child/">#topic/child</a>`, `href=child/>#topic/child</a>`) {
		t.Fatalf("topic tag page missing child tag link\n%s", tagHTML)
	}
	alphaIndex := indexAny(tagHTML, `href="../../alpha/">Alpha</a>`, `href=../../alpha/>Alpha</a>`)
	betaIndex := indexAny(tagHTML, `href="../../beta/">Beta</a>`, `href=../../beta/>Beta</a>`)
	if alphaIndex == -1 || betaIndex == -1 || alphaIndex > betaIndex {
		t.Fatalf("topic tag page note ordering is not deterministic\n%s", tagHTML)
	}

	indexHTML := readBuildOutputFile(t, outputPath, "index.html")
	alphaIndex = indexAny(indexHTML, `href="alpha/">Alpha</a>`, `href=alpha/>Alpha</a>`)
	betaIndex = indexAny(indexHTML, `href="beta/">Beta</a>`, `href=beta/>Beta</a>`)
	if alphaIndex == -1 || betaIndex == -1 || alphaIndex > betaIndex {
		t.Fatalf("index page recent note ordering is not deterministic\n%s", indexHTML)
	}
	if bytes.Count(indexHTML, []byte("\n")) >= 5 {
		t.Fatalf("index page does not appear minified\n%s", indexHTML)
	}

	notFoundHTML := readBuildOutputFile(t, outputPath, "404.html")
	if !containsAny(notFoundHTML, `href="alpha/">Alpha</a>`, `href=alpha/>Alpha</a>`) {
		t.Fatalf("404 page missing recent note clean URL\n%s", notFoundHTML)
	}

	styleCSS := readBuildOutputFile(t, outputPath, "style.css")
	templateCSS, err := os.ReadFile(filepath.Join("..", "..", "templates", "style.css"))
	if err != nil {
		t.Fatalf("os.ReadFile(templates/style.css) error = %v", err)
	}
	if len(styleCSS) >= len(templateCSS) {
		t.Fatalf("len(style.css) = %d, want minified output smaller than embedded source %d", len(styleCSS), len(templateCSS))
	}

	sitemapXML := readBuildOutputFile(t, outputPath, "sitemap.xml")
	for _, want := range []string{
		"https://example.com/blog/",
		"https://example.com/blog/alpha/",
		"https://example.com/blog/beta/",
		"https://example.com/blog/gamma/",
		"https://example.com/blog/tags/topic/",
		"https://example.com/blog/tags/topic/child/",
	} {
		if !bytes.Contains(sitemapXML, []byte(want)) {
			t.Fatalf("sitemap.xml missing %q\n%s", want, sitemapXML)
		}
	}
	if bytes.Contains(sitemapXML, []byte("404.html")) {
		t.Fatalf("sitemap.xml unexpectedly includes 404 page\n%s", sitemapXML)
	}

	robotsTXT := readBuildOutputFile(t, outputPath, "robots.txt")
	if !bytes.Contains(robotsTXT, []byte("Sitemap: https://example.com/blog/sitemap.xml")) {
		t.Fatalf("robots.txt missing sitemap URL\n%s", robotsTXT)
	}

	if got := readBuildOutputFile(t, outputPath, "assets/hero.png"); string(got) != "hero-image" {
		t.Fatalf("copied asset content = %q, want %q", string(got), "hero-image")
	}
}

func TestBuildSummarizesWarningsForCanvasAndContinues(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "notes/only.md", `---
title: Only Note
date: 2026-04-06
---
# Only Note

Body.
`)
	writeBuildTestFile(t, vaultPath, "boards/plan.canvas", `{"nodes":[]}`)

	var diagnostics bytes.Buffer
	result, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{
		concurrency:       1,
		diagnosticsWriter: &diagnostics,
	})
	if err != nil {
		t.Fatalf("buildWithOptions() error = %v", err)
	}
	if result == nil {
		t.Fatal("buildWithOptions() = nil result, want build result")
	}
	if len(result.Diagnostics) != 1 {
		t.Fatalf("len(result.Diagnostics) = %d, want 1 warning", len(result.Diagnostics))
	}
	if result.Diagnostics[0].Severity != diag.SeverityWarning {
		t.Fatalf("result.Diagnostics[0].Severity = %q, want %q", result.Diagnostics[0].Severity, diag.SeverityWarning)
	}
	if result.Diagnostics[0].Kind != diag.KindUnsupportedSyntax {
		t.Fatalf("result.Diagnostics[0].Kind = %q, want %q", result.Diagnostics[0].Kind, diag.KindUnsupportedSyntax)
	}
	if result.Diagnostics[0].Location.Path != "boards/plan.canvas" {
		t.Fatalf("result.Diagnostics[0].Location.Path = %q, want %q", result.Diagnostics[0].Location.Path, "boards/plan.canvas")
	}

	summary := diagnostics.String()
	if !strings.Contains(summary, "Warnings (1)") {
		t.Fatalf("diagnostics summary missing warning count\n%s", summary)
	}
	if !strings.Contains(summary, "boards/plan.canvas") {
		t.Fatalf("diagnostics summary missing canvas path\n%s", summary)
	}
	if !strings.Contains(summary, "canvas files are skipped") {
		t.Fatalf("diagnostics summary missing canvas degradation message\n%s", summary)
	}

	_ = readBuildOutputFile(t, outputPath, "only/index.html")
}

func TestBuildGracefullyDegradesDataviewAndBlockReferenceEmbeds(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "notes/current.md", `---
title: Current
date: 2026-04-06
---
# Current

`+"```"+`dataview
LIST
`+"```"+`

`+"```"+`dataviewjs
dv.current()
`+"```"+`

![[Target#^block-ref]]
`)
	writeBuildTestFile(t, vaultPath, "notes/target.md", `---
title: Target
date: 2026-04-06
---
# Target

Target body.
`)

	var diagnostics bytes.Buffer
	result, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{
		concurrency:       1,
		diagnosticsWriter: &diagnostics,
	})
	if err != nil {
		t.Fatalf("buildWithOptions() error = %v", err)
	}
	if result == nil {
		t.Fatal("buildWithOptions() = nil result, want build result")
	}

	want := []diag.Diagnostic{
		{
			Severity: diag.SeverityWarning,
			Kind:     diag.KindUnsupportedSyntax,
			Location: diag.Location{Path: "notes/current.md", Line: 8},
			Message:  `dataview fenced code block is not supported; rendering as plain preformatted text`,
		},
		{
			Severity: diag.SeverityWarning,
			Kind:     diag.KindUnsupportedSyntax,
			Location: diag.Location{Path: "notes/current.md", Line: 12},
			Message:  `dataviewjs fenced code block is not supported; rendering as plain preformatted text`,
		},
		{
			Severity: diag.SeverityWarning,
			Kind:     diag.KindUnsupportedSyntax,
			Location: diag.Location{Path: "notes/current.md", Line: 15},
			Message:  `embed "Target#^block-ref" block reference embeds are not supported; rendering as plain text with a link`,
		},
	}
	if got := result.Diagnostics; !reflect.DeepEqual(got, want) {
		t.Fatalf("result.Diagnostics = %#v, want %#v", got, want)
	}

	currentHTML := readBuildOutputFile(t, outputPath, "current/index.html")
	if !containsAny(currentHTML, `class="unsupported-syntax unsupported-dataview"`, `class=unsupported-syntax unsupported-dataview`) {
		t.Fatalf("current page missing dataview fallback pre\n%s", currentHTML)
	}
	if !containsAny(currentHTML, `class="unsupported-syntax unsupported-dataviewjs"`, `class=unsupported-syntax unsupported-dataviewjs`) {
		t.Fatalf("current page missing dataviewjs fallback pre\n%s", currentHTML)
	}
	for _, wantText := range []string{"LIST", "dv.current()", "Target#^block-ref"} {
		if !bytes.Contains(currentHTML, []byte(wantText)) {
			t.Fatalf("current page missing degraded content %q\n%s", wantText, currentHTML)
		}
	}
	if !containsAny(currentHTML, `href="../target/"`, `href=../target/>`, `href=../target/`) {
		t.Fatalf("current page missing block reference fallback link\n%s", currentHTML)
	}
	if bytes.Contains(currentHTML, []byte(`class="chroma"`)) || bytes.Contains(currentHTML, []byte(`class=chroma`)) {
		t.Fatalf("current page unexpectedly highlighted Dataview fences\n%s", currentHTML)
	}

	summary := diagnostics.String()
	if !strings.Contains(summary, "Warnings (3)") {
		t.Fatalf("diagnostics summary missing warning count\n%s", summary)
	}
	for _, fragment := range []string{
		`notes/current.md:8 [unsupported_syntax] dataview fenced code block is not supported; rendering as plain preformatted text`,
		`notes/current.md:12 [unsupported_syntax] dataviewjs fenced code block is not supported; rendering as plain preformatted text`,
		`notes/current.md:15 [unsupported_syntax] embed "Target#^block-ref" block reference embeds are not supported; rendering as plain text with a link`,
	} {
		if !strings.Contains(summary, fragment) {
			t.Fatalf("diagnostics summary missing %q\n%s", fragment, summary)
		}
	}
	if strings.Contains(summary, "Fatal build errors") {
		t.Fatalf("diagnostics summary = %q, want warnings only", summary)
	}
}

func TestBuildResetsManagedOutputInsideVaultBeforeScan(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(vaultPath, "site")

	writeBuildTestFile(t, vaultPath, "images/hero.png", "hero-image")
	writeBuildTestFile(t, vaultPath, "notes/alpha.md", `---
title: Alpha
date: 2026-04-06
---
# Alpha

![Hero](../images/hero.png)
`)

	if _, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{}); err != nil {
		t.Fatalf("first buildWithOptions() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(outputPath, managedOutputMarkerFilename)); err != nil {
		t.Fatalf("os.Stat(output marker) error = %v, want managed output marker after first build", err)
	}
	if err := os.WriteFile(filepath.Join(outputPath, "stale.txt"), []byte("obsolete"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(stale.txt) error = %v", err)
	}

	result, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{})
	if err != nil {
		t.Fatalf("second buildWithOptions() error = %v", err)
	}
	if result == nil {
		t.Fatal("second buildWithOptions() = nil result, want build result")
	}

	asset := result.Assets["images/hero.png"]
	if asset == nil {
		t.Fatal("result.Assets[images/hero.png] = nil, want copied asset")
	}
	if asset.DstPath != "assets/hero.png" {
		t.Fatalf("result.Assets[images/hero.png].DstPath = %q, want %q", asset.DstPath, "assets/hero.png")
	}

	alphaHTML := readBuildOutputFile(t, outputPath, "alpha/index.html")
	if !containsAny(alphaHTML, `src="../assets/hero.png"`, `src=../assets/hero.png`) {
		t.Fatalf("alpha page missing stable asset path after rebuild\n%s", alphaHTML)
	}

	if _, err := os.Stat(filepath.Join(outputPath, "stale.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("os.Stat(stale.txt) error = %v, want not-exist after rebuild", err)
	}

	assetEntries, err := os.ReadDir(filepath.Join(outputPath, "assets"))
	if err != nil {
		t.Fatalf("os.ReadDir(assets) error = %v", err)
	}
	if len(assetEntries) != 1 || assetEntries[0].Name() != "hero.png" {
		t.Fatalf("output assets = %#v, want only hero.png", assetEntries)
	}
	if _, err := os.Stat(filepath.Join(outputPath, managedOutputMarkerFilename)); err != nil {
		t.Fatalf("os.Stat(output marker) error = %v, want managed output marker after rebuild", err)
	}
}

func TestBuildRemovesStaleNoteOutputsAfterDeleteOrUnpublish(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(t *testing.T, vaultPath string)
	}{
		{
			name: "delete note",
			mutate: func(t *testing.T, vaultPath string) {
				t.Helper()
				if err := os.Remove(filepath.Join(vaultPath, "notes", "alpha.md")); err != nil {
					t.Fatalf("os.Remove(alpha.md) error = %v", err)
				}
			},
		},
		{
			name: "unpublish note",
			mutate: func(t *testing.T, vaultPath string) {
				t.Helper()
				writeBuildTestFile(t, vaultPath, "notes/alpha.md", `---
title: Alpha
date: 2026-04-06
publish: false
---
# Alpha

Hidden.
`)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			vaultPath := t.TempDir()
			outputPath := filepath.Join(t.TempDir(), "site")
			writeBuildTestFile(t, vaultPath, "notes/alpha.md", `---
title: Alpha
date: 2026-04-06
---
# Alpha

Visible.
`)

			if _, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{}); err != nil {
				t.Fatalf("first buildWithOptions() error = %v", err)
			}
			tt.mutate(t, vaultPath)

			result, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{})
			if err != nil {
				t.Fatalf("second buildWithOptions() error = %v", err)
			}
			if result.NotePages != 0 {
				t.Fatalf("result.NotePages = %d, want %d", result.NotePages, 0)
			}

			if _, err := os.Stat(filepath.Join(outputPath, "alpha", "index.html")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("os.Stat(alpha/index.html) error = %v, want not-exist after rebuild", err)
			}

			indexHTML := readBuildOutputFile(t, outputPath, "index.html")
			if bytes.Contains(indexHTML, []byte("Alpha")) {
				t.Fatalf("index.html still references removed note\n%s", indexHTML)
			}
		})
	}
}

func TestBuildPreservesPreviousManagedOutputWhenConfigValidationFails(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(vaultPath, "site")

	writeBuildTestFile(t, vaultPath, "notes/alpha.md", `---
title: Alpha
date: 2026-04-06
---
# Alpha

Stable.
`)

	if _, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard}); err != nil {
		t.Fatalf("first buildWithOptions() error = %v", err)
	}

	previousIndex := append([]byte(nil), readBuildOutputFile(t, outputPath, "index.html")...)
	previousAlpha := append([]byte(nil), readBuildOutputFile(t, outputPath, "alpha/index.html")...)

	writeBuildTestFile(t, vaultPath, "notes/beta.md", `---
title: Beta
date: 2026-04-07
---
# Beta

New note that must not publish on failure.
`)

	brokenCfg := testBuildSiteConfig()
	brokenCfg.BaseURL = ""
	_, err := buildWithOptions(brokenCfg, vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard})
	if err == nil {
		t.Fatal("second buildWithOptions() error = nil, want config validation failure")
	}
	if !strings.Contains(err.Error(), "validate config: baseURL is required") {
		t.Fatalf("second buildWithOptions() error = %v, want config validation failure", err)
	}

	if got := readBuildOutputFile(t, outputPath, "index.html"); !bytes.Equal(got, previousIndex) {
		t.Fatalf("index.html changed after failed rebuild\nwant:\n%s\n\ngot:\n%s", previousIndex, got)
	}
	if got := readBuildOutputFile(t, outputPath, "alpha/index.html"); !bytes.Equal(got, previousAlpha) {
		t.Fatalf("alpha/index.html changed after failed rebuild\nwant:\n%s\n\ngot:\n%s", previousAlpha, got)
	}
	if _, err := os.Stat(filepath.Join(outputPath, "beta", "index.html")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("os.Stat(beta/index.html) error = %v, want beta page absent after failed rebuild", err)
	}
	if bytes.Contains(readBuildOutputFile(t, outputPath, "index.html"), []byte("Beta")) {
		t.Fatalf("index.html unexpectedly published beta after failed rebuild\n%s", readBuildOutputFile(t, outputPath, "index.html"))
	}
	if _, err := os.Stat(filepath.Join(outputPath, managedOutputMarkerFilename)); err != nil {
		t.Fatalf("os.Stat(output marker) error = %v, want managed output marker preserved after failed rebuild", err)
	}
}

func TestBuildValidatesSiteConfigBeforePassOneWork(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "notes/broken.md", `---
title: Broken
tags: [broken
---
# Broken
`)

	brokenCfg := testBuildSiteConfig()
	brokenCfg.BaseURL = ""

	_, err := buildWithOptions(brokenCfg, vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard})
	if err == nil {
		t.Fatal("buildWithOptions() error = nil, want config validation failure before pass 1")
	}
	if !strings.Contains(err.Error(), "validate config: baseURL is required") {
		t.Fatalf("buildWithOptions() error = %v, want config validation failure", err)
	}
	if strings.Contains(err.Error(), "parse frontmatter") {
		t.Fatalf("buildWithOptions() error = %v, want config validation before pass-1 frontmatter parsing", err)
	}
}

func TestBuildTagPagesUsePublishedAtFallbackOrdering(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	alphaPath := filepath.Join(vaultPath, "notes", "alpha.md")
	zetaPath := filepath.Join(vaultPath, "notes", "zeta.md")
	writeBuildTestFile(t, vaultPath, "notes/alpha.md", `---
title: Alpha
tags:
  - Topic
---
# Alpha

Older note.
`)
	writeBuildTestFile(t, vaultPath, "notes/zeta.md", `---
title: Zeta
tags:
  - Topic
---
# Zeta

Newer note.
`)

	older := time.Date(2026, time.April, 4, 9, 0, 0, 0, time.UTC)
	newer := time.Date(2026, time.April, 5, 9, 0, 0, 0, time.UTC)
	if err := os.Chtimes(alphaPath, older, older); err != nil {
		t.Fatalf("os.Chtimes(alpha.md) error = %v", err)
	}
	if err := os.Chtimes(zetaPath, newer, newer); err != nil {
		t.Fatalf("os.Chtimes(zeta.md) error = %v", err)
	}

	if _, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{}); err != nil {
		t.Fatalf("buildWithOptions() error = %v", err)
	}

	indexHTML := readBuildOutputFile(t, outputPath, "index.html")
	alphaIndex := indexAny(indexHTML, `href="alpha/">Alpha</a>`, `href=alpha/>Alpha</a>`)
	zetaIndex := indexAny(indexHTML, `href="zeta/">Zeta</a>`, `href=zeta/>Zeta</a>`)
	if alphaIndex == -1 || zetaIndex == -1 || zetaIndex > alphaIndex {
		t.Fatalf("index page does not use PublishedAt fallback ordering\n%s", indexHTML)
	}

	tagHTML := readBuildOutputFile(t, outputPath, "tags/topic/index.html")
	alphaIndex = indexAny(tagHTML, `href="../../alpha/">Alpha</a>`, `href=../../alpha/>Alpha</a>`)
	zetaIndex = indexAny(tagHTML, `href="../../zeta/">Zeta</a>`, `href=../../zeta/>Zeta</a>`)
	if alphaIndex == -1 || zetaIndex == -1 || zetaIndex > alphaIndex {
		t.Fatalf("tag page does not match PublishedAt fallback ordering\n%s", tagHTML)
	}
}

func TestBuildSitemapUsesRecencyThenPathOrdering(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	alphaPath := filepath.Join(vaultPath, "notes", "alpha.md")
	betaPath := filepath.Join(vaultPath, "notes", "beta.md")
	gammaPath := filepath.Join(vaultPath, "notes", "gamma.md")
	writeBuildTestFile(t, vaultPath, "notes/alpha.md", `---
title: Alpha
date: 2099-04-06
tags:
  - Topic/Child
---
# Alpha

Alpha note.
`)
	writeBuildTestFile(t, vaultPath, "notes/beta.md", `---
title: Beta
date: 2099-04-06
tags:
  - Topic
---
# Beta

Beta note.
`)
	writeBuildTestFile(t, vaultPath, "notes/gamma.md", `---
title: Gamma
date: 2099-04-05
---
# Gamma

Gamma note.
`)

	fixedLastModified := time.Date(2026, time.April, 6, 12, 0, 0, 0, time.UTC)
	for _, notePath := range []string{alphaPath, betaPath, gammaPath} {
		if err := os.Chtimes(notePath, fixedLastModified, fixedLastModified); err != nil {
			t.Fatalf("os.Chtimes(%q) error = %v", notePath, err)
		}
	}

	if _, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{}); err != nil {
		t.Fatalf("buildWithOptions() error = %v", err)
	}

	locs := sitemapLocs(t, readBuildOutputFile(t, outputPath, "sitemap.xml"))
	want := []string{
		"https://example.com/blog/alpha/",
		"https://example.com/blog/beta/",
		"https://example.com/blog/gamma/",
		"https://example.com/blog/",
		"https://example.com/blog/tags/topic/",
		"https://example.com/blog/tags/topic/child/",
	}
	if !reflect.DeepEqual(locs, want) {
		t.Fatalf("sitemap locs = %#v, want %#v", locs, want)
	}
}

func TestBuildSucceedsWithNoPublicNotes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(t *testing.T, vaultPath string)
	}{
		{
			name: "empty vault",
			setup: func(t *testing.T, vaultPath string) {
				t.Helper()
			},
		},
		{
			name: "all unpublished",
			setup: func(t *testing.T, vaultPath string) {
				t.Helper()
				writeBuildTestFile(t, vaultPath, "notes/private.md", `---
title: Private
publish: false
---
# Private

Hidden.
`)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			vaultPath := t.TempDir()
			outputPath := filepath.Join(t.TempDir(), "site")
			tt.setup(t, vaultPath)

			result, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{})
			if err != nil {
				t.Fatalf("buildWithOptions() error = %v", err)
			}
			if result == nil {
				t.Fatal("buildWithOptions() = nil result, want build result")
			}
			if result.NotePages != 0 {
				t.Fatalf("result.NotePages = %d, want %d", result.NotePages, 0)
			}
			if result.TagPages != 0 {
				t.Fatalf("result.TagPages = %d, want %d", result.TagPages, 0)
			}

			_ = readBuildOutputFile(t, outputPath, "index.html")
			_ = readBuildOutputFile(t, outputPath, "404.html")
			robotsTXT := readBuildOutputFile(t, outputPath, "robots.txt")
			if !bytes.Contains(robotsTXT, []byte("Sitemap: https://example.com/blog/sitemap.xml")) {
				t.Fatalf("robots.txt missing sitemap URL\n%s", robotsTXT)
			}

			sitemapXML := readBuildOutputFile(t, outputPath, "sitemap.xml")
			if !bytes.Contains(sitemapXML, []byte("https://example.com/blog/")) {
				t.Fatalf("sitemap.xml missing index URL\n%s", sitemapXML)
			}
			if !bytes.Contains(sitemapXML, []byte("1970-01-01T00:00:00Z")) {
				t.Fatalf("sitemap.xml missing deterministic fallback lastmod\n%s", sitemapXML)
			}
			if bytes.Contains(sitemapXML, []byte("404.html")) {
				t.Fatalf("sitemap.xml unexpectedly includes 404 page\n%s", sitemapXML)
			}
		})
	}
}

func TestBuildRejectsVaultRootAsOutputPath(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	writeBuildTestFile(t, vaultPath, "notes/alpha.md", `# Alpha`)

	_, err := buildWithOptions(testBuildSiteConfig(), vaultPath, vaultPath, buildOptions{})
	if err == nil {
		t.Fatal("buildWithOptions() error = nil, want vault-root output rejection")
	}
	if !strings.Contains(err.Error(), "vault root") {
		t.Fatalf("buildWithOptions() error = %v, want vault-root rejection", err)
	}
}

func TestBuildRejectsDangerousOutputOverlaps(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		outputPath func(root string, vaultPath string) string
		wantError  string
	}{
		{
			name: "vault ancestor",
			outputPath: func(root string, vaultPath string) string {
				return root
			},
			wantError: "must not contain the vault",
		},
		{
			name: "notes subtree",
			outputPath: func(root string, vaultPath string) string {
				return filepath.Join(vaultPath, "notes")
			},
			wantError: "already contains unmanaged content",
		},
		{
			name: "images subtree",
			outputPath: func(root string, vaultPath string) string {
				return filepath.Join(vaultPath, "images")
			},
			wantError: "already contains unmanaged content",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			vaultPath := filepath.Join(root, "vault")
			writeBuildTestFile(t, vaultPath, "notes/alpha.md", `---
title: Alpha
---
`)
			writeBuildTestFile(t, vaultPath, "images/hero.png", "hero-image")

			_, err := buildWithOptions(testBuildSiteConfig(), vaultPath, tt.outputPath(root, vaultPath), buildOptions{
				diagnosticsWriter: io.Discard,
			})
			if err == nil {
				t.Fatal("buildWithOptions() error = nil, want dangerous output rejection")
			}
			if !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("buildWithOptions() error = %v, want substring %q", err, tt.wantError)
			}

			noteData, readErr := os.ReadFile(filepath.Join(vaultPath, "notes", "alpha.md"))
			if readErr != nil {
				t.Fatalf("os.ReadFile(notes/alpha.md) error = %v, want source note preserved", readErr)
			}
			if !bytes.Contains(noteData, []byte("title: Alpha")) {
				t.Fatalf("notes/alpha.md = %q, want preserved source note content", noteData)
			}

			imageData, readErr := os.ReadFile(filepath.Join(vaultPath, "images", "hero.png"))
			if readErr != nil {
				t.Fatalf("os.ReadFile(images/hero.png) error = %v, want source asset preserved", readErr)
			}
			if string(imageData) != "hero-image" {
				t.Fatalf("images/hero.png = %q, want preserved asset content", string(imageData))
			}
		})
	}
}

func TestBuildContinuesWhenArticleJSONLDIsIncomplete(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "notes/sparse.md", `---
title: Sparse
---
`)

	var diagnostics bytes.Buffer
	result, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{
		diagnosticsWriter: &diagnostics,
	})
	if err != nil {
		t.Fatalf("buildWithOptions() error = %v, want incomplete Article JSON-LD to degrade", err)
	}
	if result == nil {
		t.Fatal("buildWithOptions() = nil result, want build result")
	}
	if result.NotePages != 1 {
		t.Fatalf("result.NotePages = %d, want %d", result.NotePages, 1)
	}
	if len(result.Diagnostics) != 1 {
		t.Fatalf("len(result.Diagnostics) = %d, want 1 warning", len(result.Diagnostics))
	}
	if result.Diagnostics[0].Severity != diag.SeverityWarning {
		t.Fatalf("result.Diagnostics[0].Severity = %q, want %q", result.Diagnostics[0].Severity, diag.SeverityWarning)
	}
	if result.Diagnostics[0].Kind != diag.KindStructuredData {
		t.Fatalf("result.Diagnostics[0].Kind = %q, want %q", result.Diagnostics[0].Kind, diag.KindStructuredData)
	}
	if result.Diagnostics[0].Location.Path != "notes/sparse.md" {
		t.Fatalf("result.Diagnostics[0].Location.Path = %q, want %q", result.Diagnostics[0].Location.Path, "notes/sparse.md")
	}
	if !strings.Contains(result.Diagnostics[0].Message, "article JSON-LD omitted") {
		t.Fatalf("result.Diagnostics[0].Message = %q, want structured-data warning message", result.Diagnostics[0].Message)
	}

	sparseHTML := readBuildOutputFile(t, outputPath, "sparse/index.html")
	if !containsAny(sparseHTML, `<h1 class="page-title">Sparse</h1>`, `<h1 class=page-title>Sparse</h1>`) {
		t.Fatalf("sparse page missing rendered title\n%s", sparseHTML)
	}
	if !containsAny(sparseHTML, `<script type="application/ld+json">`, `<script type=application/ld+json>`) {
		t.Fatalf("sparse page missing JSON-LD script\n%s", sparseHTML)
	}
	if !bytes.Contains(sparseHTML, []byte(`"@type":"BreadcrumbList"`)) {
		t.Fatalf("sparse page JSON-LD missing breadcrumb fallback\n%s", sparseHTML)
	}
	if bytes.Contains(sparseHTML, []byte(`"@type":"Article"`)) {
		t.Fatalf("sparse page JSON-LD unexpectedly includes incomplete Article schema\n%s", sparseHTML)
	}
	if !strings.Contains(diagnostics.String(), "Warnings (1)") {
		t.Fatalf("diagnostics summary = %q, want warning summary", diagnostics.String())
	}
	if !strings.Contains(diagnostics.String(), "notes/sparse.md [structured_data]") {
		t.Fatalf("diagnostics summary = %q, want structured-data warning entry", diagnostics.String())
	}
	if strings.Contains(diagnostics.String(), "Fatal build errors") {
		t.Fatalf("diagnostics summary = %q, want no fatal build error for incomplete Article JSON-LD", diagnostics.String())
	}
}

func TestBuildAppliesRuntimeDefaultsForMinimalDirectConfig(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	writeBuildTestFile(t, vaultPath, "notes/direct-build.md", strings.Join([]string{
		"---",
		"title: Direct Build",
		"date: 2026-04-06",
		"---",
		"# Direct Build",
		"",
		"Inline math $E=mc^2$.",
		"",
		"```mermaid",
		"graph TD",
		"  A --> B",
		"```",
		"",
	}, "\n"))

	expectedCfg, err := internalconfig.Load("", internalconfig.Overrides{
		Title:   "Field Notes",
		BaseURL: "https://example.com/blog",
	})
	if err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}

	result, err := Build(model.SiteConfig{
		Title:   "Field Notes",
		BaseURL: "https://example.com/blog",
	}, vaultPath, outputPath)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if result == nil {
		t.Fatal("Build() = nil result, want build result")
	}
	if result.NotePages != 1 {
		t.Fatalf("result.NotePages = %d, want %d", result.NotePages, 1)
	}

	noteHTML := readBuildOutputFile(t, outputPath, "direct-build/index.html")
	for _, want := range []string{
		expectedCfg.KaTeXCSSURL,
		expectedCfg.KaTeXJSURL,
		expectedCfg.KaTeXAutoRenderURL,
	} {
		if !bytes.Contains(noteHTML, []byte(want)) {
			t.Fatalf("direct build page missing runtime default %q\n%s", want, noteHTML)
		}
	}
	escapedMermaidURL := strings.ReplaceAll(expectedCfg.MermaidJSURL, "/", `\/`)
	if !bytes.Contains(noteHTML, []byte(escapedMermaidURL)) {
		t.Fatalf("direct build page missing runtime default %q\n%s", escapedMermaidURL, noteHTML)
	}
	if !bytes.Contains(noteHTML, []byte("renderMathInElement")) {
		t.Fatalf("direct build page missing KaTeX runtime bootstrap\n%s", noteHTML)
	}
	if !bytes.Contains(noteHTML, []byte("import mermaid from")) {
		t.Fatalf("direct build page missing Mermaid module loader\n%s", noteHTML)
	}
}

func TestBuildRetainsPassTwoDiagnosticsWhenOneRenderFails(t *testing.T) {
	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "notes/warn.md", `---
title: Warn
date: 2026-04-06
---
# Warn
`)
	writeBuildTestFile(t, vaultPath, "notes/fail.md", `---
title: Fail
date: 2026-04-06
---
# Fail
`)

	originalRenderMarkdownNote := renderMarkdownNote
	renderMarkdownNote = func(idx *model.VaultIndex, note *model.Note, assetSink *internalasset.AssetCollector) (*renderedNote, error) {
		switch note.RelPath {
		case "notes/warn.md":
			collector := diag.NewCollector()
			collector.Warningf(diag.KindDeadLink, diag.Location{Path: note.RelPath, Line: 7}, "wikilink %q could not be resolved", "Missing")
			return &renderedNote{source: note, diag: collector}, nil
		case "notes/fail.md":
			return nil, errors.New("forced pass-2 failure")
		default:
			return originalRenderMarkdownNote(idx, note, assetSink)
		}
	}
	defer func() {
		renderMarkdownNote = originalRenderMarkdownNote
	}()

	var diagnostics bytes.Buffer
	result, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{
		concurrency:       1,
		diagnosticsWriter: &diagnostics,
	})
	if err == nil {
		t.Fatal("buildWithOptions() error = nil, want render markdown failure")
	}
	if !strings.Contains(err.Error(), "render markdown: forced pass-2 failure") {
		t.Fatalf("buildWithOptions() error = %v, want forced render markdown failure", err)
	}
	if result == nil {
		t.Fatal("buildWithOptions() = nil result, want diagnostics-bearing result")
	}
	if len(result.Diagnostics) != 1 {
		t.Fatalf("len(result.Diagnostics) = %d, want 1 preserved warning", len(result.Diagnostics))
	}
	if result.Diagnostics[0].Location.Path != "notes/warn.md" {
		t.Fatalf("result.Diagnostics[0].Location.Path = %q, want %q", result.Diagnostics[0].Location.Path, "notes/warn.md")
	}
	if result.Diagnostics[0].Kind != diag.KindDeadLink {
		t.Fatalf("result.Diagnostics[0].Kind = %q, want %q", result.Diagnostics[0].Kind, diag.KindDeadLink)
	}
	if result.WarningCount != 1 {
		t.Fatalf("result.WarningCount = %d, want %d", result.WarningCount, 1)
	}
	if !strings.Contains(diagnostics.String(), "Warnings (1)") {
		t.Fatalf("diagnostics summary = %q, want preserved warnings", diagnostics.String())
	}
	if !strings.Contains(diagnostics.String(), "notes/warn.md:7 [deadlink]") {
		t.Fatalf("diagnostics summary = %q, want preserved warning entry", diagnostics.String())
	}
	if !strings.Contains(diagnostics.String(), "Fatal build errors (1)") {
		t.Fatalf("diagnostics summary = %q, want fatal build error summary", diagnostics.String())
	}
}

func TestBuildRoutesConfiguredConcurrencyIntoPassOne(t *testing.T) {
	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "notes/alpha.md", `---
title: Alpha
date: 2026-04-06
---
# Alpha

Body.
`)

	originalBuildVaultIndex := buildVaultIndex
	seenConcurrency := 0
	buildVaultIndex = func(scanResult vault.ScanResult, frontmatterResult vault.FrontmatterResult, diagCollector *diag.Collector, concurrency int) (*model.VaultIndex, error) {
		seenConcurrency = concurrency
		return originalBuildVaultIndex(scanResult, frontmatterResult, diagCollector, concurrency)
	}
	defer func() {
		buildVaultIndex = originalBuildVaultIndex
	}()

	result, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{
		concurrency:       3,
		diagnosticsWriter: io.Discard,
	})
	if err != nil {
		t.Fatalf("buildWithOptions() error = %v", err)
	}
	if result == nil {
		t.Fatal("buildWithOptions() = nil result, want build result")
	}
	if seenConcurrency != 3 {
		t.Fatalf("pass-1 concurrency = %d, want %d", seenConcurrency, 3)
	}
}

func TestRunOrderedPipelineReturnsPartialResultsOnError(t *testing.T) {
	t.Parallel()

	got, err := runOrderedPipeline([]int{0, 1, 2}, 2, func(item int) (int, error) {
		if item == 1 {
			return 10, errors.New("boom")
		}
		return item * item, nil
	})
	if err == nil {
		t.Fatal("runOrderedPipeline() error = nil, want first error")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("runOrderedPipeline() error = %v, want boom", err)
	}
	want := []int{0, 10, 4}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runOrderedPipeline() = %#v, want %#v", got, want)
	}
}

func TestRunOrderedPipelineHonorsConcurrencyAndInputOrder(t *testing.T) {
	t.Parallel()

	items := []int{0, 1, 2, 3, 4, 5}
	started := make(chan int, len(items))
	release := make(chan struct{})
	done := make(chan struct{})

	var current atomic.Int32
	var maxSeen atomic.Int32

	var (
		got []int
		err error
	)

	go func() {
		got, err = runOrderedPipeline(items, 2, func(item int) (int, error) {
			active := current.Add(1)
			for {
				seen := maxSeen.Load()
				if active <= seen || maxSeen.CompareAndSwap(seen, active) {
					break
				}
			}
			started <- item
			<-release
			current.Add(-1)
			return item * item, nil
		})
		close(done)
	}()

	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("runOrderedPipeline() did not start work up to the configured concurrency")
		}
	}
	if maxSeen.Load() != 2 {
		t.Fatalf("max parallel workers = %d, want %d", maxSeen.Load(), 2)
	}

	close(release)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runOrderedPipeline() did not complete")
	}

	if err != nil {
		t.Fatalf("runOrderedPipeline() error = %v", err)
	}
	want := []int{0, 1, 4, 9, 16, 25}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runOrderedPipeline() = %#v, want %#v", got, want)
	}
	if maxSeen.Load() > 2 {
		t.Fatalf("max parallel workers = %d, want <= %d", maxSeen.Load(), 2)
	}
}

func testBuildSiteConfig() model.SiteConfig {
	return model.SiteConfig{
		Title:          "Field Notes",
		BaseURL:        "https://example.com/blog/",
		Author:         "Alice Example",
		Description:    "An editorial notebook.",
		Language:       "en",
		DefaultPublish: true,
	}
}

func writeBuildTestFile(t *testing.T, root string, relPath string, content string) {
	t.Helper()

	absPath := filepath.Join(root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v", filepath.Dir(absPath), err)
	}
	if err := os.WriteFile(absPath, []byte(content), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", absPath, err)
	}
}

func readBuildOutputFile(t *testing.T, outputRoot string, relPath string) []byte {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(outputRoot, filepath.FromSlash(relPath)))
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error = %v", relPath, err)
	}
	return data
}

func containsAny(data []byte, snippets ...string) bool {
	return indexAny(data, snippets...) >= 0
}

func indexAny(data []byte, snippets ...string) int {
	for _, snippet := range snippets {
		if index := bytes.Index(data, []byte(snippet)); index >= 0 {
			return index
		}
	}
	return -1
}

func sitemapLocs(t *testing.T, data []byte) []string {
	t.Helper()

	var parsed struct {
		URLs []struct {
			Loc string `xml:"loc"`
		} `xml:"url"`
	}
	if err := xml.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("xml.Unmarshal(sitemap.xml) error = %v", err)
	}

	locs := make([]string, 0, len(parsed.URLs))
	for _, url := range parsed.URLs {
		locs = append(locs, url.Loc)
	}
	return locs
}
