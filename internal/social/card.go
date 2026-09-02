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
	drawText(canvas, 72, 80, input.SiteTitle, 4, maxWidth)
	drawText(canvas, 72, 160, input.Title, 8, maxWidth)
	if input.Context != "" {
		drawTextColor(canvas, 72, 420, input.Context, 3, maxWidth, secondary)
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
		drawTextColor(canvas, 72, 540+index*24, value, 3, maxWidth, secondary)
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

func drawText(dst *image.RGBA, x, y int, text string, scale, maxWidth int) {
	drawTextColor(dst, x, y, text, scale, maxWidth, primary)
}
func drawTextColor(dst *image.RGBA, x, y int, text string, scale, maxWidth int, foreground color.Color) {
	if text == "" {
		return
	}
	lines := wrap(text, maxWidth/(6*scale), 3)
	for lineIndex, line := range lines {
		drawLineColor(dst, x, y+lineIndex*76, line, scale, foreground)
	}
}
func wrap(text string, max, maxLines int) []string {
	if max < 1 {
		return nil
	}
	clusters := make([]string, 0)
	iterator := uniseg.NewGraphemes(text)
	for iterator.Next() {
		clusters = append(clusters, iterator.Str())
	}
	lines := make([]string, 0, maxLines)
	for len(clusters) > 0 && len(lines) < maxLines {
		take := min(max, len(clusters))
		line := strings.Join(clusters[:take], "")
		clusters = clusters[take:]
		if len(clusters) > 0 && len(lines) == maxLines-1 {
			line = truncate(line, max-1) + "…"
			clusters = nil
		}
		lines = append(lines, line)
	}
	return lines
}
func truncate(value string, max int) string {
	if max <= 0 {
		return ""
	}
	iterator := uniseg.NewGraphemes(value)
	var b strings.Builder
	count := 0
	for iterator.Next() && count < max {
		b.WriteString(iterator.Str())
		count++
	}
	return b.String()
}
func drawLineColor(dst *image.RGBA, x, y int, text string, scale int, foreground color.Color) {
	fontValue, err := parsedCardFont()
	if err != nil {
		fallbackLine(dst, x, y, text, scale, foreground)
		return
	}
	face, err := opentype.NewFace(fontValue, &opentype.FaceOptions{Size: float64(scale * 8), DPI: 72, Hinting: font.HintingNone})
	if err != nil {
		fallbackLine(dst, x, y, text, scale, foreground)
		return
	}
	drawer := &font.Drawer{Dst: dst, Src: image.NewUniform(foreground), Face: face, Dot: fixed.P(x, y+face.Metrics().Ascent.Ceil())}
	drawer.DrawString(text)
}

func fallbackLine(dst *image.RGBA, x, y int, text string, scale int, foreground color.Color) {
	cursor := x
	iterator := uniseg.NewGraphemes(text)
	for iterator.Next() {
		cluster := iterator.Str()
		if len([]rune(cluster)) == 1 {
			drawGlyphColor(dst, cursor, y, []rune(cluster)[0], scale, foreground)
		} else {
			fillRect(dst, image.Rect(cursor, y, cursor+5*scale, y+7*scale), foreground)
		}
		cursor += 6 * scale
	}
}

var glyphs = map[rune][7]string{
	'A': {"01110", "10001", "10001", "11111", "10001", "10001", "10001"}, 'B': {"11110", "10001", "10001", "11110", "10001", "10001", "11110"}, 'C': {"01111", "10000", "10000", "10000", "10000", "10000", "01111"}, 'D': {"11110", "10001", "10001", "10001", "10001", "10001", "11110"}, 'E': {"11111", "10000", "10000", "11110", "10000", "10000", "11111"}, 'F': {"11111", "10000", "10000", "11110", "10000", "10000", "10000"}, 'G': {"01111", "10000", "10000", "10111", "10001", "10001", "01111"}, 'H': {"10001", "10001", "10001", "11111", "10001", "10001", "10001"}, 'I': {"11111", "00100", "00100", "00100", "00100", "00100", "11111"}, 'J': {"00111", "00010", "00010", "00010", "10010", "10010", "01100"}, 'K': {"10001", "10010", "10100", "11000", "10100", "10010", "10001"}, 'L': {"10000", "10000", "10000", "10000", "10000", "10000", "11111"}, 'M': {"10001", "11011", "10101", "10101", "10001", "10001", "10001"}, 'N': {"10001", "11001", "10101", "10011", "10001", "10001", "10001"}, 'O': {"01110", "10001", "10001", "10001", "10001", "10001", "01110"}, 'P': {"11110", "10001", "10001", "11110", "10000", "10000", "10000"}, 'Q': {"01110", "10001", "10001", "10001", "10101", "10010", "01101"}, 'R': {"11110", "10001", "10001", "11110", "10100", "10010", "10001"}, 'S': {"01111", "10000", "10000", "01110", "00001", "00001", "11110"}, 'T': {"11111", "00100", "00100", "00100", "00100", "00100", "00100"}, 'U': {"10001", "10001", "10001", "10001", "10001", "10001", "01110"}, 'V': {"10001", "10001", "10001", "10001", "10001", "01010", "00100"}, 'W': {"10001", "10001", "10001", "10101", "10101", "11011", "10001"}, 'X': {"10001", "10001", "01010", "00100", "01010", "10001", "10001"}, 'Y': {"10001", "10001", "01010", "00100", "00100", "00100", "00100"}, 'Z': {"11111", "00001", "00010", "00100", "01000", "10000", "11111"}, '0': {"01110", "10001", "10011", "10101", "11001", "10001", "01110"}, '1': {"00100", "01100", "00100", "00100", "00100", "00100", "01110"}, '2': {"01110", "10001", "00001", "00010", "00100", "01000", "11111"}, '3': {"11110", "00001", "00001", "01110", "00001", "00001", "11110"}, '4': {"00010", "00110", "01010", "10010", "11111", "00010", "00010"}, '5': {"11111", "10000", "10000", "11110", "00001", "00001", "11110"}, '6': {"01110", "10000", "10000", "11110", "10001", "10001", "01110"}, '7': {"11111", "00001", "00010", "00100", "01000", "01000", "01000"}, '8': {"01110", "10001", "10001", "01110", "10001", "10001", "01110"}, '9': {"01110", "10001", "10001", "01111", "00001", "00001", "01110"}, '-': {"00000", "00000", "00000", "11111", "00000", "00000", "00000"}, '.': {"00000", "00000", "00000", "00000", "00000", "00110", "00110"}, ':': {"00000", "00110", "00110", "00000", "00110", "00110", "00000"}, '/': {"00001", "00010", "00100", "01000", "10000", "00000", "00000"}, ' ': {"00000", "00000", "00000", "00000", "00000", "00000", "00000"}, '…': {"00000", "00000", "00000", "00000", "10101", "00000", "00000"},
}

func drawGlyphColor(dst *image.RGBA, x, y int, r rune, scale int, foreground color.Color) {
	glyph, ok := glyphs[r]
	if !ok {
		glyph, ok = glyphs[toUpperASCII(r)]
	}
	if !ok {
		fillRect(dst, image.Rect(x, y, x+5*scale, y+7*scale), foreground)
		return
	}
	for row, line := range glyph {
		for col, value := range line {
			if value == '1' {
				fillRect(dst, image.Rect(x+col*scale, y+row*scale, x+(col+1)*scale, y+(row+1)*scale), foreground)
			}
		}
	}
}
func toUpperASCII(r rune) rune {
	if r >= 'a' && r <= 'z' {
		return r - 'a' + 'A'
	}
	return r
}
