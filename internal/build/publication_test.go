package build

import (
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
