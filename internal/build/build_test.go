package build

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	xhtml "golang.org/x/net/html"

	internalconfig "github.com/simp-lee/obsite/internal/config"
	"github.com/simp-lee/obsite/internal/diag"
	internalmarkdown "github.com/simp-lee/obsite/internal/markdown"
	"github.com/simp-lee/obsite/internal/model"
	internalrender "github.com/simp-lee/obsite/internal/render"
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

	normalizedCfg, err := internalconfig.NormalizeSiteConfig(testBuildSiteConfig())
	if err != nil {
		t.Fatalf("config.NormalizeSiteConfig() error = %v", err)
	}
	unminifiedIndex, err := internalrender.RenderIndex(internalrender.IndexPageInput{
		Site:        normalizedCfg,
		RecentNotes: append([]model.NoteSummary(nil), result.RecentNotes...),
	})
	if err != nil {
		t.Fatalf("render.RenderIndex() error = %v", err)
	}
	if len(indexHTML) >= len(unminifiedIndex.HTML) {
		t.Fatalf("len(index.html) = %d, want minified output smaller than rendered template %d\n%s", len(indexHTML), len(unminifiedIndex.HTML), indexHTML)
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

func TestBuildAppliesTemplateDirOverridesAndStyleReplacement(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	templateDir := t.TempDir()

	writeBuildTestFile(t, vaultPath, "notes/guide.md", `---
title: Guide
date: 2026-04-06
tags:
  - Topic
---
# Guide

Rendered note body.
`)
	writeBuildTestFile(t, templateDir, "base.html", `{{define "base"}}<!doctype html><html><head><title>{{.Title}}</title></head><body data-build-custom-base="true">{{template "content" .}}</body></html>{{end}}`)
	writeBuildTestFile(t, templateDir, "tag.html", `{{define "content-tag"}}<section data-build-custom-tag>#{{.TagName}}</section>{{end}}`)
	writeBuildTestFile(t, templateDir, "style.css", `body{font-size:1rem}`)

	cfg := testBuildSiteConfig()
	cfg.TemplateDir = templateDir

	result, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{})
	if err != nil {
		t.Fatalf("buildWithOptions() error = %v", err)
	}
	if result == nil {
		t.Fatal("buildWithOptions() = nil result, want build result")
	}

	noteHTML := readBuildOutputFile(t, outputPath, "guide/index.html")
	if !containsAny(noteHTML, `data-build-custom-base="true"`, `data-build-custom-base=true`) {
		t.Fatalf("guide page missing base template override\n%s", noteHTML)
	}
	if !containsAny(noteHTML, `<article class="page-shell article-page">`) {
		t.Fatalf("guide page missing default note fallback content\n%s", noteHTML)
	}

	indexHTML := readBuildOutputFile(t, outputPath, "index.html")
	if !containsAny(indexHTML, `data-build-custom-base="true"`, `data-build-custom-base=true`) {
		t.Fatalf("index page missing base template override\n%s", indexHTML)
	}
	if !containsAny(indexHTML, `<h2 id="recent-notes-heading">Recent notes</h2>`, `<h2 id=recent-notes-heading>Recent notes</h2>`) {
		t.Fatalf("index page missing default fallback content\n%s", indexHTML)
	}

	tagHTML := readBuildOutputFile(t, outputPath, "tags/topic/index.html")
	if !containsAny(tagHTML, `data-build-custom-base="true"`, `data-build-custom-base=true`) {
		t.Fatalf("tag page missing base template override\n%s", tagHTML)
	}
	if !containsAny(tagHTML, `<section data-build-custom-tag>#topic</section>`, `<section data-build-custom-tag>#Topic</section>`) {
		t.Fatalf("tag page missing custom tag template override\n%s", tagHTML)
	}

	styleCSS := strings.TrimSpace(string(readBuildOutputFile(t, outputPath, "style.css")))
	if styleCSS != "body{font-size:1rem}" {
		t.Fatalf("style.css = %q, want %q", styleCSS, "body{font-size:1rem}")
	}
	if strings.Contains(styleCSS, "--surface") {
		t.Fatalf("style.css unexpectedly contains embedded stylesheet content: %q", styleCSS)
	}
}

func TestBuildReloadsTemplateDirOverridesWithinSameProcess(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	templateDir := t.TempDir()
	basePath := filepath.Join(templateDir, "base.html")

	writeBuildTestFile(t, vaultPath, "journal/guide.md", `---
title: Guide
date: 2026-04-06
---
# Guide

Rendered note body.
`)

	cfg := testBuildSiteConfig()
	cfg.TemplateDir = templateDir
	cfg.Timeline.Enabled = true

	writeBuildTestFile(t, templateDir, "base.html", `{{define "base"}}<!doctype html><html><body data-build-live-base="v1">{{template "content" .}}</body></html>{{end}}`)
	if _, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{}); err != nil {
		t.Fatalf("buildWithOptions() error = %v", err)
	}

	noteHTML := readBuildOutputFile(t, outputPath, "guide/index.html")
	if !containsAny(noteHTML, `data-build-live-base="v1"`, `data-build-live-base=v1`) {
		t.Fatalf("guide page missing initial base template override\n%s", noteHTML)
	}

	writeBuildTestFile(t, templateDir, "base.html", `{{define "base"}}<!doctype html><html><body data-build-live-base="v2">{{template "content" .}}</body></html>{{end}}`)
	if _, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{}); err != nil {
		t.Fatalf("buildWithOptions() error after base modification = %v", err)
	}

	notFoundHTML := readBuildOutputFile(t, outputPath, "404.html")
	if !containsAny(notFoundHTML, `data-build-live-base="v2"`, `data-build-live-base=v2`) {
		t.Fatalf("404 page missing updated base template override\n%s", notFoundHTML)
	}
	if containsAny(notFoundHTML, `data-build-live-base="v1"`, `data-build-live-base=v1`) {
		t.Fatalf("404 page still used stale base template override\n%s", notFoundHTML)
	}

	if err := os.Remove(basePath); err != nil {
		t.Fatalf("os.Remove(base.html) error = %v", err)
	}
	if _, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{}); err != nil {
		t.Fatalf("buildWithOptions() error after base deletion = %v", err)
	}

	indexHTML := readBuildOutputFile(t, outputPath, "index.html")
	if containsAny(indexHTML, `data-build-live-base="v2"`, `data-build-live-base=v2`) {
		t.Fatalf("index page still used deleted base template override\n%s", indexHTML)
	}
	if !containsAny(indexHTML, `<h2 id="recent-notes-heading">Recent notes</h2>`, `<h2 id=recent-notes-heading>Recent notes</h2>`) {
		t.Fatalf("index page did not fall back to default index template\n%s", indexHTML)
	}

	writeBuildTestFile(t, templateDir, "404.html", `{{define "content-404"}}<section data-build-live-404>{{.Title}}</section>{{end}}`)
	if _, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{}); err != nil {
		t.Fatalf("buildWithOptions() error after 404 addition = %v", err)
	}

	notFoundHTML = readBuildOutputFile(t, outputPath, "404.html")
	if !containsAny(notFoundHTML, `<section data-build-live-404>Not found</section>`) {
		t.Fatalf("404 page missing added page-specific override\n%s", notFoundHTML)
	}

	timelineHTML := readBuildOutputFile(t, outputPath, "notes/index.html")
	if !containsAny(timelineHTML, `<h2 id="timeline-notes-heading">Recent notes</h2>`, `<h2 id=timeline-notes-heading>Recent notes</h2>`) {
		t.Fatalf("timeline page did not keep default fallback after 404 override addition\n%s", timelineHTML)
	}
	if containsAny(timelineHTML, `data-build-live-404`) {
		t.Fatalf("timeline page unexpectedly used 404 override\n%s", timelineHTML)
	}
}

func TestBuildTemplateChangesInvalidateCachedArchivePages(t *testing.T) {
	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	templateDir := t.TempDir()

	writeBuildTestFile(t, vaultPath, "journal/guide.md", `---
title: Guide
date: 2026-04-06
tags:
  - topic
---
# Guide

Rendered note body.
`)

	cfg := testBuildSiteConfig()
	cfg.TemplateDir = templateDir
	cfg.Timeline.Enabled = true
	cfg.Timeline.Path = "notes"
	cfg.Search.Enabled = true
	cfg.Search.PagefindPath = "pagefind_extended"
	cfg.Search.PagefindVersion = "1.4.0"

	options := buildOptions{
		diagnosticsWriter: io.Discard,
		pagefindLookPath: func(path string) (string, error) {
			if path != "pagefind_extended" {
				t.Fatalf("pagefindLookPath() path = %q, want %q", path, "pagefind_extended")
			}
			return "/usr/local/bin/pagefind_extended", nil
		},
		pagefindCommand: func(name string, args ...string) ([]byte, error) {
			if name != "/usr/local/bin/pagefind_extended" {
				t.Fatalf("pagefindCommand() name = %q, want %q", name, "/usr/local/bin/pagefind_extended")
			}
			if len(args) == 1 && args[0] == "--version" {
				return []byte("pagefind_extended 1.4.0\n"), nil
			}
			if len(args) != 4 || args[0] != "--site" || args[2] != "--output-subdir" || args[3] != pagefindOutputSubdir {
				t.Fatalf("pagefindCommand() args = %#v, want site invocation", args)
			}

			writeMinimalPagefindBundle(t, filepath.Join(args[1], pagefindOutputSubdir))
			return []byte("Indexed 1 page\n"), nil
		},
	}

	writeBuildTestFile(t, templateDir, "base.html", `{{define "base"}}<!doctype html><html><body data-build-live-base="v1">{{template "content" .}}</body></html>{{end}}`)
	if _, err := buildWithOptions(cfg, vaultPath, outputPath, options); err != nil {
		t.Fatalf("first buildWithOptions() error = %v", err)
	}

	writeBuildTestFile(t, templateDir, "base.html", `{{define "base"}}<!doctype html><html><body data-build-live-base="v2">{{template "content" .}}</body></html>{{end}}`)

	getRenderedIndexPaths := captureRenderedIndexPagePaths(t)
	getRenderedTagPaths := captureRenderedTagPagePaths(t)
	getRenderedFolderPaths := captureRenderedFolderPagePaths(t)
	getRenderedTimelinePaths := captureRenderedTimelinePagePaths(t)

	result, err := buildWithOptions(cfg, vaultPath, outputPath, options)
	if err != nil {
		t.Fatalf("second buildWithOptions() error = %v", err)
	}
	if result.NotePages != 1 {
		t.Fatalf("result.NotePages = %d, want %d", result.NotePages, 1)
	}
	if got := getRenderedIndexPaths(); !reflect.DeepEqual(got, []string{"index.html"}) {
		t.Fatalf("rendered index pages = %#v, want %#v", got, []string{"index.html"})
	}
	if got := getRenderedTagPaths(); !reflect.DeepEqual(got, []string{"tags/topic/index.html"}) {
		t.Fatalf("rendered tag pages = %#v, want %#v", got, []string{"tags/topic/index.html"})
	}
	if got := getRenderedFolderPaths(); !reflect.DeepEqual(got, []string{"journal/index.html"}) {
		t.Fatalf("rendered folder pages = %#v, want %#v", got, []string{"journal/index.html"})
	}
	if got := getRenderedTimelinePaths(); !reflect.DeepEqual(got, []string{"notes/index.html"}) {
		t.Fatalf("rendered timeline pages = %#v, want %#v", got, []string{"notes/index.html"})
	}

	for _, relPath := range []string{"guide/index.html", "index.html", "tags/topic/index.html", "journal/index.html", "notes/index.html"} {
		html := readBuildOutputFile(t, outputPath, relPath)
		if !containsAny(html, `data-build-live-base="v2"`, `data-build-live-base=v2`) {
			t.Fatalf("%s missing updated base template override\n%s", relPath, html)
		}
		if containsAny(html, `data-build-live-base="v1"`, `data-build-live-base=v1`) {
			t.Fatalf("%s still used stale base template override\n%s", relPath, html)
		}
	}
}

func TestBuildAppliesAllowedSinglePageTemplateOverridesAndFallsBackElsewhere(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		overrideFile       string
		overrideContents   string
		targetPath         string
		wantTargetAny      []string
		fallbackPath       string
		wantFallbackAny    []string
		wantFallbackAbsent []string
	}{
		{
			name:               "index template override",
			overrideFile:       "index.html",
			overrideContents:   `{{define "content-index"}}<section data-build-custom-index>{{.Title}}</section>{{end}}`,
			targetPath:         "index.html",
			wantTargetAny:      []string{`data-build-custom-index`},
			fallbackPath:       "404.html",
			wantFallbackAny:    []string{`Return to the homepage`},
			wantFallbackAbsent: []string{`data-build-custom-index`},
		},
		{
			name:               "404 template override",
			overrideFile:       "404.html",
			overrideContents:   `{{define "content-404"}}<section data-build-custom-404>{{.Title}}</section>{{end}}`,
			targetPath:         "404.html",
			wantTargetAny:      []string{`data-build-custom-404`},
			fallbackPath:       "index.html",
			wantFallbackAny:    []string{`<h2 id="recent-notes-heading">Recent notes</h2>`, `<h2 id=recent-notes-heading>Recent notes</h2>`},
			wantFallbackAbsent: []string{`data-build-custom-404`},
		},
		{
			name:               "folder template override",
			overrideFile:       "folder.html",
			overrideContents:   `{{define "content-folder"}}<section data-build-custom-folder>{{.FolderPath}}</section>{{end}}`,
			targetPath:         "journal/index.html",
			wantTargetAny:      []string{`data-build-custom-folder`},
			fallbackPath:       "notes/index.html",
			wantFallbackAny:    []string{`<h2 id="timeline-notes-heading">Recent notes</h2>`, `<h2 id=timeline-notes-heading>Recent notes</h2>`},
			wantFallbackAbsent: []string{`data-build-custom-folder`},
		},
		{
			name:               "timeline template override",
			overrideFile:       "timeline.html",
			overrideContents:   `{{define "content-timeline"}}<section data-build-custom-timeline>{{.Title}}</section>{{end}}`,
			targetPath:         "notes/index.html",
			wantTargetAny:      []string{`data-build-custom-timeline`},
			fallbackPath:       "journal/index.html",
			wantFallbackAny:    []string{`<h2 id="folder-notes-heading">Notes</h2>`, `<h2 id=folder-notes-heading>Notes</h2>`},
			wantFallbackAbsent: []string{`data-build-custom-timeline`},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			vaultPath := t.TempDir()
			outputPath := filepath.Join(t.TempDir(), "site")
			templateDir := t.TempDir()

			writeBuildTestFile(t, vaultPath, "journal/guide.md", `---
title: Guide
date: 2026-04-06
---
# Guide

Rendered note body.
`)
			writeBuildTestFile(t, templateDir, tt.overrideFile, tt.overrideContents)

			cfg := testBuildSiteConfig()
			cfg.TemplateDir = templateDir
			cfg.Timeline.Enabled = true

			result, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{})
			if err != nil {
				t.Fatalf("buildWithOptions() error = %v", err)
			}
			if result == nil {
				t.Fatal("buildWithOptions() = nil result, want build result")
			}

			targetHTML := readBuildOutputFile(t, outputPath, tt.targetPath)
			if !containsAny(targetHTML, tt.wantTargetAny...) {
				t.Fatalf("%s missing target override evidence\n%s", tt.targetPath, targetHTML)
			}

			fallbackHTML := readBuildOutputFile(t, outputPath, tt.fallbackPath)
			if !containsAny(fallbackHTML, tt.wantFallbackAny...) {
				t.Fatalf("%s missing fallback default content\n%s", tt.fallbackPath, fallbackHTML)
			}
			if containsAny(fallbackHTML, tt.wantFallbackAbsent...) {
				t.Fatalf("%s unexpectedly used %s override\n%s", tt.fallbackPath, tt.overrideFile, fallbackHTML)
			}
		})
	}
}

func TestPaginateHandlesBoundaries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		items    []int
		pageSize int
		want     [][]int
	}{
		{name: "empty", pageSize: 2, want: [][]int{nil}},
		{name: "single item", items: []int{1}, pageSize: 2, want: [][]int{{1}}},
		{name: "exact threshold", items: []int{1, 2}, pageSize: 2, want: [][]int{{1, 2}}},
		{name: "threshold plus one", items: []int{1, 2, 3}, pageSize: 2, want: [][]int{{1, 2}, {3}}},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := paginate(tt.items, tt.pageSize); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("paginate(%v, %d) = %#v, want %#v", tt.items, tt.pageSize, got, tt.want)
			}
		})
	}
}

func TestBuildRunsPagefindWhenSearchEnabledAndPublishesBundle(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "notes/alpha.md", `---
title: Alpha
date: 2026-04-06
tags:
  - Topic
---
# Alpha

Searchable note body.
`)

	cfg := testBuildSiteConfig()
	cfg.Search.Enabled = true
	cfg.Search.PagefindPath = "pagefind_extended"
	cfg.Search.PagefindVersion = "1.4.0"

	var pagefindCalls [][]string
	result, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{
		pagefindLookPath: func(path string) (string, error) {
			if path != "pagefind_extended" {
				t.Fatalf("pagefindLookPath() path = %q, want %q", path, "pagefind_extended")
			}
			return "/usr/local/bin/pagefind_extended", nil
		},
		pagefindCommand: func(name string, args ...string) ([]byte, error) {
			if name != "/usr/local/bin/pagefind_extended" {
				t.Fatalf("pagefindCommand() name = %q, want %q", name, "/usr/local/bin/pagefind_extended")
			}
			pagefindCalls = append(pagefindCalls, append([]string{name}, args...))
			if len(args) == 1 && args[0] == "--version" {
				return []byte("pagefind_extended 1.4.0\n"), nil
			}
			if len(args) != 4 || args[0] != "--site" || args[2] != "--output-subdir" || args[3] != pagefindOutputSubdir {
				t.Fatalf("pagefindCommand() args = %#v, want [--site <path> --output-subdir %s]", args, pagefindOutputSubdir)
			}

			sitePath := args[1]
			bundlePath := filepath.Join(sitePath, pagefindOutputSubdir)
			writeMinimalPagefindBundle(t, bundlePath)

			return []byte("Indexed 1 page\n"), nil
		},
	})
	if err != nil {
		t.Fatalf("buildWithOptions() error = %v", err)
	}
	if result == nil {
		t.Fatal("buildWithOptions() = nil result, want build result")
	}
	if len(pagefindCalls) != 2 {
		t.Fatalf("len(pagefindCalls) = %d, want %d", len(pagefindCalls), 2)
	}

	if got := string(readBuildOutputFile(t, outputPath, "_pagefind/pagefind-ui.css")); got != ".pagefind-ui{display:block}" {
		t.Fatalf("pagefind-ui.css = %q, want %q", got, ".pagefind-ui{display:block}")
	}
	if got := string(readBuildOutputFile(t, outputPath, "_pagefind/pagefind-ui.js")); got != "window.PagefindUI=function(){};" {
		t.Fatalf("pagefind-ui.js = %q, want %q", got, "window.PagefindUI=function(){};")
	}
	if got := string(readBuildOutputFile(t, outputPath, "_pagefind/pagefind-entry.json")); got != `{"version":"1.4.0","languages":{"en":{"hash":"en-test","page_count":1}}}` {
		t.Fatalf("pagefind-entry.json = %q, want minimal entry manifest", got)
	}
	if got := string(readBuildOutputFile(t, outputPath, "_pagefind/pagefind.js")); got != "window.__pagefind=function(){};" {
		t.Fatalf("pagefind.js = %q, want %q", got, "window.__pagefind=function(){};")
	}
	if got := string(readBuildOutputFile(t, outputPath, "_pagefind/pagefind-worker.js")); got != "self.onmessage=function(){};" {
		t.Fatalf("pagefind-worker.js = %q, want %q", got, "self.onmessage=function(){};")
	}
	if got := string(readBuildOutputFile(t, outputPath, "_pagefind/pagefind.en-test.pf_meta")); got != "meta" {
		t.Fatalf("pagefind.en-test.pf_meta = %q, want %q", got, "meta")
	}
	if got := string(readBuildOutputFile(t, outputPath, "_pagefind/index/en-test.pf_index")); got != "index" {
		t.Fatalf("index/en-test.pf_index = %q, want %q", got, "index")
	}
	if got := string(readBuildOutputFile(t, outputPath, "_pagefind/fragment/en-test.pf_fragment")); got != "fragment" {
		t.Fatalf("fragment/en-test.pf_fragment = %q, want %q", got, "fragment")
	}
	if got := string(readBuildOutputFile(t, outputPath, "_pagefind/wasm.unknown.pagefind")); got != "wasm" {
		t.Fatalf("wasm.unknown.pagefind = %q, want %q", got, "wasm")
	}

	indexHTML := readBuildOutputFile(t, outputPath, "index.html")
	if !containsAny(indexHTML, `href="./_pagefind/pagefind-ui.css"`, `href=./_pagefind/pagefind-ui.css`) {
		t.Fatalf("index page missing root-relative Pagefind stylesheet\n%s", indexHTML)
	}
	if !containsAny(indexHTML, `src="./_pagefind/pagefind-ui.js"`, `src=./_pagefind/pagefind-ui.js`) {
		t.Fatalf("index page missing root-relative Pagefind script\n%s", indexHTML)
	}
	if !containsAny(indexHTML, `data-obsite-search-ui`) {
		t.Fatalf("index page missing explicit framework search marker\n%s", indexHTML)
	}
	if !containsAny(indexHTML, `<div id="search"></div>`, `<div id=search></div>`) {
		t.Fatalf("index page missing search container\n%s", indexHTML)
	}
	if !containsAny(indexHTML, `new PagefindUI({ element: "#search" });`, `new PagefindUI({element:"#search"})`) {
		t.Fatalf("index page missing requirement-shaped PagefindUI initializer\n%s", indexHTML)
	}
	if containsAny(indexHTML, `showSubResults`) {
		t.Fatalf("index page unexpectedly expands PagefindUI options\n%s", indexHTML)
	}

	tagHTML := readBuildOutputFile(t, outputPath, "tags/topic/index.html")
	if !containsAny(tagHTML, `href="../../_pagefind/pagefind-ui.css"`, `href=../../_pagefind/pagefind-ui.css`) {
		t.Fatalf("tag page missing SiteRootRel-based Pagefind stylesheet\n%s", tagHTML)
	}
	if !containsAny(tagHTML, `src="../../_pagefind/pagefind-ui.js"`, `src=../../_pagefind/pagefind-ui.js`) {
		t.Fatalf("tag page missing SiteRootRel-based Pagefind script\n%s", tagHTML)
	}
	if !containsAny(tagHTML, `data-obsite-search-ui`) {
		t.Fatalf("tag page missing explicit framework search marker\n%s", tagHTML)
	}
}

func TestBuildFailsWhenPagefindOutputOnlyContainsUIAssets(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	writeBuildTestFile(t, vaultPath, "notes/alpha.md", `---
title: Alpha
date: 2026-04-06
---
# Alpha

Searchable note body.
`)

	cfg := testBuildSiteConfig()
	cfg.Search.Enabled = true
	cfg.Search.PagefindPath = "pagefind_extended"
	cfg.Search.PagefindVersion = "1.4.0"

	var diagnostics bytes.Buffer
	_, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{
		diagnosticsWriter: &diagnostics,
		pagefindLookPath: func(path string) (string, error) {
			return "/usr/local/bin/pagefind_extended", nil
		},
		pagefindCommand: func(name string, args ...string) ([]byte, error) {
			if len(args) == 1 && args[0] == "--version" {
				return []byte("pagefind_extended 1.4.0\n"), nil
			}
			if len(args) != 4 || args[0] != "--site" || args[2] != "--output-subdir" || args[3] != pagefindOutputSubdir {
				t.Fatalf("pagefindCommand() args = %#v, want site invocation", args)
			}

			bundlePath := filepath.Join(args[1], pagefindOutputSubdir)
			if err := os.MkdirAll(bundlePath, 0o755); err != nil {
				t.Fatalf("os.MkdirAll(%q) error = %v", bundlePath, err)
			}
			if err := os.WriteFile(filepath.Join(bundlePath, "pagefind-ui.css"), []byte(".pagefind-ui{display:block}"), 0o644); err != nil {
				t.Fatalf("os.WriteFile(pagefind-ui.css) error = %v", err)
			}
			if err := os.WriteFile(filepath.Join(bundlePath, "pagefind-ui.js"), []byte("window.PagefindUI=function(){};"), 0o644); err != nil {
				t.Fatalf("os.WriteFile(pagefind-ui.js) error = %v", err)
			}

			return []byte("Indexed 1 page\n"), nil
		},
	})
	if err == nil {
		t.Fatal("buildWithOptions() error = nil, want incomplete Pagefind bundle failure")
	}
	if !strings.Contains(err.Error(), `pagefind indexing did not produce "_pagefind/pagefind-entry.json"`) {
		t.Fatalf("buildWithOptions() error = %v, want missing core Pagefind bundle diagnostic", err)
	}
	if !strings.Contains(diagnostics.String(), `build: build search index: pagefind indexing did not produce "_pagefind/pagefind-entry.json"`) {
		t.Fatalf("diagnostics summary missing incomplete bundle failure\n%s", diagnostics.String())
	}
	if _, statErr := os.Stat(outputPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("os.Stat(%q) error = %v, want rolled-back output", outputPath, statErr)
	}
}

func TestBuildFailsWhenPagefindBinaryIsMissing(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	writeBuildTestFile(t, vaultPath, "notes/alpha.md", `---
title: Alpha
date: 2026-04-06
---
# Alpha

Searchable note body.
`)

	cfg := testBuildSiteConfig()
	cfg.Search.Enabled = true
	cfg.Search.PagefindPath = "missing-pagefind"
	cfg.Search.PagefindVersion = "1.4.0"

	var diagnostics bytes.Buffer
	result, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{
		diagnosticsWriter: &diagnostics,
		pagefindLookPath: func(path string) (string, error) {
			return "", errors.New("executable file not found")
		},
	})
	if err == nil {
		t.Fatal("buildWithOptions() error = nil, want missing Pagefind binary failure")
	}
	if result == nil {
		t.Fatal("buildWithOptions() = nil result, want diagnostics-bearing result")
	}
	if !strings.Contains(err.Error(), `pagefind binary "missing-pagefind" not found`) {
		t.Fatalf("buildWithOptions() error = %v, want missing-binary diagnostic", err)
	}
	if !strings.Contains(diagnostics.String(), `build: build search index: pagefind binary "missing-pagefind" not found`) {
		t.Fatalf("diagnostics summary missing missing-binary error\n%s", diagnostics.String())
	}
	if _, statErr := os.Stat(outputPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("os.Stat(%q) error = %v, want rolled-back output", outputPath, statErr)
	}
}

func TestBuildFailsWhenPagefindVersionMismatches(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	writeBuildTestFile(t, vaultPath, "notes/alpha.md", `---
title: Alpha
date: 2026-04-06
---
# Alpha

Searchable note body.
`)

	cfg := testBuildSiteConfig()
	cfg.Search.Enabled = true
	cfg.Search.PagefindPath = "pagefind_extended"
	cfg.Search.PagefindVersion = "1.4.0"

	var diagnostics bytes.Buffer
	_, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{
		diagnosticsWriter: &diagnostics,
		pagefindLookPath: func(path string) (string, error) {
			return "/usr/local/bin/pagefind_extended", nil
		},
		pagefindCommand: func(name string, args ...string) ([]byte, error) {
			if len(args) == 1 && args[0] == "--version" {
				return []byte("pagefind_extended 0.9.9\n"), nil
			}
			t.Fatalf("pagefindCommand() unexpectedly ran indexing with args %#v", args)
			return nil, nil
		},
	})
	if err == nil {
		t.Fatal("buildWithOptions() error = nil, want version mismatch failure")
	}
	if !strings.Contains(err.Error(), `reported version "0.9.9"; want "1.4.0"`) {
		t.Fatalf("buildWithOptions() error = %v, want version mismatch diagnostic", err)
	}
	if !strings.Contains(diagnostics.String(), `build: build search index: pagefind binary "/usr/local/bin/pagefind_extended" reported version "0.9.9"; want "1.4.0"`) {
		t.Fatalf("diagnostics summary missing version mismatch\n%s", diagnostics.String())
	}
}

func TestBuildFailsWhenPagefindIndexingFails(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	writeBuildTestFile(t, vaultPath, "notes/alpha.md", `---
title: Alpha
date: 2026-04-06
---
# Alpha

Searchable note body.
`)

	cfg := testBuildSiteConfig()
	cfg.Search.Enabled = true
	cfg.Search.PagefindPath = "pagefind_extended"
	cfg.Search.PagefindVersion = "1.4.0"

	var diagnostics bytes.Buffer
	var stagedIndexHTML []byte
	_, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{
		diagnosticsWriter: &diagnostics,
		pagefindLookPath: func(path string) (string, error) {
			return "/usr/local/bin/pagefind_extended", nil
		},
		pagefindCommand: func(name string, args ...string) ([]byte, error) {
			if len(args) == 1 && args[0] == "--version" {
				return []byte("pagefind_extended 1.4.0\n"), nil
			}
			if len(args) != 4 || args[0] != "--site" || args[2] != "--output-subdir" || args[3] != pagefindOutputSubdir {
				t.Fatalf("pagefindCommand() args = %#v, want site invocation", args)
			}
			stagedIndexHTML = readBuildOutputFile(t, args[1], "index.html")
			if containsFrameworkSearchUI(stagedIndexHTML) {
				t.Fatalf("staged index unexpectedly exposes search before Pagefind succeeds\n%s", stagedIndexHTML)
			}
			return []byte("indexing exploded\n"), errors.New("exit status 1")
		},
	})
	if err == nil {
		t.Fatal("buildWithOptions() error = nil, want Pagefind indexing failure")
	}
	if len(stagedIndexHTML) == 0 {
		t.Fatal("pagefindCommand() did not inspect staged index HTML")
	}
	if !strings.Contains(err.Error(), `pagefind indexing failed`) || !strings.Contains(err.Error(), `indexing exploded`) {
		t.Fatalf("buildWithOptions() error = %v, want indexing failure diagnostics", err)
	}
	if !strings.Contains(diagnostics.String(), `build: build search index: pagefind indexing failed`) || !strings.Contains(diagnostics.String(), `indexing exploded`) {
		t.Fatalf("diagnostics summary missing indexing failure\n%s", diagnostics.String())
	}
}

func TestPaginatedListPageRelPathKeepsPageOneStable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		baseRel    string
		page       int
		wantRel    string
		wantURL    string
		currentRel string
	}{
		{name: "root page one", baseRel: "index.html", page: 1, wantRel: "index.html", wantURL: "./", currentRel: "index.html"},
		{name: "root page two", baseRel: "index.html", page: 2, wantRel: "page/2/index.html", wantURL: "page/2/", currentRel: "index.html"},
		{name: "tag page three", baseRel: "tags/topic/index.html", page: 3, wantRel: "tags/topic/page/3/index.html", wantURL: "../3/", currentRel: "tags/topic/page/2/index.html"},
		{name: "folder page two", baseRel: "alpha/beta/index.html", page: 2, wantRel: "alpha/beta/page/2/index.html", wantURL: "../2/", currentRel: "alpha/beta/page/3/index.html"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := paginatedListPageRelPath(tt.baseRel, tt.page); got != tt.wantRel {
				t.Fatalf("paginatedListPageRelPath(%q, %d) = %q, want %q", tt.baseRel, tt.page, got, tt.wantRel)
			}
			if got := paginationPageURL(tt.currentRel, tt.baseRel, tt.page); got != tt.wantURL {
				t.Fatalf("paginationPageURL(%q, %q, %d) = %q, want %q", tt.currentRel, tt.baseRel, tt.page, got, tt.wantURL)
			}
		})
	}
}

func TestNormalizePaginationGeneratedHrefsKeepsRootPageOneStable(t *testing.T) {
	t.Parallel()

	input := []byte(`<link rel=prev href=../.././><a class="pagination-link pagination-link-prev" href=../.././ rel=prev>Previous</a><a class=pagination-page href=../.././>1</a><code>href=../.././</code>`)
	got := string(normalizePaginationGeneratedHrefs(input))

	for _, want := range []string{
		`<link rel=prev href=../../>`,
		`<a class="pagination-link pagination-link-prev" href=../../ rel=prev>Previous</a>`,
		`<a class=pagination-page href=../../>1</a>`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("normalizePaginationGeneratedHrefs() = %q, want substring %q", got, want)
		}
	}
	if !strings.Contains(got, `<code>href=../.././</code>`) {
		t.Fatalf("normalizePaginationGeneratedHrefs() unexpectedly rewrote non-pagination HTML\n%s", got)
	}
}

func TestBuildPaginatesListPagesAndEmitsPrevNextLinks(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "alpha/one.md", `---
title: One
date: 2026-04-03
tags:
  - Topic
---
# One

First note.
`)
	writeBuildTestFile(t, vaultPath, "alpha/two.md", `---
title: Two
date: 2026-04-04
tags:
  - Topic
---
# Two

Second note.
`)
	writeBuildTestFile(t, vaultPath, "alpha/three.md", `---
title: Three
date: 2026-04-05
tags:
  - Topic
---
# Three

Third note.
`)

	cfg := testBuildSiteConfig()
	cfg.Pagination.PageSize = 2
	cfg.Timeline.Enabled = true

	result, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{})
	if err != nil {
		t.Fatalf("buildWithOptions() error = %v", err)
	}
	if result == nil {
		t.Fatal("buildWithOptions() = nil result, want build result")
	}

	for _, relPath := range []string{
		"index.html",
		"page/2/index.html",
		"tags/topic/index.html",
		"tags/topic/page/2/index.html",
		"alpha/index.html",
		"alpha/page/2/index.html",
		"notes/index.html",
		"notes/page/2/index.html",
	} {
		if _, err := os.Stat(filepath.Join(outputPath, filepath.FromSlash(relPath))); err != nil {
			t.Fatalf("os.Stat(%q) error = %v, want generated page", relPath, err)
		}
	}

	for _, relPath := range []string{
		"page/1/index.html",
		"tags/topic/page/1/index.html",
		"alpha/page/1/index.html",
		"notes/page/1/index.html",
	} {
		if _, err := os.Stat(filepath.Join(outputPath, filepath.FromSlash(relPath))); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("os.Stat(%q) error = %v, want page 1 to stay at its original URL", relPath, err)
		}
	}

	indexHTML := readBuildOutputFile(t, outputPath, "index.html")
	if !containsAny(indexHTML, `href="three/">Three</a>`, `href=three/>Three</a>`) || !containsAny(indexHTML, `href="two/">Two</a>`, `href=two/>Two</a>`) {
		t.Fatalf("index page missing first two notes after pagination split\n%s", indexHTML)
	}
	if containsAny(indexHTML, `href="one/">One</a>`, `href=one/>One</a>`) {
		t.Fatalf("index page unexpectedly contains page-2 note\n%s", indexHTML)
	}
	if !containsAny(indexHTML, `<link rel="next" href="page/2/">`, `<link rel=next href=page/2/>`) {
		t.Fatalf("index page missing head next link\n%s", indexHTML)
	}
	if !containsAny(indexHTML, `href="page/2/" rel="next">Next</a>`, `href=page/2/ rel=next>Next</a>`) {
		t.Fatalf("index page missing body next navigation\n%s", indexHTML)
	}

	indexPageTwoHTML := readBuildOutputFile(t, outputPath, "page/2/index.html")
	if !containsAny(indexPageTwoHTML, `href="../../">One</a>`, `href=../../>One</a>`, `href="../../one/">One</a>`, `href=../../one/>One</a>`) {
		t.Fatalf("index page 2 missing final note\n%s", indexPageTwoHTML)
	}
	if !containsAny(indexPageTwoHTML, `<link rel="prev" href="../../">`, `<link rel=prev href=../../>`) {
		t.Fatalf("index page 2 missing head prev link\n%s", indexPageTwoHTML)
	}
	if containsAny(indexPageTwoHTML, `href="../.././"`, `href=../.././`) {
		t.Fatalf("index page 2 unexpectedly rewrote page-1 URL to ../.././\n%s", indexPageTwoHTML)
	}
	if !containsAny(indexPageTwoHTML, `href="../../" rel="prev">Previous</a>`, `href=../../ rel=prev>Previous</a>`) {
		t.Fatalf("index page 2 missing body prev navigation\n%s", indexPageTwoHTML)
	}
	if !containsAny(indexPageTwoHTML, `<link rel="canonical" href="https://example.com/blog/page/2/">`, `<link rel=canonical href=https://example.com/blog/page/2/>`) {
		t.Fatalf("index page 2 missing canonical link\n%s", indexPageTwoHTML)
	}

	tagPageTwoHTML := readBuildOutputFile(t, outputPath, "tags/topic/page/2/index.html")
	if !containsAny(tagPageTwoHTML, `<link rel="prev" href="../../">`, `<link rel=prev href=../../>`) {
		t.Fatalf("tag page 2 missing head prev link\n%s", tagPageTwoHTML)
	}
	if !containsAny(tagPageTwoHTML, `<link rel="canonical" href="https://example.com/blog/tags/topic/page/2/">`, `<link rel=canonical href=https://example.com/blog/tags/topic/page/2/>`) {
		t.Fatalf("tag page 2 missing canonical link\n%s", tagPageTwoHTML)
	}

	folderPageTwoHTML := readBuildOutputFile(t, outputPath, "alpha/page/2/index.html")
	if !containsAny(folderPageTwoHTML, `<link rel="prev" href="../../">`, `<link rel=prev href=../../>`) {
		t.Fatalf("folder page 2 missing head prev link\n%s", folderPageTwoHTML)
	}
	if !containsAny(folderPageTwoHTML, `<link rel="canonical" href="https://example.com/blog/alpha/page/2/">`, `<link rel=canonical href=https://example.com/blog/alpha/page/2/>`) {
		t.Fatalf("folder page 2 missing canonical link\n%s", folderPageTwoHTML)
	}

	timelinePageTwoHTML := readBuildOutputFile(t, outputPath, "notes/page/2/index.html")
	if !containsAny(timelinePageTwoHTML, `<link rel="prev" href="../../">`, `<link rel=prev href=../../>`) {
		t.Fatalf("timeline page 2 missing head prev link\n%s", timelinePageTwoHTML)
	}
	if !containsAny(timelinePageTwoHTML, `<link rel="canonical" href="https://example.com/blog/notes/page/2/">`, `<link rel=canonical href=https://example.com/blog/notes/page/2/>`) {
		t.Fatalf("timeline page 2 missing canonical link\n%s", timelinePageTwoHTML)
	}

	sitemapXML := readBuildOutputFile(t, outputPath, "sitemap.xml")
	for _, want := range []string{
		"https://example.com/blog/page/2/",
		"https://example.com/blog/tags/topic/page/2/",
		"https://example.com/blog/alpha/page/2/",
		"https://example.com/blog/notes/page/2/",
	} {
		if !bytes.Contains(sitemapXML, []byte(want)) {
			t.Fatalf("sitemap.xml missing paginated URL %q\n%s", want, sitemapXML)
		}
	}
}

func TestBuildEmitsFolderPagesForPublishedNoteDirectories(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "alpha/one.md", `---
title: One
date: 2026-04-06
tags:
  - Topic
---
# One

First folder note.
`)
	writeBuildTestFile(t, vaultPath, "alpha/beta/two.md", `---
title: Two
date: 2026-04-07
tags:
  - Topic/Sub
---
# Two

Nested folder note.
`)
	writeBuildTestFile(t, vaultPath, "root.md", `---
title: Root
date: 2026-04-05
---
# Root

Root note.
`)

	result, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{})
	if err != nil {
		t.Fatalf("buildWithOptions() error = %v", err)
	}
	if result == nil {
		t.Fatal("buildWithOptions() = nil result, want build result")
	}

	alphaFolderHTML := readBuildOutputFile(t, outputPath, "alpha/index.html")
	if !containsAny(alphaFolderHTML, `<h1 class="page-title">alpha</h1>`, `<h1 class=page-title>alpha</h1>`) {
		t.Fatalf("alpha folder page missing folder title\n%s", alphaFolderHTML)
	}
	oneIndex := indexAny(alphaFolderHTML, `href="../one/">One</a>`, `href=../one/>One</a>`)
	twoIndex := indexAny(alphaFolderHTML, `href="../two/">Two</a>`, `href=../two/>Two</a>`)
	if oneIndex == -1 || twoIndex == -1 {
		t.Fatalf("alpha folder page missing folder note links\n%s", alphaFolderHTML)
	}
	if twoIndex > oneIndex {
		t.Fatalf("alpha folder page note ordering is not deterministic by recency\n%s", alphaFolderHTML)
	}
	if !containsAny(alphaFolderHTML, `href="../tags/topic/">#topic</a>`, `href=../tags/topic/>#topic</a>`) {
		t.Fatalf("alpha folder page missing tag link for direct child note\n%s", alphaFolderHTML)
	}
	if !containsAny(alphaFolderHTML, `href="../tags/topic/sub/">#topic/sub</a>`, `href=../tags/topic/sub/>#topic/sub</a>`) {
		t.Fatalf("alpha folder page missing nested note tag link\n%s", alphaFolderHTML)
	}

	betaFolderHTML := readBuildOutputFile(t, outputPath, "alpha/beta/index.html")
	if !containsAny(betaFolderHTML, `<h1 class="page-title">beta</h1>`, `<h1 class=page-title>beta</h1>`) {
		t.Fatalf("nested folder page missing folder title\n%s", betaFolderHTML)
	}
	if !containsAny(betaFolderHTML, `href="../../two/">Two</a>`, `href=../../two/>Two</a>`) {
		t.Fatalf("nested folder page missing note link\n%s", betaFolderHTML)
	}
	if containsAny(betaFolderHTML, `href="../../one/">One</a>`, `href=../../one/>One</a>`) {
		t.Fatalf("nested folder page unexpectedly lists ancestor sibling notes\n%s", betaFolderHTML)
	}

	sitemapXML := readBuildOutputFile(t, outputPath, "sitemap.xml")
	for _, want := range []string{
		"https://example.com/blog/alpha/",
		"https://example.com/blog/alpha/beta/",
	} {
		if !bytes.Contains(sitemapXML, []byte(want)) {
			t.Fatalf("sitemap.xml missing %q\n%s", want, sitemapXML)
		}
	}
}

func TestBuildRejectsFolderPathConflictsWithNoteSlug(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "alpha.md", `---
title: Alpha
date: 2026-04-06
---
# Alpha

Conflicting root note.
`)
	writeBuildTestFile(t, vaultPath, "alpha/nested.md", `---
title: Nested
date: 2026-04-07
---
# Nested

Nested folder note.
`)

	var diagnostics bytes.Buffer
	result, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: &diagnostics})
	if err == nil {
		t.Fatal("buildWithOptions() error = nil, want folder-vs-note slug conflict failure")
	}
	if !strings.Contains(err.Error(), `slug conflict for "alpha"`) {
		t.Fatalf("buildWithOptions() error = %v, want slug conflict for folder path and note slug", err)
	}
	if result == nil {
		t.Fatal("buildWithOptions() = nil result, want diagnostics-bearing result")
	}
	if result.ErrorCount != 2 {
		t.Fatalf("result.ErrorCount = %d, want %d", result.ErrorCount, 2)
	}
	if len(result.Diagnostics) != 2 {
		t.Fatalf("len(result.Diagnostics) = %d, want %d", len(result.Diagnostics), 2)
	}
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Kind != diag.KindSlugConflict {
			t.Fatalf("diagnostic.Kind = %q, want %q", diagnostic.Kind, diag.KindSlugConflict)
		}
	}

	summary := diagnostics.String()
	for _, want := range []string{
		`alpha.md [slug_conflict] slug "alpha" conflicts with alpha.md, alpha/`,
		`alpha/ [slug_conflict] slug "alpha" conflicts with alpha.md, alpha/`,
		`build: build folder pages: slug conflict for "alpha" across alpha.md, alpha/`,
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("diagnostics summary missing %q\n%s", want, summary)
		}
	}
}

func TestBuildRejectsFolderPathConflictsWithTagPage(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "tags/topic/one.md", `---
title: Tagged Folder Note
date: 2026-04-07
tags:
  - Topic
---
# Tagged Folder Note

Folder content that would collide with the generated tag page.
`)

	var diagnostics bytes.Buffer
	result, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: &diagnostics})
	if err == nil {
		t.Fatal("buildWithOptions() error = nil, want folder-vs-tag page conflict failure")
	}
	if !strings.Contains(err.Error(), `slug conflict for "tags/topic"`) {
		t.Fatalf("buildWithOptions() error = %v, want slug conflict for folder path and tag page", err)
	}
	if result == nil {
		t.Fatal("buildWithOptions() = nil result, want diagnostics-bearing result")
	}
	if result.ErrorCount != 2 {
		t.Fatalf("result.ErrorCount = %d, want %d", result.ErrorCount, 2)
	}
	if len(result.Diagnostics) != 2 {
		t.Fatalf("len(result.Diagnostics) = %d, want %d", len(result.Diagnostics), 2)
	}
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Kind != diag.KindSlugConflict {
			t.Fatalf("diagnostic.Kind = %q, want %q", diagnostic.Kind, diag.KindSlugConflict)
		}
	}

	summary := diagnostics.String()
	for _, want := range []string{
		`tag page tags/topic/ [slug_conflict]`,
		`tags/topic/ [slug_conflict]`,
		`build: build folder pages: slug conflict for "tags/topic"`,
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("diagnostics summary missing %q\n%s", want, summary)
		}
	}
	if _, statErr := os.Stat(filepath.Join(outputPath, "tags", "topic", "index.html")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("os.Stat(tags/topic/index.html) error = %v, want not-exist after rejected collision", statErr)
	}
}

func TestBuildRejectsFolderPathConflictsWithCaseInsensitiveTagPage(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "Tags/Topic/one.md", `---
title: Tagged Folder Note
date: 2026-04-07
tags:
  - Topic
---
# Tagged Folder Note

Folder content that would collide with the generated tag page by case only.
`)

	var diagnostics bytes.Buffer
	result, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: &diagnostics})
	if err == nil {
		t.Fatal("buildWithOptions() error = nil, want case-insensitive folder-vs-tag page conflict failure")
	}
	if !strings.Contains(err.Error(), `slug conflict for "tags/topic"`) {
		t.Fatalf("buildWithOptions() error = %v, want slug conflict for mixed-case folder path and tag page", err)
	}
	if result == nil {
		t.Fatal("buildWithOptions() = nil result, want diagnostics-bearing result")
	}
	if result.ErrorCount != 2 {
		t.Fatalf("result.ErrorCount = %d, want %d", result.ErrorCount, 2)
	}
	if len(result.Diagnostics) != 2 {
		t.Fatalf("len(result.Diagnostics) = %d, want %d", len(result.Diagnostics), 2)
	}
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Kind != diag.KindSlugConflict {
			t.Fatalf("diagnostic.Kind = %q, want %q", diagnostic.Kind, diag.KindSlugConflict)
		}
	}

	summary := diagnostics.String()
	for _, want := range []string{
		`tag page tags/topic/ [slug_conflict]`,
		`Tags/Topic/ [slug_conflict]`,
		`build: build folder pages: slug conflict for "tags/topic"`,
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("diagnostics summary missing %q\n%s", want, summary)
		}
	}
	if _, statErr := os.Stat(filepath.Join(outputPath, "Tags", "Topic", "index.html")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("os.Stat(Tags/Topic/index.html) error = %v, want not-exist after rejected collision", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(outputPath, "tags", "topic", "index.html")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("os.Stat(tags/topic/index.html) error = %v, want not-exist after rejected collision", statErr)
	}
}

func TestBuildRejectsFolderPathConflictsWithCaseInsensitiveNoteSlug(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "alpha.md", `---
title: Alpha
date: 2026-04-06
---
# Alpha

Root note.
`)
	writeBuildTestFile(t, vaultPath, "Alpha/nested.md", `---
title: Nested
date: 2026-04-07
---
# Nested

Nested note in a case-only conflicting folder.
`)

	var diagnostics bytes.Buffer
	result, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: &diagnostics})
	if err == nil {
		t.Fatal("buildWithOptions() error = nil, want case-insensitive folder-vs-note slug conflict failure")
	}
	if !strings.Contains(err.Error(), `slug conflict for "alpha"`) {
		t.Fatalf("buildWithOptions() error = %v, want slug conflict for case-only folder path and note slug", err)
	}
	if result == nil {
		t.Fatal("buildWithOptions() = nil result, want diagnostics-bearing result")
	}
	if result.ErrorCount != 2 {
		t.Fatalf("result.ErrorCount = %d, want %d", result.ErrorCount, 2)
	}
	if len(result.Diagnostics) != 2 {
		t.Fatalf("len(result.Diagnostics) = %d, want %d", len(result.Diagnostics), 2)
	}
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Kind != diag.KindSlugConflict {
			t.Fatalf("diagnostic.Kind = %q, want %q", diagnostic.Kind, diag.KindSlugConflict)
		}
	}

	summary := diagnostics.String()
	for _, want := range []string{
		`Alpha/ [slug_conflict]`,
		`alpha.md [slug_conflict]`,
		`build: build folder pages: slug conflict for "alpha"`,
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("diagnostics summary missing %q\n%s", want, summary)
		}
	}
	if _, statErr := os.Stat(filepath.Join(outputPath, "Alpha", "index.html")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("os.Stat(Alpha/index.html) error = %v, want not-exist after rejected collision", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(outputPath, "alpha", "index.html")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("os.Stat(alpha/index.html) error = %v, want not-exist after rejected collision", statErr)
	}
}

func TestBuildEmitsTimelinePageWhenEnabled(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	muPath := filepath.Join(vaultPath, "mu.md")
	alphaPath := filepath.Join(vaultPath, "alpha.md")
	zetaPath := filepath.Join(vaultPath, "zeta.md")
	writeBuildTestFile(t, vaultPath, "beta.md", `---
title: Beta
date: 2026-04-08
---
# Beta

Newest dated note.
`)
	writeBuildTestFile(t, vaultPath, "mu.md", `---
title: Mu
---
# Mu

Falls back to a newer last-modified time.
`)
	writeBuildTestFile(t, vaultPath, "alpha.md", `---
title: Alpha
---
# Alpha

Shares the same fallback timestamp as Zeta.
`)
	writeBuildTestFile(t, vaultPath, "zeta.md", `---
title: Zeta
---
# Zeta

Shares the same fallback timestamp as Alpha.
`)

	newestFallback := time.Date(2026, time.April, 7, 9, 0, 0, 0, time.UTC)
	tiedFallback := time.Date(2026, time.April, 6, 9, 0, 0, 0, time.UTC)
	if err := os.Chtimes(muPath, newestFallback, newestFallback); err != nil {
		t.Fatalf("os.Chtimes(mu.md) error = %v", err)
	}
	if err := os.Chtimes(alphaPath, tiedFallback, tiedFallback); err != nil {
		t.Fatalf("os.Chtimes(alpha.md) error = %v", err)
	}
	if err := os.Chtimes(zetaPath, tiedFallback, tiedFallback); err != nil {
		t.Fatalf("os.Chtimes(zeta.md) error = %v", err)
	}

	cfg := testBuildSiteConfig()
	cfg.Timeline.Enabled = true

	result, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{})
	if err != nil {
		t.Fatalf("buildWithOptions() error = %v", err)
	}
	if result == nil {
		t.Fatal("buildWithOptions() = nil result, want build result")
	}

	timelineHTML := readBuildOutputFile(t, outputPath, "notes/index.html")
	if !containsAny(timelineHTML, `body class=kind-timeline`, `class="page-shell timeline-page"`, `class=page-shell timeline-page`) {
		t.Fatalf("timeline page missing timeline layout markers\n%s", timelineHTML)
	}
	betaIndex := indexAny(timelineHTML, `href="../beta/">Beta</a>`, `href=../beta/>Beta</a>`)
	muIndex := indexAny(timelineHTML, `href="../mu/">Mu</a>`, `href=../mu/>Mu</a>`)
	alphaIndex := indexAny(timelineHTML, `href="../alpha/">Alpha</a>`, `href=../alpha/>Alpha</a>`)
	zetaIndex := indexAny(timelineHTML, `href="../zeta/">Zeta</a>`, `href=../zeta/>Zeta</a>`)
	if betaIndex == -1 || muIndex == -1 || alphaIndex == -1 || zetaIndex == -1 {
		t.Fatalf("timeline page missing expected note links\n%s", timelineHTML)
	}
	if !(betaIndex < muIndex && muIndex < alphaIndex && alphaIndex < zetaIndex) {
		t.Fatalf("timeline page does not follow recent-note ordering contract\n%s", timelineHTML)
	}

	indexHTML := readBuildOutputFile(t, outputPath, "index.html")
	if !containsAny(indexHTML, `body class=kind-index`, `class="page-shell landing-page"`, `class=page-shell landing-page`) {
		t.Fatalf("default index page was unexpectedly replaced\n%s", indexHTML)
	}

	sitemapXML := readBuildOutputFile(t, outputPath, "sitemap.xml")
	if !bytes.Contains(sitemapXML, []byte("https://example.com/blog/notes/")) {
		t.Fatalf("sitemap.xml missing timeline URL\n%s", sitemapXML)
	}
	if len(result.RecentNotes) == 0 || result.RecentNotes[0].URL != "beta/" {
		t.Fatalf("result.RecentNotes = %#v, want root-relative recent note summaries", result.RecentNotes)
	}
}

func TestBuildUsesTimelineAsHomepageWhenConfigured(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "guide.md", `---
title: Guide
date: 2026-04-06
---
# Guide

Guide body.
`)
	writeBuildTestFile(t, vaultPath, "journal.md", `---
title: Journal
date: 2026-04-05
---
# Journal

Journal body.
`)

	cfg := testBuildSiteConfig()
	cfg.Timeline.Enabled = true
	cfg.Timeline.AsHomepage = true

	result, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{})
	if err != nil {
		t.Fatalf("buildWithOptions() error = %v", err)
	}
	if result == nil {
		t.Fatal("buildWithOptions() = nil result, want build result")
	}

	indexHTML := readBuildOutputFile(t, outputPath, "index.html")
	if !containsAny(indexHTML, `body class=kind-timeline`, `class="page-shell timeline-page"`, `class=page-shell timeline-page`) {
		t.Fatalf("homepage timeline missing timeline layout markers\n%s", indexHTML)
	}
	if containsAny(indexHTML, `body class=kind-index`, `class="page-shell landing-page"`, `class=page-shell landing-page`) {
		t.Fatalf("homepage timeline unexpectedly rendered the default index hero\n%s", indexHTML)
	}
	if !containsAny(indexHTML, `href="guide/">Guide</a>`, `href=guide/>Guide</a>`) {
		t.Fatalf("homepage timeline missing root-relative note link\n%s", indexHTML)
	}

	if _, statErr := os.Stat(filepath.Join(outputPath, "notes", "index.html")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("os.Stat(notes/index.html) error = %v, want not-exist when timeline replaces homepage", statErr)
	}

	sitemapXML := readBuildOutputFile(t, outputPath, "sitemap.xml")
	if !bytes.Contains(sitemapXML, []byte("https://example.com/blog/")) {
		t.Fatalf("sitemap.xml missing homepage URL\n%s", sitemapXML)
	}
	if bytes.Contains(sitemapXML, []byte("https://example.com/blog/notes/")) {
		t.Fatalf("sitemap.xml unexpectedly includes separate timeline path when homepage is replaced\n%s", sitemapXML)
	}
}

func TestBuildRejectsTimelinePathConflicts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		timelinePath string
		setup        func(t *testing.T, vaultPath string)
		wantErr      string
		wantSummary  []string
	}{
		{
			name:         "note slug conflict",
			timelinePath: "notes",
			setup: func(t *testing.T, vaultPath string) {
				t.Helper()
				writeBuildTestFile(t, vaultPath, "notes.md", `---
title: Notes
date: 2026-04-06
---
# Notes

Conflicting note slug.
`)
			},
			wantErr: `slug conflict for "notes"`,
			wantSummary: []string{
				`notes.md [slug_conflict]`,
				`timeline page notes/ [slug_conflict]`,
				`build: build timeline page: slug conflict for "notes" across notes.md, timeline page notes/`,
			},
		},
		{
			name:         "folder page conflict",
			timelinePath: "notes",
			setup: func(t *testing.T, vaultPath string) {
				t.Helper()
				writeBuildTestFile(t, vaultPath, "notes/alpha.md", `---
title: Alpha
date: 2026-04-06
---
# Alpha

Conflicting folder page.
`)
			},
			wantErr: `slug conflict for "notes"`,
			wantSummary: []string{
				`notes/ [slug_conflict]`,
				`timeline page notes/ [slug_conflict]`,
				`build: build timeline page: slug conflict for "notes" across notes/, timeline page notes/`,
			},
		},
		{
			name:         "tag page conflict",
			timelinePath: "tags/topic",
			setup: func(t *testing.T, vaultPath string) {
				t.Helper()
				writeBuildTestFile(t, vaultPath, "alpha.md", `---
title: Alpha
date: 2026-04-06
tags:
  - Topic
---
# Alpha

Conflicting tag page.
`)
			},
			wantErr: `slug conflict for "tags/topic"`,
			wantSummary: []string{
				`tag page tags/topic/ [slug_conflict]`,
				`timeline page tags/topic/ [slug_conflict]`,
				`build: build timeline page: slug conflict for "tags/topic" across tag page tags/topic/, timeline page tags/topic/`,
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			vaultPath := t.TempDir()
			outputPath := filepath.Join(t.TempDir(), "site")
			tt.setup(t, vaultPath)

			cfg := testBuildSiteConfig()
			cfg.Timeline.Enabled = true
			cfg.Timeline.Path = tt.timelinePath

			var diagnostics bytes.Buffer
			result, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{diagnosticsWriter: &diagnostics})
			if err == nil {
				t.Fatal("buildWithOptions() error = nil, want timeline path conflict failure")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("buildWithOptions() error = %v, want %q", err, tt.wantErr)
			}
			if result == nil {
				t.Fatal("buildWithOptions() = nil result, want diagnostics-bearing result")
			}
			if result.ErrorCount != 2 {
				t.Fatalf("result.ErrorCount = %d, want %d", result.ErrorCount, 2)
			}
			if len(result.Diagnostics) != 2 {
				t.Fatalf("len(result.Diagnostics) = %d, want %d", len(result.Diagnostics), 2)
			}
			for _, diagnostic := range result.Diagnostics {
				if diagnostic.Kind != diag.KindSlugConflict {
					t.Fatalf("diagnostic.Kind = %q, want %q", diagnostic.Kind, diag.KindSlugConflict)
				}
			}

			summary := diagnostics.String()
			for _, want := range tt.wantSummary {
				if !strings.Contains(summary, want) {
					t.Fatalf("diagnostics summary missing %q\n%s", want, summary)
				}
			}
		})
	}
}

func TestBuildCopiesConfiguredCustomCSSAndInjectsRelativeLinks(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	customCSSContent := "body { color: tomato; }\n"
	customCSSPath := filepath.Join(vaultPath, "styles", "theme.css")

	writeBuildTestFile(t, vaultPath, "styles/theme.css", customCSSContent)
	writeBuildTestFile(t, vaultPath, "notes/alpha.md", `---
title: Alpha
date: 2026-04-06
tags:
  - Topic
---
# Alpha

Body.
`)

	cfg := testBuildSiteConfig()
	cfg.CustomCSS = customCSSPath

	result, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{})
	if err != nil {
		t.Fatalf("buildWithOptions() error = %v", err)
	}
	if result == nil {
		t.Fatal("buildWithOptions() = nil result, want build result")
	}

	if got := readBuildOutputFile(t, outputPath, "assets/custom.css"); string(got) != customCSSContent {
		t.Fatalf("custom CSS output = %q, want %q", string(got), customCSSContent)
	}

	alphaHTML := readBuildOutputFile(t, outputPath, "alpha/index.html")
	if !containsAny(alphaHTML, `href="../assets/custom.css"`, `href=../assets/custom.css`) {
		t.Fatalf("note page missing relative custom stylesheet link\n%s", alphaHTML)
	}

	tagHTML := readBuildOutputFile(t, outputPath, "tags/topic/index.html")
	if !containsAny(tagHTML, `href="../../assets/custom.css"`, `href=../../assets/custom.css`) {
		t.Fatalf("tag page missing relative custom stylesheet link\n%s", tagHTML)
	}

	indexHTML := readBuildOutputFile(t, outputPath, "index.html")
	if !containsAny(indexHTML, `href="./assets/custom.css"`, `href=./assets/custom.css`) {
		t.Fatalf("index page missing custom stylesheet link\n%s", indexHTML)
	}

	notFoundHTML := readBuildOutputFile(t, outputPath, "404.html")
	if !containsAny(notFoundHTML, `href="./assets/custom.css"`, `href=./assets/custom.css`) {
		t.Fatalf("404 page missing custom stylesheet link\n%s", notFoundHTML)
	}
}

func TestBuildCopiesAutoDetectedVaultRootCustomCSS(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	configPath := filepath.Join(t.TempDir(), "obsite.yaml")
	customCSSContent := "body { background: linen; }\n"
	customCSSPath := filepath.Join(vaultPath, "custom.css")

	writeBuildTestFile(t, vaultPath, "notes/alpha.md", `---
title: Alpha
date: 2026-04-06
---
# Alpha

Body.
`)
	writeBuildTestFile(t, vaultPath, "custom.css", customCSSContent)
	if err := os.WriteFile(configPath, []byte("title: Garden Notes\nbaseURL: https://example.com/blog\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", configPath, err)
	}

	cfg, err := internalconfig.Load(configPath, internalconfig.Overrides{VaultPath: vaultPath})
	if err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}
	if cfg.CustomCSS != customCSSPath {
		t.Fatalf("cfg.CustomCSS = %q, want %q", cfg.CustomCSS, customCSSPath)
	}

	if _, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{}); err != nil {
		t.Fatalf("buildWithOptions() error = %v", err)
	}

	if got := readBuildOutputFile(t, outputPath, "assets/custom.css"); string(got) != customCSSContent {
		t.Fatalf("auto-detected custom CSS output = %q, want %q", string(got), customCSSContent)
	}

	alphaHTML := readBuildOutputFile(t, outputPath, "alpha/index.html")
	if !containsAny(alphaHTML, `href="../assets/custom.css"`, `href=../assets/custom.css`) {
		t.Fatalf("note page missing auto-detected custom stylesheet link\n%s", alphaHTML)
	}
}

func TestBuildRejectsSymlinkCustomCSSSource(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	externalRoot := t.TempDir()
	customCSSPath := filepath.Join(vaultPath, "custom.css")

	writeBuildTestFile(t, vaultPath, "notes/alpha.md", `---
title: Alpha
date: 2026-04-06
---
# Alpha

Body.
`)
	writeBuildTestFile(t, externalRoot, "secret.css", "body { color: lime; }\n")
	writeBuildSymlinkOrSkip(t, filepath.Join(externalRoot, "secret.css"), customCSSPath)

	cfg := testBuildSiteConfig()
	cfg.CustomCSS = customCSSPath

	result, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{})
	if err != nil {
		t.Fatalf("buildWithOptions() error = %v", err)
	}
	if result == nil {
		t.Fatal("buildWithOptions() = nil result, want build result")
	}

	if _, err := os.Stat(filepath.Join(outputPath, filepath.FromSlash(customCSSOutputPath))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("os.Stat(%q) error = %v, want not-exist for rejected symlink custom CSS", customCSSOutputPath, err)
	}

	alphaHTML := readBuildOutputFile(t, outputPath, "alpha/index.html")
	if containsAny(alphaHTML, `href="../assets/custom.css"`, `href=../assets/custom.css`) {
		t.Fatalf("note page unexpectedly injected custom stylesheet link for rejected symlink source\n%s", alphaHTML)
	}
	if containsAny(readBuildOutputFile(t, outputPath, "index.html"), `href="./assets/custom.css"`, `href=./assets/custom.css`) {
		t.Fatalf("index page unexpectedly injected custom stylesheet link for rejected symlink source\n%s", readBuildOutputFile(t, outputPath, "index.html"))
	}
}

func TestBuildKeepsCustomCSSPathReservedFromReferencedAssets(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	customCSSContent := "body { color: tomato; }\n"
	attachmentCSSContent := "body { color: dodgerblue; }\n"
	customCSSPath := filepath.Join(vaultPath, "styles", "theme.css")

	writeBuildTestFile(t, vaultPath, "styles/theme.css", customCSSContent)
	writeBuildTestFile(t, vaultPath, "attachments/custom.css", attachmentCSSContent)
	writeBuildTestFile(t, vaultPath, "notes/alpha.md", `---
title: Alpha
date: 2026-04-06
---
# Alpha

![Styles](../attachments/custom.css)
`)

	cfg := testBuildSiteConfig()
	cfg.CustomCSS = customCSSPath

	result, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{})
	if err != nil {
		t.Fatalf("buildWithOptions() error = %v", err)
	}
	if result == nil {
		t.Fatal("buildWithOptions() = nil result, want build result")
	}

	if got := readBuildOutputFile(t, outputPath, customCSSOutputPath); string(got) != customCSSContent {
		t.Fatalf("custom CSS output = %q, want %q", string(got), customCSSContent)
	}

	attachmentAsset := result.Assets["attachments/custom.css"]
	if attachmentAsset == nil {
		t.Fatal("result.Assets[attachments/custom.css] = nil, want referenced attachment asset")
	}
	if attachmentAsset.DstPath == customCSSOutputPath {
		t.Fatalf("result.Assets[attachments/custom.css].DstPath = %q, want reserved custom CSS path to stay dedicated", attachmentAsset.DstPath)
	}
	if !strings.HasPrefix(attachmentAsset.DstPath, "assets/custom.") || !strings.HasSuffix(attachmentAsset.DstPath, ".css") {
		t.Fatalf("result.Assets[attachments/custom.css].DstPath = %q, want hashed custom.css asset path", attachmentAsset.DstPath)
	}
	if got := readBuildOutputFile(t, outputPath, attachmentAsset.DstPath); string(got) != attachmentCSSContent {
		t.Fatalf("attachment custom.css output = %q, want %q", string(got), attachmentCSSContent)
	}

	alphaHTML := readBuildOutputFile(t, outputPath, "alpha/index.html")
	if !containsAny(alphaHTML, `href="../assets/custom.css"`, `href=../assets/custom.css`) {
		t.Fatalf("note page missing custom stylesheet link\n%s", alphaHTML)
	}
	if !containsAny(alphaHTML, `src="../`+attachmentAsset.DstPath+`"`, `src=../`+attachmentAsset.DstPath) {
		t.Fatalf("note page missing rewritten attachment asset path %q\n%s", attachmentAsset.DstPath, alphaHTML)
	}
}

func TestBuildResultRecentNotesRetainGeneratedSummary(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "notes/summary.md", `---
title: Summary Note
date: 2026-04-06
---
Summary carried through the build result.
`)

	result, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{})
	if err != nil {
		t.Fatalf("buildWithOptions() error = %v", err)
	}
	if result == nil {
		t.Fatal("buildWithOptions() = nil result, want build result")
	}
	if len(result.RecentNotes) != 1 {
		t.Fatalf("len(result.RecentNotes) = %d, want %d", len(result.RecentNotes), 1)
	}

	note := result.Index.Notes["notes/summary.md"]
	if note == nil {
		t.Fatal("result.Index.Notes[notes/summary.md] = nil, want note")
	}
	if note.Summary == "" {
		t.Fatal("result.Index.Notes[notes/summary.md].Summary = empty, want generated summary")
	}
	if got := result.RecentNotes[0].Summary; got != note.Summary {
		t.Fatalf("result.RecentNotes[0].Summary = %q, want %q", got, note.Summary)
	}
}

func TestBuildEmitsPopoverJSONAndClientOnNoteLinkPagesWhenEnabled(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "alpha.md", `---
title: Alpha
date: 2026-04-06
tags:
  - Topic
  - Topic/Child
---
Alpha summary body.

[[Beta]]
`)
	writeBuildTestFile(t, vaultPath, "journal/beta.md", `---
title: Beta
date: 2026-04-05
tags:
  - Topic
---
Beta body.
`)

	cfg := testBuildSiteConfig()
	cfg.Popover.Enabled = true
	cfg.Timeline.Enabled = true

	result, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{})
	if err != nil {
		t.Fatalf("buildWithOptions() error = %v", err)
	}
	if result == nil {
		t.Fatal("buildWithOptions() = nil result, want build result")
	}

	alphaNote := result.Index.Notes["alpha.md"]
	if alphaNote == nil {
		t.Fatal("result.Index.Notes[alpha.md] = nil, want note")
	}

	var payload popoverPayload
	if err := json.Unmarshal(readBuildOutputFile(t, outputPath, "_popover/alpha.json"), &payload); err != nil {
		t.Fatalf("json.Unmarshal(_popover/alpha.json) error = %v", err)
	}
	if payload.Title != noteDisplayTitle(alphaNote) {
		t.Fatalf("popover title = %q, want %q", payload.Title, noteDisplayTitle(alphaNote))
	}
	if payload.Summary != alphaNote.Summary {
		t.Fatalf("popover summary = %q, want %q", payload.Summary, alphaNote.Summary)
	}
	if !reflect.DeepEqual(payload.Tags, alphaNote.Tags) {
		t.Fatalf("popover tags = %#v, want %#v", payload.Tags, alphaNote.Tags)
	}
	betaNote := result.Index.Notes["journal/beta.md"]
	if betaNote == nil {
		t.Fatal("result.Index.Notes[journal/beta.md] = nil, want note")
	}
	betaPayloadPath := filepath.Join(outputPath, "_popover", filepath.FromSlash(cleanSitePath(betaNote.Slug)+".json"))
	if _, err := os.Stat(betaPayloadPath); err != nil {
		t.Fatalf("os.Stat(%q) error = %v, want nested note payload", betaPayloadPath, err)
	}
	for _, relPath := range []string{"_popover/journal.json", "_popover/tags/topic.json", "_popover/notes.json"} {
		if _, err := os.Stat(filepath.Join(outputPath, filepath.FromSlash(relPath))); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("os.Stat(%s) error = %v, want not-exist for non-note payload", relPath, err)
		}
	}

	assertPopoverRuntime := func(t *testing.T, relPath string, siteRootRel string, popoverRoot string) []byte {
		t.Helper()

		html := readBuildOutputFile(t, outputPath, relPath)
		if !containsAny(html, `data-popover-card`, `data-popover-card=""`) {
			t.Fatalf("%s missing popover card marker\n%s", relPath, html)
		}
		if !containsAny(html, `data-site-root-rel="`+siteRootRel+`"`, `data-site-root-rel=`+siteRootRel) {
			t.Fatalf("%s missing site-root-relative popover context\n%s", relPath, html)
		}
		if !containsAny(html, `data-popover-root="`+popoverRoot+`"`, `data-popover-root=`+popoverRoot) {
			t.Fatalf("%s missing popover root context\n%s", relPath, html)
		}
		if !bytes.Contains(html, []byte("mouseenter")) {
			t.Fatalf("%s missing hover listener wiring\n%s", relPath, html)
		}
		if !bytes.Contains(html, []byte("focusin")) {
			t.Fatalf("%s missing focus listener wiring\n%s", relPath, html)
		}

		return html
	}

	noteHTML := assertPopoverRuntime(t, "alpha/index.html", "../", "../_popover/")
	indexHTML := assertPopoverRuntime(t, "index.html", "./", "./_popover/")
	tagHTML := assertPopoverRuntime(t, "tags/topic/index.html", "../../", "../../_popover/")
	folderHTML := assertPopoverRuntime(t, "journal/index.html", "../", "../_popover/")
	timelineHTML := assertPopoverRuntime(t, "notes/index.html", "../", "../_popover/")
	notFoundHTML := assertPopoverRuntime(t, "404.html", "./", "./_popover/")
	if !bytes.Contains(notFoundHTML, []byte("document.baseURI")) {
		t.Fatalf("404 page popover URL resolution does not honor document base URI\n%s", notFoundHTML)
	}

	if !containsAny(
		noteHTML,
		`querySelectorAll("[data-popover-path]")`,
		`querySelectorAll('[data-popover-path]')`,
	) {
		t.Fatalf("note page missing build-time popover link selector\n%s", noteHTML)
	}
	if !containsAny(
		noteHTML,
		`data-popover-path="beta"`,
		`data-popover-path=beta`,
	) {
		t.Fatalf("note page missing build-time popover link annotation for resolved note links\n%s", noteHTML)
	}
	if bytes.Contains(noteHTML, []byte("normalizeTargetPath")) {
		t.Fatalf("note page unexpectedly retains runtime path guessing\n%s", noteHTML)
	}
	if !bytes.Contains(noteHTML, []byte("cache.delete(popoverURL)")) {
		t.Fatalf("note page missing failed-popover cache eviction\n%s", noteHTML)
	}
	for _, html := range [][]byte{indexHTML, tagHTML, folderHTML, timelineHTML, notFoundHTML} {
		if !containsAny(html, `data-popover-path=`, `data-popover-path="`) {
			t.Fatalf("page missing build-time popover link annotations\n%s", html)
		}
	}

	styleCSS := readBuildOutputFile(t, outputPath, "style.css")
	if !bytes.Contains(styleCSS, []byte(".popover-card{")) {
		t.Fatalf("style.css missing popover card styles\n%s", styleCSS)
	}
	if !bytes.Contains(styleCSS, []byte(".popover-card-tag{")) {
		t.Fatalf("style.css missing popover tag styles\n%s", styleCSS)
	}
}

func TestBuildAnnotatesOnlyResolvableNoteLinksForPopover(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "alpha.md", `---
title: Alpha
date: 2026-04-06
---
Handwritten [Beta note](/blog/beta/) stays previewable.

Handwritten [Journal folder](/blog/journal/) must not opt into preview.

Handwritten [External note](https://outside.example/beta/) must not opt into preview.

Jump to [details](#details) must not opt into preview.

## Details
`)
	writeBuildTestFile(t, vaultPath, "journal/beta.md", `---
title: Beta
date: 2026-04-05
---
Beta body.
`)

	cfg := testBuildSiteConfig()
	cfg.Popover.Enabled = true

	result, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{})
	if err != nil {
		t.Fatalf("buildWithOptions() error = %v", err)
	}
	if result == nil {
		t.Fatal("buildWithOptions() = nil result, want build result")
	}

	alphaHTML := readBuildOutputFile(t, outputPath, "alpha/index.html")
	if got, ok := anchorAttrValueByHref(t, alphaHTML, "/blog/beta/", "data-popover-path"); !ok || got != "beta" {
		t.Fatalf("anchor data-popover-path for /blog/beta/ = %q, %t; want %q, true\n%s", got, ok, "beta", alphaHTML)
	}
	if got, ok := anchorAttrValueByHref(t, alphaHTML, "/blog/journal/", "data-popover-path"); ok {
		t.Fatalf("anchor data-popover-path for /blog/journal/ = %q, want missing attribute\n%s", got, alphaHTML)
	}
	if got, ok := anchorAttrValueByHref(t, alphaHTML, "https://outside.example/beta/", "data-popover-path"); ok {
		t.Fatalf("anchor data-popover-path for external link = %q, want missing attribute\n%s", got, alphaHTML)
	}
	if got, ok := anchorAttrValueByHref(t, alphaHTML, "#details", "data-popover-path"); ok {
		t.Fatalf("anchor data-popover-path for hash-only link = %q, want missing attribute\n%s", got, alphaHTML)
	}
}

func TestBuildEmitsSidebarTreeJSONWhenEnabled(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "root.md", `---
title: Root
date: 2026-04-06
---
Root note.
`)
	writeBuildTestFile(t, vaultPath, "notes/guide.md", `---
title: Guide
date: 2026-04-06
---
Guide note.
`)
	writeBuildTestFile(t, vaultPath, "docs/reference.md", `---
title: Reference
date: 2026-04-05
---
Reference note.
`)
	writeBuildTestFile(t, vaultPath, "drafts/private.md", `---
title: Private
publish: false
---
Private note.
`)

	cfg := testBuildSiteConfig()
	cfg.Sidebar.Enabled = true

	result, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{})
	if err != nil {
		t.Fatalf("buildWithOptions() error = %v", err)
	}
	if result == nil {
		t.Fatal("buildWithOptions() = nil result, want build result")
	}

	guideNodes := readSidebarNodesFromHTML(t, readBuildOutputFile(t, outputPath, "guide/index.html"))
	if len(guideNodes) != 3 {
		t.Fatalf("len(sidebar nodes on guide page) = %d, want %d", len(guideNodes), 3)
	}
	if guide := findSidebarNodeByURL(guideNodes, "guide/"); guide == nil || !guide.IsActive {
		t.Fatalf("guide sidebar node = %#v, want active guide node", guide)
	}
	if notes := findSidebarNodeByURL(guideNodes, "notes/"); notes == nil || !notes.IsDir || len(notes.Children) != 1 || notes.Children[0].Name != "Guide" {
		t.Fatalf("notes sidebar node = %#v, want notes directory with only the published guide child", notes)
	}
	if docs := findSidebarNodeByURL(guideNodes, "docs/"); docs == nil || !docs.IsDir || len(docs.Children) != 1 || docs.Children[0].Name != "Reference" {
		t.Fatalf("docs sidebar node = %#v, want docs directory with reference child", docs)
	}
	if draft := findSidebarNodeByURL(guideNodes, "drafts/"); draft != nil {
		t.Fatalf("drafts sidebar node = %#v, want unpublished-only directory to be excluded", draft)
	}
	if root := findSidebarNodeByURL(guideNodes, "root/"); root == nil || root.IsDir || root.Name != "Root" {
		t.Fatalf("root sidebar node = %#v, want published root note node", root)
	}

	docsNodes := readSidebarNodesFromHTML(t, readBuildOutputFile(t, outputPath, "docs/index.html"))
	if docs := findSidebarNodeByURL(docsNodes, "docs/"); docs == nil || !docs.IsActive {
		t.Fatalf("docs sidebar node on folder page = %#v, want active folder node", docs)
	}

	notFoundNodes := readSidebarNodesFromHTML(t, readBuildOutputFile(t, outputPath, "404.html"))
	if len(notFoundNodes) != len(guideNodes) {
		t.Fatalf("len(sidebar nodes on 404 page) = %d, want %d", len(notFoundNodes), len(guideNodes))
	}
	if hasActiveSidebarNode(notFoundNodes) {
		t.Fatalf("404 sidebar tree = %#v, want no active nodes", notFoundNodes)
	}

	guideHTML := readBuildOutputFile(t, outputPath, "guide/index.html")
	if !containsAny(guideHTML, `data-sidebar-toggle`, `data-sidebar-toggle=""`) {
		t.Fatalf("guide page missing sidebar toggle markup\n%s", guideHTML)
	}
	if !bytes.Contains(guideHTML, []byte("obsite.sidebar.expanded.v1")) {
		t.Fatalf("guide page missing sidebar persistence script\n%s", guideHTML)
	}
}

func TestBuildTreatsExplicitDefaultHTTPSPortAsSameOriginForPopover(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "alpha.md", `---
title: Alpha
date: 2026-04-06
---
Absolute [Beta note](https://example.com:443/blog/beta/) should stay previewable.
`)
	writeBuildTestFile(t, vaultPath, "beta.md", `---
title: Beta
date: 2026-04-05
---
Beta body.
`)

	cfg := testBuildSiteConfig()
	cfg.Popover.Enabled = true

	result, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{})
	if err != nil {
		t.Fatalf("buildWithOptions() error = %v", err)
	}
	if result == nil {
		t.Fatal("buildWithOptions() = nil result, want build result")
	}

	alphaHTML := readBuildOutputFile(t, outputPath, "alpha/index.html")
	if got, ok := anchorAttrValueByHref(t, alphaHTML, "https://example.com:443/blog/beta/", "data-popover-path"); !ok || got != "beta" {
		t.Fatalf("anchor data-popover-path for absolute same-origin default-port link = %q, %t; want %q, true\n%s", got, ok, "beta", alphaHTML)
	}
}

func TestBuildSkipsPopoverArtifactsWhenDisabled(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "notes/alpha.md", `---
title: Alpha
date: 2026-04-06
tags:
  - Topic
---
Alpha summary body.
`)

	result, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{})
	if err != nil {
		t.Fatalf("buildWithOptions() error = %v", err)
	}
	if result == nil {
		t.Fatal("buildWithOptions() = nil result, want build result")
	}

	if _, err := os.Stat(filepath.Join(outputPath, "_popover", "alpha.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("os.Stat(_popover/alpha.json) error = %v, want not-exist when popovers are disabled", err)
	}

	noteHTML := readBuildOutputFile(t, outputPath, "alpha/index.html")
	if containsAny(noteHTML, `data-popover-card`, `data-popover-root=`) {
		t.Fatalf("note page unexpectedly includes popover wiring when disabled\n%s", noteHTML)
	}
}

func TestBuildEmitsRSSFeedAndAutoDiscoveryLinks(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "notes/alpha.md", `---
title: Alpha
date: 2026-04-06
---
Alpha summary.
`)
	writeBuildTestFile(t, vaultPath, "notes/beta.md", `---
title: Beta
date: 2026-04-05
---
Beta summary.
`)

	result, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{})
	if err != nil {
		t.Fatalf("buildWithOptions() error = %v", err)
	}
	if result == nil {
		t.Fatal("buildWithOptions() = nil result, want build result")
	}

	rssXML := readBuildOutputFile(t, outputPath, "index.xml")
	for _, want := range []string{
		`<rss version="2.0">`,
		`https://example.com/blog/alpha/`,
		`https://example.com/blog/beta/`,
	} {
		if !bytes.Contains(rssXML, []byte(want)) {
			t.Fatalf("index.xml missing %q\n%s", want, rssXML)
		}
	}

	indexHTML := readBuildOutputFile(t, outputPath, "index.html")
	if !containsAny(
		indexHTML,
		`<link rel="alternate" type="application/rss+xml" title="Field Notes RSS" href="./index.xml">`,
		`<link rel=alternate type=application/rss+xml title="Field Notes RSS" href=./index.xml>`,
	) {
		t.Fatalf("index page missing RSS auto-discovery link\n%s", indexHTML)
	}

	alphaHTML := readBuildOutputFile(t, outputPath, "alpha/index.html")
	if !containsAny(
		alphaHTML,
		`<link rel="alternate" type="application/rss+xml" title="Field Notes RSS" href="../index.xml">`,
		`<link rel=alternate type=application/rss+xml title="Field Notes RSS" href=../index.xml>`,
	) {
		t.Fatalf("note page missing relative RSS auto-discovery link\n%s", alphaHTML)
	}
}

func TestBuildSkipsRSSFeedAndAutoDiscoveryLinksWhenRSSDisabled(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "notes/alpha.md", `---
title: Alpha
date: 2026-04-06
---
Alpha summary.
`)

	cfg := testBuildSiteConfig()
	cfg.RSS.Enabled = false
	cfg.RSS.EnabledSet = true

	result, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{})
	if err != nil {
		t.Fatalf("buildWithOptions() error = %v", err)
	}
	if result == nil {
		t.Fatal("buildWithOptions() = nil result, want build result")
	}

	if _, err := os.Stat(filepath.Join(outputPath, "index.xml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("os.Stat(index.xml) error = %v, want not-exist when RSS is disabled", err)
	}

	for _, page := range []struct {
		name string
		data []byte
	}{
		{name: "index.html", data: readBuildOutputFile(t, outputPath, "index.html")},
		{name: "alpha/index.html", data: readBuildOutputFile(t, outputPath, "alpha/index.html")},
	} {
		if containsAny(
			page.data,
			`<link rel="alternate" type="application/rss+xml"`,
			`<link rel=alternate type=application/rss+xml`,
		) {
			t.Fatalf("%s unexpectedly includes RSS auto-discovery link when RSS is disabled\n%s", page.name, page.data)
		}
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
		"https://example.com/blog/notes/",
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
	renderMarkdownNote = func(idx *model.VaultIndex, note *model.Note, assetSink internalmarkdown.AssetSink) (*renderedNote, error) {
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

func TestBuildNoOpSecondBuildSkipsPassTwoAndReplaysCachedRenderDiagnostics(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "notes/warn.md", `---
title: Warn
date: 2026-04-06
---
# Warn

[[Missing]]
`)

	if _, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard}); err != nil {
		t.Fatalf("first buildWithOptions() error = %v", err)
	}

	var diagnostics bytes.Buffer
	result, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: &diagnostics})
	if err != nil {
		t.Fatalf("second buildWithOptions() error = %v", err)
	}
	if result.NotePages != 0 {
		t.Fatalf("result.NotePages = %d, want %d", result.NotePages, 0)
	}
	if len(result.Diagnostics) != 1 {
		t.Fatalf("len(result.Diagnostics) = %d, want %d", len(result.Diagnostics), 1)
	}
	if result.Diagnostics[0].Kind != diag.KindDeadLink {
		t.Fatalf("result.Diagnostics[0].Kind = %q, want %q", result.Diagnostics[0].Kind, diag.KindDeadLink)
	}
	if !strings.Contains(result.Diagnostics[0].Message, `wikilink "Missing" could not be resolved`) {
		t.Fatalf("result.Diagnostics[0].Message = %q, want cached deadlink warning", result.Diagnostics[0].Message)
	}
	if !strings.Contains(diagnostics.String(), "Warnings (1)") {
		t.Fatalf("diagnostics summary = %q, want cached warning summary", diagnostics.String())
	}
	if !strings.Contains(diagnostics.String(), `wikilink "Missing" could not be resolved`) {
		t.Fatalf("diagnostics summary = %q, want cached deadlink warning", diagnostics.String())
	}
	manifestJSON := readBuildOutputFile(t, outputPath, cacheManifestRelPath)
	if !bytes.Contains(manifestJSON, []byte(`"notes/warn.md"`)) {
		t.Fatalf("manifest.json missing warn note cache entry\n%s", manifestJSON)
	}
}

func TestBuildRerendersCachedDeadLinkWhenTargetNoteAppears(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "notes/source.md", `---
title: Source
---
# Source

[[Missing]]
`)

	if _, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard}); err != nil {
		t.Fatalf("first buildWithOptions() error = %v", err)
	}

	writeBuildTestFile(t, vaultPath, "notes/missing.md", `---
title: Missing
---
# Missing

Target note.
`)

	var diagnostics bytes.Buffer
	result, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: &diagnostics})
	if err != nil {
		t.Fatalf("second buildWithOptions() error = %v", err)
	}
	if result.NotePages != 2 {
		t.Fatalf("result.NotePages = %d, want %d", result.NotePages, 2)
	}
	if len(result.Diagnostics) != 0 {
		t.Fatalf("len(result.Diagnostics) = %d, want 0 after target note appears", len(result.Diagnostics))
	}
	if strings.Contains(diagnostics.String(), `wikilink "Missing" could not be resolved`) {
		t.Fatalf("diagnostics summary = %q, want deadlink warning cleared", diagnostics.String())
	}

	sourceHTML := readBuildOutputFile(t, outputPath, "source/index.html")
	if !containsAny(sourceHTML, `href="../missing/">Missing</a>`, `href=../missing/>Missing</a>`) {
		t.Fatalf("source page missing refreshed link to new target\n%s", sourceHTML)
	}
}

func TestBuildRerendersCachedLinksWhenTargetHeadingChanges(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "notes/source.md", `---
title: Source
---
# Source

[[Beta#Section]]
`)
	writeBuildTestFile(t, vaultPath, "notes/beta.md", `---
title: Beta
---
# Beta

## Section

Initial section.
`)

	if _, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard}); err != nil {
		t.Fatalf("first buildWithOptions() error = %v", err)
	}

	writeBuildTestFile(t, vaultPath, "notes/beta.md", `---
title: Beta
---
# Beta

## Renamed

Updated section.
`)

	var diagnostics bytes.Buffer
	result, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: &diagnostics})
	if err != nil {
		t.Fatalf("second buildWithOptions() error = %v", err)
	}
	if result.NotePages != 2 {
		t.Fatalf("result.NotePages = %d, want %d", result.NotePages, 2)
	}
	if len(result.Diagnostics) != 1 {
		t.Fatalf("len(result.Diagnostics) = %d, want %d", len(result.Diagnostics), 1)
	}
	if result.Diagnostics[0].Kind != diag.KindDeadLink {
		t.Fatalf("result.Diagnostics[0].Kind = %q, want %q", result.Diagnostics[0].Kind, diag.KindDeadLink)
	}
	if !strings.Contains(result.Diagnostics[0].Message, `missing heading "Section"`) {
		t.Fatalf("result.Diagnostics[0].Message = %q, want missing-heading warning", result.Diagnostics[0].Message)
	}

	sourceHTML := readBuildOutputFile(t, outputPath, "source/index.html")
	if containsAny(sourceHTML, `href="../beta/#section"`, `href=../beta/#section`) {
		t.Fatalf("source page still contains stale heading link\n%s", sourceHTML)
	}
	if !strings.Contains(diagnostics.String(), `missing heading "Section"`) {
		t.Fatalf("diagnostics summary = %q, want missing-heading warning", diagnostics.String())
	}
}

func TestBuildRerendersEmbeddingNotesWhenEmbeddedDependenciesChange(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "notes/alpha.md", `---
title: Alpha
---
# Alpha

![[Beta]]
`)
	writeBuildTestFile(t, vaultPath, "notes/beta.md", `---
title: Beta
---
# Beta

[[Missing]]
`)

	if _, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard}); err != nil {
		t.Fatalf("first buildWithOptions() error = %v", err)
	}

	writeBuildTestFile(t, vaultPath, "notes/missing.md", `---
title: Missing
---
# Missing

Resolved target.
`)

	var diagnostics bytes.Buffer
	result, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: &diagnostics})
	if err != nil {
		t.Fatalf("second buildWithOptions() error = %v", err)
	}
	if result.NotePages != 3 {
		t.Fatalf("result.NotePages = %d, want %d", result.NotePages, 3)
	}
	if len(result.Diagnostics) != 0 {
		t.Fatalf("len(result.Diagnostics) = %d, want 0 after embedded dependency resolves", len(result.Diagnostics))
	}
	if strings.Contains(diagnostics.String(), `wikilink "Missing" could not be resolved`) {
		t.Fatalf("diagnostics summary = %q, want embedded deadlink warning cleared", diagnostics.String())
	}

	alphaHTML := readBuildOutputFile(t, outputPath, "alpha/index.html")
	if !containsAny(alphaHTML, `href="../missing/">Missing</a>`, `href=../missing/>Missing</a>`) {
		t.Fatalf("embedding note page missing refreshed embedded link\n%s", alphaHTML)
	}
}

func TestBuildRerendersNoteWhenLastModifiedChangesWithoutContentChange(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	notePath := filepath.Join(vaultPath, "notes", "guide.md")

	writeBuildTestFile(t, vaultPath, "notes/guide.md", `---
title: Guide
---
# Guide

Body.
`)

	firstModified := time.Date(2026, time.April, 6, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(notePath, firstModified, firstModified); err != nil {
		t.Fatalf("os.Chtimes(firstModified) error = %v", err)
	}

	if _, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard}); err != nil {
		t.Fatalf("first buildWithOptions() error = %v", err)
	}
	firstHTML := readBuildOutputFile(t, outputPath, "guide/index.html")
	if !bytes.Contains(firstHTML, []byte(`>06 Apr 2026</time>`)) {
		t.Fatalf("first note page missing initial updated date\n%s", firstHTML)
	}

	secondModified := time.Date(2026, time.April, 7, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(notePath, secondModified, secondModified); err != nil {
		t.Fatalf("os.Chtimes(secondModified) error = %v", err)
	}

	result, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard})
	if err != nil {
		t.Fatalf("second buildWithOptions() error = %v", err)
	}
	if result.NotePages != 1 {
		t.Fatalf("result.NotePages = %d, want %d", result.NotePages, 1)
	}

	secondHTML := readBuildOutputFile(t, outputPath, "guide/index.html")
	if !bytes.Contains(secondHTML, []byte(`>07 Apr 2026</time>`)) {
		t.Fatalf("second note page missing refreshed updated date\n%s", secondHTML)
	}
	if bytes.Contains(secondHTML, []byte(`>06 Apr 2026</time>`)) {
		t.Fatalf("second note page still contains stale updated date\n%s", secondHTML)
	}
}

func TestBuildDefaultTemplateSignalInvalidatesCacheWithoutTemplateDir(t *testing.T) {
	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "notes/guide.md", `---
title: Guide
---
# Guide

Body.
`)

	if _, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard}); err != nil {
		t.Fatalf("first buildWithOptions() error = %v", err)
	}

	result, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard})
	if err != nil {
		t.Fatalf("second buildWithOptions() error = %v", err)
	}
	if result.NotePages != 0 {
		t.Fatalf("result.NotePages = %d, want %d before embedded template signal changes", result.NotePages, 0)
	}

	originalReadDefaultTemplateAssetForSignature := readDefaultTemplateAssetForSignature
	readDefaultTemplateAssetForSignature = func(name string) ([]byte, error) {
		data, err := originalReadDefaultTemplateAssetForSignature(name)
		if err != nil {
			return nil, err
		}
		if name != "note.html" {
			return data, nil
		}

		mutated := append([]byte(nil), data...)
		mutated = append(mutated, []byte("\n<!-- template-signal-change -->")...)
		return mutated, nil
	}
	defer func() {
		readDefaultTemplateAssetForSignature = originalReadDefaultTemplateAssetForSignature
	}()

	result, err = buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard})
	if err != nil {
		t.Fatalf("third buildWithOptions() error = %v", err)
	}
	if result.NotePages != 1 {
		t.Fatalf("result.NotePages = %d, want %d after embedded template signal changes", result.NotePages, 1)
	}
}

func TestBuildDefaultTemplateSignalInvalidatesCacheWithPartialTemplateDirOverride(t *testing.T) {
	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	templateDir := t.TempDir()

	writeBuildTestFile(t, vaultPath, "notes/guide.md", `---
title: Guide
---
# Guide

Body.
`)
	writeBuildTestFile(t, templateDir, "base.html", `{{define "base"}}<!doctype html><html><body data-build-custom-base="true">{{template "content" .}}</body></html>{{end}}`)

	cfg := testBuildSiteConfig()
	cfg.TemplateDir = templateDir

	if _, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard}); err != nil {
		t.Fatalf("first buildWithOptions() error = %v", err)
	}

	result, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard})
	if err != nil {
		t.Fatalf("second buildWithOptions() error = %v", err)
	}
	if result.NotePages != 0 {
		t.Fatalf("result.NotePages = %d, want %d before embedded template signal changes", result.NotePages, 0)
	}

	originalReadDefaultTemplateAssetForSignature := readDefaultTemplateAssetForSignature
	readDefaultTemplateAssetForSignature = func(name string) ([]byte, error) {
		data, err := originalReadDefaultTemplateAssetForSignature(name)
		if err != nil {
			return nil, err
		}
		if name != "base.html" {
			return data, nil
		}

		mutated := append([]byte(nil), data...)
		mutated = append(mutated, []byte("\n<!-- template-signal-change -->")...)
		return mutated, nil
	}
	defer func() {
		readDefaultTemplateAssetForSignature = originalReadDefaultTemplateAssetForSignature
	}()

	result, err = buildWithOptions(cfg, vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard})
	if err != nil {
		t.Fatalf("third buildWithOptions() error = %v", err)
	}
	if result.NotePages != 1 {
		t.Fatalf("result.NotePages = %d, want %d after embedded template signal changes", result.NotePages, 1)
	}
}

func TestBuildLinkChangeRerendersChangedAndDependentNotePages(t *testing.T) {
	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "notes/alpha.md", `---
title: Alpha
date: 2026-04-06
---
# Alpha

[[Beta]]
`)
	writeBuildTestFile(t, vaultPath, "notes/beta.md", `---
title: Beta
date: 2026-04-06
---
# Beta

Body.
`)
	writeBuildTestFile(t, vaultPath, "notes/gamma.md", `---
title: Gamma
date: 2026-04-06
---
# Gamma

Body.
`)
	writeBuildTestFile(t, vaultPath, "notes/delta.md", `---
title: Delta
date: 2026-04-06
---
# Delta

Body.
`)

	if _, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard}); err != nil {
		t.Fatalf("first buildWithOptions() error = %v", err)
	}

	writeBuildTestFile(t, vaultPath, "notes/alpha.md", `---
title: Alpha
date: 2026-04-06
---
# Alpha

[[Gamma]]
`)

	getRenderedPaths := captureRenderedNotePagePaths(t)
	result, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard})
	if err != nil {
		t.Fatalf("second buildWithOptions() error = %v", err)
	}
	if result.NotePages != 1 {
		t.Fatalf("result.NotePages = %d, want %d", result.NotePages, 1)
	}

	got := getRenderedPaths()
	want := []string{"notes/alpha.md", "notes/beta.md", "notes/gamma.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rendered note pages = %#v, want %#v", got, want)
	}
}

func TestBuildKeepsCachedPassTwoEmbedAssetsForCleanNotes(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, ".obsidian/app.json", `{"attachmentFolderPath":"attachments"}`)
	writeBuildTestFile(t, vaultPath, "attachments/hero.png", "hero-image")
	writeBuildTestFile(t, vaultPath, "notes/host.md", `---
title: Host
date: 2026-04-06
---
# Host

![[attachments/hero.png]]
`)
	writeBuildTestFile(t, vaultPath, "notes/other.md", `---
title: Other
date: 2026-04-06
---
# Other

Body.
`)

	if _, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard}); err != nil {
		t.Fatalf("first buildWithOptions() error = %v", err)
	}

	writeBuildTestFile(t, vaultPath, "notes/other.md", `---
title: Other
date: 2026-04-06
---
# Other

Updated body.
`)

	result, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard})
	if err != nil {
		t.Fatalf("second buildWithOptions() error = %v", err)
	}
	if result.NotePages != 1 {
		t.Fatalf("result.NotePages = %d, want %d", result.NotePages, 1)
	}
	if got := string(readBuildOutputFile(t, outputPath, "assets/hero.png")); got != "hero-image" {
		t.Fatalf("assets/hero.png = %q, want %q", got, "hero-image")
	}
}

func TestBuildRerendersOnlyAffectedRelatedNotePagesWhenCorpusChanges(t *testing.T) {
	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "notes/alpha.md", `---
title: Alpha
date: 2026-04-06
---
# Alpha

Static site generator release notes.
`)
	writeBuildTestFile(t, vaultPath, "notes/beta.md", `---
title: Beta
date: 2026-04-06
---
# Beta

Static site generator deployment guide.
`)
	writeBuildTestFile(t, vaultPath, "notes/gamma.md", `---
title: Gamma
date: 2026-04-06
---
# Gamma

Kitchen recipe sourdough bread.
`)

	cfg := testBuildSiteConfig()
	cfg.Related.Enabled = true
	cfg.Related.Count = 1

	if _, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard}); err != nil {
		t.Fatalf("first buildWithOptions() error = %v", err)
	}

	writeBuildTestFile(t, vaultPath, "notes/alpha.md", `---
title: Alpha
date: 2026-04-06
---
# Alpha

Travel itinerary seaside journal.
`)

	getRenderedPaths := captureRenderedNotePagePaths(t)
	result, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard})
	if err != nil {
		t.Fatalf("second buildWithOptions() error = %v", err)
	}
	if result.NotePages != 1 {
		t.Fatalf("result.NotePages = %d, want %d", result.NotePages, 1)
	}

	got := getRenderedPaths()
	want := []string{"notes/alpha.md", "notes/beta.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rendered note pages = %#v, want %#v", got, want)
	}
}

func TestBuildRerendersOnlyAffectedRelatedNotePagesWhenTagsChange(t *testing.T) {
	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "notes/alpha.md", `---
title: Alpha
date: 2026-04-06
tags:
  - go
---
# Alpha

Static site generator release notes.
`)
	writeBuildTestFile(t, vaultPath, "notes/beta.md", `---
title: Beta
date: 2026-04-06
---
# Beta

Static site generator release notes.
`)
	writeBuildTestFile(t, vaultPath, "notes/gamma.md", `---
title: Gamma
date: 2026-04-06
tags:
  - go
---
# Gamma

Static site generator release notes.
`)

	cfg := testBuildSiteConfig()
	cfg.Related.Enabled = true
	cfg.Related.Count = 1

	if _, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard}); err != nil {
		t.Fatalf("first buildWithOptions() error = %v", err)
	}

	writeBuildTestFile(t, vaultPath, "notes/gamma.md", `---
title: Gamma
date: 2026-04-06
---
# Gamma

Static site generator release notes.
`)

	getRenderedPaths := captureRenderedNotePagePaths(t)
	result, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard})
	if err != nil {
		t.Fatalf("second buildWithOptions() error = %v", err)
	}
	if result.NotePages != 1 {
		t.Fatalf("result.NotePages = %d, want %d", result.NotePages, 1)
	}

	if got := getRenderedPaths(); !reflect.DeepEqual(got, []string{"notes/alpha.md", "notes/gamma.md"}) {
		t.Fatalf("rendered note pages = %#v, want %#v", got, []string{"notes/alpha.md", "notes/gamma.md"})
	}
}

func TestBuildRerendersOnlyAffectedRelatedNotePagesWhenLinksChange(t *testing.T) {
	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "notes/alpha.md", `---
title: Alpha
date: 2026-04-06
---
# Alpha

Shared term cluster [[Beta]].
`)
	writeBuildTestFile(t, vaultPath, "notes/beta.md", `---
title: Beta
date: 2026-04-06
---
# Beta

Shared term cluster [[Alpha]].
`)
	writeBuildTestFile(t, vaultPath, "notes/aaron.md", `---
title: Aaron
date: 2026-04-06
---
# Aaron

Shared term cluster [[Aaron]].
`)

	cfg := testBuildSiteConfig()
	cfg.Related.Enabled = true
	cfg.Related.Count = 1

	if _, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard}); err != nil {
		t.Fatalf("first buildWithOptions() error = %v", err)
	}

	writeBuildTestFile(t, vaultPath, "notes/beta.md", `---
title: Beta
date: 2026-04-06
---
# Beta

Shared term cluster [[Beta]].
`)

	getRenderedPaths := captureRenderedNotePagePaths(t)
	result, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard})
	if err != nil {
		t.Fatalf("second buildWithOptions() error = %v", err)
	}
	if result.NotePages != 1 {
		t.Fatalf("result.NotePages = %d, want %d", result.NotePages, 1)
	}

	if got := getRenderedPaths(); !reflect.DeepEqual(got, []string{"notes/alpha.md", "notes/beta.md"}) {
		t.Fatalf("rendered note pages = %#v, want %#v", got, []string{"notes/alpha.md", "notes/beta.md"})
	}
}

func TestBuildRendersRelatedArticlesSectionWhenNonEmpty(t *testing.T) {
	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "notes/alpha.md", `---
title: Alpha
date: 2026-04-06
---
# Alpha

Static site generator release notes.
`)
	writeBuildTestFile(t, vaultPath, "notes/beta.md", `---
title: Beta
date: 2026-04-06
---
# Beta

Static site generator deployment guide.
`)

	cfg := testBuildSiteConfig()
	cfg.Related.Enabled = true
	cfg.Related.Count = 1

	if _, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard}); err != nil {
		t.Fatalf("buildWithOptions() error = %v", err)
	}

	alphaHTML := readBuildOutputFile(t, outputPath, "alpha/index.html")
	if !containsAny(alphaHTML, `id="related-articles-heading">Related Articles</h2>`, `id=related-articles-heading>Related Articles</h2>`) {
		t.Fatalf("alpha page missing related-articles section\n%s", alphaHTML)
	}
	if !containsAny(alphaHTML, `href="../beta/">Beta</a>`, `href=../beta/>Beta</a>`) {
		t.Fatalf("alpha page missing related beta link\n%s", alphaHTML)
	}
}

func TestBuildOmitsRelatedArticlesSectionWhenEmpty(t *testing.T) {
	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "notes/alpha.md", `---
title: Alpha
date: 2026-04-06
---
# Alpha

Standalone note.
`)

	cfg := testBuildSiteConfig()
	cfg.Related.Enabled = true

	if _, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard}); err != nil {
		t.Fatalf("buildWithOptions() error = %v", err)
	}

	alphaHTML := readBuildOutputFile(t, outputPath, "alpha/index.html")
	if bytes.Contains(alphaHTML, []byte("Related Articles")) {
		t.Fatalf("alpha page unexpectedly rendered related-articles section\n%s", alphaHTML)
	}
}

func TestBuildRelatedDerivedSignatureTracksRenderedArticleContract(t *testing.T) {
	t.Parallel()

	base := []model.RelatedArticle{{
		Title:   "Beta",
		URL:     "../beta/",
		Summary: "Deployment guide.",
		Score:   1.25,
		Tags: []model.TagLink{{
			Name: "go",
			Slug: "go",
			URL:  "../tags/go/",
		}},
	}}
	baseSignature := buildRelatedDerivedSignature(base)
	if got := buildRelatedDerivedSignature(cloneRelatedArticles(base)); got != baseSignature {
		t.Fatalf("buildRelatedDerivedSignature(clone) = %q, want %q", got, baseSignature)
	}

	tests := []struct {
		name   string
		mutate func([]model.RelatedArticle)
	}{
		{
			name: "summary change",
			mutate: func(articles []model.RelatedArticle) {
				articles[0].Summary = "Updated deployment guide."
			},
		},
		{
			name: "score change",
			mutate: func(articles []model.RelatedArticle) {
				articles[0].Score = 1.5
			},
		},
		{
			name: "tag name change",
			mutate: func(articles []model.RelatedArticle) {
				articles[0].Tags[0].Name = "infra"
			},
		},
		{
			name: "tag slug change",
			mutate: func(articles []model.RelatedArticle) {
				articles[0].Tags[0].Slug = "infra"
			},
		},
		{
			name: "tag url change",
			mutate: func(articles []model.RelatedArticle) {
				articles[0].Tags[0].URL = "../tags/infra/"
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			articles := cloneRelatedArticles(base)
			tt.mutate(articles)
			if got := buildRelatedDerivedSignature(articles); got == baseSignature {
				t.Fatalf("buildRelatedDerivedSignature() = %q, want signature to change from %q", got, baseSignature)
			}
		})
	}
}

func TestBuildNoOpSecondBuildWithSearchDoesNotRenderCleanNotePagesTwice(t *testing.T) {
	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "notes/alpha.md", `---
title: Alpha
date: 2026-04-06
---
# Alpha

Body.
`)
	writeBuildTestFile(t, vaultPath, "notes/beta.md", `---
title: Beta
date: 2026-04-06
---
# Beta

Body.
`)

	cfg := testBuildSiteConfig()
	cfg.Search.Enabled = true
	cfg.Search.PagefindPath = "pagefind_extended"
	cfg.Search.PagefindVersion = "1.4.0"

	options := buildOptions{
		pagefindLookPath: func(path string) (string, error) {
			if path != "pagefind_extended" {
				t.Fatalf("pagefindLookPath() path = %q, want %q", path, "pagefind_extended")
			}
			return "/usr/local/bin/pagefind_extended", nil
		},
		pagefindCommand: func(name string, args ...string) ([]byte, error) {
			if name != "/usr/local/bin/pagefind_extended" {
				t.Fatalf("pagefindCommand() name = %q, want %q", name, "/usr/local/bin/pagefind_extended")
			}
			if len(args) == 1 && args[0] == "--version" {
				return []byte("pagefind_extended 1.4.0\n"), nil
			}
			if len(args) != 4 || args[0] != "--site" || args[2] != "--output-subdir" || args[3] != pagefindOutputSubdir {
				t.Fatalf("pagefindCommand() args = %#v, want site invocation", args)
			}
			writeMinimalPagefindBundle(t, filepath.Join(args[1], pagefindOutputSubdir))
			return []byte("Indexed 2 pages\n"), nil
		},
	}

	if _, err := buildWithOptions(cfg, vaultPath, outputPath, options); err != nil {
		t.Fatalf("first buildWithOptions() error = %v", err)
	}

	getRenderedCalls := captureRenderedNotePageCalls(t)
	result, err := buildWithOptions(cfg, vaultPath, outputPath, options)
	if err != nil {
		t.Fatalf("second buildWithOptions() error = %v", err)
	}
	if result.NotePages != 0 {
		t.Fatalf("result.NotePages = %d, want %d", result.NotePages, 0)
	}

	got := getRenderedCalls()
	allowed := map[string]struct{}{
		"notes/alpha.md": {},
		"notes/beta.md":  {},
	}
	if len(got) > len(allowed) {
		t.Fatalf("len(rendered note page calls) = %d, want <= %d (%#v)", len(got), len(allowed), got)
	}
	for relPath, count := range countStrings(got) {
		if _, ok := allowed[relPath]; !ok {
			t.Fatalf("rendered note page call included unexpected path %q in %#v", relPath, got)
		}
		if count > 1 {
			t.Fatalf("rendered note page call count for %q = %d, want <= 1 (%#v)", relPath, count, got)
		}
	}
}

func TestBuildNoOpSecondBuildWithSearchDoesNotRenderCleanNoteOrArchivePages(t *testing.T) {
	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "alpha/one.md", `---
title: One
date: 2026-04-06
tags:
  - alpha
---
# One

Body.
`)
	writeBuildTestFile(t, vaultPath, "beta/two.md", `---
title: Two
date: 2026-04-05
tags:
  - beta
---
# Two

Body.
`)

	cfg := testBuildSiteConfig()
	cfg.Search.Enabled = true
	cfg.Search.PagefindPath = "pagefind_extended"
	cfg.Search.PagefindVersion = "1.4.0"
	cfg.Timeline.Enabled = true
	cfg.Timeline.Path = "notes"

	pagefindIndexRuns := 0
	options := buildOptions{
		pagefindLookPath: func(path string) (string, error) {
			if path != "pagefind_extended" {
				t.Fatalf("pagefindLookPath() path = %q, want %q", path, "pagefind_extended")
			}
			return "/usr/local/bin/pagefind_extended", nil
		},
		pagefindCommand: func(name string, args ...string) ([]byte, error) {
			if name != "/usr/local/bin/pagefind_extended" {
				t.Fatalf("pagefindCommand() name = %q, want %q", name, "/usr/local/bin/pagefind_extended")
			}
			if len(args) == 1 && args[0] == "--version" {
				return []byte("pagefind_extended 1.4.0\n"), nil
			}
			if len(args) != 4 || args[0] != "--site" || args[2] != "--output-subdir" || args[3] != pagefindOutputSubdir {
				t.Fatalf("pagefindCommand() args = %#v, want site invocation", args)
			}

			pagefindIndexRuns++
			if pagefindIndexRuns == 2 {
				for relPath, html := range map[string][]byte{
					"index.html":            readBuildOutputFile(t, args[1], "index.html"),
					"tags/alpha/index.html": readBuildOutputFile(t, args[1], "tags/alpha/index.html"),
					"alpha/index.html":      readBuildOutputFile(t, args[1], "alpha/index.html"),
					"notes/index.html":      readBuildOutputFile(t, args[1], "notes/index.html"),
					"one/index.html":        readBuildOutputFile(t, args[1], "one/index.html"),
					"two/index.html":        readBuildOutputFile(t, args[1], "two/index.html"),
				} {
					if containsFrameworkSearchUI(html) {
						t.Fatalf("staged %s unexpectedly exposes search before Pagefind succeeds on no-op build\n%s", relPath, html)
					}
				}
			}

			writeMinimalPagefindBundle(t, filepath.Join(args[1], pagefindOutputSubdir))
			return []byte("Indexed 2 pages\n"), nil
		},
	}

	if _, err := buildWithOptions(cfg, vaultPath, outputPath, options); err != nil {
		t.Fatalf("first buildWithOptions() error = %v", err)
	}

	getRenderedNotePaths := captureRenderedNotePagePaths(t)
	getRenderedIndexPaths := captureRenderedIndexPagePaths(t)
	getRenderedTagPaths := captureRenderedTagPagePaths(t)
	getRenderedFolderPaths := captureRenderedFolderPagePaths(t)
	getRenderedTimelinePaths := captureRenderedTimelinePagePaths(t)

	result, err := buildWithOptions(cfg, vaultPath, outputPath, options)
	if err != nil {
		t.Fatalf("second buildWithOptions() error = %v", err)
	}
	if result.NotePages != 0 {
		t.Fatalf("result.NotePages = %d, want %d", result.NotePages, 0)
	}
	if pagefindIndexRuns != 2 {
		t.Fatalf("pagefind index runs = %d, want %d", pagefindIndexRuns, 2)
	}
	if got := getRenderedNotePaths(); len(got) != 0 {
		t.Fatalf("rendered note pages on no-op search build = %#v, want none", got)
	}
	if got := getRenderedIndexPaths(); len(got) != 0 {
		t.Fatalf("rendered index pages on no-op search build = %#v, want none", got)
	}
	if got := getRenderedTagPaths(); len(got) != 0 {
		t.Fatalf("rendered tag pages on no-op search build = %#v, want none", got)
	}
	if got := getRenderedFolderPaths(); len(got) != 0 {
		t.Fatalf("rendered folder pages on no-op search build = %#v, want none", got)
	}
	if got := getRenderedTimelinePaths(); len(got) != 0 {
		t.Fatalf("rendered timeline pages on no-op search build = %#v, want none", got)
	}

	indexHTML := readBuildOutputFile(t, outputPath, "index.html")
	if !containsAny(indexHTML, `href="./_pagefind/pagefind-ui.css"`, `href=./_pagefind/pagefind-ui.css`) {
		t.Fatalf("published index page missing search UI after successful Pagefind\n%s", indexHTML)
	}
	if !containsAny(indexHTML, `src="./_pagefind/pagefind-ui.js"`, `src=./_pagefind/pagefind-ui.js`) {
		t.Fatalf("published index page missing Pagefind script after successful Pagefind\n%s", indexHTML)
	}
	if !containsAny(indexHTML, `data-obsite-search-ui`) {
		t.Fatalf("published index page missing explicit framework search marker after successful Pagefind\n%s", indexHTML)
	}
	if !containsAny(indexHTML, `<div id="search"></div>`, `<div id=search></div>`) {
		t.Fatalf("published index page missing search mount after successful Pagefind\n%s", indexHTML)
	}
}

func TestBuildSearchNoOpStagingPreservesAuthorRawHTMLSearchID(t *testing.T) {
	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "notes/alpha.md", `---
title: Alpha
date: 2026-04-06
---
# Alpha

Before.

<div id="search"></div>

After.
`)

	cfg := testBuildSiteConfig()
	cfg.Search.Enabled = true
	cfg.Search.PagefindPath = "pagefind_extended"
	cfg.Search.PagefindVersion = "1.4.0"

	pagefindIndexRuns := 0
	var stagedNoteHTML []byte
	options := buildOptions{
		pagefindLookPath: func(path string) (string, error) {
			if path != "pagefind_extended" {
				t.Fatalf("pagefindLookPath() path = %q, want %q", path, "pagefind_extended")
			}
			return "/usr/local/bin/pagefind_extended", nil
		},
		pagefindCommand: func(name string, args ...string) ([]byte, error) {
			if name != "/usr/local/bin/pagefind_extended" {
				t.Fatalf("pagefindCommand() name = %q, want %q", name, "/usr/local/bin/pagefind_extended")
			}
			if len(args) == 1 && args[0] == "--version" {
				return []byte("pagefind_extended 1.4.0\n"), nil
			}
			if len(args) != 4 || args[0] != "--site" || args[2] != "--output-subdir" || args[3] != pagefindOutputSubdir {
				t.Fatalf("pagefindCommand() args = %#v, want site invocation", args)
			}

			pagefindIndexRuns++
			if pagefindIndexRuns == 2 {
				stagedNoteHTML = readBuildOutputFile(t, args[1], "alpha/index.html")
				if containsFrameworkSearchUI(stagedNoteHTML) {
					t.Fatalf("staged note unexpectedly exposes framework search UI before Pagefind succeeds\n%s", stagedNoteHTML)
				}
				if !containsAny(
					stagedNoteHTML,
					`<p>Before.</p><div id="search"></div><p>After.</p>`,
					`<p>Before.</p><div id=search></div><p>After.</p>`,
				) {
					t.Fatalf("staged note page lost author raw HTML search node\n%s", stagedNoteHTML)
				}
			}

			writeMinimalPagefindBundle(t, filepath.Join(args[1], pagefindOutputSubdir))
			return []byte("Indexed 1 page\n"), nil
		},
	}

	if _, err := buildWithOptions(cfg, vaultPath, outputPath, options); err != nil {
		t.Fatalf("first buildWithOptions() error = %v", err)
	}

	getRenderedNotePaths := captureRenderedNotePagePaths(t)
	result, err := buildWithOptions(cfg, vaultPath, outputPath, options)
	if err != nil {
		t.Fatalf("second buildWithOptions() error = %v", err)
	}
	if result.NotePages != 0 {
		t.Fatalf("result.NotePages = %d, want %d", result.NotePages, 0)
	}
	if pagefindIndexRuns != 2 {
		t.Fatalf("pagefind index runs = %d, want %d", pagefindIndexRuns, 2)
	}
	if len(stagedNoteHTML) == 0 {
		t.Fatal("pagefindCommand() did not inspect staged note HTML")
	}
	if got := getRenderedNotePaths(); len(got) != 0 {
		t.Fatalf("rendered note pages on no-op search build = %#v, want none", got)
	}
}

func TestBuildDoesNotRerenderAllPublicNotePagesWhenRelatedIsEnabledAndOnlyLastModifiedChanges(t *testing.T) {
	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	alphaPath := filepath.Join(vaultPath, "notes", "alpha.md")

	writeBuildTestFile(t, vaultPath, "notes/alpha.md", `---
title: Alpha
---
# Alpha

Body.
`)
	writeBuildTestFile(t, vaultPath, "notes/beta.md", `---
title: Beta
---
# Beta

Body.
`)
	writeBuildTestFile(t, vaultPath, "notes/gamma.md", `---
title: Gamma
---
# Gamma

Body.
`)

	firstModified := time.Date(2026, time.April, 6, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(alphaPath, firstModified, firstModified); err != nil {
		t.Fatalf("os.Chtimes(firstModified) error = %v", err)
	}

	cfg := testBuildSiteConfig()
	cfg.Related.Enabled = true

	if _, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard}); err != nil {
		t.Fatalf("first buildWithOptions() error = %v", err)
	}

	secondModified := time.Date(2026, time.April, 7, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(alphaPath, secondModified, secondModified); err != nil {
		t.Fatalf("os.Chtimes(secondModified) error = %v", err)
	}

	getRenderedPaths := captureRenderedNotePagePaths(t)
	result, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard})
	if err != nil {
		t.Fatalf("second buildWithOptions() error = %v", err)
	}
	if result.NotePages != 1 {
		t.Fatalf("result.NotePages = %d, want %d", result.NotePages, 1)
	}

	if got := getRenderedPaths(); !reflect.DeepEqual(got, []string{"notes/alpha.md"}) {
		t.Fatalf("rendered note pages = %#v, want %#v", got, []string{"notes/alpha.md"})
	}
}

func TestBuildDoesNotRerenderAllPublicNotePagesWhenSidebarIsEnabledAndOnlyBodyChanges(t *testing.T) {
	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "notes/alpha.md", `---
title: Alpha
---
# Alpha

Original body.
`)
	writeBuildTestFile(t, vaultPath, "notes/beta.md", `---
title: Beta
---
# Beta

Body.
`)

	cfg := testBuildSiteConfig()
	cfg.Sidebar.Enabled = true

	if _, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard}); err != nil {
		t.Fatalf("first buildWithOptions() error = %v", err)
	}

	writeBuildTestFile(t, vaultPath, "notes/alpha.md", `---
title: Alpha
---
# Alpha

Updated body.
`)

	getRenderedPaths := captureRenderedNotePagePaths(t)
	result, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard})
	if err != nil {
		t.Fatalf("second buildWithOptions() error = %v", err)
	}
	if result.NotePages != 1 {
		t.Fatalf("result.NotePages = %d, want %d", result.NotePages, 1)
	}

	if got := getRenderedPaths(); !reflect.DeepEqual(got, []string{"notes/alpha.md"}) {
		t.Fatalf("rendered note pages = %#v, want %#v", got, []string{"notes/alpha.md"})
	}
}

func TestBuildRerendersAllPublicNotePagesWhenSidebarDerivedSignatureChanges(t *testing.T) {
	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "notes/alpha.md", `---
title: Alpha
---
# Alpha

Body.
`)
	writeBuildTestFile(t, vaultPath, "notes/beta.md", `---
title: Beta
---
# Beta

Body.
`)

	cfg := testBuildSiteConfig()
	cfg.Sidebar.Enabled = true

	if _, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard}); err != nil {
		t.Fatalf("first buildWithOptions() error = %v", err)
	}

	writeBuildTestFile(t, vaultPath, "notes/alpha.md", `---
title: Alpha Updated
---
# Alpha

Body.
`)

	getRenderedPaths := captureRenderedNotePagePaths(t)
	result, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard})
	if err != nil {
		t.Fatalf("second buildWithOptions() error = %v", err)
	}
	if result.NotePages != 1 {
		t.Fatalf("result.NotePages = %d, want %d", result.NotePages, 1)
	}

	got := getRenderedPaths()
	want := []string{"notes/alpha.md", "notes/beta.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rendered note pages = %#v, want %#v", got, want)
	}
}

func TestBuildSelectivelyReusesArchivePagesAndPersistsPageAndDerivedSignatures(t *testing.T) {
	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "alpha/one.md", `---
title: One
date: 2026-04-06
tags:
  - alpha
---
# One

Body.
`)
	writeBuildTestFile(t, vaultPath, "beta/two.md", `---
title: Two
date: 2026-04-05
tags:
  - beta
---
# Two

Body.
`)

	cfg := testBuildSiteConfig()
	cfg.Related.Enabled = true
	cfg.Timeline.Enabled = true
	cfg.Timeline.Path = "notes"

	if _, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard}); err != nil {
		t.Fatalf("first buildWithOptions() error = %v", err)
	}

	manifestJSON := readBuildOutputFile(t, outputPath, cacheManifestRelPath)
	var manifest CacheManifest
	if err := json.Unmarshal(manifestJSON, &manifest); err != nil {
		t.Fatalf("json.Unmarshal(manifest) error = %v\n%s", err, manifestJSON)
	}
	if manifest.Version != cacheManifestVersion {
		t.Fatalf("manifest.Version = %d, want %d", manifest.Version, cacheManifestVersion)
	}
	for _, relPath := range []string{"index.html", "tags/alpha/index.html", "alpha/index.html", "notes/index.html"} {
		if strings.TrimSpace(manifest.Pages[relPath]) == "" {
			t.Fatalf("manifest.Pages[%q] = %q, want non-empty page signature", relPath, manifest.Pages[relPath])
		}
	}
	if strings.TrimSpace(manifest.Notes["alpha/one.md"].DerivedSignatures[derivedSignatureKeySidebar]) == "" {
		t.Fatalf("manifest.Notes[alpha/one.md].DerivedSignatures[%q] = %q, want non-empty signature", derivedSignatureKeySidebar, manifest.Notes["alpha/one.md"].DerivedSignatures[derivedSignatureKeySidebar])
	}
	if strings.TrimSpace(manifest.Notes["alpha/one.md"].DerivedSignatures[derivedSignatureKeyRelated]) == "" {
		t.Fatalf("manifest.Notes[alpha/one.md].DerivedSignatures[%q] = %q, want non-empty signature", derivedSignatureKeyRelated, manifest.Notes["alpha/one.md"].DerivedSignatures[derivedSignatureKeyRelated])
	}

	getRenderedIndexPaths := captureRenderedIndexPagePaths(t)
	getRenderedTagPaths := captureRenderedTagPagePaths(t)
	getRenderedFolderPaths := captureRenderedFolderPagePaths(t)
	getRenderedTimelinePaths := captureRenderedTimelinePagePaths(t)
	result, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard})
	if err != nil {
		t.Fatalf("second buildWithOptions() error = %v", err)
	}
	if result.NotePages != 0 {
		t.Fatalf("result.NotePages = %d, want %d", result.NotePages, 0)
	}
	if got := getRenderedIndexPaths(); len(got) != 0 {
		t.Fatalf("rendered index pages on no-op build = %#v, want none", got)
	}
	if got := getRenderedTagPaths(); len(got) != 0 {
		t.Fatalf("rendered tag pages on no-op build = %#v, want none", got)
	}
	if got := getRenderedFolderPaths(); len(got) != 0 {
		t.Fatalf("rendered folder pages on no-op build = %#v, want none", got)
	}
	if got := getRenderedTimelinePaths(); len(got) != 0 {
		t.Fatalf("rendered timeline pages on no-op build = %#v, want none", got)
	}

	writeBuildTestFile(t, vaultPath, "alpha/one.md", `---
title: One Updated
date: 2026-04-06
tags:
  - alpha
---
# One

Body.
`)

	getRenderedIndexPaths = captureRenderedIndexPagePaths(t)
	getRenderedTagPaths = captureRenderedTagPagePaths(t)
	getRenderedFolderPaths = captureRenderedFolderPagePaths(t)
	getRenderedTimelinePaths = captureRenderedTimelinePagePaths(t)
	result, err = buildWithOptions(cfg, vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard})
	if err != nil {
		t.Fatalf("third buildWithOptions() error = %v", err)
	}
	if result.NotePages != 1 {
		t.Fatalf("result.NotePages = %d, want %d", result.NotePages, 1)
	}
	if got := getRenderedIndexPaths(); !reflect.DeepEqual(got, []string{"index.html"}) {
		t.Fatalf("rendered index pages = %#v, want %#v", got, []string{"index.html"})
	}
	if got := getRenderedTagPaths(); !reflect.DeepEqual(got, []string{"tags/alpha/index.html"}) {
		t.Fatalf("rendered tag pages = %#v, want %#v", got, []string{"tags/alpha/index.html"})
	}
	if got := getRenderedFolderPaths(); !reflect.DeepEqual(got, []string{"alpha/index.html"}) {
		t.Fatalf("rendered folder pages = %#v, want %#v", got, []string{"alpha/index.html"})
	}
	if got := getRenderedTimelinePaths(); !reflect.DeepEqual(got, []string{"notes/index.html"}) {
		t.Fatalf("rendered timeline pages = %#v, want %#v", got, []string{"notes/index.html"})
	}
}

func TestBuildWithForceBypassesIncrementalCache(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "notes/alpha.md", `---
title: Alpha
date: 2026-04-06
---
# Alpha

Body.
`)
	writeBuildTestFile(t, vaultPath, "notes/beta.md", `---
title: Beta
date: 2026-04-06
---
# Beta

Body.
`)

	if _, err := BuildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, Options{}); err != nil {
		t.Fatalf("first BuildWithOptions() error = %v", err)
	}

	result, err := BuildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, Options{Force: true})
	if err != nil {
		t.Fatalf("forced BuildWithOptions() error = %v", err)
	}
	if result.NotePages != 2 {
		t.Fatalf("result.NotePages = %d, want %d", result.NotePages, 2)
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

func writeBuildSymlinkOrSkip(t *testing.T, targetPath string, linkPath string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(linkPath), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v", filepath.Dir(linkPath), err)
	}
	if err := os.Symlink(targetPath, linkPath); err != nil {
		t.Skipf("os.Symlink(%q, %q) unsupported: %v", targetPath, linkPath, err)
	}
}

func readSidebarNodesFromHTML(t *testing.T, html []byte) []model.SidebarNode {
	t.Helper()

	doc, err := xhtml.Parse(bytes.NewReader(html))
	if err != nil {
		t.Fatalf("xhtml.Parse() error = %v", err)
	}

	payload := findScriptTextByID(doc, "sidebar-data")
	if payload == "" {
		t.Fatalf("sidebar-data script payload missing\n%s", html)
	}

	var nodes []model.SidebarNode
	if err := json.Unmarshal([]byte(payload), &nodes); err != nil {
		t.Fatalf("json.Unmarshal(sidebar-data) error = %v\npayload: %s", err, payload)
	}

	return nodes
}

func findScriptTextByID(node *xhtml.Node, id string) string {
	if node == nil {
		return ""
	}

	if node.Type == xhtml.ElementNode && node.Data == "script" && htmlAttrValue(node, "id") == id {
		var builder strings.Builder
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if child.Type == xhtml.TextNode {
				builder.WriteString(child.Data)
			}
		}
		return builder.String()
	}

	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if found := findScriptTextByID(child, id); found != "" {
			return found
		}
	}

	return ""
}

func htmlAttrValue(node *xhtml.Node, key string) string {
	if node == nil {
		return ""
	}

	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, key) {
			return attr.Val
		}
	}

	return ""
}

func findSidebarNodeByURL(nodes []model.SidebarNode, url string) *model.SidebarNode {
	for index := range nodes {
		if nodes[index].URL == url {
			return &nodes[index]
		}
		if found := findSidebarNodeByURL(nodes[index].Children, url); found != nil {
			return found
		}
	}

	return nil
}

func hasActiveSidebarNode(nodes []model.SidebarNode) bool {
	for _, node := range nodes {
		if node.IsActive || hasActiveSidebarNode(node.Children) {
			return true
		}
	}

	return false
}

func captureRenderedNotePagePaths(t *testing.T) func() []string {
	t.Helper()

	originalRenderNotePage := renderNotePage
	rendered := make([]string, 0, 8)
	renderNotePage = func(input internalrender.NotePageInput) (internalrender.RenderedPage, error) {
		if input.Note != nil {
			rendered = append(rendered, input.Note.RelPath)
		}
		return originalRenderNotePage(input)
	}
	t.Cleanup(func() {
		renderNotePage = originalRenderNotePage
	})

	return func() []string {
		return uniqueSortedStrings(rendered)
	}
}

func captureRenderedNotePageCalls(t *testing.T) func() []string {
	t.Helper()

	originalRenderNotePage := renderNotePage
	rendered := make([]string, 0, 8)
	renderNotePage = func(input internalrender.NotePageInput) (internalrender.RenderedPage, error) {
		if input.Note != nil {
			rendered = append(rendered, input.Note.RelPath)
		}
		return originalRenderNotePage(input)
	}
	t.Cleanup(func() {
		renderNotePage = originalRenderNotePage
	})

	return func() []string {
		return sortedStrings(rendered)
	}
}

func captureRenderedIndexPagePaths(t *testing.T) func() []string {
	t.Helper()

	originalRenderIndexPage := renderIndexPage
	rendered := make([]string, 0, 4)
	renderIndexPage = func(input internalrender.IndexPageInput) (internalrender.RenderedPage, error) {
		rendered = append(rendered, input.RelPath)
		return originalRenderIndexPage(input)
	}
	t.Cleanup(func() {
		renderIndexPage = originalRenderIndexPage
	})

	return func() []string {
		return uniqueSortedStrings(rendered)
	}
}

func captureRenderedTagPagePaths(t *testing.T) func() []string {
	t.Helper()

	originalRenderTagPage := renderTagPage
	rendered := make([]string, 0, 4)
	renderTagPage = func(input internalrender.TagPageInput) (internalrender.RenderedPage, error) {
		rendered = append(rendered, input.RelPath)
		return originalRenderTagPage(input)
	}
	t.Cleanup(func() {
		renderTagPage = originalRenderTagPage
	})

	return func() []string {
		return uniqueSortedStrings(rendered)
	}
}

func captureRenderedFolderPagePaths(t *testing.T) func() []string {
	t.Helper()

	originalRenderFolderPage := renderFolderPage
	rendered := make([]string, 0, 4)
	renderFolderPage = func(input internalrender.FolderPageInput) (internalrender.RenderedPage, error) {
		rendered = append(rendered, input.RelPath)
		return originalRenderFolderPage(input)
	}
	t.Cleanup(func() {
		renderFolderPage = originalRenderFolderPage
	})

	return func() []string {
		return uniqueSortedStrings(rendered)
	}
}

func captureRenderedTimelinePagePaths(t *testing.T) func() []string {
	t.Helper()

	originalRenderTimelinePage := renderTimelinePage
	rendered := make([]string, 0, 4)
	renderTimelinePage = func(input internalrender.TimelinePageInput) (internalrender.RenderedPage, error) {
		rendered = append(rendered, input.RelPath)
		return originalRenderTimelinePage(input)
	}
	t.Cleanup(func() {
		renderTimelinePage = originalRenderTimelinePage
	})

	return func() []string {
		return uniqueSortedStrings(rendered)
	}
}

func sortedStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	ordered := append([]string(nil), values...)
	sort.Strings(ordered)
	return ordered
}

func uniqueSortedStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	ordered := sortedStrings(values)
	result := ordered[:0]
	for _, value := range ordered {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return append([]string(nil), result...)
}

func countStrings(values []string) map[string]int {
	if len(values) == 0 {
		return map[string]int{}
	}

	counts := make(map[string]int, len(values))
	for _, value := range values {
		counts[value]++
	}
	return counts
}

func readBuildOutputFile(t *testing.T, outputRoot string, relPath string) []byte {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(outputRoot, filepath.FromSlash(relPath)))
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error = %v", relPath, err)
	}
	return data
}

func writeMinimalPagefindBundle(t *testing.T, bundlePath string) {
	t.Helper()

	files := map[string]string{
		"pagefind-ui.css":              ".pagefind-ui{display:block}",
		"pagefind-ui.js":               "window.PagefindUI=function(){};",
		"pagefind.js":                  "window.__pagefind=function(){};",
		"pagefind-highlight.js":        "window.__pagefindHighlight=function(){};",
		"pagefind-worker.js":           "self.onmessage=function(){};",
		"pagefind-entry.json":          `{"version":"1.4.0","languages":{"en":{"hash":"en-test","page_count":1}}}`,
		"pagefind.en-test.pf_meta":     "meta",
		"index/en-test.pf_index":       "index",
		"fragment/en-test.pf_fragment": "fragment",
		"wasm.unknown.pagefind":        "wasm",
	}

	for relPath, contents := range files {
		path := filepath.Join(bundlePath, filepath.FromSlash(relPath))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("os.MkdirAll(%q) error = %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatalf("os.WriteFile(%q) error = %v", path, err)
		}
	}
}

func containsAny(data []byte, snippets ...string) bool {
	return indexAny(data, snippets...) >= 0
}

func containsFrameworkSearchUI(data []byte) bool {
	return containsAny(
		data,
		`data-obsite-search-ui`,
		`pagefind-ui.css`,
		`pagefind-ui.js`,
		`new PagefindUI({ element: "#search" });`,
		`new PagefindUI({element:"#search"})`,
	)
}

func indexAny(data []byte, snippets ...string) int {
	for _, snippet := range snippets {
		if index := bytes.Index(data, []byte(snippet)); index >= 0 {
			return index
		}
	}
	return -1
}

func anchorAttrValueByHref(t *testing.T, html []byte, href string, attr string) (string, bool) {
	t.Helper()

	doc, err := xhtml.Parse(bytes.NewReader(html))
	if err != nil {
		t.Fatalf("html.Parse() error = %v", err)
	}

	anchor := findAnchorByHref(doc, href)
	if anchor == nil {
		t.Fatalf("anchor with href %q not found\n%s", href, html)
	}

	for _, candidate := range anchor.Attr {
		if strings.EqualFold(candidate.Key, attr) {
			return candidate.Val, true
		}
	}

	return "", false
}

func findAnchorByHref(node *xhtml.Node, href string) *xhtml.Node {
	if node == nil {
		return nil
	}

	if node.Type == xhtml.ElementNode && strings.EqualFold(node.Data, "a") {
		for _, attr := range node.Attr {
			if strings.EqualFold(attr.Key, "href") && attr.Val == href {
				return node
			}
		}
	}

	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if found := findAnchorByHref(child, href); found != nil {
			return found
		}
	}

	return nil
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
