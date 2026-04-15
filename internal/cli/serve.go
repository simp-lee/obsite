package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	internalasset "github.com/simp-lee/obsite/internal/asset"
	internalbuild "github.com/simp-lee/obsite/internal/build"
	internalconfig "github.com/simp-lee/obsite/internal/config"
	internalserver "github.com/simp-lee/obsite/internal/server"
	"github.com/spf13/cobra"
)

const defaultWatchDebounce = 350 * time.Millisecond

const watchRelevantOps = fsnotify.Create | fsnotify.Write | fsnotify.Remove | fsnotify.Rename

type serveWatchInputKind uint8

const (
	serveWatchInputDir serveWatchInputKind = iota
	serveWatchInputFile
)

type serveWatchInput struct {
	path string
	kind serveWatchInputKind
}

type fileWatcher interface {
	Add(name string) error
	Remove(name string) error
	Close() error
	Events() <-chan fsnotify.Event
	Errors() <-chan error
}

type fsnotifyWatcher struct {
	watcher *fsnotify.Watcher
}

type serveWatchLoop struct {
	watcher                 fileWatcher
	vaultPath               string
	outputPath              string
	configPath              string
	extraWatchInputs        []serveWatchInput
	currentExtraWatchInputs func() []serveWatchInput
	debounce                time.Duration
	rebuild                 func() error
	notifyReload            func()
	onError                 func(error)
	watchedDirs             map[string]struct{}
	vaultWatchDirs          map[string]struct{}
	configWatchDirs         map[string]struct{}
	extraWatchDirs          map[string]struct{}
}

func newServeCommand(deps commandDependencies) *cobra.Command {
	var outputPath string
	var vaultPath string
	var configPath string
	var port int
	var watch bool

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve a generated site for local preview",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			trimmedOutputPath, err := requiredPathFlag("output", outputPath)
			if err != nil {
				return err
			}

			if watch {
				return runServeWatchMode(cmd, deps, trimmedOutputPath, vaultPath, configPath, port)
			}

			srv, err := deps.newPreviewServer(trimmedOutputPath, port)
			if err != nil {
				return fmt.Errorf("create preview server: %w", err)
			}

			if err := srv.ListenAndServe(); err != nil {
				return fmt.Errorf("listen and serve: %w", err)
			}

			return nil
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&outputPath, "output", "", "Path to the generated site output")
	flags.StringVar(&vaultPath, "vault", "", "Path to the Obsidian vault (required when --watch is enabled)")
	flags.StringVar(&configPath, "config", "", "Path to obsite.yaml (defaults to <vault>/obsite.yaml when --watch is enabled)")
	flags.IntVar(&port, "port", 0, fmt.Sprintf("Port for the local preview server (default %d)", internalserver.DefaultPort))
	flags.BoolVar(&watch, "watch", false, "Rebuild on vault/config changes and live-reload connected browsers")
	_ = cmd.MarkFlagRequired("output")

	return cmd
}

func runServeWatchMode(cmd *cobra.Command, deps commandDependencies, outputPath string, vaultPath string, configPath string, port int) error {
	trimmedVaultPath := strings.TrimSpace(vaultPath)
	if trimmedVaultPath == "" {
		return fmt.Errorf("--vault is required when --watch is enabled")
	}

	normalizedVaultPath, err := internalbuild.NormalizeVaultPath(trimmedVaultPath)
	if err != nil {
		return err
	}

	resolvedConfigPath, err := resolveBuildConfigPath(normalizedVaultPath, configPath)
	if err != nil {
		return err
	}

	absoluteOutputPath, err := filepath.Abs(outputPath)
	if err != nil {
		return fmt.Errorf("resolve output path %q: %w", outputPath, err)
	}

	var currentExtraWatchInputs []serveWatchInput

	build := func() error {
		input, err := deps.loadSiteInput(resolvedConfigPath, internalconfig.Overrides{VaultPath: normalizedVaultPath})
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}
		cfg := input.Config

		nextExtraWatchInputs := collectServeWatchInputs(cfg.TemplateDir, cfg.CustomCSS, normalizedVaultPath)
		currentExtraWatchInputs = nextExtraWatchInputs

		if _, err := deps.buildSiteWithOptions(input, normalizedVaultPath, outputPath, internalbuild.Options{DiagnosticsWriter: cmd.ErrOrStderr()}); err != nil {
			return fmt.Errorf("build site: %w", err)
		}

		return nil
	}

	if err := build(); err != nil {
		return err
	}

	srv, err := deps.newPreviewServer(outputPath, port)
	if err != nil {
		return fmt.Errorf("create preview server: %w", err)
	}
	srv.EnableLiveReload()

	watcher, err := deps.newFileWatcher()
	if err != nil {
		return fmt.Errorf("create file watcher: %w", err)
	}

	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	if err := startServeWatchLoop(ctx, serveWatchLoop{
		watcher:          watcher,
		vaultPath:        normalizedVaultPath,
		outputPath:       absoluteOutputPath,
		configPath:       resolvedConfigPath,
		extraWatchInputs: currentExtraWatchInputs,
		currentExtraWatchInputs: func() []serveWatchInput {
			return append([]serveWatchInput(nil), currentExtraWatchInputs...)
		},
		debounce: defaultWatchDebounce,
		rebuild:  build,
		notifyReload: func() {
			srv.NotifyReload()
		},
		onError: func(err error) {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "watch: %v\n", err)
		},
	}); err != nil {
		return fmt.Errorf("start watch loop: %w", err)
	}

	if err := srv.ListenAndServe(); err != nil {
		return fmt.Errorf("listen and serve: %w", err)
	}

	return nil
}

func newFSNotifyWatcher() (fileWatcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	return &fsnotifyWatcher{watcher: watcher}, nil
}

func (w *fsnotifyWatcher) Add(name string) error {
	return w.watcher.Add(name)
}

func (w *fsnotifyWatcher) Remove(name string) error {
	return w.watcher.Remove(name)
}

func (w *fsnotifyWatcher) Close() error {
	return w.watcher.Close()
}

func (w *fsnotifyWatcher) Events() <-chan fsnotify.Event {
	return w.watcher.Events
}

func (w *fsnotifyWatcher) Errors() <-chan error {
	return w.watcher.Errors
}

func startServeWatchLoop(ctx context.Context, loop serveWatchLoop) error {
	if loop.watcher == nil {
		return fmt.Errorf("watcher is nil")
	}
	if loop.rebuild == nil {
		return closeServeWatchLoopWatcher(fmt.Errorf("rebuild function is nil"), loop.watcher)
	}
	if loop.debounce <= 0 {
		loop.debounce = defaultWatchDebounce
	}
	loop.extraWatchInputs = normalizeServeWatchInputs(loop.extraWatchInputs)
	loop.watchedDirs = make(map[string]struct{})
	loop.vaultWatchDirs = make(map[string]struct{})
	loop.configWatchDirs = make(map[string]struct{})
	loop.extraWatchDirs = make(map[string]struct{})

	if err := loop.addVaultTree(loop.vaultPath); err != nil {
		return closeServeWatchLoopWatcher(err, loop.watcher)
	}
	if err := loop.addConfigParentWatch(); err != nil {
		return closeServeWatchLoopWatcher(err, loop.watcher)
	}
	if err := loop.addExtraInputWatches(); err != nil {
		return closeServeWatchLoopWatcher(err, loop.watcher)
	}

	go loop.run(ctx)
	return nil
}

func (loop *serveWatchLoop) run(ctx context.Context) {
	defer loop.closeWatcher()

	var timer *time.Timer
	var timerC <-chan time.Time

	for {
		select {
		case <-ctx.Done():
			if timer != nil {
				stopWatchTimer(timer)
			}
			return
		case err, ok := <-loop.watcher.Errors():
			if !ok {
				return
			}
			if err != nil {
				loop.reportError(fmt.Errorf("watcher error: %w", err))
			}
		case event, ok := <-loop.watcher.Events():
			if !ok {
				return
			}

			cleanPath := filepath.Clean(event.Name)
			effectiveOp := relevantWatchOp(cleanPath, event.Op)
			if effectiveOp == 0 {
				continue
			}

			wasWatchedDir := loop.isWatchedDir(cleanPath)
			if hasDirectoryLifecycleOp(effectiveOp) {
				loop.removeWatchedDirSubtree(cleanPath)
			}
			if err := loop.addNewDirectoryWatch(cleanPath); err != nil {
				loop.reportError(err)
			}
			if !loop.shouldTrigger(cleanPath, effectiveOp, wasWatchedDir) {
				continue
			}

			if timer == nil {
				timer = time.NewTimer(loop.debounce)
				timerC = timer.C
				continue
			}

			stopWatchTimer(timer)
			timer.Reset(loop.debounce)
			timerC = timer.C
		case <-timerC:
			timerC = nil
			rebuildErr := loop.rebuild()
			if loop.currentExtraWatchInputs != nil {
				if err := loop.syncExtraWatchInputs(loop.currentExtraWatchInputs()); err != nil {
					loop.reportError(err)
				}
			}
			if rebuildErr != nil {
				loop.reportError(rebuildErr)
				continue
			}
			if loop.notifyReload != nil {
				loop.notifyReload()
			}
		}
	}
}

func closeServeWatchLoopWatcher(cause error, watcher fileWatcher) error {
	if watcher == nil {
		return cause
	}

	if err := watcher.Close(); err != nil {
		return errors.Join(cause, fmt.Errorf("close watcher: %w", err))
	}

	return cause
}

func (loop *serveWatchLoop) closeWatcher() {
	if loop == nil || loop.watcher == nil {
		return
	}

	if err := loop.watcher.Close(); err != nil {
		loop.reportError(fmt.Errorf("close watcher: %w", err))
	}
}

func (loop *serveWatchLoop) addVaultTree(root string) error {
	return filepath.WalkDir(root, func(current string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			return nil
		}

		cleanPath := filepath.Clean(current)
		if loop.pathIsOutput(cleanPath) {
			return filepath.SkipDir
		}
		if !loop.shouldWatchVaultDirectory(cleanPath) {
			if cleanPath == loop.vaultPath {
				return nil
			}
			return filepath.SkipDir
		}

		return loop.addVaultWatchDir(cleanPath)
	})
}

func (loop *serveWatchLoop) addConfigParentWatch() error {
	loop.configWatchDirs = loop.watchDirsForInputs([]serveWatchInput{{path: loop.configPath, kind: serveWatchInputFile}})
	for watchDir := range loop.configWatchDirs {
		if err := loop.addWatchDir(watchDir); err != nil {
			return err
		}
	}

	return nil
}

func (loop *serveWatchLoop) addExtraInputWatches() error {
	return loop.syncExtraWatchInputs(loop.extraWatchInputs)
}

func (loop *serveWatchLoop) syncExtraWatchInputs(inputs []serveWatchInput) error {
	normalizedInputs := normalizeServeWatchInputs(inputs)
	nextWatchDirs := loop.watchDirsForInputs(normalizedInputs)

	for watchDir := range nextWatchDirs {
		if _, ok := loop.extraWatchDirs[watchDir]; ok {
			continue
		}
		if err := loop.addWatchDir(watchDir); err != nil {
			return err
		}
	}

	for watchDir := range loop.extraWatchDirs {
		if _, ok := nextWatchDirs[watchDir]; ok {
			continue
		}
		if loop.shouldRetainBaseWatchDir(watchDir) {
			continue
		}
		if err := loop.removeWatchDir(watchDir); err != nil {
			return err
		}
	}

	loop.extraWatchInputs = normalizedInputs
	loop.extraWatchDirs = nextWatchDirs
	return nil
}

func (loop *serveWatchLoop) watchDirsForInputs(inputs []serveWatchInput) map[string]struct{} {
	watchDirs := make(map[string]struct{}, len(inputs))
	for _, input := range inputs {
		cleanInput := filepath.Clean(input.path)
		if loop.pathIsOutput(cleanInput) {
			continue
		}
		if loop.pathWithinVault(cleanInput) && !shouldExplicitlyWatchConfiguredPath(cleanInput, loop.vaultPath) {
			continue
		}

		targetWatchDir := serveWatchDir(input)
		if strings.TrimSpace(targetWatchDir) == "" || targetWatchDir == "." {
			continue
		}

		watchDir := nearestExistingWatchDir(targetWatchDir)
		if strings.TrimSpace(watchDir) == "" || watchDir == "." || loop.pathIsOutput(watchDir) {
			continue
		}
		if isFilesystemRoot(watchDir) && filepath.Clean(watchDir) != filepath.Clean(targetWatchDir) {
			continue
		}

		watchDirs[filepath.Clean(watchDir)] = struct{}{}
		if filepath.Clean(watchDir) != filepath.Clean(targetWatchDir) {
			continue
		}

		recoveryDir := nearestExistingWatchDir(filepath.Dir(targetWatchDir))
		if strings.TrimSpace(recoveryDir) == "" || recoveryDir == "." || filepath.Clean(recoveryDir) == filepath.Clean(watchDir) || isFilesystemRoot(recoveryDir) || loop.pathIsOutput(recoveryDir) {
			continue
		}

		watchDirs[filepath.Clean(recoveryDir)] = struct{}{}
	}

	return watchDirs
}

func (loop *serveWatchLoop) shouldRetainBaseWatchDir(path string) bool {
	cleanPath := filepath.Clean(path)
	if _, ok := loop.configWatchDirs[cleanPath]; ok {
		return true
	}
	_, ok := loop.vaultWatchDirs[cleanPath]
	return ok
}

func (loop *serveWatchLoop) addNewDirectoryWatch(path string) error {
	if path == "" || loop.pathIsOutput(path) {
		return nil
	}

	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return nil
	}
	if loop.shouldDirectlyWatchDirectory(path) {
		return loop.addWatchDir(path)
	}
	if !loop.pathWithinVault(path) || !loop.shouldWatchVaultDirectory(path) {
		return nil
	}

	return loop.addVaultTree(path)
}

func (loop *serveWatchLoop) shouldDirectlyWatchDirectory(path string) bool {
	cleanPath := filepath.Clean(path)
	for _, input := range loop.extraWatchInputs {
		if filepath.Clean(serveWatchDir(input)) == cleanPath {
			return true
		}
	}

	if strings.TrimSpace(loop.configPath) == "" {
		return false
	}

	return filepath.Clean(filepath.Dir(loop.configPath)) == cleanPath && !loop.pathWithinVault(cleanPath)
}

func (loop *serveWatchLoop) addWatchDir(path string) error {
	cleanPath := filepath.Clean(path)
	if _, ok := loop.watchedDirs[cleanPath]; ok {
		return nil
	}
	if err := loop.watcher.Add(cleanPath); err != nil {
		return fmt.Errorf("watch %q: %w", cleanPath, err)
	}
	loop.watchedDirs[cleanPath] = struct{}{}
	return nil
}

func (loop *serveWatchLoop) addVaultWatchDir(path string) error {
	cleanPath := filepath.Clean(path)
	if err := loop.addWatchDir(cleanPath); err != nil {
		return err
	}
	loop.vaultWatchDirs[cleanPath] = struct{}{}
	return nil
}

func (loop *serveWatchLoop) isWatchedDir(path string) bool {
	_, ok := loop.watchedDirs[filepath.Clean(path)]
	return ok
}

func (loop *serveWatchLoop) removeWatchedDirSubtree(path string) {
	cleanPath := filepath.Clean(path)
	for watchedPath := range loop.watchedDirs {
		if watchedPath == cleanPath || pathWithinRoot(cleanPath, watchedPath) {
			if err := loop.removeWatchDir(watchedPath); err != nil {
				loop.reportError(err)
			}
		}
	}
}

func (loop *serveWatchLoop) removeWatchDir(path string) error {
	cleanPath := filepath.Clean(path)
	delete(loop.watchedDirs, cleanPath)
	delete(loop.vaultWatchDirs, cleanPath)
	if err := loop.watcher.Remove(cleanPath); err != nil && !errors.Is(err, fsnotify.ErrNonExistentWatch) {
		return fmt.Errorf("unwatch %q: %w", cleanPath, err)
	}

	return nil
}

func (loop *serveWatchLoop) shouldTrigger(path string, op fsnotify.Op, wasWatchedDir bool) bool {
	if !hasRelevantWatchOp(op) {
		return false
	}
	if path == "" || loop.pathIsOutput(path) {
		return false
	}
	cleanPath := filepath.Clean(path)
	if cleanPath == filepath.Clean(loop.configPath) {
		return true
	}
	if loop.matchesExtraWatchInput(cleanPath, op) {
		return true
	}
	if !loop.pathWithinVault(cleanPath) {
		return false
	}

	relPath, err := filepath.Rel(loop.vaultPath, cleanPath)
	if err != nil {
		return false
	}
	relPath = filepath.Clean(relPath)
	if relPath == "." {
		return false
	}
	if relPath == filepath.Join(".obsidian", "app.json") {
		return true
	}
	if !isWatchableVaultPath(relPath) {
		return false
	}
	if isWatchableDirectoryEvent(cleanPath, op, wasWatchedDir) {
		return true
	}

	return isWatchableVaultFile(relPath)
}

func (loop *serveWatchLoop) matchesExtraWatchInput(path string, op fsnotify.Op) bool {
	cleanPath := filepath.Clean(path)
	for _, input := range loop.extraWatchInputs {
		trimmedInput := strings.TrimSpace(input.path)
		if trimmedInput == "" {
			continue
		}

		cleanInput := filepath.Clean(trimmedInput)
		if pathWithinRoot(cleanInput, cleanPath) {
			return true
		}
		if hasDirectoryLifecycleOp(op) && pathWithinRoot(cleanPath, cleanInput) {
			return true
		}
	}

	return false
}

func hasRelevantWatchOp(op fsnotify.Op) bool {
	return op&watchRelevantOps != 0
}

func relevantWatchOp(path string, op fsnotify.Op) fsnotify.Op {
	relevantOp := op & watchRelevantOps
	if relevantOp != 0 {
		return relevantOp
	}
	if op&fsnotify.Chmod != 0 && watchPathMissing(path) {
		return fsnotify.Remove
	}

	return 0
}

func hasDirectoryLifecycleOp(op fsnotify.Op) bool {
	return op&(fsnotify.Remove|fsnotify.Rename) != 0
}

func watchPathMissing(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}

	_, err := os.Lstat(path)
	return os.IsNotExist(err)
}

func isWatchableDirectoryEvent(path string, op fsnotify.Op, wasWatchedDir bool) bool {
	if op&(fsnotify.Create|fsnotify.Remove|fsnotify.Rename) == 0 {
		return false
	}
	if wasWatchedDir {
		return true
	}

	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func isWatchableVaultPath(relPath string) bool {
	parts := splitWatchPath(relPath)
	for _, part := range parts {
		if part == "node_modules" {
			return false
		}
		if strings.HasPrefix(part, ".") {
			return false
		}
	}

	return true
}

func isWatchableVaultFile(relPath string) bool {
	return internalasset.IsPublishableAssetPath(filepath.Clean(relPath))
}

func (loop *serveWatchLoop) shouldWatchVaultDirectory(path string) bool {
	relPath, err := filepath.Rel(loop.vaultPath, path)
	if err != nil {
		return false
	}
	relPath = filepath.Clean(relPath)
	if relPath == "." {
		return true
	}
	if relPath == ".obsidian" {
		return true
	}

	parts := splitWatchPath(relPath)
	for _, part := range parts {
		if part == "node_modules" {
			return false
		}
		if strings.HasPrefix(part, ".") {
			return false
		}
	}

	return true
}

func (loop *serveWatchLoop) pathIsOutput(path string) bool {
	if strings.TrimSpace(loop.outputPath) == "" {
		return false
	}
	return pathWithinRoot(loop.outputPath, path)
}

func (loop *serveWatchLoop) pathWithinVault(path string) bool {
	return pathWithinRoot(loop.vaultPath, path)
}

func (loop *serveWatchLoop) reportError(err error) {
	if err == nil || loop.onError == nil {
		return
	}
	loop.onError(err)
}

func splitWatchPath(relPath string) []string {
	if relPath == "." || relPath == "" {
		return nil
	}

	return strings.Split(relPath, string(filepath.Separator))
}

func pathWithinRoot(root string, candidate string) bool {
	root = filepath.Clean(root)
	candidate = filepath.Clean(candidate)
	relPath, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	if relPath == "." {
		return true
	}

	return relPath != ".." && !strings.HasPrefix(relPath, ".."+string(filepath.Separator))
}

func normalizeServeWatchInputs(inputs []serveWatchInput) []serveWatchInput {
	if len(inputs) == 0 {
		return nil
	}

	normalized := make([]serveWatchInput, 0, len(inputs))
	seen := make(map[string]struct{}, len(inputs))
	for _, input := range inputs {
		trimmedInput := strings.TrimSpace(input.path)
		if trimmedInput == "" {
			continue
		}

		cleanInput := filepath.Clean(trimmedInput)
		key := fmt.Sprintf("%d:%s", input.kind, cleanInput)
		if _, ok := seen[key]; ok {
			continue
		}

		seen[key] = struct{}{}
		normalized = append(normalized, serveWatchInput{path: cleanInput, kind: input.kind})
	}

	if len(normalized) == 0 {
		return nil
	}

	return normalized
}

func collectServeWatchInputs(templateDir string, customCSSPath string, vaultPath string) []serveWatchInput {
	inputs := make([]serveWatchInput, 0, 2)
	for _, input := range []serveWatchInput{{path: templateDir, kind: serveWatchInputDir}, {path: customCSSPath, kind: serveWatchInputFile}} {
		trimmedInput := strings.TrimSpace(input.path)
		if trimmedInput == "" {
			continue
		}

		cleanInput := filepath.Clean(trimmedInput)
		if pathWithinRoot(vaultPath, cleanInput) && !shouldExplicitlyWatchConfiguredPath(cleanInput, vaultPath) {
			continue
		}

		inputs = append(inputs, serveWatchInput{path: cleanInput, kind: input.kind})
	}

	return normalizeServeWatchInputs(inputs)
}

func serveWatchDir(input serveWatchInput) string {
	cleanPath := filepath.Clean(input.path)
	if strings.TrimSpace(cleanPath) == "" {
		return ""
	}
	if input.kind == serveWatchInputDir {
		return cleanPath
	}

	return filepath.Dir(cleanPath)
}

func nearestExistingWatchDir(path string) string {
	cleanPath := filepath.Clean(path)
	if strings.TrimSpace(cleanPath) == "" || cleanPath == "." {
		return ""
	}

	for {
		info, err := os.Stat(cleanPath)
		if err == nil && info.IsDir() {
			return cleanPath
		}

		parent := filepath.Dir(cleanPath)
		if parent == cleanPath {
			return ""
		}
		cleanPath = parent
	}
}

func isFilesystemRoot(path string) bool {
	cleanPath := filepath.Clean(path)
	return filepath.Dir(cleanPath) == cleanPath
}

func shouldExplicitlyWatchConfiguredPath(path string, vaultPath string) bool {
	if strings.TrimSpace(vaultPath) == "" || !pathWithinRoot(vaultPath, path) {
		return false
	}

	relPath, err := filepath.Rel(vaultPath, path)
	if err != nil {
		return false
	}
	relPath = filepath.Clean(relPath)
	if relPath == "." || relPath == filepath.Join(".obsidian", "app.json") {
		return false
	}

	return !isWatchableVaultPath(relPath)
}

func stopWatchTimer(timer *time.Timer) {
	if timer == nil {
		return
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}
