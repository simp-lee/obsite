package asset

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/simp-lee/obsite/internal/diag"
	"github.com/simp-lee/obsite/internal/model"
)

func TestResolvePathAttachmentFallbackOnlyForBarePaths(t *testing.T) {
	t.Parallel()

	note := &model.Note{RelPath: "notes/chapter/current.md"}
	attachmentPath := "attachments/poster.png"

	tests := []struct {
		name                    string
		rawDestination          string
		want                    string
		wantAttachmentCandidate bool
	}{
		{
			name:                    "bare_filename",
			rawDestination:          "poster.png",
			want:                    attachmentPath,
			wantAttachmentCandidate: true,
		},
		{
			name:                    "vault_root_path",
			rawDestination:          "/poster.png",
			want:                    "",
			wantAttachmentCandidate: false,
		},
		{
			name:                    "note_relative_path",
			rawDestination:          "./poster.png",
			want:                    "",
			wantAttachmentCandidate: false,
		},
		{
			name:                    "parent_relative_path",
			rawDestination:          "../poster.png",
			want:                    "",
			wantAttachmentCandidate: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidates := CandidatePaths(note, "attachments", tt.rawDestination)
			hasAttachmentCandidate := false
			for _, candidate := range candidates {
				if candidate == attachmentPath {
					hasAttachmentCandidate = true
					break
				}
			}
			if hasAttachmentCandidate != tt.wantAttachmentCandidate {
				t.Fatalf("CandidatePaths(%q) attachment candidate = %v, want %v (candidates=%v)", tt.rawDestination, hasAttachmentCandidate, tt.wantAttachmentCandidate, candidates)
			}

			got := ResolvePath(note, "attachments", tt.rawDestination, func(candidate string) bool {
				return candidate == attachmentPath
			})
			if got != tt.want {
				t.Fatalf("ResolvePath(%q) = %q, want %q", tt.rawDestination, got, tt.want)
			}
		})
	}
}

func TestAssetCollectorRegisterConcurrent(t *testing.T) {
	t.Parallel()

	vaultRoot := t.TempDir()
	writeAssetFile(t, vaultRoot, "images/hero.png", "hero")
	indexed := map[string]*model.Asset{
		"images/hero.png": {SrcPath: "images/hero.png", RefCount: 1},
	}

	collector := NewCollector(vaultRoot, indexed)

	const registrations = 32
	expected := "assets/hero.png"
	results := make(chan string, registrations)
	var wg sync.WaitGroup
	for range registrations {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- collector.Register("images/hero.png")
		}()
	}
	wg.Wait()
	close(results)

	for sitePath := range results {
		if sitePath != expected {
			t.Fatalf("Register() = %q, want %q", sitePath, expected)
		}
	}

	snapshot := collector.Snapshot()
	asset := snapshot["images/hero.png"]
	if asset == nil {
		t.Fatal("Snapshot()[images/hero.png] = nil, want asset")
	}
	if asset.RefCount != registrations {
		t.Fatalf("asset.RefCount = %d, want %d", asset.RefCount, registrations)
	}
	if asset.DstPath != expected {
		t.Fatalf("asset.DstPath = %q, want %q", asset.DstPath, expected)
	}
}

func TestAssetCollectorRegisterRejectsScanExcludedInputsAndKeepsVisiblePathPlain(t *testing.T) {
	t.Parallel()

	vaultRoot := t.TempDir()
	writeAssetFile(t, vaultRoot, "images/photo.png", "left")
	writeAssetFile(t, vaultRoot, ".hidden/attachments/photo.png", "right")
	writeAssetFile(t, vaultRoot, ".obsidian/assets/photo.png", "obsidian")
	writeAssetFile(t, vaultRoot, "node_modules/pkg/photo.png", "node")

	indexed := map[string]*model.Asset{
		"images/photo.png": {SrcPath: "images/photo.png", RefCount: 1},
	}

	collector := NewCollector(vaultRoot, indexed)
	visible := collector.Register("images/photo.png")
	if visible != "assets/photo.png" {
		t.Fatalf("Register(images/photo.png) = %q, want %q", visible, "assets/photo.png")
	}

	for _, input := range []string{
		".hidden/attachments/photo.png",
		".obsidian/assets/photo.png",
		"node_modules/pkg/photo.png",
	} {
		if got := collector.Register(input); got != "" {
			t.Fatalf("Register(%q) = %q, want empty path for scan-excluded input", input, got)
		}
	}

	if got := collector.Register("images/photo.png"); got != visible {
		t.Fatalf("second Register(images/photo.png) = %q, want stable visible path %q", got, visible)
	}

	snapshot := collector.Snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("len(Snapshot()) = %d, want 1 accepted asset", len(snapshot))
	}
	if asset := snapshot["images/photo.png"]; asset == nil || asset.DstPath != visible || asset.RefCount != 2 {
		t.Fatalf("Snapshot()[images/photo.png] = %#v, want plain visible asset with refcount 2", asset)
	}

	merged := MergeAssets(vaultRoot, indexed, collector)
	if len(merged) != 1 {
		t.Fatalf("len(MergeAssets()) = %d, want 1 accepted asset", len(merged))
	}
	if asset := merged["images/photo.png"]; asset == nil || asset.DstPath != visible {
		t.Fatalf("merged[images/photo.png] = %#v, want stable plain DstPath %q", asset, visible)
	}
}

func TestAssetCollectorRegisterBuildsVaultInventoryOnce(t *testing.T) {
	t.Parallel()

	vaultRoot := t.TempDir()
	writeAssetFile(t, vaultRoot, "images/photo.png", "photo")
	writeAssetFile(t, vaultRoot, "files/manual.pdf", "manual")
	writeAssetFile(t, vaultRoot, "docs/guide.txt", "guide")

	scans := 0
	collector := newCollector(vaultRoot, nil, func() {
		scans++
	})
	if scans != 1 {
		t.Fatalf("inventory scans after construction = %d, want 1", scans)
	}

	for srcPath, want := range map[string]string{
		"images/photo.png": "assets/photo.png",
		"files/manual.pdf": "assets/manual.pdf",
		"docs/guide.txt":   "assets/guide.txt",
	} {
		if got := collector.Register(srcPath); got != want {
			t.Fatalf("Register(%q) = %q, want %q", srcPath, got, want)
		}
	}

	if scans != 1 {
		t.Fatalf("inventory scans after repeated Register calls = %d, want 1", scans)
	}
}

func TestAssetCollectorRegisterKeepsPlainPathForUniquePass2AssetWithoutIndexedPlan(t *testing.T) {
	t.Parallel()

	vaultRoot := t.TempDir()
	writeAssetFile(t, vaultRoot, "images/hero.png", "hero")

	collector := NewCollector(vaultRoot, nil)
	got := collector.Register("images/hero.png")
	want := "assets/hero.png"
	if got != want {
		t.Fatalf("Register() = %q, want %q", got, want)
	}
}

func TestAssetCollectorRegisterHashesMissingPass2AssetWithoutIndexedPlan(t *testing.T) {
	t.Parallel()

	vaultRoot := t.TempDir()

	collector := NewCollector(vaultRoot, nil)
	got := collector.Register("images/missing.png")
	want := expectedHashedAssetPath(t, vaultRoot, "images/missing.png")
	if got != want {
		t.Fatalf("Register() = %q, want %q", got, want)
	}
}

func TestMergeAssetsDedupesAcrossPasses(t *testing.T) {
	t.Parallel()

	vaultRoot := t.TempDir()
	writeAssetFile(t, vaultRoot, "images/hero.png", "hero")
	writeAssetFile(t, vaultRoot, "files/manual.pdf", "manual")
	writeAssetFile(t, vaultRoot, "files/extra.bin", "extra")

	indexed := map[string]*model.Asset{
		"images/hero.png":  {SrcPath: "images/hero.png", RefCount: 1},
		"files/manual.pdf": {SrcPath: "files/manual.pdf", RefCount: 2},
		"files/extra.bin":  {SrcPath: "files/extra.bin", RefCount: 1},
	}
	collector := NewCollector(vaultRoot, indexed)
	heroPath := collector.Register("images/hero.png")
	extraPath := collector.Register("files/extra.bin")
	collector.Register("files/extra.bin")

	merged := MergeAssets(vaultRoot, indexed, collector)
	if len(merged) != 3 {
		t.Fatalf("len(MergeAssets()) = %d, want 3", len(merged))
	}

	if asset := merged["images/hero.png"]; asset == nil || asset.RefCount != 2 || asset.DstPath != heroPath {
		t.Fatalf("merged[images/hero.png] = %#v, want deduped asset with refcount 2", asset)
	}
	if asset := merged["files/manual.pdf"]; asset == nil || asset.RefCount != 2 || asset.DstPath != "assets/manual.pdf" {
		t.Fatalf("merged[files/manual.pdf] = %#v, want pass-1-only asset preserved", asset)
	}
	if asset := merged["files/extra.bin"]; asset == nil || asset.RefCount != 3 || asset.DstPath != extraPath {
		t.Fatalf("merged[files/extra.bin] = %#v, want merged asset with stable plain path", asset)
	}
}

func TestMergeAssetsHashesSameBasenameCollisions(t *testing.T) {
	t.Parallel()

	vaultRoot := t.TempDir()
	writeAssetFile(t, vaultRoot, "images/photo.png", "left")
	writeAssetFile(t, vaultRoot, "attachments/photo.png", "right")

	indexed := map[string]*model.Asset{
		"images/photo.png":      {SrcPath: "images/photo.png", RefCount: 1},
		"attachments/photo.png": {SrcPath: "attachments/photo.png", RefCount: 1},
	}
	collector := NewCollector(vaultRoot, indexed)
	registered := collector.Register("attachments/photo.png")
	if registered == "assets/photo.png" {
		t.Fatalf("Register() = %q, want hashed collision path", registered)
	}
	if !strings.HasPrefix(registered, "assets/photo.") || !strings.HasSuffix(registered, ".png") {
		t.Fatalf("Register() = %q, want assets/photo.<hash>.png", registered)
	}

	merged := MergeAssets(vaultRoot, indexed, collector)
	left := merged["images/photo.png"]
	right := merged["attachments/photo.png"]
	if left == nil || right == nil {
		t.Fatalf("merged collision assets = %#v, want both assets", merged)
	}
	if left.DstPath == "assets/photo.png" || right.DstPath == "assets/photo.png" {
		t.Fatalf("collision DstPaths = %q, %q, want hashed outputs for both assets", left.DstPath, right.DstPath)
	}
	if left.DstPath == right.DstPath {
		t.Fatalf("collision DstPaths = %q and %q, want different hashes for different file contents", left.DstPath, right.DstPath)
	}
	if right.DstPath != registered {
		t.Fatalf("right.DstPath = %q, want stable registered path %q", right.DstPath, registered)
	}
}

func TestAssetCollectorRegisterReplansIndexedPlainPathWhenPass2CollisionExists(t *testing.T) {
	t.Parallel()

	vaultRoot := t.TempDir()
	writeAssetFile(t, vaultRoot, "images/photo.png", "left")
	writeAssetFile(t, vaultRoot, "attachments/photo.png", "right")

	indexed := map[string]*model.Asset{
		"images/photo.png": {SrcPath: "images/photo.png", RefCount: 1},
	}
	collector := NewCollector(vaultRoot, indexed)
	left := collector.Register("images/photo.png")
	if left == "assets/photo.png" {
		t.Fatalf("Register(images/photo.png) = %q, want hashed collision path", left)
	}
	if !strings.HasPrefix(left, "assets/photo.") || !strings.HasSuffix(left, ".png") {
		t.Fatalf("Register(images/photo.png) = %q, want assets/photo.<hash>.png", left)
	}

	right := collector.Register("attachments/photo.png")
	if right == "assets/photo.png" {
		t.Fatalf("Register(attachments/photo.png) = %q, want hashed collision path", right)
	}
	if !strings.HasPrefix(right, "assets/photo.") || !strings.HasSuffix(right, ".png") {
		t.Fatalf("Register(attachments/photo.png) = %q, want assets/photo.<hash>.png", right)
	}
	if left == right {
		t.Fatalf("Register() returned %q and %q, want distinct hashed paths for different contents", left, right)
	}

	snapshot := collector.Snapshot()
	if asset := snapshot["images/photo.png"]; asset == nil || asset.DstPath != left {
		t.Fatalf("Snapshot()[images/photo.png] = %#v, want stable DstPath %q", asset, left)
	}
	if asset := snapshot["attachments/photo.png"]; asset == nil || asset.DstPath != right {
		t.Fatalf("Snapshot()[attachments/photo.png] = %#v, want stable DstPath %q", asset, right)
	}

	merged := MergeAssets(vaultRoot, indexed, collector)
	if asset := merged["images/photo.png"]; asset == nil || asset.DstPath != left {
		t.Fatalf("merged[images/photo.png] = %#v, want stable DstPath %q", asset, left)
	}
	if asset := merged["attachments/photo.png"]; asset == nil || asset.DstPath != right {
		t.Fatalf("merged[attachments/photo.png] = %#v, want stable DstPath %q", asset, right)
	}
}

func TestAssetCollectorRegisterHashesPass2OnlyCollisionIndependentlyOfOrder(t *testing.T) {
	t.Parallel()

	vaultRoot := t.TempDir()
	writeAssetFile(t, vaultRoot, "images/photo.png", "left")
	writeAssetFile(t, vaultRoot, "attachments/photo.png", "right")

	collectorA := NewCollector(vaultRoot, nil)
	forward := map[string]string{
		"images/photo.png":      collectorA.Register("images/photo.png"),
		"attachments/photo.png": collectorA.Register("attachments/photo.png"),
	}

	collectorB := NewCollector(vaultRoot, nil)
	reverse := map[string]string{
		"attachments/photo.png": collectorB.Register("attachments/photo.png"),
		"images/photo.png":      collectorB.Register("images/photo.png"),
	}

	for srcPath, sitePath := range forward {
		if sitePath == "assets/photo.png" {
			t.Fatalf("forward Register(%q) = %q, want hashed collision path", srcPath, sitePath)
		}
		if !strings.HasPrefix(sitePath, "assets/photo.") || !strings.HasSuffix(sitePath, ".png") {
			t.Fatalf("forward Register(%q) = %q, want assets/photo.<hash>.png", srcPath, sitePath)
		}
		if reverse[srcPath] != sitePath {
			t.Fatalf("Register(%q) = %q in forward order, %q in reverse order, want order-independent site path", srcPath, sitePath, reverse[srcPath])
		}
	}
}

func TestAssetCollectorRegisterKeepsExistingPathPlainAfterMissingSameBasename(t *testing.T) {
	t.Parallel()

	vaultRoot := t.TempDir()
	writeAssetFile(t, vaultRoot, "attachments/photo.png", "photo")

	collector := NewCollector(vaultRoot, nil)
	missing := collector.Register("images/photo.png")
	wantMissing := expectedHashedAssetPath(t, vaultRoot, "images/photo.png")
	if missing != wantMissing {
		t.Fatalf("Register(images/photo.png) = %q, want %q", missing, wantMissing)
	}

	existing := collector.Register("attachments/photo.png")
	if existing != "assets/photo.png" {
		t.Fatalf("Register(attachments/photo.png) = %q, want %q", existing, "assets/photo.png")
	}

	merged := MergeAssets(vaultRoot, nil, collector)
	if asset := merged["attachments/photo.png"]; asset == nil || asset.DstPath != existing {
		t.Fatalf("merged[attachments/photo.png] = %#v, want stable plain DstPath %q", asset, existing)
	}
	if asset := merged["images/photo.png"]; asset == nil || asset.DstPath == "assets/photo.png" || !strings.HasPrefix(asset.DstPath, "assets/photo.") || !strings.HasSuffix(asset.DstPath, ".png") {
		t.Fatalf("merged[images/photo.png] = %#v, want hashed DstPath distinct from %q", asset, existing)
	}
}

func TestAssetCollectorRegisterKeepsIndexedCollisionStable(t *testing.T) {
	t.Parallel()

	vaultRoot := t.TempDir()
	writeAssetFile(t, vaultRoot, "images/photo.png", "left")
	writeAssetFile(t, vaultRoot, "attachments/photo.png", "right")

	indexed := map[string]*model.Asset{
		"images/photo.png":      {SrcPath: "images/photo.png", RefCount: 1},
		"attachments/photo.png": {SrcPath: "attachments/photo.png", RefCount: 1},
	}
	collector := NewCollector(vaultRoot, indexed)
	left := collector.Register("images/photo.png")
	right := collector.Register("attachments/photo.png")

	if left == "assets/photo.png" || right == "assets/photo.png" {
		t.Fatalf("Register() returned %q and %q, want hashed collision paths for both sources", left, right)
	}
	if left == right {
		t.Fatalf("Register() returned %q and %q, want distinct paths for different contents", left, right)
	}

	merged := MergeAssets(vaultRoot, indexed, collector)
	if asset := merged["images/photo.png"]; asset == nil || asset.DstPath != left {
		t.Fatalf("merged[images/photo.png] = %#v, want stable DstPath %q", asset, left)
	}
	if asset := merged["attachments/photo.png"]; asset == nil || asset.DstPath != right {
		t.Fatalf("merged[attachments/photo.png] = %#v, want stable DstPath %q", asset, right)
	}
}

func TestCopyAssetsCopiesMergedAssets(t *testing.T) {
	t.Parallel()

	vaultRoot := t.TempDir()
	outputRoot := t.TempDir()
	writeAssetFile(t, vaultRoot, "images/hero.png", "hero-bytes")

	collector := NewCollector(vaultRoot, nil)
	registered := collector.Register("images/hero.png")
	if registered != "assets/hero.png" {
		t.Fatalf("Register() = %q, want %q", registered, "assets/hero.png")
	}
	merged := MergeAssets(vaultRoot, nil, collector)
	collectorDiag := diag.NewCollector()

	if err := CopyAssets(vaultRoot, outputRoot, merged, collectorDiag); err != nil {
		t.Fatalf("CopyAssets() error = %v", err)
	}
	if collectorDiag.Len() != 0 {
		t.Fatalf("collectorDiag.Diagnostics() = %#v, want no diagnostics", collectorDiag.Diagnostics())
	}

	got, err := os.ReadFile(filepath.Join(outputRoot, filepath.FromSlash(registered)))
	if err != nil {
		t.Fatalf("ReadFile(output asset) error = %v", err)
	}
	if string(got) != "hero-bytes" {
		t.Fatalf("copied asset contents = %q, want %q", string(got), "hero-bytes")
	}
	if asset := merged["images/hero.png"]; asset == nil || asset.DstPath != registered {
		t.Fatalf("merged[images/hero.png] = %#v, want populated DstPath", asset)
	}
}

func TestMergeAssetsRewritesNonAssetDestinationUnderAssets(t *testing.T) {
	t.Parallel()

	vaultRoot := t.TempDir()
	outputRoot := t.TempDir()
	writeAssetFile(t, vaultRoot, "images/hero.png", "hero-bytes")

	indexed := map[string]*model.Asset{
		"images/hero.png": {SrcPath: "images/hero.png", RefCount: 1, DstPath: "notes/hero.png"},
	}

	merged := MergeAssets(vaultRoot, indexed, nil)
	asset := merged["images/hero.png"]
	if asset == nil {
		t.Fatal("merged[images/hero.png] = nil, want asset")
	}
	if asset.DstPath != "assets/hero.png" {
		t.Fatalf("asset.DstPath = %q, want %q", asset.DstPath, "assets/hero.png")
	}

	collectorDiag := diag.NewCollector()
	if err := CopyAssets(vaultRoot, outputRoot, merged, collectorDiag); err != nil {
		t.Fatalf("CopyAssets() error = %v", err)
	}
	if collectorDiag.Len() != 0 {
		t.Fatalf("collectorDiag.Diagnostics() = %#v, want no diagnostics", collectorDiag.Diagnostics())
	}

	if _, err := os.Stat(filepath.Join(outputRoot, "notes", "hero.png")); !os.IsNotExist(err) {
		t.Fatalf("Stat(non-asset destination) error = %v, want not-exist", err)
	}
	got, err := os.ReadFile(filepath.Join(outputRoot, "assets", "hero.png"))
	if err != nil {
		t.Fatalf("ReadFile(output asset) error = %v", err)
	}
	if string(got) != "hero-bytes" {
		t.Fatalf("copied asset contents = %q, want %q", string(got), "hero-bytes")
	}
}

func TestCopyAssetsRecordsMissingAssetDiagnostics(t *testing.T) {
	t.Parallel()

	vaultRoot := t.TempDir()
	outputRoot := t.TempDir()

	collector := NewCollector(vaultRoot, nil)
	collector.Register("images/missing.png")
	merged := MergeAssets(vaultRoot, nil, collector)
	collectorDiag := diag.NewCollector()

	if err := CopyAssets(vaultRoot, outputRoot, merged, collectorDiag); err != nil {
		t.Fatalf("CopyAssets() error = %v", err)
	}

	diagnostics := collectorDiag.Diagnostics()
	if len(diagnostics) != 1 {
		t.Fatalf("len(collectorDiag.Diagnostics()) = %d, want 1", len(diagnostics))
	}
	if got := diagnostics[0]; got.Kind != diag.KindUnresolvedAsset || got.Location.Path != "images/missing.png" {
		t.Fatalf("collectorDiag.Diagnostics()[0] = %#v, want unresolved_asset for missing source", got)
	}
	if _, err := os.Stat(filepath.Join(outputRoot, "assets", "missing.png")); !os.IsNotExist(err) {
		t.Fatalf("Stat(output missing asset) error = %v, want not-exist", err)
	}
}

func TestAssetCollectorRegisterRejectsWindowsAbsoluteAndUNCPaths(t *testing.T) {
	t.Parallel()

	for _, input := range []string{
		"C:/Windows/System32/drivers/etc/hosts",
		"C:\\Windows\\System32\\drivers\\etc\\hosts",
		"//server/share/photo.png",
		"\\\\server\\share\\photo.png",
	} {
		t.Run(input, func(t *testing.T) {
			collector := NewCollector(t.TempDir(), nil)
			if got := collector.Register(input); got != "" {
				t.Fatalf("Register(%q) = %q, want empty path", input, got)
			}
			if snapshot := collector.Snapshot(); len(snapshot) != 0 {
				t.Fatalf("len(Snapshot()) = %d, want 0", len(snapshot))
			}
		})
	}
}

func TestAssetCollectorRegisterRejectsSchemeBasedPaths(t *testing.T) {
	t.Parallel()

	for _, input := range []string{
		"http://example.com/photo.png",
		"https://example.com/photo.png",
		"data:image/png;base64,AAAA",
		"file:///tmp/photo.png",
		"mailto:foo@example.com",
	} {
		t.Run(input, func(t *testing.T) {
			collector := NewCollector(t.TempDir(), nil)
			if got := collector.Register(input); got != "" {
				t.Fatalf("Register(%q) = %q, want empty path", input, got)
			}
			if snapshot := collector.Snapshot(); len(snapshot) != 0 {
				t.Fatalf("len(Snapshot()) = %d, want 0", len(snapshot))
			}
		})
	}
}

func TestAssetCollectorRegisterTreatsSymlinkedSourceAsMissing(t *testing.T) {
	t.Parallel()

	vaultRoot := t.TempDir()
	externalRoot := t.TempDir()
	writeAssetFile(t, externalRoot, "hero.png", "outside")
	writeAssetSymlinkOrSkip(t, filepath.Join(externalRoot, "hero.png"), filepath.Join(vaultRoot, "images", "hero.png"))

	collector := NewCollector(vaultRoot, nil)
	got := collector.Register("images/hero.png")
	want := expectedHashedAssetPath(t, vaultRoot, "images/hero.png")
	if got != want {
		t.Fatalf("Register(images/hero.png) = %q, want %q", got, want)
	}
}

func TestCopyAssetsRecordsDiagnosticsForSymlinkedSources(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		srcPath  string
		setupSrc func(t *testing.T, vaultRoot string, externalRoot string)
	}{
		{
			name:    "file_symlink",
			srcPath: "images/hero.png",
			setupSrc: func(t *testing.T, vaultRoot string, externalRoot string) {
				writeAssetSymlinkOrSkip(t, filepath.Join(externalRoot, "hero.png"), filepath.Join(vaultRoot, "images", "hero.png"))
			},
		},
		{
			name:    "directory_symlink",
			srcPath: "images/hero.png",
			setupSrc: func(t *testing.T, vaultRoot string, externalRoot string) {
				writeAssetSymlinkOrSkip(t, externalRoot, filepath.Join(vaultRoot, "images"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vaultRoot := t.TempDir()
			outputRoot := t.TempDir()
			externalRoot := t.TempDir()
			writeAssetFile(t, externalRoot, "hero.png", "outside")
			tt.setupSrc(t, vaultRoot, externalRoot)

			assets := map[string]*model.Asset{
				tt.srcPath: {
					SrcPath:  tt.srcPath,
					RefCount: 1,
					DstPath:  "assets/hero.png",
				},
			}
			collectorDiag := diag.NewCollector()

			if err := CopyAssets(vaultRoot, outputRoot, assets, collectorDiag); err != nil {
				t.Fatalf("CopyAssets() error = %v", err)
			}

			diagnostics := collectorDiag.Diagnostics()
			if len(diagnostics) != 1 {
				t.Fatalf("len(collectorDiag.Diagnostics()) = %d, want 1", len(diagnostics))
			}
			if got := diagnostics[0]; got.Kind != diag.KindUnresolvedAsset || got.Location.Path != tt.srcPath {
				t.Fatalf("collectorDiag.Diagnostics()[0] = %#v, want unresolved_asset for symlink source", got)
			}
			if _, err := os.Stat(filepath.Join(outputRoot, "assets", "hero.png")); !os.IsNotExist(err) {
				t.Fatalf("Stat(output symlink asset) error = %v, want not-exist", err)
			}
		})
	}
}

func TestAssetCollectorRegisterReturnsStableSitePath(t *testing.T) {
	t.Parallel()

	vaultRoot := t.TempDir()
	writeAssetFile(t, vaultRoot, "images/photo.png", "left")
	writeAssetFile(t, vaultRoot, "attachments/photo.png", "right")

	indexed := map[string]*model.Asset{
		"attachments/photo.png": {SrcPath: "attachments/photo.png", RefCount: 1},
		"images/photo.png":      {SrcPath: "images/photo.png", RefCount: 1},
	}
	collector := NewCollector(vaultRoot, indexed)
	first := collector.Register("images/photo.png")
	second := collector.Register("images/photo.png")
	if first != second {
		t.Fatalf("Register() returned %q then %q, want stable site path", first, second)
	}

	merged := MergeAssets(vaultRoot, indexed, collector)
	if asset := merged["images/photo.png"]; asset == nil || asset.DstPath != first || asset.RefCount != 3 {
		t.Fatalf("merged[images/photo.png] = %#v, want stable DstPath %q and refcount 3", asset, first)
	}
}

func TestAssetCollectorRegisterHashesCaseInsensitiveBasenameCollisions(t *testing.T) {
	t.Parallel()

	vaultRoot := t.TempDir()
	outputRoot := t.TempDir()
	writeAssetFile(t, vaultRoot, "images/Photo.png", "left")
	writeAssetFile(t, vaultRoot, "attachments/photo.png", "right")

	indexed := map[string]*model.Asset{
		"images/Photo.png":      {SrcPath: "images/Photo.png", RefCount: 1},
		"attachments/photo.png": {SrcPath: "attachments/photo.png", RefCount: 1},
	}
	collector := NewCollector(vaultRoot, indexed)
	left := collector.Register("images/Photo.png")
	right := collector.Register("attachments/photo.png")

	if left == "assets/Photo.png" || left == "assets/photo.png" {
		t.Fatalf("Register(images/Photo.png) = %q, want hashed collision path", left)
	}
	if right == "assets/Photo.png" || right == "assets/photo.png" {
		t.Fatalf("Register(attachments/photo.png) = %q, want hashed collision path", right)
	}
	if !strings.HasPrefix(left, "assets/photo.") || !strings.HasSuffix(left, ".png") {
		t.Fatalf("Register(images/Photo.png) = %q, want assets/photo.<hash>.png", left)
	}
	if !strings.HasPrefix(right, "assets/photo.") || !strings.HasSuffix(right, ".png") {
		t.Fatalf("Register(attachments/photo.png) = %q, want assets/photo.<hash>.png", right)
	}
	if left == right {
		t.Fatalf("Register() returned %q and %q, want distinct paths for different contents", left, right)
	}

	merged := MergeAssets(vaultRoot, indexed, collector)
	if asset := merged["images/Photo.png"]; asset == nil || asset.DstPath != left {
		t.Fatalf("merged[images/Photo.png] = %#v, want stable DstPath %q", asset, left)
	}
	if asset := merged["attachments/photo.png"]; asset == nil || asset.DstPath != right {
		t.Fatalf("merged[attachments/photo.png] = %#v, want stable DstPath %q", asset, right)
	}

	collectorDiag := diag.NewCollector()
	if err := CopyAssets(vaultRoot, outputRoot, merged, collectorDiag); err != nil {
		t.Fatalf("CopyAssets() error = %v", err)
	}
	if collectorDiag.Len() != 0 {
		t.Fatalf("collectorDiag.Diagnostics() = %#v, want no diagnostics", collectorDiag.Diagnostics())
	}
	if _, err := os.Stat(filepath.Join(outputRoot, filepath.FromSlash(left))); err != nil {
		t.Fatalf("Stat(%q) error = %v", left, err)
	}
	if _, err := os.Stat(filepath.Join(outputRoot, filepath.FromSlash(right))); err != nil {
		t.Fatalf("Stat(%q) error = %v", right, err)
	}
}

func writeAssetFile(t *testing.T, root string, relPath string, content string) {
	t.Helper()

	absPath := filepath.Join(root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", absPath, err)
	}
	if err := os.WriteFile(absPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", absPath, err)
	}
}

func expectedHashedAssetPath(t *testing.T, vaultRoot string, srcPath string) string {
	t.Helper()

	hashValue, err := assetHash(vaultRoot, srcPath)
	if err != nil {
		hashValue = missingAssetHash(srcPath)
	}

	return hashedAssetPath(srcPath, hashValue)
}

func writeAssetSymlinkOrSkip(t *testing.T, targetPath string, linkPath string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(linkPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", filepath.Dir(linkPath), err)
	}
	if err := os.Symlink(targetPath, linkPath); err != nil {
		t.Skipf("Symlink(%q, %q) unsupported: %v", targetPath, linkPath, err)
	}
}
