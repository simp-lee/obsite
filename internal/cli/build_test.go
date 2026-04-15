package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	internalbuild "github.com/simp-lee/obsite/internal/build"
	internalconfig "github.com/simp-lee/obsite/internal/config"
	"github.com/simp-lee/obsite/internal/model"
)

func TestBuildCommandRequiresFlags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "missing vault flag",
			args:    []string{"build", "--output", filepath.Join(t.TempDir(), "site")},
			wantErr: `required flag(s) "vault" not set`,
		},
		{
			name:    "missing output flag",
			args:    []string{"build", "--vault", t.TempDir()},
			wantErr: `required flag(s) "output" not set`,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			stdout, stderr, err := executeForTest(t, testCommandDependencies(), tt.args)
			if err == nil {
				t.Fatal("executeForTest() error = nil, want flag validation error")
			}
			if stdout != "" {
				t.Fatalf("stdout = %q, want empty stdout", stdout)
			}
			if stderr != "" {
				t.Fatalf("stderr = %q, want empty stderr", stderr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestBuildCommandUsesDefaultVaultConfigPathAndCallsBuild(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	configPath := filepath.Join(vaultPath, defaultConfigFilename)
	if err := os.WriteFile(configPath, []byte("title: ignored\nbaseURL: https://example.com\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", configPath, err)
	}

	deps := testCommandDependencies()
	var gotConfigPath string
	var gotOverrides internalconfig.Overrides
	var gotVaultPath string
	var gotOutputPath string
	var gotInput internalbuild.SiteInput
	var gotOptions internalbuild.Options
	expectedInput := internalbuild.SiteInput{Config: model.SiteConfig{Title: "Garden Notes", BaseURL: "https://example.com/"}}
	deps.loadSiteInput = func(path string, overrides internalconfig.Overrides) (internalbuild.SiteInput, error) {
		gotConfigPath = path
		gotOverrides = overrides
		return expectedInput, nil
	}
	deps.buildSiteWithOptions = func(input internalbuild.SiteInput, vaultPath string, outputPath string, options internalbuild.Options) (*internalbuild.BuildResult, error) {
		gotInput = input
		gotOptions = options
		gotVaultPath = vaultPath
		gotOutputPath = outputPath
		if options.Force {
			t.Fatalf("build options = %#v, want non-force default build", options)
		}
		return &internalbuild.BuildResult{}, nil
	}

	stdout, stderr, err := executeForTest(t, deps, []string{"build", "--vault", vaultPath, "--output", outputPath})
	if err != nil {
		t.Fatalf("executeForTest() error = %v", err)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty stdout", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty stderr", stderr)
	}
	if gotConfigPath != configPath {
		t.Fatalf("loadConfig path = %q, want %q", gotConfigPath, configPath)
	}
	if gotOverrides.VaultPath != vaultPath {
		t.Fatalf("loadConfig overrides.VaultPath = %q, want %q", gotOverrides.VaultPath, vaultPath)
	}
	if gotVaultPath != vaultPath {
		t.Fatalf("build vaultPath = %q, want %q", gotVaultPath, vaultPath)
	}
	if gotOutputPath != outputPath {
		t.Fatalf("build outputPath = %q, want %q", gotOutputPath, outputPath)
	}
	if gotInput != expectedInput {
		t.Fatalf("build input = %#v, want %#v", gotInput, expectedInput)
	}
	if gotOptions.DiagnosticsWriter == nil {
		t.Fatal("build options DiagnosticsWriter = nil, want injected stderr writer")
	}
}

func TestBuildCommandAllowsConfigOverride(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	overrideConfigPath := filepath.Join(t.TempDir(), "custom.yaml")
	if err := os.WriteFile(overrideConfigPath, []byte("title: ignored\nbaseURL: https://example.com\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", overrideConfigPath, err)
	}

	deps := testCommandDependencies()
	var gotConfigPath string
	var gotOverrides internalconfig.Overrides
	expectedInput := internalbuild.SiteInput{Config: model.SiteConfig{Title: "Garden Notes", BaseURL: "https://example.com/"}}
	deps.loadSiteInput = func(path string, overrides internalconfig.Overrides) (internalbuild.SiteInput, error) {
		gotConfigPath = path
		gotOverrides = overrides
		return expectedInput, nil
	}
	deps.buildSiteWithOptions = func(input internalbuild.SiteInput, vaultPath string, outputPath string, options internalbuild.Options) (*internalbuild.BuildResult, error) {
		if options.Force {
			t.Fatalf("build options = %#v, want non-force config override build", options)
		}
		if input != expectedInput {
			t.Fatalf("build input = %#v, want %#v", input, expectedInput)
		}
		return &internalbuild.BuildResult{}, nil
	}

	_, _, err := executeForTest(t, deps, []string{
		"build",
		"--vault", vaultPath,
		"--output", filepath.Join(t.TempDir(), "site"),
		"--config", overrideConfigPath,
	})
	if err != nil {
		t.Fatalf("executeForTest() error = %v", err)
	}
	if gotConfigPath != overrideConfigPath {
		t.Fatalf("loadConfig path = %q, want %q", gotConfigPath, overrideConfigPath)
	}
	if gotOverrides.VaultPath != vaultPath {
		t.Fatalf("loadConfig overrides.VaultPath = %q, want %q", gotOverrides.VaultPath, vaultPath)
	}
}

func TestBuildCommandFailsWhenDefaultConfigMissing(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()

	stdout, stderr, err := executeForTest(t, testCommandDependencies(), []string{
		"build",
		"--vault", vaultPath,
		"--output", filepath.Join(t.TempDir(), "site"),
	})
	if err == nil {
		t.Fatal("executeForTest() error = nil, want missing config error")
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty stdout", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty stderr", stderr)
	}
	if !strings.Contains(err.Error(), "default config file") {
		t.Fatalf("error = %q, want missing default config message", err.Error())
	}
	if !strings.Contains(err.Error(), "pass --config") {
		t.Fatalf("error = %q, want override guidance", err.Error())
	}
}

func TestBuildCommandReturnsVaultPathErrorBeforeDefaultConfigLookup(t *testing.T) {
	t.Parallel()

	vaultPath := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(vaultPath, []byte("content"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", vaultPath, err)
	}

	stdout, stderr, err := executeForTest(t, testCommandDependencies(), []string{
		"build",
		"--vault", vaultPath,
		"--output", filepath.Join(t.TempDir(), "site"),
	})
	if err == nil {
		t.Fatal("executeForTest() error = nil, want vault path error")
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty stdout", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty stderr", stderr)
	}
	if !strings.Contains(err.Error(), "vault path") || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("error = %q, want vault directory validation message", err.Error())
	}
	if strings.Contains(err.Error(), "default config file") {
		t.Fatalf("error = %q, do not want default config lookup to mask vault path validation", err.Error())
	}
}

func TestBuildCommandPropagatesBuildFailure(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	configPath := filepath.Join(vaultPath, defaultConfigFilename)
	if err := os.WriteFile(configPath, []byte("title: ignored\nbaseURL: https://example.com\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", configPath, err)
	}

	deps := testCommandDependencies()
	deps.loadSiteInput = func(path string, overrides internalconfig.Overrides) (internalbuild.SiteInput, error) {
		return internalbuild.SiteInput{Config: model.SiteConfig{Title: "Garden Notes", BaseURL: "https://example.com/"}}, nil
	}
	deps.buildSiteWithOptions = func(input internalbuild.SiteInput, vaultPath string, outputPath string, options internalbuild.Options) (*internalbuild.BuildResult, error) {
		return nil, errors.New("boom")
	}

	_, _, err := executeForTest(t, deps, []string{
		"build",
		"--vault", vaultPath,
		"--output", filepath.Join(t.TempDir(), "site"),
	})
	if err == nil {
		t.Fatal("executeForTest() error = nil, want build failure")
	}
	if !strings.Contains(err.Error(), "build site: boom") {
		t.Fatalf("error = %q, want wrapped build failure", err.Error())
	}
}

func TestBuildCommandPassesForceOption(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	configPath := filepath.Join(vaultPath, defaultConfigFilename)
	if err := os.WriteFile(configPath, []byte("title: ignored\nbaseURL: https://example.com\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", configPath, err)
	}

	deps := testCommandDependencies()
	deps.loadSiteInput = func(path string, overrides internalconfig.Overrides) (internalbuild.SiteInput, error) {
		return internalbuild.SiteInput{Config: model.SiteConfig{Title: "Garden Notes", BaseURL: "https://example.com/"}}, nil
	}
	gotOptions := internalbuild.Options{}
	deps.buildSiteWithOptions = func(input internalbuild.SiteInput, vaultPath string, outputPath string, options internalbuild.Options) (*internalbuild.BuildResult, error) {
		gotOptions = options
		return &internalbuild.BuildResult{}, nil
	}

	_, _, err := executeForTest(t, deps, []string{
		"build",
		"--vault", vaultPath,
		"--output", outputPath,
		"--force",
	})
	if err != nil {
		t.Fatalf("executeForTest() error = %v", err)
	}
	if !gotOptions.Force {
		t.Fatalf("build options = %#v, want force enabled", gotOptions)
	}
}

func TestBuildCommandRoutesRealBuildWarningsToInjectedStderr(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	configPath := filepath.Join(vaultPath, defaultConfigFilename)
	notePath := filepath.Join(vaultPath, "alpha.md")
	canvasPath := filepath.Join(vaultPath, "sketch.canvas")

	if err := os.WriteFile(configPath, []byte("title: Garden Notes\nbaseURL: https://example.com\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", configPath, err)
	}
	if err := os.WriteFile(notePath, []byte("---\ntitle: Alpha\ndate: 2026-04-10\n---\n# Alpha\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", notePath, err)
	}
	if err := os.WriteFile(canvasPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", canvasPath, err)
	}

	stdout, stderr, err := executeForTest(t, defaultCommandDependencies(), []string{
		"build",
		"--vault", vaultPath,
		"--output", outputPath,
	})
	if err != nil {
		t.Fatalf("executeForTest() error = %v", err)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty stdout", stdout)
	}
	if !strings.Contains(stderr, "Warnings (") {
		t.Fatalf("stderr = %q, want warning summary", stderr)
	}
	if !strings.Contains(stderr, "sketch.canvas [unsupported_syntax] canvas files are skipped during site builds") {
		t.Fatalf("stderr = %q, want canvas warning routed through injected stderr", stderr)
	}
}
