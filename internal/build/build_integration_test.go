package build

import (
	"bytes"
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
	internalserver "github.com/simp-lee/obsite/internal/server"
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
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("generated config missing %q\n%s", want, content)
		}
	}

	cfg, err := internalconfig.Load(configPath, internalconfig.Overrides{})
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
	if !strings.Contains(err.Error(), `slug conflict for "shared"`) {
		t.Fatalf("buildWithOptions() error = %v, want slug conflict", err)
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
		`notes/alpha.md [slug_conflict] slug "shared" conflicts with notes/alpha.md, notes/beta.md`,
		`notes/beta.md [slug_conflict] slug "shared" conflicts with notes/alpha.md, notes/beta.md`,
		`build: build index: slug conflict for "shared" across notes/alpha.md, notes/beta.md`,
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

func assertPathMissing(t *testing.T, targetPath string) {
	t.Helper()

	if _, err := os.Stat(targetPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("os.Stat(%q) error = %v, want not-exist", targetPath, err)
	}
}
