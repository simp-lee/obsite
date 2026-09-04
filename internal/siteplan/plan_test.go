package siteplan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/simp-lee/obsite/internal/diag"
	"github.com/simp-lee/obsite/internal/model"
)

func TestBuildWithConfigPlansSectionsAndDocumentOrder(t *testing.T) {
	vault := t.TempDir()
	writePlanFile(t, vault, "_index.md", "---\ntitle: Home\npublish: true\norder: 0\n---\nHome\n")
	writePlanFile(t, vault, "guide/_index.md", "---\ntitle: Guide\npublish: true\norder: 1\n---\nGuide\n")
	writePlanFile(t, vault, "guide/01-First.md", "---\ntitle: First\npublish: true\ntype: doc\n---\nFirst\n")
	writePlanFile(t, vault, "guide/02 second.md", "---\ntitle: Second\npublish: true\ntype: doc\n---\nSecond\n")

	cfg := model.SiteConfig{Title: "Site", BaseURL: "https://example.test/docs/", Navigation: []model.NavigationItem{{Name: "Guide", Section: "guide"}}}
	result, err := BuildWithConfig(vault, cfg)
	if err != nil {
		t.Fatalf("BuildWithConfig() error = %v; diagnostics=%v", err, result.Diagnostics)
	}
	if result.Plan.Root == nil || result.Plan.Root.Route != "/" {
		t.Fatalf("root = %#v, want root route", result.Plan.Root)
	}
	guide := result.Plan.Root.Children[0]
	if guide.Route != "/guide/" || guide.Banner != "" {
		t.Fatalf("guide = %#v", guide)
	}
	if len(guide.Documents) != 2 || guide.Documents[0].Frontmatter.Title != "First" || guide.Documents[1].Frontmatter.Title != "Second" {
		t.Fatalf("documents = %#v, want filename-prefix order", guide.Documents)
	}
	if result.Plan.Documents[0].Route != "/guide/First/" || result.Plan.Documents[1].Route != "/guide/second/" {
		t.Fatalf("routes = %q, %q", result.Plan.Documents[0].Route, result.Plan.Documents[1].Route)
	}
	if got := guide.Breadcrumbs; len(got) != 2 || got[0].URL != "/" || got[1].URL != "/guide/" {
		t.Fatalf("breadcrumbs = %#v", got)
	}
}

func TestBuildWithConfigRejectsMissingIndexAndHiddenOverride(t *testing.T) {
	vault := t.TempDir()
	writePlanFile(t, vault, "_index.md", "---\ntitle: Home\npublish: true\n---\n")
	writePlanFile(t, vault, "private/_index.md", "---\ntitle: Private\npublish: false\n---\n")
	writePlanFile(t, vault, "private/public/_index.md", "---\ntitle: Public\npublish: true\n---\n")
	writePlanFile(t, vault, "private/public/visible.md", "---\ntitle: Visible\npublish: true\ntype: page\n---\n")
	writePlanFile(t, vault, "private/direct.md", "---\ntitle: Direct\npublish: true\ntype: page\n---\n")
	writePlanFile(t, vault, "missing/article.md", "---\ntitle: Article\npublish: true\ntype: doc\n---\n")

	result, err := BuildWithConfig(vault, model.SiteConfig{Title: "Site", BaseURL: "https://example.test/"})
	if err == nil {
		t.Fatal("BuildWithConfig() error = nil, want structural errors")
	}
	joined := diagnosticMessages(result.Diagnostics)
	for _, want := range []string{"missing required _index.md", "private", "public"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("diagnostics = %q, want %q", joined, want)
		}
	}
}

func TestBuildWithConfigPlansIndependentVersionTreesAndFallbacks(t *testing.T) {
	vault := t.TempDir()
	writePlanFile(t, vault, "_index.md", "---\ntitle: Home\npublish: true\n---\n")
	writePlanFile(t, vault, "docs/_index.md", "---\ntitle: Docs\npublish: true\n---\n")
	for _, version := range []string{"v1", "v2"} {
		writePlanFile(t, vault, "docs/"+version+"/_index.md", "---\ntitle: "+version+"\npublish: true\n---\n")
	}
	writePlanFile(t, vault, "docs/v1/01-intro.md", "---\ntitle: Intro\npublish: true\ntype: doc\n---\n")
	writePlanFile(t, vault, "docs/v1/only-v1.md", "---\ntitle: Only v1\npublish: true\ntype: doc\n---\n")
	writePlanFile(t, vault, "docs/v2/intro.md", "---\ntitle: Intro\npublish: true\ntype: doc\n---\n")
	cfg := model.SiteConfig{Title: "Site", BaseURL: "https://example.test/", Versions: &model.VersionsConfig{
		Root: "docs", Default: "v1", Entries: []model.VersionEntry{{ID: "v1", Label: "Version 1", Source: "v1"}, {ID: "v2", Label: "Version 2", Source: "v2"}},
	}}
	result, err := BuildWithConfig(vault, cfg)
	if err != nil {
		t.Fatalf("BuildWithConfig() error = %v; diagnostics=%v", err, result.Diagnostics)
	}
	if len(result.Plan.Versions) != 2 || result.Plan.Versions[0].Root == nil {
		t.Fatalf("versions = %#v", result.Plan.Versions)
	}
	var v1Only *model.Note
	for _, note := range result.Plan.Documents {
		if note.Frontmatter.Title == "Only v1" {
			v1Only = note
		}
	}
	if v1Only == nil || v1Only.VersionRoutes["v2"] != "/docs/v2/" {
		t.Fatalf("v1 fallback routes = %#v", v1Only)
	}
	var intro *model.Note
	for _, note := range result.Plan.Documents {
		if note.Frontmatter.Title == "Intro" && note.VersionID == "v1" {
			intro = note
		}
	}
	if intro == nil || intro.VersionRoutes["v2"] != "/docs/v2/intro/" {
		t.Fatalf("matching version routes = %#v", intro)
	}
}

func TestBuildWithConfigNormalizesUnicodeRoutesAndClaimsReservedOutputs(t *testing.T) {
	vault := t.TempDir()
	writePlanFile(t, vault, "_index.md", "---\ntitle: Home\npublish: true\n---\n")
	writePlanFile(t, vault, "Cafe\u0301/_index.md", "---\ntitle: Decomposed\npublish: true\n---\n")
	writePlanFile(t, vault, "Café/_index.md", "---\ntitle: Composed\npublish: true\n---\n")
	writePlanFile(t, vault, "assets/_index.md", "---\ntitle: Assets\npublish: true\n---\n")
	result, err := BuildWithConfig(vault, model.SiteConfig{Title: "Site", BaseURL: "https://example.test/"})
	if err == nil {
		t.Fatal("BuildWithConfig() error = nil, want route collisions")
	}
	messages := diagnosticMessages(result.Diagnostics)
	if !strings.Contains(messages, "conflicts") || !strings.Contains(messages, "reserved output") {
		t.Fatalf("diagnostics = %q", messages)
	}
}

func TestBuildWithConfigRejectsCaseInsensitiveAndReservedPhysicalRoutes(t *testing.T) {
	vault := t.TempDir()
	writePlanFile(t, vault, "_index.md", "---\ntitle: Home\npublish: true\n---\n")
	writePlanFile(t, vault, "Foo/_index.md", "---\ntitle: Upper\npublish: true\n---\n")
	writePlanFile(t, vault, "foo/_index.md", "---\ntitle: Lower\npublish: true\n---\n")
	writePlanFile(t, vault, "CON.md", "---\ntitle: Device\npublish: true\ntype: page\n---\n")
	result, err := BuildWithConfig(vault, model.SiteConfig{Title: "Site", BaseURL: "https://example.test/"})
	if err == nil || !strings.Contains(diagnosticMessages(result.Diagnostics), "Windows-reserved") {
		t.Fatalf("BuildWithConfig() error=%v diagnostics=%v, want physical route rejection", err, result.Diagnostics)
	}
	if !strings.Contains(diagnosticMessages(result.Diagnostics), "conflicts") {
		t.Fatalf("diagnostics=%v, want case-insensitive route collision", result.Diagnostics)
	}
}

func TestBuildWithConfigRejectsUnknownAndImplicitArticleMetadata(t *testing.T) {
	vault := t.TempDir()
	writePlanFile(t, vault, "_index.md", "---\ntitle: Home\npublish: true\n---\n")
	writePlanFile(t, vault, "article.md", "---\ntitle: Article\npublish: true\nlayout: legacy\n---\n")

	result, err := BuildWithConfig(vault, model.SiteConfig{Title: "Site", BaseURL: "https://example.test/"})
	if err == nil || !strings.Contains(diagnosticMessages(result.Diagnostics), "unknown frontmatter field") {
		t.Fatalf("BuildWithConfig() error=%v diagnostics=%v, want unknown-field rejection", err, result.Diagnostics)
	}
}

func TestBuildWithConfigRejectsMissingLocalMarkdownAssets(t *testing.T) {
	vault := t.TempDir()
	writePlanFile(t, vault, "_index.md", "---\ntitle: Home\npublish: true\n---\n")
	writePlanFile(t, vault, "images/Cafe\u0301.png", "decomposed")
	writePlanFile(t, vault, "images/Café.png", "composed")
	writePlanFile(t, vault, "files/Cafe\u0301", "decomposed")
	writePlanFile(t, vault, "files/Café", "composed")
	writePlanFile(t, vault, "article.md", "---\ntitle: Article\npublish: true\ntype: page\n---\n![Missing](missing.png)\n\n[Attachment](missing.pdf)\n\n![[missing-embed.png]]\n\n![[missing%2Epng]]\n\n![Outside](../outside.png)\n\n[Root attachment](/missing-root.pdf)\n\n[Fragment attachment](missing-fragment.pdf#page=2)\n\n![[images/CAF%C3%89%2Epng]]\n\n![[images/CAFÉ.png]]\n\n![Exact](images/Café.png)\n\n[Ambiguous attachment](/files/CAFÉ#part)\n\n[Windows attachment](C:/assets/manual.pdf)\n\n[Malformed attachment](missing%ZZ.pdf)\n\n![Remote](https://images.example.test/remote.png)\n\n[Remote attachment](https://files.example.test/manual.pdf)\n\n![[https://cdn.example.test/embed.png]]\n\n[Missing route](missing/)\n\n[Missing extensionless route](missing-page)\n\n[[Missing Note]]\n")

	result, err := BuildWithConfig(vault, model.SiteConfig{Title: "Site", BaseURL: "https://example.test/"})
	if err == nil {
		t.Fatal("BuildWithConfig() error = nil, want unresolved local asset errors")
	}

	wantErrors := map[string]int{
		"missing.png":                 6,
		"missing.pdf":                 8,
		"missing-embed.png":           10,
		"missing%2Epng":               12,
		"../outside.png":              14,
		"/missing-root.pdf":           16,
		"missing-fragment.pdf#page=2": 18,
		"images/CAF%C3%89%2Epng":      20,
		"images/CAFÉ.png":             22,
		"/files/CAFÉ#part":            26,
		"C:/assets/manual.pdf":        28,
		"missing%ZZ.pdf":              30,
	}
	for target, line := range wantErrors {
		found := false
		for _, item := range result.Diagnostics {
			if item.Target != target {
				continue
			}
			found = true
			if item.Severity != diag.SeverityError || item.Kind != diag.KindUnresolvedAsset || item.Location.Path != "article.md" || item.Location.Line != line {
				t.Fatalf("diagnostic for %q = %#v, want unresolved_asset error at article.md:%d", target, item, line)
			}
		}
		if !found {
			t.Fatalf("diagnostics = %#v, want error for %q", result.Diagnostics, target)
		}
	}
	for _, item := range result.Diagnostics {
		if item.Target == "https://images.example.test/remote.png" || item.Target == "https://files.example.test/manual.pdf" {
			t.Fatalf("standard external target produced diagnostic: %#v", item)
		}
	}
	wantWarnings := map[string]diag.Kind{
		"https://cdn.example.test/embed.png": diag.KindUnresolvedAsset,
		"missing/":                           diag.KindDeadLink,
		"missing-page":                       diag.KindDeadLink,
		"Missing Note":                       diag.KindDeadLink,
	}
	for target, kind := range wantWarnings {
		found := false
		for _, item := range result.Diagnostics {
			if item.Target == target && item.Severity == diag.SeverityWarning && item.Kind == kind {
				found = true
			}
		}
		if !found {
			t.Fatalf("diagnostics = %#v, want %s warning for %q", result.Diagnostics, kind, target)
		}
	}
}

func diagnosticMessages(diagnostics []diag.Diagnostic) string {
	var values []string
	for _, diagnostic := range diagnostics {
		values = append(values, diagnostic.Message)
	}
	return strings.Join(values, "\n")
}

func writePlanFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
