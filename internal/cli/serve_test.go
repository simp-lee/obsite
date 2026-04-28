package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	internalbuild "github.com/simp-lee/obsite/internal/build"
	internalconfig "github.com/simp-lee/obsite/internal/config"
	"github.com/simp-lee/obsite/internal/model"
	internalrender "github.com/simp-lee/obsite/internal/render"
)

func TestServeCommandRequiresOutputFlag(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := executeForTest(t, testCommandDependencies(), []string{"serve"})
	if err == nil {
		t.Fatal("executeForTest() error = nil, want missing output flag error")
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty stdout", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty stderr", stderr)
	}
	if !strings.Contains(err.Error(), `required flag(s) "output" not set`) {
		t.Fatalf("error = %q, want required output flag message", err.Error())
	}
}

func TestServeCommandUsesServerDefaultPortWhenOmitted(t *testing.T) {
	t.Parallel()

	outputPath := filepath.Join(t.TempDir(), "site")
	deps := testCommandDependencies()
	server := &fakePreviewServer{}
	var gotOutputPath string
	var gotPort int
	deps.newPreviewServer = func(outputPath string, port int) (previewServer, error) {
		gotOutputPath = outputPath
		gotPort = port
		return server, nil
	}

	_, _, err := executeForTest(t, deps, []string{"serve", "--output", outputPath})
	if err != nil {
		t.Fatalf("executeForTest() error = %v", err)
	}
	if gotOutputPath != outputPath {
		t.Fatalf("newPreviewServer outputPath = %q, want %q", gotOutputPath, outputPath)
	}
	if gotPort != 0 {
		t.Fatalf("newPreviewServer port = %d, want 0 so server.New applies its default", gotPort)
	}
	if server.listenCalls != 1 {
		t.Fatalf("ListenAndServe calls = %d, want 1", server.listenCalls)
	}
}

func TestServeCommandWatchRoutesBuildDiagnosticsAndWatchErrorsToInjectedStderr(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	configPath := filepath.Join(vaultPath, defaultConfigFilename)
	outputPath := filepath.Join(t.TempDir(), "site")
	if err := os.WriteFile(configPath, []byte("title: Garden\nbaseURL: https://example.com\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", configPath, err)
	}

	deps := testCommandDependencies()
	watcher := newFakeFileWatcher()
	listenStarted := make(chan struct{}, 1)
	listenBlock := make(chan struct{})
	server := &fakePreviewServer{listenStarted: listenStarted, listenBlock: listenBlock}
	expectedInput := internalbuild.SiteInput{Config: model.SiteConfig{Title: "Garden", BaseURL: "https://example.com/"}}
	deps.loadSiteInput = func(path string, overrides internalconfig.Overrides) (internalbuild.SiteInput, error) {
		return expectedInput, nil
	}
	deps.buildSiteWithOptions = func(input internalbuild.SiteInput, vaultPath string, outputPath string, options internalbuild.Options) (*internalbuild.BuildResult, error) {
		if options.DiagnosticsWriter == nil {
			t.Fatal("build options DiagnosticsWriter = nil, want injected stderr writer")
		}
		if !reflect.DeepEqual(input, expectedInput) {
			t.Fatalf("build input = %#v, want %#v", input, expectedInput)
		}
		if _, err := options.DiagnosticsWriter.Write([]byte("Warnings (1):\n- build [structured_data] synthetic build warning\n")); err != nil {
			t.Fatalf("DiagnosticsWriter.Write() error = %v", err)
		}
		return &internalbuild.BuildResult{}, nil
	}
	deps.newPreviewServer = func(outputPath string, port int) (previewServer, error) {
		return server, nil
	}
	deps.newFileWatcher = func() (fileWatcher, error) {
		return watcher, nil
	}

	var stdoutBuf lockedBuffer
	var stderrBuf lockedBuffer
	errCh := make(chan error, 1)
	go func() {
		errCh <- executeWithDeps([]string{"serve", "--output", outputPath, "--watch", "--vault", vaultPath, "--config", configPath}, deps, &stdoutBuf, &stderrBuf)
	}()

	select {
	case <-listenStarted:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("timed out waiting for preview server to start listening")
	}
	waitForServeWatchAddCount(t, watcher, vaultPath, 1)

	watcher.errors <- errors.New("boom")
	waitForLockedBufferContains(t, &stderrBuf, "watch: watcher error: boom")
	close(listenBlock)

	err := <-errCh
	if err != nil {
		t.Fatalf("executeWithDeps() error = %v", err)
	}
	if got := stdoutBuf.String(); got != "" {
		t.Fatalf("stdout = %q, want empty stdout", got)
	}
	stderr := stderrBuf.String()
	if !strings.Contains(stderr, "synthetic build warning") {
		t.Fatalf("stderr = %q, want build diagnostics routed through injected stderr", stderr)
	}
	if !strings.Contains(stderr, "watch: watcher error: boom") {
		t.Fatalf("stderr = %q, want watch error prefix routed through injected stderr", stderr)
	}
	if strings.Count(stderr, "watch: watcher error: boom") != 1 {
		t.Fatalf("stderr = %q, want exactly one watch error entry", stderr)
	}
	if server.enableCalls != 1 {
		t.Fatalf("EnableLiveReload calls = %d, want 1 in watch mode", server.enableCalls)
	}
	if server.listenCalls != 1 {
		t.Fatalf("ListenAndServe calls = %d, want 1", server.listenCalls)
	}
}

func TestServeCommandPassesExplicitPortToPreviewServer(t *testing.T) {
	t.Parallel()

	outputPath := filepath.Join(t.TempDir(), "site")
	deps := testCommandDependencies()
	server := &fakePreviewServer{}
	var gotOutputPath string
	var gotPort int
	deps.newPreviewServer = func(outputPath string, port int) (previewServer, error) {
		gotOutputPath = outputPath
		gotPort = port
		return server, nil
	}

	stdout, stderr, err := executeForTest(t, deps, []string{"serve", "--output", outputPath, "--port", "9090"})
	if err != nil {
		t.Fatalf("executeForTest() error = %v", err)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty stdout", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty stderr", stderr)
	}
	if gotOutputPath != outputPath {
		t.Fatalf("newPreviewServer outputPath = %q, want %q", gotOutputPath, outputPath)
	}
	if gotPort != 9090 {
		t.Fatalf("newPreviewServer port = %d, want %d", gotPort, 9090)
	}
	if server.listenCalls != 1 {
		t.Fatalf("ListenAndServe calls = %d, want 1", server.listenCalls)
	}
}

func TestServeCommandPropagatesListenFailure(t *testing.T) {
	t.Parallel()

	deps := testCommandDependencies()
	server := &fakePreviewServer{listenErr: errors.New("bind failed")}
	deps.newPreviewServer = func(outputPath string, port int) (previewServer, error) {
		return server, nil
	}

	_, _, err := executeForTest(t, deps, []string{"serve", "--output", filepath.Join(t.TempDir(), "site"), "--port", "9090"})
	if err == nil {
		t.Fatal("executeForTest() error = nil, want listen failure")
	}
	if !strings.Contains(err.Error(), "listen and serve: bind failed") {
		t.Fatalf("error = %q, want wrapped listen failure", err.Error())
	}
	if server.listenCalls != 1 {
		t.Fatalf("ListenAndServe calls = %d, want 1", server.listenCalls)
	}
}

func TestServeCommandDoesNotEnableLiveReloadWithoutWatch(t *testing.T) {
	t.Parallel()

	deps := testCommandDependencies()
	server := &fakePreviewServer{}
	deps.newPreviewServer = func(outputPath string, port int) (previewServer, error) {
		return server, nil
	}

	_, _, err := executeForTest(t, deps, []string{"serve", "--output", filepath.Join(t.TempDir(), "site")})
	if err != nil {
		t.Fatalf("executeForTest() error = %v", err)
	}
	if server.enableCalls != 0 {
		t.Fatalf("EnableLiveReload calls = %d, want 0 without --watch", server.enableCalls)
	}
}

func TestServeCommandWatchRequiresVaultFlag(t *testing.T) {
	t.Parallel()

	stdOut, stdErr, err := executeForTest(t, testCommandDependencies(), []string{"serve", "--output", filepath.Join(t.TempDir(), "site"), "--watch"})
	if err == nil {
		t.Fatal("executeForTest() error = nil, want missing watch vault error")
	}
	if stdOut != "" {
		t.Fatalf("stdout = %q, want empty stdout", stdOut)
	}
	if stdErr != "" {
		t.Fatalf("stderr = %q, want empty stderr", stdErr)
	}
	if err.Error() != "--vault is required when --watch is enabled" {
		t.Fatalf("error = %q, want watch vault guidance", err.Error())
	}
}

func TestServeCommandRejectsThemeWithoutWatch(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := executeForTest(t, testCommandDependencies(), []string{
		"serve",
		"--output", filepath.Join(t.TempDir(), "site"),
		"--theme", "feature",
	})
	if err == nil {
		t.Fatal("executeForTest() error = nil, want invalid --theme combination")
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty stdout", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty stderr", stderr)
	}
	if err.Error() != "--theme can only be used together with --watch" {
		t.Fatalf("error = %q, want explicit --theme watch-only guidance", err.Error())
	}
}

func TestServeCommandWatchBuildsBeforeServing(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "obsite.yaml")
	themeRoot := filepath.Join(configDir, "themes", "feature")
	extraWatchFilePath := filepath.Join(t.TempDir(), "watch-inputs", "explicit-input.txt")
	if err := os.WriteFile(configPath, []byte("title: ignored\nbaseURL: https://example.com\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", configPath, err)
	}
	if err := os.MkdirAll(themeRoot, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v", themeRoot, err)
	}
	if err := os.MkdirAll(filepath.Dir(extraWatchFilePath), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v", filepath.Dir(extraWatchFilePath), err)
	}
	if err := os.WriteFile(extraWatchFilePath, []byte("watch input\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", extraWatchFilePath, err)
	}
	outputPath := filepath.Join(t.TempDir(), "site")

	deps := testCommandDependencies()
	server := &fakePreviewServer{}
	watcher := newFakeFileWatcher()
	var gotConfigPath string
	var gotOverrides internalconfig.Overrides
	var gotVaultPath string
	var gotOutputPath string
	expectedInput := internalbuild.SiteInput{Config: model.SiteConfig{Title: "Garden", BaseURL: "https://example.com/", ThemeRoot: themeRoot, CustomCSS: extraWatchFilePath}}
	deps.loadSiteInput = func(path string, overrides internalconfig.Overrides) (internalbuild.SiteInput, error) {
		gotConfigPath = path
		gotOverrides = overrides
		return expectedInput, nil
	}
	deps.buildSiteWithOptions = func(input internalbuild.SiteInput, vaultPath string, outputPath string, options internalbuild.Options) (*internalbuild.BuildResult, error) {
		gotVaultPath = vaultPath
		gotOutputPath = outputPath
		if options.DiagnosticsWriter == nil {
			t.Fatal("build options DiagnosticsWriter = nil, want injected stderr writer")
		}
		if !reflect.DeepEqual(input, expectedInput) {
			t.Fatalf("build input = %#v, want %#v", input, expectedInput)
		}
		if err := os.MkdirAll(outputPath, 0o755); err != nil {
			return nil, err
		}
		return &internalbuild.BuildResult{}, nil
	}
	deps.newPreviewServer = func(outputPath string, port int) (previewServer, error) {
		return server, nil
	}
	deps.newFileWatcher = func() (fileWatcher, error) {
		return watcher, nil
	}

	stdout, stderr, err := executeForTest(t, deps, []string{"serve", "--output", outputPath, "--watch", "--vault", vaultPath, "--config", configPath, "--theme", "feature"})
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
	if gotOverrides.Theme != "feature" {
		t.Fatalf("loadConfig overrides.Theme = %q, want %q", gotOverrides.Theme, "feature")
	}
	if gotVaultPath != vaultPath {
		t.Fatalf("build vaultPath = %q, want %q", gotVaultPath, vaultPath)
	}
	if gotOutputPath != outputPath {
		t.Fatalf("build outputPath = %q, want %q", gotOutputPath, outputPath)
	}
	if server.enableCalls != 1 {
		t.Fatalf("EnableLiveReload calls = %d, want 1 in watch mode", server.enableCalls)
	}
	waitForServeWatchAddCount(t, watcher, themeRoot, 1)
	waitForServeWatchAddCount(t, watcher, filepath.Dir(extraWatchFilePath), 1)
	if server.listenCalls != 1 {
		t.Fatalf("ListenAndServe calls = %d, want 1", server.listenCalls)
	}
}

func TestServeCommandWatchUsesThemeOverrideOnRebuild(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	configPath := filepath.Join(vaultPath, defaultConfigFilename)
	outputPath := filepath.Join(t.TempDir(), "site")
	if err := os.WriteFile(configPath, []byte("title: ignored\nbaseURL: https://example.com\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", configPath, err)
	}

	deps := testCommandDependencies()
	watcher := newFakeFileWatcher()
	listenStarted := make(chan struct{}, 1)
	listenBlock := make(chan struct{})
	server := &fakePreviewServer{listenStarted: listenStarted, listenBlock: listenBlock}
	buildSignal := make(chan struct{}, 4)
	var overridesMu sync.Mutex
	gotOverrides := make([]internalconfig.Overrides, 0, 2)
	expectedInput := internalbuild.SiteInput{Config: model.SiteConfig{Title: "Garden", BaseURL: "https://example.com/"}}

	deps.loadSiteInput = func(path string, overrides internalconfig.Overrides) (internalbuild.SiteInput, error) {
		overridesMu.Lock()
		gotOverrides = append(gotOverrides, overrides)
		overridesMu.Unlock()
		return expectedInput, nil
	}
	deps.buildSiteWithOptions = func(input internalbuild.SiteInput, vaultPath string, outputPath string, options internalbuild.Options) (*internalbuild.BuildResult, error) {
		if !reflect.DeepEqual(input, expectedInput) {
			t.Fatalf("build input = %#v, want %#v", input, expectedInput)
		}
		if err := os.MkdirAll(outputPath, 0o755); err != nil {
			return nil, err
		}
		buildSignal <- struct{}{}
		return &internalbuild.BuildResult{}, nil
	}
	deps.newPreviewServer = func(outputPath string, port int) (previewServer, error) {
		return server, nil
	}
	deps.newFileWatcher = func() (fileWatcher, error) {
		return watcher, nil
	}

	var stdoutBuf lockedBuffer
	var stderrBuf lockedBuffer
	errCh := make(chan error, 1)
	go func() {
		errCh <- executeWithDeps([]string{"serve", "--output", outputPath, "--watch", "--vault", vaultPath, "--config", configPath, "--theme", "feature"}, deps, &stdoutBuf, &stderrBuf)
	}()

	select {
	case <-listenStarted:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("timed out waiting for preview server to start listening")
	}
	waitForServeWatchSignal(t, buildSignal, "initial build")
	waitForServeWatchAddCount(t, watcher, vaultPath, 1)

	watcher.send(fsnotify.Event{Name: configPath, Op: fsnotify.Write})
	waitForServeWatchSignalWithin(t, buildSignal, "config rebuild", 900*time.Millisecond)

	overridesMu.Lock()
	overrides := append([]internalconfig.Overrides(nil), gotOverrides...)
	overridesMu.Unlock()
	if len(overrides) < 2 {
		t.Fatalf("loadSiteInput override calls = %d, want at least %d", len(overrides), 2)
	}
	for index, overrides := range overrides[:2] {
		if overrides.VaultPath != vaultPath {
			t.Fatalf("loadSiteInput call %d overrides.VaultPath = %q, want %q", index, overrides.VaultPath, vaultPath)
		}
		if overrides.Theme != "feature" {
			t.Fatalf("loadSiteInput call %d overrides.Theme = %q, want %q", index, overrides.Theme, "feature")
		}
	}

	close(listenBlock)
	if err := <-errCh; err != nil {
		t.Fatalf("executeWithDeps() error = %v", err)
	}
	if got := stdoutBuf.String(); got != "" {
		t.Fatalf("stdout = %q, want empty stdout", got)
	}
	if got := stderrBuf.String(); got != "" {
		t.Fatalf("stderr = %q, want empty stderr", got)
	}
}

func TestServeCommandWatchRecoversAfterConfigSelectsMissingThemeRoot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		hiddenInVault bool
	}{
		{name: "missing external selected theme root"},
		{name: "missing hidden selected theme root", hiddenInVault: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			vaultPath := t.TempDir()
			configPath := filepath.Join(vaultPath, defaultConfigFilename)
			outputPath := filepath.Join(t.TempDir(), "site")
			initialThemeRoot := filepath.Join(t.TempDir(), "themes-initial", "feature")
			writeServeWatchThemeTemplates(t, initialThemeRoot)

			var nextThemeRootValue string
			var nextThemeRootPath string
			var recoveryEventDirs []string
			var recoveryWatchDir string
			if tt.hiddenInVault {
				nextThemeRootValue = filepath.ToSlash(filepath.Join(".obsidian", "themes", "experiments", "feature"))
				nextThemeRootPath = filepath.Join(vaultPath, filepath.FromSlash(nextThemeRootValue))
				recoveryEventDirs = []string{
					filepath.Join(vaultPath, ".obsidian"),
					filepath.Join(vaultPath, ".obsidian", "themes"),
					filepath.Join(vaultPath, ".obsidian", "themes", "experiments"),
				}
			} else {
				recoveryWatchDir = t.TempDir()
				nextThemeRootPath = filepath.Join(recoveryWatchDir, "themes-next", "nested", "feature")
				nextThemeRootValue = nextThemeRootPath
				recoveryEventDirs = []string{
					filepath.Join(recoveryWatchDir, "themes-next"),
					filepath.Join(recoveryWatchDir, "themes-next", "nested"),
				}
			}

			writeServeWatchThemeConfig(t, configPath, initialThemeRoot)

			deps := testCommandDependencies()
			deps.loadSiteInput = internalbuild.LoadSiteInput

			watcher := newFakeFileWatcher()
			listenStarted := make(chan struct{}, 1)
			listenBlock := make(chan struct{})
			server := &fakePreviewServer{listenStarted: listenStarted, listenBlock: listenBlock}
			buildSignal := make(chan struct{}, 4)
			var builtThemeRootsMu sync.Mutex
			builtThemeRoots := make([]string, 0, 2)
			deps.buildSiteWithOptions = func(input internalbuild.SiteInput, vaultPath string, outputPath string, options internalbuild.Options) (*internalbuild.BuildResult, error) {
				builtThemeRootsMu.Lock()
				builtThemeRoots = append(builtThemeRoots, input.Config.ThemeRoot)
				builtThemeRootsMu.Unlock()
				if err := os.MkdirAll(outputPath, 0o755); err != nil {
					return nil, err
				}
				buildSignal <- struct{}{}
				return &internalbuild.BuildResult{}, nil
			}
			deps.newPreviewServer = func(outputPath string, port int) (previewServer, error) {
				return server, nil
			}
			deps.newFileWatcher = func() (fileWatcher, error) {
				return watcher, nil
			}

			var stdoutBuf lockedBuffer
			var stderrBuf lockedBuffer
			errCh := make(chan error, 1)
			go func() {
				errCh <- executeWithDeps([]string{"serve", "--output", outputPath, "--watch", "--vault", vaultPath, "--config", configPath}, deps, &stdoutBuf, &stderrBuf)
			}()

			select {
			case <-listenStarted:
			case <-time.After(250 * time.Millisecond):
				t.Fatal("timed out waiting for preview server to start listening")
			}
			waitForServeWatchSignal(t, buildSignal, "initial build")
			waitForServeWatchAddCount(t, watcher, vaultPath, 1)
			waitForServeWatchAddCount(t, watcher, initialThemeRoot, 1)

			writeServeWatchThemeConfig(t, configPath, nextThemeRootValue)
			watcher.send(fsnotify.Event{Name: configPath, Op: fsnotify.Write})
			waitForLockedBufferContainsWithin(t, &stderrBuf, "watch: load config:", 900*time.Millisecond)
			if recoveryWatchDir != "" {
				waitForServeWatchAddCount(t, watcher, recoveryWatchDir, 1)
			}
			waitForServeWatchRemoveCount(t, watcher, initialThemeRoot, 1)
			select {
			case <-buildSignal:
				t.Fatal("missing selected theme root config unexpectedly completed a rebuild")
			default:
			}

			for _, dirPath := range recoveryEventDirs {
				mkdirServeWatchDir(t, dirPath)
				sendServeWatchCreateFromWatchedParent(t, watcher, dirPath)
				waitForServeWatchAddCount(t, watcher, dirPath, 1)
			}

			mkdirServeWatchDir(t, nextThemeRootPath)
			writeServeWatchThemeTemplates(t, nextThemeRootPath)
			sendServeWatchCreateFromWatchedParent(t, watcher, nextThemeRootPath)
			waitForServeWatchAddCount(t, watcher, nextThemeRootPath, 1)
			waitForServeWatchSignalWithin(t, buildSignal, "recovered selected theme root rebuild", 900*time.Millisecond)

			builtThemeRootsMu.Lock()
			gotBuiltThemeRoots := append([]string(nil), builtThemeRoots...)
			builtThemeRootsMu.Unlock()
			if len(gotBuiltThemeRoots) != 2 {
				t.Fatalf("successful builds used theme roots %#v, want initial and recovered roots", gotBuiltThemeRoots)
			}
			if gotBuiltThemeRoots[0] != initialThemeRoot {
				t.Fatalf("initial build theme root = %q, want %q", gotBuiltThemeRoots[0], initialThemeRoot)
			}
			if gotBuiltThemeRoots[1] != nextThemeRootPath {
				t.Fatalf("recovered build theme root = %q, want %q", gotBuiltThemeRoots[1], nextThemeRootPath)
			}

			close(listenBlock)
			if err := <-errCh; err != nil {
				t.Fatalf("executeWithDeps() error = %v", err)
			}
			if got := stdoutBuf.String(); got != "" {
				t.Fatalf("stdout = %q, want empty stdout", got)
			}
		})
	}
}

func TestServeCommandWatchRecoversAfterOneShotDeepRecreationOfSelectedThemeRoot(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	configPath := filepath.Join(vaultPath, defaultConfigFilename)
	outputPath := filepath.Join(t.TempDir(), "site")
	initialThemeRoot := filepath.Join(t.TempDir(), "themes-initial", "feature")
	writeServeWatchThemeTemplates(t, initialThemeRoot)

	recoveryWatchDir := t.TempDir()
	nextThemeRootPath := filepath.Join(recoveryWatchDir, "themes-next", "nested", "feature")
	brokenThemeTopDir := filepath.Join(recoveryWatchDir, "themes-next")
	brokenThemeParent := filepath.Join(recoveryWatchDir, "themes-next", "nested")
	writeServeWatchThemeConfig(t, configPath, initialThemeRoot)

	deps := testCommandDependencies()
	deps.loadSiteInput = internalbuild.LoadSiteInput

	watcher := newFakeFileWatcher()
	listenStarted := make(chan struct{}, 1)
	listenBlock := make(chan struct{})
	server := &fakePreviewServer{listenStarted: listenStarted, listenBlock: listenBlock}
	buildSignal := make(chan struct{}, 4)
	var builtThemeRootsMu sync.Mutex
	builtThemeRoots := make([]string, 0, 2)
	deps.buildSiteWithOptions = func(input internalbuild.SiteInput, vaultPath string, outputPath string, options internalbuild.Options) (*internalbuild.BuildResult, error) {
		builtThemeRootsMu.Lock()
		builtThemeRoots = append(builtThemeRoots, input.Config.ThemeRoot)
		builtThemeRootsMu.Unlock()
		if err := os.MkdirAll(outputPath, 0o755); err != nil {
			return nil, err
		}
		buildSignal <- struct{}{}
		return &internalbuild.BuildResult{}, nil
	}
	deps.newPreviewServer = func(outputPath string, port int) (previewServer, error) {
		return server, nil
	}
	deps.newFileWatcher = func() (fileWatcher, error) {
		return watcher, nil
	}

	var stdoutBuf lockedBuffer
	var stderrBuf lockedBuffer
	errCh := make(chan error, 1)
	go func() {
		errCh <- executeWithDeps([]string{"serve", "--output", outputPath, "--watch", "--vault", vaultPath, "--config", configPath}, deps, &stdoutBuf, &stderrBuf)
	}()

	select {
	case <-listenStarted:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("timed out waiting for preview server to start listening")
	}
	waitForServeWatchSignal(t, buildSignal, "initial build")
	waitForServeWatchAddCount(t, watcher, vaultPath, 1)
	waitForServeWatchAddCount(t, watcher, initialThemeRoot, 1)

	writeServeWatchThemeConfig(t, configPath, nextThemeRootPath)
	watcher.send(fsnotify.Event{Name: configPath, Op: fsnotify.Write})
	waitForLockedBufferContainsWithin(t, &stderrBuf, "watch: load config:", 900*time.Millisecond)
	waitForServeWatchAddCount(t, watcher, recoveryWatchDir, 1)
	waitForServeWatchRemoveCount(t, watcher, initialThemeRoot, 1)
	select {
	case <-buildSignal:
		t.Fatal("missing selected theme root config unexpectedly completed a rebuild")
	default:
	}

	writeServeWatchThemeTemplates(t, nextThemeRootPath)
	sendServeWatchCreateFromWatchedParent(t, watcher, brokenThemeTopDir)
	waitForServeWatchAddCount(t, watcher, brokenThemeParent, 1)
	waitForServeWatchAddCount(t, watcher, nextThemeRootPath, 1)
	waitForServeWatchSignalWithin(t, buildSignal, "one-shot recovered selected theme root rebuild", 900*time.Millisecond)

	builtThemeRootsMu.Lock()
	gotBuiltThemeRoots := append([]string(nil), builtThemeRoots...)
	builtThemeRootsMu.Unlock()
	if len(gotBuiltThemeRoots) != 2 {
		t.Fatalf("successful builds used theme roots %#v, want initial and recovered roots", gotBuiltThemeRoots)
	}
	if gotBuiltThemeRoots[0] != initialThemeRoot {
		t.Fatalf("initial build theme root = %q, want %q", gotBuiltThemeRoots[0], initialThemeRoot)
	}
	if gotBuiltThemeRoots[1] != nextThemeRootPath {
		t.Fatalf("recovered build theme root = %q, want %q", gotBuiltThemeRoots[1], nextThemeRootPath)
	}

	close(listenBlock)
	if err := <-errCh; err != nil {
		t.Fatalf("executeWithDeps() error = %v", err)
	}
	if got := stdoutBuf.String(); got != "" {
		t.Fatalf("stdout = %q, want empty stdout", got)
	}
	if got := stderrBuf.String(); strings.Count(got, "watch: load config:") != 1 {
		t.Fatalf("stderr = %q, want exactly one missing-theme-root diagnostic", got)
	}
}

func TestStartServeWatchLoopDebouncesRebuildsAndNotifiesReload(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	notePath := filepath.Join(vaultPath, "notes", "alpha.md")
	configPath := filepath.Join(vaultPath, defaultConfigFilename)
	outputPath := filepath.Join(vaultPath, "public")
	for _, filePath := range []string{notePath, configPath} {
		if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
			t.Fatalf("os.MkdirAll(%q) error = %v", filepath.Dir(filePath), err)
		}
		if err := os.WriteFile(filePath, []byte("content"), 0o644); err != nil {
			t.Fatalf("os.WriteFile(%q) error = %v", filePath, err)
		}
	}
	if err := os.MkdirAll(outputPath, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v", outputPath, err)
	}

	watcher := newFakeFileWatcher()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var reloadCalls atomic.Int32
	rebuildSignal := make(chan struct{}, 4)
	errorSignal := make(chan error, 2)
	if err := startServeWatchLoop(ctx, serveWatchLoop{
		watcher:    watcher,
		vaultPath:  vaultPath,
		outputPath: outputPath,
		configPath: configPath,
		debounce:   15 * time.Millisecond,
		rebuild: func() error {
			rebuildSignal <- struct{}{}
			return nil
		},
		notifyReload: func() {
			reloadCalls.Add(1)
		},
		onError: func(err error) {
			errorSignal <- err
		},
	}); err != nil {
		t.Fatalf("startServeWatchLoop() error = %v", err)
	}

	watcher.send(fsnotify.Event{Name: notePath, Op: fsnotify.Write})
	watcher.send(fsnotify.Event{Name: notePath, Op: fsnotify.Write})
	waitForServeWatchSignal(t, rebuildSignal, "vault rebuild")
	select {
	case <-rebuildSignal:
		t.Fatal("received unexpected second rebuild for debounced vault writes")
	case err := <-errorSignal:
		t.Fatalf("watch loop reported error: %v", err)
	case <-time.After(60 * time.Millisecond):
	}
	if got := reloadCalls.Load(); got != 1 {
		t.Fatalf("NotifyReload calls = %d, want 1", got)
	}

	watcher.send(fsnotify.Event{Name: filepath.Join(outputPath, "index.html"), Op: fsnotify.Write})
	select {
	case <-rebuildSignal:
		t.Fatal("output-path change triggered rebuild, want ignored output updates")
	case err := <-errorSignal:
		t.Fatalf("watch loop reported error: %v", err)
	case <-time.After(40 * time.Millisecond):
	}

	watcher.send(fsnotify.Event{Name: configPath, Op: fsnotify.Write})
	waitForServeWatchSignal(t, rebuildSignal, "config rebuild")
	if got := reloadCalls.Load(); got != 2 {
		t.Fatalf("NotifyReload calls after config change = %d, want 2", got)
	}
}

func TestStartServeWatchLoopReaddsRemovedOrRenamedDirectories(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		op            fsnotify.Op
		removeWatched func(t *testing.T, watchedDir string)
	}{
		{
			name: "remove",
			op:   fsnotify.Remove,
			removeWatched: func(t *testing.T, watchedDir string) {
				t.Helper()
				if err := os.RemoveAll(watchedDir); err != nil {
					t.Fatalf("os.RemoveAll(%q) error = %v", watchedDir, err)
				}
			},
		},
		{
			name: "rename",
			op:   fsnotify.Rename,
			removeWatched: func(t *testing.T, watchedDir string) {
				t.Helper()
				renamedPath := watchedDir + "-renamed"
				if err := os.Rename(watchedDir, renamedPath); err != nil {
					t.Fatalf("os.Rename(%q, %q) error = %v", watchedDir, renamedPath, err)
				}
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			vaultPath := t.TempDir()
			watchedDir := filepath.Join(vaultPath, "notes")
			nestedDir := filepath.Join(watchedDir, "alpha")
			notePath := filepath.Join(nestedDir, "guide.md")
			configPath := filepath.Join(vaultPath, defaultConfigFilename)
			outputPath := filepath.Join(vaultPath, "public")
			for _, filePath := range []string{notePath, configPath} {
				if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
					t.Fatalf("os.MkdirAll(%q) error = %v", filepath.Dir(filePath), err)
				}
				if err := os.WriteFile(filePath, []byte("content"), 0o644); err != nil {
					t.Fatalf("os.WriteFile(%q) error = %v", filePath, err)
				}
			}
			if err := os.MkdirAll(outputPath, 0o755); err != nil {
				t.Fatalf("os.MkdirAll(%q) error = %v", outputPath, err)
			}

			watcher := newFakeFileWatcher()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			rebuildSignal := make(chan struct{}, 6)
			errorSignal := make(chan error, 4)
			if err := startServeWatchLoop(ctx, serveWatchLoop{
				watcher:    watcher,
				vaultPath:  vaultPath,
				outputPath: outputPath,
				configPath: configPath,
				debounce:   15 * time.Millisecond,
				rebuild: func() error {
					rebuildSignal <- struct{}{}
					return nil
				},
				onError: func(err error) {
					errorSignal <- err
				},
			}); err != nil {
				t.Fatalf("startServeWatchLoop() error = %v", err)
			}

			waitForServeWatchAddCount(t, watcher, watchedDir, 1)
			waitForServeWatchAddCount(t, watcher, nestedDir, 1)
			watcher.setRemoveErr(watchedDir, fsnotify.ErrNonExistentWatch)
			watcher.setRemoveErr(nestedDir, fsnotify.ErrNonExistentWatch)

			tt.removeWatched(t, watchedDir)
			watcher.send(fsnotify.Event{Name: watchedDir, Op: tt.op})
			waitForServeWatchRemoveCount(t, watcher, watchedDir, 1)
			waitForServeWatchRemoveCount(t, watcher, nestedDir, 1)

			if err := os.MkdirAll(nestedDir, 0o755); err != nil {
				t.Fatalf("os.MkdirAll(%q) error = %v", nestedDir, err)
			}
			if err := os.WriteFile(notePath, []byte("updated"), 0o644); err != nil {
				t.Fatalf("os.WriteFile(%q) error = %v", notePath, err)
			}

			watcher.send(fsnotify.Event{Name: watchedDir, Op: fsnotify.Create})
			waitForServeWatchAddCount(t, watcher, watchedDir, 2)
			waitForServeWatchAddCount(t, watcher, nestedDir, 2)

			drainServeWatchSignals(rebuildSignal)
			watcher.send(fsnotify.Event{Name: notePath, Op: fsnotify.Write})
			waitForServeWatchSignal(t, rebuildSignal, "recreated directory rebuild")
			assertNoServeWatchError(t, errorSignal)
		})
	}
}

func TestStartServeWatchLoopTreatsMissingPathChmodAsRemove(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	notePath := filepath.Join(vaultPath, "notes", "guide.md")
	configPath := filepath.Join(vaultPath, defaultConfigFilename)
	outputPath := filepath.Join(vaultPath, "public")
	for _, filePath := range []string{notePath, configPath} {
		if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
			t.Fatalf("os.MkdirAll(%q) error = %v", filepath.Dir(filePath), err)
		}
		if err := os.WriteFile(filePath, []byte("content"), 0o644); err != nil {
			t.Fatalf("os.WriteFile(%q) error = %v", filePath, err)
		}
	}
	if err := os.MkdirAll(outputPath, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v", outputPath, err)
	}

	watcher := newFakeFileWatcher()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rebuildSignal := make(chan struct{}, 4)
	errorSignal := make(chan error, 4)
	if err := startServeWatchLoop(ctx, serveWatchLoop{
		watcher:    watcher,
		vaultPath:  vaultPath,
		outputPath: outputPath,
		configPath: configPath,
		debounce:   15 * time.Millisecond,
		rebuild: func() error {
			rebuildSignal <- struct{}{}
			return nil
		},
		onError: func(err error) {
			errorSignal <- err
		},
	}); err != nil {
		t.Fatalf("startServeWatchLoop() error = %v", err)
	}

	watcher.send(fsnotify.Event{Name: notePath, Op: fsnotify.Chmod})
	assertNoServeWatchSignal(t, rebuildSignal, errorSignal, "chmod on existing markdown input")

	if err := os.Remove(notePath); err != nil {
		t.Fatalf("os.Remove(%q) error = %v", notePath, err)
	}
	watcher.send(fsnotify.Event{Name: notePath, Op: fsnotify.Chmod})
	waitForServeWatchSignal(t, rebuildSignal, "missing-path chmod rebuild")
	assertNoServeWatchError(t, errorSignal)
}

func TestStartServeWatchLoopFiltersNonBuildOpsAndHiddenFiles(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	notePath := filepath.Join(vaultPath, "notes", "guide.md")
	yamlPath := filepath.Join(vaultPath, "notes", "frontmatter.yaml")
	imagePath := filepath.Join(vaultPath, "attachments", "hero.png")
	hiddenPath := filepath.Join(vaultPath, ".hidden", "scratch.txt")
	configPath := filepath.Join(vaultPath, defaultConfigFilename)
	outputPath := filepath.Join(vaultPath, "public")
	for _, filePath := range []string{notePath, yamlPath, imagePath, hiddenPath, configPath} {
		if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
			t.Fatalf("os.MkdirAll(%q) error = %v", filepath.Dir(filePath), err)
		}
		if err := os.WriteFile(filePath, []byte("content"), 0o644); err != nil {
			t.Fatalf("os.WriteFile(%q) error = %v", filePath, err)
		}
	}
	if err := os.MkdirAll(outputPath, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v", outputPath, err)
	}

	watcher := newFakeFileWatcher()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rebuildSignal := make(chan struct{}, 6)
	errorSignal := make(chan error, 4)
	if err := startServeWatchLoop(ctx, serveWatchLoop{
		watcher:    watcher,
		vaultPath:  vaultPath,
		outputPath: outputPath,
		configPath: configPath,
		debounce:   15 * time.Millisecond,
		rebuild: func() error {
			rebuildSignal <- struct{}{}
			return nil
		},
		onError: func(err error) {
			errorSignal <- err
		},
	}); err != nil {
		t.Fatalf("startServeWatchLoop() error = %v", err)
	}

	watcher.send(fsnotify.Event{Name: notePath, Op: fsnotify.Chmod})
	assertNoServeWatchSignal(t, rebuildSignal, errorSignal, "chmod on existing markdown input")

	watcher.send(fsnotify.Event{Name: hiddenPath, Op: fsnotify.Write})
	assertNoServeWatchSignal(t, rebuildSignal, errorSignal, "hidden file")

	watcher.send(fsnotify.Event{Name: yamlPath, Op: fsnotify.Write})
	waitForServeWatchSignal(t, rebuildSignal, "yaml rebuild")
	assertNoServeWatchError(t, errorSignal)

	watcher.send(fsnotify.Event{Name: imagePath, Op: fsnotify.Write})
	waitForServeWatchSignal(t, rebuildSignal, "image rebuild")
	assertNoServeWatchError(t, errorSignal)
}

func TestStartServeWatchLoopRebuildsForAttachmentsVaultCustomCSSAndExtraWatchFile(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	attachmentPath := filepath.Join(vaultPath, "files", "manual.pdf")
	customCSSPath := filepath.Join(vaultPath, "custom.css")
	extraWatchFilePath := filepath.Join(t.TempDir(), "watch-inputs", "explicit-input.txt")
	configPath := filepath.Join(vaultPath, defaultConfigFilename)
	outputPath := filepath.Join(vaultPath, "public")
	for _, filePath := range []string{attachmentPath, customCSSPath, extraWatchFilePath, configPath} {
		if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
			t.Fatalf("os.MkdirAll(%q) error = %v", filepath.Dir(filePath), err)
		}
		if err := os.WriteFile(filePath, []byte("content"), 0o644); err != nil {
			t.Fatalf("os.WriteFile(%q) error = %v", filePath, err)
		}
	}
	if err := os.MkdirAll(outputPath, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v", outputPath, err)
	}

	watcher := newFakeFileWatcher()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rebuildSignal := make(chan struct{}, 6)
	reloadSignal := make(chan struct{}, 6)
	errorSignal := make(chan error, 4)
	if err := startServeWatchLoop(ctx, serveWatchLoop{
		watcher:          watcher,
		vaultPath:        vaultPath,
		outputPath:       outputPath,
		configPath:       configPath,
		extraWatchInputs: []serveWatchInput{{path: extraWatchFilePath, kind: serveWatchInputFile}},
		debounce:         15 * time.Millisecond,
		rebuild: func() error {
			rebuildSignal <- struct{}{}
			return nil
		},
		notifyReload: func() {
			reloadSignal <- struct{}{}
		},
		onError: func(err error) {
			errorSignal <- err
		},
	}); err != nil {
		t.Fatalf("startServeWatchLoop() error = %v", err)
	}
	waitForServeWatchAddCount(t, watcher, filepath.Dir(extraWatchFilePath), 1)

	tests := []struct {
		name   string
		path   string
		op     fsnotify.Op
		before func(t *testing.T)
	}{
		{
			name: "non-image attachment write",
			path: attachmentPath,
			op:   fsnotify.Write,
			before: func(t *testing.T) {
				t.Helper()
				if err := os.WriteFile(attachmentPath, []byte("updated manual"), 0o644); err != nil {
					t.Fatalf("os.WriteFile(%q) error = %v", attachmentPath, err)
				}
			},
		},
		{
			name: "non-image attachment remove",
			path: attachmentPath,
			op:   fsnotify.Remove,
			before: func(t *testing.T) {
				t.Helper()
				if err := os.Remove(attachmentPath); err != nil {
					t.Fatalf("os.Remove(%q) error = %v", attachmentPath, err)
				}
			},
		},
		{
			name: "vault custom.css write",
			path: customCSSPath,
			op:   fsnotify.Write,
			before: func(t *testing.T) {
				t.Helper()
				if err := os.WriteFile(customCSSPath, []byte("body { color: tomato; }\n"), 0o644); err != nil {
					t.Fatalf("os.WriteFile(%q) error = %v", customCSSPath, err)
				}
			},
		},
		{
			name: "generic extra watch file write",
			path: extraWatchFilePath,
			op:   fsnotify.Write,
			before: func(t *testing.T) {
				t.Helper()
				if err := os.WriteFile(extraWatchFilePath, []byte("updated extra watch input\n"), 0o644); err != nil {
					t.Fatalf("os.WriteFile(%q) error = %v", extraWatchFilePath, err)
				}
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			tt.before(t)
			watcher.send(fsnotify.Event{Name: tt.path, Op: tt.op})
			waitForServeWatchSignal(t, rebuildSignal, tt.name+" rebuild")
			waitForServeWatchSignal(t, reloadSignal, tt.name+" reload")
			assertNoServeWatchError(t, errorSignal)
		})
	}
}

func TestStartServeWatchLoopRecoversExternalExtraWatchFileAfterFailedRebuild(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	configPath := filepath.Join(vaultPath, defaultConfigFilename)
	outputPath := filepath.Join(vaultPath, "public")
	oldExtraWatchFilePath := filepath.Join(t.TempDir(), "watch-a", "explicit-input.txt")
	recoveryWatchDir := t.TempDir()
	brokenExtraWatchParent := filepath.Join(recoveryWatchDir, "watch-b", "nested")
	brokenExtraWatchFilePath := filepath.Join(brokenExtraWatchParent, "explicit-input.txt")
	for _, filePath := range []string{configPath, oldExtraWatchFilePath} {
		if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
			t.Fatalf("os.MkdirAll(%q) error = %v", filepath.Dir(filePath), err)
		}
		if err := os.WriteFile(filePath, []byte("content"), 0o644); err != nil {
			t.Fatalf("os.WriteFile(%q) error = %v", filePath, err)
		}
	}
	if err := os.MkdirAll(outputPath, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v", outputPath, err)
	}

	watcher := newFakeFileWatcher()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	currentExtraWatchInputs := []serveWatchInput{{path: oldExtraWatchFilePath, kind: serveWatchInputFile}}
	rebuildSignal := make(chan struct{}, 8)
	errorSignal := make(chan error, 4)
	rebuildCalls := 0
	if err := startServeWatchLoop(ctx, serveWatchLoop{
		watcher:          watcher,
		vaultPath:        vaultPath,
		outputPath:       outputPath,
		configPath:       configPath,
		extraWatchInputs: currentExtraWatchInputs,
		currentExtraWatchInputs: func() []serveWatchInput {
			return append([]serveWatchInput(nil), currentExtraWatchInputs...)
		},
		debounce: 15 * time.Millisecond,
		rebuild: func() error {
			rebuildCalls++
			if rebuildCalls == 1 {
				currentExtraWatchInputs = []serveWatchInput{{path: brokenExtraWatchFilePath, kind: serveWatchInputFile}}
				return errors.New("missing external extra watch file")
			}

			rebuildSignal <- struct{}{}
			return nil
		},
		onError: func(err error) {
			errorSignal <- err
		},
	}); err != nil {
		t.Fatalf("startServeWatchLoop() error = %v", err)
	}

	waitForServeWatchAddCount(t, watcher, filepath.Dir(oldExtraWatchFilePath), 1)

	watcher.send(fsnotify.Event{Name: configPath, Op: fsnotify.Write})
	waitForServeWatchErrorContains(t, errorSignal, "missing external extra watch file")
	waitForServeWatchAddCount(t, watcher, recoveryWatchDir, 1)
	waitForServeWatchRemoveCount(t, watcher, filepath.Dir(oldExtraWatchFilePath), 1)

	brokenExtraWatchTopDir := filepath.Join(recoveryWatchDir, "watch-b")
	mkdirServeWatchDir(t, brokenExtraWatchTopDir)
	sendServeWatchCreateFromWatchedParent(t, watcher, brokenExtraWatchTopDir)
	waitForServeWatchAddCount(t, watcher, brokenExtraWatchTopDir, 1)

	mkdirServeWatchDir(t, brokenExtraWatchParent)
	sendServeWatchCreateFromWatchedParent(t, watcher, brokenExtraWatchParent)
	waitForServeWatchAddCount(t, watcher, brokenExtraWatchParent, 1)

	if err := os.WriteFile(brokenExtraWatchFilePath, []byte("restored extra watch input\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", brokenExtraWatchFilePath, err)
	}
	sendServeWatchCreateFromWatchedParent(t, watcher, brokenExtraWatchFilePath)
	waitForServeWatchSignal(t, rebuildSignal, "recreated external extra watch file rebuild")
	assertNoServeWatchError(t, errorSignal)

	if err := os.WriteFile(brokenExtraWatchFilePath, []byte("updated extra watch input\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", brokenExtraWatchFilePath, err)
	}
	watcher.send(fsnotify.Event{Name: brokenExtraWatchFilePath, Op: fsnotify.Write})
	waitForServeWatchSignal(t, rebuildSignal, "fixed external extra watch file rebuild")
	assertNoServeWatchError(t, errorSignal)
}

func TestStartServeWatchLoopRecoversExternalExtraWatchFileAfterFailedRebuildFromOneShotDeepCreation(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	configPath := filepath.Join(vaultPath, defaultConfigFilename)
	outputPath := filepath.Join(vaultPath, "public")
	oldExtraWatchFilePath := filepath.Join(t.TempDir(), "watch-a", "explicit-input.txt")
	recoveryWatchDir := t.TempDir()
	brokenExtraWatchParent := filepath.Join(recoveryWatchDir, "watch-b", "nested")
	brokenExtraWatchFilePath := filepath.Join(brokenExtraWatchParent, "explicit-input.txt")
	brokenExtraWatchTopDir := filepath.Join(recoveryWatchDir, "watch-b")
	for _, filePath := range []string{configPath, oldExtraWatchFilePath} {
		if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
			t.Fatalf("os.MkdirAll(%q) error = %v", filepath.Dir(filePath), err)
		}
		if err := os.WriteFile(filePath, []byte("content"), 0o644); err != nil {
			t.Fatalf("os.WriteFile(%q) error = %v", filePath, err)
		}
	}
	if err := os.MkdirAll(outputPath, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v", outputPath, err)
	}

	watcher := newFakeFileWatcher()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	currentExtraWatchInputs := []serveWatchInput{{path: oldExtraWatchFilePath, kind: serveWatchInputFile}}
	rebuildSignal := make(chan struct{}, 8)
	errorSignal := make(chan error, 4)
	rebuildCalls := 0
	if err := startServeWatchLoop(ctx, serveWatchLoop{
		watcher:          watcher,
		vaultPath:        vaultPath,
		outputPath:       outputPath,
		configPath:       configPath,
		extraWatchInputs: currentExtraWatchInputs,
		currentExtraWatchInputs: func() []serveWatchInput {
			return append([]serveWatchInput(nil), currentExtraWatchInputs...)
		},
		debounce: 15 * time.Millisecond,
		rebuild: func() error {
			rebuildCalls++
			if rebuildCalls == 1 {
				currentExtraWatchInputs = []serveWatchInput{{path: brokenExtraWatchFilePath, kind: serveWatchInputFile}}
				return errors.New("missing external extra watch file")
			}

			rebuildSignal <- struct{}{}
			return nil
		},
		onError: func(err error) {
			errorSignal <- err
		},
	}); err != nil {
		t.Fatalf("startServeWatchLoop() error = %v", err)
	}

	waitForServeWatchAddCount(t, watcher, filepath.Dir(oldExtraWatchFilePath), 1)

	watcher.send(fsnotify.Event{Name: configPath, Op: fsnotify.Write})
	waitForServeWatchErrorContains(t, errorSignal, "missing external extra watch file")
	waitForServeWatchAddCount(t, watcher, recoveryWatchDir, 1)
	waitForServeWatchRemoveCount(t, watcher, filepath.Dir(oldExtraWatchFilePath), 1)

	if err := os.MkdirAll(brokenExtraWatchParent, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v", brokenExtraWatchParent, err)
	}
	if err := os.WriteFile(brokenExtraWatchFilePath, []byte("restored extra watch input\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", brokenExtraWatchFilePath, err)
	}
	sendServeWatchCreateFromWatchedParent(t, watcher, brokenExtraWatchTopDir)
	waitForServeWatchAddCount(t, watcher, brokenExtraWatchParent, 1)
	waitForServeWatchSignal(t, rebuildSignal, "one-shot recreated external extra watch file rebuild")
	assertNoServeWatchError(t, errorSignal)

	if err := os.WriteFile(brokenExtraWatchFilePath, []byte("updated extra watch input\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", brokenExtraWatchFilePath, err)
	}
	watcher.send(fsnotify.Event{Name: brokenExtraWatchFilePath, Op: fsnotify.Write})
	waitForServeWatchSignal(t, rebuildSignal, "fixed external extra watch file rebuild after one-shot recovery")
	assertNoServeWatchError(t, errorSignal)
}

func TestServeWatchDirsForInputsRejectFilesystemRootRecoveryForMissingExternalInputs(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputPath := filepath.Join(vaultPath, "public")
	if err := os.MkdirAll(outputPath, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v", outputPath, err)
	}

	rootDir := filesystemRootForPath(vaultPath)
	missingRoot := filepath.Join(rootDir, "obsite-root-fallback-"+strings.ReplaceAll(time.Now().UTC().Format("20060102150405.000000000"), ".", "-"))
	if _, err := os.Stat(missingRoot); err == nil {
		t.Fatalf("os.Stat(%q) unexpectedly succeeded; want a missing root-level directory for this regression", missingRoot)
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("os.Stat(%q) error = %v", missingRoot, err)
	}

	loop := serveWatchLoop{
		vaultPath:  vaultPath,
		outputPath: outputPath,
	}
	got, err := loop.watchDirsForInputs([]serveWatchInput{
		{path: filepath.Join(missingRoot, "templates"), kind: serveWatchInputDir},
		{path: filepath.Join(missingRoot, "extra-watch", "explicit-input.txt"), kind: serveWatchInputFile},
	})
	if err != nil {
		t.Fatalf("watchDirsForInputs() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("watchDirsForInputs() = %#v, want no filesystem-root fallback watches for fully missing external inputs", got)
	}
}

func TestStartServeWatchLoopRebuildsForDeepNestedExternalThemeRootFile(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	themeRoot := filepath.Join(t.TempDir(), "themes", "feature")
	deepDir := filepath.Join(themeRoot, "partials", "cards")
	deepFile := filepath.Join(deepDir, "badge.html")
	configPath := filepath.Join(vaultPath, defaultConfigFilename)
	outputPath := filepath.Join(vaultPath, "public")
	for _, filePath := range []string{configPath, deepFile} {
		if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
			t.Fatalf("os.MkdirAll(%q) error = %v", filepath.Dir(filePath), err)
		}
		if err := os.WriteFile(filePath, []byte("content"), 0o644); err != nil {
			t.Fatalf("os.WriteFile(%q) error = %v", filePath, err)
		}
	}
	if err := os.MkdirAll(outputPath, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v", outputPath, err)
	}

	watcher := newFakeFileWatcher()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rebuildSignal := make(chan struct{}, 4)
	errorSignal := make(chan error, 4)
	if err := startServeWatchLoop(ctx, serveWatchLoop{
		watcher:          watcher,
		vaultPath:        vaultPath,
		outputPath:       outputPath,
		configPath:       configPath,
		extraWatchInputs: collectServeWatchInputs(themeRoot, "", vaultPath),
		themeRoots:       []string{themeRoot},
		debounce:         15 * time.Millisecond,
		rebuild: func() error {
			rebuildSignal <- struct{}{}
			return nil
		},
		onError: func(err error) {
			errorSignal <- err
		},
	}); err != nil {
		t.Fatalf("startServeWatchLoop() error = %v", err)
	}

	waitForServeWatchAddCount(t, watcher, themeRoot, 1)
	waitForServeWatchAddCount(t, watcher, filepath.Join(themeRoot, "partials"), 1)
	waitForServeWatchAddCount(t, watcher, deepDir, 1)

	if err := os.WriteFile(deepFile, []byte("updated"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", deepFile, err)
	}
	watcher.send(fsnotify.Event{Name: deepFile, Op: fsnotify.Write})
	waitForServeWatchSignal(t, rebuildSignal, "deep external theme root rebuild")
	assertNoServeWatchError(t, errorSignal)
}

func TestStartServeWatchLoopRecoversExternalExtraWatchRootAfterFailedRebuild(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	configPath := filepath.Join(vaultPath, defaultConfigFilename)
	outputPath := filepath.Join(vaultPath, "public")
	oldThemeRoot := filepath.Join(t.TempDir(), "themes-a", "feature")
	oldThemeFile := filepath.Join(oldThemeRoot, "base.html")
	recoveryWatchDir := t.TempDir()
	brokenThemeParent := filepath.Join(recoveryWatchDir, "themes-b", "nested")
	brokenThemeRoot := filepath.Join(brokenThemeParent, "feature")
	brokenThemeFile := filepath.Join(brokenThemeRoot, "base.html")
	for _, filePath := range []string{configPath, oldThemeFile} {
		if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
			t.Fatalf("os.MkdirAll(%q) error = %v", filepath.Dir(filePath), err)
		}
		if err := os.WriteFile(filePath, []byte("content"), 0o644); err != nil {
			t.Fatalf("os.WriteFile(%q) error = %v", filePath, err)
		}
	}
	if err := os.MkdirAll(outputPath, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v", outputPath, err)
	}

	watcher := newFakeFileWatcher()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	currentExtraWatchInputs := []serveWatchInput{{path: oldThemeRoot, kind: serveWatchInputDir}}
	rebuildSignal := make(chan struct{}, 8)
	errorSignal := make(chan error, 4)
	rebuildCalls := 0
	if err := startServeWatchLoop(ctx, serveWatchLoop{
		watcher:          watcher,
		vaultPath:        vaultPath,
		outputPath:       outputPath,
		configPath:       configPath,
		extraWatchInputs: currentExtraWatchInputs,
		currentExtraWatchInputs: func() []serveWatchInput {
			return append([]serveWatchInput(nil), currentExtraWatchInputs...)
		},
		debounce: 15 * time.Millisecond,
		rebuild: func() error {
			rebuildCalls++
			if rebuildCalls == 1 {
				currentExtraWatchInputs = []serveWatchInput{{path: brokenThemeRoot, kind: serveWatchInputDir}}
				return errors.New("missing external theme root")
			}

			rebuildSignal <- struct{}{}
			return nil
		},
		onError: func(err error) {
			errorSignal <- err
		},
	}); err != nil {
		t.Fatalf("startServeWatchLoop() error = %v", err)
	}

	waitForServeWatchAddCount(t, watcher, oldThemeRoot, 1)

	watcher.send(fsnotify.Event{Name: configPath, Op: fsnotify.Write})
	waitForServeWatchErrorContains(t, errorSignal, "missing external theme root")
	waitForServeWatchAddCount(t, watcher, recoveryWatchDir, 1)
	waitForServeWatchRemoveCount(t, watcher, oldThemeRoot, 1)

	brokenThemeTopDir := filepath.Join(recoveryWatchDir, "themes-b")
	mkdirServeWatchDir(t, brokenThemeTopDir)
	sendServeWatchCreateFromWatchedParent(t, watcher, brokenThemeTopDir)
	waitForServeWatchAddCount(t, watcher, brokenThemeTopDir, 1)

	mkdirServeWatchDir(t, brokenThemeParent)
	sendServeWatchCreateFromWatchedParent(t, watcher, brokenThemeParent)
	waitForServeWatchAddCount(t, watcher, brokenThemeParent, 1)

	mkdirServeWatchDir(t, brokenThemeRoot)
	if err := os.WriteFile(brokenThemeFile, []byte("content"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", brokenThemeFile, err)
	}

	sendServeWatchCreateFromWatchedParent(t, watcher, brokenThemeRoot)
	waitForServeWatchAddCount(t, watcher, brokenThemeRoot, 1)
	waitForServeWatchSignal(t, rebuildSignal, "recreated external theme root rebuild")
	assertNoServeWatchError(t, errorSignal)

	if err := os.WriteFile(brokenThemeFile, []byte("updated"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", brokenThemeFile, err)
	}
	watcher.send(fsnotify.Event{Name: brokenThemeFile, Op: fsnotify.Write})
	waitForServeWatchSignal(t, rebuildSignal, "fixed external theme root rebuild")
	assertNoServeWatchError(t, errorSignal)
}

func TestStartServeWatchLoopReaddsRemovedOrRenamedExternalExtraWatchRoot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		op            fsnotify.Op
		removeWatched func(t *testing.T, watchedDir string)
	}{
		{
			name: "remove",
			op:   fsnotify.Remove,
			removeWatched: func(t *testing.T, watchedDir string) {
				t.Helper()
				if err := os.RemoveAll(watchedDir); err != nil {
					t.Fatalf("os.RemoveAll(%q) error = %v", watchedDir, err)
				}
			},
		},
		{
			name: "rename",
			op:   fsnotify.Rename,
			removeWatched: func(t *testing.T, watchedDir string) {
				t.Helper()
				renamedPath := watchedDir + "-renamed"
				if err := os.Rename(watchedDir, renamedPath); err != nil {
					t.Fatalf("os.Rename(%q, %q) error = %v", watchedDir, renamedPath, err)
				}
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			vaultPath := t.TempDir()
			configPath := filepath.Join(vaultPath, defaultConfigFilename)
			outputPath := filepath.Join(vaultPath, "public")
			externalRoot := t.TempDir()
			recoveryParent := filepath.Join(externalRoot, "themes")
			externalThemeRoot := filepath.Join(externalRoot, "themes", "feature")
			externalThemeFile := filepath.Join(externalThemeRoot, "base.html")
			for _, filePath := range []string{configPath, externalThemeFile} {
				if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
					t.Fatalf("os.MkdirAll(%q) error = %v", filepath.Dir(filePath), err)
				}
				if err := os.WriteFile(filePath, []byte("content"), 0o644); err != nil {
					t.Fatalf("os.WriteFile(%q) error = %v", filePath, err)
				}
			}
			if err := os.MkdirAll(outputPath, 0o755); err != nil {
				t.Fatalf("os.MkdirAll(%q) error = %v", outputPath, err)
			}

			watcher := newFakeFileWatcher()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			rebuildSignal := make(chan struct{}, 8)
			errorSignal := make(chan error, 4)
			if err := startServeWatchLoop(ctx, serveWatchLoop{
				watcher:          watcher,
				vaultPath:        vaultPath,
				outputPath:       outputPath,
				configPath:       configPath,
				extraWatchInputs: []serveWatchInput{{path: externalThemeRoot, kind: serveWatchInputDir}},
				debounce:         15 * time.Millisecond,
				rebuild: func() error {
					rebuildSignal <- struct{}{}
					return nil
				},
				onError: func(err error) {
					errorSignal <- err
				},
			}); err != nil {
				t.Fatalf("startServeWatchLoop() error = %v", err)
			}

			waitForServeWatchAddCount(t, watcher, externalThemeRoot, 1)
			waitForServeWatchAddCount(t, watcher, recoveryParent, 1)

			tt.removeWatched(t, externalThemeRoot)
			watcher.send(fsnotify.Event{Name: externalThemeRoot, Op: tt.op})
			waitForServeWatchRemoveCount(t, watcher, externalThemeRoot, 1)
			waitForServeWatchSignal(t, rebuildSignal, "external theme root removal rebuild")
			assertNoServeWatchError(t, errorSignal)

			if err := os.MkdirAll(externalThemeRoot, 0o755); err != nil {
				t.Fatalf("os.MkdirAll(%q) error = %v", externalThemeRoot, err)
			}
			if err := os.WriteFile(externalThemeFile, []byte("restored"), 0o644); err != nil {
				t.Fatalf("os.WriteFile(%q) error = %v", externalThemeFile, err)
			}

			sendServeWatchCreateFromWatchedParent(t, watcher, externalThemeRoot)
			waitForServeWatchAddCount(t, watcher, externalThemeRoot, 2)
			waitForServeWatchSignal(t, rebuildSignal, "recreated external theme root rebuild")
			assertNoServeWatchError(t, errorSignal)

			if err := os.WriteFile(externalThemeFile, []byte("updated"), 0o644); err != nil {
				t.Fatalf("os.WriteFile(%q) error = %v", externalThemeFile, err)
			}
			watcher.send(fsnotify.Event{Name: externalThemeFile, Op: fsnotify.Write})
			waitForServeWatchSignal(t, rebuildSignal, "recreated external theme root edit rebuild")
			assertNoServeWatchError(t, errorSignal)
		})
	}
}

func TestStartServeWatchLoopAddsRecursiveWatchesForNewDeepDirectoriesInsideSelectedThemeRoot(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	themeRoot := filepath.Join(t.TempDir(), "themes", "feature")
	configPath := filepath.Join(vaultPath, defaultConfigFilename)
	outputPath := filepath.Join(vaultPath, "public")
	for _, filePath := range []string{configPath, filepath.Join(themeRoot, "base.html")} {
		if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
			t.Fatalf("os.MkdirAll(%q) error = %v", filepath.Dir(filePath), err)
		}
		if err := os.WriteFile(filePath, []byte("content"), 0o644); err != nil {
			t.Fatalf("os.WriteFile(%q) error = %v", filePath, err)
		}
	}
	if err := os.MkdirAll(outputPath, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v", outputPath, err)
	}

	watcher := newFakeFileWatcher()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rebuildSignal := make(chan struct{}, 6)
	errorSignal := make(chan error, 4)
	if err := startServeWatchLoop(ctx, serveWatchLoop{
		watcher:          watcher,
		vaultPath:        vaultPath,
		outputPath:       outputPath,
		configPath:       configPath,
		extraWatchInputs: collectServeWatchInputs(themeRoot, "", vaultPath),
		themeRoots:       []string{themeRoot},
		debounce:         15 * time.Millisecond,
		rebuild: func() error {
			rebuildSignal <- struct{}{}
			return nil
		},
		onError: func(err error) {
			errorSignal <- err
		},
	}); err != nil {
		t.Fatalf("startServeWatchLoop() error = %v", err)
	}

	waitForServeWatchAddCount(t, watcher, themeRoot, 1)

	newDir := filepath.Join(themeRoot, "partials", "cards", "shared")
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v", newDir, err)
	}
	watcher.send(fsnotify.Event{Name: filepath.Join(themeRoot, "partials"), Op: fsnotify.Create})
	waitForServeWatchAddCount(t, watcher, filepath.Join(themeRoot, "partials"), 1)
	waitForServeWatchAddCount(t, watcher, filepath.Join(themeRoot, "partials", "cards"), 1)
	waitForServeWatchAddCount(t, watcher, newDir, 1)
	waitForServeWatchSignal(t, rebuildSignal, "new selected theme root directory rebuild")
	assertNoServeWatchError(t, errorSignal)

	deepFile := filepath.Join(newDir, "badge.html")
	if err := os.WriteFile(deepFile, []byte("updated"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", deepFile, err)
	}
	watcher.send(fsnotify.Event{Name: deepFile, Op: fsnotify.Write})
	waitForServeWatchSignal(t, rebuildSignal, "deep file rebuild after recursive watch add")
	assertNoServeWatchError(t, errorSignal)
}

func TestStartServeWatchLoopRebuildsForDeepNestedHiddenInVaultThemeRootFile(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	hiddenThemeRoot := filepath.Join(vaultPath, ".obsidian", "themes", "feature")
	hiddenThemeDir := filepath.Join(hiddenThemeRoot, "partials", "cards")
	hiddenThemePath := filepath.Join(hiddenThemeDir, "badge.html")
	configPath := filepath.Join(vaultPath, defaultConfigFilename)
	outputPath := filepath.Join(vaultPath, "public")
	for _, filePath := range []string{configPath, hiddenThemePath} {
		if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
			t.Fatalf("os.MkdirAll(%q) error = %v", filepath.Dir(filePath), err)
		}
		if err := os.WriteFile(filePath, []byte("content"), 0o644); err != nil {
			t.Fatalf("os.WriteFile(%q) error = %v", filePath, err)
		}
	}
	if err := os.MkdirAll(outputPath, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v", outputPath, err)
	}

	watcher := newFakeFileWatcher()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rebuildSignal := make(chan struct{}, 4)
	errorSignal := make(chan error, 4)
	if err := startServeWatchLoop(ctx, serveWatchLoop{
		watcher:          watcher,
		vaultPath:        vaultPath,
		outputPath:       outputPath,
		configPath:       configPath,
		extraWatchInputs: collectServeWatchInputs(hiddenThemeRoot, "", vaultPath),
		themeRoots:       []string{hiddenThemeRoot},
		debounce:         15 * time.Millisecond,
		rebuild: func() error {
			rebuildSignal <- struct{}{}
			return nil
		},
		onError: func(err error) {
			errorSignal <- err
		},
	}); err != nil {
		t.Fatalf("startServeWatchLoop() error = %v", err)
	}

	waitForServeWatchAddCount(t, watcher, hiddenThemeRoot, 1)
	waitForServeWatchAddCount(t, watcher, hiddenThemeDir, 1)

	if err := os.WriteFile(hiddenThemePath, []byte("updated"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", hiddenThemePath, err)
	}
	watcher.send(fsnotify.Event{Name: hiddenThemePath, Op: fsnotify.Write})
	waitForServeWatchSignal(t, rebuildSignal, "hidden in-vault theme root rebuild")
	assertNoServeWatchError(t, errorSignal)
}

func TestStartServeWatchLoopIgnoresUnselectedThemeRootChanges(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	selectedThemeRoot := filepath.Join(vaultPath, "themes", "feature")
	unselectedThemeRoot := filepath.Join(vaultPath, "themes", "serif")
	selectedThemePath := filepath.Join(selectedThemeRoot, "partials", "cards", "badge.html")
	unselectedThemePath := filepath.Join(unselectedThemeRoot, "partials", "cards", "badge.html")
	configPath := filepath.Join(vaultPath, defaultConfigFilename)
	outputPath := filepath.Join(vaultPath, "public")
	for _, filePath := range []string{configPath, selectedThemePath, unselectedThemePath} {
		if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
			t.Fatalf("os.MkdirAll(%q) error = %v", filepath.Dir(filePath), err)
		}
		if err := os.WriteFile(filePath, []byte("content"), 0o644); err != nil {
			t.Fatalf("os.WriteFile(%q) error = %v", filePath, err)
		}
	}
	if err := os.MkdirAll(outputPath, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v", outputPath, err)
	}

	watcher := newFakeFileWatcher()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rebuildSignal := make(chan struct{}, 4)
	errorSignal := make(chan error, 4)
	if err := startServeWatchLoop(ctx, serveWatchLoop{
		watcher:          watcher,
		vaultPath:        vaultPath,
		outputPath:       outputPath,
		configPath:       configPath,
		extraWatchInputs: collectServeWatchInputs(selectedThemeRoot, "", vaultPath),
		themeRoots:       []string{selectedThemeRoot, unselectedThemeRoot},
		debounce:         15 * time.Millisecond,
		rebuild: func() error {
			rebuildSignal <- struct{}{}
			return nil
		},
		onError: func(err error) {
			errorSignal <- err
		},
	}); err != nil {
		t.Fatalf("startServeWatchLoop() error = %v", err)
	}

	waitForServeWatchAddCount(t, watcher, filepath.Join(selectedThemeRoot, "partials", "cards"), 1)
	waitForServeWatchAddCount(t, watcher, filepath.Join(unselectedThemeRoot, "partials", "cards"), 1)

	if err := os.WriteFile(unselectedThemePath, []byte("updated"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", unselectedThemePath, err)
	}
	watcher.send(fsnotify.Event{Name: unselectedThemePath, Op: fsnotify.Write})
	assertNoServeWatchSignal(t, rebuildSignal, errorSignal, "unselected theme root write")

	if err := os.WriteFile(selectedThemePath, []byte("updated"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", selectedThemePath, err)
	}
	watcher.send(fsnotify.Event{Name: selectedThemePath, Op: fsnotify.Write})
	waitForServeWatchSignal(t, rebuildSignal, "selected theme root rebuild")
	assertNoServeWatchError(t, errorSignal)
}

func TestStartServeWatchLoopIgnoresNestedUnselectedThemeRootInsideSelectedThemeTree(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	selectedThemeRoot := filepath.Join(vaultPath, "themes")
	unselectedThemeRoot := filepath.Join(selectedThemeRoot, "serif")
	selectedThemePath := filepath.Join(selectedThemeRoot, "partials", "cards", "badge.html")
	configPath := filepath.Join(vaultPath, defaultConfigFilename)
	outputPath := filepath.Join(vaultPath, "public")
	for _, filePath := range []string{configPath, filepath.Join(selectedThemeRoot, "base.html"), selectedThemePath, filepath.Join(unselectedThemeRoot, "base.html")} {
		if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
			t.Fatalf("os.MkdirAll(%q) error = %v", filepath.Dir(filePath), err)
		}
		if err := os.WriteFile(filePath, []byte("content"), 0o644); err != nil {
			t.Fatalf("os.WriteFile(%q) error = %v", filePath, err)
		}
	}
	if err := os.MkdirAll(outputPath, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v", outputPath, err)
	}

	watcher := newFakeFileWatcher()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rebuildSignal := make(chan struct{}, 6)
	errorSignal := make(chan error, 4)
	if err := startServeWatchLoop(ctx, serveWatchLoop{
		watcher:          watcher,
		vaultPath:        vaultPath,
		outputPath:       outputPath,
		configPath:       configPath,
		extraWatchInputs: collectServeWatchInputs(selectedThemeRoot, "", vaultPath),
		themeRoots:       []string{selectedThemeRoot, unselectedThemeRoot},
		debounce:         15 * time.Millisecond,
		rebuild: func() error {
			rebuildSignal <- struct{}{}
			return nil
		},
		onError: func(err error) {
			errorSignal <- err
		},
	}); err != nil {
		t.Fatalf("startServeWatchLoop() error = %v", err)
	}

	waitForServeWatchAddCount(t, watcher, selectedThemeRoot, 1)
	waitForServeWatchAddCount(t, watcher, filepath.Join(selectedThemeRoot, "partials"), 1)
	waitForServeWatchAddCount(t, watcher, filepath.Join(selectedThemeRoot, "partials", "cards"), 1)
	if !watcher.hasActiveWatch(selectedThemeRoot) {
		t.Fatalf("watcher missing active watch for selected theme root %q", selectedThemeRoot)
	}
	if !watcher.hasActiveWatch(filepath.Join(selectedThemeRoot, "partials")) {
		t.Fatalf("watcher missing active watch for selected theme child dir %q", filepath.Join(selectedThemeRoot, "partials"))
	}
	if !watcher.hasActiveWatch(filepath.Join(selectedThemeRoot, "partials", "cards")) {
		t.Fatalf("watcher missing active watch for selected theme child dir %q", filepath.Join(selectedThemeRoot, "partials", "cards"))
	}
	if got := watcher.countAddCalls(unselectedThemeRoot); got != 0 {
		t.Fatalf("watcher.Add(%q) count = %d, want 0 for nested unselected theme root", unselectedThemeRoot, got)
	}
	if watcher.hasActiveWatch(unselectedThemeRoot) {
		t.Fatalf("watcher unexpectedly has active watch for nested unselected theme root %q", unselectedThemeRoot)
	}

	if err := os.WriteFile(selectedThemePath, []byte("updated"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", selectedThemePath, err)
	}
	watcher.send(fsnotify.Event{Name: selectedThemePath, Op: fsnotify.Write})
	waitForServeWatchSignal(t, rebuildSignal, "selected theme child directory rebuild")
	assertNoServeWatchError(t, errorSignal)

	newDir := filepath.Join(unselectedThemeRoot, "partials", "cards", "shared")
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v", newDir, err)
	}
	watcher.send(fsnotify.Event{Name: filepath.Join(unselectedThemeRoot, "partials"), Op: fsnotify.Create})
	assertNoServeWatchSignal(t, rebuildSignal, errorSignal, "nested unselected theme root directory create")
	if watcher.hasActiveWatch(filepath.Join(unselectedThemeRoot, "partials")) {
		t.Fatalf("watcher unexpectedly has active watch for nested unselected theme child dir %q", filepath.Join(unselectedThemeRoot, "partials"))
	}
	if watcher.hasActiveWatch(filepath.Join(unselectedThemeRoot, "partials", "cards")) {
		t.Fatalf("watcher unexpectedly has active watch for nested unselected theme child dir %q", filepath.Join(unselectedThemeRoot, "partials", "cards"))
	}
	if watcher.hasActiveWatch(newDir) {
		t.Fatalf("watcher unexpectedly has active watch for nested unselected theme child dir %q", newDir)
	}
	if got := watcher.countAddCalls(filepath.Join(unselectedThemeRoot, "partials")); got != 0 {
		t.Fatalf("watcher.Add(%q) count = %d, want 0 for nested unselected theme root", filepath.Join(unselectedThemeRoot, "partials"), got)
	}
	if got := watcher.countAddCalls(filepath.Join(unselectedThemeRoot, "partials", "cards")); got != 0 {
		t.Fatalf("watcher.Add(%q) count = %d, want 0 for nested unselected theme root", filepath.Join(unselectedThemeRoot, "partials", "cards"), got)
	}
	if got := watcher.countAddCalls(newDir); got != 0 {
		t.Fatalf("watcher.Add(%q) count = %d, want 0 for nested unselected theme root", newDir, got)
	}

	deepFile := filepath.Join(newDir, "badge.html")
	if err := os.WriteFile(deepFile, []byte("updated"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", deepFile, err)
	}
	watcher.send(fsnotify.Event{Name: deepFile, Op: fsnotify.Write})
	assertNoServeWatchSignal(t, rebuildSignal, errorSignal, "nested unselected theme root deep file write")
	if watcher.hasActiveWatch(newDir) {
		t.Fatalf("watcher unexpectedly has active watch for nested unselected theme child dir %q after deep file write", newDir)
	}
	assertNoServeWatchError(t, errorSignal)
}

func TestStartServeWatchLoopRetainsVaultRootWatchWhenMissingHiddenThemeRootIsRemoved(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	rootNotePath := filepath.Join(vaultPath, "root-note.md")
	hiddenThemeRoot := filepath.Join(vaultPath, ".obsidian", "themes", "feature")
	configPath := filepath.Join(vaultPath, defaultConfigFilename)
	outputPath := filepath.Join(vaultPath, "public")
	for _, filePath := range []string{rootNotePath, configPath} {
		if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
			t.Fatalf("os.MkdirAll(%q) error = %v", filepath.Dir(filePath), err)
		}
		if err := os.WriteFile(filePath, []byte("content"), 0o644); err != nil {
			t.Fatalf("os.WriteFile(%q) error = %v", filePath, err)
		}
	}
	if err := os.MkdirAll(outputPath, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v", outputPath, err)
	}

	watcher := newFakeFileWatcher()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	currentExtraWatchInputs := collectServeWatchInputs(hiddenThemeRoot, "", vaultPath)
	rebuildSignal := make(chan struct{}, 4)
	errorSignal := make(chan error, 4)
	if err := startServeWatchLoop(ctx, serveWatchLoop{
		watcher:          watcher,
		vaultPath:        vaultPath,
		outputPath:       outputPath,
		configPath:       configPath,
		extraWatchInputs: currentExtraWatchInputs,
		currentExtraWatchInputs: func() []serveWatchInput {
			return append([]serveWatchInput(nil), currentExtraWatchInputs...)
		},
		debounce: 15 * time.Millisecond,
		rebuild: func() error {
			currentExtraWatchInputs = nil
			rebuildSignal <- struct{}{}
			return nil
		},
		onError: func(err error) {
			errorSignal <- err
		},
	}); err != nil {
		t.Fatalf("startServeWatchLoop() error = %v", err)
	}

	waitForServeWatchAddCount(t, watcher, vaultPath, 1)

	watcher.send(fsnotify.Event{Name: configPath, Op: fsnotify.Write})
	waitForServeWatchSignal(t, rebuildSignal, "config rebuild")
	assertNoServeWatchError(t, errorSignal)
	if got := watcher.countRemoveCalls(vaultPath); got != 0 {
		t.Fatalf("watcher.Remove(%q) count = %d, want 0 so the base vault watch stays active", vaultPath, got)
	}

	if !watcher.sendIfWatched(fsnotify.Event{Name: rootNotePath, Op: fsnotify.Write}) {
		t.Fatalf("root note %q is no longer covered by any active watch after removing the missing hidden override", rootNotePath)
	}
	waitForServeWatchSignal(t, rebuildSignal, "root note rebuild")
	assertNoServeWatchError(t, errorSignal)
}

func TestStartServeWatchLoopReportsWatcherCloseErrors(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	configPath := filepath.Join(vaultPath, defaultConfigFilename)
	rootNotePath := filepath.Join(vaultPath, "root-note.md")
	outputPath := filepath.Join(vaultPath, "public")
	for _, filePath := range []string{configPath, rootNotePath} {
		if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
			t.Fatalf("os.MkdirAll(%q) error = %v", filepath.Dir(filePath), err)
		}
		if err := os.WriteFile(filePath, []byte("content"), 0o644); err != nil {
			t.Fatalf("os.WriteFile(%q) error = %v", filePath, err)
		}
	}
	if err := os.MkdirAll(outputPath, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v", outputPath, err)
	}

	watcher := newFakeFileWatcher()
	watcher.setCloseErr(errors.New("close failed"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errorSignal := make(chan error, 4)
	if err := startServeWatchLoop(ctx, serveWatchLoop{
		watcher:    watcher,
		vaultPath:  vaultPath,
		outputPath: outputPath,
		configPath: configPath,
		debounce:   15 * time.Millisecond,
		rebuild: func() error {
			return nil
		},
		onError: func(err error) {
			errorSignal <- err
		},
	}); err != nil {
		t.Fatalf("startServeWatchLoop() error = %v", err)
	}

	waitForServeWatchAddCount(t, watcher, vaultPath, 1)
	cancel()
	waitForServeWatchErrorContains(t, errorSignal, "close watcher: close failed")
	if got := watcher.countCloseCalls(); got != 1 {
		t.Fatalf("watcher.Close() calls = %d, want %d", got, 1)
	}
}

func TestStartServeWatchLoopRefreshesSelectedThemeRootWatchesAfterRebuild(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	configPath := filepath.Join(vaultPath, defaultConfigFilename)
	outputPath := filepath.Join(vaultPath, "public")
	oldThemeRoot := filepath.Join(t.TempDir(), "themes-a", "feature")
	newThemeRoot := filepath.Join(t.TempDir(), "themes-b", "feature")
	oldThemePath := filepath.Join(oldThemeRoot, "partials", "cards", "badge.html")
	newThemePath := filepath.Join(newThemeRoot, "partials", "cards", "badge.html")
	for _, filePath := range []string{configPath, oldThemePath, newThemePath} {
		if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
			t.Fatalf("os.MkdirAll(%q) error = %v", filepath.Dir(filePath), err)
		}
		if err := os.WriteFile(filePath, []byte("content"), 0o644); err != nil {
			t.Fatalf("os.WriteFile(%q) error = %v", filePath, err)
		}
	}
	if err := os.MkdirAll(outputPath, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v", outputPath, err)
	}

	watcher := newFakeFileWatcher()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	currentExtraWatchInputs := collectServeWatchInputs(oldThemeRoot, "", vaultPath)
	currentThemeRoots := normalizeServeWatchPaths([]string{oldThemeRoot, newThemeRoot})
	rebuildSignal := make(chan struct{}, 8)
	errorSignal := make(chan error, 4)
	if err := startServeWatchLoop(ctx, serveWatchLoop{
		watcher:          watcher,
		vaultPath:        vaultPath,
		outputPath:       outputPath,
		configPath:       configPath,
		extraWatchInputs: currentExtraWatchInputs,
		currentExtraWatchInputs: func() []serveWatchInput {
			return append([]serveWatchInput(nil), currentExtraWatchInputs...)
		},
		themeRoots: currentThemeRoots,
		currentThemeRoots: func() []string {
			return append([]string(nil), currentThemeRoots...)
		},
		debounce: 15 * time.Millisecond,
		rebuild: func() error {
			currentExtraWatchInputs = collectServeWatchInputs(newThemeRoot, "", vaultPath)
			rebuildSignal <- struct{}{}
			return nil
		},
		onError: func(err error) {
			errorSignal <- err
		},
	}); err != nil {
		t.Fatalf("startServeWatchLoop() error = %v", err)
	}

	waitForServeWatchAddCount(t, watcher, oldThemeRoot, 1)
	waitForServeWatchAddCount(t, watcher, filepath.Join(oldThemeRoot, "partials", "cards"), 1)

	watcher.send(fsnotify.Event{Name: configPath, Op: fsnotify.Write})
	waitForServeWatchSignal(t, rebuildSignal, "config rebuild")
	waitForServeWatchAddCount(t, watcher, newThemeRoot, 1)
	waitForServeWatchAddCount(t, watcher, filepath.Join(newThemeRoot, "partials", "cards"), 1)
	assertNoServeWatchError(t, errorSignal)

	watcher.send(fsnotify.Event{Name: oldThemePath, Op: fsnotify.Write})
	assertNoServeWatchSignal(t, rebuildSignal, errorSignal, "stale selected theme root")

	watcher.send(fsnotify.Event{Name: newThemePath, Op: fsnotify.Write})
	waitForServeWatchSignal(t, rebuildSignal, "refreshed selected theme root rebuild")
	assertNoServeWatchError(t, errorSignal)
}

func TestStartServeWatchLoopIgnoresStaleInVaultThemeRootAfterRefresh(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	configPath := filepath.Join(vaultPath, defaultConfigFilename)
	outputPath := filepath.Join(vaultPath, "public")
	oldThemeRoot := filepath.Join(vaultPath, "themes", "alpha")
	newThemeRoot := filepath.Join(vaultPath, ".hidden-alpha")
	oldThemePath := filepath.Join(oldThemeRoot, "partials", "cards", "badge.html")
	newThemePath := filepath.Join(newThemeRoot, "partials", "cards", "badge.html")
	for _, filePath := range []string{configPath, oldThemePath, newThemePath} {
		if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
			t.Fatalf("os.MkdirAll(%q) error = %v", filepath.Dir(filePath), err)
		}
		if err := os.WriteFile(filePath, []byte("content"), 0o644); err != nil {
			t.Fatalf("os.WriteFile(%q) error = %v", filePath, err)
		}
	}
	if err := os.MkdirAll(outputPath, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v", outputPath, err)
	}

	watcher := newFakeFileWatcher()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	currentExtraWatchInputs := collectServeWatchInputs(oldThemeRoot, "", vaultPath)
	currentThemeRoots := normalizeServeWatchPaths([]string{oldThemeRoot, newThemeRoot})
	rebuildSignal := make(chan struct{}, 8)
	errorSignal := make(chan error, 4)
	if err := startServeWatchLoop(ctx, serveWatchLoop{
		watcher:          watcher,
		vaultPath:        vaultPath,
		outputPath:       outputPath,
		configPath:       configPath,
		extraWatchInputs: currentExtraWatchInputs,
		currentExtraWatchInputs: func() []serveWatchInput {
			return append([]serveWatchInput(nil), currentExtraWatchInputs...)
		},
		themeRoots: currentThemeRoots,
		currentThemeRoots: func() []string {
			return append([]string(nil), currentThemeRoots...)
		},
		debounce: 15 * time.Millisecond,
		rebuild: func() error {
			currentExtraWatchInputs = collectServeWatchInputs(newThemeRoot, "", vaultPath)
			currentThemeRoots = normalizeServeWatchPaths([]string{newThemeRoot})
			rebuildSignal <- struct{}{}
			return nil
		},
		onError: func(err error) {
			errorSignal <- err
		},
	}); err != nil {
		t.Fatalf("startServeWatchLoop() error = %v", err)
	}

	waitForServeWatchAddCount(t, watcher, oldThemeRoot, 1)
	waitForServeWatchAddCount(t, watcher, filepath.Join(oldThemeRoot, "partials", "cards"), 1)

	watcher.send(fsnotify.Event{Name: configPath, Op: fsnotify.Write})
	waitForServeWatchSignal(t, rebuildSignal, "config rebuild")
	waitForServeWatchAddCount(t, watcher, newThemeRoot, 1)
	waitForServeWatchAddCount(t, watcher, filepath.Join(newThemeRoot, "partials", "cards"), 1)
	assertNoServeWatchError(t, errorSignal)

	watcher.send(fsnotify.Event{Name: oldThemePath, Op: fsnotify.Write})
	assertNoServeWatchSignal(t, rebuildSignal, errorSignal, "stale in-vault selected theme root")

	watcher.send(fsnotify.Event{Name: newThemePath, Op: fsnotify.Write})
	waitForServeWatchSignal(t, rebuildSignal, "refreshed in-vault selected theme root rebuild")
	assertNoServeWatchError(t, errorSignal)
}

type fakePreviewServer struct {
	listenErr     error
	enableCalls   int
	listenCalls   int
	reloadCalls   int
	reloadCalled  chan struct{}
	listenStarted chan struct{}
	listenBlock   <-chan struct{}
}

func (s *fakePreviewServer) EnableLiveReload() {
	s.enableCalls++
}

func (s *fakePreviewServer) ListenAndServe() error {
	s.listenCalls++
	if s.listenStarted != nil {
		select {
		case s.listenStarted <- struct{}{}:
		default:
		}
	}
	if s.listenBlock != nil {
		<-s.listenBlock
	}
	return s.listenErr
}

func (s *fakePreviewServer) NotifyReload() {
	s.reloadCalls++
	if s.reloadCalled != nil {
		s.reloadCalled <- struct{}{}
	}
}

type fakeFileWatcher struct {
	mu          sync.Mutex
	addCalls    []string
	active      map[string]struct{}
	removeCalls []string
	removeErrs  map[string]error
	closeErr    error
	closeCalls  int
	events      chan fsnotify.Event
	errors      chan error
}

func newFakeFileWatcher() *fakeFileWatcher {
	return &fakeFileWatcher{
		active:     make(map[string]struct{}),
		events:     make(chan fsnotify.Event, 16),
		errors:     make(chan error, 4),
		removeErrs: make(map[string]error),
	}
}

func (w *fakeFileWatcher) Add(name string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	cleanPath := filepath.Clean(name)
	w.addCalls = append(w.addCalls, cleanPath)
	w.active[cleanPath] = struct{}{}
	return nil
}

func (w *fakeFileWatcher) Remove(name string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	cleanPath := filepath.Clean(name)
	w.removeCalls = append(w.removeCalls, cleanPath)
	if err := w.removeErrs[cleanPath]; err != nil {
		return err
	}
	delete(w.active, cleanPath)
	return nil
}

func (w *fakeFileWatcher) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closeCalls++
	return w.closeErr
}

func (w *fakeFileWatcher) Events() <-chan fsnotify.Event {
	return w.events
}

func (w *fakeFileWatcher) Errors() <-chan error {
	return w.errors
}

func (w *fakeFileWatcher) send(event fsnotify.Event) {
	w.events <- event
}

func (w *fakeFileWatcher) sendIfWatched(event fsnotify.Event) bool {
	if !w.isWatchingPath(event.Name) {
		return false
	}
	w.send(event)
	return true
}

func (w *fakeFileWatcher) setRemoveErr(path string, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.removeErrs[filepath.Clean(path)] = err
}

func (w *fakeFileWatcher) setCloseErr(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.closeErr = err
}

func (w *fakeFileWatcher) countAddCalls(path string) int {
	w.mu.Lock()
	defer w.mu.Unlock()

	count := 0
	cleanPath := filepath.Clean(path)
	for _, addedPath := range w.addCalls {
		if addedPath == cleanPath {
			count++
		}
	}

	return count
}

func (w *fakeFileWatcher) countRemoveCalls(path string) int {
	w.mu.Lock()
	defer w.mu.Unlock()

	count := 0
	cleanPath := filepath.Clean(path)
	for _, removedPath := range w.removeCalls {
		if removedPath == cleanPath {
			count++
		}
	}

	return count
}

func (w *fakeFileWatcher) countCloseCalls() int {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.closeCalls
}

func (w *fakeFileWatcher) isWatchingPath(path string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	cleanPath := filepath.Clean(path)
	for watchedPath := range w.active {
		if pathWithinRoot(watchedPath, cleanPath) {
			return true
		}
	}

	return false
}

func (w *fakeFileWatcher) hasActiveWatch(path string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	_, ok := w.active[filepath.Clean(path)]
	return ok
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func waitForLockedBufferContains(t *testing.T, buffer *lockedBuffer, want string) {
	t.Helper()
	waitForLockedBufferContainsWithin(t, buffer, want, 250*time.Millisecond)
}

func waitForLockedBufferContainsWithin(t *testing.T, buffer *lockedBuffer, want string, timeout time.Duration) {
	t.Helper()

	deadline := time.After(timeout)
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()

	for {
		if strings.Contains(buffer.String(), want) {
			return
		}

		select {
		case <-deadline:
			t.Fatalf("timed out waiting for output containing %q; got %q", want, buffer.String())
		case <-ticker.C:
		}
	}
}

func mkdirServeWatchDir(t *testing.T, path string) {
	t.Helper()

	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatalf("os.Mkdir(%q) error = %v", path, err)
	}
}

func sendServeWatchCreateFromWatchedParent(t *testing.T, watcher *fakeFileWatcher, path string) {
	t.Helper()

	if !watcher.sendIfWatched(fsnotify.Event{Name: path, Op: fsnotify.Create}) {
		t.Fatalf("create event for %q is not covered by any active watch", path)
	}
}

func writeServeWatchThemeConfig(t *testing.T, configPath string, themeRoot string) {
	t.Helper()

	content := strings.Join([]string{
		"title: Garden",
		"baseURL: https://example.com",
		"themes:",
		"  feature:",
		fmt.Sprintf("    root: %q", themeRoot),
		"defaultTheme: feature",
	}, "\n") + "\n"
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", configPath, err)
	}
}

func writeServeWatchThemeTemplates(t *testing.T, root string) {
	t.Helper()

	for _, name := range internalrender.RequiredHTMLTemplateNames {
		filePath := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
			t.Fatalf("os.MkdirAll(%q) error = %v", filepath.Dir(filePath), err)
		}
		if err := os.WriteFile(filePath, []byte("content"), 0o644); err != nil {
			t.Fatalf("os.WriteFile(%q) error = %v", filePath, err)
		}
	}
}

func waitForServeWatchSignal(t *testing.T, signal <-chan struct{}, label string) {
	t.Helper()
	waitForServeWatchSignalWithin(t, signal, label, 250*time.Millisecond)
}

func waitForServeWatchSignalWithin(t *testing.T, signal <-chan struct{}, label string, timeout time.Duration) {
	t.Helper()

	select {
	case <-signal:
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for %s", label)
	}
}

func waitForServeWatchAddCount(t *testing.T, watcher *fakeFileWatcher, path string, want int) {
	t.Helper()

	deadline := time.After(250 * time.Millisecond)
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()

	for {
		if got := watcher.countAddCalls(path); got >= want {
			return
		}

		select {
		case <-deadline:
			t.Fatalf("watcher.Add(%q) count did not reach %d", path, want)
		case <-ticker.C:
		}
	}
}

func waitForServeWatchRemoveCount(t *testing.T, watcher *fakeFileWatcher, path string, want int) {
	t.Helper()

	deadline := time.After(250 * time.Millisecond)
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()

	for {
		if got := watcher.countRemoveCalls(path); got >= want {
			return
		}

		select {
		case <-deadline:
			t.Fatalf("watcher.Remove(%q) count did not reach %d", path, want)
		case <-ticker.C:
		}
	}
}

func assertNoServeWatchSignal(t *testing.T, signal <-chan struct{}, errorSignal <-chan error, label string) {
	t.Helper()

	select {
	case <-signal:
		t.Fatalf("received unexpected rebuild for %s", label)
	case err := <-errorSignal:
		t.Fatalf("watch loop reported error during %s: %v", label, err)
	case <-time.After(40 * time.Millisecond):
	}
}

func assertNoServeWatchError(t *testing.T, errorSignal <-chan error) {
	t.Helper()

	select {
	case err := <-errorSignal:
		t.Fatalf("watch loop reported error: %v", err)
	default:
	}
}

func waitForServeWatchErrorContains(t *testing.T, errorSignal <-chan error, want string) {
	t.Helper()

	select {
	case err := <-errorSignal:
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("watch loop error = %q, want substring %q", err.Error(), want)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatalf("timed out waiting for watch loop error containing %q", want)
	}
}

func drainServeWatchSignals(signal <-chan struct{}) {
	for {
		select {
		case <-signal:
		default:
			return
		}
	}
}

func filesystemRootForPath(path string) string {
	cleanPath := filepath.Clean(path)
	volume := filepath.VolumeName(cleanPath)
	if volume != "" {
		return volume + string(filepath.Separator)
	}

	return string(filepath.Separator)
}
