package vault

import (
	"strings"
	"testing"

	"github.com/simp-lee/obsite/internal/diag"
)

func TestParseStrictFrontmatterSeparatesSectionsAndRequiresExplicitArticleFields(t *testing.T) {
	vaultPath := t.TempDir()
	writeVaultFile(t, vaultPath, "_index.md", "---\ntitle: Home\npublish: true\n---\n")
	writeVaultFile(t, vaultPath, "article.md", "---\ntitle: Article\npublish: true\ntype: post\ndate: 2026-04-05\ntags: [go, docs]\naliases: [Guide]\nauthor: Alice\nstatus: deprecated\n---\nbody\n")
	scan, err := Scan(vaultPath)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ParseStrictFrontmatter(scan)
	if err != nil {
		t.Fatalf("ParseStrictFrontmatter() error = %v", err)
	}
	if len(result.Sections) != 1 || len(result.Articles) != 1 || len(result.AllArticles) != 1 {
		t.Fatalf("result = %#v", result)
	}
	note := result.Articles[0]
	if note.Tags[0] != "go" || note.Aliases[0] != "Guide" || note.Frontmatter.Author != "Alice" || note.Frontmatter.Status != "deprecated" {
		t.Fatalf("note = %#v", note)
	}
}

func TestParseStrictFrontmatterRejectsUnknownDuplicateNullAndInvalidMetadata(t *testing.T) {
	tests := map[string]string{
		"unknown":        "title: A\npublish: true\ntype: doc\nlayout: old\n",
		"duplicate":      "title: A\ntitle: B\npublish: true\ntype: doc\n",
		"null":           "title: null\npublish: true\ntype: doc\n",
		"invalid status": "title: A\npublish: true\ntype: doc\nstatus: draft\n",
		"post date":      "title: A\npublish: true\ntype: post\n",
		"slug":           "title: A\npublish: true\ntype: doc\nslug: bad.slug\n",
	}
	for name, frontmatter := range tests {
		t.Run(name, func(t *testing.T) {
			vaultPath := t.TempDir()
			writeVaultFile(t, vaultPath, "_index.md", "---\ntitle: Home\npublish: true\n---\n")
			writeVaultFile(t, vaultPath, "article.md", "---\n"+frontmatter+"---\nbody\n")
			scan, err := Scan(vaultPath)
			if err != nil {
				t.Fatal(err)
			}
			_, err = ParseStrictFrontmatter(scan)
			if err == nil {
				t.Fatal("ParseStrictFrontmatter() error = nil")
			}
		})
	}
}

func TestParseStrictFrontmatterKeepsQuotedNullAsString(t *testing.T) {
	vaultPath := t.TempDir()
	writeVaultFile(t, vaultPath, "_index.md", "---\ntitle: \"null\"\npublish: true\n---\n")
	scan, err := Scan(vaultPath)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ParseStrictFrontmatter(scan)
	if err != nil {
		t.Fatalf("ParseStrictFrontmatter() error = %v", err)
	}
	if result.Sections[0].Frontmatter.Title != "null" {
		t.Fatalf("title = %q", result.Sections[0].Frontmatter.Title)
	}
}

func TestBuildStrictIndexResolvesMarkdownEntitiesBeforeAssetLookup(t *testing.T) {
	vaultPath := t.TempDir()
	writeVaultFile(t, vaultPath, "_index.md", "---\ntitle: Home\npublish: true\n---\n")
	writeVaultFile(t, vaultPath, "article.md", "---\ntitle: Article\npublish: true\ntype: doc\n---\n![image](A&amp;B.png)\n\n[file](A&amp;B.pdf)\n\n![remote](https&colon;//example.test/a&Tab;b.png)\n")
	writeVaultFile(t, vaultPath, "A&B.png", "png")
	writeVaultFile(t, vaultPath, "A&B.pdf", "pdf")

	scan, err := Scan(vaultPath)
	if err != nil {
		t.Fatal(err)
	}
	sources, err := ParseStrictFrontmatter(scan)
	if err != nil {
		t.Fatalf("ParseStrictFrontmatter() error = %v", err)
	}
	collector := diag.NewCollector()
	result, err := BuildStrictIndex(scan, sources, sources.Articles, nil, collector, BuildIndexOptions{Concurrency: 1})
	if err != nil {
		t.Fatalf("BuildStrictIndex() error = %v", err)
	}
	for _, resource := range []string{"A&B.png", "A&B.pdf"} {
		if result.Index.Assets[resource] == nil {
			t.Fatalf("Index.Assets[%q] = nil, want Markdown entity destination resolved", resource)
		}
	}
	if got := collector.Diagnostics(); len(got) != 0 {
		t.Fatalf("collector.Diagnostics() = %#v, want no diagnostics", got)
	}
}

func TestParseStrictFrontmatterRejectsImplicitFrontmatterAndDateBeforeUpdated(t *testing.T) {
	vaultPath := t.TempDir()
	writeVaultFile(t, vaultPath, "_index.md", "---\ntitle: Home\npublish: true\n---\n")
	writeVaultFile(t, vaultPath, "article.md", "---\ntitle: A\npublish: true\ntype: doc\ndate: 2026-04-05\nupdated: 2026-04-04\n---\n")
	scan, err := Scan(vaultPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ParseStrictFrontmatter(scan)
	if err == nil || !strings.Contains(err.Error(), "updated") {
		t.Fatalf("error = %v", err)
	}
}
