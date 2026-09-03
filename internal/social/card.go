// Package social generates deterministic page-level social card assets.
package social

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg"
	"image/png"
	"strings"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	_ "golang.org/x/image/webp"

	"github.com/rivo/uniseg"
)

const (
	Width            = 1200
	Height           = 630
	GeneratorVersion = "obsite-social-card-v1"
)

// cardFontData is the pinned KaTeX Main font used for every card. It is
// embedded so generation never consults a machine font installation.
//
//go:embed assets/KaTeX_Main-Regular.ttf
var cardFontData []byte

var parsedCardFont = sync.OnceValues(func() (*opentype.Font, error) { return opentype.Parse(cardFontData) })

var (
	background = color.RGBA{R: 0x0f, G: 0x17, B: 0x2a, A: 0xff}
	primary    = color.RGBA{R: 0xf8, G: 0xfa, B: 0xfc, A: 0xff}
	secondary  = color.RGBA{R: 0xcb, G: 0xd5, B: 0xe1, A: 0xff}
	accent     = color.RGBA{R: 0x38, G: 0xbd, B: 0xf8, A: 0xff}
)

// Input is the visible, normalized article input to the card generator.
type Input struct {
	CanonicalURL string
	SiteTitle    string
	Title        string
	Context      string
	Author       string
	Date         string
	Status       string
	Cover        []byte
}

// Result contains the canonical input, PNG bytes, and content-addressed output
// path. Path is always relative to the generated output root.
type Result struct {
	CanonicalJSON []byte
	PNG           []byte
	Path          string
	InputHash     string
	PNGHash       string
}

type canonicalInput struct {
	CanonicalURL string `json:"canonicalURL"`
	SiteTitle    string `json:"siteTitle"`
	Title        string `json:"title"`
	Context      string `json:"context"`
	Author       string `json:"author,omitempty"`
	Date         string `json:"date,omitempty"`
	Status       string `json:"status,omitempty"`
	CoverHash    string `json:"coverHash,omitempty"`
	Generator    string `json:"generator"`
}

// Generate creates a deterministic PNG without system fonts, timestamps, or
// randomness. A non-empty Cover must be a supported, decodable local image.
func Generate(input Input) (Result, error) {
	if strings.TrimSpace(input.CanonicalURL) == "" || strings.TrimSpace(input.Title) == "" {
		return Result{}, fmt.Errorf("canonical URL and title are required")
	}
	coverHash := ""
	var cover image.Image
	if len(input.Cover) > 0 {
		decoded, format, err := image.Decode(bytes.NewReader(input.Cover))
		if err != nil {
			return Result{}, fmt.Errorf("decode cover: %w", err)
		}
		if format != "png" && format != "jpeg" && format != "webp" {
			return Result{}, fmt.Errorf("cover format %q is not supported", format)
		}
		cover = decoded
		hash := sha256.Sum256(input.Cover)
		coverHash = hex.EncodeToString(hash[:])
	}
	canonical := canonicalInput{CanonicalURL: input.CanonicalURL, SiteTitle: input.SiteTitle, Title: input.Title, Context: input.Context, Author: input.Author, Date: input.Date, Status: input.Status, CoverHash: coverHash, Generator: GeneratorVersion}
	canonicalJSON, err := json.Marshal(canonical)
	if err != nil {
		return Result{}, err
	}
	canvas := image.NewRGBA(image.Rect(0, 0, Width, Height))
	draw.Draw(canvas, canvas.Bounds(), &image.Uniform{C: background}, image.Point{}, draw.Src)
	if cover != nil {
		drawCover(canvas, cover)
	} else {
		fillRect(canvas, image.Rect(72, 72, 84, 558), accent)
	}
	maxWidth := 1056
	if cover != nil {
		maxWidth = 624
	}
	if err := drawText(canvas, 72, 80, input.SiteTitle, 28, maxWidth); err != nil {
		return Result{}, err
	}
	if err := drawText(canvas, 72, 160, input.Title, 64, maxWidth); err != nil {
		return Result{}, err
	}
	if input.Context != "" {
		if err := drawTextColor(canvas, 72, 420, input.Context, 24, maxWidth, secondary); err != nil {
			return Result{}, err
		}
	}
	metadata := make([]string, 0, 3)
	if input.Author != "" {
		metadata = append(metadata, input.Author)
	}
	if input.Date != "" {
		metadata = append(metadata, input.Date)
	}
	if input.Status != "" {
		metadata = append(metadata, input.Status)
	}
	for index, value := range metadata {
		if err := drawTextColor(canvas, 72, 478+index*24, value, 24, maxWidth, secondary); err != nil {
			return Result{}, err
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, canvas); err != nil {
		return Result{}, err
	}
	pngBytes := encoded.Bytes()
	inputHash := sha256.Sum256(canonicalJSON)
	pngHash := sha256.Sum256(pngBytes)
	urlHash := sha256.Sum256([]byte(input.CanonicalURL))
	return Result{CanonicalJSON: append([]byte(nil), canonicalJSON...), PNG: append([]byte(nil), pngBytes...), Path: fmt.Sprintf("assets/social/%x/%x-%x.png", urlHash, inputHash, pngHash), InputHash: hex.EncodeToString(inputHash[:]), PNGHash: hex.EncodeToString(pngHash[:])}, nil
}

func drawCover(dst *image.RGBA, src image.Image) {
	bounds := src.Bounds()
	sw, sh := bounds.Dx(), bounds.Dy()
	if sw == 0 || sh == 0 {
		return
	}
	scaleX, scaleY := float64(432)/float64(sw), float64(630)/float64(sh)
	scale := scaleX
	if scaleY > scale {
		scale = scaleY
	}
	width, height := int(float64(sw)*scale+0.5), int(float64(sh)*scale+0.5)
	left, top := 768+(432-width)/2, (630-height)/2
	for y := range height {
		for x := range width {
			dx, dy := left+x, top+y
			if dx < 768 || dx >= 1200 || dy < 0 || dy >= 630 {
				continue
			}
			sx := bounds.Min.X + int(float64(x)/scale)
			sy := bounds.Min.Y + int(float64(y)/scale)
			pixel := color.NRGBAModel.Convert(src.At(sx, sy)).(color.NRGBA)
			composite := func(channel, base uint8) uint8 {
				return uint8((uint32(channel)*uint32(pixel.A) + uint32(base)*uint32(255-pixel.A)) / 255)
			}
			r, g, b := composite(pixel.R, background.R), composite(pixel.G, background.G), composite(pixel.B, background.B)
			const overlayAlpha = uint32(46)
			r = uint8((uint32(r)*(255-overlayAlpha) + uint32(background.R)*overlayAlpha) / 255)
			g = uint8((uint32(g)*(255-overlayAlpha) + uint32(background.G)*overlayAlpha) / 255)
			b = uint8((uint32(b)*(255-overlayAlpha) + uint32(background.B)*overlayAlpha) / 255)
			dst.SetRGBA(dx, dy, color.RGBA{R: r, G: g, B: b, A: 0xff})
		}
	}
}

func fillRect(dst *image.RGBA, rect image.Rectangle, value color.Color) {
	draw.Draw(dst, rect, &image.Uniform{C: value}, image.Point{}, draw.Src)
}

func drawText(dst *image.RGBA, x, y int, text string, size, maxWidth int) error {
	return drawTextColorLines(dst, x, y, text, size, maxWidth, 3, primary)
}

func drawTextColor(dst *image.RGBA, x, y int, text string, size, maxWidth int, foreground color.Color) error {
	return drawTextColorLines(dst, x, y, text, size, maxWidth, 1, foreground)
}

func drawTextColorLines(dst *image.RGBA, x, y int, text string, size, maxWidth, maxLines int, foreground color.Color) error {
	if text == "" {
		return nil
	}
	fontValue, err := parsedCardFont()
	if err != nil {
		return fmt.Errorf("parse social-card font: %w", err)
	}
	face, err := opentype.NewFace(fontValue, &opentype.FaceOptions{Size: float64(size), DPI: 72, Hinting: font.HintingNone})
	if err != nil {
		return fmt.Errorf("create social-card font face: %w", err)
	}
	lines := wrapMeasured(text, maxWidth, maxLines, face)
	for lineIndex, line := range lines {
		if err := drawLineColor(dst, x, y+lineIndex*76, line, face, foreground); err != nil {
			return err
		}
	}
	return nil
}

func wrapMeasured(text string, maxWidth, maxLines int, face font.Face) []string {
	clusters := make([]string, 0)
	iterator := uniseg.NewGraphemes(text)
	for iterator.Next() {
		clusters = append(clusters, iterator.Str())
	}
	lines := make([]string, 0, maxLines)
	current := ""
	currentWidth := 0
	for len(clusters) > 0 {
		cluster := clusters[0]
		clusters = clusters[1:]
		width := font.MeasureString(face, cluster).Ceil()
		if current != "" && currentWidth+width > maxWidth {
			if len(lines) == maxLines-1 {
				return append(lines, truncateMeasured(current, maxWidth, face))
			}
			lines = append(lines, current)
			current, currentWidth = "", 0
		}
		current += cluster
		currentWidth += width
	}
	if current != "" && len(lines) < maxLines {
		lines = append(lines, current)
	} else if current != "" && len(lines) == maxLines {
		lines[maxLines-1] = truncateMeasured(lines[maxLines-1], maxWidth, face)
	}
	return lines
}

func truncateMeasured(value string, maxWidth int, face font.Face) string {
	ellipsis := "…"
	if font.MeasureString(face, value+ellipsis).Ceil() <= maxWidth {
		return value + ellipsis
	}
	iterator := uniseg.NewGraphemes(value)
	result := ""
	for iterator.Next() {
		candidate := result + iterator.Str()
		if font.MeasureString(face, candidate+ellipsis).Ceil() > maxWidth {
			break
		}
		result = candidate
	}
	return result + ellipsis
}

func drawLineColor(dst *image.RGBA, x, y int, text string, face font.Face, foreground color.Color) error {
	if face == nil {
		return fmt.Errorf("social-card font face is required")
	}
	drawer := &font.Drawer{Dst: dst, Src: image.NewUniform(foreground), Face: face, Dot: fixed.P(x, y+face.Metrics().Ascent.Ceil())}
	drawer.DrawString(text)
	return nil
}
