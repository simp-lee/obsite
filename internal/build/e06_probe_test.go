package build

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	internalconfig "github.com/simp-lee/obsite/internal/config"
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

	pagefindPath := filepath.Join(vaultPath, "tools", "pagefind_extended")
	if err := os.Chmod(pagefindPath, 0o755); err != nil {
		t.Fatalf("os.Chmod(%q) error = %v", pagefindPath, err)
	}

	configPath := filepath.Join(vaultPath, "obsite.yaml")
	loadedCfg, err := internalconfig.LoadForBuild(configPath, internalconfig.Overrides{VaultPath: vaultPath})
	if err != nil {
		t.Fatalf("config.LoadForBuild(%q) error = %v", configPath, err)
	}
	cfg := loadedCfg.Config

	var baselineDiagnostics bytes.Buffer
	baselineResult, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{
		concurrency:       2,
		diagnosticsWriter: &baselineDiagnostics,
	})
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
		result, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{
			concurrency:       2,
			diagnosticsWriter: &diagnostics,
		})
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
		result, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{
			concurrency:       2,
			diagnosticsWriter: &diagnostics,
		})
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

	if err := writeE06ProbeReport(reportPath, report); err != nil {
		t.Fatalf("writeE06ProbeReport(%q) error = %v", reportPath, err)
	}
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
