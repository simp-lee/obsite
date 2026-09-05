package social

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestGenerateHasFixedCanonicalInputAndPNGSnapshot(t *testing.T) {
	result, err := Generate(Input{CanonicalURL: "https://example.test/docs/start/", SiteTitle: "Obsite", Title: "Deterministic title", Context: "Docs / v1", Author: "Alice", Date: "2026-04-06T00:00:00Z", Status: "stable"})
	if err != nil {
		t.Fatal(err)
	}
	const wantJSON = `{"canonicalURL":"https://example.test/docs/start/","siteTitle":"Obsite","title":"Deterministic title","context":"Docs / v1","author":"Alice","date":"2026-04-06T00:00:00Z","status":"stable","generator":"obsite-social-card-v1"}`
	const wantPNGHash = "278d7c15699d84b77d8b69e30e2007c827a8c81ede047610ed7b8fadee8eb8c7"
	if string(result.CanonicalJSON) != wantJSON {
		t.Fatalf("canonical JSON = %s, want %s", result.CanonicalJSON, wantJSON)
	}
	hash := sha256.Sum256(result.PNG)
	if got := hex.EncodeToString(hash[:]); got != wantPNGHash {
		t.Fatalf("PNG hash = %s, want %s", got, wantPNGHash)
	}
	if want := "assets/social/2647f899c632ffe86c58d0dc5bea3d840e2aaec648cbcac1706defb3f772b7ea/135aae56e0b483dbe9d00d16d19016f01bd8b40d7f2005c25c8f93508c602f0a-" + wantPNGHash + ".png"; result.Path != want {
		t.Fatalf("path = %s, want %s", result.Path, want)
	}
}

func TestGenerateEnforcesFixedColorsAndNoCoverGeometry(t *testing.T) {
	result, err := Generate(Input{CanonicalURL: "https://example.test/no-cover/", SiteTitle: "Site", Title: "Title"})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := png.Decode(bytes.NewReader(result.PNG))
	if err != nil {
		t.Fatal(err)
	}
	for point, want := range map[image.Point]color.RGBA{
		{0, 0}:    {R: 0x0f, G: 0x17, B: 0x2a, A: 0xff},
		{72, 72}:  {R: 0x38, G: 0xbd, B: 0xf8, A: 0xff},
		{83, 557}: {R: 0x38, G: 0xbd, B: 0xf8, A: 0xff},
		{84, 72}:  {R: 0x0f, G: 0x17, B: 0x2a, A: 0xff},
	} {
		if got := decoded.At(point.X, point.Y); got != want {
			t.Fatalf("pixel %v = %#v, want %#v", point, got, want)
		}
	}
	cover := image.NewRGBA(image.Rect(0, 0, 1, 1))
	cover.Set(0, 0, color.RGBA{R: 255, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, cover); err != nil {
		t.Fatal(err)
	}
	withCover, err := Generate(Input{CanonicalURL: "https://example.test/cover/", SiteTitle: "Site", Title: "Title", Cover: encoded.Bytes()})
	if err != nil {
		t.Fatal(err)
	}
	coverImage, err := png.Decode(bytes.NewReader(withCover.PNG))
	if err != nil {
		t.Fatal(err)
	}
	if got := coverImage.At(767, 0); got != (color.RGBA{R: 0x0f, G: 0x17, B: 0x2a, A: 0xff}) {
		t.Fatalf("cover boundary pixel = %#v", got)
	}
	if got := coverImage.At(768, 0); got != (color.RGBA{R: 211, G: 4, B: 7, A: 0xff}) {
		t.Fatalf("cover overlay pixel = %#v", got)
	}
}

func TestGenerateTruncatesLongGraphemeTextDeterministically(t *testing.T) {
	input := Input{CanonicalURL: "https://example.test/long/", SiteTitle: "Site", Title: "这是一个非常长的标题😀这是一个非常长的标题😀这是一个非常长的标题😀", Context: "这是一个很长的上下文", Author: "作者作者作者作者作者", Date: "2026-04-06", Status: "stable"}
	first, err := Generate(input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Generate(input)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.PNG, second.PNG) {
		t.Fatal("long grapheme input was not stable PNG output")
	}
	const wantHash = "ea44797781746d84ed2d9240774e3926e778befeff47eec0a4d438d7d775b3f6"
	if first.PNGHash != wantHash {
		t.Fatalf("long grapheme PNG hash = %s, want %s", first.PNGHash, wantHash)
	}
	if !bytes.HasPrefix(first.PNG, []byte{137, 80, 78, 71, 13, 10, 26, 10}) {
		t.Fatal("long grapheme output is not a PNG")
	}
}

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

func TestGenerateDistinguishesUnsupportedUnicodeTitles(t *testing.T) {
	grinning, err := Generate(Input{CanonicalURL: "https://example.test/emoji/", SiteTitle: "Site", Title: "😀"})
	if err != nil {
		t.Fatal(err)
	}
	unicorn, err := Generate(Input{CanonicalURL: "https://example.test/emoji/", SiteTitle: "Site", Title: "🦄"})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(grinning.PNG, unicorn.PNG) || grinning.PNGHash == unicorn.PNGHash {
		t.Fatal("distinct unsupported Unicode titles produced the same PNG")
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
