package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	internalbuild "github.com/simp-lee/obsite/internal/build"
	"github.com/simp-lee/obsite/internal/model"
)

func TestServeCommandDefaultsToCurrentVaultPublicOutput(t *testing.T) {
	vaultPath := t.TempDir()
	t.Chdir(vaultPath)
	deps := testCommandDependencies()
	server := &fakePreviewServer{}
	var gotOutput string
	deps.newPreviewServer = func(output string, port int) (previewServer, error) {
		gotOutput = output
		return server, nil
	}
	_, _, err := executeForTest(t, deps, []string{"serve"})
	if err != nil {
		t.Fatalf("executeForTest() error = %v", err)
	}
	if gotOutput != filepath.Join(vaultPath, "public") {
		t.Fatalf("output = %q", gotOutput)
	}
}

func TestServeCommandUsesServerDefaultPortWhenOmitted(t *testing.T) {
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
	deps.loadSiteInput = func(resolvedVault string) (internalbuild.SiteInput, error) {
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
		errCh <- executeWithDeps([]string{"serve", "--output", outputPath, "--watch", "--vault", vaultPath}, deps, &stdoutBuf, &stderrBuf)
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

func TestServeCommandRejectsRemovedFlags(t *testing.T) {
	for _, flag := range []string{"--config=other.yaml", "--theme=feature"} {
		_, _, err := executeForTest(t, testCommandDependencies(), []string{"serve", flag})
		if err == nil || !strings.Contains(err.Error(), "unknown flag") {
			t.Fatalf("serve %s error = %v", flag, err)
		}
	}
}

func TestServeCommandReportsMissingDefaultOutput(t *testing.T) {
	vaultPath := t.TempDir()
	t.Chdir(vaultPath)
	_, _, err := executeForTest(t, defaultCommandDependencies(), []string{"serve"})
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("executeForTest() error = %v", err)
	}
}

func TestServeCommandWatchDefaultsToCurrentVaultAndPublicOutput(t *testing.T) {
	vaultPath := t.TempDir()
	t.Chdir(vaultPath)
	writeCLIConfig(t, vaultPath)
	deps := testCommandDependencies()
	deps.loadSiteInput = func(vault string) (internalbuild.SiteInput, error) {
		return internalbuild.SiteInput{Config: model.SiteConfig{Title: "Garden", BaseURL: "https://example.com/"}}, nil
	}
	var builtVault, builtOutput, servedOutput string
	deps.buildSiteWithOptions = func(_ internalbuild.SiteInput, vault string, output string, _ internalbuild.Options) (*internalbuild.BuildResult, error) {
		builtVault, builtOutput = vault, output
		if err := os.MkdirAll(output, 0o755); err != nil {
			return nil, err
		}
		return &internalbuild.BuildResult{}, nil
	}
	server := &fakePreviewServer{}
	deps.newPreviewServer = func(output string, _ int) (previewServer, error) {
		servedOutput = output
		return server, nil
	}
	deps.newFileWatcher = func() (fileWatcher, error) { return newFakeFileWatcher(), nil }
	if _, _, err := executeForTest(t, deps, []string{"serve", "--watch"}); err != nil {
		t.Fatalf("executeForTest() error = %v", err)
	}
	wantOutput := filepath.Join(vaultPath, "public")
	if builtVault != vaultPath || builtOutput != wantOutput || servedOutput != wantOutput {
		t.Fatalf("paths = vault %q, build %q, serve %q", builtVault, builtOutput, servedOutput)
	}
}

func TestServeCommandWatchBuildsBeforeServing(t *testing.T) {
	vaultPath := t.TempDir()
	configPath := filepath.Join(vaultPath, defaultConfigFilename)
	if err := os.WriteFile(configPath, []byte("title: ignored\nbaseURL: https://example.com\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", configPath, err)
	}
	outputPath := filepath.Join(t.TempDir(), "site")

	deps := testCommandDependencies()
	server := &fakePreviewServer{}
	watcher := newFakeFileWatcher()
	var gotLoadedVault string
	var gotVaultPath string
	var gotOutputPath string
	expectedInput := internalbuild.SiteInput{Config: model.SiteConfig{Title: "Garden", BaseURL: "https://example.com/"}}
	deps.loadSiteInput = func(resolvedVault string) (internalbuild.SiteInput, error) {
		gotLoadedVault = resolvedVault
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

	stdout, stderr, err := executeForTest(t, deps, []string{"serve", "--output", outputPath, "--watch", "--vault", vaultPath})
	if err != nil {
		t.Fatalf("executeForTest() error = %v", err)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty stdout", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty stderr", stderr)
	}
	if gotLoadedVault != vaultPath {
		t.Fatalf("loaded vault = %q, want %q", gotLoadedVault, vaultPath)
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
	if server.listenCalls != 1 {
		t.Fatalf("ListenAndServe calls = %d, want 1", server.listenCalls)
	}
}

func TestServeCommandWatchReloadsFixedVaultConfig(t *testing.T) {
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
	var loadedVaultsMu sync.Mutex
	loadedVaults := make([]string, 0, 2)
	expectedInput := internalbuild.SiteInput{Config: model.SiteConfig{Title: "Garden", BaseURL: "https://example.com/"}}

	deps.loadSiteInput = func(resolvedVault string) (internalbuild.SiteInput, error) {
		loadedVaultsMu.Lock()
		loadedVaults = append(loadedVaults, resolvedVault)
		loadedVaultsMu.Unlock()
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
		errCh <- executeWithDeps([]string{"serve", "--output", outputPath, "--watch", "--vault", vaultPath}, deps, &stdoutBuf, &stderrBuf)
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

	loadedVaultsMu.Lock()
	gotVaults := append([]string(nil), loadedVaults...)
	loadedVaultsMu.Unlock()
	if len(gotVaults) < 2 || gotVaults[0] != vaultPath || gotVaults[1] != vaultPath {
		t.Fatalf("loadSiteInput vault calls = %#v, want %q twice", gotVaults, vaultPath)
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

func TestStartServeWatchLoopDebouncesRebuildsAndNotifiesReload(t *testing.T) {
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

	rebuildSignal := make(chan struct{}, 4)
	reloadSignal := make(chan struct{}, 4)
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
			reloadSignal <- struct{}{}
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
	waitForServeWatchSignal(t, reloadSignal, "vault reload")
	select {
	case <-rebuildSignal:
		t.Fatal("received unexpected second rebuild for debounced vault writes")
	case err := <-errorSignal:
		t.Fatalf("watch loop reported error: %v", err)
	case <-time.After(60 * time.Millisecond):
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
	waitForServeWatchSignal(t, reloadSignal, "config reload")
}

func TestStartServeWatchLoopReaddsRemovedOrRenamedDirectories(t *testing.T) {
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

func TestStartServeWatchLoopRebuildsForAttachmentsAndVaultCustomCSS(t *testing.T) {
	vaultPath := t.TempDir()
	attachmentPath := filepath.Join(vaultPath, "files", "manual.pdf")
	customCSSPath := filepath.Join(vaultPath, "custom.css")
	configPath := filepath.Join(vaultPath, defaultConfigFilename)
	outputPath := filepath.Join(vaultPath, "public")
	for _, filePath := range []string{attachmentPath, customCSSPath, configPath} {
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
			reloadSignal <- struct{}{}
		},
		onError: func(err error) {
			errorSignal <- err
		},
	}); err != nil {
		t.Fatalf("startServeWatchLoop() error = %v", err)
	}
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

func TestStartServeWatchLoopReportsWatcherCloseErrors(t *testing.T) {
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
