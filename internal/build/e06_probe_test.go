package build

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	internalconfig "github.com/simp-lee/obsite/internal/config"
	"github.com/simp-lee/obsite/internal/model"
)

type e06RenderCalls struct {
	Note     []string `json:"note"`
	Index    []string `json:"index"`
	Tag      []string `json:"tag"`
	Folder   []string `json:"folder"`
	Timeline []string `json:"timeline"`
}

type e06BuildObservation struct {
	NotePages       int            `json:"notePages"`
	Diagnostics     string         `json:"diagnostics"`
	RenderCalls     e06RenderCalls `json:"renderCalls,omitempty"`
	ManifestChanged []string       `json:"manifestChanged,omitempty"`
	ManifestStable  []string       `json:"manifestStable,omitempty"`
	FileChanged     []string       `json:"fileChanged,omitempty"`
	FileStable      []string       `json:"fileStable,omitempty"`
}

type e06ProbeReport struct {
	WorkRoot   string `json:"workRoot"`
	VaultPath  string `json:"vaultPath"`
	OutputPath string `json:"outputPath"`
	Cache      struct {
		ManifestRelPath   string `json:"manifestRelPath"`
		ManifestExists    bool   `json:"manifestExists"`
		LegacyCacheExists bool   `json:"legacyCacheExists"`
	} `json:"cache"`
	Baseline       e06BuildObservation `json:"baseline"`
	NoOp           e06BuildObservation `json:"noOp"`
	Mutation       e06BuildObservation `json:"mutation"`
	ArtifactChecks struct {
		PagefindEntryStableAfterNoOp      bool `json:"pagefindEntryStableAfterNoOp"`
		PagefindUIStableAfterNoOp         bool `json:"pagefindUIStableAfterNoOp"`
		CustomCSSStableAfterMutation      bool `json:"customCSSStableAfterMutation"`
		PagefindEntryChangedAfterMutation bool `json:"pagefindEntryChangedAfterMutation"`
		PagefindUIStableAfterMutation     bool `json:"pagefindUIStableAfterMutation"`
	} `json:"artifactChecks"`
	ContentChecks struct {
		ArchiveHasProbe               bool `json:"archiveHasProbe"`
		PageThreeHasProbe             bool `json:"pageThreeHasProbe"`
		NotesPageThreeHasFolderMarker bool `json:"notesPageThreeHasFolderMarker"`
		NotesPageThreeHasArchiveLink  bool `json:"notesPageThreeHasArchiveLink"`
	} `json:"contentChecks"`
	Monitored struct {
		NoOpStableFiles      []string `json:"noOpStableFiles"`
		MutationStableFiles  []string `json:"mutationStableFiles"`
		MutationManifestDiff []string `json:"mutationManifestDiff"`
	} `json:"monitored"`
	ThemeSwitch e06ThemeSwitchProbeReport `json:"themeSwitch,omitempty"`
}

type e06ThemeSwitchProbeReport struct {
	ComparedFiles          []string            `json:"comparedFiles,omitempty"`
	AlphaBaseline          e06BuildObservation `json:"alphaBaseline"`
	AlphaToBeta            e06BuildObservation `json:"alphaToBeta"`
	AlphaToEmbeddedDefault e06BuildObservation `json:"alphaToEmbeddedDefault"`
	ArtifactChecks         struct {
		AlphaToBetaStyleChanged                  bool `json:"alphaToBetaStyleChanged"`
		AlphaToBetaThemeAssetsChanged            bool `json:"alphaToBetaThemeAssetsChanged"`
		AlphaToEmbeddedDefaultStyleChanged       bool `json:"alphaToEmbeddedDefaultStyleChanged"`
		AlphaToEmbeddedDefaultThemeAssetsChanged bool `json:"alphaToEmbeddedDefaultThemeAssetsChanged"`
	} `json:"artifactChecks"`
}

type e06OutputSnapshot struct {
	Exists bool
	Data   []byte
}

func TestScopeE06FeatureVaultProbe(t *testing.T) {
	reportPath := strings.TrimSpace(os.Getenv("OBSITE_E06_REPORT_JSON"))
	workRoot := strings.TrimSpace(os.Getenv("OBSITE_E06_WORK_ROOT"))
	switch {
	case reportPath == "" && workRoot == "":
		t.Skip("set both OBSITE_E06_REPORT_JSON and OBSITE_E06_WORK_ROOT to run this probe")
	case reportPath == "":
		t.Fatal("OBSITE_E06_REPORT_JSON is required when OBSITE_E06_WORK_ROOT is set")
	case workRoot == "":
		t.Fatal("OBSITE_E06_WORK_ROOT is required when OBSITE_E06_REPORT_JSON is set")
	}
	if err := os.MkdirAll(workRoot, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v", workRoot, err)
	}

	vaultPath := filepath.Join(workRoot, "vault")
	outputPath := filepath.Join(workRoot, "site")
	if err := copyFixtureVaultToPath("feature-vault", vaultPath); err != nil {
		t.Fatalf("copyFixtureVaultToPath() error = %v", err)
	}

	configPath := filepath.Join(vaultPath, "obsite.yaml")
	loadedCfg, err := internalconfig.LoadForBuild(configPath, internalconfig.Overrides{VaultPath: vaultPath})
	if err != nil {
		t.Fatalf("config.LoadForBuild(%q) error = %v", configPath, err)
	}
	cfg := loadedCfg.Config

	var baselineDiagnostics bytes.Buffer
	baselineResult, err := buildWithOptions(cfg, vaultPath, outputPath, e06FeatureVaultBuildOptions(t, cfg, &baselineDiagnostics))
	if err != nil {
		t.Fatalf("first buildWithOptions() error = %v", err)
	}
	if baselineResult == nil {
		t.Fatal("first buildWithOptions() = nil result")
	}

	baselineManifest := readBuildCacheManifest(t, outputPath)
	manifestPath := filepath.Join(outputPath, filepath.FromSlash(cacheManifestRelPath))
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("os.Stat(%q) error = %v", manifestPath, err)
	}

	noOpStableFiles := []string{
		"index.html",
		"page/2/index.html",
		"page/3/index.html",
		"tags/field/index.html",
		"tags/field/page/2/index.html",
		"notes/index.html",
		"notes/page/2/index.html",
		"notes/page/3/index.html",
		"notes/garden/index.html",
		"notes/garden/page/2/index.html",
	}
	mutationStableFiles := []string{
		"index.html",
		"page/2/index.html",
		"tags/field/index.html",
		"tags/field/page/2/index.html",
		"notes/index.html",
		"notes/page/2/index.html",
		"notes/garden/index.html",
		"notes/garden/page/2/index.html",
	}
	allSnapshotFiles := append([]string{
		"archive/index.html",
		"assets/custom.css",
		"_pagefind/pagefind-entry.json",
		"_pagefind/pagefind-ui.js",
	}, noOpStableFiles...)
	baselineFiles := snapshotOutputFiles(t, outputPath, allSnapshotFiles)

	report := e06ProbeReport{
		WorkRoot:   workRoot,
		VaultPath:  vaultPath,
		OutputPath: outputPath,
		Baseline: e06BuildObservation{
			NotePages:   baselineResult.NotePages,
			Diagnostics: strings.TrimSpace(baselineDiagnostics.String()),
		},
	}
	report.Cache.ManifestRelPath = cacheManifestRelPath
	report.Cache.ManifestExists = pathExists(manifestPath)
	report.Cache.LegacyCacheExists = pathExists(filepath.Join(outputPath, ".obsite-cache.json"))
	report.Monitored.NoOpStableFiles = append([]string(nil), noOpStableFiles...)
	report.Monitored.MutationStableFiles = append([]string(nil), mutationStableFiles...)

	t.Run("no-op rebuild", func(t *testing.T) {
		getRenderedNotePaths := captureRenderedNotePagePaths(t)
		getRenderedIndexPaths := captureRenderedIndexPagePaths(t)
		getRenderedTagPaths := captureRenderedTagPagePaths(t)
		getRenderedFolderPaths := captureRenderedFolderPagePaths(t)
		getRenderedTimelinePaths := captureRenderedTimelinePagePaths(t)

		var diagnostics bytes.Buffer
		result, err := buildWithOptions(cfg, vaultPath, outputPath, e06FeatureVaultBuildOptions(t, cfg, &diagnostics))
		if err != nil {
			t.Fatalf("second buildWithOptions() error = %v", err)
		}
		if result == nil {
			t.Fatal("second buildWithOptions() = nil result")
		}

		manifest := readBuildCacheManifest(t, outputPath)
		changedManifest, stableManifest := diffManifestPages(baselineManifest.Pages, manifest.Pages)
		changedFiles, stableFiles := diffSnapshotFiles(baselineFiles, snapshotOutputFiles(t, outputPath, noOpStableFiles))

		report.NoOp = e06BuildObservation{
			NotePages:   result.NotePages,
			Diagnostics: strings.TrimSpace(diagnostics.String()),
			RenderCalls: e06RenderCalls{
				Note:     getRenderedNotePaths(),
				Index:    getRenderedIndexPaths(),
				Tag:      getRenderedTagPaths(),
				Folder:   getRenderedFolderPaths(),
				Timeline: getRenderedTimelinePaths(),
			},
			ManifestChanged: changedManifest,
			ManifestStable:  stableManifest,
			FileChanged:     changedFiles,
			FileStable:      stableFiles,
		}
		report.ArtifactChecks.PagefindEntryStableAfterNoOp = bytes.Equal(readBuildOutputFile(t, outputPath, "_pagefind/pagefind-entry.json"), baselineFiles["_pagefind/pagefind-entry.json"])
		report.ArtifactChecks.PagefindUIStableAfterNoOp = bytes.Equal(readBuildOutputFile(t, outputPath, "_pagefind/pagefind-ui.js"), baselineFiles["_pagefind/pagefind-ui.js"])
	})

	mutationPath := filepath.Join(vaultPath, "notes", "archive.md")
	const mutatedArchive = `---
title: Archive
date: 2026-04-01
---
# Archive

Archive entry captures a focused incremental rebuild probe with lighthouse ledger terms that stay isolated from the field-note corpus.
`
	if err := os.WriteFile(mutationPath, []byte(mutatedArchive), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", mutationPath, err)
	}
	fixtureStamp := time.Date(2026, time.April, 6, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(mutationPath, fixtureStamp, fixtureStamp); err != nil {
		t.Fatalf("os.Chtimes(%q) error = %v", mutationPath, err)
	}

	t.Run("targeted mutation", func(t *testing.T) {
		getRenderedNotePaths := captureRenderedNotePagePaths(t)
		getRenderedIndexPaths := captureRenderedIndexPagePaths(t)
		getRenderedTagPaths := captureRenderedTagPagePaths(t)
		getRenderedFolderPaths := captureRenderedFolderPagePaths(t)
		getRenderedTimelinePaths := captureRenderedTimelinePagePaths(t)

		var diagnostics bytes.Buffer
		result, err := buildWithOptions(cfg, vaultPath, outputPath, e06FeatureVaultBuildOptions(t, cfg, &diagnostics))
		if err != nil {
			t.Fatalf("third buildWithOptions() error = %v", err)
		}
		if result == nil {
			t.Fatal("third buildWithOptions() = nil result")
		}

		manifest := readBuildCacheManifest(t, outputPath)
		changedManifest, stableManifest := diffManifestPages(baselineManifest.Pages, manifest.Pages)
		changedFiles, stableFiles := diffSnapshotFiles(subsetSnapshot(baselineFiles, mutationStableFiles), snapshotOutputFiles(t, outputPath, mutationStableFiles))

		report.Mutation = e06BuildObservation{
			NotePages:   result.NotePages,
			Diagnostics: strings.TrimSpace(diagnostics.String()),
			RenderCalls: e06RenderCalls{
				Note:     getRenderedNotePaths(),
				Index:    getRenderedIndexPaths(),
				Tag:      getRenderedTagPaths(),
				Folder:   getRenderedFolderPaths(),
				Timeline: getRenderedTimelinePaths(),
			},
			ManifestChanged: changedManifest,
			ManifestStable:  stableManifest,
			FileChanged:     changedFiles,
			FileStable:      stableFiles,
		}
		report.Monitored.MutationManifestDiff = append([]string(nil), changedManifest...)

		archiveHTML := readBuildOutputFile(t, outputPath, "archive/index.html")
		pageThreeHTML := readBuildOutputFile(t, outputPath, "page/3/index.html")
		notesPageThreeHTML := readBuildOutputFile(t, outputPath, "notes/page/3/index.html")

		report.ArtifactChecks.CustomCSSStableAfterMutation = bytes.Equal(readBuildOutputFile(t, outputPath, "assets/custom.css"), baselineFiles["assets/custom.css"])
		report.ArtifactChecks.PagefindEntryChangedAfterMutation = !bytes.Equal(readBuildOutputFile(t, outputPath, "_pagefind/pagefind-entry.json"), baselineFiles["_pagefind/pagefind-entry.json"])
		report.ArtifactChecks.PagefindUIStableAfterMutation = bytes.Equal(readBuildOutputFile(t, outputPath, "_pagefind/pagefind-ui.js"), baselineFiles["_pagefind/pagefind-ui.js"])
		report.ContentChecks.ArchiveHasProbe = bytes.Contains(archiveHTML, []byte("focused incremental rebuild probe"))
		report.ContentChecks.PageThreeHasProbe = bytes.Contains(pageThreeHTML, []byte("focused incremental rebuild probe"))
		report.ContentChecks.NotesPageThreeHasFolderMarker = bytesContainsAny(notesPageThreeHTML, []byte(`data-e2e-custom-folder="notes"`), []byte(`data-e2e-custom-folder=notes`))
		report.ContentChecks.NotesPageThreeHasArchiveLink = bytesContainsAny(notesPageThreeHTML, []byte(`href="../../../archive/"`), []byte(`href=../../../archive/`))
	})

	report.ThemeSwitch = runE06ThemeSwitchProbe(t, workRoot)

	if err := writeE06ProbeReport(reportPath, report); err != nil {
		t.Fatalf("writeE06ProbeReport(%q) error = %v", reportPath, err)
	}
}

func e06FeatureVaultBuildOptions(t *testing.T, cfg model.SiteConfig, diagnosticsWriter io.Writer) buildOptions {
	t.Helper()

	return buildOptions{
		concurrency:       2,
		diagnosticsWriter: diagnosticsWriter,
		pagefindLookPath: func(name string) (string, error) {
			if name != cfg.Search.PagefindPath {
				t.Fatalf("pagefindLookPath() name = %q, want %q", name, cfg.Search.PagefindPath)
			}
			return "/usr/local/bin/pagefind_extended", nil
		},
		pagefindCommand: func(name string, args ...string) ([]byte, error) {
			if name != "/usr/local/bin/pagefind_extended" {
				t.Fatalf("pagefindCommand() name = %q, want %q", name, "/usr/local/bin/pagefind_extended")
			}
			if len(args) == 1 && args[0] == "--version" {
				return []byte("pagefind_extended 1.5.2\n"), nil
			}
			if len(args) != 4 || args[0] != "--site" || args[2] != "--output-subdir" || args[3] != pagefindOutputSubdir {
				t.Fatalf("pagefindCommand() args = %#v, want [--site <path> --output-subdir %s]", args, pagefindOutputSubdir)
			}

			bundlePath := filepath.Join(args[1], pagefindOutputSubdir)
			writeMinimalPagefindBundle(t, bundlePath)
			writeBuildTestFile(t, bundlePath, "pagefind-entry.json", fmt.Sprintf(
				`{"version":"1.5.2","e06Digest":"%s","languages":{"en":{"hash":"en-test","page_count":1}}}`,
				e06PagefindContentDigest(t, args[1]),
			))
			return []byte("Indexed 10 pages\n"), nil
		},
	}
}

func e06PagefindContentDigest(t *testing.T, sitePath string) string {
	t.Helper()

	hasher := sha256.New()
	relPaths := make([]string, 0, 16)
	pagefindRoot := filepath.Join(sitePath, pagefindOutputSubdir)
	err := filepath.Walk(sitePath, func(currentPath string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info == nil {
			return nil
		}
		if info.IsDir() {
			if filepath.Clean(currentPath) == filepath.Clean(pagefindRoot) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(currentPath), ".html") {
			return nil
		}

		relPath, err := filepath.Rel(sitePath, currentPath)
		if err != nil {
			return err
		}
		relPaths = append(relPaths, filepath.ToSlash(relPath))
		return nil
	})
	if err != nil {
		t.Fatalf("filepath.Walk(%q) error = %v", sitePath, err)
	}

	sort.Strings(relPaths)
	for _, relPath := range relPaths {
		absPath := filepath.Join(sitePath, filepath.FromSlash(relPath))
		data, err := os.ReadFile(absPath)
		if err != nil {
			t.Fatalf("os.ReadFile(%q) error = %v", absPath, err)
		}
		_, _ = hasher.Write([]byte(relPath))
		_, _ = hasher.Write([]byte{0})
		_, _ = hasher.Write(data)
		_, _ = hasher.Write([]byte{0})
	}

	return fmt.Sprintf("%x", hasher.Sum(nil))
}

func runE06ThemeSwitchProbe(t *testing.T, workRoot string) e06ThemeSwitchProbeReport {
	t.Helper()

	themeWorkRoot := filepath.Join(workRoot, "theme-switch")
	vaultPath := filepath.Join(themeWorkRoot, "vault")
	configPath := filepath.Join(vaultPath, "obsite.yaml")
	const (
		alphaAssetRelPath = "assets/theme/nested/alpha/theme-marker.txt"
		betaAssetRelPath  = "assets/theme/nested/beta/theme-marker.txt"
	)
	comparedFiles := []string{
		"switchboard/index.html",
		"style.css",
		alphaAssetRelPath,
		betaAssetRelPath,
	}

	if err := copyFixtureVaultToPath("theme-switch-vault", vaultPath); err != nil {
		t.Fatalf("copyFixtureVaultToPath(theme-switch-vault) error = %v", err)
	}

	loadCfg := func(t *testing.T, theme string) model.SiteConfig {
		t.Helper()

		overrides := internalconfig.Overrides{VaultPath: vaultPath}
		if trimmedTheme := strings.TrimSpace(theme); trimmedTheme != "" {
			overrides.Theme = trimmedTheme
		}

		loadedCfg, err := internalconfig.LoadForBuild(configPath, overrides)
		if err != nil {
			t.Fatalf("config.LoadForBuild(%q, theme=%q) error = %v", configPath, theme, err)
		}

		cfg := loadedCfg.Config
		cfg.Search.Enabled = false
		return cfg
	}

	runBuild := func(t *testing.T, label string, cfg model.SiteConfig, outputPath string) e06BuildObservation {
		t.Helper()

		var diagnostics bytes.Buffer
		result, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{concurrency: 2, diagnosticsWriter: &diagnostics})
		if err != nil {
			t.Fatalf("%s buildWithOptions() error = %v", label, err)
		}
		if result == nil {
			t.Fatalf("%s buildWithOptions() = nil result", label)
		}
		if result.NotePages != 2 {
			t.Fatalf("%s result.NotePages = %d, want %d", label, result.NotePages, 2)
		}
		if strings.TrimSpace(diagnostics.String()) != "" {
			t.Fatalf("%s diagnostics summary = %q, want empty summary", label, diagnostics.String())
		}

		return e06BuildObservation{
			NotePages:   result.NotePages,
			Diagnostics: strings.TrimSpace(diagnostics.String()),
		}
	}

	assertAlphaHTML := func(t *testing.T, outputPath string) {
		t.Helper()

		alphaHTML := readBuildOutputFile(t, outputPath, "switchboard/index.html")
		for _, snippets := range [][]byte{
			[]byte(`data-theme-shell="alpha-shell"`),
			[]byte(`data-theme-note="alpha-note"`),
			[]byte(`href="../assets/theme/nested/alpha/theme-marker.txt"`),
		} {
			if !bytesContainsAny(alphaHTML, snippets, bytes.ReplaceAll(snippets, []byte(`"`), nil)) {
				t.Fatalf("alpha switchboard missing marker %q\n%s", snippets, alphaHTML)
			}
		}
		alphaStyle := readBuildOutputFile(t, outputPath, "style.css")
		if !bytesContainsAny(alphaStyle, []byte("--theme-switch-marker: alpha"), []byte("--theme-switch-marker:alpha")) {
			t.Fatalf("style.css missing alpha marker\n%s", alphaStyle)
		}
		alphaAsset := readBuildOutputFile(t, outputPath, alphaAssetRelPath)
		if !bytes.Contains(alphaAsset, []byte("alpha asset marker")) {
			t.Fatalf("%s missing alpha marker\n%s", alphaAssetRelPath, alphaAsset)
		}
	}

	assertBetaOutputs := func(t *testing.T, outputPath string) {
		t.Helper()

		betaHTML := readBuildOutputFile(t, outputPath, "switchboard/index.html")
		for _, snippets := range [][]byte{
			[]byte(`data-theme-shell="beta-shell"`),
			[]byte(`data-theme-note="beta-note"`),
			[]byte(`href="../assets/theme/nested/beta/theme-marker.txt"`),
		} {
			if !bytesContainsAny(betaHTML, snippets, bytes.ReplaceAll(snippets, []byte(`"`), nil)) {
				t.Fatalf("beta switchboard missing marker %q\n%s", snippets, betaHTML)
			}
		}
		for _, forbidden := range []string{"alpha-shell", "alpha-note", `../assets/theme/nested/alpha/theme-marker.txt`} {
			if bytes.Contains(betaHTML, []byte(forbidden)) {
				t.Fatalf("beta switchboard retained alpha marker %q\n%s", forbidden, betaHTML)
			}
		}
		betaStyle := readBuildOutputFile(t, outputPath, "style.css")
		if !bytesContainsAny(betaStyle, []byte("--theme-switch-marker: beta"), []byte("--theme-switch-marker:beta")) {
			t.Fatalf("style.css missing beta marker\n%s", betaStyle)
		}
		if bytesContainsAny(betaStyle, []byte("--theme-switch-marker: alpha"), []byte("--theme-switch-marker:alpha")) {
			t.Fatalf("style.css retained alpha marker after beta rebuild\n%s", betaStyle)
		}
		betaAsset := readBuildOutputFile(t, outputPath, betaAssetRelPath)
		if !bytes.Contains(betaAsset, []byte("beta asset marker")) {
			t.Fatalf("%s missing beta marker\n%s", betaAssetRelPath, betaAsset)
		}
		assertPathMissing(t, filepath.Join(outputPath, filepath.FromSlash(alphaAssetRelPath)))
	}

	assertEmbeddedDefaultOutputs := func(t *testing.T, outputPath string) {
		t.Helper()

		defaultHTML := readBuildOutputFile(t, outputPath, "switchboard/index.html")
		for _, forbidden := range []string{"alpha-shell", "alpha-note", `../assets/theme/nested/alpha/theme-marker.txt`} {
			if bytes.Contains(defaultHTML, []byte(forbidden)) {
				t.Fatalf("embedded default switchboard retained alpha marker %q\n%s", forbidden, defaultHTML)
			}
		}
		if !bytesContainsAny(defaultHTML, []byte(`href="../assets/custom.css"`), []byte(`href=../assets/custom.css`)) {
			t.Fatalf("embedded default switchboard missing vault custom.css link\n%s", defaultHTML)
		}
		defaultStyle := readBuildOutputFile(t, outputPath, "style.css")
		if bytesContainsAny(defaultStyle, []byte("--theme-switch-marker: alpha"), []byte("--theme-switch-marker:alpha")) {
			t.Fatalf("embedded default style.css retained alpha marker\n%s", defaultStyle)
		}
		assertPathMissing(t, filepath.Join(outputPath, filepath.FromSlash(alphaAssetRelPath)))
		assertPathMissing(t, filepath.Join(outputPath, filepath.FromSlash(betaAssetRelPath)))
	}

	report := e06ThemeSwitchProbeReport{}
	report.ComparedFiles = append([]string(nil), comparedFiles...)

	t.Run("theme-switch alpha to beta", func(t *testing.T) {
		outputPath := filepath.Join(themeWorkRoot, "site-alpha-beta")
		alphaCfg := loadCfg(t, "alpha")
		report.AlphaBaseline = runBuild(t, "theme alpha baseline", alphaCfg, outputPath)
		assertAlphaHTML(t, outputPath)
		alphaSnapshot := snapshotOptionalOutputFiles(t, outputPath, comparedFiles)

		betaCfg := loadCfg(t, "beta")
		report.AlphaToBeta = runBuild(t, "theme beta rebuild", betaCfg, outputPath)
		assertBetaOutputs(t, outputPath)
		changedFiles, stableFiles := diffOptionalSnapshotFiles(alphaSnapshot, snapshotOptionalOutputFiles(t, outputPath, comparedFiles))
		report.AlphaToBeta.FileChanged = changedFiles
		report.AlphaToBeta.FileStable = stableFiles
		report.ArtifactChecks.AlphaToBetaStyleChanged = slicesContainsString(changedFiles, "style.css")
		report.ArtifactChecks.AlphaToBetaThemeAssetsChanged = slicesContainsString(changedFiles, alphaAssetRelPath) && slicesContainsString(changedFiles, betaAssetRelPath)
		if !report.ArtifactChecks.AlphaToBetaStyleChanged {
			t.Fatalf("theme alpha->beta snapshot diff did not include style.css\nchanged=%#v\nstable=%#v", changedFiles, stableFiles)
		}
		if !report.ArtifactChecks.AlphaToBetaThemeAssetsChanged {
			t.Fatalf("theme alpha->beta snapshot diff did not include both theme asset changes\nchanged=%#v\nstable=%#v", changedFiles, stableFiles)
		}
	})

	t.Run("theme-switch alpha to embedded default", func(t *testing.T) {
		outputPath := filepath.Join(themeWorkRoot, "site-alpha-default")
		alphaCfg := loadCfg(t, "alpha")
		runBuild(t, "theme alpha baseline for embedded default", alphaCfg, outputPath)
		assertAlphaHTML(t, outputPath)
		alphaSnapshot := snapshotOptionalOutputFiles(t, outputPath, comparedFiles)

		defaultCfg := loadCfg(t, "")
		if defaultCfg.ActiveThemeName != "" {
			t.Fatalf("defaultCfg.ActiveThemeName = %q, want empty string", defaultCfg.ActiveThemeName)
		}
		if defaultCfg.ThemeRoot != "" {
			t.Fatalf("defaultCfg.ThemeRoot = %q, want empty string", defaultCfg.ThemeRoot)
		}
		report.AlphaToEmbeddedDefault = runBuild(t, "embedded default rebuild", defaultCfg, outputPath)
		assertEmbeddedDefaultOutputs(t, outputPath)
		changedFiles, stableFiles := diffOptionalSnapshotFiles(alphaSnapshot, snapshotOptionalOutputFiles(t, outputPath, comparedFiles))
		report.AlphaToEmbeddedDefault.FileChanged = changedFiles
		report.AlphaToEmbeddedDefault.FileStable = stableFiles
		report.ArtifactChecks.AlphaToEmbeddedDefaultStyleChanged = slicesContainsString(changedFiles, "style.css")
		report.ArtifactChecks.AlphaToEmbeddedDefaultThemeAssetsChanged = slicesContainsString(changedFiles, alphaAssetRelPath)
		if !report.ArtifactChecks.AlphaToEmbeddedDefaultStyleChanged {
			t.Fatalf("theme alpha->embedded-default snapshot diff did not include style.css\nchanged=%#v\nstable=%#v", changedFiles, stableFiles)
		}
		if !report.ArtifactChecks.AlphaToEmbeddedDefaultThemeAssetsChanged {
			t.Fatalf("theme alpha->embedded-default snapshot diff did not include theme asset removal\nchanged=%#v\nstable=%#v", changedFiles, stableFiles)
		}
	})

	return report
}

func copyFixtureVaultToPath(fixtureName string, dstRoot string) error {
	srcRoot := filepath.Join("..", "..", "test", "testdata", "e2e", filepath.FromSlash(fixtureName))
	stamp := time.Date(2026, time.April, 6, 12, 0, 0, 0, time.UTC)

	if err := os.RemoveAll(dstRoot); err != nil {
		return err
	}

	return filepath.Walk(srcRoot, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		relPath, err := filepath.Rel(srcRoot, path)
		if err != nil {
			return err
		}
		if relPath == "." {
			return os.MkdirAll(dstRoot, 0o755)
		}

		dstPath := filepath.Join(dstRoot, relPath)
		if info.IsDir() {
			return os.MkdirAll(dstPath, 0o755)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dstPath, data, info.Mode().Perm()); err != nil {
			return err
		}
		return os.Chtimes(dstPath, stamp, stamp)
	})
}

func snapshotOutputFiles(t *testing.T, outputRoot string, relPaths []string) map[string][]byte {
	t.Helper()

	snapshot := make(map[string][]byte, len(relPaths))
	for _, relPath := range relPaths {
		snapshot[relPath] = append([]byte(nil), readBuildOutputFile(t, outputRoot, relPath)...)
	}
	return snapshot
}

func subsetSnapshot(snapshot map[string][]byte, relPaths []string) map[string][]byte {
	filtered := make(map[string][]byte, len(relPaths))
	for _, relPath := range relPaths {
		filtered[relPath] = append([]byte(nil), snapshot[relPath]...)
	}
	return filtered
}

func snapshotOptionalOutputFiles(t *testing.T, outputRoot string, relPaths []string) map[string]e06OutputSnapshot {
	t.Helper()

	snapshot := make(map[string]e06OutputSnapshot, len(relPaths))
	for _, relPath := range relPaths {
		absPath := filepath.Join(outputRoot, filepath.FromSlash(relPath))
		if _, err := os.Stat(absPath); err != nil {
			if os.IsNotExist(err) {
				snapshot[relPath] = e06OutputSnapshot{}
				continue
			}
			t.Fatalf("os.Stat(%q) error = %v", absPath, err)
		}
		snapshot[relPath] = e06OutputSnapshot{
			Exists: true,
			Data:   append([]byte(nil), readBuildOutputFile(t, outputRoot, relPath)...),
		}
	}
	return snapshot
}

func diffSnapshotFiles(left map[string][]byte, right map[string][]byte) ([]string, []string) {
	changed := make([]string, 0, len(right))
	stable := make([]string, 0, len(right))
	for relPath, rightData := range right {
		if bytes.Equal(left[relPath], rightData) {
			stable = append(stable, relPath)
			continue
		}
		changed = append(changed, relPath)
	}
	sort.Strings(changed)
	sort.Strings(stable)
	return changed, stable
}

func diffOptionalSnapshotFiles(left map[string]e06OutputSnapshot, right map[string]e06OutputSnapshot) ([]string, []string) {
	keySet := make(map[string]struct{}, len(left)+len(right))
	for key := range left {
		keySet[key] = struct{}{}
	}
	for key := range right {
		keySet[key] = struct{}{}
	}

	changed := make([]string, 0, len(keySet))
	stable := make([]string, 0, len(keySet))
	for key := range keySet {
		leftSnapshot := left[key]
		rightSnapshot := right[key]
		if leftSnapshot.Exists == rightSnapshot.Exists && bytes.Equal(leftSnapshot.Data, rightSnapshot.Data) {
			stable = append(stable, key)
			continue
		}
		changed = append(changed, key)
	}
	sort.Strings(changed)
	sort.Strings(stable)
	return changed, stable
}

func diffManifestPages(left map[string]string, right map[string]string) ([]string, []string) {
	keySet := make(map[string]struct{}, len(left)+len(right))
	for key := range left {
		keySet[key] = struct{}{}
	}
	for key := range right {
		keySet[key] = struct{}{}
	}

	changed := make([]string, 0, len(keySet))
	stable := make([]string, 0, len(keySet))
	for key := range keySet {
		if left[key] == right[key] {
			stable = append(stable, key)
			continue
		}
		changed = append(changed, key)
	}
	sort.Strings(changed)
	sort.Strings(stable)
	return changed, stable
}

func bytesContainsAny(data []byte, needles ...[]byte) bool {
	for _, needle := range needles {
		if bytes.Contains(data, needle) {
			return true
		}
	}
	return false
}

func slicesContainsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func writeE06ProbeReport(reportPath string, report e06ProbeReport) error {
	if err := os.MkdirAll(filepath.Dir(reportPath), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(reportPath, data, 0o644)
}
