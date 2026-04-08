package build

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	internalconfig "github.com/simp-lee/obsite/internal/config"
	"github.com/simp-lee/obsite/internal/diag"
	internalmodel "github.com/simp-lee/obsite/internal/model"
	internalserver "github.com/simp-lee/obsite/internal/server"
	xhtml "golang.org/x/net/html"
)

func TestBuildIntegrationPublishedFixtureProducesDeployableSite(t *testing.T) {
	t.Parallel()

	vaultPath := copyFixtureVault(t, "site-vault")
	outputPath := filepath.Join(t.TempDir(), "site")

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
	if result.NotePages != 7 {
		t.Fatalf("result.NotePages = %d, want %d", result.NotePages, 7)
	}
	if result.TagPages != 3 {
		t.Fatalf("result.TagPages = %d, want %d", result.TagPages, 3)
	}

	for _, relPath := range []string{
		"index.html",
		"404.html",
		"style.css",
		"sitemap.xml",
		"robots.txt",
		"launch-pad/index.html",
		"body-html/index.html",
		"shared-note/index.html",
		"cycle-a/index.html",
		"cycle-b/index.html",
		"duplicate-north/index.html",
		"duplicate-south/index.html",
		"tags/field/index.html",
		"tags/parent/index.html",
		"tags/parent/child/index.html",
		"assets/hero.png",
		"assets/photo.png",
		"assets/diagram.png",
	} {
		_ = readBuildOutputFile(t, outputPath, relPath)
	}
	assertPathMissing(t, filepath.Join(outputPath, "private", "index.html"))
	assertPathMissing(t, filepath.Join(outputPath, ".obsidian", "plugins", "ignored.txt"))

	landingHTML := readBuildOutputFile(t, outputPath, "launch-pad/index.html")
	if !containsAny(landingHTML,
		`href="../body-html/"`,
		`href=../body-html/`,
	) {
		t.Fatalf("launch-pad page missing relative link to body-html\n%s", landingHTML)
	}
	if !containsAny(landingHTML,
		`href="../shared-note/"`,
		`href=../shared-note/`,
	) {
		t.Fatalf("launch-pad page missing relative link to shared-note\n%s", landingHTML)
	}
	if !containsAny(landingHTML,
		`href="../shared-note/#shared-section">section jump</a>`,
		`href=../shared-note/#shared-section>section jump</a>`,
	) {
		t.Fatalf("launch-pad page missing heading fragment wikilink\n%s", landingHTML)
	}
	if !containsAny(landingHTML,
		`href="../tags/parent/child/"`,
		`href=../tags/parent/child/`,
	) {
		t.Fatalf("launch-pad page missing nested tag URL\n%s", landingHTML)
	}
	if !containsAny(landingHTML,
		`href="../duplicate-north/"`,
		`href=../duplicate-north/`,
	) {
		t.Fatalf("launch-pad page missing deterministic ambiguous wikilink target\n%s", landingHTML)
	}
	if containsAny(landingHTML,
		`href="../duplicate-south/"`,
		`href=../duplicate-south/`,
	) {
		t.Fatalf("launch-pad page resolved ambiguous wikilink to non-deterministic target\n%s", landingHTML)
	}
	if containsAny(landingHTML, `href="../private/"`, `href=../private/`) {
		t.Fatalf("launch-pad page unexpectedly links to unpublished note\n%s", landingHTML)
	}
	if containsAny(landingHTML, `href="../missing-page/"`, `href=../missing-page/`) {
		t.Fatalf("launch-pad page unexpectedly links to missing page\n%s", landingHTML)
	}
	if !containsAny(landingHTML,
		`href="../style.css"`,
		`href=../style.css`,
	) {
		t.Fatalf("launch-pad page missing relative stylesheet href\n%s", landingHTML)
	}
	if !containsAny(landingHTML,
		`src="../assets/hero.png"`,
		`src=../assets/hero.png`,
	) {
		t.Fatalf("launch-pad page missing rewritten markdown image path\n%s", landingHTML)
	}
	if !containsAny(landingHTML,
		`src="../assets/photo.png"`,
		`src=../assets/photo.png`,
	) {
		t.Fatalf("launch-pad page missing rewritten image embed path\n%s", landingHTML)
	}
	if !containsAny(landingHTML, `width="480"`, `width=480`) {
		t.Fatalf("launch-pad page missing image embed width\n%s", landingHTML)
	}
	if !containsAny(landingHTML,
		`src="../assets/diagram.png"`,
		`src=../assets/diagram.png`,
	) {
		t.Fatalf("launch-pad page missing asset discovered through embedded note\n%s", landingHTML)
	}
	if !containsAny(landingHTML,
		`class="callout callout-note"`,
		`class=callout callout-note`,
	) {
		t.Fatalf("launch-pad page missing callout output\n%s", landingHTML)
	}
	if !bytes.Contains(landingHTML, []byte(`<mark>highlighted text</mark>`)) {
		t.Fatalf("launch-pad page missing highlight output\n%s", landingHTML)
	}
	if bytes.Contains(landingHTML, []byte("hidden comment")) || bytes.Contains(landingHTML, []byte("Ignore Me")) {
		t.Fatalf("launch-pad page leaked stripped comment text\n%s", landingHTML)
	}
	if !containsAny(landingHTML,
		`class="math math-inline"`,
		`class=math math-inline`,
	) {
		t.Fatalf("launch-pad page missing inline math output\n%s", landingHTML)
	}
	if !containsAny(landingHTML,
		`class="math math-display"`,
		`class=math math-display`,
	) {
		t.Fatalf("launch-pad page missing display math output\n%s", landingHTML)
	}
	if !containsAny(landingHTML,
		`<pre class="mermaid">`,
		`<pre class=mermaid>`,
	) {
		t.Fatalf("launch-pad page missing Mermaid container\n%s", landingHTML)
	}
	if !containsAny(landingHTML,
		`class="unsupported-syntax unsupported-dataview"`,
		`class=unsupported-syntax unsupported-dataview`,
	) {
		t.Fatalf("launch-pad page missing Dataview degradation output\n%s", landingHTML)
	}
	if got := bytes.Count(landingHTML, []byte("Shared section intro")); got != 2 {
		t.Fatalf("launch-pad page rendered %d shared-section intro copies, want 2 from full-note and heading embeds\n%s", got, landingHTML)
	}
	if got := bytes.Count(landingHTML, []byte("More shared content for the full-note embed.")); got != 1 {
		t.Fatalf("launch-pad page rendered %d full-note-only embed copies, want 1\n%s", got, landingHTML)
	}
	if !containsAny(landingHTML,
		`rel="canonical" href="https://example.com/blog/launch-pad/"`,
		`rel=canonical href=https://example.com/blog/launch-pad/`,
	) {
		t.Fatalf("launch-pad page missing canonical URL\n%s", landingHTML)
	}
	if !containsAny(landingHTML,
		`property="og:url" content="https://example.com/blog/launch-pad/"`,
		`property=og:url content=https://example.com/blog/launch-pad/`,
	) {
		t.Fatalf("launch-pad page missing Open Graph URL\n%s", landingHTML)
	}
	if !containsAny(landingHTML,
		`type="application/ld+json"`,
		`type=application/ld+json`,
	) {
		t.Fatalf("launch-pad page missing JSON-LD script\n%s", landingHTML)
	}

	sharedHTML := readBuildOutputFile(t, outputPath, "shared-note/index.html")
	if !containsAny(sharedHTML,
		`<h2 id="shared-section">`,
		`<h2 id=shared-section>`,
	) {
		t.Fatalf("shared-note page missing shared-section anchor\n%s", sharedHTML)
	}

	indexHTML := readBuildOutputFile(t, outputPath, "index.html")
	if bytes.Contains(indexHTML, []byte("Private Note")) {
		t.Fatalf("index page unexpectedly references unpublished note\n%s", indexHTML)
	}

	bodyHTML := readBuildOutputFile(t, outputPath, "body-html/index.html")
	if !bytes.Contains(bodyHTML, []byte(`<sup>inline raw</sup>`)) {
		t.Fatalf("body-html page missing inline raw HTML passthrough\n%s", bodyHTML)
	}
	if bytes.Count(bodyHTML, []byte(`<h1`)) != 1 {
		t.Fatalf("body-html page rendered %d h1 elements, want 1 page-level heading\n%s", bytes.Count(bodyHTML, []byte(`<h1`)), bodyHTML)
	}
	if !containsAny(bodyHTML,
		`<h1 class="page-title" id="body-html">body-html</h1>`,
		`<h1 class=page-title id=body-html>body-html</h1>`,
	) {
		t.Fatalf("body-html page missing promoted top-level heading id\n%s", bodyHTML)
	}
	if !bytes.Contains(bodyHTML, []byte(`<details><summary>Expand</summary>`)) ||
		!bytes.Contains(bodyHTML, []byte(`Block raw html stays in body.`)) {
		t.Fatalf("body-html page missing block raw HTML passthrough\n%s", bodyHTML)
	}

	headHTML := htmlHead(t, bodyHTML)
	if bytes.Contains(headHTML, []byte(`<sup>`)) || bytes.Contains(headHTML, []byte(`<details>`)) {
		t.Fatalf("raw HTML leaked into page head metadata\n%s", headHTML)
	}
	description := mustRegexSubmatch(t, headHTML, `(?is)<meta[^>]*name=?"?description"?[^>]*content="([^"]*)"`)
	if !strings.Contains(description, "HTML fallback note with inline raw markup.") {
		t.Fatalf("meta description = %q, want stripped summary fallback", description)
	}
	if strings.Contains(description, "<") || strings.Contains(description, ">") {
		t.Fatalf("meta description = %q, want no raw HTML tags", description)
	}
	jsonLD := mustRegexSubmatch(t, bodyHTML, `(?is)<script type=?"?application/ld\+json"?>(.*?)</script>`)
	if !strings.Contains(jsonLD, `"@type":"Article"`) || !strings.Contains(jsonLD, `"@type":"BreadcrumbList"`) {
		t.Fatalf("JSON-LD = %q, want Article and BreadcrumbList payload", jsonLD)
	}
	if strings.Contains(jsonLD, `<sup>`) || strings.Contains(jsonLD, `<details>`) {
		t.Fatalf("JSON-LD = %q, want raw HTML stripped from metadata fallbacks", jsonLD)
	}

	sitemapXML := readBuildOutputFile(t, outputPath, "sitemap.xml")
	for _, want := range []string{
		"https://example.com/blog/",
		"https://example.com/blog/launch-pad/",
		"https://example.com/blog/body-html/",
		"https://example.com/blog/shared-note/",
		"https://example.com/blog/tags/field/",
		"https://example.com/blog/tags/parent/",
		"https://example.com/blog/tags/parent/child/",
	} {
		if !bytes.Contains(sitemapXML, []byte(want)) {
			t.Fatalf("sitemap.xml missing %q\n%s", want, sitemapXML)
		}
	}
	if bytes.Contains(sitemapXML, []byte("private")) {
		t.Fatalf("sitemap.xml unexpectedly includes unpublished note\n%s", sitemapXML)
	}

	robotsTXT := readBuildOutputFile(t, outputPath, "robots.txt")
	if !bytes.Contains(robotsTXT, []byte("Sitemap: https://example.com/blog/sitemap.xml")) {
		t.Fatalf("robots.txt missing sitemap URL\n%s", robotsTXT)
	}

	summary := diagnostics.String()
	for _, want := range []string{
		`wikilink "Missing Page" could not be resolved`,
		`dataview fenced code block is not supported; rendering as plain preformatted text`,
		`wikilink "private" points to unpublished note`,
		`notes/launch-pad.md:13 [ambiguous_wikilink] wikilink "duplicate" matched multiple notes at the same path distance (notes/alpha/duplicate.md, notes/beta/duplicate.md); choosing "notes/alpha/duplicate.md"`,
		`note embed "cycle-a" would create a transclusion cycle`,
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("diagnostics summary missing %q\n%s", want, summary)
		}
	}
	if strings.Contains(summary, "Fatal build errors") {
		t.Fatalf("diagnostics summary unexpectedly reports fatal errors\n%s", summary)
	}

	handler := http.NewServeMux()
	handler.Handle("/blog/", http.StripPrefix("/blog", http.FileServer(http.Dir(outputPath))))
	deployed := httptest.NewServer(handler)
	defer deployed.Close()

	mustHTTPStatus(t, deployed.Client(), deployed.URL+"/blog/launch-pad/", http.StatusOK, "Launch Pad")
	mustHTTPStatus(t, deployed.Client(), deployed.URL+"/blog/style.css", http.StatusOK, "")
	heroBody := mustHTTPStatus(t, deployed.Client(), deployed.URL+"/blog/assets/hero.png", http.StatusOK, "")
	if string(heroBody) != "hero-image" {
		t.Fatalf("hero asset body = %q, want %q", string(heroBody), "hero-image")
	}
	mustHTTPStatus(t, deployed.Client(), deployed.URL+"/blog/body-html/", http.StatusOK, "HTML fallback note")
	mustHTTPStatus(t, deployed.Client(), deployed.URL+"/blog/tags/parent/child/", http.StatusOK, "Tag archive")
}

func TestBuildIntegrationPreviewServerUsesBuiltOutput(t *testing.T) {
	t.Parallel()

	vaultPath := copyFixtureVault(t, "site-vault")
	outputPath := filepath.Join(t.TempDir(), "site")
	if _, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard}); err != nil {
		t.Fatalf("buildWithOptions() error = %v", err)
	}

	srv, err := internalserver.New(outputPath, internalserver.DefaultPort)
	if err != nil {
		t.Fatalf("server.New() error = %v", err)
	}

	preview := httptest.NewServer(srv)
	defer preview.Close()

	redirectClient := &http.Client{
		Transport: preview.Client().Transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, body := mustHTTPResponse(t, redirectClient, preview.URL+"/launch-pad")
	if resp.StatusCode != http.StatusMovedPermanently {
		t.Fatalf("GET /launch-pad status = %d, want %d", resp.StatusCode, http.StatusMovedPermanently)
	}
	if location := resp.Header.Get("Location"); location != "/launch-pad/" {
		t.Fatalf("GET /launch-pad Location = %q, want %q", location, "/launch-pad/")
	}
	if !bytes.Contains(body, []byte("Moved Permanently")) {
		t.Fatalf("GET /launch-pad body = %q, want redirect body", string(body))
	}

	mustHTTPStatus(t, preview.Client(), preview.URL+"/launch-pad/", http.StatusOK, "Launch Pad")
	missingBody := mustHTTPStatus(t, preview.Client(), preview.URL+"/missing/path", http.StatusNotFound, "The note you asked for is not here.")
	if !bytes.Contains(missingBody, []byte(`<base href="/">`)) {
		t.Fatalf("preview 404 page missing injected base href\n%s", missingBody)
	}
}

func TestBuildIntegrationInitCommandGeneratesParseableCommentedConfig(t *testing.T) {
	t.Parallel()

	vaultPath := filepath.Join(t.TempDir(), "fresh-vault")
	runObsiteCLI(t, "init", "--vault", vaultPath)

	configPath := filepath.Join(vaultPath, "obsite.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error = %v", configPath, err)
	}
	content := string(data)
	for _, want := range []string{
		"# Obsite site configuration.",
		"# baseURL must be the public site URL used for canonical links and sitemap entries.",
		"baseURL: https://example.com/",
		"title: My Obsite Site",
		"author: Your Name",
		"description: Notes published with obsite.",
		"defaultPublish: true",
		"search:",
		"pagefindPath: pagefind_extended",
		"pagefindVersion: 1.4.0",
		"pagination:",
		"pageSize: 20",
		"related:",
		"count: 5",
		"rss:",
		"enabled: true",
		"timeline:",
		"path: notes",
		"templateDir:",
		"customCSS:",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("generated config missing %q\n%s", want, content)
		}
	}

	cfg, err := internalconfig.Load(configPath, internalconfig.Overrides{VaultPath: vaultPath})
	if err != nil {
		t.Fatalf("config.Load(%q) error = %v", configPath, err)
	}
	if cfg.BaseURL != "https://example.com/" {
		t.Fatalf("cfg.BaseURL = %q, want %q", cfg.BaseURL, "https://example.com/")
	}
	if cfg.Title != "My Obsite Site" {
		t.Fatalf("cfg.Title = %q, want %q", cfg.Title, "My Obsite Site")
	}
	if cfg.Author != "Your Name" {
		t.Fatalf("cfg.Author = %q, want %q", cfg.Author, "Your Name")
	}
	if cfg.Description != "Notes published with obsite." {
		t.Fatalf("cfg.Description = %q, want %q", cfg.Description, "Notes published with obsite.")
	}
	if !cfg.DefaultPublish {
		t.Fatal("cfg.DefaultPublish = false, want true")
	}
	if cfg.Search.PagefindPath != "pagefind_extended" || cfg.Search.PagefindVersion != "1.4.0" {
		t.Fatalf("cfg.Search = %#v, want default Pagefind settings", cfg.Search)
	}
	if cfg.Pagination.PageSize != 20 {
		t.Fatalf("cfg.Pagination.PageSize = %d, want %d", cfg.Pagination.PageSize, 20)
	}
	if cfg.Related.Count != 5 {
		t.Fatalf("cfg.Related.Count = %d, want %d", cfg.Related.Count, 5)
	}
	if !cfg.RSS.Enabled {
		t.Fatal("cfg.RSS.Enabled = false, want true")
	}
	if cfg.Timeline.Enabled || cfg.Timeline.AsHomepage || cfg.Timeline.Path != "notes" {
		t.Fatalf("cfg.Timeline = %#v, want disabled timeline defaults", cfg.Timeline)
	}
}

func TestBuildIntegrationFeatureFixtureCoversAdvancedSiteFeatures(t *testing.T) {
	t.Parallel()

	vaultPath := copyFixtureVault(t, "feature-vault")
	outputPath := filepath.Join(t.TempDir(), "site")
	configPath := filepath.Join(vaultPath, "obsite.yaml")

	cfg, err := internalconfig.Load(configPath, internalconfig.Overrides{VaultPath: vaultPath})
	if err != nil {
		t.Fatalf("config.Load(%q) error = %v", configPath, err)
	}
	if cfg.TemplateDir != filepath.Join(vaultPath, "templates") {
		t.Fatalf("cfg.TemplateDir = %q, want %q", cfg.TemplateDir, filepath.Join(vaultPath, "templates"))
	}
	if cfg.CustomCSS != filepath.Join(vaultPath, "custom.css") {
		t.Fatalf("cfg.CustomCSS = %q, want %q", cfg.CustomCSS, filepath.Join(vaultPath, "custom.css"))
	}

	var diagnostics bytes.Buffer
	var pagefindCalls [][]string
	result, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{
		concurrency:       2,
		diagnosticsWriter: &diagnostics,
		pagefindLookPath: func(name string) (string, error) {
			if name != cfg.Search.PagefindPath {
				t.Fatalf("pagefindLookPath() name = %q, want %q", name, cfg.Search.PagefindPath)
			}
			return "/usr/local/bin/pagefind_extended", nil
		},
		pagefindCommand: func(name string, args ...string) ([]byte, error) {
			pagefindCalls = append(pagefindCalls, append([]string{name}, args...))
			if name != "/usr/local/bin/pagefind_extended" {
				t.Fatalf("pagefindCommand() name = %q, want %q", name, "/usr/local/bin/pagefind_extended")
			}
			if len(args) == 1 && args[0] == "--version" {
				return []byte("pagefind_extended 1.4.0\n"), nil
			}
			if len(args) != 4 || args[0] != "--site" || args[2] != "--output-subdir" || args[3] != pagefindOutputSubdir {
				t.Fatalf("pagefindCommand() args = %#v, want [--site <path> --output-subdir %s]", args, pagefindOutputSubdir)
			}
			writeMinimalPagefindBundle(t, filepath.Join(args[1], pagefindOutputSubdir))
			return []byte("Indexed 5 pages\n"), nil
		},
	})
	if err != nil {
		t.Fatalf("buildWithOptions() error = %v", err)
	}
	if result == nil {
		t.Fatal("buildWithOptions() = nil result, want build result")
	}
	if result.NotePages != 5 {
		t.Fatalf("result.NotePages = %d, want %d", result.NotePages, 5)
	}
	if len(pagefindCalls) != 2 {
		t.Fatalf("len(pagefindCalls) = %d, want %d", len(pagefindCalls), 2)
	}
	if strings.TrimSpace(diagnostics.String()) != "" {
		t.Fatalf("diagnostics summary = %q, want empty summary for advanced feature fixture", diagnostics.String())
	}

	t.Run("artifacts", func(t *testing.T) {
		styleCSS := readBuildOutputFile(t, outputPath, "style.css")
		for _, want := range []string{
			`:root[data-theme=dark]{`,
			`--theme-toggle-bg:`,
			`@media(prefers-color-scheme:dark){`,
		} {
			if !bytes.Contains(styleCSS, []byte(want)) {
				t.Fatalf("style.css missing %q\n%s", want, styleCSS)
			}
		}

		customCSS := readBuildOutputFile(t, outputPath, "assets/custom.css")
		if len(bytes.TrimSpace(customCSS)) == 0 {
			t.Fatal("assets/custom.css = empty, want copied fixture stylesheet")
		}
		if !regexp.MustCompile(`(?s)\bbody\s*\{[^}]*\boutline:\s*3px\s+solid\s+rgb\(12,\s*34,\s*56\)\s*;?[^}]*\}`).Match(customCSS) {
			t.Fatalf("assets/custom.css missing body outline rule\n%s", customCSS)
		}

		rssXML := readBuildOutputFile(t, outputPath, "index.xml")
		for _, want := range []string{
			`<rss`,
			`https://example.com/blog/updated-story/`,
			`<title>Updated Story</title>`,
		} {
			if !bytes.Contains(rssXML, []byte(want)) {
				t.Fatalf("index.xml missing %q\n%s", want, rssXML)
			}
		}

		popoverJSON := readBuildOutputFile(t, outputPath, "_popover/updated-story.json")
		popover := mustUnmarshalJSON[popoverPreviewPayload](t, popoverJSON)
		if popover.Title != "Updated Story" {
			t.Fatalf("_popover/updated-story.json title = %q, want %q", popover.Title, "Updated Story")
		}
		if len(popover.Tags) != 1 || popover.Tags[0] != "field" {
			t.Fatalf("_popover/updated-story.json tags = %#v, want %#v", popover.Tags, []string{"field"})
		}
		if !strings.Contains(popover.Summary, "static site generator publishes linked notes") ||
			!strings.Contains(popover.Summary, "exposes summaries for previews") {
			t.Fatalf("_popover/updated-story.json summary = %q, want stable excerpt phrases", popover.Summary)
		}

		for _, relPath := range []string{
			"_pagefind/pagefind-entry.json",
			"_pagefind/pagefind-ui.css",
			"_pagefind/pagefind-ui.js",
			"_pagefind/index/en-test.pf_index",
			"_pagefind/fragment/en-test.pf_fragment",
			"page/2/index.html",
			"page/3/index.html",
			"notes/index.html",
			"notes/garden/index.html",
			"notes/garden/page/2/index.html",
			"tags/field/index.html",
			"tags/field/page/2/index.html",
		} {
			_ = readBuildOutputFile(t, outputPath, relPath)
		}
	})

	t.Run("note page", func(t *testing.T) {
		updatedHTML := readBuildOutputFile(t, outputPath, "updated-story/index.html")

		for _, snippets := range [][]string{
			{`data-theme-toggle`, `data-theme-toggle=""`},
			{`__obsiteInitThemeToggle`, `window.__obsiteInitThemeToggle`},
			{`localStorage.getItem(storageKey)`},
			{`rel="alternate" type="application/rss+xml" title="Feature Garden RSS" href="../index.xml"`, `rel=alternate type=application/rss+xml title="Feature Garden RSS" href=../index.xml`},
			{`href="../assets/custom.css"`, `href=../assets/custom.css`},
			{`href="../_pagefind/pagefind-ui.css"`, `href=../_pagefind/pagefind-ui.css`},
			{`data-obsite-search-ui`},
			{`1 min read`},
			{`<span class="meta-label">Published</span>`, `<span class=meta-label>Published</span>`},
			{`04 Apr 2026`},
			{`<span class="meta-label">Updated</span>`, `<span class=meta-label>Updated</span>`},
			{`08 Apr 2026`},
			{`<nav class="toc-nav" aria-label="Contents">`, `<nav class=toc-nav aria-label=Contents>`},
			{`href="#overview"`, `href=#overview`},
			{`href="#deep-dive"`, `href=#deep-dive`},
			{`<h2 id="overview">Overview</h2>`, `<h2 id=overview>Overview</h2>`},
			{`<h3 id="deep-dive">Deep Dive</h3>`, `<h3 id=deep-dive>Deep Dive</h3>`},
			{`<figure>`, `<figure `},
			{`src="../assets/cover.png"`, `src=../assets/cover.png`},
			{`<figcaption><p>Caption with <strong>bold</strong> detail.`, `<figcaption><p>Caption with <strong>bold</strong> detail</figcaption>`},
			{`<iframe src="https://www.youtube.com/embed/dQw4w9WgXcQ"`, `<iframe src=https://www.youtube.com/embed/dQw4w9WgXcQ`},
			{`allowfullscreen`},
			{`data-popover-path="reference-guide"`, `data-popover-path=reference-guide`},
			{`id="related-articles-heading">Related Articles</h2>`, `id=related-articles-heading>Related Articles</h2>`},
		} {
			if !containsAny(updatedHTML, snippets...) {
				t.Fatalf("updated-story page missing one of %#v\n%s", snippets, updatedHTML)
			}
		}
		if !containsAny(updatedHTML,
			`href="../reference-guide/"`,
			`href=../reference-guide/`,
			`href="../roadmap/"`,
			`href=../roadmap/`,
		) {
			t.Fatalf("updated-story page missing non-empty related articles list\n%s", updatedHTML)
		}

		assertVisibleBreadcrumbs(t, updatedHTML, []internalmodel.Breadcrumb{
			{Name: "Home", URL: "../"},
			{Name: "notes", URL: "../notes/"},
			{Name: "garden", URL: "../notes/garden/"},
			{Name: "Updated Story"},
		})

		jsonLD := mustJSONLDPayloads(t, updatedHTML)
		if !hasStructuredDataType(jsonLD, "Article") {
			t.Fatalf("updated-story JSON-LD missing Article payload\n%s", mustScriptText(t, updatedHTML, func(node *xhtml.Node) bool {
				return integrationHTMLAttrValue(node, "type") == "application/ld+json"
			}, "application/ld+json"))
		}
		assertBreadcrumbListItems(t, jsonLD, []internalmodel.Breadcrumb{
			{Name: "Home", URL: "https://example.com/blog/"},
			{Name: "notes", URL: "https://example.com/blog/notes/"},
			{Name: "garden", URL: "https://example.com/blog/notes/garden/"},
			{Name: "Updated Story", URL: "https://example.com/blog/updated-story/"},
		})

		sidebarTree := mustSidebarTree(t, updatedHTML)
		notesNode := mustSidebarNode(t, sidebarTree, "notes")
		if !notesNode.IsDir || notesNode.URL != "notes/" || notesNode.IsActive || len(notesNode.Children) == 0 {
			t.Fatalf("sidebar notes node = %#v, want nested inactive directory at notes/", *notesNode)
		}
		gardenNode := mustSidebarNode(t, sidebarTree, "notes", "garden")
		if !gardenNode.IsDir || gardenNode.URL != "notes/garden/" || gardenNode.IsActive || len(gardenNode.Children) < 3 {
			t.Fatalf("sidebar garden node = %#v, want nested inactive directory with note children", *gardenNode)
		}
		updatedNode := mustSidebarNode(t, sidebarTree, "notes", "garden", "Updated Story")
		if updatedNode.IsDir || updatedNode.URL != "updated-story/" || !updatedNode.IsActive {
			t.Fatalf("sidebar updated-story node = %#v, want active note leaf", *updatedNode)
		}
		referenceNode := mustSidebarNode(t, sidebarTree, "notes", "garden", "Reference Guide")
		if referenceNode.IsDir || referenceNode.IsActive {
			t.Fatalf("sidebar reference-guide node = %#v, want inactive note sibling", *referenceNode)
		}
	})

	t.Run("folders and pagination", func(t *testing.T) {
		indexHTML := readBuildOutputFile(t, outputPath, "index.html")
		if !containsAny(indexHTML,
			`body class=kind-timeline`,
			`class="page-shell timeline-page"`,
			`class=page-shell timeline-page`,
		) {
			t.Fatalf("homepage missing timeline layout markers\n%s", indexHTML)
		}
		if containsAny(indexHTML,
			`body class=kind-index`,
			`class="page-shell landing-page"`,
			`class=page-shell landing-page`,
		) {
			t.Fatalf("homepage unexpectedly rendered default index layout\n%s", indexHTML)
		}
		if !containsAny(indexHTML,
			`href="page/2/" rel="next">Next</a>`,
			`href=page/2/ rel=next>Next</a>`,
			`<link rel="next" href="page/2/">`,
			`<link rel=next href=page/2/>`,
		) {
			t.Fatalf("homepage missing pagination to page 2\n%s", indexHTML)
		}

		indexPageTwoHTML := readBuildOutputFile(t, outputPath, "page/2/index.html")
		for _, snippets := range [][]string{
			{`<link rel="prev" href="../../">`, `<link rel=prev href=../../>`},
			{`<link rel="next" href="../3/">`, `<link rel=next href=../3/>`},
			{`href="../../" rel="prev">Previous</a>`, `href=../../ rel=prev>Previous</a>`},
			{`href="../3/" rel="next">Next</a>`, `href=../3/ rel=next>Next</a>`},
		} {
			if !containsAny(indexPageTwoHTML, snippets...) {
				t.Fatalf("page/2 missing one of %#v\n%s", snippets, indexPageTwoHTML)
			}
		}

		notesFolderHTML := readBuildOutputFile(t, outputPath, "notes/index.html")
		if !containsAny(notesFolderHTML,
			`data-e2e-custom-folder="notes"`,
			`data-e2e-custom-folder=notes`,
		) {
			t.Fatalf("notes folder page missing override marker\n%s", notesFolderHTML)
		}

		gardenFolderHTML := readBuildOutputFile(t, outputPath, "notes/garden/index.html")
		if !containsAny(gardenFolderHTML,
			`data-e2e-custom-folder="notes/garden"`,
			`data-e2e-custom-folder=notes/garden`,
		) {
			t.Fatalf("notes/garden folder page missing override marker\n%s", gardenFolderHTML)
		}
		if !containsAny(gardenFolderHTML,
			`href="../../updated-story/"`,
			`href=../../updated-story/`,
		) {
			t.Fatalf("notes/garden folder page missing updated-story link\n%s", gardenFolderHTML)
		}

		gardenPageTwoHTML := readBuildOutputFile(t, outputPath, "notes/garden/page/2/index.html")
		for _, snippets := range [][]string{
			{`data-e2e-custom-folder="notes/garden"`, `data-e2e-custom-folder=notes/garden`},
			{`href="../../../../field-notes/"`, `href=../../../../field-notes/`},
			{`href="../../" rel="prev">Previous</a>`, `href=../../ rel=prev>Previous</a>`},
		} {
			if !containsAny(gardenPageTwoHTML, snippets...) {
				t.Fatalf("notes/garden/page/2 missing one of %#v\n%s", snippets, gardenPageTwoHTML)
			}
		}
		assertVisibleBreadcrumbs(t, gardenPageTwoHTML, []internalmodel.Breadcrumb{
			{Name: "Home", URL: "../../../../"},
			{Name: "notes", URL: "../../../"},
			{Name: "garden"},
		})
		assertBreadcrumbListItems(t, mustJSONLDPayloads(t, gardenPageTwoHTML), []internalmodel.Breadcrumb{
			{Name: "Home", URL: "https://example.com/blog/"},
			{Name: "notes", URL: "https://example.com/blog/notes/"},
			{Name: "garden", URL: "https://example.com/blog/notes/garden/page/2/"},
		})

		tagPageTwoHTML := readBuildOutputFile(t, outputPath, "tags/field/page/2/index.html")
		if !containsAny(tagPageTwoHTML,
			`href="../../../../reference-guide/"`,
			`href=../../../../reference-guide/`,
			`href="../../../../field-notes/"`,
			`href=../../../../field-notes/`,
		) {
			t.Fatalf("tags/field/page/2 missing paginated note links\n%s", tagPageTwoHTML)
		}
		assertVisibleBreadcrumbs(t, tagPageTwoHTML, []internalmodel.Breadcrumb{
			{Name: "Home", URL: "../../../../"},
			{Name: "field"},
		})
		assertBreadcrumbListItems(t, mustJSONLDPayloads(t, tagPageTwoHTML), []internalmodel.Breadcrumb{
			{Name: "Home", URL: "https://example.com/blog/"},
			{Name: "field", URL: "https://example.com/blog/tags/field/page/2/"},
		})

		sitemapXML := readBuildOutputFile(t, outputPath, "sitemap.xml")
		for _, want := range []string{
			`https://example.com/blog/page/2/`,
			`https://example.com/blog/page/3/`,
			`https://example.com/blog/notes/`,
			`https://example.com/blog/notes/garden/`,
			`https://example.com/blog/notes/garden/page/2/`,
			`https://example.com/blog/tags/field/page/2/`,
		} {
			if !bytes.Contains(sitemapXML, []byte(want)) {
				t.Fatalf("sitemap.xml missing %q\n%s", want, sitemapXML)
			}
		}
	})

	t.Run("serve reachability", func(t *testing.T) {
		handler := http.NewServeMux()
		handler.Handle("/blog/", http.StripPrefix("/blog", http.FileServer(http.Dir(outputPath))))
		deployed := httptest.NewServer(handler)
		defer deployed.Close()

		mustHTTPStatus(t, deployed.Client(), deployed.URL+"/blog/updated-story/", http.StatusOK, "Updated Story")
		mustHTTPStatus(t, deployed.Client(), deployed.URL+"/blog/page/2/", http.StatusOK, "Reference Guide")
		mustHTTPStatus(t, deployed.Client(), deployed.URL+"/blog/page/3/", http.StatusOK, "Archive")
		mustHTTPStatus(t, deployed.Client(), deployed.URL+"/blog/notes/", http.StatusOK, `data-e2e-custom-folder=notes`)
		mustHTTPStatus(t, deployed.Client(), deployed.URL+"/blog/notes/garden/page/2/", http.StatusOK, "Field Notes")
		mustHTTPStatus(t, deployed.Client(), deployed.URL+"/blog/tags/field/page/2/", http.StatusOK, "Field Notes")
		mustHTTPStatus(t, deployed.Client(), deployed.URL+"/blog/index.xml", http.StatusOK, "Updated Story")
		mustHTTPStatus(t, deployed.Client(), deployed.URL+"/blog/_pagefind/pagefind-entry.json", http.StatusOK, `"page_count":1`)
		mustHTTPStatus(t, deployed.Client(), deployed.URL+"/blog/assets/custom.css", http.StatusOK, "outline: 3px solid")
	})
}

func TestBuildIntegrationFeatureFixtureEmitsStandaloneTimelineRoute(t *testing.T) {
	t.Parallel()

	vaultPath := copyFixtureVault(t, "feature-vault")
	outputPath := filepath.Join(t.TempDir(), "site")
	configPath := filepath.Join(vaultPath, "obsite.yaml")

	cfg, err := internalconfig.Load(configPath, internalconfig.Overrides{VaultPath: vaultPath})
	if err != nil {
		t.Fatalf("config.Load(%q) error = %v", configPath, err)
	}
	cfg.Search.Enabled = false
	cfg.Timeline.Enabled = true
	cfg.Timeline.AsHomepage = false
	cfg.Timeline.Path = "timeline"

	var diagnostics bytes.Buffer
	result, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{
		concurrency:       2,
		diagnosticsWriter: &diagnostics,
	})
	if err != nil {
		t.Fatalf("buildWithOptions() error = %v", err)
	}
	if result == nil {
		t.Fatal("buildWithOptions() = nil result, want build result")
	}
	if result.NotePages != 5 {
		t.Fatalf("result.NotePages = %d, want %d", result.NotePages, 5)
	}
	if strings.TrimSpace(diagnostics.String()) != "" {
		t.Fatalf("diagnostics summary = %q, want empty summary for standalone timeline fixture", diagnostics.String())
	}

	indexHTML := readBuildOutputFile(t, outputPath, "index.html")
	if !containsAny(indexHTML,
		`body class=kind-index`,
		`class="page-shell landing-page"`,
		`class=page-shell landing-page`,
	) {
		t.Fatalf("homepage missing default index layout markers\n%s", indexHTML)
	}
	if containsAny(indexHTML,
		`body class=kind-timeline`,
		`class="page-shell timeline-page"`,
		`class=page-shell timeline-page`,
	) {
		t.Fatalf("homepage unexpectedly rendered timeline layout\n%s", indexHTML)
	}

	timelineHTML := readBuildOutputFile(t, outputPath, "timeline/index.html")
	if !containsAny(timelineHTML,
		`body class=kind-timeline`,
		`class="page-shell timeline-page"`,
		`class=page-shell timeline-page`,
	) {
		t.Fatalf("timeline page missing timeline layout markers\n%s", timelineHTML)
	}
	for _, snippets := range [][]string{
		{`href="../updated-story/"`, `href=../updated-story/`},
		{`href="page/2/" rel="next">Next</a>`, `href=page/2/ rel=next>Next</a>`},
		{`<link rel="next" href="page/2/">`, `<link rel=next href=page/2/>`},
	} {
		if !containsAny(timelineHTML, snippets...) {
			t.Fatalf("timeline page missing one of %#v\n%s", snippets, timelineHTML)
		}
	}

	timelinePageTwoHTML := readBuildOutputFile(t, outputPath, "timeline/page/2/index.html")
	for _, snippets := range [][]string{
		{`<link rel="prev" href="../../">`, `<link rel=prev href=../../>`},
		{`<link rel="canonical" href="https://example.com/blog/timeline/page/2/">`, `<link rel=canonical href=https://example.com/blog/timeline/page/2/>`},
		{`href="../../../reference-guide/"`, `href=../../../reference-guide/`},
	} {
		if !containsAny(timelinePageTwoHTML, snippets...) {
			t.Fatalf("timeline/page/2 missing one of %#v\n%s", snippets, timelinePageTwoHTML)
		}
	}

	notesFolderHTML := readBuildOutputFile(t, outputPath, "notes/index.html")
	if !containsAny(notesFolderHTML,
		`data-e2e-custom-folder="notes"`,
		`data-e2e-custom-folder=notes`,
	) {
		t.Fatalf("notes folder page missing override marker while standalone timeline route exists\n%s", notesFolderHTML)
	}

	sitemapXML := readBuildOutputFile(t, outputPath, "sitemap.xml")
	for _, want := range []string{
		`https://example.com/blog/`,
		`https://example.com/blog/timeline/`,
		`https://example.com/blog/timeline/page/2/`,
		`https://example.com/blog/notes/`,
	} {
		if !bytes.Contains(sitemapXML, []byte(want)) {
			t.Fatalf("sitemap.xml missing %q\n%s", want, sitemapXML)
		}
	}

	handler := http.NewServeMux()
	handler.Handle("/blog/", http.StripPrefix("/blog", http.FileServer(http.Dir(outputPath))))
	deployed := httptest.NewServer(handler)
	defer deployed.Close()

	mustHTTPStatus(t, deployed.Client(), deployed.URL+"/blog/", http.StatusOK, "Feature Garden")
	mustHTTPStatus(t, deployed.Client(), deployed.URL+"/blog/timeline/", http.StatusOK, "Updated Story")
	mustHTTPStatus(t, deployed.Client(), deployed.URL+"/blog/timeline/page/2/", http.StatusOK, "Reference Guide")
	mustHTTPStatus(t, deployed.Client(), deployed.URL+"/blog/notes/", http.StatusOK, `data-e2e-custom-folder=notes`)
}

func TestBuildIntegrationSlugConflictFixtureReportsFatalDiagnostics(t *testing.T) {
	t.Parallel()

	vaultPath := copyFixtureVault(t, "slug-conflict-vault")
	outputPath := filepath.Join(t.TempDir(), "site")

	var diagnostics bytes.Buffer
	result, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: &diagnostics})
	if err == nil {
		t.Fatal("buildWithOptions() error = nil, want slug conflict failure")
	}
	if !strings.Contains(err.Error(), `slug conflict for "alpha"`) {
		t.Fatalf("buildWithOptions() error = %v, want folder-vs-note slug conflict", err)
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
	if !strings.Contains(summary, "Fatal build errors (") {
		t.Fatalf("diagnostics summary missing fatal section\n%s", summary)
	}
	for _, want := range []string{
		`alpha.md [slug_conflict] slug "alpha" conflicts with alpha.md, alpha/`,
		`alpha/ [slug_conflict] slug "alpha" conflicts with alpha.md, alpha/`,
		`build: build folder pages: slug conflict for "alpha" across alpha.md, alpha/`,
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("diagnostics summary missing %q\n%s", want, summary)
		}
	}
	if strings.Contains(summary, "Warnings (") {
		t.Fatalf("diagnostics summary unexpectedly reports warnings\n%s", summary)
	}
}

func copyFixtureVault(t *testing.T, fixtureName string) string {
	t.Helper()

	srcRoot := filepath.Join("..", "..", "test", "testdata", "e2e", filepath.FromSlash(fixtureName))
	dstRoot := t.TempDir()

	err := filepath.Walk(srcRoot, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		relPath, err := filepath.Rel(srcRoot, path)
		if err != nil {
			return err
		}
		if relPath == "." {
			return nil
		}

		dstPath := filepath.Join(dstRoot, relPath)
		if info.IsDir() {
			return os.MkdirAll(dstPath, 0o755)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dstPath, data, 0o644); err != nil {
			return err
		}

		stamp := time.Date(2026, time.April, 6, 12, 0, 0, 0, time.UTC)
		return os.Chtimes(dstPath, stamp, stamp)
	})
	if err != nil {
		t.Fatalf("copy fixture %q error = %v", fixtureName, err)
	}

	return dstRoot
}

func runObsiteCLI(t *testing.T, args ...string) {
	t.Helper()

	commandArgs := append([]string{"run", "./cmd/obsite"}, args...)
	cmd := exec.Command("go", commandArgs...)
	cmd.Dir = filepath.Join("..", "..")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go %s error = %v\n%s", strings.Join(commandArgs, " "), err, output)
	}
}

func mustHTTPStatus(t *testing.T, client *http.Client, url string, wantStatus int, wantBodyFragment string) []byte {
	t.Helper()

	resp, body := mustHTTPResponse(t, client, url)
	if resp.StatusCode != wantStatus {
		t.Fatalf("GET %s status = %d, want %d\n%s", url, resp.StatusCode, wantStatus, body)
	}
	if wantBodyFragment != "" && !bytes.Contains(body, []byte(wantBodyFragment)) {
		t.Fatalf("GET %s body missing %q\n%s", url, wantBodyFragment, body)
	}
	return body
}

func mustHTTPResponse(t *testing.T, client *http.Client, url string) (*http.Response, []byte) {
	t.Helper()

	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s error = %v", url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("io.ReadAll(%s) error = %v", url, err)
	}
	return resp, body
}

func htmlHead(t *testing.T, document []byte) []byte {
	t.Helper()

	lower := bytes.ToLower(document)
	bodyIndex := bytes.Index(lower, []byte("<body"))
	if bodyIndex == -1 {
		t.Fatalf("document missing <body>\n%s", document)
	}
	return document[:bodyIndex]
}

func mustRegexSubmatch(t *testing.T, data []byte, pattern string) string {
	t.Helper()

	re := regexp.MustCompile(pattern)
	matches := re.FindSubmatch(data)
	if len(matches) < 2 {
		t.Fatalf("pattern %q did not match\n%s", pattern, data)
	}
	return string(matches[1])
}

type popoverPreviewPayload struct {
	Title   string   `json:"title"`
	Summary string   `json:"summary"`
	Tags    []string `json:"tags"`
}

type structuredDataPayload struct {
	Type            string               `json:"@type"`
	ItemListElement []breadcrumbListItem `json:"itemListElement,omitempty"`
}

type breadcrumbListItem struct {
	Type     string `json:"@type"`
	Position int    `json:"position"`
	Name     string `json:"name"`
	Item     string `json:"item,omitempty"`
}

func mustUnmarshalJSON[T any](t *testing.T, data []byte) T {
	t.Helper()

	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("json.Unmarshal() error = %v\n%s", err, data)
	}

	return value
}

func assertVisibleBreadcrumbs(t *testing.T, document []byte, want []internalmodel.Breadcrumb) {
	t.Helper()

	got := visibleBreadcrumbs(t, document)
	if len(got) != len(want) {
		t.Fatalf("len(visible breadcrumbs) = %d, want %d\n%#v", len(got), len(want), got)
	}
	for index := range want {
		if got[index].Name != want[index].Name || got[index].URL != want[index].URL {
			t.Fatalf("visible breadcrumbs[%d] = %#v, want %#v", index, got[index], want[index])
		}
	}
}

func visibleBreadcrumbs(t *testing.T, document []byte) []internalmodel.Breadcrumb {
	t.Helper()

	root := mustParseHTMLDocument(t, document)
	nav := integrationFindHTMLNode(root, func(node *xhtml.Node) bool {
		return node.Type == xhtml.ElementNode && node.Data == "nav" && integrationHTMLClassContains(node, "breadcrumbs")
	})
	if nav == nil {
		t.Fatalf("document missing breadcrumbs nav\n%s", document)
	}

	ol := integrationFirstHTMLElementChild(nav, "ol")
	if ol == nil {
		t.Fatalf("breadcrumbs nav missing ordered list\n%s", document)
	}

	breadcrumbs := make([]internalmodel.Breadcrumb, 0, 4)
	for child := ol.FirstChild; child != nil; child = child.NextSibling {
		if child.Type != xhtml.ElementNode || child.Data != "li" {
			continue
		}

		if anchor := integrationFindHTMLNode(child, func(node *xhtml.Node) bool {
			return node.Type == xhtml.ElementNode && node.Data == "a"
		}); anchor != nil {
			breadcrumbs = append(breadcrumbs, internalmodel.Breadcrumb{
				Name: strings.TrimSpace(integrationHTMLNodeText(anchor)),
				URL:  integrationHTMLAttrValue(anchor, "href"),
			})
			continue
		}

		current := integrationFindHTMLNode(child, func(node *xhtml.Node) bool {
			return node.Type == xhtml.ElementNode && node.Data == "span" && integrationHTMLAttrValue(node, "aria-current") == "page"
		})
		if current == nil {
			t.Fatalf("breadcrumb item missing link or current-page marker\n%s", document)
		}

		breadcrumbs = append(breadcrumbs, internalmodel.Breadcrumb{Name: strings.TrimSpace(integrationHTMLNodeText(current))})
	}

	return breadcrumbs
}

func mustJSONLDPayloads(t *testing.T, document []byte) []structuredDataPayload {
	t.Helper()

	raw := mustScriptText(t, document, func(node *xhtml.Node) bool {
		return integrationHTMLAttrValue(node, "type") == "application/ld+json"
	}, "application/ld+json")
	return mustUnmarshalJSON[[]structuredDataPayload](t, []byte(raw))
}

func hasStructuredDataType(payloads []structuredDataPayload, want string) bool {
	for _, payload := range payloads {
		if payload.Type == want {
			return true
		}
	}

	return false
}

func assertBreadcrumbListItems(t *testing.T, payloads []structuredDataPayload, want []internalmodel.Breadcrumb) {
	t.Helper()

	var breadcrumb *structuredDataPayload
	for index := range payloads {
		if payloads[index].Type == "BreadcrumbList" {
			breadcrumb = &payloads[index]
			break
		}
	}
	if breadcrumb == nil {
		t.Fatalf("structured data missing BreadcrumbList: %#v", payloads)
	}
	if len(breadcrumb.ItemListElement) != len(want) {
		t.Fatalf("len(BreadcrumbList.itemListElement) = %d, want %d", len(breadcrumb.ItemListElement), len(want))
	}
	for index, wantCrumb := range want {
		got := breadcrumb.ItemListElement[index]
		if got.Type != "ListItem" {
			t.Fatalf("BreadcrumbList.itemListElement[%d].@type = %q, want %q", index, got.Type, "ListItem")
		}
		if got.Position != index+1 {
			t.Fatalf("BreadcrumbList.itemListElement[%d].position = %d, want %d", index, got.Position, index+1)
		}
		if got.Name != wantCrumb.Name {
			t.Fatalf("BreadcrumbList.itemListElement[%d].name = %q, want %q", index, got.Name, wantCrumb.Name)
		}
		if got.Item != wantCrumb.URL {
			t.Fatalf("BreadcrumbList.itemListElement[%d].item = %q, want %q", index, got.Item, wantCrumb.URL)
		}
	}
}

func mustSidebarTree(t *testing.T, document []byte) []internalmodel.SidebarNode {
	t.Helper()

	raw := mustScriptText(t, document, func(node *xhtml.Node) bool {
		return integrationHTMLAttrValue(node, "id") == "sidebar-data"
	}, "sidebar-data")
	return mustUnmarshalJSON[[]internalmodel.SidebarNode](t, []byte(raw))
}

func mustSidebarNode(t *testing.T, nodes []internalmodel.SidebarNode, names ...string) *internalmodel.SidebarNode {
	t.Helper()

	currentNodes := nodes
	var current *internalmodel.SidebarNode
	for _, name := range names {
		current = nil
		for index := range currentNodes {
			if currentNodes[index].Name == name {
				current = &currentNodes[index]
				currentNodes = current.Children
				break
			}
		}
		if current == nil {
			t.Fatalf("sidebar path %q not found in %#v", strings.Join(names, " -> "), nodes)
		}
	}

	return current
}

func mustScriptText(t *testing.T, document []byte, match func(*xhtml.Node) bool, label string) string {
	t.Helper()

	root := mustParseHTMLDocument(t, document)
	script := integrationFindHTMLNode(root, func(node *xhtml.Node) bool {
		return node.Type == xhtml.ElementNode && node.Data == "script" && match(node)
	})
	if script == nil {
		t.Fatalf("document missing script %q\n%s", label, document)
	}

	return integrationHTMLNodeText(script)
}

func mustParseHTMLDocument(t *testing.T, document []byte) *xhtml.Node {
	t.Helper()

	root, err := xhtml.Parse(bytes.NewReader(document))
	if err != nil {
		t.Fatalf("xhtml.Parse() error = %v\n%s", err, document)
	}

	return root
}

func integrationFirstHTMLElementChild(node *xhtml.Node, tag string) *xhtml.Node {
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == xhtml.ElementNode && child.Data == tag {
			return child
		}
	}

	return nil
}

func integrationFindHTMLNode(node *xhtml.Node, match func(*xhtml.Node) bool) *xhtml.Node {
	if node == nil {
		return nil
	}
	if match(node) {
		return node
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if found := integrationFindHTMLNode(child, match); found != nil {
			return found
		}
	}

	return nil
}

func integrationHTMLAttrValue(node *xhtml.Node, key string) string {
	for _, attr := range node.Attr {
		if attr.Key == key {
			return attr.Val
		}
	}

	return ""
}

func integrationHTMLClassContains(node *xhtml.Node, className string) bool {
	for _, candidate := range strings.Fields(integrationHTMLAttrValue(node, "class")) {
		if candidate == className {
			return true
		}
	}

	return false
}

func integrationHTMLNodeText(node *xhtml.Node) string {
	var builder strings.Builder
	integrationCollectHTMLText(&builder, node)
	return builder.String()
}

func integrationCollectHTMLText(builder *strings.Builder, node *xhtml.Node) {
	if node == nil {
		return
	}
	if node.Type == xhtml.TextNode {
		builder.WriteString(node.Data)
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		integrationCollectHTMLText(builder, child)
	}
}

func assertPathMissing(t *testing.T, targetPath string) {
	t.Helper()

	if _, err := os.Stat(targetPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("os.Stat(%q) error = %v, want not-exist", targetPath, err)
	}
}
