package build

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	xhtml "golang.org/x/net/html"

	internalconfig "github.com/simp-lee/obsite/internal/config"
	"github.com/simp-lee/obsite/internal/diag"
	"github.com/simp-lee/obsite/internal/link"
	internalmarkdown "github.com/simp-lee/obsite/internal/markdown"
	"github.com/simp-lee/obsite/internal/model"
	"github.com/simp-lee/obsite/internal/recommend"
	internalrender "github.com/simp-lee/obsite/internal/render"
	"github.com/simp-lee/obsite/internal/vault"
)

var (
	buildTestRenderHookScopes sync.Map
	buildTestOutputHookMu     sync.Mutex
	buildTestOutputHookScopes sync.Map
)

func TestPreRenderRelatedRanking(t *testing.T) {
	t.Parallel()

	semantics := []model.RelatedSemanticDocument{
		{RelPath: "a.md", Body: "database protocol"},
		{RelPath: "b.md", Body: "database protocol"},
	}
	semanticBacking := semantics
	idx := &model.VaultIndex{Notes: map[string]*model.Note{
		"a.md": {RelPath: "a.md", Slug: "a"},
		"b.md": {RelPath: "b.md", Slug: "b"},
	}}
	sourceGraph := link.BuildSourceGraph(idx)
	result, err := preparePreRenderRelatedRanking(&semantics, idx, sourceGraph, recommend.EngineParameters{
		Features:    recommend.FeatureParameters{MaxFeatures: 32, MaxDFRatio: 0.40},
		Content:     recommend.ContentParameters{MinCosine: 0.05, MaxSingleTermRatio: 0.10},
		Count:       1,
		WorkerCount: 1,
	})
	if err != nil {
		t.Fatalf("preparePreRenderRelatedRanking() error = %v", err)
	}
	if result == nil || len(result.Documents) != 2 {
		t.Fatalf("preparePreRenderRelatedRanking() = %#v, want two compact documents", result)
	}
	if len(result.Documents[0].Related) != 1 || result.Documents[result.Documents[0].Related[0].DocID].RelPath != "b.md" {
		t.Fatalf("a.md related ranking = %#v, want b.md", result.Documents[0].Related)
	}
	if semantics != nil {
		t.Fatalf("semantic owner after successful ranking = %#v, want released", semantics)
	}
	for index, document := range semanticBacking {
		if document.RelPath != "" || document.Title != "" || len(document.Aliases) != 0 || len(document.Headings) != 0 || document.Body != "" {
			t.Fatalf("semantic backing document %d = %#v, want cleared after tokenization", index, document)
		}
	}

	singletonOwner := []model.RelatedSemanticDocument{{RelPath: "a.md", Body: "database"}}
	singletonBacking := singletonOwner
	singleton, err := preparePreRenderRelatedRanking(&singletonOwner, idx, sourceGraph, recommend.EngineParameters{})
	if err != nil || singleton == nil || len(singleton.Documents) != 0 {
		t.Fatalf("preparePreRenderRelatedRanking(singleton) = %#v, %v; want empty result", singleton, err)
	}
	if singletonOwner != nil {
		t.Fatalf("semantic owner after singleton ranking = %#v, want released", singletonOwner)
	}
	if singletonBacking[0].RelPath != "" || singletonBacking[0].Body != "" {
		t.Fatalf("singleton semantic backing = %#v, want cleared on early return", singletonBacking)
	}
}

func TestNoRecommendationFallback(t *testing.T) {
	t.Parallel()

	semanticOwner := []model.RelatedSemanticDocument{{RelPath: "a.md"}, {RelPath: "b.md"}}
	if result, err := preparePreRenderRelatedRanking(&semanticOwner, nil, nil, recommend.EngineParameters{}); err == nil || result != nil {
		t.Fatalf("preparePreRenderRelatedRanking(invalid engine) = %#v, %v; want hard error without fallback", result, err)
	}
	if semanticOwner != nil {
		t.Fatalf("semantic owner after ranking error = %#v, want released", semanticOwner)
	}
}

func TestRelatedDisabled(t *testing.T) {
	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	writeBuildTestFile(t, vaultPath, "notes/alpha.md", "# Alpha\n\n数据库一致性协议。\n")

	originalBuildVaultIndex := buildVaultIndex
	var collected bool
	buildVaultIndex = func(scanResult vault.ScanResult, frontmatterResult vault.FrontmatterResult, collector *diag.Collector, options vault.BuildIndexOptions) (vault.IndexResult, error) {
		collected = options.CollectRelatedSemantic
		return originalBuildVaultIndex(scanResult, frontmatterResult, collector, options)
	}
	defer func() { buildVaultIndex = originalBuildVaultIndex }()

	cfg := testBuildSiteConfig()
	cfg.Related.Enabled = false
	if _, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard}); err != nil {
		t.Fatalf("buildWithOptions() error = %v", err)
	}
	if collected {
		t.Fatal("disabled related build collected semantic sidecar")
	}
	if html := readBuildOutputFile(t, outputPath, "alpha/index.html"); bytes.Contains(html, []byte("Related Articles")) {
		t.Fatalf("disabled related build rendered recommendations\n%s", html)
	}
}

func TestBuildDisablingRelatedRemovesCachedNoteSectionsWithoutMarkdownRerender(t *testing.T) {
	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	writeBuildTestFile(t, vaultPath, "notes/alpha.md", "# Alpha\n\nshared database protocol consistency\n")
	writeBuildTestFile(t, vaultPath, "notes/beta.md", "# Beta\n\nshared database protocol consistency\n")
	cfg := testBuildSiteConfig()
	cfg.Related.Enabled = true
	cfg.Related.Count = 1
	if _, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{}); err != nil {
		t.Fatal(err)
	}
	if html := readBuildOutputFile(t, outputPath, "alpha/index.html"); !bytes.Contains(html, []byte("Related Articles")) {
		t.Fatalf("enabled related section missing\n%s", html)
	}

	cfg.Related.Enabled = false
	getRenderedMarkdown := captureRenderedMarkdownNotePaths(t)
	result, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(getRenderedMarkdown()) != 0 {
		t.Fatalf("related toggle rerendered Markdown: %#v", getRenderedMarkdown())
	}
	if result.NotePages != 2 {
		t.Fatalf("result.NotePages = %d, want both note pages rewritten", result.NotePages)
	}
	if html := readBuildOutputFile(t, outputPath, "alpha/index.html"); bytes.Contains(html, []byte("Related Articles")) {
		t.Fatalf("disabled related section remained cached\n%s", html)
	}
}

func TestRelatedTagLimit(t *testing.T) {
	t.Parallel()

	candidate := &model.Note{RelPath: "candidate.md", Slug: "candidate"}
	idx := &model.VaultIndex{Tags: make(map[string]*model.Tag)}
	for index := 0; index < 10; index++ {
		name := fmt.Sprintf("tag-%02d", index)
		candidate.Tags = append(candidate.Tags, name)
		idx.Tags[name] = &model.Tag{Name: name, Slug: "tags/" + name}
	}
	article := materializeRelatedArticle("source/index.html", idx, candidate, "summary", 0.5)
	if len(article.Tags) != 8 || cap(article.Tags) != 8 {
		t.Fatalf("article.Tags len/cap = %d/%d, want bounded 8/8", len(article.Tags), cap(article.Tags))
	}
	for index, tag := range article.Tags {
		if want := fmt.Sprintf("tag-%02d", index); tag.Name != want {
			t.Fatalf("article.Tags[%d].Name = %q, want stable %q", index, tag.Name, want)
		}
	}
	if len(candidate.Tags) != 10 {
		t.Fatalf("candidate scoring tags were truncated to %d, want all 10 retained", len(candidate.Tags))
	}
}

func TestRelatedSummaryLimit(t *testing.T) {
	t.Parallel()

	summary := strings.Repeat("summary ", 40)
	candidate := &model.Note{RelPath: "candidate.md", Slug: "candidate"}
	article := materializeRelatedArticle("source/index.html", &model.VaultIndex{}, candidate, summary, 0.5)
	if article.Summary != summary {
		t.Fatalf("materialized summary changed existing summary contract: got %q, want %q", article.Summary, summary)
	}
}

func TestQualityFixturesAreNotPublished(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	writeBuildTestFile(t, vaultPath, "notes/public.md", "---\ntitle: Public\n---\n# Public\n\nOrdinary published note.\n")
	if _, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard}); err != nil {
		t.Fatal(err)
	}
	for _, marker := range [][]byte{[]byte("testdata/quality"), []byte("Rebuilding State from an Event Log"), []byte("cal-zh-hans-00")} {
		err := filepath.Walk(outputPath, func(currentPath string, info os.FileInfo, walkErr error) error {
			if walkErr != nil || info.IsDir() {
				return walkErr
			}
			data, err := os.ReadFile(currentPath)
			if err != nil {
				return err
			}
			if bytes.Contains(data, marker) {
				return fmt.Errorf("quality marker %q published in %s", marker, currentPath)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestRelatedFullAndIncrementalIdentity(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	writeBuildTestFile(t, vaultPath, "notes/alpha.md", "---\ntitle: Alpha\ndate: 2026-04-06\ntags: [go]\n---\n# Alpha\n\nDatabase consistency protocol and recovery workflow.\n")
	writeBuildTestFile(t, vaultPath, "notes/beta.md", "---\ntitle: Beta\ndate: 2026-04-05\ntags: [go]\n---\n# Beta\n\nDatabase consistency protocol and replication workflow.\n")
	writeBuildTestFile(t, vaultPath, "notes/gamma.md", "---\ntitle: Gamma\ndate: 2026-04-04\n---\n# Gamma\n\nKitchen fermentation schedule and bread starter.\n")

	cfg := testBuildSiteConfig()
	cfg.Related.Enabled = true
	cfg.Related.Count = 2
	if _, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{force: true, diagnosticsWriter: io.Discard}); err != nil {
		t.Fatalf("full build error = %v", err)
	}
	paths := []string{"alpha/index.html", "beta/index.html", "gamma/index.html", cacheManifestRelPath}
	full := make(map[string][]byte, len(paths))
	for _, relPath := range paths {
		full[relPath] = append([]byte(nil), readBuildOutputFile(t, outputPath, relPath)...)
	}

	result, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard})
	if err != nil {
		t.Fatalf("incremental build error = %v", err)
	}
	if result.NotePages != 0 {
		t.Fatalf("incremental no-op NotePages = %d, want 0", result.NotePages)
	}
	for _, relPath := range paths {
		if got := readBuildOutputFile(t, outputPath, relPath); !bytes.Equal(got, full[relPath]) {
			t.Fatalf("incremental output %s differs from full build", relPath)
		}
	}
}

func TestBacklinks(t *testing.T) {
	t.Parallel()

	alpha := &model.Note{RelPath: "alpha.md", Slug: "alpha", Frontmatter: model.Frontmatter{Title: "Alpha"}}
	beta := &model.Note{RelPath: "beta.md", Slug: "beta", Frontmatter: model.Frontmatter{Title: "Beta"}}
	idx := &model.VaultIndex{Notes: map[string]*model.Note{"alpha.md": alpha, "beta.md": beta}}
	graph := &model.LinkGraph{Backward: map[string][]string{"alpha.md": {"beta.md"}}}
	if got, want := buildBacklinks("alpha/index.html", idx, graph, "alpha.md"), []model.BacklinkEntry{{Title: "Beta", URL: "../beta/"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("buildBacklinks() = %#v, want %#v", got, want)
	}
}

func TestNonRelatedPages(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	writeBuildTestFile(t, vaultPath, "notes/alpha.md", "---\ntitle: Alpha\ndate: 2026-04-06\ntags: [go]\n---\n# Alpha\n\nDatabase consistency protocol.\n")
	writeBuildTestFile(t, vaultPath, "notes/beta.md", "---\ntitle: Beta\ndate: 2026-04-05\ntags: [go]\n---\n# Beta\n\nDatabase consistency protocol.\n")

	disabledOutput := filepath.Join(t.TempDir(), "disabled")
	enabledOutput := filepath.Join(t.TempDir(), "enabled")
	disabled := testBuildSiteConfig()
	disabled.Related.Enabled = false
	enabled := disabled
	enabled.Related.Enabled = true
	if _, err := buildWithOptions(disabled, vaultPath, disabledOutput, buildOptions{diagnosticsWriter: io.Discard}); err != nil {
		t.Fatalf("disabled build error = %v", err)
	}
	if _, err := buildWithOptions(enabled, vaultPath, enabledOutput, buildOptions{diagnosticsWriter: io.Discard}); err != nil {
		t.Fatalf("enabled build error = %v", err)
	}
	for _, relPath := range []string{"index.html", "tags/go/index.html"} {
		disabledPage := readBuildOutputFile(t, disabledOutput, relPath)
		enabledPage := readBuildOutputFile(t, enabledOutput, relPath)
		if !bytes.Equal(enabledPage, disabledPage) {
			t.Fatalf("non-related page %s changed when recommendations were enabled", relPath)
		}
	}
}

func lockBuildTestRenderHooks(t *testing.T) {
	if t == nil {
		return
	}
	t.Helper()
	if _, loaded := buildTestRenderHookScopes.LoadOrStore(t, struct{}{}); loaded {
		return
	}

	lockBuildRenderHookIsolation()
	t.Cleanup(func() {
		buildTestRenderHookScopes.Delete(t)
		unlockBuildRenderHookIsolation()
	})
}

func lockBuildTestOutputHooks(t *testing.T) {
	if t == nil {
		return
	}
	t.Helper()
	if _, loaded := buildTestOutputHookScopes.LoadOrStore(t, struct{}{}); loaded {
		return
	}

	buildTestOutputHookMu.Lock()
	t.Cleanup(func() {
		buildTestOutputHookScopes.Delete(t)
		buildTestOutputHookMu.Unlock()
	})
}

func overrideStagedOutputFileOps(
	t *testing.T,
	rename func(string, string) error,
	removeAll func(string) error,
	stat func(string) (os.FileInfo, error),
) {
	t.Helper()
	lockBuildTestOutputHooks(t)

	originalRename := stagedOutputRename
	originalRemoveAll := stagedOutputRemoveAll
	originalStat := stagedOutputStat
	if rename != nil {
		stagedOutputRename = rename
	}
	if removeAll != nil {
		stagedOutputRemoveAll = removeAll
	}
	if stat != nil {
		stagedOutputStat = stat
	}
	t.Cleanup(func() {
		stagedOutputRename = originalRename
		stagedOutputRemoveAll = originalRemoveAll
		stagedOutputStat = originalStat
	})
}

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
	if !containsAny(notFoundHTML, `<base href="/blog/">`, `<base href=/blog/>`) {
		t.Fatalf("404 page missing static site base href\n%s", notFoundHTML)
	}
	if !containsAny(notFoundHTML, `href="alpha/">Alpha</a>`, `href=alpha/>Alpha</a>`) {
		t.Fatalf("404 page missing recent note clean URL\n%s", notFoundHTML)
	}

	styleCSS := readBuildOutputFile(t, outputPath, "style.css")
	templateCSS, err := os.ReadFile(filepath.Join("..", "render", "site", "style.css"))
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

func TestBuildIsolatesNotePageHooksAcrossConcurrentBuilds(t *testing.T) {
	t.Parallel()

	type notePageCapture struct {
		reached chan<- string
		release <-chan struct{}
		once    sync.Once
		mu      sync.Mutex
		paths   []string
	}

	newVault := func(prefix string) string {
		vaultPath := t.TempDir()
		writeBuildTestFile(t, vaultPath, filepath.ToSlash(filepath.Join("notes", prefix+"-one.md")), fmt.Sprintf(`---
title: %s one
date: 2026-04-06
---
# %s one

First note.
`, prefix, prefix))
		writeBuildTestFile(t, vaultPath, filepath.ToSlash(filepath.Join("notes", prefix+"-two.md")), fmt.Sprintf(`---
title: %s two
date: 2026-04-05
---
# %s two

Second note.
`, prefix, prefix))
		return vaultPath
	}

	newCapture := func(label string, reached chan<- string, release <-chan struct{}) (*notePageCapture, func(internalrender.NotePageInput)) {
		capture := &notePageCapture{reached: reached, release: release}
		return capture, func(input internalrender.NotePageInput) {
			if input.Note == nil {
				return
			}

			capture.mu.Lock()
			capture.paths = append(capture.paths, input.Note.RelPath)
			capture.mu.Unlock()

			capture.once.Do(func() {
				reached <- label
				<-release
			})
		}
	}

	readPaths := func(capture *notePageCapture) []string {
		capture.mu.Lock()
		defer capture.mu.Unlock()
		return sortedStrings(append([]string(nil), capture.paths...))
	}

	type buildResult struct {
		label  string
		result *BuildResult
		err    error
	}

	vaultAlpha := newVault("alpha")
	vaultBeta := newVault("beta")
	outputAlpha := filepath.Join(t.TempDir(), "site-alpha")
	outputBeta := filepath.Join(t.TempDir(), "site-beta")
	reached := make(chan string, 2)
	release := make(chan struct{})
	done := make(chan buildResult, 2)

	alphaCapture, alphaHook := newCapture("alpha", reached, release)
	betaCapture, betaHook := newCapture("beta", reached, release)

	runBuild := func(label string, vaultPath string, outputPath string, hook func(internalrender.NotePageInput)) {
		go func() {
			result, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{
				concurrency:       1,
				diagnosticsWriter: io.Discard,
				testNotePageHook:  hook,
			})
			done <- buildResult{label: label, result: result, err: err}
		}()
	}

	runBuild("alpha", vaultAlpha, outputAlpha, alphaHook)
	runBuild("beta", vaultBeta, outputBeta, betaHook)

	started := map[string]bool{}
	for len(started) < 2 {
		select {
		case label := <-reached:
			started[label] = true
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent build hook capture did not reach both build-local sinks")
		}
	}

	select {
	case result := <-done:
		t.Fatalf("%s build completed before concurrent hook gate released", result.label)
	default:
	}

	close(release)

	for index := 0; index < 2; index++ {
		select {
		case result := <-done:
			if result.err != nil {
				t.Fatalf("%s buildWithOptions() error = %v", result.label, result.err)
			}
			if result.result == nil {
				t.Fatalf("%s buildWithOptions() = nil result, want build result", result.label)
			}
			if result.result.NotePages != 2 {
				t.Fatalf("%s result.NotePages = %d, want %d", result.label, result.result.NotePages, 2)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent build hook capture did not complete")
		}
	}

	if got := readPaths(alphaCapture); !reflect.DeepEqual(got, []string{"notes/alpha-one.md", "notes/alpha-two.md"}) {
		t.Fatalf("alpha hook captured note pages = %#v, want %#v", got, []string{"notes/alpha-one.md", "notes/alpha-two.md"})
	}
	if got := readPaths(betaCapture); !reflect.DeepEqual(got, []string{"notes/beta-one.md", "notes/beta-two.md"}) {
		t.Fatalf("beta hook captured note pages = %#v, want %#v", got, []string{"notes/beta-one.md", "notes/beta-two.md"})
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
	if !containsAny(indexPageTwoHTML, `href="../../one/">One</a>`, `href=../../one/>One</a>`) {
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
	if !containsAny(tagPageTwoHTML, `href="../../../../one/">One</a>`, `href=../../../../one/>One</a>`) {
		t.Fatalf("tag page 2 missing final note link\n%s", tagPageTwoHTML)
	}
	if !containsAny(tagPageTwoHTML, `<link rel="prev" href="../../">`, `<link rel=prev href=../../>`) {
		t.Fatalf("tag page 2 missing head prev link\n%s", tagPageTwoHTML)
	}
	if !containsAny(tagPageTwoHTML, `<link rel="canonical" href="https://example.com/blog/tags/topic/page/2/">`, `<link rel=canonical href=https://example.com/blog/tags/topic/page/2/>`) {
		t.Fatalf("tag page 2 missing canonical link\n%s", tagPageTwoHTML)
	}

	folderPageTwoHTML := readBuildOutputFile(t, outputPath, "alpha/page/2/index.html")
	if !containsAny(folderPageTwoHTML, `href="../../../one/">One</a>`, `href=../../../one/>One</a>`) {
		t.Fatalf("folder page 2 missing final note link\n%s", folderPageTwoHTML)
	}
	if !containsAny(folderPageTwoHTML, `<link rel="prev" href="../../">`, `<link rel=prev href=../../>`) {
		t.Fatalf("folder page 2 missing head prev link\n%s", folderPageTwoHTML)
	}
	if !containsAny(folderPageTwoHTML, `<link rel="canonical" href="https://example.com/blog/alpha/page/2/">`, `<link rel=canonical href=https://example.com/blog/alpha/page/2/>`) {
		t.Fatalf("folder page 2 missing canonical link\n%s", folderPageTwoHTML)
	}

	timelinePageTwoHTML := readBuildOutputFile(t, outputPath, "notes/page/2/index.html")
	if !containsAny(timelinePageTwoHTML, `href="../../../one/">One</a>`, `href=../../../one/>One</a>`) {
		t.Fatalf("timeline page 2 missing final note link\n%s", timelinePageTwoHTML)
	}
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

	diagnostics := bytes.Buffer{}
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

	diagnostics := bytes.Buffer{}
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

	diagnostics := bytes.Buffer{}
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

	diagnostics := bytes.Buffer{}
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

func TestBuildRejectsFolderPathConflictsWithPaginationRoutes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		setup           func(t *testing.T, vaultPath string)
		wantErr         string
		wantSummary     []string
		wantMissingPath string
	}{
		{
			name: "index pagination conflict",
			setup: func(t *testing.T, vaultPath string) {
				t.Helper()
				writeBuildTestFile(t, vaultPath, "page/2/collision.md", `---
title: Collision
date: 2026-04-04
---
# Collision

Real folder content.
`)
				writeBuildTestFile(t, vaultPath, "alpha.md", `---
title: Alpha
date: 2026-04-06
---
# Alpha

Alpha note.
`)
				writeBuildTestFile(t, vaultPath, "beta.md", `---
title: Beta
date: 2026-04-05
---
# Beta

Beta note.
`)
			},
			wantErr: `generated route conflict for "page/2/index.html"`,
			wantSummary: []string{
				`page/2/ [slug_conflict] generated route "page/2/index.html" conflicts with page/2/, index pagination / page 2`,
				`index pagination / page 2 [slug_conflict] generated route "page/2/index.html" conflicts with page/2/, index pagination / page 2`,
				`build: build route manifest: generated route conflict for "page/2/index.html" across page/2/, index pagination / page 2`,
			},
			wantMissingPath: filepath.Join("page", "2", "index.html"),
		},
		{
			name: "folder pagination conflict",
			setup: func(t *testing.T, vaultPath string) {
				t.Helper()
				writeBuildTestFile(t, vaultPath, "alpha/one.md", `---
title: One
date: 2026-04-06
---
# One

First note.
`)
				writeBuildTestFile(t, vaultPath, "alpha/two.md", `---
title: Two
date: 2026-04-05
---
# Two

Second note.
`)
				writeBuildTestFile(t, vaultPath, "alpha/page/2/collision.md", `---
title: Collision
date: 2026-04-04
---
# Collision

Nested folder content.
`)
			},
			wantErr: `generated route conflict for "alpha/page/2/index.html"`,
			wantSummary: []string{
				`folder pagination alpha/ page 2 [slug_conflict] generated route "alpha/page/2/index.html" conflicts with folder pagination alpha/ page 2, alpha/page/2/`,
				`alpha/page/2/ [slug_conflict] generated route "alpha/page/2/index.html" conflicts with folder pagination alpha/ page 2, alpha/page/2/`,
				`build: build route manifest: generated route conflict for "alpha/page/2/index.html" across folder pagination alpha/ page 2, alpha/page/2/`,
			},
			wantMissingPath: filepath.Join("alpha", "page", "2", "index.html"),
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
			cfg.Pagination.PageSize = 2

			var diagnostics bytes.Buffer
			result, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{diagnosticsWriter: &diagnostics})
			if err == nil {
				t.Fatal("buildWithOptions() error = nil, want folder-vs-pagination route conflict failure")
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
			if _, statErr := os.Stat(filepath.Join(outputPath, tt.wantMissingPath)); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("os.Stat(%s) error = %v, want not-exist after rejected route collision", tt.wantMissingPath, statErr)
			}
		})
	}
}

func TestBuildRejectsNestedTagPathConflictsWithTagPaginationRoutes(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "alpha.md", `---
title: Alpha
date: 2026-04-06
tags:
  - Topic
---
# Alpha

Alpha note.
`)
	writeBuildTestFile(t, vaultPath, "beta.md", `---
title: Beta
date: 2026-04-05
tags:
  - Topic
---
# Beta

Beta note.
`)
	writeBuildTestFile(t, vaultPath, "gamma.md", `---
title: Gamma
date: 2026-04-04
tags:
  - Topic
  - Topic/Page/2
---
# Gamma

Gamma note.
`)

	cfg := testBuildSiteConfig()
	cfg.Pagination.PageSize = 2

	var diagnostics bytes.Buffer
	result, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{diagnosticsWriter: &diagnostics})
	if err == nil {
		t.Fatal("buildWithOptions() error = nil, want nested-tag-vs-pagination route conflict failure")
	}
	if !strings.Contains(err.Error(), `generated route conflict for "tags/topic/page/2/index.html"`) {
		t.Fatalf("buildWithOptions() error = %v, want nested tag pagination conflict", err)
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
		`tag pagination tags/topic/ page 2 [slug_conflict] generated route "tags/topic/page/2/index.html" conflicts with tag pagination tags/topic/ page 2, tag page tags/topic/page/2/`,
		`tag page tags/topic/page/2/ [slug_conflict] generated route "tags/topic/page/2/index.html" conflicts with tag pagination tags/topic/ page 2, tag page tags/topic/page/2/`,
		`build: build route manifest: generated route conflict for "tags/topic/page/2/index.html" across tag pagination tags/topic/ page 2, tag page tags/topic/page/2/`,
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("diagnostics summary missing %q\n%s", want, summary)
		}
	}
	if _, statErr := os.Stat(filepath.Join(outputPath, "tags", "topic", "page", "2", "index.html")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("os.Stat(tags/topic/page/2/index.html) error = %v, want not-exist after rejected route collision", statErr)
	}
}

func TestBuildGeneratedPageRoutesRejectStandaloneTimelinePaginationConflicts(t *testing.T) {
	t.Parallel()

	cfg := testBuildSiteConfig()
	cfg.Pagination.PageSize = 2
	cfg.Timeline.Enabled = true
	cfg.Timeline.Path = "notes"

	alpha := &model.Note{RelPath: "alpha.md", Slug: "alpha", Frontmatter: model.Frontmatter{Date: time.Date(2026, time.April, 6, 0, 0, 0, 0, time.UTC)}}
	beta := &model.Note{RelPath: "beta.md", Slug: "beta", Frontmatter: model.Frontmatter{Date: time.Date(2026, time.April, 5, 0, 0, 0, 0, time.UTC)}}
	gamma := &model.Note{RelPath: "gamma.md", Slug: "gamma", Frontmatter: model.Frontmatter{Date: time.Date(2026, time.April, 4, 0, 0, 0, 0, time.UTC)}}
	idx := &model.VaultIndex{
		Notes: map[string]*model.Note{
			alpha.RelPath: alpha,
			beta.RelPath:  beta,
			gamma.RelPath: gamma,
		},
		Tags: map[string]*model.Tag{},
	}
	folders := []folderPageSpec{{Path: "notes/page/2", Notes: []*model.Note{alpha}}}

	routes := buildGeneratedPageRoutes(cfg, idx, folders)
	registry := generatedPageRouteRegistry{seen: make(map[string]generatedPageRoute, len(routes))}
	diagnostics := diag.NewCollector()

	var gotErr error
	for _, route := range routes {
		if err := registry.add(route, diagnostics); err != nil {
			gotErr = err
			break
		}
	}
	if gotErr == nil {
		t.Fatal("generatedPageRouteRegistry.add() error = nil, want standalone timeline pagination conflict")
	}
	if !strings.Contains(gotErr.Error(), `generated route conflict for "notes/page/2/index.html" across notes/page/2/, timeline pagination notes/ page 2`) {
		t.Fatalf("generatedPageRouteRegistry.add() error = %v, want standalone timeline pagination conflict", gotErr)
	}

	gotDiagnostics := diagnostics.Diagnostics()
	if len(gotDiagnostics) != 2 {
		t.Fatalf("len(diagnostics.Diagnostics()) = %d, want %d", len(gotDiagnostics), 2)
	}
	for _, diagnostic := range gotDiagnostics {
		if diagnostic.Kind != diag.KindSlugConflict {
			t.Fatalf("diagnostic.Kind = %q, want %q", diagnostic.Kind, diag.KindSlugConflict)
		}
	}
	if gotDiagnostics[0].Location.Path != "notes/page/2/" {
		t.Fatalf("diagnostics[0].Location.Path = %q, want %q", gotDiagnostics[0].Location.Path, "notes/page/2/")
	}
	if gotDiagnostics[1].Location.Path != "timeline pagination notes/ page 2" {
		t.Fatalf("diagnostics[1].Location.Path = %q, want %q", gotDiagnostics[1].Location.Path, "timeline pagination notes/ page 2")
	}
}

func TestBuildRejectsTimelineHomepagePathConflictsWithPaginationRoutes(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	writeBuildTestFile(t, vaultPath, "page/2/collision.md", `---
title: Collision
date: 2026-04-04
---
# Collision

Homepage timeline folder content.
`)
	writeBuildTestFile(t, vaultPath, "alpha.md", `---
title: Alpha
date: 2026-04-06
---
# Alpha

Alpha note.
`)
	writeBuildTestFile(t, vaultPath, "beta.md", `---
title: Beta
date: 2026-04-05
---
# Beta

Beta note.
`)

	cfg := testBuildSiteConfig()
	cfg.Pagination.PageSize = 2
	cfg.Timeline.Enabled = true
	cfg.Timeline.AsHomepage = true

	var diagnostics bytes.Buffer
	result, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{diagnosticsWriter: &diagnostics})
	if err == nil {
		t.Fatal("buildWithOptions() error = nil, want timeline homepage pagination conflict failure")
	}
	if !strings.Contains(err.Error(), `generated route conflict for "page/2/index.html"`) {
		t.Fatalf("buildWithOptions() error = %v, want timeline homepage pagination conflict", err)
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
		`page/2/ [slug_conflict] generated route "page/2/index.html" conflicts with page/2/, timeline pagination / page 2`,
		`timeline pagination / page 2 [slug_conflict] generated route "page/2/index.html" conflicts with page/2/, timeline pagination / page 2`,
		`build: build route manifest: generated route conflict for "page/2/index.html" across page/2/, timeline pagination / page 2`,
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("diagnostics summary missing %q\n%s", want, summary)
		}
	}
	if _, statErr := os.Stat(filepath.Join(outputPath, "page", "2", "index.html")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("os.Stat(page/2/index.html) error = %v, want not-exist after rejected route collision", statErr)
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
	if betaIndex >= muIndex || muIndex >= alphaIndex || alphaIndex >= zetaIndex {
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
			name:         "note slug conflict after unicode case normalization",
			timelinePath: "J\u030c",
			setup: func(t *testing.T, vaultPath string) {
				t.Helper()
				writeBuildTestFile(t, vaultPath, "ǰ.md", `---
title: Lowercase Precomposed
date: 2026-04-06
---
# Lowercase Precomposed

Conflicting note slug.
`)
			},
			wantErr: `slug conflict for "ǰ"`,
			wantSummary: []string{
				`ǰ.md [slug_conflict]`,
				`timeline page J̌/ [slug_conflict]`,
				`build: build timeline page: slug conflict for "ǰ"`,
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

func TestWriteOutputFileRejectsPathOutsideOutputRoot(t *testing.T) {
	t.Parallel()

	baseRoot := t.TempDir()
	outputRoot := filepath.Join(baseRoot, "site")
	escapedPath := filepath.Join(baseRoot, "escaped", "index.html")

	err := writeOutputFile(outputRoot, `..\escaped\index.html`, []byte("blocked"))
	if err == nil {
		t.Fatal("writeOutputFile() error = nil, want containment failure")
	}
	if !strings.Contains(err.Error(), `output path "../escaped/index.html" must stay within output root`) {
		t.Fatalf("writeOutputFile() error = %q, want containment error", err.Error())
	}
	if _, statErr := os.Stat(escapedPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("os.Stat(%q) error = %v, want not-exist for blocked escaped write", escapedPath, statErr)
	}
}

func TestBuildCopiesFixedCustomCSSAndInjectsRelativeLinks(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	customCSSContent := "body { color: tomato; }\n"
	customCSSPath := filepath.Join(vaultPath, "custom.css")

	writeBuildTestFile(t, vaultPath, "custom.css", customCSSContent)
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
	configPath := filepath.Join(vaultPath, "obsite.yaml")
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

	cfg, err := internalconfig.LoadForBuild(vaultPath)
	if err != nil {
		t.Fatalf("config.LoadForBuild() error = %v", err)
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

func TestBuildRejectsMissingConfiguredVaultCustomCSSSource(t *testing.T) {
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

	cfg := testBuildSiteConfig()
	cfg.CustomCSS = filepath.Join(vaultPath, "custom.css")

	_, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{})
	if err == nil {
		t.Fatal("buildWithOptions() error = nil, want explicit missing custom CSS error")
	}
	if !strings.Contains(err.Error(), `custom CSS "`+cfg.CustomCSS+`" does not exist`) {
		t.Fatalf("buildWithOptions() error = %q, want missing custom CSS path error", err.Error())
	}
}

func TestBuildWithOptionsRejectsInvalidCustomCSSSource(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	customCSSPath := filepath.Join(vaultPath, "custom.css")

	writeBuildTestFile(t, vaultPath, "notes/alpha.md", `---
title: Alpha
date: 2026-04-06
---
# Alpha

Body.
`)
	if err := os.Mkdir(customCSSPath, 0o755); err != nil {
		t.Fatalf("os.Mkdir(%q) error = %v", customCSSPath, err)
	}

	cfg := testBuildSiteConfig()
	cfg.CustomCSS = customCSSPath

	_, err := BuildWithOptions(SiteInput{Config: cfg}, vaultPath, outputPath, Options{})
	if err == nil {
		t.Fatal("BuildWithOptions() error = nil, want invalid custom CSS error")
	}
	if !strings.Contains(err.Error(), `custom CSS "`+customCSSPath+`" must be a regular non-symlink file`) {
		t.Fatalf("BuildWithOptions() error = %q, want invalid custom CSS path error", err.Error())
	}
}

func TestBuildRejectsNonFixedCustomCSSSource(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	nonFixedPath := filepath.Join(vaultPath, "styles", "custom.css")

	writeBuildTestFile(t, vaultPath, "styles/custom.css", "body {}\n")
	writeBuildTestFile(t, vaultPath, "notes/alpha.md", `---
title: Alpha
date: 2026-04-06
---
# Alpha

Body.
`)

	cfg := testBuildSiteConfig()
	cfg.CustomCSS = nonFixedPath

	_, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{})
	if err == nil {
		t.Fatal("buildWithOptions() error = nil, want fixed custom CSS path error")
	}
	if !strings.Contains(err.Error(), `custom CSS "`+nonFixedPath+`" must be the fixed vault input`) {
		t.Fatalf("buildWithOptions() error = %q, want fixed custom CSS path error", err.Error())
	}
}

func TestBuildComposesWithLoadForBuildWhenAutoDetectedCustomCSSDisappears(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	configPath := filepath.Join(vaultPath, "obsite.yaml")
	customCSSPath := filepath.Join(vaultPath, "custom.css")

	writeBuildTestFile(t, vaultPath, "notes/alpha.md", `---
title: Alpha
date: 2026-04-06
---
# Alpha

Body.
`)
	if err := os.WriteFile(configPath, []byte("title: Garden Notes\nbaseURL: https://example.com/blog\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", configPath, err)
	}
	if err := os.WriteFile(customCSSPath, []byte("body { background: linen; }\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", customCSSPath, err)
	}

	input, err := LoadSiteInput(vaultPath)
	if err != nil {
		t.Fatalf("LoadSiteInput(%q) error = %v", vaultPath, err)
	}
	if input.Config.CustomCSS != customCSSPath {
		t.Fatalf("input.Config.CustomCSS = %q, want %q", input.Config.CustomCSS, customCSSPath)
	}

	if err := os.Remove(customCSSPath); err != nil {
		t.Fatalf("os.Remove(%q) error = %v", customCSSPath, err)
	}

	_, err = BuildWithOptions(input, vaultPath, outputPath, Options{})
	if err == nil {
		t.Fatal("BuildWithOptions() error = nil, want explicit missing auto-detected custom CSS error")
	}
	if !strings.Contains(err.Error(), `custom CSS "`+customCSSPath+`" does not exist`) {
		t.Fatalf("BuildWithOptions() error = %q, want missing custom CSS path error", err.Error())
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

	_, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{})
	if err == nil {
		t.Fatal("buildWithOptions() error = nil, want explicit symlink custom CSS error")
	}
	if !strings.Contains(err.Error(), `custom CSS "`+customCSSPath+`" must be a regular non-symlink file`) {
		t.Fatalf("buildWithOptions() error = %q, want explicit symlink custom CSS error", err.Error())
	}
}

func TestBuildKeepsCustomCSSPathReservedFromReferencedAssets(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	customCSSContent := "body { color: tomato; }\n"
	attachmentCSSContent := "body { color: dodgerblue; }\n"
	customCSSPath := filepath.Join(vaultPath, "custom.css")

	writeBuildTestFile(t, vaultPath, "custom.css", customCSSContent)
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
	if note.Summary != "" {
		t.Fatalf("result.Index.Notes[notes/summary.md].Summary = %q, want shared index note left untouched", note.Summary)
	}
	if got := result.RecentNotes[0].Summary; got != "Summary carried through the build result." {
		t.Fatalf("result.RecentNotes[0].Summary = %q, want %q", got, "Summary carried through the build result.")
	}
}

func TestBuildSummariesFollowNormalizedRenderedVisibleContent(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "notes/guide.md", `---
title: Guide
date: 2026-04-06
---
# Guide

Body after title.
`)
	writeBuildTestFile(t, vaultPath, "notes/details.md", `---
title: Details
date: 2026-04-05
---
Visible intro.

<details><summary>Expand</summary><p>Hidden body words.</p></details>

Visible outro.
`)
	writeBuildTestFile(t, vaultPath, "notes/source.md", `---
title: Source
date: 2026-04-04
---
Embedded intro.

Embedded outro.
`)
	writeBuildTestFile(t, vaultPath, "notes/host.md", `---
title: Host
date: 2026-04-03
---
![[Source]]

Tail words.
`)

	cfg := testBuildSiteConfig()
	cfg.Popover.Enabled = true

	result, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{})
	if err != nil {
		t.Fatalf("buildWithOptions() error = %v", err)
	}

	guide := result.Index.Notes["notes/guide.md"]
	if guide == nil {
		t.Fatal("result.Index.Notes[notes/guide.md] = nil, want note")
	}
	if guide.Summary != "" {
		t.Fatalf("guide.Summary = %q, want shared index note left untouched", guide.Summary)
	}
	guideDescription := mustRegexSubmatch(t, htmlHead(t, readBuildOutputFile(t, outputPath, "guide/index.html")), `(?is)<meta[^>]*name=?"?description"?[^>]*content="([^"]*)"`)
	if guideDescription != "Body after title." {
		t.Fatalf("guide meta description = %q, want %q", guideDescription, "Body after title.")
	}

	details := result.Index.Notes["notes/details.md"]
	if details == nil {
		t.Fatal("result.Index.Notes[notes/details.md] = nil, want note")
	}
	if details.Summary != "" {
		t.Fatalf("details.Summary = %q, want shared index note left untouched", details.Summary)
	}
	detailsDescription := mustRegexSubmatch(t, htmlHead(t, readBuildOutputFile(t, outputPath, "details/index.html")), `(?is)<meta[^>]*name=?"?description"?[^>]*content="([^"]*)"`)
	if detailsDescription != "Visible intro. Expand Visible outro." {
		t.Fatalf("details meta description = %q, want %q", detailsDescription, "Visible intro. Expand Visible outro.")
	}
	if strings.Contains(detailsDescription, "Hidden body words") {
		t.Fatalf("details meta description = %q, want collapsed body omitted", detailsDescription)
	}

	host := result.Index.Notes["notes/host.md"]
	if host == nil {
		t.Fatal("result.Index.Notes[notes/host.md] = nil, want note")
	}
	if host.Summary != "" {
		t.Fatalf("host.Summary = %q, want shared index note left untouched", host.Summary)
	}
	if got := result.RecentNotes[len(result.RecentNotes)-1].Summary; got != "Embedded intro. Embedded outro. Tail words." {
		t.Fatalf("recent host summary = %q, want %q", got, "Embedded intro. Embedded outro. Tail words.")
	}

	var payload popoverPayload
	if err := json.Unmarshal(readBuildOutputFile(t, outputPath, "_popover/host.json"), &payload); err != nil {
		t.Fatalf("json.Unmarshal(_popover/host.json) error = %v", err)
	}
	if payload.Summary != "Embedded intro. Embedded outro. Tail words." {
		t.Fatalf("host popover summary = %q, want %q", payload.Summary, "Embedded intro. Embedded outro. Tail words.")
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
	if alphaNote.Summary != "" {
		t.Fatalf("alphaNote.Summary = %q, want shared index note left untouched", alphaNote.Summary)
	}
	if payload.Summary != "Alpha summary body. Beta" {
		t.Fatalf("popover summary = %q, want %q", payload.Summary, "Alpha summary body. Beta")
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

	assertPopoverMount := func(t *testing.T, relPath string) []byte {
		t.Helper()

		html := readBuildOutputFile(t, outputPath, relPath)
		if !containsAny(html, `data-obsite-popover`, `data-obsite-popover=""`) {
			t.Fatalf("%s missing Popover runtime flag\n%s", relPath, html)
		}
		if !containsAny(html, `id="obsite-popover-card"`, `id=obsite-popover-card`) || !containsAny(html, `role="tooltip"`, `role=tooltip`) {
			t.Fatalf("%s missing accessible Popover mount\n%s", relPath, html)
		}
		for _, forbidden := range [][]byte{[]byte("mouseenter"), []byte("focusin"), []byte("window.fetch"), []byte("data-popover-root"), []byte("data-site-root-rel")} {
			if bytes.Contains(html, forbidden) {
				t.Fatalf("%s retains inline Popover runtime %q\n%s", relPath, forbidden, html)
			}
		}
		return html
	}

	noteHTML := assertPopoverMount(t, "alpha/index.html")
	indexHTML := assertPopoverMount(t, "index.html")
	tagHTML := assertPopoverMount(t, "tags/topic/index.html")
	folderHTML := assertPopoverMount(t, "journal/index.html")
	timelineHTML := assertPopoverMount(t, "notes/index.html")
	notFoundHTML := assertPopoverMount(t, "404.html")

	runtimePath, err := internalrender.SharedRuntimeOutputPath()
	if err != nil {
		t.Fatal(err)
	}
	runtimeJS := readBuildOutputFile(t, outputPath, runtimePath)
	for _, want := range [][]byte{[]byte(`document.addEventListener("mouseover"`), []byte(`document.addEventListener("focusin"`), []byte("Popover request failed."), []byte(`target.closest("[data-popover-path]")`)} {
		if !bytes.Contains(runtimeJS, want) {
			t.Fatalf("shared runtime missing delegated Popover behavior %q", want)
		}
	}
	if bytes.Contains(runtimeJS, []byte("cache.delete(popoverURL)")) {
		t.Fatal("shared runtime retries failed Popover requests")
	}
	if !containsAny(
		noteHTML,
		`data-popover-path="beta"`,
		`data-popover-path=beta`,
	) {
		t.Fatalf("note page missing build-time popover link annotation for resolved note links\n%s", noteHTML)
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
	if !bytes.Contains(styleCSS, []byte("pointer-events:auto")) {
		t.Fatalf("style.css prevents Popover hover persistence\n%s", styleCSS)
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

	sidebarJSON := readBuildOutputFile(t, outputPath, sidebarJSONOutputPath)
	var nodes []model.SidebarNode
	if err := json.Unmarshal(sidebarJSON, &nodes); err != nil {
		t.Fatalf("json.Unmarshal(sidebar.json) error = %v\n%s", err, sidebarJSON)
	}
	if len(nodes) != 3 {
		t.Fatalf("len(sidebar nodes) = %d, want %d", len(nodes), 3)
	}
	if guide := findSidebarNodeByURL(nodes, "guide/"); guide == nil || guide.IsDir {
		t.Fatalf("guide sidebar node = %#v, want note leaf", guide)
	}
	if notes := findSidebarNodeByURL(nodes, "notes/"); notes == nil || !notes.IsDir || len(notes.Children) != 1 || notes.Children[0].Name != "Guide" {
		t.Fatalf("notes sidebar node = %#v, want notes directory with only the published guide child", notes)
	}
	if docs := findSidebarNodeByURL(nodes, "docs/"); docs == nil || !docs.IsDir || len(docs.Children) != 1 || docs.Children[0].Name != "Reference" {
		t.Fatalf("docs sidebar node = %#v, want docs directory with reference child", docs)
	}
	if draft := findSidebarNodeByURL(nodes, "drafts/"); draft != nil {
		t.Fatalf("drafts sidebar node = %#v, want unpublished-only directory to be excluded", draft)
	}
	if root := findSidebarNodeByURL(nodes, "root/"); root == nil || root.IsDir || root.Name != "Root" {
		t.Fatalf("root sidebar node = %#v, want published root note node", root)
	}
	if bytes.Contains(sidebarJSON, []byte("isActive")) {
		t.Fatalf("sidebar.json contains per-page active state\n%s", sidebarJSON)
	}

	for _, relPath := range []string{"guide/index.html", "docs/index.html", "404.html"} {
		html := readBuildOutputFile(t, outputPath, relPath)
		if !containsAny(html, `data-sidebar-toggle`, `data-sidebar-toggle=""`) {
			t.Fatalf("%s missing sidebar toggle markup\n%s", relPath, html)
		}
		for _, forbidden := range [][]byte{[]byte(`id="sidebar-data"`), []byte(`id=sidebar-data`), []byte(`"children"`), []byte("obsite.sidebar.expanded.v1")} {
			if bytes.Contains(html, forbidden) {
				t.Fatalf("%s embeds Sidebar data/runtime %q\n%s", relPath, forbidden, html)
			}
		}
	}

	cfg.Sidebar.Enabled = false
	if _, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{}); err != nil {
		t.Fatalf("disabled Sidebar rebuild error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(outputPath, filepath.FromSlash(sidebarJSONOutputPath))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("os.Stat(sidebar.json) error = %v, want not-exist when disabled", err)
	}
	if html := readBuildOutputFile(t, outputPath, "guide/index.html"); bytes.Contains(html, []byte("data-obsite-sidebar")) || bytes.Contains(html, []byte("data-sidebar-shell")) {
		t.Fatalf("disabled Sidebar page retains mount points\n%s", html)
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
	if containsAny(noteHTML, `data-obsite-popover`, `data-popover-card`, `data-popover-root=`, `data-popover-path=`) {
		t.Fatalf("note page unexpectedly includes Popover wiring when disabled\n%s", noteHTML)
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

func TestBuildRSSChannelLastBuildDateTracksUpdatedNotes(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	alphaPublished := time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC)
	betaPublished := time.Date(2026, 4, 7, 0, 0, 0, 0, time.UTC)

	writeBuildTestFile(t, vaultPath, "notes/alpha.md", `---
title: Alpha
date: 2026-04-05
updated: 2026-04-08T12:00:00Z
---
Alpha summary.
`)
	writeBuildTestFile(t, vaultPath, "notes/beta.md", `---
title: Beta
date: 2026-04-07
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
	if len(result.RecentNotes) != 2 {
		t.Fatalf("len(result.RecentNotes) = %d, want %d", len(result.RecentNotes), 2)
	}

	var alphaSummary *model.NoteSummary
	expectedLastBuildDate := time.Time{}
	for index := range result.RecentNotes {
		summary := &result.RecentNotes[index]
		candidate := summary.LastModified.Round(0).UTC().Truncate(time.Second)
		if expectedLastBuildDate.IsZero() || candidate.After(expectedLastBuildDate) {
			expectedLastBuildDate = candidate
		}
		if summary.Title == "Alpha" {
			alphaSummary = summary
		}
	}
	if alphaSummary == nil {
		t.Fatal("result.RecentNotes missing Alpha summary")
	}
	if !alphaSummary.LastModified.After(alphaSummary.Date) {
		t.Fatalf("Alpha summary LastModified = %v, want a value after published date %v", alphaSummary.LastModified, alphaSummary.Date)
	}

	rssXML := readBuildOutputFile(t, outputPath, "index.xml")
	if !bytes.Contains(rssXML, []byte(`<lastBuildDate>`+expectedLastBuildDate.Format(time.RFC1123Z)+`</lastBuildDate>`)) {
		t.Fatalf("index.xml missing updated channel lastBuildDate %q\n%s", expectedLastBuildDate.Format(time.RFC1123Z), rssXML)
	}
	if !bytes.Contains(rssXML, []byte(`<pubDate>`+alphaPublished.Format(time.RFC1123Z)+`</pubDate>`)) {
		t.Fatalf("index.xml missing Alpha publication date %q\n%s", alphaPublished.Format(time.RFC1123Z), rssXML)
	}
	if !bytes.Contains(rssXML, []byte(`<pubDate>`+betaPublished.Format(time.RFC1123Z)+`</pubDate>`)) {
		t.Fatalf("index.xml missing Beta publication date %q\n%s", betaPublished.Format(time.RFC1123Z), rssXML)
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

func TestBuildUsesCanonicalRSSDefault(t *testing.T) {
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
	cfg.RSS = internalconfig.Defaults().RSS

	result, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{})
	if err != nil {
		t.Fatalf("buildWithOptions() error = %v", err)
	}
	if result == nil {
		t.Fatal("buildWithOptions() = nil result, want build result")
	}

	_ = readBuildOutputFile(t, outputPath, "index.xml")

	for _, page := range []struct {
		name string
		data []byte
	}{
		{name: "index.html", data: readBuildOutputFile(t, outputPath, "index.html")},
		{name: "alpha/index.html", data: readBuildOutputFile(t, outputPath, "alpha/index.html")},
	} {
		if !containsAny(
			page.data,
			`<link rel="alternate" type="application/rss+xml"`,
			`<link rel=alternate type=application/rss+xml`,
		) {
			t.Fatalf("%s missing RSS autodiscovery link for canonical default\n%s", page.name, page.data)
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

func TestBuildClassifiesCanvasLinksAndEmbedsAsUnsupportedSyntax(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "notes/current.md", `---
title: Current
date: 2026-04-06
---
# Current

[[plan.canvas]]
![[plan.canvas]]
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
	if len(result.Diagnostics) != 3 {
		t.Fatalf("len(result.Diagnostics) = %d, want 3 canvas warnings", len(result.Diagnostics))
	}
	if result.WarningCount != 3 {
		t.Fatalf("result.WarningCount = %d, want %d", result.WarningCount, 3)
	}
	for i, diagnostic := range result.Diagnostics {
		if diagnostic.Severity != diag.SeverityWarning {
			t.Fatalf("result.Diagnostics[%d].Severity = %q, want %q", i, diagnostic.Severity, diag.SeverityWarning)
		}
		if diagnostic.Kind != diag.KindUnsupportedSyntax {
			t.Fatalf("result.Diagnostics[%d].Kind = %q, want %q", i, diagnostic.Kind, diag.KindUnsupportedSyntax)
		}
	}
	if result.Diagnostics[0].Location.Path != "boards/plan.canvas" {
		t.Fatalf("result.Diagnostics[0].Location.Path = %q, want %q", result.Diagnostics[0].Location.Path, "boards/plan.canvas")
	}
	if result.Diagnostics[1].Location.Path != "notes/current.md" || result.Diagnostics[1].Location.Line != 7 {
		t.Fatalf("result.Diagnostics[1].Location = %#v, want notes/current.md:7", result.Diagnostics[1].Location)
	}
	if !strings.Contains(result.Diagnostics[1].Message, `wikilink "plan.canvas" targets unsupported canvas content`) {
		t.Fatalf("result.Diagnostics[1].Message = %q, want canvas wikilink degradation", result.Diagnostics[1].Message)
	}
	if result.Diagnostics[2].Location.Path != "notes/current.md" || result.Diagnostics[2].Location.Line != 8 {
		t.Fatalf("result.Diagnostics[2].Location = %#v, want notes/current.md:8", result.Diagnostics[2].Location)
	}
	if !strings.Contains(result.Diagnostics[2].Message, `embed "plan.canvas" targets unsupported canvas content`) {
		t.Fatalf("result.Diagnostics[2].Message = %q, want canvas embed degradation", result.Diagnostics[2].Message)
	}

	summary := diagnostics.String()
	if !strings.Contains(summary, "Warnings (3)") {
		t.Fatalf("diagnostics summary missing warning count\n%s", summary)
	}
	if strings.Contains(summary, "[deadlink]") {
		t.Fatalf("diagnostics summary = %q, want canvas links kept out of deadlink warnings", summary)
	}
	if strings.Contains(summary, "[unresolved_asset]") {
		t.Fatalf("diagnostics summary = %q, want canvas embeds kept out of unresolved asset warnings", summary)
	}
	if !strings.Contains(summary, `wikilink "plan.canvas" targets unsupported canvas content`) {
		t.Fatalf("diagnostics summary missing canvas wikilink degradation\n%s", summary)
	}
	if !strings.Contains(summary, `embed "plan.canvas" targets unsupported canvas content`) {
		t.Fatalf("diagnostics summary missing canvas embed degradation\n%s", summary)
	}
}

func TestBuildMissingCanvasTargetsStayDeadLinks(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "notes/current.md", `---
title: Current
date: 2026-04-06
---
# Current

[[missing.canvas]]
![[missing.canvas]]
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
	if len(result.Diagnostics) != 2 {
		t.Fatalf("len(result.Diagnostics) = %d, want 2 deadlink warnings", len(result.Diagnostics))
	}
	if result.WarningCount != 2 {
		t.Fatalf("result.WarningCount = %d, want %d", result.WarningCount, 2)
	}
	for i, diagnostic := range result.Diagnostics {
		if diagnostic.Severity != diag.SeverityWarning {
			t.Fatalf("result.Diagnostics[%d].Severity = %q, want %q", i, diagnostic.Severity, diag.SeverityWarning)
		}
		if diagnostic.Kind != diag.KindDeadLink {
			t.Fatalf("result.Diagnostics[%d].Kind = %q, want %q", i, diagnostic.Kind, diag.KindDeadLink)
		}
	}
	if result.Diagnostics[0].Location.Path != "notes/current.md" || result.Diagnostics[0].Location.Line != 7 {
		t.Fatalf("result.Diagnostics[0].Location = %#v, want notes/current.md:7", result.Diagnostics[0].Location)
	}
	if !strings.Contains(result.Diagnostics[0].Message, `wikilink "missing.canvas" could not be resolved`) {
		t.Fatalf("result.Diagnostics[0].Message = %q, want dead wikilink warning", result.Diagnostics[0].Message)
	}
	if result.Diagnostics[1].Location.Path != "notes/current.md" || result.Diagnostics[1].Location.Line != 8 {
		t.Fatalf("result.Diagnostics[1].Location = %#v, want notes/current.md:8", result.Diagnostics[1].Location)
	}
	if !strings.Contains(result.Diagnostics[1].Message, `note embed "missing.canvas" could not be resolved`) {
		t.Fatalf("result.Diagnostics[1].Message = %q, want dead embed warning", result.Diagnostics[1].Message)
	}

	summary := diagnostics.String()
	if strings.Contains(summary, "unsupported canvas content") {
		t.Fatalf("diagnostics summary = %q, want missing canvas targets kept out of unsupported syntax warnings", summary)
	}
	if !strings.Contains(summary, `[deadlink]`) {
		t.Fatalf("diagnostics summary missing deadlink warnings\n%s", summary)
	}
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
	assetEntryNames := make([]string, 0, len(assetEntries))
	for _, entry := range assetEntries {
		assetEntryNames = append(assetEntryNames, entry.Name())
	}
	sort.Strings(assetEntryNames)
	if !reflect.DeepEqual(assetEntryNames, []string{"hero.png", "obsite", "obsite-runtime"}) {
		t.Fatalf("output asset entries = %#v, want %#v", assetEntryNames, []string{"hero.png", "obsite", "obsite-runtime"})
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

func TestStagedOutputPublisherRemovesFirstPublicationWhenRenameMovesThenReportsError(t *testing.T) {
	root := t.TempDir()
	vaultPath := filepath.Join(root, "vault")
	outputPath := filepath.Join(root, "site")
	if err := os.MkdirAll(vaultPath, 0o755); err != nil {
		t.Fatal(err)
	}
	publisher, err := prepareStagedOutputPublisher(vaultPath, outputPath)
	if err != nil {
		t.Fatal(err)
	}
	stagingPath := publisher.stagingPath
	if err := os.WriteFile(filepath.Join(stagingPath, "index.html"), []byte("unconfirmed output"), 0o644); err != nil {
		t.Fatal(err)
	}
	publishErr := errors.New("first publish rename reported failure after move")
	renameCalls := 0
	overrideStagedOutputFileOps(t, func(oldPath string, newPath string) error {
		renameCalls++
		if renameCalls == 1 {
			if err := os.Rename(oldPath, newPath); err != nil {
				return err
			}
			return publishErr
		}
		return os.Rename(oldPath, newPath)
	}, nil, nil)

	err = publisher.Finalize(true)
	if !errors.Is(err, publishErr) {
		t.Fatalf("Finalize() error = %v, want %v", err, publishErr)
	}
	if renameCalls != 2 {
		t.Fatalf("rename calls = %d, want publish attempt plus quarantine", renameCalls)
	}
	if _, err := os.Stat(outputPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("formal first output = %v, want absent after failed publish", err)
	}
	if _, err := os.Stat(stagingPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging output = %v, want absent", err)
	}
	if matches, _ := filepath.Glob(filepath.Join(root, managedOutputTempPattern(outputPath, "failed"))); len(matches) != 0 {
		t.Fatalf("failed-output quarantine paths = %#v, want cleaned", matches)
	}
}

func TestStagedOutputPublisherPreservesConcurrentFirstBuildDestination(t *testing.T) {
	root := t.TempDir()
	vaultPath := filepath.Join(root, "vault")
	outputPath := filepath.Join(root, "site")
	if err := os.MkdirAll(vaultPath, 0o755); err != nil {
		t.Fatal(err)
	}
	publisher, err := prepareStagedOutputPublisher(vaultPath, outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(publisher.stagingPath, "index.html"), []byte("staged"), 0o644); err != nil {
		t.Fatal(err)
	}
	publishErr := errors.New("publish rename failed")
	overrideStagedOutputFileOps(t, func(oldPath string, newPath string) error {
		if err := os.MkdirAll(newPath, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(newPath, "user.txt"), []byte("concurrent"), 0o644); err != nil {
			return err
		}
		return publishErr
	}, nil, nil)

	err = publisher.Finalize(true)
	if !errors.Is(err, publishErr) || !strings.Contains(err.Error(), "different filesystem object") {
		t.Fatalf("Finalize() error = %v, want publish/concurrent destination errors", err)
	}
	if got := string(readBuildOutputFile(t, outputPath, "user.txt")); got != "concurrent" {
		t.Fatalf("concurrent destination = %q, want preserved", got)
	}
}

func TestStagedOutputPublisherPreservesConcurrentReplacementAndBackup(t *testing.T) {
	publisher, outputPath, _ := prepareStagedOutputPublisherForFailureTest(t)
	publishErr := errors.New("publish rename failed")
	renameCalls := 0
	overrideStagedOutputFileOps(t, func(oldPath string, newPath string) error {
		renameCalls++
		if renameCalls == 2 {
			if err := os.MkdirAll(newPath, 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(newPath, "user.txt"), []byte("replacement"), 0o644); err != nil {
				return err
			}
			return publishErr
		}
		return os.Rename(oldPath, newPath)
	}, nil, nil)

	err := publisher.Finalize(true)
	if !errors.Is(err, publishErr) || !strings.Contains(err.Error(), "different filesystem object") {
		t.Fatalf("Finalize() error = %v, want publish/concurrent replacement errors", err)
	}
	if got := string(readBuildOutputFile(t, outputPath, "user.txt")); got != "replacement" {
		t.Fatalf("concurrent replacement = %q, want preserved", got)
	}
	matches, globErr := filepath.Glob(filepath.Join(filepath.Dir(outputPath), managedOutputTempPattern(outputPath, "backup")))
	if globErr != nil || len(matches) != 1 {
		t.Fatalf("recoverable backup paths = %#v, %v", matches, globErr)
	}
	if got := string(readBuildOutputFile(t, matches[0], "index.html")); got != "stable output" {
		t.Fatalf("backup = %q, want stable output", got)
	}
}

func TestStagedOutputPublisherRestoresConcurrentObjectMovedByQuarantineRace(t *testing.T) {
	publisher, outputPath, _ := prepareStagedOutputPublisherForFailureTest(t)
	publishErr := errors.New("publish rename reported failure after move")
	renameCalls := 0
	overrideStagedOutputFileOps(t, func(oldPath string, newPath string) error {
		renameCalls++
		switch renameCalls {
		case 2:
			if err := os.Rename(oldPath, newPath); err != nil {
				return err
			}
			return publishErr
		case 3:
			if err := os.RemoveAll(oldPath); err != nil {
				return err
			}
			if err := os.MkdirAll(oldPath, 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(oldPath, "user.txt"), []byte("concurrent after identity check"), 0o644); err != nil {
				return err
			}
			return os.Rename(oldPath, newPath)
		default:
			return os.Rename(oldPath, newPath)
		}
	}, nil, nil)

	err := publisher.Finalize(true)
	if !errors.Is(err, publishErr) || !strings.Contains(err.Error(), "different filesystem object") {
		t.Fatalf("Finalize() error = %v, want publish/quarantine race errors", err)
	}
	if renameCalls != 4 {
		t.Fatalf("rename calls = %d, want backup, publish, quarantine, concurrent restore", renameCalls)
	}
	if got := string(readBuildOutputFile(t, outputPath, "user.txt")); got != "concurrent after identity check" {
		t.Fatalf("concurrent output = %q, want restored", got)
	}
	matches, globErr := filepath.Glob(filepath.Join(filepath.Dir(outputPath), managedOutputTempPattern(outputPath, "backup")))
	if globErr != nil || len(matches) != 1 {
		t.Fatalf("recoverable backup paths = %#v, %v", matches, globErr)
	}
}

func TestStagedOutputPublisherLeavesOldOutputUntouchedWhenBackupRenameFails(t *testing.T) {
	publisher, outputPath, stagingPath := prepareStagedOutputPublisherForFailureTest(t)
	backupErr := errors.New("backup rename failed")
	renameCalls := 0
	overrideStagedOutputFileOps(t, func(oldPath string, newPath string) error {
		renameCalls++
		if renameCalls == 1 {
			return backupErr
		}
		return os.Rename(oldPath, newPath)
	}, nil, nil)

	err := publisher.Finalize(true)
	if !errors.Is(err, backupErr) {
		t.Fatalf("Finalize() error = %v, want %v", err, backupErr)
	}
	if renameCalls != 1 {
		t.Fatalf("rename calls = %d, want 1", renameCalls)
	}
	if got := string(readBuildOutputFile(t, outputPath, "index.html")); got != "stable output" {
		t.Fatalf("old output = %q, want untouched stable output", got)
	}
	if _, err := os.Stat(stagingPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging path cleanup error = %v", err)
	}
}

func TestStagedOutputPublisherRestoresOldOutputWhenBackupRenameMovesThenReportsError(t *testing.T) {
	publisher, outputPath, stagingPath := prepareStagedOutputPublisherForFailureTest(t)
	backupErr := errors.New("backup rename reported failure after move")
	renameCalls := 0
	overrideStagedOutputFileOps(t, func(oldPath string, newPath string) error {
		renameCalls++
		if renameCalls == 1 {
			if err := os.Rename(oldPath, newPath); err != nil {
				return err
			}
			return backupErr
		}
		return os.Rename(oldPath, newPath)
	}, nil, nil)

	err := publisher.Finalize(true)
	if !errors.Is(err, backupErr) {
		t.Fatalf("Finalize() error = %v, want %v", err, backupErr)
	}
	if renameCalls != 2 {
		t.Fatalf("rename calls = %d, want uncertain backup rename plus restore", renameCalls)
	}
	if got := string(readBuildOutputFile(t, outputPath, "index.html")); got != "stable output" {
		t.Fatalf("formal output = %q, want restored stable output", got)
	}
	if _, err := os.Stat(stagingPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging path cleanup error = %v", err)
	}
}

func TestStagedOutputPublisherKeepsCommittedSiteWhenPartialBackupCleanupFails(t *testing.T) {
	publisher, outputPath, stagingPath := prepareStagedOutputPublisherForFailureTest(t)
	cleanupErr := errors.New("partial backup cleanup failed")
	renameCalls := 0
	overrideStagedOutputFileOps(t,
		func(oldPath string, newPath string) error {
			renameCalls++
			return os.Rename(oldPath, newPath)
		},
		func(target string) error {
			if strings.Contains(filepath.Base(target), "-obsite-backup-") {
				_ = os.Remove(filepath.Join(target, "index.html"))
				return cleanupErr
			}
			return os.RemoveAll(target)
		},
		nil,
	)

	err := publisher.Finalize(true)
	if !errors.Is(err, cleanupErr) {
		t.Fatalf("Finalize() error = %v, want %v", err, cleanupErr)
	}
	if renameCalls != 2 {
		t.Fatalf("rename calls = %d, want committed old->backup and stage->output only", renameCalls)
	}
	if got := string(readBuildOutputFile(t, outputPath, "index.html")); got != "new output" {
		t.Fatalf("formal output = %q, want intact committed new output", got)
	}
	if _, err := os.Stat(stagingPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging path = %v, want removed", err)
	}
	matches, globErr := filepath.Glob(filepath.Join(filepath.Dir(outputPath), managedOutputTempPattern(outputPath, "backup")))
	if globErr != nil || len(matches) != 1 {
		t.Fatalf("partial obsolete backup paths = %#v, %v; want one retained cleanup artifact", matches, globErr)
	}
}

func TestStagedOutputPublisherRestoresOldOutputBeforePartialQuarantineCleanupFailure(t *testing.T) {
	publisher, outputPath, _ := prepareStagedOutputPublisherForFailureTest(t)
	publishErr := errors.New("publish rename reported failure after moving output")
	quarantineCleanupErr := errors.New("partial quarantine cleanup failed")
	renameCalls := 0
	overrideStagedOutputFileOps(t,
		func(oldPath string, newPath string) error {
			renameCalls++
			if renameCalls == 2 {
				if err := os.Rename(oldPath, newPath); err != nil {
					return err
				}
				return publishErr
			}
			return os.Rename(oldPath, newPath)
		},
		func(target string) error {
			if strings.Contains(filepath.Base(target), "-obsite-failed-") {
				_ = os.Remove(filepath.Join(target, "index.html"))
				return quarantineCleanupErr
			}
			return os.RemoveAll(target)
		},
		nil,
	)

	err := publisher.Finalize(true)
	if !errors.Is(err, publishErr) || !errors.Is(err, quarantineCleanupErr) {
		t.Fatalf("Finalize() error = %v, want joined publish/quarantine errors", err)
	}
	if renameCalls != 4 {
		t.Fatalf("rename calls = %d, want full quarantine/restore transaction", renameCalls)
	}
	if got := string(readBuildOutputFile(t, outputPath, "index.html")); got != "stable output" {
		t.Fatalf("formal output = %q, want restored stable output", got)
	}
	if matches, _ := filepath.Glob(filepath.Join(filepath.Dir(outputPath), managedOutputTempPattern(outputPath, "backup"))); len(matches) != 0 {
		t.Fatalf("backup paths after restore = %#v", matches)
	}
	matches, globErr := filepath.Glob(filepath.Join(filepath.Dir(outputPath), managedOutputTempPattern(outputPath, "failed")))
	if globErr != nil || len(matches) != 1 {
		t.Fatalf("failed-output quarantine paths = %#v, %v; want one partial quarantine", matches, globErr)
	}
}

func TestStagedOutputPublisherRestoresBackupWhenQuarantineRenameMovesThenReportsError(t *testing.T) {
	publisher, outputPath, _ := prepareStagedOutputPublisherForFailureTest(t)
	publishErr := errors.New("publish rename reported failure after move")
	quarantineErr := errors.New("quarantine rename reported failure after move")
	renameCalls := 0
	overrideStagedOutputFileOps(t, func(oldPath string, newPath string) error {
		renameCalls++
		if renameCalls == 2 || renameCalls == 3 {
			if err := os.Rename(oldPath, newPath); err != nil {
				return err
			}
			if renameCalls == 2 {
				return publishErr
			}
			return quarantineErr
		}
		return os.Rename(oldPath, newPath)
	}, nil, nil)

	err := publisher.Finalize(true)
	if !errors.Is(err, publishErr) || !errors.Is(err, quarantineErr) {
		t.Fatalf("Finalize() error = %v, want joined publish/quarantine errors", err)
	}
	if renameCalls != 4 {
		t.Fatalf("rename calls = %d, want backup, uncertain publish, uncertain quarantine, restore", renameCalls)
	}
	if got := string(readBuildOutputFile(t, outputPath, "index.html")); got != "stable output" {
		t.Fatalf("formal output = %q, want restored stable output", got)
	}
	if matches, _ := filepath.Glob(filepath.Join(filepath.Dir(outputPath), managedOutputTempPattern(outputPath, "backup"))); len(matches) != 0 {
		t.Fatalf("backup paths = %#v, want none", matches)
	}
	if matches, _ := filepath.Glob(filepath.Join(filepath.Dir(outputPath), managedOutputTempPattern(outputPath, "failed"))); len(matches) != 0 {
		t.Fatalf("failed-output paths = %#v, want cleaned", matches)
	}
}

func TestStagedOutputPublisherJoinsRestoreFailureAfterPublishRenameError(t *testing.T) {
	publisher, outputPath, _ := prepareStagedOutputPublisherForFailureTest(t)
	publishErr := errors.New("publish rename failed")
	restoreErr := errors.New("restore rename failed")
	renameCalls := 0
	overrideStagedOutputFileOps(t, func(oldPath string, newPath string) error {
		renameCalls++
		switch renameCalls {
		case 2:
			return publishErr
		case 3:
			return restoreErr
		default:
			return os.Rename(oldPath, newPath)
		}
	}, nil, nil)

	err := publisher.Finalize(true)
	if !errors.Is(err, publishErr) || !errors.Is(err, restoreErr) {
		t.Fatalf("Finalize() error = %v, want joined publish/restore errors", err)
	}
	if _, err := os.Stat(outputPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("formal output after failed restore = %v, want absent rather than mixed", err)
	}
	matches, globErr := filepath.Glob(filepath.Join(filepath.Dir(outputPath), managedOutputTempPattern(outputPath, "backup")))
	if globErr != nil || len(matches) != 1 {
		t.Fatalf("recoverable backup paths = %#v, %v; want one retained backup", matches, globErr)
	}
	if got := string(readBuildOutputFile(t, matches[0], "index.html")); got != "stable output" {
		t.Fatalf("retained backup = %q, want stable output", got)
	}
}

func TestStagedOutputPublisherRestoresBackupAndCleansStageWhenPublishFailsAfterBackupCreation(t *testing.T) {
	publisher, outputPath, stagingPath := prepareStagedOutputPublisherForFailureTest(t)
	publishErr := errors.New("publish rename failed")
	renameCalls := 0

	overrideStagedOutputFileOps(t,
		func(oldPath string, newPath string) error {
			renameCalls++
			if renameCalls == 2 {
				if oldPath != stagingPath || newPath != outputPath {
					t.Fatalf("stagedOutputRename() call 2 = %q -> %q, want %q -> %q", oldPath, newPath, stagingPath, outputPath)
				}
				return publishErr
			}
			return os.Rename(oldPath, newPath)
		},
		nil,
		nil,
	)

	err := publisher.Finalize(true)
	if !errors.Is(err, publishErr) {
		t.Fatalf("publisher.Finalize(true) error = %v, want wrapped publish error %v", err, publishErr)
	}
	if renameCalls != 3 {
		t.Fatalf("stagedOutputRename() calls = %d, want %d", renameCalls, 3)
	}

	if got := string(readBuildOutputFile(t, outputPath, "index.html")); got != "stable output" {
		t.Fatalf("restored output = %q, want %q", got, "stable output")
	}
	if _, err := os.Stat(filepath.Join(outputPath, managedOutputMarkerFilename)); err != nil {
		t.Fatalf("os.Stat(output marker) error = %v, want restored managed marker", err)
	}
	if _, err := os.Stat(stagingPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("os.Stat(stagingPath) error = %v, want %v after rollback cleanup", err, os.ErrNotExist)
	}
	if matches, err := filepath.Glob(filepath.Join(filepath.Dir(outputPath), managedOutputTempPattern(outputPath, "backup"))); err != nil {
		t.Fatalf("filepath.Glob(backup pattern) error = %v", err)
	} else if len(matches) != 0 {
		t.Fatalf("backup paths = %#v, want none after restore", matches)
	}
}

func TestStagedOutputPublisherJoinsRollbackCleanupFailureAfterPublishError(t *testing.T) {
	publisher, outputPath, stagingPath := prepareStagedOutputPublisherForFailureTest(t)
	publishErr := errors.New("publish rename failed")
	cleanupErr := errors.New("remove staged output failed")
	renameCalls := 0

	overrideStagedOutputFileOps(t,
		func(oldPath string, newPath string) error {
			renameCalls++
			if renameCalls == 2 {
				if oldPath != stagingPath || newPath != outputPath {
					t.Fatalf("stagedOutputRename() call 2 = %q -> %q, want %q -> %q", oldPath, newPath, stagingPath, outputPath)
				}
				return publishErr
			}
			return os.Rename(oldPath, newPath)
		},
		func(target string) error {
			if target == stagingPath {
				return cleanupErr
			}
			return os.RemoveAll(target)
		},
		nil,
	)

	err := publisher.Finalize(true)
	if !errors.Is(err, publishErr) {
		t.Fatalf("publisher.Finalize(true) error = %v, want wrapped publish error %v", err, publishErr)
	}
	if !errors.Is(err, cleanupErr) {
		t.Fatalf("publisher.Finalize(true) error = %v, want joined cleanup error %v", err, cleanupErr)
	}
	if renameCalls != 3 {
		t.Fatalf("stagedOutputRename() calls = %d, want %d", renameCalls, 3)
	}

	if got := string(readBuildOutputFile(t, outputPath, "index.html")); got != "stable output" {
		t.Fatalf("restored output = %q, want %q", got, "stable output")
	}
	if _, err := os.Stat(stagingPath); err != nil {
		t.Fatalf("os.Stat(stagingPath) error = %v, want staged directory left behind after cleanup failure", err)
	}
	if matches, err := filepath.Glob(filepath.Join(filepath.Dir(outputPath), managedOutputTempPattern(outputPath, "backup"))); err != nil {
		t.Fatalf("filepath.Glob(backup pattern) error = %v", err)
	} else if len(matches) != 0 {
		t.Fatalf("backup paths = %#v, want none after restore", matches)
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

func TestBuildRejectsSymlinkOutputRootBeforeManagedOutputFlow(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	root := t.TempDir()
	targetPath := filepath.Join(root, "managed-target")
	outputPath := filepath.Join(root, "site")

	writeBuildTestFile(t, vaultPath, "notes/alpha.md", `---
title: Alpha
date: 2026-04-06
---
# Alpha

Body.
`)
	if err := writeManagedOutputMarker(targetPath); err != nil {
		t.Fatalf("writeManagedOutputMarker(%q) error = %v", targetPath, err)
	}
	if err := os.WriteFile(filepath.Join(targetPath, "index.html"), []byte("previous output"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(index.html) error = %v", err)
	}
	writeBuildSymlinkOrSkip(t, targetPath, outputPath)

	_, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{
		diagnosticsWriter: io.Discard,
	})
	if err == nil {
		t.Fatal("buildWithOptions() error = nil, want symlink output rejection")
	}
	if !strings.Contains(err.Error(), `output path "`+outputPath+`" must not be a symbolic link`) {
		t.Fatalf("buildWithOptions() error = %v, want symlink output rejection", err)
	}

	info, err := os.Lstat(outputPath)
	if err != nil {
		t.Fatalf("os.Lstat(%q) error = %v, want symlink preserved after rejection", outputPath, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("os.Lstat(%q) mode = %v, want symlink preserved after rejection", outputPath, info.Mode())
	}
	if got := readBuildOutputFile(t, targetPath, "index.html"); string(got) != "previous output" {
		t.Fatalf("target index.html = %q, want %q", string(got), "previous output")
	}
	if _, err := os.Stat(filepath.Join(targetPath, "alpha", "index.html")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("os.Stat(target alpha/index.html) error = %v, want %v after rejection", err, os.ErrNotExist)
	}
	stagePaths, err := filepath.Glob(filepath.Join(root, managedOutputTempPattern(outputPath, "stage")))
	if err != nil {
		t.Fatalf("filepath.Glob(stage pattern) error = %v", err)
	}
	if len(stagePaths) != 0 {
		t.Fatalf("stage paths = %#v, want no staged output created for symlink rejection", stagePaths)
	}
	backupPaths, err := filepath.Glob(filepath.Join(root, managedOutputTempPattern(outputPath, "backup")))
	if err != nil {
		t.Fatalf("filepath.Glob(backup pattern) error = %v", err)
	}
	if len(backupPaths) != 0 {
		t.Fatalf("backup paths = %#v, want no backup path reserved for symlink rejection", backupPaths)
	}
}

func TestBuildResolvesOutputParentSymlinkToVaultDescendant(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	root := t.TempDir()
	symlinkParentPath := filepath.Join(root, "linkdir")
	outputPath := filepath.Join(symlinkParentPath, "site")
	targetOutputPath := filepath.Join(vaultPath, "site")

	writeBuildTestFile(t, vaultPath, "notes/alpha.md", `---
title: Alpha
date: 2026-04-06
---
# Alpha

Body.
`)
	if err := writeManagedOutputMarker(targetOutputPath); err != nil {
		t.Fatalf("writeManagedOutputMarker(%q) error = %v", targetOutputPath, err)
	}
	if err := os.WriteFile(filepath.Join(targetOutputPath, "index.html"), []byte("previous output"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(index.html) error = %v", err)
	}
	writeBuildSymlinkOrSkip(t, vaultPath, symlinkParentPath)

	result, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{
		diagnosticsWriter: io.Discard,
	})
	if err != nil {
		t.Fatalf("buildWithOptions() error = %v", err)
	}
	if result.OutputPath != targetOutputPath {
		t.Fatalf("result.OutputPath = %q, want resolved path %q", result.OutputPath, targetOutputPath)
	}
	if got := readBuildOutputFile(t, targetOutputPath, "alpha/index.html"); !bytes.Contains(got, []byte("Body.")) {
		t.Fatalf("target alpha page missing generated content\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(targetOutputPath, managedOutputMarkerFilename)); err != nil {
		t.Fatalf("os.Stat(output marker) error = %v", err)
	}
	if got, readErr := os.ReadFile(filepath.Join(vaultPath, "notes", "alpha.md")); readErr != nil {
		t.Fatalf("os.ReadFile(notes/alpha.md) error = %v, want source note preserved", readErr)
	} else if !bytes.Contains(got, []byte("title: Alpha")) {
		t.Fatalf("notes/alpha.md = %q, want preserved source note content", got)
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

func TestBuildEmitsArticleJSONLDForSparseNote(t *testing.T) {
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
		t.Fatalf("buildWithOptions() error = %v", err)
	}
	if result == nil {
		t.Fatal("buildWithOptions() = nil result, want build result")
	}
	if result.NotePages != 1 {
		t.Fatalf("result.NotePages = %d, want %d", result.NotePages, 1)
	}
	if len(result.Diagnostics) != 0 {
		t.Fatalf("len(result.Diagnostics) = %d, want 0 warnings for sparse note with deterministic fallbacks", len(result.Diagnostics))
	}
	if got := strings.TrimSpace(diagnostics.String()); got != "" {
		t.Fatalf("diagnostics summary = %q, want empty summary", got)
	}

	sparseHTML := readBuildOutputFile(t, outputPath, "sparse/index.html")
	if !containsAny(sparseHTML, `<h1 class="page-title">Sparse</h1>`, `<h1 class=page-title>Sparse</h1>`) {
		t.Fatalf("sparse page missing rendered title\n%s", sparseHTML)
	}
	if !containsAny(sparseHTML, `<script type="application/ld+json">`, `<script type=application/ld+json>`) {
		t.Fatalf("sparse page missing JSON-LD script\n%s", sparseHTML)
	}
	if !bytes.Contains(sparseHTML, []byte(`"@type":"BreadcrumbList"`)) {
		t.Fatalf("sparse page JSON-LD missing breadcrumb data\n%s", sparseHTML)
	}
	if !bytes.Contains(sparseHTML, []byte(`"@type":"Article"`)) {
		t.Fatalf("sparse page JSON-LD missing Article schema\n%s", sparseHTML)
	}
}

func TestBuildUsesLoadedConfigDefaultsForMinimalConfig(t *testing.T) {
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

	cfg := internalconfig.Defaults()
	cfg.Title = "Field Notes"
	cfg.BaseURL = "https://example.com/blog"
	loadedCfg, err := internalconfig.NormalizeSiteConfig(cfg)
	if err != nil {
		t.Fatalf("config.NormalizeSiteConfig() error = %v", err)
	}
	expectedInput := SiteInput{Config: loadedCfg}

	result, err := BuildWithOptions(expectedInput, vaultPath, outputPath, Options{})
	if err != nil {
		t.Fatalf("BuildWithOptions() error = %v", err)
	}
	if result == nil {
		t.Fatal("BuildWithOptions() = nil result, want build result")
	}
	if result.NotePages != 1 {
		t.Fatalf("result.NotePages = %d, want %d", result.NotePages, 1)
	}

	noteHTML := readBuildOutputFile(t, outputPath, "direct-build/index.html")
	runtimePath, err := internalrender.SharedRuntimeOutputPath()
	if err != nil {
		t.Fatalf("render.SharedRuntimeOutputPath() error = %v", err)
	}
	const (
		katexCSSOutputPath  = "assets/obsite-runtime/katex.min.css"
		katexJSOutputPath   = "assets/obsite-runtime/katex.min.js"
		katexAutoOutputPath = "assets/obsite-runtime/auto-render.min.js"
		mermaidJSOutputPath = "assets/obsite-runtime/mermaid.min.js"
	)
	for _, want := range []string{
		"../" + runtimePath,
		"../" + katexCSSOutputPath,
		"data-obsite-math",
		"data-obsite-mermaid",
	} {
		if !bytes.Contains(noteHTML, []byte(want)) {
			t.Fatalf("direct build page missing shared runtime contract %q\n%s", want, noteHTML)
		}
	}
	for _, forbidden := range [][]byte{[]byte(katexJSOutputPath), []byte(katexAutoOutputPath), []byte(mermaidJSOutputPath), []byte("renderMathInElement"), []byte("window.mermaid.initialize"), []byte("cdn.jsdelivr.net")} {
		if bytes.Contains(noteHTML, forbidden) {
			t.Fatalf("direct build page contains duplicated runtime loader %q\n%s", forbidden, noteHTML)
		}
	}
	for _, relPath := range []string{katexCSSOutputPath, katexJSOutputPath, katexAutoOutputPath, mermaidJSOutputPath, runtimePath} {
		if _, err := os.Stat(filepath.Join(outputPath, filepath.FromSlash(relPath))); err != nil {
			t.Fatalf("os.Stat(%q) error = %v", relPath, err)
		}
	}
	runtimeJS := readBuildOutputFile(t, outputPath, runtimePath)
	for _, want := range [][]byte{[]byte("renderMathInElement"), []byte("window.mermaid.initialize"), []byte("Mermaid could not render diagram"), []byte("KaTeX could not render formula")} {
		if !bytes.Contains(runtimeJS, want) {
			t.Fatalf("shared runtime missing %q", want)
		}
	}
}

func TestBuildRetainsPassTwoDiagnosticsWhenOneRenderFails(t *testing.T) {
	lockBuildTestRenderHooks(t)

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

func TestBuildKeepsHeadingEmbedHostCachedWhenNonTargetSectionChanges(t *testing.T) {
	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "notes/host.md", `---
title: Host
---
# Host

![[Beta#Target]]
`)
	writeBuildTestFile(t, vaultPath, "notes/beta.md", `---
title: Beta
---
# Beta

## Target

Selected section body.

## Other

Original unrelated section body.
`)

	if _, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard}); err != nil {
		t.Fatalf("first buildWithOptions() error = %v", err)
	}

	firstHostHTML := readBuildOutputFile(t, outputPath, "host/index.html")
	if !bytes.Contains(firstHostHTML, []byte("Selected section body.")) {
		t.Fatalf("host page missing targeted embedded section\n%s", firstHostHTML)
	}
	if bytes.Contains(firstHostHTML, []byte("Original unrelated section body.")) {
		t.Fatalf("host page unexpectedly rendered unrelated embedded section\n%s", firstHostHTML)
	}

	getRenderedPaths := captureRenderedNotePagePaths(t)
	writeBuildTestFile(t, vaultPath, "notes/beta.md", `---
title: Beta
---
# Beta

## Target

Selected section body.

## Other

Updated unrelated section body.
`)

	result, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard})
	if err != nil {
		t.Fatalf("second buildWithOptions() error = %v", err)
	}
	if result.NotePages != 1 {
		t.Fatalf("result.NotePages = %d, want %d", result.NotePages, 1)
	}
	if got := getRenderedPaths(); !reflect.DeepEqual(got, []string{"notes/beta.md"}) {
		t.Fatalf("rendered note pages = %#v, want %#v", got, []string{"notes/beta.md"})
	}

	secondHostHTML := readBuildOutputFile(t, outputPath, "host/index.html")
	if !bytes.Equal(secondHostHTML, firstHostHTML) {
		t.Fatalf("host page changed after unrelated embedded section edit\nbefore:\n%s\nafter:\n%s", firstHostHTML, secondHostHTML)
	}
	if bytes.Contains(secondHostHTML, []byte("Updated unrelated section body.")) {
		t.Fatalf("host page unexpectedly picked up unrelated embedded section update\n%s", secondHostHTML)
	}
}

func TestBuildRefreshesCachedHeadingEmbedDiagnosticsWhenSectionLineNumbersShift(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "notes/host.md", `# Host

![[Beta#Target]]
`)
	writeBuildTestFile(t, vaultPath, "notes/beta.md", `# Beta

## Target

![[Missing]]
`)

	if _, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard}); err != nil {
		t.Fatalf("first buildWithOptions() error = %v", err)
	}

	firstHostHTML := readBuildOutputFile(t, outputPath, "host/index.html")

	writeBuildTestFile(t, vaultPath, "notes/beta.md", `# Beta


## Target

![[Missing]]
`)

	var diagnostics bytes.Buffer
	result, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: &diagnostics})
	if err != nil {
		t.Fatalf("second buildWithOptions() error = %v", err)
	}

	secondHostHTML := readBuildOutputFile(t, outputPath, "host/index.html")
	if !bytes.Equal(secondHostHTML, firstHostHTML) {
		t.Fatalf("host page changed after only rebasing embedded section line numbers\nbefore:\n%s\nafter:\n%s", firstHostHTML, secondHostHTML)
	}

	matchedNewLine := 0
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Kind != diag.KindDeadLink {
			continue
		}
		if diagnostic.Location.Path != "notes/beta.md" {
			continue
		}
		if !strings.Contains(diagnostic.Message, `note embed "Missing" could not be resolved`) {
			continue
		}
		if diagnostic.Location.Line == 5 {
			t.Fatalf("result.Diagnostics contains stale embedded warning line %#v", diagnostic)
		}
		if diagnostic.Location.Line == 6 {
			matchedNewLine++
		}
	}
	if matchedNewLine == 0 {
		t.Fatalf("result.Diagnostics = %#v, want rebased embedded warning at notes/beta.md:6", result.Diagnostics)
	}

	summary := diagnostics.String()
	if strings.Contains(summary, "notes/beta.md:5 [deadlink]") {
		t.Fatalf("diagnostics summary = %q, want rebased warning line", summary)
	}
	if !strings.Contains(summary, "notes/beta.md:6 [deadlink]") {
		t.Fatalf("diagnostics summary = %q, want rebased warning line", summary)
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

func TestBuildDefaultTemplateSignalInvalidatesCacheWithoutThemeRoot(t *testing.T) {
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

func TestBuildBuildABISignatureInvalidatesCache(t *testing.T) {
	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "notes/guide.md", `---
title: Guide
---
# Guide

Body.
`)

	originalReadBuildABISignature := readBuildABISignature
	readBuildABISignature = func() (string, error) {
		return "abi-v1", nil
	}
	defer func() {
		readBuildABISignature = originalReadBuildABISignature
	}()

	if _, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard}); err != nil {
		t.Fatalf("first buildWithOptions() error = %v", err)
	}

	manifestJSON := readBuildOutputFile(t, outputPath, cacheManifestRelPath)
	var manifest CacheManifest
	if err := json.Unmarshal(manifestJSON, &manifest); err != nil {
		t.Fatalf("json.Unmarshal(manifest) error = %v\n%s", err, manifestJSON)
	}
	if manifest.BuildABISignature != "abi-v1" {
		t.Fatalf("manifest.BuildABISignature = %q, want %q", manifest.BuildABISignature, "abi-v1")
	}

	result, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard})
	if err != nil {
		t.Fatalf("second buildWithOptions() error = %v", err)
	}
	if result.NotePages != 0 {
		t.Fatalf("result.NotePages = %d, want %d before build ABI changes", result.NotePages, 0)
	}

	getRenderedPaths := captureRenderedNotePagePaths(t)
	readBuildABISignature = func() (string, error) {
		return "abi-v2", nil
	}

	result, err = buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard})
	if err != nil {
		t.Fatalf("third buildWithOptions() error = %v", err)
	}
	if result.NotePages != 1 {
		t.Fatalf("result.NotePages = %d, want %d after build ABI changes", result.NotePages, 1)
	}
	if got := getRenderedPaths(); !reflect.DeepEqual(got, []string{"notes/guide.md"}) {
		t.Fatalf("rendered note pages = %#v, want %#v", got, []string{"notes/guide.md"})
	}

	manifestJSON = readBuildOutputFile(t, outputPath, cacheManifestRelPath)
	if err := json.Unmarshal(manifestJSON, &manifest); err != nil {
		t.Fatalf("json.Unmarshal(updated manifest) error = %v\n%s", err, manifestJSON)
	}
	if manifest.BuildABISignature != "abi-v2" {
		t.Fatalf("manifest.BuildABISignature = %q, want %q", manifest.BuildABISignature, "abi-v2")
	}
}

func TestBuildDisablesCacheReuseWhenBuildABISourceSignatureFails(t *testing.T) {
	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "notes/guide.md", `---
title: Guide
---
# Guide

Body.
`)

	originalReadBuildABISignature := readBuildABISignature
	originalReadBuildABISourceSignature := readBuildABISourceSignature
	readBuildABISourceSignature = func() (string, bool, error) {
		return "", false, errors.New("walk build ABI dir \"internal\": permission denied")
	}
	readBuildABISignature = computeBuildABISignature
	defer func() {
		readBuildABISourceSignature = originalReadBuildABISourceSignature
		readBuildABISignature = originalReadBuildABISignature
	}()

	var firstDiagnostics bytes.Buffer
	firstResult, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: &firstDiagnostics})
	if err != nil {
		t.Fatalf("first buildWithOptions() error = %v", err)
	}
	if firstResult.NotePages != 1 {
		t.Fatalf("firstResult.NotePages = %d, want %d when ABI source signature disables cache reuse", firstResult.NotePages, 1)
	}
	if len(firstResult.Diagnostics) != 1 {
		t.Fatalf("len(firstResult.Diagnostics) = %d, want 1 ABI cache warning", len(firstResult.Diagnostics))
	}
	if firstResult.Diagnostics[0].Kind != diag.KindStructuredData {
		t.Fatalf("firstResult.Diagnostics[0].Kind = %q, want %q", firstResult.Diagnostics[0].Kind, diag.KindStructuredData)
	}
	if !strings.Contains(firstResult.Diagnostics[0].Message, "build ABI source signature could not be collected") {
		t.Fatalf("firstResult.Diagnostics[0].Message = %q, want ABI warning", firstResult.Diagnostics[0].Message)
	}
	if !strings.Contains(firstResult.Diagnostics[0].Message, "disabling incremental cache reuse") {
		t.Fatalf("firstResult.Diagnostics[0].Message = %q, want cache disable guidance", firstResult.Diagnostics[0].Message)
	}
	if !strings.Contains(firstResult.Diagnostics[0].Message, "forcing a full rebuild") {
		t.Fatalf("firstResult.Diagnostics[0].Message = %q, want full rebuild guidance", firstResult.Diagnostics[0].Message)
	}
	if _, err := os.Stat(filepath.Join(outputPath, cacheManifestRelPath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cache manifest stat error = %v, want %v", err, os.ErrNotExist)
	}
	if summary := firstDiagnostics.String(); !strings.Contains(summary, "disabling incremental cache reuse") {
		t.Fatalf("diagnostics summary = %q, want ABI cache warning", summary)
	}

	var secondDiagnostics bytes.Buffer
	secondResult, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: &secondDiagnostics})
	if err != nil {
		t.Fatalf("second buildWithOptions() error = %v", err)
	}
	if secondResult.NotePages != 1 {
		t.Fatalf("secondResult.NotePages = %d, want %d when ABI source signature keeps cache disabled", secondResult.NotePages, 1)
	}
	if len(secondResult.Diagnostics) != 1 {
		t.Fatalf("len(secondResult.Diagnostics) = %d, want 1 ABI cache warning on repeated build", len(secondResult.Diagnostics))
	}
	if _, err := os.Stat(filepath.Join(outputPath, cacheManifestRelPath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cache manifest stat error after repeated build = %v, want %v", err, os.ErrNotExist)
	}
	if summary := secondDiagnostics.String(); !strings.Contains(summary, "forcing a full rebuild") {
		t.Fatalf("second diagnostics summary = %q, want full rebuild guidance", summary)
	}
}

func TestBuildTreatsSameVersionPartialManifestAsCompleteMiss(t *testing.T) {
	for _, missingField := range []string{"pageInputSignature", "managedAssetSignatures", "notePageSignature", "notes.guide.htmlContent"} {
		t.Run(missingField, func(t *testing.T) {
			vaultPath := t.TempDir()
			outputPath := filepath.Join(t.TempDir(), "site")
			writeBuildTestFile(t, vaultPath, "notes/guide.md", "# Guide\n\nBody.\n")
			if _, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{}); err != nil {
				t.Fatal(err)
			}
			var raw map[string]any
			if err := json.Unmarshal(readBuildOutputFile(t, outputPath, cacheManifestRelPath), &raw); err != nil {
				t.Fatal(err)
			}
			if missingField == "notes.guide.htmlContent" {
				notes := raw["notes"].(map[string]any)
				guide := notes["notes/guide.md"].(map[string]any)
				delete(guide, "htmlContent")
			} else {
				delete(raw, missingField)
			}
			data, err := json.Marshal(raw)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(outputPath, cacheManifestRelPath), data, 0o644); err != nil {
				t.Fatal(err)
			}

			getRenderedMarkdown := captureRenderedMarkdownNotePaths(t)
			result, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if result.NotePages != 1 || !reflect.DeepEqual(getRenderedMarkdown(), []string{"notes/guide.md"}) {
				t.Fatalf("partial manifest reused cache: NotePages=%d markdown=%#v", result.NotePages, getRenderedMarkdown())
			}
		})
	}
}

func TestBuildWarnsAndFallsBackToFullRebuildWhenManifestIsCorrupt(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(outputPath, cacheManifestRelPath), []byte(`{"notes":`), 0o644); err != nil {
		t.Fatalf("os.WriteFile(manifest) error = %v", err)
	}

	var diagnostics bytes.Buffer
	result, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: &diagnostics})
	if err != nil {
		t.Fatalf("second buildWithOptions() error = %v", err)
	}
	if result.NotePages != 1 {
		t.Fatalf("result.NotePages = %d, want %d after manifest corruption forces a full rebuild", result.NotePages, 1)
	}
	if len(result.Diagnostics) != 1 {
		t.Fatalf("len(result.Diagnostics) = %d, want 1 manifest warning", len(result.Diagnostics))
	}
	if result.Diagnostics[0].Severity != diag.SeverityWarning {
		t.Fatalf("result.Diagnostics[0].Severity = %q, want %q", result.Diagnostics[0].Severity, diag.SeverityWarning)
	}
	if result.Diagnostics[0].Kind != diag.KindStructuredData {
		t.Fatalf("result.Diagnostics[0].Kind = %q, want %q", result.Diagnostics[0].Kind, diag.KindStructuredData)
	}
	if result.Diagnostics[0].Location.Path != cacheManifestRelPath {
		t.Fatalf("result.Diagnostics[0].Location.Path = %q, want %q", result.Diagnostics[0].Location.Path, cacheManifestRelPath)
	}
	if !strings.Contains(result.Diagnostics[0].Message, "incremental cache manifest could not be loaded") {
		t.Fatalf("result.Diagnostics[0].Message = %q, want manifest warning", result.Diagnostics[0].Message)
	}
	if !strings.Contains(result.Diagnostics[0].Message, "falling back to a full rebuild") {
		t.Fatalf("result.Diagnostics[0].Message = %q, want full-rebuild guidance", result.Diagnostics[0].Message)
	}
	if !strings.Contains(result.Diagnostics[0].Message, cacheManifestRelPath) {
		t.Fatalf("result.Diagnostics[0].Message = %q, want manifest cleanup hint", result.Diagnostics[0].Message)
	}

	summary := diagnostics.String()
	if !strings.Contains(summary, "Warnings (1)") {
		t.Fatalf("diagnostics summary missing warning count\n%s", summary)
	}
	if !strings.Contains(summary, cacheManifestRelPath) {
		t.Fatalf("diagnostics summary missing manifest path\n%s", summary)
	}
	if !strings.Contains(summary, "falling back to a full rebuild") {
		t.Fatalf("diagnostics summary missing fallback guidance\n%s", summary)
	}

	manifestJSON := readBuildOutputFile(t, outputPath, cacheManifestRelPath)
	var manifest CacheManifest
	if err := json.Unmarshal(manifestJSON, &manifest); err != nil {
		t.Fatalf("json.Unmarshal(rewritten manifest) error = %v\n%s", err, manifestJSON)
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
	got := getRenderedPaths()
	want := []string{"notes/alpha.md", "notes/beta.md", "notes/gamma.md"}
	if result.NotePages != len(want) {
		t.Fatalf("result.NotePages = %d, want %d", result.NotePages, len(want))
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rendered note pages = %#v, want %#v", got, want)
	}
}

func TestBuildDoesNotRerenderLinkedTargetWhenSourceBodyChangesOnly(t *testing.T) {
	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "notes/alpha.md", `---
title: Alpha
date: 2026-04-06
---
# Alpha

[[Beta]]

Original body.
`)
	writeBuildTestFile(t, vaultPath, "notes/beta.md", `---
title: Beta
date: 2026-04-06
---
# Beta

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

[[Beta]]

Updated body.
`)

	getRenderedPaths := captureRenderedNotePagePaths(t)
	result, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard})
	if err != nil {
		t.Fatalf("second buildWithOptions() error = %v", err)
	}
	if result.NotePages != 1 {
		t.Fatalf("result.NotePages = %d, want %d", result.NotePages, 1)
	}

	if got := getRenderedPaths(); !reflect.DeepEqual(got, []string{"notes/alpha.md"}) {
		t.Fatalf("rendered note pages = %#v, want %#v", got, []string{"notes/alpha.md"})
	}

	betaHTML := readBuildOutputFile(t, outputPath, "beta/index.html")
	if !containsAny(betaHTML, `href="../alpha/">Alpha</a>`, `href=../alpha/>Alpha</a>`) {
		t.Fatalf("beta page missing backlink for alpha after source body-only change\n%s", betaHTML)
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

func TestBuildPublishesNestedSlashPathImageEmbedAndReusesCache(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	noteRelPath := "notes/deep/gallery.md"

	writeBuildTestFile(t, vaultPath, noteRelPath, `---
title: Gallery
date: 2026-04-06
---
# Gallery

![[assets/diagram.png|600]]
`)
	writeBuildTestFile(t, vaultPath, "assets/diagram.png", "diagram-image")

	first, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard})
	if err != nil {
		t.Fatalf("first buildWithOptions() error = %v", err)
	}
	if first.NotePages != 1 {
		t.Fatalf("first result.NotePages = %d, want %d", first.NotePages, 1)
	}
	if len(first.Diagnostics) != 0 {
		t.Fatalf("len(first.Diagnostics) = %d, want 0; diagnostics = %#v", len(first.Diagnostics), first.Diagnostics)
	}

	note := first.Index.Notes[noteRelPath]
	if note == nil {
		t.Fatalf("first.Index.Notes[%s] = nil, want note", noteRelPath)
	}
	if note.Slug == "" {
		t.Fatalf("first.Index.Notes[%s].Slug = %q, want populated slug", noteRelPath, note.Slug)
	}

	asset := first.Assets["assets/diagram.png"]
	if asset == nil {
		t.Fatal("first.Assets[assets/diagram.png] = nil, want published asset")
	}
	if asset.DstPath != "assets/diagram.png" {
		t.Fatalf("first.Assets[assets/diagram.png].DstPath = %q, want %q", asset.DstPath, "assets/diagram.png")
	}
	if got := string(readBuildOutputFile(t, outputPath, asset.DstPath)); got != "diagram-image" {
		t.Fatalf("%s = %q, want %q", asset.DstPath, got, "diagram-image")
	}

	pageRelPath := filepath.ToSlash(filepath.Join(note.Slug, "index.html"))
	expectedSrc, err := filepath.Rel(filepath.Dir(pageRelPath), asset.DstPath)
	if err != nil {
		t.Fatalf("filepath.Rel(%q, %q) error = %v", filepath.Dir(pageRelPath), asset.DstPath, err)
	}
	expectedSrc = filepath.ToSlash(expectedSrc)

	pageHTML := readBuildOutputFile(t, outputPath, pageRelPath)
	if !containsAny(pageHTML, `src="`+expectedSrc+`"`, `src=`+expectedSrc) {
		t.Fatalf("gallery page missing rewritten slash-path image embed\n%s", pageHTML)
	}
	if !containsAny(pageHTML, `width="600"`, `width=600`) {
		t.Fatalf("gallery page missing image embed width\n%s", pageHTML)
	}

	firstManifest := readBuildCacheManifest(t, outputPath)
	firstEntry, ok := firstManifest.Notes[noteRelPath]
	if !ok {
		t.Fatalf("first manifest missing %q entry", noteRelPath)
	}
	if len(firstEntry.RenderDiagnostics) != 0 {
		t.Fatalf("len(first manifest diagnostics) = %d, want 0; diagnostics = %#v", len(firstEntry.RenderDiagnostics), firstEntry.RenderDiagnostics)
	}
	if len(firstEntry.Assets) != 1 || firstEntry.Assets[0].SrcPath != "assets/diagram.png" || firstEntry.Assets[0].DstPath != "assets/diagram.png" {
		t.Fatalf("first manifest assets = %#v, want published slash-path embed asset", firstEntry.Assets)
	}

	getRenderedPaths := captureRenderedNotePagePaths(t)
	second, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard})
	if err != nil {
		t.Fatalf("second buildWithOptions() error = %v", err)
	}
	if second.NotePages != 0 {
		t.Fatalf("second result.NotePages = %d, want %d for cached rebuild", second.NotePages, 0)
	}
	if len(second.Diagnostics) != 0 {
		t.Fatalf("len(second.Diagnostics) = %d, want 0; diagnostics = %#v", len(second.Diagnostics), second.Diagnostics)
	}
	if got := getRenderedPaths(); len(got) != 0 {
		t.Fatalf("rendered note pages on cached rebuild = %#v, want none", got)
	}

	secondAsset := second.Assets["assets/diagram.png"]
	if secondAsset == nil {
		t.Fatal("second.Assets[assets/diagram.png] = nil, want cached asset")
	}
	if secondAsset.DstPath != asset.DstPath {
		t.Fatalf("second.Assets[assets/diagram.png].DstPath = %q, want cached destination %q", secondAsset.DstPath, asset.DstPath)
	}

	secondManifest := readBuildCacheManifest(t, outputPath)
	secondEntry, ok := secondManifest.Notes[noteRelPath]
	if !ok {
		t.Fatalf("second manifest missing %q entry", noteRelPath)
	}
	if secondEntry.RenderSignature != firstEntry.RenderSignature {
		t.Fatalf("second render signature = %q, want cached signature %q", secondEntry.RenderSignature, firstEntry.RenderSignature)
	}
	if len(secondEntry.RenderDiagnostics) != 0 {
		t.Fatalf("len(second manifest diagnostics) = %d, want 0; diagnostics = %#v", len(secondEntry.RenderDiagnostics), secondEntry.RenderDiagnostics)
	}
	if !reflect.DeepEqual(secondEntry.Assets, firstEntry.Assets) {
		t.Fatalf("second manifest assets = %#v, want cached assets %#v", secondEntry.Assets, firstEntry.Assets)
	}

	pageHTML = readBuildOutputFile(t, outputPath, pageRelPath)
	if !containsAny(pageHTML, `src="`+expectedSrc+`"`, `src=`+expectedSrc) {
		t.Fatalf("gallery page lost rewritten slash-path image embed after cached rebuild\n%s", pageHTML)
	}
	if !containsAny(pageHTML, `width="600"`, `width=600`) {
		t.Fatalf("gallery page lost image embed width after cached rebuild\n%s", pageHTML)
	}
}

func TestBuildRefreshesImageEmbedDiagnosticsWhenAssetInventoryChangesWithoutNoteEditsAtBuildLevel(t *testing.T) {
	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	noteRelPath := "notes/deep/gallery.md"
	resolvedAssetPath := "assets/diagram.png"

	writeBuildTestFile(t, vaultPath, noteRelPath, `---
title: Gallery
date: 2026-04-06
---
# Gallery

![[assets/diagram.png|600]]
`)

	first, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard})
	if err != nil {
		t.Fatalf("first buildWithOptions() error = %v", err)
	}
	if first.NotePages != 1 {
		t.Fatalf("first result.NotePages = %d, want %d", first.NotePages, 1)
	}
	assertSingleUnresolvedAssetDiagnosticContains(t, first.Diagnostics, "could not be resolved to a vault asset")

	firstManifest := readBuildCacheManifest(t, outputPath)
	firstEntry, ok := firstManifest.Notes[noteRelPath]
	if !ok {
		t.Fatalf("first manifest missing %q entry", noteRelPath)
	}
	assertCachedUnresolvedAssetDiagnosticContains(t, firstEntry, "could not be resolved to a vault asset")
	if len(firstEntry.Assets) != 0 {
		t.Fatalf("first manifest assets = %#v, want no published assets while embed is unresolved", firstEntry.Assets)
	}

	writeBuildTestFile(t, vaultPath, resolvedAssetPath, "resolved-image")

	getRenderedPaths := captureRenderedMarkdownNotePaths(t)
	second, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard})
	if err != nil {
		t.Fatalf("second buildWithOptions() error = %v", err)
	}
	if got := getRenderedPaths(); !reflect.DeepEqual(got, []string{noteRelPath}) {
		t.Fatalf("second rendered markdown notes = %#v, want %#v", got, []string{noteRelPath})
	}
	if len(second.Diagnostics) != 0 {
		t.Fatalf("len(second.Diagnostics) = %d, want 0; diagnostics = %#v", len(second.Diagnostics), second.Diagnostics)
	}

	secondManifest := readBuildCacheManifest(t, outputPath)
	secondEntry, ok := secondManifest.Notes[noteRelPath]
	if !ok {
		t.Fatalf("second manifest missing %q entry", noteRelPath)
	}
	if secondEntry.RenderSignature == firstEntry.RenderSignature {
		t.Fatal("second render signature did not change when image embed inventory became resolved")
	}
	if len(secondEntry.RenderDiagnostics) != 0 {
		t.Fatalf("len(second manifest diagnostics) = %d, want 0; diagnostics = %#v", len(secondEntry.RenderDiagnostics), secondEntry.RenderDiagnostics)
	}

	asset := second.Assets[resolvedAssetPath]
	if asset == nil {
		t.Fatalf("second.Assets[%s] = nil, want resolved asset", resolvedAssetPath)
	}
	if asset.DstPath == "" {
		t.Fatalf("second.Assets[%s].DstPath = %q, want published destination", resolvedAssetPath, asset.DstPath)
	}
	if got := string(readBuildOutputFile(t, outputPath, asset.DstPath)); got != "resolved-image" {
		t.Fatalf("%s = %q, want %q", asset.DstPath, got, "resolved-image")
	}
	if len(secondEntry.Assets) != 1 || secondEntry.Assets[0].SrcPath != resolvedAssetPath || secondEntry.Assets[0].DstPath != asset.DstPath {
		t.Fatalf("second manifest assets = %#v, want resolved asset entry", secondEntry.Assets)
	}

	note := second.Index.Notes[noteRelPath]
	if note == nil {
		t.Fatalf("second.Index.Notes[%s] = nil, want note", noteRelPath)
	}
	pageRelPath := filepath.ToSlash(filepath.Join(note.Slug, "index.html"))
	expectedSrc, err := filepath.Rel(filepath.Dir(pageRelPath), asset.DstPath)
	if err != nil {
		t.Fatalf("filepath.Rel(%q, %q) error = %v", filepath.Dir(pageRelPath), asset.DstPath, err)
	}
	expectedSrc = filepath.ToSlash(expectedSrc)

	pageHTML := readBuildOutputFile(t, outputPath, pageRelPath)
	if !containsAny(pageHTML, `src="`+expectedSrc+`"`, `src=`+expectedSrc) {
		t.Fatalf("gallery page missing resolved image embed after inventory-only rebuild\n%s", pageHTML)
	}
	if !containsAny(pageHTML, `width="600"`, `width=600`) {
		t.Fatalf("gallery page missing image embed width after inventory-only rebuild\n%s", pageHTML)
	}
}

func TestBuildKeepsPlainReferencedAssetWhenVisibleUnreferencedCollisionExists(t *testing.T) {
	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, ".obsidian/app.json", `{"attachmentFolderPath":"attachments"}`)
	writeBuildTestFile(t, vaultPath, "attachments/hero.png", "attachment-image")
	writeBuildTestFile(t, vaultPath, "images/hero.png", "visible-but-unreferenced")
	writeBuildTestFile(t, vaultPath, "notes/host.md", `---
title: Host
date: 2026-04-06
---
# Host

![Hero](hero.png)
`)

	result, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard})
	if err != nil {
		t.Fatalf("buildWithOptions() error = %v", err)
	}
	if result.NotePages != 1 {
		t.Fatalf("result.NotePages = %d, want %d", result.NotePages, 1)
	}
	if stale := result.Assets["images/hero.png"]; stale != nil {
		t.Fatalf("result.Assets[images/hero.png] = %#v, want nil for unreferenced collision peer", stale)
	}

	hostAsset := result.Assets["attachments/hero.png"]
	if hostAsset == nil {
		t.Fatal("result.Assets[attachments/hero.png] = nil, want referenced asset")
	}
	if hostAsset.DstPath != "assets/hero.png" {
		t.Fatalf("result.Assets[attachments/hero.png].DstPath = %q, want %q when the only peer is unreferenced", hostAsset.DstPath, "assets/hero.png")
	}
	if got := string(readBuildOutputFile(t, outputPath, hostAsset.DstPath)); got != "attachment-image" {
		t.Fatalf("%s = %q, want %q", hostAsset.DstPath, got, "attachment-image")
	}
	if _, statErr := os.Stat(filepath.Join(outputPath, "assets", "hero.png")); statErr != nil {
		t.Fatalf("os.Stat(assets/hero.png) error = %v, want plain output present", statErr)
	}

	hostHTML := readBuildOutputFile(t, outputPath, "host/index.html")
	if !containsAny(hostHTML, `src="../assets/hero.png"`, `src=../assets/hero.png`) {
		t.Fatalf("host page missing stable plain asset path when only an unreferenced peer exists\n%s", hostHTML)
	}
}

func TestBuildKeepsCachedMarkdownImageNoteStableWhenUnpublishedCollisionAppears(t *testing.T) {
	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, ".obsidian/app.json", `{"attachmentFolderPath":"attachments"}`)
	writeBuildTestFile(t, vaultPath, "attachments/hero.png", "attachment-image")
	writeBuildTestFile(t, vaultPath, "notes/host.md", `---
title: Host
date: 2026-04-06
---
# Host

![Hero](hero.png)
`)

	first, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard})
	if err != nil {
		t.Fatalf("first buildWithOptions() error = %v", err)
	}
	initialHostAsset := first.Assets["attachments/hero.png"]
	if initialHostAsset == nil {
		t.Fatal("first.Assets[attachments/hero.png] = nil, want referenced asset")
	}
	if initialHostAsset.DstPath != "assets/hero.png" {
		t.Fatalf("first.Assets[attachments/hero.png].DstPath = %q, want %q before collision appears", initialHostAsset.DstPath, "assets/hero.png")
	}

	writeBuildTestFile(t, vaultPath, "images/hero.png", "draft-image")
	writeBuildTestFile(t, vaultPath, "notes/draft.md", `---
title: Draft
date: 2026-04-06
publish: false
---
# Draft

![Hero](../images/hero.png)
`)

	getRenderedMarkdownPaths := captureRenderedMarkdownNotePaths(t)
	result, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard})
	if err != nil {
		t.Fatalf("second buildWithOptions() error = %v", err)
	}
	if result.NotePages != 0 {
		t.Fatalf("result.NotePages = %d, want %d when only unpublished inputs change", result.NotePages, 0)
	}
	if got := getRenderedMarkdownPaths(); len(got) != 0 {
		t.Fatalf("rendered markdown notes = %#v, want no public rerenders when only unpublished inputs change", got)
	}
	if stale := result.Assets["images/hero.png"]; stale != nil {
		t.Fatalf("result.Assets[images/hero.png] = %#v, want nil for unpublished collision peer", stale)
	}

	hostAsset := result.Assets["attachments/hero.png"]
	if hostAsset == nil {
		t.Fatal("result.Assets[attachments/hero.png] = nil, want referenced asset")
	}
	if hostAsset.DstPath != initialHostAsset.DstPath {
		t.Fatalf("result.Assets[attachments/hero.png].DstPath = %q, want cached public asset URL to stay %q when only unpublished inputs change", hostAsset.DstPath, initialHostAsset.DstPath)
	}
	if got := string(readBuildOutputFile(t, outputPath, hostAsset.DstPath)); got != "attachment-image" {
		t.Fatalf("%s = %q, want %q", hostAsset.DstPath, got, "attachment-image")
	}
	if _, statErr := os.Stat(filepath.Join(outputPath, "assets", "hero.png")); statErr != nil {
		t.Fatalf("os.Stat(assets/hero.png) error = %v, want plain output to remain", statErr)
	}

	hostHTML := readBuildOutputFile(t, outputPath, "host/index.html")
	if !containsAny(hostHTML, `src="../assets/hero.png"`, `src=../assets/hero.png`) {
		t.Fatalf("host page missing stable plain asset path after unpublished collision appears\n%s", hostHTML)
	}
}

func TestBuildRerendersCleanMarkdownImageNotesWhenAssetPlanChanges(t *testing.T) {
	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, ".obsidian/app.json", `{"attachmentFolderPath":"attachments"}`)
	writeBuildTestFile(t, vaultPath, "attachments/hero.png", "attachment-image")
	writeBuildTestFile(t, vaultPath, "notes/host.md", `---
title: Host
date: 2026-04-06
---
# Host

![Hero](hero.png)
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

	writeBuildTestFile(t, vaultPath, "images/hero.png", "second-image")
	writeBuildTestFile(t, vaultPath, "notes/collision.md", `---
title: Collision
date: 2026-04-06
---
# Collision

![Hero](../images/hero.png)
`)

	getRenderedPaths := captureRenderedNotePagePaths(t)
	result, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard})
	if err != nil {
		t.Fatalf("second buildWithOptions() error = %v", err)
	}
	if result.NotePages != 2 {
		t.Fatalf("result.NotePages = %d, want %d", result.NotePages, 2)
	}
	if got := getRenderedPaths(); !reflect.DeepEqual(got, []string{"notes/collision.md", "notes/host.md"}) {
		t.Fatalf("rendered note pages = %#v, want %#v", got, []string{"notes/collision.md", "notes/host.md"})
	}

	hostAsset := result.Assets["attachments/hero.png"]
	if hostAsset == nil {
		t.Fatal("result.Assets[attachments/hero.png] = nil, want asset")
	}
	if hostAsset.DstPath == "assets/hero.png" {
		t.Fatalf("result.Assets[attachments/hero.png].DstPath = %q, want hashed collision path", hostAsset.DstPath)
	}

	collisionAsset := result.Assets["images/hero.png"]
	if collisionAsset == nil {
		t.Fatal("result.Assets[images/hero.png] = nil, want asset")
	}
	if collisionAsset.DstPath == "assets/hero.png" {
		t.Fatalf("result.Assets[images/hero.png].DstPath = %q, want hashed collision path", collisionAsset.DstPath)
	}
	if collisionAsset.DstPath == hostAsset.DstPath {
		t.Fatalf("collision asset DstPath = %q, want distinct hashed destination from %q", collisionAsset.DstPath, hostAsset.DstPath)
	}

	hostHTML := readBuildOutputFile(t, outputPath, "host/index.html")
	if !containsAny(hostHTML, `src="../`+hostAsset.DstPath+`"`, `src=../`+hostAsset.DstPath) {
		t.Fatalf("host page missing rerendered hashed asset link\n%s", hostHTML)
	}
	if got := string(readBuildOutputFile(t, outputPath, hostAsset.DstPath)); got != "attachment-image" {
		t.Fatalf("%s = %q, want %q", hostAsset.DstPath, got, "attachment-image")
	}
}

func TestBuildRestoresPlainMarkdownImagePathWhenCollisionDisappearsOnCachedNote(t *testing.T) {
	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, ".obsidian/app.json", `{"attachmentFolderPath":"attachments"}`)
	writeBuildTestFile(t, vaultPath, "attachments/hero.png", "attachment-image")
	writeBuildTestFile(t, vaultPath, "images/hero.png", "collision-image")
	writeBuildTestFile(t, vaultPath, "notes/host.md", `---
title: Host
date: 2026-04-06
---
# Host

![Hero](hero.png)
`)
	writeBuildTestFile(t, vaultPath, "notes/collision.md", `---
title: Collision
date: 2026-04-06
---
# Collision

![Hero](../images/hero.png)
`)

	first, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard})
	if err != nil {
		t.Fatalf("first buildWithOptions() error = %v", err)
	}

	initialHostAsset := first.Assets["attachments/hero.png"]
	if initialHostAsset == nil {
		t.Fatal("first.Assets[attachments/hero.png] = nil, want asset")
	}
	if initialHostAsset.DstPath == "assets/hero.png" {
		t.Fatalf("first.Assets[attachments/hero.png].DstPath = %q, want hashed collision path", initialHostAsset.DstPath)
	}
	initialHostDst := initialHostAsset.DstPath

	initialCollisionAsset := first.Assets["images/hero.png"]
	if initialCollisionAsset == nil {
		t.Fatal("first.Assets[images/hero.png] = nil, want asset")
	}
	if initialCollisionAsset.DstPath == "assets/hero.png" {
		t.Fatalf("first.Assets[images/hero.png].DstPath = %q, want hashed collision path", initialCollisionAsset.DstPath)
	}

	if err := os.Remove(filepath.Join(vaultPath, "images", "hero.png")); err != nil {
		t.Fatalf("os.Remove(images/hero.png) error = %v", err)
	}
	if err := os.Remove(filepath.Join(vaultPath, "notes", "collision.md")); err != nil {
		t.Fatalf("os.Remove(notes/collision.md) error = %v", err)
	}

	getRenderedMarkdownPaths := captureRenderedMarkdownNotePaths(t)
	result, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard})
	if err != nil {
		t.Fatalf("second buildWithOptions() error = %v", err)
	}
	if result.NotePages != 1 {
		t.Fatalf("result.NotePages = %d, want %d", result.NotePages, 1)
	}
	if got := getRenderedMarkdownPaths(); !reflect.DeepEqual(got, []string{"notes/host.md"}) {
		t.Fatalf("rendered markdown notes = %#v, want %#v", got, []string{"notes/host.md"})
	}
	if stale := result.Assets["images/hero.png"]; stale != nil {
		t.Fatalf("result.Assets[images/hero.png] = %#v, want nil after collision source removal", stale)
	}

	hostAsset := result.Assets["attachments/hero.png"]
	if hostAsset == nil {
		t.Fatal("result.Assets[attachments/hero.png] = nil, want asset")
	}
	if hostAsset.DstPath != "assets/hero.png" {
		t.Fatalf("result.Assets[attachments/hero.png].DstPath = %q, want %q", hostAsset.DstPath, "assets/hero.png")
	}
	if got := string(readBuildOutputFile(t, outputPath, hostAsset.DstPath)); got != "attachment-image" {
		t.Fatalf("%s = %q, want %q", hostAsset.DstPath, got, "attachment-image")
	}

	hostHTML := readBuildOutputFile(t, outputPath, "host/index.html")
	if !containsAny(hostHTML, `src="../assets/hero.png"`, `src=../assets/hero.png`) {
		t.Fatalf("host page missing restored plain asset link\n%s", hostHTML)
	}
	if containsAny(hostHTML, `src="../`+initialHostDst+`"`, `src=../`+initialHostDst) {
		t.Fatalf("host page still references stale hashed asset path %q\n%s", initialHostDst, hostHTML)
	}
}

func TestBuildRecomputesCollisionAssetURLWhenAssetContentChangesOnCachedNote(t *testing.T) {
	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, ".obsidian/app.json", `{"attachmentFolderPath":"attachments"}`)
	writeBuildTestFile(t, vaultPath, "attachments/hero.png", "attachment-image")
	writeBuildTestFile(t, vaultPath, "images/hero.png", "collision-image")
	writeBuildTestFile(t, vaultPath, "notes/host.md", `---
title: Host
date: 2026-04-06
---
# Host

![Hero](hero.png)
`)
	writeBuildTestFile(t, vaultPath, "notes/collision.md", `---
title: Collision
date: 2026-04-06
---
# Collision

![Hero](../images/hero.png)
`)

	first, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard})
	if err != nil {
		t.Fatalf("first buildWithOptions() error = %v", err)
	}

	initialCollisionAsset := first.Assets["images/hero.png"]
	if initialCollisionAsset == nil {
		t.Fatal("first.Assets[images/hero.png] = nil, want asset")
	}
	if initialCollisionAsset.DstPath == "assets/hero.png" {
		t.Fatalf("first.Assets[images/hero.png].DstPath = %q, want hashed collision path", initialCollisionAsset.DstPath)
	}
	initialCollisionDst := initialCollisionAsset.DstPath

	writeBuildTestFile(t, vaultPath, "images/hero.png", "collision-image-v2")

	getRenderedMarkdownPaths := captureRenderedMarkdownNotePaths(t)
	result, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard})
	if err != nil {
		t.Fatalf("second buildWithOptions() error = %v", err)
	}
	if result.NotePages != 1 {
		t.Fatalf("result.NotePages = %d, want %d", result.NotePages, 1)
	}
	if got := getRenderedMarkdownPaths(); !reflect.DeepEqual(got, []string{"notes/collision.md"}) {
		t.Fatalf("rendered markdown notes = %#v, want %#v", got, []string{"notes/collision.md"})
	}

	collisionAsset := result.Assets["images/hero.png"]
	if collisionAsset == nil {
		t.Fatal("result.Assets[images/hero.png] = nil, want asset")
	}
	if collisionAsset.DstPath == initialCollisionDst {
		t.Fatalf("result.Assets[images/hero.png].DstPath = %q, want recomputed hashed destination after content change", collisionAsset.DstPath)
	}
	if collisionAsset.DstPath == "assets/hero.png" {
		t.Fatalf("result.Assets[images/hero.png].DstPath = %q, want hashed collision path", collisionAsset.DstPath)
	}
	if got := string(readBuildOutputFile(t, outputPath, collisionAsset.DstPath)); got != "collision-image-v2" {
		t.Fatalf("%s = %q, want %q", collisionAsset.DstPath, got, "collision-image-v2")
	}
	if _, statErr := os.Stat(filepath.Join(outputPath, filepath.FromSlash(initialCollisionDst))); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("os.Stat(%q) error = %v, want old collision asset path removed from rebuilt output", initialCollisionDst, statErr)
	}

	collisionHTML := readBuildOutputFile(t, outputPath, "collision/index.html")
	if !containsAny(collisionHTML, `src="../`+collisionAsset.DstPath+`"`, `src=../`+collisionAsset.DstPath) {
		t.Fatalf("collision page missing recomputed hashed asset link\n%s", collisionHTML)
	}
	if containsAny(collisionHTML, `src="../`+initialCollisionDst+`"`, `src=../`+initialCollisionDst) {
		t.Fatalf("collision page still references stale hashed asset path %q\n%s", initialCollisionDst, collisionHTML)
	}
	if got := string(readBuildOutputFile(t, outputPath, result.Assets["attachments/hero.png"].DstPath)); got != "attachment-image" {
		t.Fatalf("host collision peer asset contents = %q, want %q", got, "attachment-image")
	}
}

func TestBuildRestoresPlainMarkdownImagePathWhenReservedOutputIsReleasedOnCachedNote(t *testing.T) {
	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	customCSSContent := "body { color: tomato; }\n"
	attachmentCSSContent := "body { color: dodgerblue; }\n"
	customCSSPath := filepath.Join(vaultPath, "custom.css")

	writeBuildTestFile(t, vaultPath, "custom.css", customCSSContent)
	writeBuildTestFile(t, vaultPath, "attachments/custom.css", attachmentCSSContent)
	writeBuildTestFile(t, vaultPath, "notes/host.md", `---
title: Host
date: 2026-04-06
---
# Host

![Styles](../attachments/custom.css)
`)

	reservedCfg := testBuildSiteConfig()
	reservedCfg.CustomCSS = customCSSPath

	first, err := buildWithOptions(reservedCfg, vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard})
	if err != nil {
		t.Fatalf("first buildWithOptions() error = %v", err)
	}

	initialHostAsset := first.Assets["attachments/custom.css"]
	if initialHostAsset == nil {
		t.Fatal("first.Assets[attachments/custom.css] = nil, want asset")
	}
	if initialHostAsset.DstPath == "assets/custom.css" {
		t.Fatalf("first.Assets[attachments/custom.css].DstPath = %q, want hashed reserved path", initialHostAsset.DstPath)
	}
	initialHostDst := initialHostAsset.DstPath
	if err := os.Remove(customCSSPath); err != nil {
		t.Fatalf("os.Remove(%q) error = %v", customCSSPath, err)
	}

	getRenderedMarkdownPaths := captureRenderedMarkdownNotePaths(t)
	result, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard})
	if err != nil {
		t.Fatalf("second buildWithOptions() error = %v", err)
	}
	if result.NotePages != 1 {
		t.Fatalf("result.NotePages = %d, want %d", result.NotePages, 1)
	}
	if got := getRenderedMarkdownPaths(); !reflect.DeepEqual(got, []string{"notes/host.md"}) {
		t.Fatalf("rendered markdown notes = %#v, want %#v", got, []string{"notes/host.md"})
	}

	hostAsset := result.Assets["attachments/custom.css"]
	if hostAsset == nil {
		t.Fatal("result.Assets[attachments/custom.css] = nil, want asset")
	}
	if hostAsset.DstPath != "assets/custom.css" {
		t.Fatalf("result.Assets[attachments/custom.css].DstPath = %q, want %q", hostAsset.DstPath, "assets/custom.css")
	}
	if got := string(readBuildOutputFile(t, outputPath, hostAsset.DstPath)); got != attachmentCSSContent {
		t.Fatalf("%s = %q, want %q", hostAsset.DstPath, got, attachmentCSSContent)
	}

	hostHTML := readBuildOutputFile(t, outputPath, "host/index.html")
	if !containsAny(hostHTML, `src="../assets/custom.css"`, `src=../assets/custom.css`) {
		t.Fatalf("host page missing restored plain reserved-path asset link\n%s", hostHTML)
	}
	if containsAny(hostHTML, `src="../`+initialHostDst+`"`, `src=../`+initialHostDst) {
		t.Fatalf("host page still references stale reserved-path asset %q\n%s", initialHostDst, hostHTML)
	}
}

func TestBuildRerendersCleanMarkdownImageNotesWhenResolutionChanges(t *testing.T) {
	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, ".obsidian/app.json", `{"attachmentFolderPath":"attachments"}`)
	writeBuildTestFile(t, vaultPath, "attachments/hero.png", "attachment-image")
	writeBuildTestFile(t, vaultPath, "notes/host.md", `---
title: Host
date: 2026-04-06
---
# Host

![Hero](hero.png)
`)

	if _, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard}); err != nil {
		t.Fatalf("first buildWithOptions() error = %v", err)
	}

	if err := os.Remove(filepath.Join(vaultPath, "attachments", "hero.png")); err != nil {
		t.Fatalf("os.Remove(attachments/hero.png) error = %v", err)
	}
	writeBuildTestFile(t, vaultPath, "notes/hero.png", "note-local-image")

	getRenderedPaths := captureRenderedNotePagePaths(t)
	result, err := buildWithOptions(testBuildSiteConfig(), vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard})
	if err != nil {
		t.Fatalf("second buildWithOptions() error = %v", err)
	}
	if result.NotePages != 1 {
		t.Fatalf("result.NotePages = %d, want %d", result.NotePages, 1)
	}
	if got := getRenderedPaths(); !reflect.DeepEqual(got, []string{"notes/host.md"}) {
		t.Fatalf("rendered note pages = %#v, want %#v", got, []string{"notes/host.md"})
	}
	if stale := result.Assets["attachments/hero.png"]; stale != nil {
		t.Fatalf("result.Assets[attachments/hero.png] = %#v, want nil after resolution moved", stale)
	}

	hostAsset := result.Assets["notes/hero.png"]
	if hostAsset == nil {
		t.Fatal("result.Assets[notes/hero.png] = nil, want resolved note-local asset")
	}
	if hostAsset.DstPath != "assets/hero.png" {
		t.Fatalf("result.Assets[notes/hero.png].DstPath = %q, want %q", hostAsset.DstPath, "assets/hero.png")
	}
	if got := string(readBuildOutputFile(t, outputPath, hostAsset.DstPath)); got != "note-local-image" {
		t.Fatalf("%s = %q, want %q", hostAsset.DstPath, got, "note-local-image")
	}

	hostHTML := readBuildOutputFile(t, outputPath, "host/index.html")
	if !containsAny(hostHTML, `src="../assets/hero.png"`, `src=../assets/hero.png`) {
		t.Fatalf("host page missing rerendered markdown image link\n%s", hostHTML)
	}

	manifestJSON := readBuildOutputFile(t, outputPath, cacheManifestRelPath)
	var manifest CacheManifest
	if err := json.Unmarshal(manifestJSON, &manifest); err != nil {
		t.Fatalf("json.Unmarshal(manifest) error = %v\n%s", err, manifestJSON)
	}
	entry, ok := manifest.Notes["notes/host.md"]
	if !ok {
		t.Fatal("manifest.Notes[notes/host.md] missing entry")
	}
	if len(entry.Assets) != 1 || entry.Assets[0].SrcPath != "notes/hero.png" || entry.Assets[0].DstPath != "assets/hero.png" {
		t.Fatalf("manifest.Notes[notes/host.md].Assets = %#v, want note-local hero asset", entry.Assets)
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
	got := getRenderedPaths()
	want := []string{"notes/alpha.md", "notes/beta.md"}
	if result.NotePages != len(want) {
		t.Fatalf("result.NotePages = %d, want %d", result.NotePages, len(want))
	}
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
	want := []string{"notes/alpha.md", "notes/gamma.md"}
	if result.NotePages != len(want) {
		t.Fatalf("result.NotePages = %d, want %d", result.NotePages, len(want))
	}

	if got := getRenderedPaths(); !reflect.DeepEqual(got, want) {
		t.Fatalf("rendered note pages = %#v, want %#v", got, want)
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
	want := []string{"notes/aaron.md", "notes/alpha.md", "notes/beta.md"}
	if result.NotePages != len(want) {
		t.Fatalf("result.NotePages = %d, want %d", result.NotePages, len(want))
	}

	if got := getRenderedPaths(); !reflect.DeepEqual(got, want) {
		t.Fatalf("rendered note pages = %#v, want %#v", got, want)
	}
}

func TestBuildTreatsSuccessfulTransclusionsAsDirectHostReferences(t *testing.T) {
	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "notes/host.md", `---
title: Host
date: 2026-04-06
---

![[Portal]]
`)
	writeBuildTestFile(t, vaultPath, "notes/guide.md", `---
title: Guide
date: 2026-04-06
aliases:
  - Portal
---

[[Host]]
`)

	cfg := testBuildSiteConfig()
	cfg.Related.Enabled = true
	cfg.Related.Count = 1

	result, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard})
	if err != nil {
		t.Fatalf("buildWithOptions() error = %v", err)
	}
	if result == nil {
		t.Fatal("buildWithOptions() = nil result, want build result")
	}
	if result.Graph == nil {
		t.Fatal("result.Graph = nil, want link graph")
	}
	if got := result.Graph.Forward["notes/host.md"]; !reflect.DeepEqual(got, []string{"notes/guide.md"}) {
		t.Fatalf("result.Graph.Forward[notes/host.md] = %#v, want %#v", got, []string{"notes/guide.md"})
	}
	if got := result.Graph.Backward["notes/guide.md"]; !reflect.DeepEqual(got, []string{"notes/host.md"}) {
		t.Fatalf("result.Graph.Backward[notes/guide.md] = %#v, want %#v", got, []string{"notes/host.md"})
	}
	if got := result.Graph.Backward["notes/host.md"]; !reflect.DeepEqual(got, []string{"notes/guide.md"}) {
		t.Fatalf("result.Graph.Backward[notes/host.md] = %#v, want %#v", got, []string{"notes/guide.md"})
	}
	if len(result.Diagnostics) != 0 {
		t.Fatalf("result.Diagnostics = %#v, want no diagnostics", result.Diagnostics)
	}

	guideHTML := readBuildOutputFile(t, outputPath, "guide/index.html")
	if !containsAny(guideHTML, `href="../host/">Host</a>`, `href=../host/>Host</a>`) {
		t.Fatalf("guide page missing backlink for transcluding host\n%s", guideHTML)
	}

	hostHTML := readBuildOutputFile(t, outputPath, "host/index.html")
	if !containsAny(hostHTML, `id="related-articles-heading">Related Articles</h2>`, `id=related-articles-heading>Related Articles</h2>`) {
		t.Fatalf("host page missing related-articles section for embedded mutual reference\n%s", hostHTML)
	}
	if !containsAny(hostHTML, `href="../guide/">Guide</a>`, `href=../guide/>Guide</a>`) {
		t.Fatalf("host page missing related guide link for embedded mutual reference\n%s", hostHTML)
	}
}

func TestRelatedProductionCutoverRendersRelatedArticles(t *testing.T) {
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

func TestBuildAddingSidebarNodeKeepsExistingNotePagesByteIdentical(t *testing.T) {
	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")

	writeBuildTestFile(t, vaultPath, "notes/alpha.md", "# Alpha\n\nBody.\n")
	writeBuildTestFile(t, vaultPath, "notes/beta.md", "# Beta\n\nBody.\n")

	cfg := testBuildSiteConfig()
	cfg.Sidebar.Enabled = true
	cfg.Related.Enabled = false
	if _, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard}); err != nil {
		t.Fatalf("first buildWithOptions() error = %v", err)
	}
	alphaBefore := append([]byte(nil), readBuildOutputFile(t, outputPath, "alpha/index.html")...)
	betaBefore := append([]byte(nil), readBuildOutputFile(t, outputPath, "beta/index.html")...)
	sidebarBefore := append([]byte(nil), readBuildOutputFile(t, outputPath, sidebarJSONOutputPath)...)

	writeBuildTestFile(t, vaultPath, "notes/gamma.md", "# Gamma\n\nBody.\n")
	getRenderedPaths := captureRenderedNotePagePaths(t)
	result, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard})
	if err != nil {
		t.Fatalf("second buildWithOptions() error = %v", err)
	}
	if result.NotePages != 1 {
		t.Fatalf("result.NotePages = %d, want 1 for the new note only", result.NotePages)
	}
	if got, want := getRenderedPaths(), []string{"notes/gamma.md"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rendered note pages = %#v, want %#v", got, want)
	}
	if got := readBuildOutputFile(t, outputPath, "alpha/index.html"); !bytes.Equal(got, alphaBefore) {
		t.Fatalf("alpha page changed after unrelated Sidebar node addition")
	}
	if got := readBuildOutputFile(t, outputPath, "beta/index.html"); !bytes.Equal(got, betaBefore) {
		t.Fatalf("beta page changed after unrelated Sidebar node addition")
	}
	sidebarAfter := readBuildOutputFile(t, outputPath, sidebarJSONOutputPath)
	if bytes.Equal(sidebarAfter, sidebarBefore) || !bytes.Contains(sidebarAfter, []byte(`"url":"gamma/"`)) {
		t.Fatalf("sidebar.json did not update for gamma\n%s", sidebarAfter)
	}
}

func TestBuildPaginationChangeDoesNotRerenderMarkdownOrNotePages(t *testing.T) {
	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	for _, name := range []string{"alpha", "beta", "gamma"} {
		writeBuildTestFile(t, vaultPath, "notes/"+name+".md", "# "+name+"\n\nBody.\n")
	}
	cfg := testBuildSiteConfig()
	cfg.Pagination.PageSize = 20
	if _, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{}); err != nil {
		t.Fatal(err)
	}
	alphaBefore := append([]byte(nil), readBuildOutputFile(t, outputPath, "alpha/index.html")...)

	cfg.Pagination.PageSize = 2
	getRenderedMarkdown := captureRenderedMarkdownNotePaths(t)
	getRenderedPages := captureRenderedNotePagePaths(t)
	result, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.NotePages != 0 || len(getRenderedMarkdown()) != 0 || len(getRenderedPages()) != 0 {
		t.Fatalf("pagination change rerendered notes: NotePages=%d markdown=%#v pages=%#v", result.NotePages, getRenderedMarkdown(), getRenderedPages())
	}
	if got := readBuildOutputFile(t, outputPath, "alpha/index.html"); !bytes.Equal(got, alphaBefore) {
		t.Fatal("pagination change altered note page bytes")
	}
	_ = readBuildOutputFile(t, outputPath, "page/2/index.html")
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
	if strings.TrimSpace(manifest.BuildABISignature) == "" {
		t.Fatal("manifest.BuildABISignature = empty, want non-empty build ABI signal")
	}
	for _, relPath := range []string{"index.html", "tags/alpha/index.html", "alpha/index.html", "notes/index.html"} {
		if strings.TrimSpace(manifest.Pages[relPath]) == "" {
			t.Fatalf("manifest.Pages[%q] = %q, want non-empty page signature", relPath, manifest.Pages[relPath])
		}
	}
	if _, ok := manifest.Notes["alpha/one.md"].DerivedSignatures["sidebar"]; ok {
		t.Fatalf("manifest note retains obsolete per-page Sidebar signature: %#v", manifest.Notes["alpha/one.md"].DerivedSignatures)
	}
	if strings.TrimSpace(manifest.Notes["alpha/one.md"].DerivedSignatures[derivedSignatureKeyRelated]) == "" {
		t.Fatalf("manifest.Notes[alpha/one.md].DerivedSignatures[%q] = %q, want non-empty signature", derivedSignatureKeyRelated, manifest.Notes["alpha/one.md"].DerivedSignatures[derivedSignatureKeyRelated])
	}
	if strings.TrimSpace(manifest.Notes["alpha/one.md"].DerivedSignatures[derivedSignatureKeyBacklinks]) == "" {
		t.Fatalf("manifest.Notes[alpha/one.md].DerivedSignatures[%q] = %q, want non-empty signature", derivedSignatureKeyBacklinks, manifest.Notes["alpha/one.md"].DerivedSignatures[derivedSignatureKeyBacklinks])
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
	getRenderedNotePaths := captureRenderedNotePagePaths(t)
	result, err = buildWithOptions(cfg, vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard})
	if err != nil {
		t.Fatalf("third buildWithOptions() error = %v", err)
	}
	wantNotePaths := []string{"alpha/one.md"}
	if result.NotePages != len(wantNotePaths) {
		t.Fatalf("result.NotePages = %d, want %d", result.NotePages, len(wantNotePaths))
	}
	if got := getRenderedNotePaths(); !reflect.DeepEqual(got, wantNotePaths) {
		t.Fatalf("rendered note pages = %#v, want %#v", got, wantNotePaths)
	}
	assertRenderedArchivePageCalls(t, "index pages", getRenderedIndexPaths(), []string{"index.html"})
	assertRenderedArchivePageCalls(t, "tag pages", getRenderedTagPaths(), []string{"tags/alpha/index.html"})
	assertRenderedArchivePageCalls(t, "folder pages", getRenderedFolderPaths(), []string{"alpha/index.html"})
	assertRenderedArchivePageCalls(t, "timeline pages", getRenderedTimelinePaths(), []string{"notes/index.html"})
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

	if _, err := BuildWithOptions(SiteInput{Config: testBuildSiteConfig()}, vaultPath, outputPath, Options{}); err != nil {
		t.Fatalf("first BuildWithOptions() error = %v", err)
	}

	result, err := BuildWithOptions(SiteInput{Config: testBuildSiteConfig()}, vaultPath, outputPath, Options{Force: true})
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
	buildVaultIndex = func(scanResult vault.ScanResult, frontmatterResult vault.FrontmatterResult, diagCollector *diag.Collector, options vault.BuildIndexOptions) (vault.IndexResult, error) {
		seenConcurrency = options.Concurrency
		return originalBuildVaultIndex(scanResult, frontmatterResult, diagCollector, options)
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
		RSS: model.RSSConfig{
			Enabled: true,
		},
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

func captureRenderedNotePagePaths(t *testing.T) func() []string {
	t.Helper()
	lockBuildTestRenderHooks(t)

	originalRenderNotePage := renderNotePage
	var renderedMu sync.Mutex
	rendered := make([]string, 0, 8)
	renderNotePage = func(input internalrender.NotePageInput) (internalrender.RenderedPage, error) {
		if input.Note != nil {
			renderedMu.Lock()
			rendered = append(rendered, input.Note.RelPath)
			renderedMu.Unlock()
		}
		return originalRenderNotePage(input)
	}
	t.Cleanup(func() {
		renderNotePage = originalRenderNotePage
	})

	return func() []string {
		renderedMu.Lock()
		defer renderedMu.Unlock()
		return uniqueSortedStrings(append([]string(nil), rendered...))
	}
}

func captureRenderedMarkdownNotePaths(t *testing.T) func() []string {
	t.Helper()
	lockBuildTestRenderHooks(t)

	originalRenderMarkdownNote := renderMarkdownNote
	var renderedMu sync.Mutex
	rendered := make([]string, 0, 8)
	renderMarkdownNote = func(idx *model.VaultIndex, note *model.Note, assetSink internalmarkdown.AssetSink) (*renderedNote, error) {
		if note != nil {
			renderedMu.Lock()
			rendered = append(rendered, note.RelPath)
			renderedMu.Unlock()
		}
		return originalRenderMarkdownNote(idx, note, assetSink)
	}
	t.Cleanup(func() {
		renderMarkdownNote = originalRenderMarkdownNote
	})

	return func() []string {
		renderedMu.Lock()
		defer renderedMu.Unlock()
		return uniqueSortedStrings(append([]string(nil), rendered...))
	}
}

func captureRenderedIndexPagePaths(t *testing.T) func() []string {
	t.Helper()
	lockBuildTestRenderHooks(t)

	originalRenderIndexPage := renderIndexPage
	var renderedMu sync.Mutex
	rendered := make([]string, 0, 4)
	renderIndexPage = func(input internalrender.IndexPageInput) (internalrender.RenderedPage, error) {
		renderedMu.Lock()
		rendered = append(rendered, input.RelPath)
		renderedMu.Unlock()
		return originalRenderIndexPage(input)
	}
	t.Cleanup(func() {
		renderIndexPage = originalRenderIndexPage
	})

	return func() []string {
		renderedMu.Lock()
		defer renderedMu.Unlock()
		return sortedStrings(append([]string(nil), rendered...))
	}
}

func captureRenderedTagPagePaths(t *testing.T) func() []string {
	t.Helper()
	lockBuildTestRenderHooks(t)

	originalRenderTagPage := renderTagPage
	var renderedMu sync.Mutex
	rendered := make([]string, 0, 4)
	renderTagPage = func(input internalrender.TagPageInput) (internalrender.RenderedPage, error) {
		renderedMu.Lock()
		rendered = append(rendered, input.RelPath)
		renderedMu.Unlock()
		return originalRenderTagPage(input)
	}
	t.Cleanup(func() {
		renderTagPage = originalRenderTagPage
	})

	return func() []string {
		renderedMu.Lock()
		defer renderedMu.Unlock()
		return sortedStrings(append([]string(nil), rendered...))
	}
}

func captureRenderedFolderPagePaths(t *testing.T) func() []string {
	t.Helper()
	lockBuildTestRenderHooks(t)

	originalRenderFolderPage := renderFolderPage
	var renderedMu sync.Mutex
	rendered := make([]string, 0, 4)
	renderFolderPage = func(input internalrender.FolderPageInput) (internalrender.RenderedPage, error) {
		renderedMu.Lock()
		rendered = append(rendered, input.RelPath)
		renderedMu.Unlock()
		return originalRenderFolderPage(input)
	}
	t.Cleanup(func() {
		renderFolderPage = originalRenderFolderPage
	})

	return func() []string {
		renderedMu.Lock()
		defer renderedMu.Unlock()
		return sortedStrings(append([]string(nil), rendered...))
	}
}

func captureRenderedTimelinePagePaths(t *testing.T) func() []string {
	t.Helper()
	lockBuildTestRenderHooks(t)

	originalRenderTimelinePage := renderTimelinePage
	var renderedMu sync.Mutex
	rendered := make([]string, 0, 4)
	renderTimelinePage = func(input internalrender.TimelinePageInput) (internalrender.RenderedPage, error) {
		renderedMu.Lock()
		rendered = append(rendered, input.RelPath)
		renderedMu.Unlock()
		return originalRenderTimelinePage(input)
	}
	t.Cleanup(func() {
		renderTimelinePage = originalRenderTimelinePage
	})

	return func() []string {
		renderedMu.Lock()
		defer renderedMu.Unlock()
		return sortedStrings(append([]string(nil), rendered...))
	}
}

func assertRenderedArchivePageCalls(t *testing.T, label string, got []string, want []string) {
	t.Helper()

	for relPath, count := range countStrings(got) {
		if count > 1 {
			t.Fatalf("%s render call count for %q = %d, want <= 1 (%#v)", label, relPath, count, got)
		}
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rendered %s = %#v, want %#v", label, got, want)
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

func readBuildCacheManifest(t *testing.T, outputRoot string) CacheManifest {
	t.Helper()

	data := readBuildOutputFile(t, outputRoot, cacheManifestRelPath)
	var manifest CacheManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("json.Unmarshal(cache manifest) error = %v\n%s", err, data)
	}
	return manifest
}

func prepareStagedOutputPublisherForFailureTest(t *testing.T) (*stagedOutputPublisher, string, string) {
	t.Helper()

	root := t.TempDir()
	vaultPath := filepath.Join(root, "vault")
	outputPath := filepath.Join(root, "site")
	if err := os.MkdirAll(vaultPath, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(vaultPath) error = %v", err)
	}
	if err := writeManagedOutputMarker(outputPath); err != nil {
		t.Fatalf("writeManagedOutputMarker(%q) error = %v", outputPath, err)
	}
	if err := os.WriteFile(filepath.Join(outputPath, "index.html"), []byte("stable output"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(output index) error = %v", err)
	}

	publisher, err := prepareStagedOutputPublisher(vaultPath, outputPath)
	if err != nil {
		t.Fatalf("prepareStagedOutputPublisher() error = %v", err)
	}
	if publisher == nil {
		t.Fatal("prepareStagedOutputPublisher() = nil, want publisher")
	}
	if err := os.WriteFile(filepath.Join(publisher.stagingPath, "index.html"), []byte("new output"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(staged index) error = %v", err)
	}

	return publisher, outputPath, publisher.stagingPath
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
