package social

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestGenerateIsDeterministicAndContentAddressed(t *testing.T) {
	input := Input{CanonicalURL: "https://example.test/guide/start/", SiteTitle: "Site", Title: "A deterministic card", Context: "Guide", Author: "Alice", Date: "2026-04-05", Status: "stable"}
	first, err := Generate(input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Generate(input)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.PNG, second.PNG) || first.Path != second.Path || !bytes.Equal(first.CanonicalJSON, second.CanonicalJSON) {
		t.Fatal("same input produced different card output")
	}
	decoded, err := png.Decode(bytes.NewReader(first.PNG))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Bounds().Dx() != 1200 || decoded.Bounds().Dy() != 630 {
		t.Fatalf("bounds = %v", decoded.Bounds())
	}
	if first.Path == "" || first.InputHash == "" || first.PNGHash == "" {
		t.Fatalf("result = %#v", first)
	}
}

func TestGenerateSupportsEmbeddedCJKFallbackText(t *testing.T) {
	result, err := Generate(Input{CanonicalURL: "https://example.test/cjk/", SiteTitle: "Obsite 站点", Title: "中文文档", Context: "指南"})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := png.Decode(bytes.NewReader(result.PNG))
	if err != nil {
		t.Fatal(err)
	}
	backgroundPixel := decoded.At(100, 200)
	drawn := 0
	for y := 160; y < 390; y++ {
		for x := 72; x < 696; x++ {
			if decoded.At(x, y) != backgroundPixel {
				drawn++
			}
		}
	}
	if drawn == 0 {
		t.Fatal("CJK title produced no visible pixels")
	}
}

func TestGenerateUsesCoverAndRejectsInvalidCover(t *testing.T) {
	cover := image.NewRGBA(image.Rect(0, 0, 10, 10))
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			cover.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, cover); err != nil {
		t.Fatal(err)
	}
	withCover, err := Generate(Input{CanonicalURL: "https://example.test/a/", SiteTitle: "Site", Title: "Title", Cover: encoded.Bytes()})
	if err != nil {
		t.Fatal(err)
	}
	withoutCover, err := Generate(Input{CanonicalURL: "https://example.test/a/", SiteTitle: "Site", Title: "Title"})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(withCover.PNG, withoutCover.PNG) || withCover.Path == withoutCover.Path {
		t.Fatal("cover did not affect card output")
	}
	if _, err := Generate(Input{CanonicalURL: "https://example.test/a/", SiteTitle: "Site", Title: "Title", Cover: []byte("not an image")}); err == nil {
		t.Fatal("invalid cover accepted")
	}
}
