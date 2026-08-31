package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	internalconfig "github.com/simp-lee/obsite/internal/config"
)

func TestInitCommandRequiresVaultFlag(t *testing.T) {
	t.Parallel()
	_, _, err := executeForTest(t, testCommandDependencies(), []string{"init"})
	if err == nil || !strings.Contains(err.Error(), `required flag(s) "vault" not set`) {
		t.Fatalf("executeForTest() error = %v", err)
	}
}

func TestInitCommandWritesStrictBuildableConfig(t *testing.T) {
	t.Parallel()

	vaultPath := filepath.Join(t.TempDir(), "nested", "vault")
	_, _, err := executeForTest(t, testCommandDependencies(), []string{"init", "--vault", vaultPath})
	if err != nil {
		t.Fatalf("executeForTest() error = %v", err)
	}
	configPath := filepath.Join(vaultPath, defaultConfigFilename)
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{"Replace baseURL", "defaultImg: \"\"", "defaultPublish: true", "pageSize: 20", "count: 5", "enabled: true", "path: notes"} {
		if !strings.Contains(content, want) {
			t.Fatalf("generated config missing %q\n%s", want, content)
		}
	}
	for _, forbidden := range []string{"search:", "pagefind", "themes:", "defaultTheme", "templateDir", "themeRoot", "customCSS", "kaTeX", "mermaid"} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("generated config contains %q\n%s", forbidden, content)
		}
	}

	cfg, err := internalconfig.LoadForBuild(vaultPath)
	if err != nil {
		t.Fatalf("config.LoadForBuild() error = %v", err)
	}
	defaults := internalconfig.Defaults()
	if cfg.Title != "My Obsite Site" || cfg.BaseURL != "https://example.com/" || cfg.Language != defaults.Language || cfg.DefaultPublish != defaults.DefaultPublish || cfg.Pagination != defaults.Pagination || cfg.Related != defaults.Related || cfg.RSS != defaults.RSS || cfg.Timeline != defaults.Timeline {
		t.Fatalf("generated config = %#v", cfg)
	}
}

func TestInitCommandRejectsExistingConfigFile(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	configPath := filepath.Join(vaultPath, defaultConfigFilename)
	if err := os.WriteFile(configPath, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := executeForTest(t, testCommandDependencies(), []string{"init", "--vault", vaultPath})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("executeForTest() error = %v", err)
	}
}
