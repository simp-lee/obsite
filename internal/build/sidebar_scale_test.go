package build

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	internalrender "github.com/simp-lee/obsite/internal/render"
)

type sidebarScaleSnapshot struct {
	sidebarJSON      []byte
	sidebarFileCount int
	htmlBytes        int64
	runtimePath      string
	runtimeSize      int
	themeCSS         []byte
	themeAsset       []byte
}

func TestBuildSidebarDataAndSiteOutputGrowLinearly(t *testing.T) {
	t.Parallel()

	const smallCount = 16
	small := buildSidebarScaleFixture(t, smallCount)
	large := buildSidebarScaleFixture(t, smallCount*2)

	if bytes.Contains(small.sidebarJSON, []byte("isActive")) || bytes.Contains(large.sidebarJSON, []byte("isActive")) {
		t.Fatal("sidebar.json contains per-page active state")
	}
	for count, snapshot := range map[int]sidebarScaleSnapshot{smallCount: small, smallCount * 2: large} {
		for index := 0; index < count; index++ {
			marker := []byte(fmt.Sprintf("SidebarNode%03dZ", index))
			if got := bytes.Count(snapshot.sidebarJSON, marker); got != 1 {
				t.Fatalf("%d-note sidebar marker %q count = %d, want 1", count, marker, got)
			}
		}
		if got := bytes.Count(snapshot.sidebarJSON, []byte(`"name":`)); got != count+1 {
			t.Fatalf("%d-note sidebar name field count = %d, want %d", count, got, count+1)
		}
	}

	if len(large.sidebarJSON) <= len(small.sidebarJSON) || len(large.sidebarJSON) > len(small.sidebarJSON)*23/10 {
		t.Fatalf("sidebar JSON sizes small=%d large=%d, want approximately linear growth", len(small.sidebarJSON), len(large.sidebarJSON))
	}
	if large.htmlBytes <= small.htmlBytes || large.htmlBytes > small.htmlBytes*5/2 {
		t.Fatalf("HTML byte totals small=%d large=%d, want no node-by-page growth", small.htmlBytes, large.htmlBytes)
	}
	if small.runtimePath != large.runtimePath || small.runtimeSize != large.runtimeSize {
		t.Fatalf("shared runtime changed with vault size: small=(%q,%d) large=(%q,%d)", small.runtimePath, small.runtimeSize, large.runtimePath, large.runtimeSize)
	}
	if small.sidebarFileCount != 1 || large.sidebarFileCount != 1 {
		t.Fatalf("Sidebar data file counts small=%d large=%d, want exactly one each", small.sidebarFileCount, large.sidebarFileCount)
	}
	if !bytes.Equal(small.themeCSS, large.themeCSS) || !bytes.Equal(small.themeAsset, large.themeAsset) {
		t.Fatal("fixed theme assets changed with vault size")
	}
}

func buildSidebarScaleFixture(t *testing.T, count int) sidebarScaleSnapshot {
	t.Helper()
	vaultPath := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "site")
	for index := 0; index < count; index++ {
		writeBuildTestFile(t, vaultPath, fmt.Sprintf("notes/note-%03d.md", index), fmt.Sprintf("---\ntitle: SidebarNode%03dZ\n---\n# SidebarNode%03dZ\n\nBody.\n", index, index))
	}
	writeBuildTestFile(t, vaultPath, ".obsite/theme/theme.css", ":root { --obsite-accent: purple; }\n")
	writeBuildTestFile(t, vaultPath, ".obsite/theme/assets/fixed.txt", "fixed-theme-asset\n")
	cfg := testBuildSiteConfig()
	cfg.Sidebar.Enabled = true
	cfg.Related.Enabled = false
	if _, err := buildWithOptions(cfg, vaultPath, outputPath, buildOptions{diagnosticsWriter: io.Discard}); err != nil {
		t.Fatalf("buildWithOptions(%d notes) error = %v", count, err)
	}

	snapshot := sidebarScaleSnapshot{
		sidebarJSON: append([]byte(nil), readBuildOutputFile(t, outputPath, sidebarJSONOutputPath)...),
		themeCSS:    append([]byte(nil), readBuildOutputFile(t, outputPath, themeCSSOutputPath)...),
		themeAsset:  append([]byte(nil), readBuildOutputFile(t, outputPath, "assets/theme/fixed.txt")...),
	}
	runtimePath, err := internalrender.SharedRuntimeOutputPath()
	if err != nil {
		t.Fatal(err)
	}
	snapshot.runtimePath = runtimePath
	snapshot.runtimeSize = len(readBuildOutputFile(t, outputPath, runtimePath))

	err = filepath.Walk(outputPath, func(current string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir() {
			return walkErr
		}
		relative, err := filepath.Rel(outputPath, current)
		if err != nil {
			return err
		}
		if strings.EqualFold(filepath.Base(relative), "sidebar.json") {
			snapshot.sidebarFileCount++
		}
		if filepath.Ext(current) != ".html" {
			return nil
		}
		data, err := os.ReadFile(current)
		if err != nil {
			return err
		}
		if bytes.Contains(data, []byte(`"isDir":`)) || bytes.Contains(data, []byte(`"children":`)) || bytes.Contains(data, []byte("sidebar-data")) {
			return fmt.Errorf("%s embeds Sidebar tree data", current)
		}
		snapshot.htmlBytes += int64(len(data))
		return nil
	})
	if err != nil {
		t.Fatalf("inspect generated output: %v", err)
	}
	return snapshot
}
