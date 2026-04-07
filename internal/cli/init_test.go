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
	for _, want := range []string{
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
