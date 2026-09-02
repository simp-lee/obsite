package build

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestStrictBuildIsByteStableAcrossOutputRootsAndRebuilds(t *testing.T) {
	vault := copyFixtureVault(t, "feature-vault")
	firstRoot := filepath.Join(t.TempDir(), "first")
	secondRoot := filepath.Join(t.TempDir(), "second")
	if _, err := BuildWithOptions(vault, firstRoot, Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildWithOptions(vault, secondRoot, Options{}); err != nil {
		t.Fatal(err)
	}
	first := strictOutputBytes(t, firstRoot)
	second := strictOutputBytes(t, secondRoot)
	if len(first) != len(second) {
		t.Fatalf("file counts = %d and %d", len(first), len(second))
	}
	for name, data := range first {
		if !bytes.Equal(data, second[name]) {
			t.Fatalf("output %q differs across roots", name)
		}
	}
	if _, err := BuildWithOptions(vault, firstRoot, Options{}); err != nil {
		t.Fatal(err)
	}
	third := strictOutputBytes(t, firstRoot)
	for name, data := range first {
		if !bytes.Equal(data, third[name]) {
			t.Fatalf("rebuild output %q differs", name)
		}
	}
}

func strictOutputBytes(t *testing.T, root string) map[string][]byte {
	t.Helper()
	paths := make([]string, 0)
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			paths = append(paths, rel)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)
	result := make(map[string][]byte, len(paths))
	for _, rel := range paths {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		result[rel] = data
	}
	return result
}
