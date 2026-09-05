package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateCommandIsReadOnlyAndAcceptsStrictVault(t *testing.T) {
	vault := t.TempDir()
	writeValidateFile(t, vault, "obsite.yaml", "title: Site\nbaseURL: https://example.test/\nnavigation: []\n")
	writeValidateFile(t, vault, "_index.md", "---\ntitle: Home\npublish: true\n---\n")
	writeValidateFile(t, vault, "article.md", "---\ntitle: Article\npublish: true\ntype: doc\n---\n")
	_, stderr, err := executeForTest(t, testCommandDependencies(), []string{"validate", "--vault", vault})
	if err != nil {
		t.Fatalf("validate error = %v; stderr=%q", err, stderr)
	}
	if _, statErr := os.Stat(filepath.Join(vault, "public")); !os.IsNotExist(statErr) {
		t.Fatalf("validate created output: %v", statErr)
	}
}

func TestValidateCommandReportsStrictSchemaFailure(t *testing.T) {
	vault := t.TempDir()
	writeValidateFile(t, vault, "obsite.yaml", "title: Site\nbaseURL: https://example.test/\nnavigation: []\ndefaultPublish: true\n")
	_, stderr, err := executeForTest(t, testCommandDependencies(), []string{"validate", "--vault", vault})
	if err == nil || !strings.Contains(stderr, "error schema") || !strings.Contains(stderr, "defaultPublish") {
		t.Fatalf("validate error=%v stderr=%q", err, stderr)
	}
}

func TestCLIRejectsSyntacticallyMalformedYAML(t *testing.T) {
	cases := []struct {
		name     string
		config   string
		files    map[string]string
		wantFile string
		wantLine string
	}{
		{
			name:     "configuration",
			config:   "title: Site\nbaseURL: https://example.test/\nnavigation: [\n",
			wantFile: "obsite.yaml",
			wantLine: "obsite.yaml:3",
		},
		{
			name:   "frontmatter",
			config: "title: Site\nbaseURL: https://example.test/\nnavigation: []\n",
			files: map[string]string{
				"_index.md":  "---\ntitle: Home\npublish: true\n---\n",
				"article.md": "---\ntitle: [Article\npublish: true\ntype: doc\n---\nArticle\n",
			},
			wantFile: "article.md",
			wantLine: "article.md:2",
		},
	}

	for _, fixture := range cases {
		for _, command := range []string{"validate", "build"} {
			t.Run(fixture.name+"/"+command, func(t *testing.T) {
				vault := t.TempDir()
				writeValidateFile(t, vault, "obsite.yaml", fixture.config)
				for rel, content := range fixture.files {
					writeValidateFile(t, vault, rel, content)
				}

				output := filepath.Join(t.TempDir(), "public")
				const previousOutput = "previous output"
				if command == "build" {
					if err := os.MkdirAll(output, 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(output, "index.html"), []byte(previousOutput), 0o644); err != nil {
						t.Fatal(err)
					}
				}

				args := []string{command, "--vault", vault}
				if command == "build" {
					args = append(args, "--output", output)
				}
				_, stderr, err := executeForTest(t, defaultCommandDependencies(), args)
				if err == nil {
					t.Fatal("command error = nil, want malformed YAML failure")
				}
				for _, want := range []string{"error schema", "yaml:", fixture.wantFile, fixture.wantLine} {
					if !strings.Contains(stderr, want) {
						t.Fatalf("stderr = %q, want %q", stderr, want)
					}
				}

				if command != "build" {
					return
				}
				got, readErr := os.ReadFile(filepath.Join(output, "index.html"))
				if readErr != nil {
					t.Fatalf("read previous output: %v", readErr)
				}
				if string(got) != previousOutput {
					t.Fatalf("previous output = %q, want %q", got, previousOutput)
				}
			})
		}
	}
}

func writeValidateFile(t *testing.T, root, rel, content string) {
	t.Helper()
	filename := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
