package vault

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestScanCollectsMarkdownAndResourceCandidates(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	writeVaultFile(t, vaultPath, ".obsidian/app.json", `{"attachmentFolderPath":"assets/uploads"}`)
	writeVaultFile(t, vaultPath, ".obsidian/workspace.json", `{}`)
	writeVaultFile(t, vaultPath, "notes/alpha.md", "# Alpha")
	writeVaultFile(t, vaultPath, "notes/Guide.MD", "# Guide")
	writeVaultFile(t, vaultPath, "notes/diagram.png", "png")
	writeVaultFile(t, vaultPath, "assets/uploads/photo.jpg", "jpg")
	writeVaultFile(t, vaultPath, "assets/uploads/attachment.pdf", "pdf")
	writeVaultFile(t, vaultPath, "scripts/build.js", "console.log('ok')")
	writeVaultFile(t, vaultPath, ".hidden/private.md", "# Hidden")
	writeVaultFile(t, vaultPath, "notes/.draft.md", "# Draft")
	writeVaultFile(t, vaultPath, "node_modules/pkg/readme.md", "# Ignore")

	got, err := Scan(vaultPath)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	if got.VaultPath != vaultPath {
		t.Fatalf("VaultPath = %q, want %q", got.VaultPath, vaultPath)
	}
	if got.AttachmentFolderPath != "assets/uploads" {
		t.Fatalf("AttachmentFolderPath = %q, want %q", got.AttachmentFolderPath, "assets/uploads")
	}

	wantMarkdown := []string{
		"notes/Guide.MD",
		"notes/alpha.md",
	}
	if !reflect.DeepEqual(got.MarkdownFiles, wantMarkdown) {
		t.Fatalf("MarkdownFiles = %#v, want %#v", got.MarkdownFiles, wantMarkdown)
	}

	wantResources := []string{
		"assets/uploads/attachment.pdf",
		"assets/uploads/photo.jpg",
		"notes/diagram.png",
		"scripts/build.js",
	}
	if !reflect.DeepEqual(got.ResourceFiles, wantResources) {
		t.Fatalf("ResourceFiles = %#v, want %#v", got.ResourceFiles, wantResources)
	}

	if !got.HasResource("assets/uploads/photo.jpg") {
		t.Fatal("HasResource(photo.jpg) = false, want true")
	}
	if got.HasResource(".obsidian/workspace.json") {
		t.Fatal("HasResource(.obsidian/workspace.json) = true, want false")
	}
	if got.HasResource("node_modules/pkg/readme.md") {
		t.Fatal("HasResource(node_modules/pkg/readme.md) = true, want false")
	}
	if got.HasResource(".hidden/private.md") {
		t.Fatal("HasResource(.hidden/private.md) = true, want false")
	}
}

func TestScanNormalizesAttachmentFolderPath(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	writeVaultFile(t, vaultPath, ".obsidian/app.json", `{"attachmentFolderPath":".\\assets\\images\\..\\uploads"}`)
	writeVaultFile(t, vaultPath, "notes/post.md", "# Post")

	got, err := Scan(vaultPath)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	if got.AttachmentFolderPath != "assets/uploads" {
		t.Fatalf("AttachmentFolderPath = %q, want %q", got.AttachmentFolderPath, "assets/uploads")
	}
	if len(got.MarkdownFiles) != 1 || got.MarkdownFiles[0] != "notes/post.md" {
		t.Fatalf("MarkdownFiles = %#v, want %#v", got.MarkdownFiles, []string{"notes/post.md"})
	}
}

func TestScanRejectsWindowsAbsoluteAttachmentFolderPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
	}{
		{name: "windows backslash absolute path", raw: `C:\attachments`},
		{name: "windows slash absolute path", raw: "C:/attachments"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vaultPath := t.TempDir()
			writeVaultFile(t, vaultPath, ".obsidian/app.json", fmt.Sprintf(`{"attachmentFolderPath":%q}`, tt.raw))
			writeVaultFile(t, vaultPath, "notes/post.md", "# Post")

			_, err := Scan(vaultPath)
			if err == nil {
				t.Fatalf("Scan() error = nil, want rejection for %q", tt.raw)
			}
			if !strings.Contains(err.Error(), "attachmentFolderPath must stay inside the vault") {
				t.Fatalf("Scan() error = %v, want vault boundary rejection", err)
			}
		})
	}
}

func TestScanPreservesLeadingSlashAttachmentFolderPath(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	writeVaultFile(t, vaultPath, ".obsidian/app.json", `{"attachmentFolderPath":"/assets/uploads"}`)
	writeVaultFile(t, vaultPath, "notes/post.md", "# Post")

	got, err := Scan(vaultPath)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	if got.AttachmentFolderPath != "assets/uploads" {
		t.Fatalf("AttachmentFolderPath = %q, want %q", got.AttachmentFolderPath, "assets/uploads")
	}
}

func TestScanKeepsConfiguredAttachmentFolderInsideSkippedSubtreeExcluded(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                 string
		attachmentFolderPath string
		attachmentResources  []string
		skippedMarkdown      []string
		skippedResources     []string
	}{
		{
			name:                 "hidden directory",
			attachmentFolderPath: ".hidden/attachments",
			attachmentResources: []string{
				".hidden/attachments/nested/diagram.svg",
				".hidden/attachments/photo.png",
			},
			skippedMarkdown: []string{
				".hidden/private.md",
			},
			skippedResources: []string{
				".hidden/other/skip.txt",
			},
		},
		{
			name:                 "node_modules subtree",
			attachmentFolderPath: "node_modules/pkg/assets",
			attachmentResources: []string{
				"node_modules/pkg/assets/photo.png",
			},
			skippedMarkdown: []string{
				"node_modules/pkg/readme.md",
			},
			skippedResources: []string{
				"node_modules/other/skip.txt",
			},
		},
		{
			name:                 ".obsidian subtree",
			attachmentFolderPath: ".obsidian/assets",
			attachmentResources: []string{
				".obsidian/assets/photo.png",
			},
			skippedResources: []string{
				".obsidian/plugins/extra.js",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vaultPath := t.TempDir()
			writeVaultFile(t, vaultPath, ".obsidian/app.json", fmt.Sprintf(`{"attachmentFolderPath":%q}`, tt.attachmentFolderPath))
			writeVaultFile(t, vaultPath, "notes/post.md", "# Post")
			writeVaultFile(t, vaultPath, "assets/public/logo.png", "logo")

			for _, relPath := range tt.attachmentResources {
				writeVaultFile(t, vaultPath, relPath, "asset")
			}
			for _, relPath := range tt.skippedMarkdown {
				writeVaultFile(t, vaultPath, relPath, "# Skip")
			}
			for _, relPath := range tt.skippedResources {
				writeVaultFile(t, vaultPath, relPath, "skip")
			}

			got, err := Scan(vaultPath)
			if err != nil {
				t.Fatalf("Scan() error = %v", err)
			}

			if got.AttachmentFolderPath != tt.attachmentFolderPath {
				t.Fatalf("AttachmentFolderPath = %q, want %q", got.AttachmentFolderPath, tt.attachmentFolderPath)
			}

			wantMarkdown := []string{"notes/post.md"}
			if !reflect.DeepEqual(got.MarkdownFiles, wantMarkdown) {
				t.Fatalf("MarkdownFiles = %#v, want %#v", got.MarkdownFiles, wantMarkdown)
			}

			wantResources := []string{"assets/public/logo.png"}
			if !reflect.DeepEqual(got.ResourceFiles, wantResources) {
				t.Fatalf("ResourceFiles = %#v, want %#v", got.ResourceFiles, wantResources)
			}

			for _, relPath := range tt.attachmentResources {
				if got.HasResource(relPath) {
					t.Fatalf("HasResource(%q) = true, want false", relPath)
				}
			}
			for _, relPath := range tt.skippedMarkdown {
				if got.HasMarkdown(relPath) {
					t.Fatalf("HasMarkdown(%q) = true, want false", relPath)
				}
			}
			for _, relPath := range tt.skippedResources {
				if got.HasResource(relPath) {
					t.Fatalf("HasResource(%q) = true, want false", relPath)
				}
			}
			if !got.HasMarkdown("notes/post.md") {
				t.Fatal("HasMarkdown(notes/post.md) = false, want true")
			}
			if !got.HasResource("assets/public/logo.png") {
				t.Fatal("HasResource(assets/public/logo.png) = false, want true")
			}
		})
	}
}

func TestScanTreatsConfiguredAttachmentFolderPathAsMetadataInsideSkippedSubtree(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	writeVaultFile(t, vaultPath, ".obsidian/app.json", `{"attachmentFolderPath":".hidden/attachments"}`)
	writeVaultFile(t, vaultPath, "notes/post.md", "# Post")
	writeVaultFile(t, vaultPath, ".hidden/attachments/nested/diagram.svg", "svg")
	writeVaultFile(t, vaultPath, ".hidden/attachments/photo.png", "png")
	writeVaultFile(t, vaultPath, ".hidden/attachments/.hidden/secret.png", "secret")
	writeVaultFile(t, vaultPath, ".hidden/attachments/.obsidian/workspace.json", `{}`)
	writeVaultFile(t, vaultPath, ".hidden/attachments/node_modules/pkg/file.bin", "bin")

	got, err := Scan(vaultPath)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	if got.AttachmentFolderPath != ".hidden/attachments" {
		t.Fatalf("AttachmentFolderPath = %q, want %q", got.AttachmentFolderPath, ".hidden/attachments")
	}

	wantMarkdown := []string{"notes/post.md"}
	if !reflect.DeepEqual(got.MarkdownFiles, wantMarkdown) {
		t.Fatalf("MarkdownFiles = %#v, want %#v", got.MarkdownFiles, wantMarkdown)
	}

	if len(got.ResourceFiles) != 0 {
		t.Fatalf("ResourceFiles = %#v, want empty", got.ResourceFiles)
	}

	if !got.HasMarkdown("notes/post.md") {
		t.Fatal("HasMarkdown(notes/post.md) = false, want true")
	}
	if got.HasResource(".hidden/attachments/photo.png") {
		t.Fatal("HasResource(.hidden/attachments/photo.png) = true, want false")
	}
	if got.HasResource(".hidden/attachments/.hidden/secret.png") {
		t.Fatal("HasResource(.hidden/attachments/.hidden/secret.png) = true, want false")
	}
	if got.HasResource(".hidden/attachments/.obsidian/workspace.json") {
		t.Fatal("HasResource(.hidden/attachments/.obsidian/workspace.json) = true, want false")
	}
	if got.HasResource(".hidden/attachments/node_modules/pkg/file.bin") {
		t.Fatal("HasResource(.hidden/attachments/node_modules/pkg/file.bin) = true, want false")
	}
}

func TestScanSkipsSymlinkEntries(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	outsidePath := t.TempDir()

	writeVaultFile(t, vaultPath, ".obsidian/app.json", `{"attachmentFolderPath":"assets/uploads"}`)
	writeVaultFile(t, vaultPath, "notes/post.md", "# Post")
	writeVaultFile(t, vaultPath, "assets/uploads/photo.png", "png")
	writeVaultFile(t, outsidePath, "outside.md", "# Outside")
	writeVaultFile(t, outsidePath, "outside.png", "png")

	writeVaultSymlink(t, filepath.Join(outsidePath, "outside.md"), filepath.Join(vaultPath, "notes", "link.md"))
	writeVaultSymlink(t, filepath.Join(outsidePath, "outside.png"), filepath.Join(vaultPath, "assets", "link.png"))

	got, err := Scan(vaultPath)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	wantMarkdown := []string{"notes/post.md"}
	if !reflect.DeepEqual(got.MarkdownFiles, wantMarkdown) {
		t.Fatalf("MarkdownFiles = %#v, want %#v", got.MarkdownFiles, wantMarkdown)
	}

	wantResources := []string{"assets/uploads/photo.png"}
	if !reflect.DeepEqual(got.ResourceFiles, wantResources) {
		t.Fatalf("ResourceFiles = %#v, want %#v", got.ResourceFiles, wantResources)
	}

	if got.HasMarkdown("notes/link.md") {
		t.Fatal("HasMarkdown(notes/link.md) = true, want false")
	}
	if got.HasResource("assets/link.png") {
		t.Fatal("HasResource(assets/link.png) = true, want false")
	}
}

func TestScanRejectsSymlinkedObsidianConfigPaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		setupVault func(t *testing.T, vaultPath string)
	}{
		{
			name: "symlinked obsidian directory",
			setupVault: func(t *testing.T, vaultPath string) {
				outsidePath := t.TempDir()
				externalConfigDir := filepath.Join(outsidePath, "external-obsidian")
				writeVaultFile(t, externalConfigDir, "app.json", `{"attachmentFolderPath":"outside-assets"}`)
				writeVaultSymlink(t, externalConfigDir, filepath.Join(vaultPath, ".obsidian"))
			},
		},
		{
			name: "symlinked app json",
			setupVault: func(t *testing.T, vaultPath string) {
				outsidePath := t.TempDir()
				writeVaultFile(t, outsidePath, "app.json", `{"attachmentFolderPath":"outside-assets"}`)
				if err := os.MkdirAll(filepath.Join(vaultPath, ".obsidian"), 0o755); err != nil {
					t.Fatalf("MkdirAll(.obsidian) error = %v", err)
				}
				writeVaultSymlink(t, filepath.Join(outsidePath, "app.json"), filepath.Join(vaultPath, ".obsidian", "app.json"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vaultPath := t.TempDir()
			writeVaultFile(t, vaultPath, "notes/post.md", "# Post")
			tt.setupVault(t, vaultPath)

			_, err := Scan(vaultPath)
			if err == nil {
				t.Fatal("Scan() error = nil, want symlink rejection")
			}
			if !strings.Contains(err.Error(), "must not be a symbolic link") {
				t.Fatalf("Scan() error = %v, want symlink rejection", err)
			}
		})
	}
}

func writeVaultFile(t *testing.T, vaultPath string, relPath string, content string) {
	t.Helper()

	absPath := filepath.Join(vaultPath, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", filepath.Dir(absPath), err)
	}
	if err := os.WriteFile(absPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", absPath, err)
	}
}

func writeVaultSymlink(t *testing.T, targetPath string, linkPath string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(linkPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", filepath.Dir(linkPath), err)
	}
	if err := os.Symlink(targetPath, linkPath); err != nil {
		t.Skipf("Symlink(%q, %q) unsupported: %v", targetPath, linkPath, err)
	}
}
