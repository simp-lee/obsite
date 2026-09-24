package social

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

// Keep real fixtures for all accepted cover formats in the package. This keeps
// the acceptance tests independent of encoders and machine-local files.
//
//go:embed testdata/cover.png
var acceptancePNGCover []byte

//go:embed testdata/cover.jpg
var acceptanceJPEGCover []byte

//go:embed testdata/cover.webp
var acceptanceWebPCover []byte

func TestGenerateAcceptsPNGJPEGAndWebPCovers(t *testing.T) {
	covers := []struct {
		name string
		data []byte
	}{
		{name: "png", data: acceptancePNGCover},
		{name: "jpeg", data: acceptanceJPEGCover},
		{name: "webp", data: acceptanceWebPCover},
	}
	for _, fixture := range covers {
		t.Run(fixture.name, func(t *testing.T) {
			format, cover := fixture.name, fixture.data
			result, err := Generate(Input{CanonicalURL: "https://example.test/covers/" + format + "/", SiteTitle: "Site", Title: "Cover", Cover: cover})
			if err != nil {
				t.Fatalf("Generate() error = %v", err)
			}
			decoded, err := png.Decode(bytes.NewReader(result.PNG))
			if err != nil {
				t.Fatalf("generated card is not PNG: %v", err)
			}
			if got := decoded.Bounds().Size(); got != (image.Point{X: Width, Y: Height}) {
				t.Fatalf("card size = %v, want %dx%d", got, Width, Height)
			}
			if !bytes.Contains(result.CanonicalJSON, []byte(`"coverHash":"`)) {
				t.Fatalf("canonical input omitted cover hash: %s", result.CanonicalJSON)
			}
			withoutCover, err := Generate(Input{CanonicalURL: "https://example.test/covers/" + format + "/", SiteTitle: "Site", Title: "Cover"})
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Equal(result.PNG, withoutCover.PNG) {
				t.Fatalf("%s cover was accepted but not rendered", format)
			}
		})
	}
}

func TestGenerateTruncatesEachMetadataLine(t *testing.T) {
	const (
		long     = "metadata-value metadata-value metadata-value metadata-value metadata-value metadata-value"
		wantLine = "metadata-value metadata-value metadata-value metadat…"
	)
	for _, field := range []string{"Author", "Status"} {
		t.Run(field, func(t *testing.T) {
			input := Input{CanonicalURL: "https://example.test/metadata/", SiteTitle: "Site", Title: "Title", Cover: acceptancePNGCover}
			switch field {
			case "Author":
				input.Author = long
			case "Status":
				input.Status = long
			}
			got, err := Generate(input)
			if err != nil {
				t.Fatal(err)
			}
			expected := input
			switch field {
			case "Author":
				expected.Author = wantLine
			case "Status":
				expected.Status = wantLine
			}
			want, err := Generate(expected)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got.PNG, want.PNG) {
				t.Fatalf("%s was not rendered as its measured truncated line", field)
			}
		})
	}
}

func TestGenerateKeepsOversizedGraphemeInsideTextRegion(t *testing.T) {
	input := Input{
		CanonicalURL: "https://example.test/oversized-grapheme/",
		SiteTitle:    "Site",
		Title:        "Title",
		Author:       strings.Repeat("\u1100", 100),
		Cover:        acceptancePNGCover,
	}
	got, err := Generate(input)
	if err != nil {
		t.Fatal(err)
	}
	expected := input
	expected.Author = "…"
	want, err := Generate(expected)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.PNG, want.PNG) {
		t.Fatal("oversized author grapheme was not atomically ellipsized")
	}

	decoded, err := png.Decode(bytes.NewReader(got.PNG))
	if err != nil {
		t.Fatal(err)
	}
	outsideText := image.Rect(72+624, 0, 768, Height)
	for y := outsideText.Min.Y; y < outsideText.Max.Y; y++ {
		for x := outsideText.Min.X; x < outsideText.Max.X; x++ {
			pixel := color.RGBAModel.Convert(decoded.At(x, y)).(color.RGBA)
			if pixel != background {
				t.Fatalf("pixel outside fixed text region at (%d,%d) = %#v, want %#v", x, y, pixel, background)
			}
		}
	}
}

func TestGenerateTruncatesContextToOneGraphemeAwareLine(t *testing.T) {
	const (
		longContext = "文档 / 指南 / 上下文 / 文档 / 指南 / 上下文 / 文档 / 指南 / 上下文 / 文档 / 指南 / 上下文 / 文档 / 指南 / 上下文"
		wantContext = "文档 / 指南 / 上下文 / 文档 / 指南 / 上下文 / 文档 / 指…"
	)
	input := Input{CanonicalURL: "https://example.test/context/", SiteTitle: "Site", Title: "Title", Context: longContext, Cover: acceptancePNGCover}
	got, err := Generate(input)
	if err != nil {
		t.Fatal(err)
	}
	expected := input
	expected.Context = wantContext
	want, err := Generate(expected)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.PNG, want.PNG) {
		t.Fatal("context was not rendered as one grapheme-aware ellipsized line")
	}
}

func TestGenerateCanonicalInputMutationsChangeContentAddress(t *testing.T) {
	order := 3
	base := Input{
		CanonicalURL:   "https://example.test/canonical/",
		SiteTitle:      "Site",
		Title:          "Title",
		Context:        "Context",
		Description:    "Description",
		Date:           "2026-04-06",
		Updated:        "2026-04-07",
		Tags:           []string{"guide"},
		Aliases:        []string{"overview"},
		Slug:           "title",
		Type:           "doc",
		Order:          &order,
		Author:         "Author",
		Reviewed:       "2026-04-08",
		Status:         "stable",
		Audience:       "developers",
		ProductVersion: "v1",
		Series:         "guide",
		Cover:          acceptancePNGCover,
	}
	mutations := []struct {
		name       string
		mutate     func(*Input)
		visualCard bool
	}{
		{"canonical URL", func(input *Input) { input.CanonicalURL += "changed/" }, false},
		{"site title", func(input *Input) { input.SiteTitle = "Changed site" }, true},
		{"title", func(input *Input) { input.Title = "Changed title" }, true},
		{"context", func(input *Input) { input.Context = "Changed context" }, true},
		{"description", func(input *Input) { input.Description = "Changed description" }, false},
		{"date", func(input *Input) { input.Date = "2026-05-07" }, true},
		{"updated", func(input *Input) { input.Updated = "2026-05-08" }, false},
		{"tags", func(input *Input) { input.Tags = []string{"reference"} }, false},
		{"aliases", func(input *Input) { input.Aliases = []string{"changed"} }, false},
		{"slug", func(input *Input) { input.Slug = "changed" }, false},
		{"type", func(input *Input) { input.Type = "page" }, false},
		{"order", func(input *Input) { value := 4; input.Order = &value }, false},
		{"author", func(input *Input) { input.Author = "Bob" }, true},
		{"reviewed", func(input *Input) { input.Reviewed = "2026-05-09" }, false},
		{"status", func(input *Input) { input.Status = "experimental" }, true},
		{"audience", func(input *Input) { input.Audience = "operators" }, false},
		{"product version", func(input *Input) { input.ProductVersion = "v2" }, false},
		{"series", func(input *Input) { input.Series = "reference" }, false},
		{"cover", func(input *Input) { input.Cover = acceptanceJPEGCover }, true},
	}
	original, err := Generate(base)
	if err != nil {
		t.Fatal(err)
	}
	wantCoverHash := sha256.Sum256(base.Cover)
	if !strings.Contains(string(original.CanonicalJSON), `"coverHash":"`+hex.EncodeToString(wantCoverHash[:])+`"`) {
		t.Fatalf("canonical input omitted exact cover hash: %s", original.CanonicalJSON)
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			input := base
			mutation.mutate(&input)
			changed, err := Generate(input)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Equal(original.CanonicalJSON, changed.CanonicalJSON) || original.InputHash == changed.InputHash || original.Path == changed.Path {
				t.Fatalf("canonical mutation did not change identity: original=%#v changed=%#v", original, changed)
			}
			if mutation.visualCard && original.PNGHash == changed.PNGHash {
				t.Fatalf("canonical mutation did not change rendered card")
			}
			if mutation.name == "cover" {
				want := sha256.Sum256(acceptanceJPEGCover)
				if !strings.Contains(string(changed.CanonicalJSON), `"coverHash":"`+hex.EncodeToString(want[:])+`"`) {
					t.Fatalf("changed canonical input omitted exact cover hash: %s", changed.CanonicalJSON)
				}
			}
		})
	}
}

func TestGenerateNormalizesCanonicalInputBeforeRendering(t *testing.T) {
	firstOrder, secondOrder := 1, 1
	first, err := Generate(Input{
		CanonicalURL: " https://example.test/normalized/ ", SiteTitle: " Site ", Title: " Title ", Context: " Context ",
		Description: " Description ", Date: "2026-04-06T02:00:00+02:00", Updated: "2026-04-07",
		Tags: []string{" guide "}, Aliases: []string{" start "}, Slug: " start ", Type: " doc ", Order: &firstOrder,
		Author: " Alice ", Reviewed: "2026-04-08T00:00:00+00:00", Status: " stable ", Audience: " developers ",
		ProductVersion: " v1 ", Series: " guide ", Cover: acceptancePNGCover,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Generate(Input{
		CanonicalURL: "https://example.test/normalized/", SiteTitle: "Site", Title: "Title", Context: "Context",
		Description: "Description", Date: "2026-04-06T00:00:00Z", Updated: "2026-04-07T00:00:00Z",
		Tags: []string{"guide"}, Aliases: []string{"start"}, Slug: "start", Type: "doc", Order: &secondOrder,
		Author: "Alice", Reviewed: "2026-04-08T00:00:00Z", Status: "stable", Audience: "developers",
		ProductVersion: "v1", Series: "guide", Cover: acceptancePNGCover,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.CanonicalJSON, second.CanonicalJSON) || first.Path != second.Path || !bytes.Equal(first.PNG, second.PNG) {
		t.Fatalf("equivalent normalized inputs differ: first=%#v second=%#v", first, second)
	}
}

func TestGenerateUsesIndependentURLNamespaces(t *testing.T) {
	first, err := Generate(Input{CanonicalURL: "https://example.test/one/", SiteTitle: "Site", Title: "Same"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Generate(Input{CanonicalURL: "https://example.test/two/", SiteTitle: "Site", Title: "Same"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.PNG, second.PNG) {
		t.Fatal("URL-only change unexpectedly changed card pixels")
	}
	if first.Path == second.Path || first.InputHash == second.InputHash {
		t.Fatalf("distinct canonical URLs shared card identity: %q and %q", first.Path, second.Path)
	}
	for _, want := range []struct {
		url  string
		path string
	}{
		{url: "https://example.test/one/", path: first.Path},
		{url: "https://example.test/two/", path: second.Path},
	} {
		hash := sha256.Sum256([]byte(want.url))
		prefix := "assets/social/" + hex.EncodeToString(hash[:]) + "/"
		if !strings.HasPrefix(want.path, prefix) {
			t.Fatalf("path %q does not use URL namespace %q", want.path, prefix)
		}
	}
}
