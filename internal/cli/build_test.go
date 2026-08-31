package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	internalbuild "github.com/simp-lee/obsite/internal/build"
	"github.com/simp-lee/obsite/internal/model"
)

func TestBuildCommandRequiresFlags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		args []string
		want string
	}{
		{args: []string{"build", "--output", filepath.Join(t.TempDir(), "site")}, want: `required flag(s) "vault" not set`},
		{args: []string{"build", "--vault", t.TempDir()}, want: `required flag(s) "output" not set`},
	}
	for _, tt := range tests {
		_, _, err := executeForTest(t, testCommandDependencies(), tt.args)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Fatalf("executeForTest() error = %v, want %q", err, tt.want)
		}
	}
}

func TestBuildCommandLoadsOnlyResolvedVaultAndCallsBuild(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	writeCLIConfig(t, vaultPath)

	deps := testCommandDependencies()
	var loadedVault, builtVault, builtOutput string
	expected := internalbuild.SiteInput{Config: model.SiteConfig{Title: "Garden", BaseURL: "https://example.com/"}}
	deps.loadSiteInput = func(vault string) (internalbuild.SiteInput, error) {
		loadedVault = vault
		return expected, nil
	}
	deps.buildSiteWithOptions = func(input internalbuild.SiteInput, vault string, output string, options internalbuild.Options) (*internalbuild.BuildResult, error) {
		builtVault, builtOutput = vault, output
		if options.DiagnosticsWriter == nil || options.Force {
			t.Fatalf("options = %#v", options)
		}
		return &internalbuild.BuildResult{}, nil
	}

	_, _, err := executeForTest(t, deps, []string{"build", "--vault", vaultPath, "--output", outputPath})
	if err != nil {
		t.Fatalf("executeForTest() error = %v", err)
	}
	if loadedVault != vaultPath || builtVault != vaultPath || builtOutput != outputPath {
		t.Fatalf("paths = load %q, build %q -> %q", loadedVault, builtVault, builtOutput)
	}
}

func TestBuildCommandRejectsRemovedConfigAndThemeFlags(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{name: "config", args: []string{"--config", "other.yaml"}, want: "--config has been removed"},
		{name: "theme", args: []string{"--theme", "feature"}, want: "--theme has been removed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			args := append([]string{"build", "--vault", t.TempDir(), "--output", filepath.Join(t.TempDir(), "site")}, tt.args...)
			_, _, err := executeForTest(t, testCommandDependencies(), args)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("executeForTest() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestBuildCommandReturnsVaultPathErrorBeforeConfigRead(t *testing.T) {
	t.Parallel()

	vaultPath := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(vaultPath, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := executeForTest(t, testCommandDependencies(), []string{"build", "--vault", vaultPath, "--output", filepath.Join(t.TempDir(), "site")})
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("executeForTest() error = %v", err)
	}
}

func TestBuildCommandPropagatesBuildFailureAndForce(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	writeCLIConfig(t, vaultPath)
	deps := testCommandDependencies()
	deps.loadSiteInput = func(string) (internalbuild.SiteInput, error) {
		return internalbuild.SiteInput{Config: model.SiteConfig{Title: "Garden", BaseURL: "https://example.com/"}}, nil
	}
	deps.buildSiteWithOptions = func(_ internalbuild.SiteInput, _ string, _ string, options internalbuild.Options) (*internalbuild.BuildResult, error) {
		if !options.Force {
			t.Fatal("Force = false")
		}
		return nil, errors.New("boom")
	}
	_, _, err := executeForTest(t, deps, []string{"build", "--vault", vaultPath, "--output", filepath.Join(t.TempDir(), "site"), "--force"})
	if err == nil || !strings.Contains(err.Error(), "build site: boom") {
		t.Fatalf("executeForTest() error = %v", err)
	}
}

func TestBuildCommandRoutesRealBuildWarningsToInjectedStderr(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	writeCLIConfig(t, vaultPath)
	if err := os.WriteFile(filepath.Join(vaultPath, "alpha.md"), []byte("# Alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vaultPath, "sketch.canvas"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, stderr, err := executeForTest(t, defaultCommandDependencies(), []string{"build", "--vault", vaultPath, "--output", outputPath})
	if err != nil {
		t.Fatalf("executeForTest() error = %v", err)
	}
	if !strings.Contains(stderr, "sketch.canvas [unsupported_syntax]") {
		t.Fatalf("stderr = %q", stderr)
	}
}

func writeCLIConfig(t *testing.T, vault string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(vault, defaultConfigFilename), []byte("title: Garden\nbaseURL: https://example.com/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
