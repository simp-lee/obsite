package build

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestStrictBuildPublishesOneIndependentSocialCardPerArticle(t *testing.T) {
	vault := copyFixtureVault(t, "feature-vault")
	output := filepath.Join(t.TempDir(), "site")
	result, err := BuildWithOptions(vault, output, Options{})
	if err != nil {
		t.Fatal(err)
	}
	cards := make([]string, 0)
	if err := filepath.Walk(output, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !info.IsDir() && filepath.Dir(filepath.Dir(path)) == filepath.Join(output, "assets", "social") {
			cards = append(cards, path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(cards) != result.NotePages {
		t.Fatalf("social card count = %d, article pages = %d", len(cards), result.NotePages)
	}
	for _, card := range cards {
		file, err := os.Open(card)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := png.DecodeConfig(file)
		_ = file.Close()
		if err != nil {
			t.Fatalf("decode social card %q: %v", card, err)
		}
		if decoded.Width != 1200 || decoded.Height != 630 {
			t.Fatalf("social card %q dimensions = %dx%d", card, decoded.Width, decoded.Height)
		}
	}
}
