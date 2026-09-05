package build

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublisherRestoresPreviousOutputWhenBackupCleanupFails(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "site")
	if err := os.MkdirAll(output, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(output, managedOutputMarkerFilename), []byte(managedOutputMarkerContents), 0o644); err != nil {
		t.Fatal(err)
	}
	before := []byte("old output")
	if err := os.WriteFile(filepath.Join(output, "index.html"), before, 0o644); err != nil {
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
	if err := writeOutputFile(staging, "index.html", []byte("new output")); err != nil {
		t.Fatal(err)
	}
	originalRemove := stagedOutputRemoveAll
	stagedOutputRemoveAll = func(name string) error {
		if strings.Contains(name, "-backup-") {
			return errors.New("simulated backup cleanup failure")
		}
		return originalRemove(name)
	}
	defer func() { stagedOutputRemoveAll = originalRemove }()
	if err := publisher.Finalize(true); err == nil {
		t.Fatal("Finalize() error = nil")
	}
	after, err := os.ReadFile(filepath.Join(output, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("output = %q, want previous output %q", after, before)
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
