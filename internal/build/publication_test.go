package build

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPublisherKeepsPublishedOutputWhenBackupCleanupPartiallyFails(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "site")
	if err := os.MkdirAll(output, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(output, managedOutputMarkerFilename), []byte(managedOutputMarkerContents), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(output, "index.html"), []byte("old output"), 0o644); err != nil {
		t.Fatal(err)
	}
	publisher, err := prepareStagedOutputPublisher(root, output)
	if err != nil {
		t.Fatal(err)
	}
	staging := publisher.OutputPath()
	if err := writeManagedOutputMarker(staging); err != nil {
		t.Fatal(err)
	}
	want := []byte("new output")
	if err := writeOutputFile(staging, "index.html", want); err != nil {
		t.Fatal(err)
	}
	cleanupFailure := errors.New("simulated partial backup cleanup failure")
	originalRemove := stagedOutputRemoveAll
	stagedOutputRemoveAll = func(name string) error {
		if strings.Contains(name, "-backup-") {
			if err := os.Remove(filepath.Join(name, managedOutputMarkerFilename)); err != nil {
				return err
			}
			if err := os.Remove(filepath.Join(name, "index.html")); err != nil {
				return err
			}
			return cleanupFailure
		}
		return originalRemove(name)
	}
	defer func() { stagedOutputRemoveAll = originalRemove }()
	if err := publisher.Finalize(true); err != nil {
		t.Fatalf("Finalize() error = %v", err)
	}
	if !errors.Is(publisher.cleanupErr, cleanupFailure) {
		t.Fatalf("cleanup error = %v, want %v", publisher.cleanupErr, cleanupFailure)
	}
	after, err := os.ReadFile(filepath.Join(output, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, want) {
		t.Fatalf("output = %q, want published output %q", after, want)
	}
	marker, err := os.ReadFile(filepath.Join(output, managedOutputMarkerFilename))
	if err != nil {
		t.Fatal(err)
	}
	if string(marker) != managedOutputMarkerContents {
		t.Fatalf("output marker = %q, want %q", marker, managedOutputMarkerContents)
	}
}

func TestPublisherPublishesExpectedOutputDirectoryPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permission bits are not portable on Windows")
	}

	for _, tt := range []struct {
		name       string
		prepareOld bool
		wantMode   os.FileMode
	}{
		{name: "new output", wantMode: 0o755},
		{name: "replace managed output", prepareOld: true, wantMode: 0o751},
		{name: "replace read-only managed output", prepareOld: true, wantMode: 0o555},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			output := filepath.Join(root, "site")
			t.Cleanup(func() { _ = os.Chmod(output, 0o755) })
			if tt.prepareOld {
				if err := writeManagedOutputMarker(output); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(output, tt.wantMode); err != nil {
					t.Fatal(err)
				}
			}

			publisher, err := prepareStagedOutputPublisher(root, output)
			if err != nil {
				t.Fatal(err)
			}
			if err := writeManagedOutputMarker(publisher.OutputPath()); err != nil {
				t.Fatal(err)
			}
			if err := publisher.Finalize(true); err != nil {
				t.Fatal(err)
			}
			if publisher.cleanupErr != nil {
				t.Fatalf("backup cleanup error = %v", publisher.cleanupErr)
			}

			info, err := os.Stat(output)
			if err != nil {
				t.Fatal(err)
			}
			if got := info.Mode().Perm(); got != tt.wantMode {
				t.Fatalf("published output mode = %04o, want %04o", got, tt.wantMode)
			}
		})
	}
}

func TestPublisherCleansStagingWhenReadOnlyOutputBackupFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permission bits are not portable on Windows")
	}

	root := t.TempDir()
	output := filepath.Join(root, "site")
	if err := writeManagedOutputMarker(output); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(output, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(output, 0o755) })

	publisher, err := prepareStagedOutputPublisher(root, output)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeManagedOutputMarker(publisher.OutputPath()); err != nil {
		t.Fatal(err)
	}
	originalRename := stagedOutputRename
	stagedOutputRename = func(oldPath, newPath string) error {
		if oldPath == output {
			return errors.New("injected backup failure")
		}
		return originalRename(oldPath, newPath)
	}
	t.Cleanup(func() { stagedOutputRename = originalRename })

	if err := publisher.Finalize(true); err == nil || !strings.Contains(err.Error(), "injected backup failure") {
		t.Fatalf("Finalize() error = %v, want injected backup failure", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "site" {
		t.Fatalf("transaction left temporary output: %v", entries)
	}
}

func TestPublisherPreservesAllOutputAndCleansStagingOnPublicationFailures(t *testing.T) {
	for _, failure := range []string{"staging write", "backup rename", "publication rename"} {
		t.Run(failure, func(t *testing.T) {
			root := t.TempDir()
			output := filepath.Join(root, "site")
			if err := writeManagedOutputMarker(output); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"index.html", ".obsite-cache/manifest.json", "assets/social/old/card.png"} {
				if err := writeOutputFile(output, name, []byte("previous "+name)); err != nil {
					t.Fatal(err)
				}
			}
			before := strictOutputBytes(t, output)
			publisher, err := prepareStagedOutputPublisher(root, output)
			if err != nil {
				t.Fatal(err)
			}
			staging := publisher.OutputPath()
			if err := writeManagedOutputMarker(staging); err != nil {
				t.Fatal(err)
			}
			if err := writeOutputFile(staging, "assets/social/new/card.png", []byte("new card")); err != nil {
				t.Fatal(err)
			}
			if failure == "staging write" {
				// A directory at a file destination deterministically fails on all
				// supported platforms, including privileged test processes.
				if err := os.Mkdir(filepath.Join(staging, "index.html"), 0o755); err != nil {
					t.Fatal(err)
				}
				registry := newStrictOutputRegistry("", nil)
				if err := registry.write(staging, "index.html", "index", []byte("new page")); err == nil {
					t.Fatal("staging write unexpectedly succeeded")
				}
				if err := publisher.Finalize(false); err != nil {
					t.Fatal(err)
				}
			} else {
				originalRename := stagedOutputRename
				stagedOutputRename = func(oldPath, newPath string) error {
					if failure == "backup rename" && oldPath == output || failure == "publication rename" && oldPath == staging {
						return errors.New("injected " + failure + " failure")
					}
					return originalRename(oldPath, newPath)
				}
				t.Cleanup(func() { stagedOutputRename = originalRename })
				if err := publisher.Finalize(true); err == nil || !strings.Contains(err.Error(), "injected "+failure) {
					t.Fatalf("Finalize() error = %v, want injected failure", err)
				}
			}
			after := strictOutputBytes(t, output)
			if len(after) != len(before) {
				t.Fatalf("output file count changed: %d -> %d", len(before), len(after))
			}
			for name, data := range before {
				if !bytes.Equal(data, after[name]) {
					t.Fatalf("output %q changed after %s failure", name, failure)
				}
			}
			entries, err := os.ReadDir(root)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 || entries[0].Name() != "site" {
				t.Fatalf("transaction left temporary output: %v", entries)
			}
		})
	}
}

func TestStrictOutputRegistryRejectsDuplicateOwners(t *testing.T) {
	root := t.TempDir()
	registry := newStrictOutputRegistry("", nil)
	if err := registry.write(root, "index.html", "index", []byte("one")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "index.html")); err != nil {
		t.Fatal(err)
	}
	if err := registry.write(root, "index.html", "article:home", []byte("two")); err == nil {
		t.Fatal("duplicate output write error = nil")
	}
	if err := registry.claim("assets/card.png", "asset:left"); err != nil {
		t.Fatal(err)
	}
	if err := registry.claim("assets/card.png", "asset:right"); err != nil {
		t.Fatalf("identical asset destination claim error = %v", err)
	}
	if err := registry.claim("assets/card.png", "runtime"); err == nil {
		t.Fatal("cross-owner output claim error = nil")
	}
}
