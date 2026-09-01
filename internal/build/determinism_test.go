package build

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestBuildManagedOutputIsByteDeterministicAcrossFullIncrementalAndConcurrency(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outputA := filepath.Join(t.TempDir(), "site-a")
	outputB := filepath.Join(t.TempDir(), "site-b")
	writeBuildTestFile(t, vaultPath, "notes/alpha.md", "---\ntitle: Alpha\ntags: [topic]\n---\n# Alpha\n\n[[Beta]]\n")
	writeBuildTestFile(t, vaultPath, "notes/beta.md", "---\ntitle: Beta\ntags: [topic]\n---\n# Beta\n\n[[Alpha]]\n")
	writeBuildTestFile(t, vaultPath, "custom.css", "body { outline-color: purple; }\n")
	writeBuildTestFile(t, vaultPath, ".obsite/theme/theme.css", ":root { --obsite-accent: purple; }\n")
	writeBuildTestFile(t, vaultPath, ".obsite/theme/assets/fixed.txt", "fixed\n")
	cfg := testBuildSiteConfig()
	cfg.Sidebar.Enabled = true
	cfg.Popover.Enabled = true
	cfg.Related.Enabled = false

	if _, err := buildWithOptions(cfg, vaultPath, outputA, buildOptions{concurrency: 1, diagnosticsWriter: io.Discard}); err != nil {
		t.Fatal(err)
	}
	full := snapshotThemeTestOutput(t, outputA)
	result, err := buildWithOptions(cfg, vaultPath, outputA, buildOptions{concurrency: 4, diagnosticsWriter: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if result.NotePages != 0 {
		t.Fatalf("no-op incremental NotePages = %d, want 0", result.NotePages)
	}
	incremental := snapshotThemeTestOutput(t, outputA)
	if !reflect.DeepEqual(incremental, full) {
		t.Fatalf("full and no-op incremental output hashes differ\nfull=%#v\nincremental=%#v", full, incremental)
	}

	if _, err := buildWithOptions(cfg, vaultPath, outputB, buildOptions{concurrency: 3, diagnosticsWriter: io.Discard, force: true}); err != nil {
		t.Fatal(err)
	}
	secondFull := snapshotThemeTestOutput(t, outputB)
	if !reflect.DeepEqual(secondFull, full) {
		t.Fatalf("full builds differ across output roots/concurrency\nfirst=%#v\nsecond=%#v", full, secondFull)
	}
}

func TestBuildOutputIgnoresFilesystemCreationOrder(t *testing.T) {
	t.Parallel()

	entries := []struct{ path, content string }{
		{"notes/alpha.md", "---\ntitle: Alpha\ntags: [topic]\n---\n# Alpha\n\n[[Beta]]\n"},
		{"notes/beta.md", "---\ntitle: Beta\ntags: [topic]\n---\n# Beta\n\n[[Alpha]]\n"},
		{"images/hero.png", "hero"},
		{"custom.css", "body { color: purple; }\n"},
	}
	create := func(reverse bool) (string, string) {
		vaultPath := t.TempDir()
		outputPath := filepath.Join(t.TempDir(), "site")
		for step := range entries {
			index := step
			if reverse {
				index = len(entries) - 1 - step
			}
			entry := entries[index]
			writeBuildTestFile(t, vaultPath, entry.path, entry.content)
		}
		fixed := time.Unix(1_700_000_000, 0)
		for _, entry := range entries {
			if err := os.Chtimes(filepath.Join(vaultPath, filepath.FromSlash(entry.path)), fixed, fixed); err != nil {
				t.Fatal(err)
			}
		}
		cfg := testBuildSiteConfig()
		cfg.Sidebar.Enabled = true
		if _, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard}); err != nil {
			t.Fatal(err)
		}
		return vaultPath, outputPath
	}
	_, forwardOutput := create(false)
	_, reverseOutput := create(true)
	forward := snapshotThemeTestOutput(t, forwardOutput)
	reverse := snapshotThemeTestOutput(t, reverseOutput)
	if !reflect.DeepEqual(forward, reverse) {
		t.Fatalf("creation-order outputs differ\nforward=%#v\nreverse=%#v", forward, reverse)
	}
	if got, want := readBuildOutputFile(t, forwardOutput, sidebarJSONOutputPath), readBuildOutputFile(t, reverseOutput, sidebarJSONOutputPath); !reflect.DeepEqual(got, want) {
		t.Fatalf("creation-order Sidebar JSON differs\nforward=%s\nreverse=%s", got, want)
	}
}

func TestOutputAndAssetNormalizationIsSeparatorIndependent(t *testing.T) {
	t.Parallel()

	if got, want := normalizeNoteAssetPath(`images\hero.png`), normalizeNoteAssetPath("images/hero.png"); got != want {
		t.Fatalf("asset normalization = %q, want %q", got, want)
	}
	left, leftKey, leftErr := normalizedOutputClaim(`assets\theme\hero.png`, outputOwnerThemeAsset, "left")
	right, rightKey, rightErr := normalizedOutputClaim("assets/theme/hero.png", outputOwnerThemeAsset, "left")
	if leftErr != nil || rightErr != nil || left != right || leftKey != rightKey {
		t.Fatalf("output normalization differs: left=%#v/%q/%v right=%#v/%q/%v", left, leftKey, leftErr, right, rightKey, rightErr)
	}
}
