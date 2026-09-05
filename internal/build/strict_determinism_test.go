package build

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestStrictBuildIsByteStableAcrossConcurrentBuilds(t *testing.T) {
	vault := copyFixtureVault(t, "feature-vault")
	roots := make([]string, 4)
	for index := range roots {
		roots[index] = filepath.Join(t.TempDir(), "site")
	}
	results := make(chan error, len(roots))
	for _, root := range roots {
		root := root
		go func() {
			_, err := BuildWithOptions(vault, root, Options{})
			results <- err
		}()
	}
	for range roots {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	want := strictOutputBytes(t, roots[0])
	for _, root := range roots[1:] {
		got := strictOutputBytes(t, root)
		if len(got) != len(want) {
			t.Fatalf("file counts differ: %d and %d", len(want), len(got))
		}
		for name, data := range want {
			if !bytes.Equal(data, got[name]) {
				t.Fatalf("concurrent output %q differs", name)
			}
		}
	}
}

func TestStrictBuildIsByteStableAcrossEquivalentInputOrders(t *testing.T) {
	makeVault := func(t *testing.T, reverse bool) string {
		t.Helper()
		vault := t.TempDir()
		config := "title: Ordered\nbaseURL: https://example.test/\nnavigation: []\n"
		rootIndex := "---\ntitle: Home\npublish: true\n---\nHome\n"
		sectionIndex := "---\ntitle: Docs\npublish: true\n---\nDocs\n"
		if reverse {
			config = "navigation: []\nbaseURL: https://example.test/\ntitle: Ordered\n"
			rootIndex = "---\npublish: true\ntitle: Home\n---\nHome\n"
			sectionIndex = "---\npublish: true\ntitle: Docs\n---\nDocs\n"
		}
		writeStrictFile(t, vault, "obsite.yaml", config)
		writeStrictFile(t, vault, "_index.md", rootIndex)
		writeStrictFile(t, vault, "docs/_index.md", sectionIndex)
		files := []struct{ name, title string }{{"02-beta.md", "Beta"}, {"01-alpha.md", "Alpha"}, {"10-page.md", "Page"}}
		if reverse {
			for left, right := 0, len(files)-1; left < right; left, right = left+1, right-1 {
				files[left], files[right] = files[right], files[left]
			}
		}
		for _, file := range files {
			frontmatter := "title: " + file.title + "\npublish: true\ntype: doc\n"
			if reverse {
				frontmatter = "type: doc\npublish: true\ntitle: " + file.title + "\n"
			}
			writeStrictFile(t, vault, "docs/"+file.name, "---\n"+frontmatter+"---\n"+file.title+"\n")
		}
		return vault
	}
	firstRoot := filepath.Join(t.TempDir(), "site")
	secondRoot := filepath.Join(t.TempDir(), "site")
	if _, err := BuildWithOptions(makeVault(t, false), firstRoot, Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildWithOptions(makeVault(t, true), secondRoot, Options{}); err != nil {
		t.Fatal(err)
	}
	first, second := strictOutputBytes(t, firstRoot), strictOutputBytes(t, secondRoot)
	if len(first) != len(second) {
		t.Fatalf("file counts = %d and %d", len(first), len(second))
	}
	for name, data := range first {
		if !bytes.Equal(data, second[name]) {
			t.Fatalf("input creation order changed output %q", name)
		}
	}
}

func TestStrictBuildIsByteStableAcrossOutputRootsAndRebuilds(t *testing.T) {
	vault := copyFixtureVault(t, "feature-vault")
	secondVault := copyFixtureVault(t, "feature-vault")
	firstRoot := filepath.Join(t.TempDir(), "first")
	secondRoot := filepath.Join(t.TempDir(), "second")
	if _, err := BuildWithOptions(vault, firstRoot, Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildWithOptions(secondVault, secondRoot, Options{}); err != nil {
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
