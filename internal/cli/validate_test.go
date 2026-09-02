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
