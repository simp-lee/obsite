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

	stdout, stderr, err := executeForTest(t, testCommandDependencies(), []string{"init"})
	if err == nil {
		t.Fatal("executeForTest() error = nil, want missing vault flag error")
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty stdout", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty stderr", stderr)
	}
	if !strings.Contains(err.Error(), `required flag(s) "vault" not set`) {
		t.Fatalf("error = %q, want required vault flag message", err.Error())
	}
}

func TestInitCommandWritesCommentedConfigTemplate(t *testing.T) {
	t.Parallel()

	vaultPath := filepath.Join(t.TempDir(), "nested", "vault")

	stdout, stderr, err := executeForTest(t, testCommandDependencies(), []string{"init", "--vault", vaultPath})
	if err != nil {
		t.Fatalf("executeForTest() error = %v", err)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty stdout", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty stderr", stderr)
	}

	configPath := filepath.Join(vaultPath, defaultConfigFilename)
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error = %v", configPath, err)
	}
	content := string(data)
	expectedPagefindPath := filepath.Join(vaultPath, "tools", "pagefind_extended")
	for _, want := range []string{
		"# baseURL must be the public site URL used for canonical links and sitemap entries.",
		"baseURL: https://example.com/",
		"title: My Obsite Site",
		"author: Your Name",
		"description: Notes published with obsite.",
		"defaultPublish: true",
		"search:",
		"# pagefindPath points to the pagefind_extended executable used during build, relative to this obsite.yaml file.",
		"pagefindPath: tools/pagefind_extended",
		"pagefindVersion: 1.5.2",
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

	loaded, err := internalconfig.LoadForBuild(configPath, internalconfig.Overrides{VaultPath: vaultPath})
	if err != nil {
		t.Fatalf("config.LoadForBuild(%q) error = %v", configPath, err)
	}
	cfg := loaded.Config
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
	if cfg.Search.PagefindPath != expectedPagefindPath || cfg.Search.PagefindVersion != "1.5.2" {
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

func TestInitCommandRejectsExistingConfigFile(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	configPath := filepath.Join(vaultPath, defaultConfigFilename)
	if err := os.WriteFile(configPath, []byte("title: Existing\nbaseURL: https://example.com\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", configPath, err)
	}

	_, _, err := executeForTest(t, testCommandDependencies(), []string{"init", "--vault", vaultPath})
	if err == nil {
		t.Fatal("executeForTest() error = nil, want existing file error")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("error = %q, want existing file message", err.Error())
	}
}
