package fsutil

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestResolveVaultOutputBoundary(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	vault := filepath.Join(root, "vault")
	if err := os.MkdirAll(vault, 0o755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		output     string
		wantInside bool
		wantErr    string
	}{
		{name: "vault descendant", output: filepath.Join(vault, "public"), wantInside: true},
		{name: "external output", output: filepath.Join(root, "published", "site")},
		{name: "vault root", output: vault, wantErr: "must not equal the vault root"},
		{name: "vault ancestor", output: root, wantErr: "must not contain the vault"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveVaultOutput(vault, tt.output)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ResolveVaultOutput() error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveVaultOutput() error = %v", err)
			}
			if got.VaultPath != vault {
				t.Fatalf("VaultPath = %q, want %q", got.VaultPath, vault)
			}
			if gotInside := PathWithinRoot(got.VaultPath, got.OutputPath); gotInside != tt.wantInside {
				t.Fatalf("resolved output inside vault = %v, want %v", gotInside, tt.wantInside)
			}
		})
	}
}

func TestResolveVaultOutputResolvesExistingParentSymlink(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on some Windows hosts")
	}

	root := t.TempDir()
	vault := filepath.Join(root, "vault")
	external := filepath.Join(root, "external")
	alias := filepath.Join(root, "alias")
	for _, dir := range []string{vault, external} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(external, alias); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveVaultOutput(vault, filepath.Join(alias, "nested", "site"))
	if err != nil {
		t.Fatalf("ResolveVaultOutput() error = %v", err)
	}
	want := filepath.Join(external, "nested", "site")
	if got.OutputPath != want {
		t.Fatalf("OutputPath = %q, want %q", got.OutputPath, want)
	}
	if PathWithinRoot(got.VaultPath, got.OutputPath) {
		t.Fatal("resolved output is inside vault, want external output")
	}
}

func TestResolveVaultOutputRejectsFinalSymlink(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on some Windows hosts")
	}

	root := t.TempDir()
	vault := filepath.Join(root, "vault")
	target := filepath.Join(root, "target")
	output := filepath.Join(root, "site")
	for _, dir := range []string{vault, target} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(target, output); err != nil {
		t.Fatal(err)
	}

	_, err := ResolveVaultOutput(vault, output)
	if err == nil || !strings.Contains(err.Error(), "must not be a symbolic link") {
		t.Fatalf("ResolveVaultOutput() error = %v, want final symlink rejection", err)
	}
}

func TestContainedRegularFileRejectsExternalAndSymlinkInputs(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on some Windows hosts")
	}

	root := t.TempDir()
	vault := filepath.Join(root, "vault")
	external := filepath.Join(root, "sentinel.txt")
	if err := os.MkdirAll(filepath.Join(vault, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(external, []byte("sentinel"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vault, "notes", "valid.md"), []byte("valid"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(vault, "notes", "escaped.md")); err != nil {
		t.Fatal(err)
	}

	if _, _, err := InspectContainedRegularFile(vault, external); !errors.Is(err, ErrPathOutsideRoot) {
		t.Fatalf("external InspectContainedRegularFile() error = %v, want ErrPathOutsideRoot", err)
	}
	if _, _, err := InspectContainedRegularFile(vault, filepath.Join("notes", "escaped.md")); !errors.Is(err, ErrSymlinkPath) {
		t.Fatalf("symlink InspectContainedRegularFile() error = %v, want ErrSymlinkPath", err)
	}
	resolved, data, _, err := ReadContainedRegularFile(vault, filepath.Join("notes", "valid.md"))
	if err != nil {
		t.Fatalf("ReadContainedRegularFile() error = %v", err)
	}
	if resolved != filepath.Join(vault, "notes", "valid.md") || string(data) != "valid" {
		t.Fatalf("ReadContainedRegularFile() = %q, %q", resolved, data)
	}
}
