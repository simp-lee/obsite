package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	internalbuild "github.com/simp-lee/obsite/internal/build"
	internalconfig "github.com/simp-lee/obsite/internal/config"
	"github.com/simp-lee/obsite/internal/model"
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

func TestServeCommandWatchBuildsBeforeServing(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "obsite.yaml")
	templateDir := filepath.Join(configDir, "templates")
	customCSSPath := filepath.Join(configDir, "styles", "custom.css")
	if err := os.WriteFile(configPath, []byte("title: ignored\nbaseURL: https://example.com\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", configPath, err)
	}
	if err := os.MkdirAll(templateDir, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v", templateDir, err)
	}
	if err := os.MkdirAll(filepath.Dir(customCSSPath), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v", filepath.Dir(customCSSPath), err)
	}
	if err := os.WriteFile(customCSSPath, []byte("body { color: tomato; }\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", customCSSPath, err)
	}
	outputPath := filepath.Join(t.TempDir(), "site")

	deps := testCommandDependencies()
	server := &fakePreviewServer{}
	watcher := newFakeFileWatcher()
	var gotConfigPath string
	var gotOverrides internalconfig.Overrides
	var gotVaultPath string
	var gotOutputPath string
	deps.loadConfig = func(path string, overrides internalconfig.Overrides) (model.SiteConfig, error) {
		gotConfigPath = path
		gotOverrides = overrides
		return model.SiteConfig{Title: "Garden", BaseURL: "https://example.com/", TemplateDir: templateDir, CustomCSS: customCSSPath}, nil
	}
	deps.buildSite = func(cfg model.SiteConfig, vaultPath string, outputPath string) (*internalbuild.BuildResult, error) {
		gotVaultPath = vaultPath
		gotOutputPath = outputPath
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

	stdout, stderr, err := executeForTest(t, deps, []string{"serve", "--output", outputPath, "--watch", "--vault", vaultPath, "--config", configPath})
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
	if server.enableCalls != 1 {
		t.Fatalf("EnableLiveReload calls = %d, want 1 in watch mode", server.enableCalls)
	}
	waitForServeWatchAddCount(t, watcher, templateDir, 1)
	waitForServeWatchAddCount(t, watcher, filepath.Dir(customCSSPath), 1)
	if server.listenCalls != 1 {
		t.Fatalf("ListenAndServe calls = %d, want 1", server.listenCalls)
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

func TestStartServeWatchLoopRebuildsForAttachmentsAndCustomCSS(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	configDir := t.TempDir()
	attachmentPath := filepath.Join(vaultPath, "files", "manual.pdf")
	customCSSPath := filepath.Join(vaultPath, "custom.css")
	externalCustomCSSPath := filepath.Join(configDir, "styles", "custom.css")
	configPath := filepath.Join(vaultPath, defaultConfigFilename)
	outputPath := filepath.Join(vaultPath, "public")
	for _, filePath := range []string{attachmentPath, customCSSPath, externalCustomCSSPath, configPath} {
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
		extraWatchInputs: []serveWatchInput{{path: externalCustomCSSPath, kind: serveWatchInputFile}},
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
	waitForServeWatchAddCount(t, watcher, filepath.Dir(externalCustomCSSPath), 1)

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
			name: "config-relative custom.css write",
			path: externalCustomCSSPath,
			op:   fsnotify.Write,
			before: func(t *testing.T) {
				t.Helper()
				if err := os.WriteFile(externalCustomCSSPath, []byte("body { color: royalblue; }\n"), 0o644); err != nil {
					t.Fatalf("os.WriteFile(%q) error = %v", externalCustomCSSPath, err)
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

func TestStartServeWatchLoopRecoversExternalTemplateDirAfterFailedRebuild(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	configPath := filepath.Join(vaultPath, defaultConfigFilename)
	outputPath := filepath.Join(vaultPath, "public")
	oldTemplateDir := filepath.Join(t.TempDir(), "templates-a")
	oldTemplatePath := filepath.Join(oldTemplateDir, "base.html")
	brokenTemplateRoot := t.TempDir()
	brokenTemplateDir := filepath.Join(brokenTemplateRoot, "templates-b")
	brokenTemplatePath := filepath.Join(brokenTemplateDir, "base.html")
	for _, filePath := range []string{configPath, oldTemplatePath} {
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

	currentExtraWatchInputs := []serveWatchInput{{path: oldTemplateDir, kind: serveWatchInputDir}}
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
				currentExtraWatchInputs = []serveWatchInput{{path: brokenTemplateDir, kind: serveWatchInputDir}}
				return errors.New("missing external template dir")
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

	waitForServeWatchAddCount(t, watcher, oldTemplateDir, 1)

	watcher.send(fsnotify.Event{Name: configPath, Op: fsnotify.Write})
	waitForServeWatchErrorContains(t, errorSignal, "missing external template dir")
	waitForServeWatchAddCount(t, watcher, brokenTemplateRoot, 1)
	waitForServeWatchRemoveCount(t, watcher, oldTemplateDir, 1)

	if err := os.MkdirAll(brokenTemplateDir, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v", brokenTemplateDir, err)
	}
	if err := os.WriteFile(brokenTemplatePath, []byte("content"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", brokenTemplatePath, err)
	}

	watcher.send(fsnotify.Event{Name: brokenTemplateDir, Op: fsnotify.Create})
	waitForServeWatchAddCount(t, watcher, brokenTemplateDir, 1)
	waitForServeWatchSignal(t, rebuildSignal, "recreated external template dir rebuild")
	assertNoServeWatchError(t, errorSignal)

	if err := os.WriteFile(brokenTemplatePath, []byte("updated"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", brokenTemplatePath, err)
	}
	watcher.send(fsnotify.Event{Name: brokenTemplatePath, Op: fsnotify.Write})
	waitForServeWatchSignal(t, rebuildSignal, "fixed external template dir rebuild")
	assertNoServeWatchError(t, errorSignal)
}

func TestStartServeWatchLoopReaddsRemovedOrRenamedExternalTemplateDir(t *testing.T) {
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
			externalTemplateDir := filepath.Join(externalRoot, "templates")
			externalTemplatePath := filepath.Join(externalTemplateDir, "base.html")
			for _, filePath := range []string{configPath, externalTemplatePath} {
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
				extraWatchInputs: []serveWatchInput{{path: externalTemplateDir, kind: serveWatchInputDir}},
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

			waitForServeWatchAddCount(t, watcher, externalTemplateDir, 1)
			waitForServeWatchAddCount(t, watcher, externalRoot, 1)

			tt.removeWatched(t, externalTemplateDir)
			watcher.send(fsnotify.Event{Name: externalTemplateDir, Op: tt.op})
			waitForServeWatchRemoveCount(t, watcher, externalTemplateDir, 1)
			waitForServeWatchSignal(t, rebuildSignal, "external template dir removal rebuild")
			assertNoServeWatchError(t, errorSignal)

			if err := os.MkdirAll(externalTemplateDir, 0o755); err != nil {
				t.Fatalf("os.MkdirAll(%q) error = %v", externalTemplateDir, err)
			}
			if err := os.WriteFile(externalTemplatePath, []byte("restored"), 0o644); err != nil {
				t.Fatalf("os.WriteFile(%q) error = %v", externalTemplatePath, err)
			}

			watcher.send(fsnotify.Event{Name: externalTemplateDir, Op: fsnotify.Create})
			waitForServeWatchAddCount(t, watcher, externalTemplateDir, 2)
			waitForServeWatchSignal(t, rebuildSignal, "recreated external template dir rebuild")
			assertNoServeWatchError(t, errorSignal)

			if err := os.WriteFile(externalTemplatePath, []byte("updated"), 0o644); err != nil {
				t.Fatalf("os.WriteFile(%q) error = %v", externalTemplatePath, err)
			}
			watcher.send(fsnotify.Event{Name: externalTemplatePath, Op: fsnotify.Write})
			waitForServeWatchSignal(t, rebuildSignal, "recreated external template edit rebuild")
			assertNoServeWatchError(t, errorSignal)
		})
	}
}

func TestStartServeWatchLoopWatchesConfiguredHiddenInVaultTemplateDir(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	hiddenTemplateDir := filepath.Join(vaultPath, ".obsidian", "templates")
	hiddenTemplatePath := filepath.Join(hiddenTemplateDir, "base.html")
	configPath := filepath.Join(vaultPath, defaultConfigFilename)
	outputPath := filepath.Join(vaultPath, "public")
	for _, filePath := range []string{configPath, hiddenTemplatePath} {
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
		extraWatchInputs: collectServeWatchInputs(hiddenTemplateDir, "", vaultPath),
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

	waitForServeWatchAddCount(t, watcher, hiddenTemplateDir, 1)

	if err := os.WriteFile(hiddenTemplatePath, []byte("updated"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", hiddenTemplatePath, err)
	}
	watcher.send(fsnotify.Event{Name: hiddenTemplatePath, Op: fsnotify.Write})
	waitForServeWatchSignal(t, rebuildSignal, "hidden template override rebuild")
	assertNoServeWatchError(t, errorSignal)
}

func TestStartServeWatchLoopRefreshesExternalTemplateDirAndCustomCSSWatchesAfterRebuild(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	configPath := filepath.Join(vaultPath, defaultConfigFilename)
	outputPath := filepath.Join(vaultPath, "public")
	oldTemplateDir := filepath.Join(t.TempDir(), "templates-a")
	newTemplateDir := filepath.Join(t.TempDir(), "templates-b")
	oldTemplatePath := filepath.Join(oldTemplateDir, "base.html")
	newTemplatePath := filepath.Join(newTemplateDir, "base.html")
	oldCustomCSSPath := filepath.Join(t.TempDir(), "styles-a", "custom.css")
	newCustomCSSPath := filepath.Join(t.TempDir(), "styles-b", "custom.css")
	for _, filePath := range []string{configPath, oldTemplatePath, newTemplatePath, oldCustomCSSPath, newCustomCSSPath} {
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

	currentExtraWatchInputs := []serveWatchInput{{path: oldTemplateDir, kind: serveWatchInputDir}, {path: oldCustomCSSPath, kind: serveWatchInputFile}}
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
		debounce: 15 * time.Millisecond,
		rebuild: func() error {
			currentExtraWatchInputs = []serveWatchInput{{path: newTemplateDir, kind: serveWatchInputDir}, {path: newCustomCSSPath, kind: serveWatchInputFile}}
			rebuildSignal <- struct{}{}
			return nil
		},
		onError: func(err error) {
			errorSignal <- err
		},
	}); err != nil {
		t.Fatalf("startServeWatchLoop() error = %v", err)
	}

	waitForServeWatchAddCount(t, watcher, oldTemplateDir, 1)
	waitForServeWatchAddCount(t, watcher, filepath.Dir(oldCustomCSSPath), 1)

	watcher.send(fsnotify.Event{Name: configPath, Op: fsnotify.Write})
	waitForServeWatchSignal(t, rebuildSignal, "config rebuild")
	waitForServeWatchAddCount(t, watcher, newTemplateDir, 1)
	waitForServeWatchAddCount(t, watcher, filepath.Dir(newCustomCSSPath), 1)
	waitForServeWatchRemoveCount(t, watcher, oldTemplateDir, 1)
	waitForServeWatchRemoveCount(t, watcher, filepath.Dir(oldCustomCSSPath), 1)
	assertNoServeWatchError(t, errorSignal)

	watcher.send(fsnotify.Event{Name: oldTemplatePath, Op: fsnotify.Write})
	assertNoServeWatchSignal(t, rebuildSignal, errorSignal, "stale template override")

	watcher.send(fsnotify.Event{Name: oldCustomCSSPath, Op: fsnotify.Write})
	assertNoServeWatchSignal(t, rebuildSignal, errorSignal, "stale custom css")

	watcher.send(fsnotify.Event{Name: newTemplatePath, Op: fsnotify.Write})
	waitForServeWatchSignal(t, rebuildSignal, "refreshed template override rebuild")
	assertNoServeWatchError(t, errorSignal)

	watcher.send(fsnotify.Event{Name: newCustomCSSPath, Op: fsnotify.Write})
	waitForServeWatchSignal(t, rebuildSignal, "refreshed custom css rebuild")
	assertNoServeWatchError(t, errorSignal)
}

type fakePreviewServer struct {
	listenErr    error
	enableCalls  int
	listenCalls  int
	reloadCalls  int
	reloadCalled chan struct{}
}

func (s *fakePreviewServer) EnableLiveReload() {
	s.enableCalls++
}

func (s *fakePreviewServer) ListenAndServe() error {
	s.listenCalls++
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
	removeCalls []string
	removeErrs  map[string]error
	closeCalls  int
	events      chan fsnotify.Event
	errors      chan error
}

func newFakeFileWatcher() *fakeFileWatcher {
	return &fakeFileWatcher{
		events:     make(chan fsnotify.Event, 16),
		errors:     make(chan error, 4),
		removeErrs: make(map[string]error),
	}
}

func (w *fakeFileWatcher) Add(name string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.addCalls = append(w.addCalls, filepath.Clean(name))
	return nil
}

func (w *fakeFileWatcher) Remove(name string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	cleanPath := filepath.Clean(name)
	w.removeCalls = append(w.removeCalls, cleanPath)
	return w.removeErrs[cleanPath]
}

func (w *fakeFileWatcher) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closeCalls++
	return nil
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

func waitForServeWatchSignal(t *testing.T, signal <-chan struct{}, label string) {
	t.Helper()

	select {
	case <-signal:
	case <-time.After(250 * time.Millisecond):
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
