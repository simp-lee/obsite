package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStrictBuildStopsBeforePublicationOnSchemaFailure(t *testing.T) {
	vault := t.TempDir()
	output := filepath.Join(t.TempDir(), "public")
	writeValidateFile(t, vault, "obsite.yaml", "title: Site\nbaseURL: https://example.test/\nnavigation: []\ndefaultPublish: true\n")
	writeValidateFile(t, vault, "_index.md", "---\ntitle: Home\npublish: true\n---\n")
	_, stderr, err := executeForTest(t, defaultCommandDependencies(), []string{"build", "--vault", vault, "--output", output, "--strict"})
	if err == nil || !strings.Contains(err.Error(), "defaultPublish") {
		t.Fatalf("strict build error=%v stderr=%q", err, stderr)
	}
	if _, statErr := os.Stat(output); !os.IsNotExist(statErr) {
		t.Fatalf("strict failure changed output: %v", statErr)
	}
}
