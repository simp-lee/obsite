package markdown

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"

	figureast "github.com/mangoumbrella/goldmark-figure/ast"
	"github.com/simp-lee/obsite/internal/diag"
	"github.com/simp-lee/obsite/internal/markdown/callout"
	internalhighlight "github.com/simp-lee/obsite/internal/markdown/highlight"
	"github.com/simp-lee/obsite/internal/markdown/math"
	"github.com/simp-lee/obsite/internal/model"
	gast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	gmwikilink "go.abhg.dev/goldmark/wikilink"
)

func TestNewParserParsesCoreCustomNodes(t *testing.T) {
	t.Parallel()

	md := NewParser(diag.NewCollector())
	if md != NewParser(nil) {
		t.Fatal("NewParser() did not return the shared instance")
	}

	source := []byte("==highlight==\n\n> [!note] Heads up\n> body\n\n$E=mc^2$\n")
	doc := md.Parser().Parse(text.NewReader(source))

	var (
		highlightCount int
		calloutCount   int
		mathCount      int
	)

	err := gast.Walk(doc, func(node gast.Node, entering bool) (gast.WalkStatus, error) {
		if !entering {
			return gast.WalkContinue, nil
		}

		switch node.(type) {
		case *internalhighlight.Mark:
			highlightCount++
		case *callout.Callout:
			calloutCount++
		}

		if math.IsMathNode(node) {
			mathCount++
		}

		return gast.WalkContinue, nil
	})
	if err != nil {
		t.Fatalf("Walk() error = %v", err)
	}

	if highlightCount != 1 {
		t.Fatalf("highlightCount = %d, want 1", highlightCount)
	}
	if calloutCount != 1 {
		t.Fatalf("calloutCount = %d, want 1", calloutCount)
	}
	if mathCount != 1 {
		t.Fatalf("mathCount = %d, want 1", mathCount)
	}
}

func TestNewParserParsesWikilinkNodes(t *testing.T) {
	t.Parallel()

	md := NewParser(diag.NewCollector())
	source := []byte("[[Page Title]]")
	doc := md.Parser().Parse(text.NewReader(source))

	var wikilinks []*gmwikilink.Node
	err := gast.Walk(doc, func(node gast.Node, entering bool) (gast.WalkStatus, error) {
		if !entering {
			return gast.WalkContinue, nil
		}

		if wikilinkNode, ok := node.(*gmwikilink.Node); ok {
			wikilinks = append(wikilinks, wikilinkNode)
		}

		return gast.WalkContinue, nil
	})
	if err != nil {
		t.Fatalf("Walk() error = %v", err)
	}

	if len(wikilinks) != 1 {
		t.Fatalf("wikilink count = %d, want 1", len(wikilinks))
	}
	if got := string(wikilinks[0].Target); got != "Page Title" {
		t.Fatalf("wikilink target = %q, want %q", got, "Page Title")
	}
}

func TestNewParserDoesNotRegisterFigureNodes(t *testing.T) {
	t.Parallel()

	md := NewParser(diag.NewCollector())
	source := []byte("![Hero](../images/hero.png)\nCaption text.\n")
	doc := md.Parser().Parse(text.NewReader(source))

	err := gast.Walk(doc, func(node gast.Node, entering bool) (gast.WalkStatus, error) {
		if !entering {
			return gast.WalkContinue, nil
		}

		switch node.Kind() {
		case figureast.KindFigure, figureast.KindFigureImage, figureast.KindFigureCaption:
			t.Fatalf("NewParser() unexpectedly registered figure node kind %s", node.Kind())
		}

		return gast.WalkContinue, nil
	})
	if err != nil {
		t.Fatalf("Walk() error = %v", err)
	}
}

func TestNewParserAssignsStableHeadingIDsWithoutAlteringVisibleHeadingText(t *testing.T) {
	t.Parallel()

	md := NewParser(diag.NewCollector())
	source := []byte("# Intro Heading\n\n# Intro Heading\n")
	doc := md.Parser().Parse(text.NewReader(source))
	headings := collectHeadings(t, doc)
	if len(headings) != 2 {
		t.Fatalf("heading count = %d, want 2", len(headings))
	}

	wantIDs := []string{"intro-heading", "intro-heading-1"}
	for i, heading := range headings {
		id, ok := heading.AttributeString("id")
		if !ok {
			t.Fatalf("heading %d missing id attribute", i)
		}

		idBytes, ok := id.([]byte)
		if !ok {
			t.Fatalf("heading %d id type = %T, want []byte", i, id)
		}
		if got := string(idBytes); got != wantIDs[i] {
			t.Fatalf("heading %d id = %q, want %q", i, got, wantIDs[i])
		}

		if heading.FirstChild() == nil {
			t.Fatalf("heading %d has no children, want visible inline content preserved", i)
		}
		if got := VisibleHeadingText(heading, source); got != "Intro Heading" {
			t.Fatalf("heading %d visible text = %q, want %q", i, got, "Intro Heading")
		}
	}
}

func TestNewParserAssignsHeadingIDsFromVisibleText(t *testing.T) {
	t.Parallel()

	md := NewParser(diag.NewCollector())
	source := []byte("# Intro *Bold*\n\n## [[Target Page|Shown Label]]\n\n### 中文 标题\n")
	doc := md.Parser().Parse(text.NewReader(source))
	headings := collectHeadings(t, doc)
	if len(headings) != 3 {
		t.Fatalf("heading count = %d, want 3", len(headings))
	}

	wantIDs := []string{"intro-bold", "shown-label", "中文-标题"}
	for i, heading := range headings {
		id, ok := heading.AttributeString("id")
		if !ok {
			t.Fatalf("heading %d missing id attribute", i)
		}

		idBytes, ok := id.([]byte)
		if !ok {
			t.Fatalf("heading %d id type = %T, want []byte", i, id)
		}
		if got := string(idBytes); got != wantIDs[i] {
			t.Fatalf("heading %d id = %q, want %q", i, got, wantIDs[i])
		}
	}
}

func TestNewParserPass1HeadingIDsIgnoreInvisibleRawHTMLAndPreserveEntities(t *testing.T) {
	t.Parallel()

	md := NewParser(diag.NewCollector())
	source := []byte("# Hello <script>alert(1)</script><style>.x{}</style><template><span>Ghost</span></template><span hidden>Skip <span>Deeper</span></span><span>&amp;lt;</span> World\n")
	doc := md.Parser().Parse(text.NewReader(source))
	headings := collectHeadings(t, doc)
	if len(headings) != 1 {
		t.Fatalf("heading count = %d, want 1", len(headings))
	}

	id, ok := headings[0].AttributeString("id")
	if !ok {
		t.Fatal("heading missing id attribute")
	}

	idBytes, ok := id.([]byte)
	if !ok {
		t.Fatalf("heading id type = %T, want []byte", id)
	}
	if got := string(idBytes); got != "hello-lt-world" {
		t.Fatalf("heading id = %q, want %q", got, "hello-lt-world")
	}
	if got := VisibleHeadingText(headings[0], source); got != "Hello &lt; World" {
		t.Fatalf("heading visible text = %q, want %q", got, "Hello &lt; World")
	}
}

func TestNewParserPass1HeadingIDsPreserveBrowserVisibleTextAcrossVoidRawHTMLTags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		source      string
		wantID      string
		wantVisible string
	}{
		{name: "after-visible-span", source: "# Hello <span>Alpha</span>&amp;lt; World\n", wantID: "hello-alpha-lt-world", wantVisible: "Hello Alpha&lt; World"},
		{name: "after-hidden-span", source: "# Hello <span hidden>Ghost</span>&amp;lt; World\n", wantID: "hello-lt-world", wantVisible: "Hello &lt; World"},
		{name: "br", source: "# Hello <br>&amp;lt; World\n", wantID: "hello-lt-world", wantVisible: "Hello &lt; World"},
		{name: "hr", source: "# Hello <hr>&amp;lt; World\n", wantID: "hello-lt-world", wantVisible: "Hello &lt; World"},
		{name: "br-self-closing", source: "# Hello <br/>&amp;lt; World\n", wantID: "hello-lt-world", wantVisible: "Hello &lt; World"},
		{name: "hr-self-closing", source: "# Hello <hr/>&amp;lt; World\n", wantID: "hello-lt-world", wantVisible: "Hello &lt; World"},
		{name: "hidden-img", source: "# Hello <img hidden>&amp;lt; World\n", wantID: "hello-lt-world", wantVisible: "Hello &lt; World"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			md := NewParser(diag.NewCollector())
			source := []byte(tt.source)
			doc := md.Parser().Parse(text.NewReader(source))
			headings := collectHeadings(t, doc)
			if len(headings) != 1 {
				t.Fatalf("heading count = %d, want 1", len(headings))
			}

			id, ok := headings[0].AttributeString("id")
			if !ok {
				t.Fatal("heading missing id attribute")
			}

			idBytes, ok := id.([]byte)
			if !ok {
				t.Fatalf("heading id type = %T, want []byte", id)
			}
			if got := string(idBytes); got != tt.wantID {
				t.Fatalf("heading id = %q, want %q", got, tt.wantID)
			}
			if got := VisibleHeadingText(headings[0], source); got != tt.wantVisible {
				t.Fatalf("heading visible text = %q, want %q", got, tt.wantVisible)
			}
		})
	}
}

func TestNewMarkdownMermaidRendererMarksNote(t *testing.T) {
	t.Parallel()

	note := &model.Note{Slug: "guides/diagram", RelPath: "notes/diagram.md"}
	md, renderResult := NewMarkdown(nil, note, nil, diag.NewCollector())

	var buf bytes.Buffer
	source := []byte("```mermaid\ngraph TD;\nA-->B\n```\n")
	if err := md.Convert(source, &buf); err != nil {
		t.Fatalf("Convert() error = %v", err)
	}

	html := buf.String()
	if !strings.Contains(html, `<pre class="mermaid">`) {
		t.Fatalf("HTML = %q, want mermaid <pre>", html)
	}
	if strings.Contains(html, "language-mermaid") {
		t.Fatalf("HTML = %q, want mermaid fence to bypass syntax highlighting wrapper", html)
	}
	if note.HasMermaid {
		t.Fatal("note.HasMermaid = true, want source note to remain unchanged")
	}
	if renderResult == nil || !renderResult.HasMermaid() {
		t.Fatal("renderResult.HasMermaid() = false, want true")
	}
}

func TestNewMarkdownMermaidEscapesSourceAndKeepsFallbackForOtherFences(t *testing.T) {
	t.Parallel()

	note := &model.Note{Slug: "guides/diagram", RelPath: "notes/diagram.md"}
	md, renderResult := NewMarkdown(nil, note, nil, diag.NewCollector())

	var buf bytes.Buffer
	source := []byte("```mermaid\ngraph TD;\nA[\"<b>Unsafe</b>\"] --> B\n```\n\n```go\nfmt.Println(\"hi\")\n```\n")
	if err := md.Convert(source, &buf); err != nil {
		t.Fatalf("Convert() error = %v", err)
	}

	html := buf.String()
	if !strings.Contains(html, `<pre class="mermaid">`) {
		t.Fatalf("HTML = %q, want mermaid <pre>", html)
	}
	if strings.Contains(html, `<b>Unsafe</b>`) {
		t.Fatalf("HTML = %q, want Mermaid source escaped", html)
	}
	if !strings.Contains(html, `&lt;b&gt;Unsafe&lt;/b&gt;`) {
		t.Fatalf("HTML = %q, want escaped Mermaid source", html)
	}
	if strings.Count(html, `<pre`) < 2 || !strings.Contains(html, `<code>`) || !strings.Contains(html, `Println`) || (!strings.Contains(html, `class="chroma"`) && !strings.Contains(html, `class="language-go"`) && !strings.Contains(html, `<pre style=`)) {
		t.Fatalf("HTML = %q, want non-Mermaid fence to use highlighted code fallback output", html)
	}
	if note.HasMermaid {
		t.Fatal("note.HasMermaid = true, want source note to remain unchanged")
	}
	if renderResult == nil || !renderResult.HasMermaid() {
		t.Fatal("renderResult.HasMermaid() = false, want true")
	}
}

func TestNewMarkdownResolvesInlineHashtagsToTagPages(t *testing.T) {
	t.Parallel()

	idx := &model.VaultIndex{
		Tags: map[string]*model.Tag{
			"field":        {Name: "field", Slug: "tags/field"},
			"parent/child": {Name: "parent/child", Slug: "tags/parent/child"},
		},
	}
	note := &model.Note{Slug: "launch-pad", RelPath: "notes/launch-pad.md"}
	md, _ := NewMarkdown(idx, note, nil, diag.NewCollector())

	var buf bytes.Buffer
	source := []byte("Inline tags #field and #parent/child stay linked while #missing stays plain.\n")
	if err := md.Convert(source, &buf); err != nil {
		t.Fatalf("Convert() error = %v", err)
	}

	html := buf.String()
	if !strings.Contains(html, `<span class="hashtag"><a href="../tags/field/">#field</a></span>`) {
		t.Fatalf("HTML = %q, want inline field hashtag link", html)
	}
	if !strings.Contains(html, `<span class="hashtag"><a href="../tags/parent/child/">#parent/child</a></span>`) {
		t.Fatalf("HTML = %q, want inline nested hashtag link", html)
	}
	if !strings.Contains(html, `<span class="hashtag">#missing</span>`) {
		t.Fatalf("HTML = %q, want unresolved hashtag to remain plain text", html)
	}
}

func TestNewMarkdownRewritesImagesRegistersAssetsAndLazyLoadsAfterFirst(t *testing.T) {
	t.Parallel()

	sink := &recordingAssetSink{
		paths: map[string]string{
			"images/hero.png":  "assets/hero.123.png",
			"images/chart.png": "assets/chart.456.png",
		},
	}
	note := &model.Note{Slug: "posts/guide", RelPath: "notes/guide.md"}
	idx := &model.VaultIndex{
		Assets: map[string]*model.Asset{
			"images/hero.png":  {SrcPath: "images/hero.png"},
			"images/chart.png": {SrcPath: "images/chart.png"},
		},
	}
	md, _ := NewMarkdown(idx, note, sink, diag.NewCollector())

	var buf bytes.Buffer
	source := []byte("![Hero](../images/hero.png)\n\n![Chart](../images/chart.png \"Chart Title\")\n")
	if err := md.Convert(source, &buf); err != nil {
		t.Fatalf("Convert() error = %v", err)
	}

	html := buf.String()
	if !strings.Contains(html, `<img src="../../assets/hero.123.png" alt="Hero">`) {
		t.Fatalf("HTML = %q, want rewritten first image without lazy loading", html)
	}
	if !strings.Contains(html, `<img src="../../assets/chart.456.png" alt="Chart" title="Chart Title" loading="lazy">`) {
		t.Fatalf("HTML = %q, want rewritten second image with lazy loading", html)
	}

	wantRegistered := []string{"images/hero.png", "images/chart.png"}
	if !reflect.DeepEqual(sink.registered, wantRegistered) {
		t.Fatalf("registered = %#v, want %#v", sink.registered, wantRegistered)
	}
}

func TestNewMarkdownEscapesCodeSpanQuotesInImageAltAttributes(t *testing.T) {
	t.Parallel()

	note := &model.Note{Slug: "posts/guide", RelPath: "notes/guide.md"}
	md, _ := NewMarkdown(nil, note, nil, diag.NewCollector())

	var buf bytes.Buffer
	source := []byte("![code `\" onerror=alert(1) x=\"`](hero.png)\n")
	if err := md.Convert(source, &buf); err != nil {
		t.Fatalf("Convert() error = %v", err)
	}

	html := buf.String()
	if !strings.Contains(html, `<img src="hero.png" alt="code &#34; onerror=alert(1) x=&#34;">`) {
		t.Fatalf("HTML = %q, want code span quotes escaped inside alt attribute", html)
	}
	if strings.Contains(html, `alt="code " onerror=`) {
		t.Fatalf("HTML = %q, want quoted payload to remain inside escaped alt text", html)
	}
}

func TestNewMarkdownRendersSupportedVideoImageDestinationsAsResponsiveEmbeds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		source       string
		wantEmbedURL string
	}{
		{
			name:         "youtube watch url",
			source:       "![Launch video](https://www.youtube.com/watch?v=dQw4w9WgXcQ&feature=shared)\n",
			wantEmbedURL: "https://www.youtube.com/embed/dQw4w9WgXcQ",
		},
		{
			name:         "youtube short url",
			source:       "![Launch video](https://youtu.be/dQw4w9WgXcQ?t=43)\n",
			wantEmbedURL: "https://www.youtube.com/embed/dQw4w9WgXcQ",
		},
		{
			name:         "vimeo url",
			source:       "![Talk recording](https://vimeo.com/76979871)\n",
			wantEmbedURL: "https://player.vimeo.com/video/76979871",
		},
		{
			name:         "vimeo player url",
			source:       "![Talk recording](https://player.vimeo.com/video/76979871?autoplay=1)\n",
			wantEmbedURL: "https://player.vimeo.com/video/76979871",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			note := &model.Note{Slug: "posts/guide", RelPath: "notes/guide.md"}
			md, _ := NewMarkdown(nil, note, nil, diag.NewCollector())

			var buf bytes.Buffer
			if err := md.Convert([]byte(tt.source), &buf); err != nil {
				t.Fatalf("Convert() error = %v", err)
			}

			html := buf.String()
			if !strings.Contains(html, `class="video-embed"`) {
				t.Fatalf("HTML = %q, want responsive video wrapper", html)
			}
			if !strings.Contains(html, `<iframe src="`+tt.wantEmbedURL+`"`) {
				t.Fatalf("HTML = %q, want iframe src %q", html, tt.wantEmbedURL)
			}
			if !strings.Contains(html, `loading="lazy"`) {
				t.Fatalf("HTML = %q, want lazy-loaded iframe", html)
			}
			if !strings.Contains(html, `allowfullscreen`) {
				t.Fatalf("HTML = %q, want allowfullscreen iframe", html)
			}
			if strings.Contains(html, `<img `) {
				t.Fatalf("HTML = %q, want video URL rendered without <img>", html)
			}
		})
	}
}

func TestNewMarkdownRendersVideoEmbedsOnlyForStandaloneImageBlocks(t *testing.T) {
	t.Parallel()

	t.Run("standalone image paragraph upgrades to embed without paragraph wrapper", func(t *testing.T) {
		t.Parallel()

		note := &model.Note{Slug: "posts/guide", RelPath: "notes/guide.md"}
		md, _ := NewMarkdown(nil, note, nil, diag.NewCollector())

		var buf bytes.Buffer
		if err := md.Convert([]byte("![Launch video](https://youtu.be/dQw4w9WgXcQ)\n"), &buf); err != nil {
			t.Fatalf("Convert() error = %v", err)
		}

		html := buf.String()
		if !strings.Contains(html, `class="video-embed"`) {
			t.Fatalf("HTML = %q, want standalone video image upgraded to embed", html)
		}
		if !strings.Contains(html, `<iframe src="https://www.youtube.com/embed/dQw4w9WgXcQ"`) {
			t.Fatalf("HTML = %q, want standalone video image iframe", html)
		}
		if strings.Contains(html, `<p><div class="video-embed"`) || strings.Contains(html, `</div></p>`) {
			t.Fatalf("HTML = %q, want standalone video embed emitted without invalid paragraph wrapper", html)
		}
	})

	t.Run("inline image fallback stays a normal image inside paragraph text", func(t *testing.T) {
		t.Parallel()

		note := &model.Note{Slug: "posts/guide", RelPath: "notes/guide.md"}
		md, _ := NewMarkdown(nil, note, nil, diag.NewCollector())

		var buf bytes.Buffer
		if err := md.Convert([]byte("Watch this ![Launch video](https://youtu.be/dQw4w9WgXcQ) now.\n"), &buf); err != nil {
			t.Fatalf("Convert() error = %v", err)
		}

		html := buf.String()
		if strings.Contains(html, `class="video-embed"`) {
			t.Fatalf("HTML = %q, want inline video image to avoid block video wrapper", html)
		}
		if strings.Contains(html, `<iframe `) {
			t.Fatalf("HTML = %q, want inline video image to avoid iframe output", html)
		}
		if !strings.Contains(html, `<p>Watch this <img src="https://youtu.be/dQw4w9WgXcQ" alt="Launch video"> now.</p>`) {
			t.Fatalf("HTML = %q, want inline video image to fall back to normal image output inside paragraph", html)
		}
	})
}

func TestNewMarkdownFallsBackToImagesForMalformedYouTubeVideoDestinations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		source       string
		wantImageSrc string
	}{
		{
			name:         "watch url with malformed video id",
			source:       "![Launch video](https://www.youtube.com/watch?v=dQw4w9WgXcQ/extra)\n",
			wantImageSrc: "https://www.youtube.com/watch?v=dQw4w9WgXcQ/extra",
		},
		{
			name:         "watch url with encoded query content in video id",
			source:       "![Launch video](https://www.youtube.com/watch?v=dQw4w9WgXcQ%26list%3DPL123)\n",
			wantImageSrc: "https://www.youtube.com/watch?v=dQw4w9WgXcQ%26list%3DPL123",
		},
		{
			name:         "short url with non canonical path",
			source:       "![Launch video](https://youtu.be/watch/dQw4w9WgXcQ)\n",
			wantImageSrc: "https://youtu.be/watch/dQw4w9WgXcQ",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			note := &model.Note{Slug: "posts/guide", RelPath: "notes/guide.md"}
			md, _ := NewMarkdown(nil, note, nil, diag.NewCollector())

			var buf bytes.Buffer
			if err := md.Convert([]byte(tt.source), &buf); err != nil {
				t.Fatalf("Convert() error = %v", err)
			}

			html := buf.String()
			if strings.Contains(html, `class="video-embed"`) {
				t.Fatalf("HTML = %q, want malformed YouTube URL to avoid video embed wrapper", html)
			}
			if strings.Contains(html, `<iframe `) {
				t.Fatalf("HTML = %q, want malformed YouTube URL to avoid iframe output", html)
			}
			if !strings.Contains(html, `<img src="`+tt.wantImageSrc+`" alt="Launch video">`) {
				t.Fatalf("HTML = %q, want malformed YouTube URL to fall back to normal image %q", html, tt.wantImageSrc)
			}
		})
	}
}

func TestNewMarkdownFallsBackToImagesForUnsupportedVimeoVideoDestinations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		source       string
		wantImageSrc string
	}{
		{
			name:         "direct url with extra path segment",
			source:       "![Talk recording](https://vimeo.com/76979871/preview)\n",
			wantImageSrc: "https://vimeo.com/76979871/preview",
		},
		{
			name:         "player url with non canonical path",
			source:       "![Talk recording](https://player.vimeo.com/videos/76979871)\n",
			wantImageSrc: "https://player.vimeo.com/videos/76979871",
		},
		{
			name:         "non permalink path with trailing numeric segment",
			source:       "![Talk recording](https://vimeo.com/channels/staffpicks/not-a-video/12345/preview)\n",
			wantImageSrc: "https://vimeo.com/channels/staffpicks/not-a-video/12345/preview",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			note := &model.Note{Slug: "posts/guide", RelPath: "notes/guide.md"}
			md, _ := NewMarkdown(nil, note, nil, diag.NewCollector())

			var buf bytes.Buffer
			if err := md.Convert([]byte(tt.source), &buf); err != nil {
				t.Fatalf("Convert() error = %v", err)
			}

			html := buf.String()
			if strings.Contains(html, `class="video-embed"`) {
				t.Fatalf("HTML = %q, want unsupported Vimeo URL to avoid video embed wrapper", html)
			}
			if strings.Contains(html, `<iframe `) {
				t.Fatalf("HTML = %q, want unsupported Vimeo URL to avoid iframe output", html)
			}
			if !strings.Contains(html, `<img src="`+tt.wantImageSrc+`" alt="Talk recording">`) {
				t.Fatalf("HTML = %q, want unsupported Vimeo URL to fall back to normal image %q", html, tt.wantImageSrc)
			}
		})
	}
}

func TestNewMarkdownVideoEmbedsDoNotAlterNonVideoImageRewritePath(t *testing.T) {
	t.Parallel()

	sink := &recordingAssetSink{
		paths: map[string]string{
			"images/hero.png": "assets/hero.123.png",
		},
	}
	note := &model.Note{Slug: "posts/guide", RelPath: "notes/guide.md"}
	idx := &model.VaultIndex{
		Assets: map[string]*model.Asset{
			"images/hero.png": {SrcPath: "images/hero.png"},
		},
	}
	md, _ := NewMarkdown(idx, note, sink, diag.NewCollector())

	var buf bytes.Buffer
	source := []byte("![Launch video](https://youtu.be/dQw4w9WgXcQ)\n\n![Hero](../images/hero.png)\n")
	if err := md.Convert(source, &buf); err != nil {
		t.Fatalf("Convert() error = %v", err)
	}

	html := buf.String()
	if !strings.Contains(html, `<iframe src="https://www.youtube.com/embed/dQw4w9WgXcQ"`) {
		t.Fatalf("HTML = %q, want YouTube iframe embed", html)
	}
	if !strings.Contains(html, `<img src="../../assets/hero.123.png" alt="Hero">`) {
		t.Fatalf("HTML = %q, want normal asset rewrite for non-video image", html)
	}
	if strings.Contains(html, `<img src="../../assets/hero.123.png" alt="Hero" loading="lazy">`) {
		t.Fatalf("HTML = %q, want first non-video image to remain eager-loaded", html)
	}
	if !reflect.DeepEqual(sink.registered, []string{"images/hero.png"}) {
		t.Fatalf("registered = %#v, want %#v", sink.registered, []string{"images/hero.png"})
	}
}

func TestNewMarkdownWrapsStandaloneImageParagraphsInFigureWithoutFigcaption(t *testing.T) {
	t.Parallel()

	sink := &recordingAssetSink{
		paths: map[string]string{
			"images/hero.png": "assets/hero.123.png",
		},
	}
	note := &model.Note{Slug: "posts/guide", RelPath: "notes/guide.md"}
	idx := &model.VaultIndex{
		Assets: map[string]*model.Asset{
			"images/hero.png": {SrcPath: "images/hero.png"},
		},
	}
	md, _ := NewMarkdown(idx, note, sink, diag.NewCollector())

	var buf bytes.Buffer
	if err := md.Convert([]byte("![Hero](../images/hero.png)\n"), &buf); err != nil {
		t.Fatalf("Convert() error = %v", err)
	}

	html := buf.String()
	if !strings.Contains(html, "<figure>") {
		t.Fatalf("HTML = %q, want standalone image wrapped in <figure>", html)
	}
	if !strings.Contains(html, `<img src="../../assets/hero.123.png" alt="Hero">`) {
		t.Fatalf("HTML = %q, want rewritten image inside figure", html)
	}
	if strings.Contains(html, "<figcaption>") {
		t.Fatalf("HTML = %q, want no figcaption when only alt text is present", html)
	}
	if !reflect.DeepEqual(sink.registered, []string{"images/hero.png"}) {
		t.Fatalf("registered = %#v, want %#v", sink.registered, []string{"images/hero.png"})
	}
	if note.HasMath {
		t.Fatal("note.HasMath = true, want source note to remain unchanged")
	}
	if note.HasMermaid {
		t.Fatal("note.HasMermaid = true, want source note to remain unchanged")
	}
}

func TestNewMarkdownRendersFigureFigcaptionWhenCaptionTextFollowsImage(t *testing.T) {
	t.Parallel()

	sink := &recordingAssetSink{
		paths: map[string]string{
			"images/hero.png": "assets/hero.123.png",
		},
	}
	note := &model.Note{Slug: "posts/guide", RelPath: "notes/guide.md"}
	idx := &model.VaultIndex{
		Assets: map[string]*model.Asset{
			"images/hero.png": {SrcPath: "images/hero.png"},
		},
	}
	md, _ := NewMarkdown(idx, note, sink, diag.NewCollector())

	var buf bytes.Buffer
	source := []byte("![Hero](../images/hero.png)\nCaption with **bold** text.\n")
	if err := md.Convert(source, &buf); err != nil {
		t.Fatalf("Convert() error = %v", err)
	}

	html := buf.String()
	if !strings.Contains(html, "<figure>") {
		t.Fatalf("HTML = %q, want figure wrapper", html)
	}
	if !strings.Contains(html, `<img src="../../assets/hero.123.png" alt="Hero">`) {
		t.Fatalf("HTML = %q, want rewritten image inside figure", html)
	}
	if !strings.Contains(html, `<figcaption><p>Caption with <strong>bold</strong> text.</p></figcaption>`) {
		t.Fatalf("HTML = %q, want explicit caption rendered as figcaption", html)
	}
	if !reflect.DeepEqual(sink.registered, []string{"images/hero.png"}) {
		t.Fatalf("registered = %#v, want %#v", sink.registered, []string{"images/hero.png"})
	}
}

func TestNewMarkdownWrapsStandaloneImageEmbedsInFigureWithoutFigcaption(t *testing.T) {
	t.Parallel()

	sink := &recordingAssetSink{
		paths: map[string]string{
			"images/diagram.png": "assets/diagram.123.png",
		},
	}
	note := &model.Note{
		Slug:    "notes/current",
		RelPath: "notes/current.md",
		Embeds: []model.EmbedRef{{
			Target:  "../images/diagram.png",
			IsImage: true,
			Line:    1,
		}},
	}
	idx := &model.VaultIndex{
		Assets: map[string]*model.Asset{
			"images/diagram.png": {SrcPath: "images/diagram.png"},
		},
	}
	md, _ := NewMarkdown(idx, note, sink, diag.NewCollector())

	var buf bytes.Buffer
	if err := md.Convert([]byte("![[../images/diagram.png|Shown Label]]\n"), &buf); err != nil {
		t.Fatalf("Convert() error = %v", err)
	}

	html := buf.String()
	if !strings.Contains(html, "<figure>") {
		t.Fatalf("HTML = %q, want standalone image embed wrapped in <figure>", html)
	}
	if !strings.Contains(html, `<img src="../../assets/diagram.123.png" alt="Shown Label">`) {
		t.Fatalf("HTML = %q, want rewritten image embed inside figure with alt label", html)
	}
	if strings.Contains(html, "<figcaption>") {
		t.Fatalf("HTML = %q, want no figcaption when only image alt text is present", html)
	}
	if strings.Contains(html, "<p><figure>") || strings.Contains(html, "</figure></p>") {
		t.Fatalf("HTML = %q, want figure emitted without invalid paragraph wrapper", html)
	}
	if !reflect.DeepEqual(sink.registered, []string{"images/diagram.png"}) {
		t.Fatalf("registered = %#v, want %#v", sink.registered, []string{"images/diagram.png"})
	}
	if note.HasMath {
		t.Fatal("note.HasMath = true, want source note to remain unchanged")
	}
	if note.HasMermaid {
		t.Fatal("note.HasMermaid = true, want source note to remain unchanged")
	}
}

func TestNewMarkdownRendersFigureFigcaptionForImageEmbedsWhenCaptionTextFollows(t *testing.T) {
	t.Parallel()

	sink := &recordingAssetSink{
		paths: map[string]string{
			"images/diagram.png": "assets/diagram.123.png",
		},
	}
	note := &model.Note{
		Slug:    "notes/current",
		RelPath: "notes/current.md",
		Embeds: []model.EmbedRef{{
			Target:  "../images/diagram.png",
			IsImage: true,
			Line:    1,
		}},
	}
	idx := &model.VaultIndex{
		Assets: map[string]*model.Asset{
			"images/diagram.png": {SrcPath: "images/diagram.png"},
		},
	}
	md, _ := NewMarkdown(idx, note, sink, diag.NewCollector())

	var buf bytes.Buffer
	source := []byte("![[../images/diagram.png]]\nCaption with **bold** text.\n")
	if err := md.Convert(source, &buf); err != nil {
		t.Fatalf("Convert() error = %v", err)
	}

	html := buf.String()
	if !strings.Contains(html, "<figure>") {
		t.Fatalf("HTML = %q, want standalone image embed wrapped in <figure>", html)
	}
	if !strings.Contains(html, `<img src="../../assets/diagram.123.png" alt="diagram">`) {
		t.Fatalf("HTML = %q, want rewritten image embed inside figure", html)
	}
	if !strings.Contains(html, `<figcaption><p>Caption with <strong>bold</strong> text.</p></figcaption>`) {
		t.Fatalf("HTML = %q, want explicit caption rendered as figcaption for image embed", html)
	}
	if strings.Contains(html, "<p><figure>") || strings.Contains(html, "</figure></p>") {
		t.Fatalf("HTML = %q, want figure emitted without invalid paragraph wrapper", html)
	}
	if !reflect.DeepEqual(sink.registered, []string{"images/diagram.png"}) {
		t.Fatalf("registered = %#v, want %#v", sink.registered, []string{"images/diagram.png"})
	}
}

func TestNewMarkdownFallsBackToImageTargetWhenEmbeddedAssetIsMissing(t *testing.T) {
	t.Parallel()

	note := &model.Note{
		Slug:    "posts/missing-image",
		RelPath: "notes/missing-image.md",
		Embeds:  []model.EmbedRef{{Target: "missing.png", IsImage: true, Width: 600, Line: 1}},
	}
	collector := diag.NewCollector()
	md, _ := NewMarkdown(nil, note, nil, collector)

	var buf bytes.Buffer
	if err := md.Convert([]byte("![[missing.png|600]]\n"), &buf); err != nil {
		t.Fatalf("Convert() error = %v", err)
	}

	html := buf.String()
	if !strings.Contains(html, `<p>missing.png</p>`) {
		t.Fatalf("HTML = %q, want unresolved image embed to fall back to the target text", html)
	}
	if strings.Contains(html, `<p>600</p>`) {
		t.Fatalf("HTML = %q, want width label not to leak into fallback output", html)
	}

	gotDiagnostics := collector.Diagnostics()
	if len(gotDiagnostics) != 1 {
		t.Fatalf("len(collector.Diagnostics()) = %d, want 1", len(gotDiagnostics))
	}
	if gotDiagnostics[0].Kind != diag.KindUnresolvedAsset {
		t.Fatalf("collector.Diagnostics()[0] = %#v, want unresolved_asset warning", gotDiagnostics[0])
	}
}

func TestNewMarkdownURLEscapesImageEmbedSources(t *testing.T) {
	t.Parallel()

	sink := &recordingAssetSink{
		paths: map[string]string{
			"images/My Chart.png": "assets/My Chart.123.png",
		},
	}
	note := &model.Note{
		Slug:    "posts/guide",
		RelPath: "notes/guide.md",
		Embeds:  []model.EmbedRef{{Target: "../images/My Chart.png", IsImage: true, Line: 1}},
	}
	idx := &model.VaultIndex{
		Assets: map[string]*model.Asset{
			"images/My Chart.png": {SrcPath: "images/My Chart.png"},
		},
	}
	md, _ := NewMarkdown(idx, note, sink, diag.NewCollector())

	var buf bytes.Buffer
	if err := md.Convert([]byte("![[../images/My Chart.png]]\n"), &buf); err != nil {
		t.Fatalf("Convert() error = %v", err)
	}

	html := buf.String()
	if !strings.Contains(html, `<img src="../../assets/My%20Chart.123.png" alt="My Chart">`) {
		t.Fatalf("HTML = %q, want image embed src to be URL-escaped", html)
	}
	if !reflect.DeepEqual(sink.registered, []string{"images/My Chart.png"}) {
		t.Fatalf("registered = %#v, want %#v", sink.registered, []string{"images/My Chart.png"})
	}
}

func TestNewMarkdownRejectsImagesOutsideVaultRoot(t *testing.T) {
	t.Parallel()

	sink := &recordingAssetSink{}
	note := &model.Note{Slug: "posts/guide", RelPath: "notes/guide.md"}
	md, _ := NewMarkdown(nil, note, sink, diag.NewCollector())

	var buf bytes.Buffer
	source := []byte("![Secret](../../secret.png)\n")
	if err := md.Convert(source, &buf); err != nil {
		t.Fatalf("Convert() error = %v", err)
	}

	html := buf.String()
	if len(sink.registered) != 0 {
		t.Fatalf("registered = %#v, want no asset registration", sink.registered)
	}
	if !strings.Contains(html, `<img src="../../secret.png" alt="Secret">`) {
		t.Fatalf("HTML = %q, want original destination preserved when path escapes vault root", html)
	}
}

func TestNewMarkdownPassesThroughRawHTML(t *testing.T) {
	t.Parallel()

	note := &model.Note{Slug: "posts/raw", RelPath: "notes/raw.md"}
	md, _ := NewMarkdown(nil, note, nil, diag.NewCollector())

	var buf bytes.Buffer
	source := []byte("before\n\n<span data-kind=\"raw\">inline</span>\n\n<div>block</div>\n")
	if err := md.Convert(source, &buf); err != nil {
		t.Fatalf("Convert() error = %v", err)
	}

	html := buf.String()
	if !strings.Contains(html, `<span data-kind="raw">inline</span>`) {
		t.Fatalf("HTML = %q, want inline raw HTML passthrough", html)
	}
	if !strings.Contains(html, `<div>block</div>`) {
		t.Fatalf("HTML = %q, want block raw HTML passthrough", html)
	}
	if strings.Contains(html, "raw HTML omitted") {
		t.Fatalf("HTML = %q, want raw HTML to pass through unchanged", html)
	}
}

// AC-3: 支持 CommonMark/GFM 基线能力，包括表格、删除线、任务列表、脚注、fenced code blocks，并在正文透传 raw HTML
func TestNewMarkdownRendersCommonMarkAndGFMBaselineFeatures(t *testing.T) {
	t.Parallel()

	note := &model.Note{Slug: "posts/gfm-baseline", RelPath: "notes/gfm-baseline.md"}
	md, _ := NewMarkdown(nil, note, nil, diag.NewCollector())

	var buf bytes.Buffer
	source := []byte("| Feature | Status |\n| --- | --- |\n| Table | Ready |\n\n~~deprecated~~ and <sup>raw</sup>\n\n- [x] shipped\n- [ ] pending\n\n```go\nfmt.Println(\"hi\")\n```\n\nFootnote ref[^1]\n\n[^1]: Footnote body.\n")
	if err := md.Convert(source, &buf); err != nil {
		t.Fatalf("Convert() error = %v", err)
	}

	html := buf.String()
	for _, fragment := range []string{
		`<table>`,
		`<th>Feature</th>`,
		`<td>Ready</td>`,
		`<del>deprecated</del>`,
		`<sup>raw</sup>`,
		`<pre`,
		`<code`,
		`href="#fn:1"`,
		`id="fn:1"`,
		`Footnote body.`,
	} {
		if !strings.Contains(html, fragment) {
			t.Fatalf("HTML = %q, want fragment %q", html, fragment)
		}
	}

	if strings.Contains(html, `&lt;sup&gt;raw&lt;/sup&gt;`) {
		t.Fatalf("HTML = %q, want inline raw HTML to pass through instead of being escaped", html)
	}
	if count := strings.Count(html, `type="checkbox"`); count != 2 {
		t.Fatalf("checkbox input count = %d, want %d\n%s", count, 2, html)
	}
	if count := strings.Count(html, `disabled=""`); count != 2 {
		t.Fatalf("disabled checkbox count = %d, want %d\n%s", count, 2, html)
	}
	if count := strings.Count(html, `checked=""`); count != 1 {
		t.Fatalf("checked checkbox count = %d, want %d\n%s", count, 1, html)
	}
	if !strings.Contains(html, `fmt`) || !strings.Contains(html, `Println`) {
		t.Fatalf("HTML = %q, want fenced code block content preserved", html)
	}
	if !strings.Contains(html, `class="footnotes"`) {
		t.Fatalf("HTML = %q, want rendered footnotes section", html)
	}
	if strings.Contains(html, `[^1]`) {
		t.Fatalf("HTML = %q, want footnote syntax rendered instead of kept as literal markdown", html)
	}
}

func TestNewMarkdownRendersHeadingIDsWithoutPermalinkUI(t *testing.T) {
	t.Parallel()

	note := &model.Note{Slug: "posts/headings", RelPath: "notes/headings.md"}
	md, _ := NewMarkdown(nil, note, nil, diag.NewCollector())

	var buf bytes.Buffer
	source := []byte("# Intro Heading\n\n# Intro Heading\n")
	if err := md.Convert(source, &buf); err != nil {
		t.Fatalf("Convert() error = %v", err)
	}

	html := buf.String()
	if !strings.Contains(html, `<h1 id="intro-heading">Intro Heading</h1>`) {
		t.Fatalf("HTML = %q, want first heading id without extra permalink markup", html)
	}
	if !strings.Contains(html, `<h1 id="intro-heading-1">Intro Heading</h1>`) {
		t.Fatalf("HTML = %q, want duplicate heading to receive stable deduplicated id", html)
	}

	for _, disallowed := range []string{`class="anchor"`, `href="#intro-heading"`, `href="#intro-heading-1"`, `>¶<`} {
		if strings.Contains(html, disallowed) {
			t.Fatalf("HTML = %q, want no extra permalink UI marker %q", html, disallowed)
		}
	}
}

func TestNewMarkdownRendersVisibleHeadingIDs(t *testing.T) {
	t.Parallel()

	note := &model.Note{Slug: "posts/headings-visible", RelPath: "notes/headings-visible.md"}
	md, _ := NewMarkdown(nil, note, nil, diag.NewCollector())

	var buf bytes.Buffer
	source := []byte("# Intro *Bold*\n\n## [[Target Page|Shown Label]]\n\n### 中文 标题\n\n#### RFC <sup>2</sup> and <span>Alpha</span>\n")
	if err := md.Convert(source, &buf); err != nil {
		t.Fatalf("Convert() error = %v", err)
	}

	html := buf.String()
	for _, fragment := range []string{
		`<h1 id="intro-bold">Intro <em>Bold</em></h1>`,
		`<h2 id="shown-label">`,
		`<h3 id="中文-标题">中文 标题</h3>`,
		`<h4 id="rfc-2-and-alpha">RFC <sup>2</sup> and <span>Alpha</span></h4>`,
	} {
		if !strings.Contains(html, fragment) {
			t.Fatalf("HTML = %q, want fragment %q", html, fragment)
		}
	}
}

func TestNewMarkdownRendersRawHTMLHeadingIDsFromBrowserVisibleText(t *testing.T) {
	t.Parallel()

	note := &model.Note{Slug: "posts/headings-raw-html", RelPath: "notes/headings-raw-html.md"}
	md, _ := NewMarkdown(nil, note, nil, diag.NewCollector())

	var buf bytes.Buffer
	source := []byte("# Hello <script>alert(1)</script><style>.x{}</style><template><span>Ghost</span></template><span hidden>Skip <span>Deeper</span></span><span>&amp;lt;</span> World\n")
	if err := md.Convert(source, &buf); err != nil {
		t.Fatalf("Convert() error = %v", err)
	}

	html := buf.String()
	if !strings.Contains(html, `<h1 id="hello-lt-world">`) {
		t.Fatalf("HTML = %q, want raw-HTML heading id derived from browser-visible text", html)
	}
}

func TestNewMarkdownRendersVoidRawHTMLHeadingIDsFromBrowserVisibleText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		source        string
		noteSlug      string
		noteRelPath   string
		wantID        string
		wantFragments []string
	}{
		{
			name:        "after-visible-span",
			source:      "# Hello <span>Alpha</span>&amp;lt; World\n",
			noteSlug:    "posts/headings-after-visible-span",
			noteRelPath: "notes/headings-after-visible-span.md",
			wantID:      "hello-alpha-lt-world",
			wantFragments: []string{
				`<h1 id="hello-alpha-lt-world">Hello <span>Alpha</span>&amp;lt; World</h1>`,
			},
		},
		{
			name:        "after-hidden-span",
			source:      "# Hello <span hidden>Ghost</span>&amp;lt; World\n",
			noteSlug:    "posts/headings-after-hidden-span",
			noteRelPath: "notes/headings-after-hidden-span.md",
			wantID:      "hello-lt-world",
			wantFragments: []string{
				`<h1 id="hello-lt-world">Hello <span hidden>Ghost</span>&amp;lt; World</h1>`,
			},
		},
		{
			name:          "br",
			source:        "# Hello <br>&amp;lt; World\n",
			noteSlug:      "posts/headings-void-br",
			noteRelPath:   "notes/headings-void-br.md",
			wantID:        "hello-lt-world",
			wantFragments: []string{`<h1 id="hello-lt-world">Hello <br>&amp;lt; World</h1>`},
		},
		{
			name:          "hr",
			source:        "# Hello <hr>&amp;lt; World\n",
			noteSlug:      "posts/headings-void-hr",
			noteRelPath:   "notes/headings-void-hr.md",
			wantID:        "hello-lt-world",
			wantFragments: []string{`<h1 id="hello-lt-world">Hello <hr>&amp;lt; World</h1>`},
		},
		{
			name:          "br-self-closing",
			source:        "# Hello <br/>&amp;lt; World\n",
			noteSlug:      "posts/headings-void-br-self-closing",
			noteRelPath:   "notes/headings-void-br-self-closing.md",
			wantID:        "hello-lt-world",
			wantFragments: []string{`<h1 id="hello-lt-world">Hello <br/>&amp;lt; World</h1>`},
		},
		{
			name:          "hr-self-closing",
			source:        "# Hello <hr/>&amp;lt; World\n",
			noteSlug:      "posts/headings-void-hr-self-closing",
			noteRelPath:   "notes/headings-void-hr-self-closing.md",
			wantID:        "hello-lt-world",
			wantFragments: []string{`<h1 id="hello-lt-world">Hello <hr/>&amp;lt; World</h1>`},
		},
		{
			name:        "hidden-img",
			source:      "# Hello <img hidden>&amp;lt; World\n",
			noteSlug:    "posts/headings-void-hidden-img",
			noteRelPath: "notes/headings-void-hidden-img.md",
			wantID:      "hello-lt-world",
			wantFragments: []string{
				`<h1 id="hello-lt-world">Hello <img hidden`,
				`&amp;lt; World</h1>`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			note := &model.Note{Slug: tt.noteSlug, RelPath: tt.noteRelPath}
			md, _ := NewMarkdown(nil, note, nil, diag.NewCollector())

			var buf bytes.Buffer
			source := []byte(tt.source)
			if err := md.Convert(source, &buf); err != nil {
				t.Fatalf("Convert() error = %v", err)
			}

			html := buf.String()
			wantHeadingOpen := `<h1 id="` + tt.wantID + `">`
			if !strings.Contains(html, wantHeadingOpen) {
				t.Fatalf("HTML = %q, want heading id %q", html, tt.wantID)
			}
			for _, wantFragment := range tt.wantFragments {
				if !strings.Contains(html, wantFragment) {
					t.Fatalf("HTML = %q, want fragment %q", html, wantFragment)
				}
			}
		})
	}
}

func TestNewMarkdownRendersRawHTMLHeadingIDsWithoutDecodeLeakageAcrossBoundaries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		source        string
		noteSlug      string
		noteRelPath   string
		wantID        string
		wantFragments []string
	}{
		{
			name:        "entity-before-raw-html",
			source:      "# &amp;lt; <span>Alpha</span>\n",
			noteSlug:    "posts/headings-entity-before-raw-html",
			noteRelPath: "notes/headings-entity-before-raw-html.md",
			wantID:      "lt-alpha",
			wantFragments: []string{
				`<h1 id="lt-alpha">&amp;lt; <span>Alpha</span></h1>`,
			},
		},
		{
			name:        "code-span-after-raw-html",
			source:      "# <span>Alpha</span> `&amp;lt;`\n",
			noteSlug:    "posts/headings-code-span-after-raw-html",
			noteRelPath: "notes/headings-code-span-after-raw-html.md",
			wantID:      "alpha-amp-lt",
			wantFragments: []string{
				`<h1 id="alpha-amp-lt"><span>Alpha</span> <code>&amp;amp;lt;</code></h1>`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			note := &model.Note{Slug: tt.noteSlug, RelPath: tt.noteRelPath}
			md, _ := NewMarkdown(nil, note, nil, diag.NewCollector())

			var buf bytes.Buffer
			source := []byte(tt.source)
			if err := md.Convert(source, &buf); err != nil {
				t.Fatalf("Convert() error = %v", err)
			}

			html := buf.String()
			wantHeadingOpen := `<h1 id="` + tt.wantID + `">`
			if !strings.Contains(html, wantHeadingOpen) {
				t.Fatalf("HTML = %q, want heading id %q", html, tt.wantID)
			}
			for _, wantFragment := range tt.wantFragments {
				if !strings.Contains(html, wantFragment) {
					t.Fatalf("HTML = %q, want fragment %q", html, wantFragment)
				}
			}
		})
	}
}

func TestNewMarkdownResolvesWikilinksToRawHTMLHeadingFragments(t *testing.T) {
	t.Parallel()

	current := &model.Note{
		Slug:    "notes/current",
		RelPath: "notes/current.md",
		OutLinks: []model.LinkRef{
			{RawTarget: "Guide#RFC 2 and Alpha", Line: 1},
		},
	}
	target := &model.Note{
		Slug:    "guides/guide",
		RelPath: "guides/guide.md",
		Headings: []model.Heading{
			{Level: 2, Text: "RFC 2 and Alpha", ID: "rfc-2-and-alpha"},
		},
	}
	idx := &model.VaultIndex{
		Notes: map[string]*model.Note{
			current.RelPath: current,
			target.RelPath:  target,
		},
		NoteBySlug: map[string]*model.Note{
			current.Slug: current,
			target.Slug:  target,
		},
		NoteByName: map[string][]*model.Note{
			"current": {current},
			"guide":   {target},
		},
		AliasByName: map[string][]*model.Note{},
	}
	collector := diag.NewCollector()
	md, renderResult := NewMarkdown(idx, current, nil, collector)

	var buf bytes.Buffer
	source := []byte("[[Guide#RFC 2 and Alpha|Docs]]\n")
	if err := md.Convert(source, &buf); err != nil {
		t.Fatalf("Convert() error = %v", err)
	}

	html := buf.String()
	if !strings.Contains(html, `<a href="../../guides/guide/#rfc-2-and-alpha">Docs</a>`) {
		t.Fatalf("HTML = %q, want raw-HTML-derived heading fragment to resolve", html)
	}

	gotOutLinks := renderResult.OutLinks()
	if len(gotOutLinks) != 1 {
		t.Fatalf("len(renderResult.OutLinks()) = %d, want 1", len(gotOutLinks))
	}
	if gotOutLinks[0].ResolvedRelPath != target.RelPath {
		t.Fatalf("renderResult.OutLinks()[0].ResolvedRelPath = %q, want %q", gotOutLinks[0].ResolvedRelPath, target.RelPath)
	}
	if got := collector.Diagnostics(); len(got) != 0 {
		t.Fatalf("collector.Diagnostics() = %#v, want no diagnostics", got)
	}
}

func TestNewMarkdownResolvesWikilinksAndCollectsOutlinks(t *testing.T) {
	t.Parallel()

	current := &model.Note{
		Slug:    "notes/current",
		RelPath: "notes/current.md",
		Headings: []model.Heading{
			{Level: 2, Text: "Current Section", ID: "current-section"},
		},
		OutLinks: []model.LinkRef{
			{RawTarget: "Guide#Section Title", Line: 1},
			{RawTarget: "Draft", Line: 1},
			{RawTarget: "Missing", Line: 1},
			{RawTarget: "#Current Section", Line: 1},
		},
	}
	target := &model.Note{
		Slug:    "guides/guide",
		RelPath: "guides/guide.md",
		Headings: []model.Heading{
			{Level: 2, Text: "Section Title", ID: "section-title"},
		},
	}
	unpublished := &model.Note{Slug: "drafts/draft", RelPath: "drafts/draft.md"}

	idx := &model.VaultIndex{
		Notes: map[string]*model.Note{
			current.RelPath: current,
			target.RelPath:  target,
		},
		NoteBySlug: map[string]*model.Note{
			current.Slug: current,
			target.Slug:  target,
		},
		NoteByName: map[string][]*model.Note{
			"current": {current},
			"guide":   {target},
		},
		AliasByName: map[string][]*model.Note{},
		Unpublished: model.UnpublishedLookup{
			Notes: map[string]*model.Note{
				unpublished.RelPath: unpublished,
			},
			NoteByName: map[string][]*model.Note{
				"draft": {unpublished},
			},
			AliasByName: map[string][]*model.Note{},
		},
	}
	collector := diag.NewCollector()
	md, renderResult := NewMarkdown(idx, current, nil, collector)

	var buf bytes.Buffer
	source := []byte("[[Guide#Section Title|Docs]] [[Draft]] [[Missing]] [[#Current Section]]\n")
	if err := md.Convert(source, &buf); err != nil {
		t.Fatalf("Convert() error = %v", err)
	}

	html := buf.String()
	if !strings.Contains(html, `<a href="../../guides/guide/#section-title">Docs</a>`) {
		t.Fatalf("HTML = %q, want resolved guide wikilink", html)
	}
	for _, plainText := range []string{"Draft", "Missing", "Current Section"} {
		if !strings.Contains(html, plainText) {
			t.Fatalf("HTML = %q, want plain-text fragment %q present", html, plainText)
		}
	}
	for _, forbidden := range []string{`href="Draft"`, `href="Missing"`, `href="#Current Section"`} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("HTML = %q, want %q to remain unlinked", html, forbidden)
		}
	}
	if !strings.Contains(html, `<a href="#current-section">#Current Section</a>`) {
		t.Fatalf("HTML = %q, want fragment-only self-link to resolve", html)
	}

	gotOutLinks := renderResult.OutLinks()
	if len(gotOutLinks) != 4 {
		t.Fatalf("len(renderResult.OutLinks()) = %d, want 4", len(gotOutLinks))
	}
	for i, want := range []string{target.RelPath, "", "", current.RelPath} {
		if gotOutLinks[i].ResolvedRelPath != want {
			t.Fatalf("renderResult.OutLinks()[%d].ResolvedRelPath = %q, want %q", i, gotOutLinks[i].ResolvedRelPath, want)
		}
	}
	for i := range current.OutLinks {
		if current.OutLinks[i].ResolvedRelPath != "" {
			t.Fatalf("current.OutLinks[%d].ResolvedRelPath = %q, want source note to remain unchanged", i, current.OutLinks[i].ResolvedRelPath)
		}
	}

	gotDiagnostics := collector.Diagnostics()
	if len(gotDiagnostics) != 2 {
		t.Fatalf("len(collector.Diagnostics()) = %d, want 2", len(gotDiagnostics))
	}
	if gotDiagnostics[0].Kind != diag.KindDeadLink || gotDiagnostics[0].Message != `wikilink "Missing" could not be resolved` {
		t.Fatalf("collector.Diagnostics()[0] = %#v, want deadlink warning", gotDiagnostics[0])
	}
	if gotDiagnostics[1].Message != `wikilink "Draft" points to unpublished note "drafts/draft.md"; rendering as plain text` {
		t.Fatalf("collector.Diagnostics()[1] = %#v, want unpublished warning", gotDiagnostics[1])
	}
}

func TestNewMarkdownLeavesMissingHeadingWikilinksAsPlainText(t *testing.T) {
	t.Parallel()

	current := &model.Note{
		Slug:    "notes/current",
		RelPath: "notes/current.md",
		OutLinks: []model.LinkRef{
			{RawTarget: "Guide#Missing Heading", Line: 1},
		},
	}
	target := &model.Note{
		Slug:    "guides/guide",
		RelPath: "guides/guide.md",
		Headings: []model.Heading{
			{Level: 2, Text: "Section Title", ID: "section-title"},
		},
	}
	idx := &model.VaultIndex{
		Notes: map[string]*model.Note{
			current.RelPath: current,
			target.RelPath:  target,
		},
		NoteBySlug: map[string]*model.Note{
			current.Slug: current,
			target.Slug:  target,
		},
		NoteByName: map[string][]*model.Note{
			"current": {current},
			"guide":   {target},
		},
		AliasByName: map[string][]*model.Note{},
	}
	collector := diag.NewCollector()
	md, renderResult := NewMarkdown(idx, current, nil, collector)

	var buf bytes.Buffer
	source := []byte("[[Guide#Missing Heading|Broken]]\n")
	if err := md.Convert(source, &buf); err != nil {
		t.Fatalf("Convert() error = %v", err)
	}

	html := buf.String()
	if !strings.Contains(html, "Broken") {
		t.Fatalf("HTML = %q, want plain text label preserved", html)
	}
	if strings.Contains(html, `<a href="../../guides/guide/#`) {
		t.Fatalf("HTML = %q, want missing heading wikilink to remain unlinked", html)
	}
	gotOutLinks := renderResult.OutLinks()
	if len(gotOutLinks) != 1 {
		t.Fatalf("len(renderResult.OutLinks()) = %d, want 1", len(gotOutLinks))
	}
	if gotOutLinks[0].ResolvedRelPath != "" {
		t.Fatalf("renderResult.OutLinks()[0].ResolvedRelPath = %q, want empty for missing fragment", gotOutLinks[0].ResolvedRelPath)
	}
	if current.OutLinks[0].ResolvedRelPath != "" {
		t.Fatalf("current.OutLinks[0].ResolvedRelPath = %q, want source note to remain unchanged", current.OutLinks[0].ResolvedRelPath)
	}

	want := []diag.Diagnostic{{
		Severity: diag.SeverityWarning,
		Kind:     diag.KindDeadLink,
		Location: diag.Location{Path: current.RelPath, Line: 1},
		Message:  `wikilink "Guide#Missing Heading" points to missing heading "Missing Heading" in "guides/guide.md"`,
	}}
	if got := collector.Diagnostics(); !reflect.DeepEqual(got, want) {
		t.Fatalf("collector.Diagnostics() = %#v, want %#v", got, want)
	}
}

func TestNewMarkdownResolvesDottedWikilinkTargets(t *testing.T) {
	t.Parallel()

	current := &model.Note{
		Slug:    "notes/current",
		RelPath: "notes/current.md",
		OutLinks: []model.LinkRef{
			{RawTarget: "Release v1.0", Line: 1},
			{RawTarget: "Team Docs v2.1", Line: 1},
		},
	}
	filename := &model.Note{Slug: "docs/release-v1-0", RelPath: "docs/Release v1.0.md"}
	alias := &model.Note{Slug: "docs/team-docs", RelPath: "docs/team-docs.md", Aliases: []string{"Team Docs v2.1"}}
	idx := &model.VaultIndex{
		Notes: map[string]*model.Note{
			current.RelPath:  current,
			filename.RelPath: filename,
			alias.RelPath:    alias,
		},
		NoteBySlug: map[string]*model.Note{
			current.Slug:  current,
			filename.Slug: filename,
			alias.Slug:    alias,
		},
		NoteByName: map[string][]*model.Note{
			"current":      {current},
			"release v1.0": {filename},
			"team-docs":    {alias},
		},
		AliasByName: map[string][]*model.Note{
			"team docs v2.1": {alias},
		},
	}
	collector := diag.NewCollector()
	md, renderResult := NewMarkdown(idx, current, nil, collector)

	var buf bytes.Buffer
	source := []byte("[[Release v1.0]] [[Team Docs v2.1|Docs]]\n")
	if err := md.Convert(source, &buf); err != nil {
		t.Fatalf("Convert() error = %v", err)
	}

	html := buf.String()
	if !strings.Contains(html, `<a href="../../docs/release-v1-0/">Release v1.0</a>`) {
		t.Fatalf("HTML = %q, want dotted filename wikilink resolved", html)
	}
	if !strings.Contains(html, `<a href="../../docs/team-docs/">Docs</a>`) {
		t.Fatalf("HTML = %q, want dotted alias wikilink resolved", html)
	}
	gotOutLinks := renderResult.OutLinks()
	if len(gotOutLinks) != 2 {
		t.Fatalf("len(renderResult.OutLinks()) = %d, want 2", len(gotOutLinks))
	}
	if gotOutLinks[0].ResolvedRelPath != filename.RelPath {
		t.Fatalf("renderResult.OutLinks()[0].ResolvedRelPath = %q, want %q", gotOutLinks[0].ResolvedRelPath, filename.RelPath)
	}
	if gotOutLinks[1].ResolvedRelPath != alias.RelPath {
		t.Fatalf("renderResult.OutLinks()[1].ResolvedRelPath = %q, want %q", gotOutLinks[1].ResolvedRelPath, alias.RelPath)
	}
	for i := range current.OutLinks {
		if current.OutLinks[i].ResolvedRelPath != "" {
			t.Fatalf("current.OutLinks[%d].ResolvedRelPath = %q, want source note to remain unchanged", i, current.OutLinks[i].ResolvedRelPath)
		}
	}
	if got := collector.Diagnostics(); len(got) != 0 {
		t.Fatalf("collector.Diagnostics() = %#v, want no diagnostics", got)
	}
}

func TestNewMarkdownRendersImageEmbedsWithWidthAltFallbackRegistersAssetsAndSharesLazyLoading(t *testing.T) {
	t.Parallel()

	sink := &recordingAssetSink{
		paths: map[string]string{
			"images/hero.png":           "assets/hero.123.png",
			"images/diagram.png":        "assets/diagram.456.png",
			"assets/uploads/poster.png": "assets/poster.789.png",
		},
	}
	current := &model.Note{
		Slug:    "notes/current",
		RelPath: "notes/current.md",
		Embeds: []model.EmbedRef{
			{Target: "../images/diagram.png", IsImage: true, Width: 600, Line: 3},
			{Target: "poster.png", IsImage: true, Line: 5},
		},
	}
	idx := &model.VaultIndex{
		AttachmentFolderPath: "assets/uploads",
		Notes: map[string]*model.Note{
			current.RelPath: current,
		},
		NoteBySlug: map[string]*model.Note{
			current.Slug: current,
		},
		NoteByName: map[string][]*model.Note{
			"current": {current},
		},
		AliasByName: map[string][]*model.Note{},
		Assets: map[string]*model.Asset{
			"images/hero.png":           {SrcPath: "images/hero.png"},
			"images/diagram.png":        {SrcPath: "images/diagram.png"},
			"assets/uploads/poster.png": {SrcPath: "assets/uploads/poster.png"},
		},
	}
	md, _ := NewMarkdown(idx, current, sink, diag.NewCollector())

	var buf bytes.Buffer
	source := []byte("![Hero](../images/hero.png)\n\n![[../images/diagram.png|600]]\n\n![[poster.png]]\n")
	if err := md.Convert(source, &buf); err != nil {
		t.Fatalf("Convert() error = %v", err)
	}

	html := buf.String()
	if !strings.Contains(html, `<img src="../../assets/hero.123.png" alt="Hero">`) {
		t.Fatalf("HTML = %q, want rewritten first standard image without lazy loading", html)
	}
	if !strings.Contains(html, `<img src="../../assets/diagram.456.png" alt="diagram" width="600" loading="lazy">`) {
		t.Fatalf("HTML = %q, want numeric-width image embed with fallback alt and shared lazy loading", html)
	}
	if !strings.Contains(html, `<img src="../../assets/poster.789.png" alt="poster" loading="lazy">`) {
		t.Fatalf("HTML = %q, want attachment-folder image embed with fallback alt", html)
	}

	wantRegistered := []string{"images/hero.png", "images/diagram.png", "assets/uploads/poster.png"}
	if !reflect.DeepEqual(sink.registered, wantRegistered) {
		t.Fatalf("registered = %#v, want %#v", sink.registered, wantRegistered)
	}
}

func TestNewMarkdownRendersNoteEmbeds(t *testing.T) {
	t.Parallel()

	current := &model.Note{
		Slug:    "notes/current",
		RelPath: "notes/current.md",
		Embeds:  []model.EmbedRef{{Target: "Guide", Line: 1}},
	}
	target := &model.Note{
		Slug:       "guides/guide",
		RelPath:    "guides/guide.md",
		RawContent: []byte("# Embedded Title\n\nBody paragraph.\n"),
	}
	idx := &model.VaultIndex{
		Notes: map[string]*model.Note{
			current.RelPath: current,
			target.RelPath:  target,
		},
		NoteBySlug: map[string]*model.Note{
			current.Slug: current,
			target.Slug:  target,
		},
		NoteByName: map[string][]*model.Note{
			"current": {current},
			"guide":   {target},
		},
		AliasByName: map[string][]*model.Note{},
	}
	collector := diag.NewCollector()
	md, renderResult := NewMarkdown(idx, current, nil, collector)

	var buf bytes.Buffer
	if err := md.Convert([]byte("![[Guide]]\n"), &buf); err != nil {
		t.Fatalf("Convert() error = %v", err)
	}

	html := buf.String()
	if !strings.Contains(html, `<h1 id="embed-1-embedded-title">Embedded Title</h1>`) {
		t.Fatalf("HTML = %q, want embedded note heading rendered inline", html)
	}
	if !strings.Contains(html, `<p>Body paragraph.</p>`) {
		t.Fatalf("HTML = %q, want embedded note paragraph rendered inline", html)
	}
	if got := collector.Diagnostics(); len(got) != 0 {
		t.Fatalf("collector.Diagnostics() = %#v, want no diagnostics", got)
	}
	if renderResult == nil {
		t.Fatal("renderResult = nil, want render-local result")
	}
	gotOutLinks := renderResult.OutLinks()
	if len(gotOutLinks) != 1 {
		t.Fatalf("len(renderResult.OutLinks()) = %d, want 1 direct embed outlink", len(gotOutLinks))
	}
	if gotOutLinks[0].ResolvedRelPath != target.RelPath {
		t.Fatalf("renderResult.OutLinks()[0].ResolvedRelPath = %q, want %q", gotOutLinks[0].ResolvedRelPath, target.RelPath)
	}
}

func TestNewMarkdownRendersHeadingEmbeds(t *testing.T) {
	t.Parallel()

	rawContent := "# Top\n\n## Section Title\n\nWanted.\n\n## Later\n\nSkip.\n"
	current := &model.Note{
		Slug:    "notes/current",
		RelPath: "notes/current.md",
		Embeds:  []model.EmbedRef{{Target: "Guide", Fragment: "Section Title", Line: 1}},
	}
	target := &model.Note{
		Slug:       "guides/guide",
		RelPath:    "guides/guide.md",
		RawContent: []byte(rawContent),
		Headings: []model.Heading{
			{Level: 2, Text: "Section Title", ID: "section-title"},
		},
		HeadingSections: map[string]model.SectionRange{
			"section-title": sectionRangeForTest(t, rawContent, "## Section Title", "## Later"),
		},
	}
	idx := &model.VaultIndex{
		Notes: map[string]*model.Note{
			current.RelPath: current,
			target.RelPath:  target,
		},
		NoteBySlug: map[string]*model.Note{
			current.Slug: current,
			target.Slug:  target,
		},
		NoteByName: map[string][]*model.Note{
			"current": {current},
			"guide":   {target},
		},
		AliasByName: map[string][]*model.Note{},
	}
	collector := diag.NewCollector()
	md, renderResult := NewMarkdown(idx, current, nil, collector)

	var buf bytes.Buffer
	if err := md.Convert([]byte("![[Guide#Section Title]]\n"), &buf); err != nil {
		t.Fatalf("Convert() error = %v", err)
	}

	html := buf.String()
	if !strings.Contains(html, `<h2 id="embed-1-section-title">Section Title</h2>`) {
		t.Fatalf("HTML = %q, want embedded heading section rendered inline", html)
	}
	if !strings.Contains(html, `<p>Wanted.</p>`) {
		t.Fatalf("HTML = %q, want embedded heading body rendered inline", html)
	}
	for _, forbidden := range []string{"Later", "Skip"} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("HTML = %q, want heading embed to stop before next sibling heading", html)
		}
	}
	if got := collector.Diagnostics(); len(got) != 0 {
		t.Fatalf("collector.Diagnostics() = %#v, want no diagnostics", got)
	}
	if renderResult == nil {
		t.Fatal("renderResult = nil, want render-local result")
	}
	if renderResult.HasMath() {
		t.Fatal("renderResult.HasMath() = true, want false for selected heading without math")
	}
	if renderResult.HasMermaid() {
		t.Fatal("renderResult.HasMermaid() = true, want false for selected heading without Mermaid")
	}
	gotOutLinks := renderResult.OutLinks()
	if len(gotOutLinks) != 1 {
		t.Fatalf("len(renderResult.OutLinks()) = %d, want 1 direct embed outlink", len(gotOutLinks))
	}
	if gotOutLinks[0].ResolvedRelPath != target.RelPath {
		t.Fatalf("renderResult.OutLinks()[0].ResolvedRelPath = %q, want %q", gotOutLinks[0].ResolvedRelPath, target.RelPath)
	}
}

func TestNewMarkdownHeadingEmbedsOnlyPropagateRenderedSectionFeatures(t *testing.T) {
	t.Parallel()

	rawContent := "# Top\n\n## Section Title\n\nWanted.\n\n## Later\n\n$E=mc^2$\n\n```mermaid\ngraph TD\nA-->B\n```\n"
	current := &model.Note{
		Slug:    "notes/current",
		RelPath: "notes/current.md",
		Embeds:  []model.EmbedRef{{Target: "Guide", Fragment: "Section Title", Line: 1}},
	}
	target := &model.Note{
		Slug:       "guides/guide",
		RelPath:    "guides/guide.md",
		RawContent: []byte(rawContent),
		HasMath:    true,
		HasMermaid: true,
		Headings: []model.Heading{
			{Level: 2, Text: "Section Title", ID: "section-title"},
			{Level: 2, Text: "Later", ID: "later"},
		},
		HeadingSections: map[string]model.SectionRange{
			"section-title": sectionRangeForTest(t, rawContent, "## Section Title", "## Later"),
			"later":         sectionRangeForTest(t, rawContent, "## Later", ""),
		},
	}
	idx := &model.VaultIndex{
		Notes: map[string]*model.Note{
			current.RelPath: current,
			target.RelPath:  target,
		},
		NoteBySlug: map[string]*model.Note{
			current.Slug: current,
			target.Slug:  target,
		},
		NoteByName: map[string][]*model.Note{
			"current": {current},
			"guide":   {target},
		},
		AliasByName: map[string][]*model.Note{},
	}
	collector := diag.NewCollector()
	md, renderResult := NewMarkdown(idx, current, nil, collector)

	var buf bytes.Buffer
	if err := md.Convert([]byte("![[Guide#Section Title]]\n"), &buf); err != nil {
		t.Fatalf("Convert() error = %v", err)
	}

	html := buf.String()
	if strings.Contains(html, `class="math math-inline"`) || strings.Contains(html, `<pre class="mermaid">`) {
		t.Fatalf("HTML = %q, want later-section math and Mermaid to stay out of the selected embed", html)
	}
	if renderResult == nil {
		t.Fatal("renderResult = nil, want render-local result")
	}
	if renderResult.HasMath() {
		t.Fatal("renderResult.HasMath() = true, want false when only a later section contains math")
	}
	if renderResult.HasMermaid() {
		t.Fatal("renderResult.HasMermaid() = true, want false when only a later section contains Mermaid")
	}
	if target.HasMath != true || target.HasMermaid != true {
		t.Fatalf("target flags = (%t, %t), want indexed note metadata unchanged", target.HasMath, target.HasMermaid)
	}
	if got := collector.Diagnostics(); len(got) != 0 {
		t.Fatalf("collector.Diagnostics() = %#v, want no diagnostics", got)
	}
}

func TestNewMarkdownKeepsEmbeddedLinksAssetsAndHeadingsInHostContext(t *testing.T) {
	t.Parallel()

	sink := &recordingAssetSink{
		paths: map[string]string{
			"images/chart.png":  "assets/chart.123.png",
			"media/diagram.png": "assets/diagram.456.png",
		},
	}
	host := &model.Note{
		Slug:    "posts/2024/host",
		RelPath: "notes/posts/host.md",
		Embeds:  []model.EmbedRef{{Target: "Guide", Line: 3}},
	}
	guide := &model.Note{
		Slug:       "guides/guide",
		RelPath:    "guides/guide.md",
		RawContent: []byte("## Intro\n\n[[Reference|Reference]]\n\n[[#Intro|Back]]\n\n![Chart](../images/chart.png)\n\n![[../media/diagram.png]]\n"),
		Headings: []model.Heading{
			{Level: 2, Text: "Intro", ID: "intro"},
		},
		OutLinks: []model.LinkRef{
			{RawTarget: "Reference", Line: 3},
			{RawTarget: "#Intro", Line: 5},
		},
		Embeds: []model.EmbedRef{{Target: "../media/diagram.png", IsImage: true, Line: 9}},
	}
	reference := &model.Note{Slug: "reference/deep-dive", RelPath: "library/reference.md"}
	idx := &model.VaultIndex{
		Notes: map[string]*model.Note{
			host.RelPath:      host,
			guide.RelPath:     guide,
			reference.RelPath: reference,
		},
		NoteBySlug: map[string]*model.Note{
			host.Slug:      host,
			guide.Slug:     guide,
			reference.Slug: reference,
		},
		NoteByName: map[string][]*model.Note{
			"host":      {host},
			"guide":     {guide},
			"reference": {reference},
		},
		AliasByName: map[string][]*model.Note{},
		Assets: map[string]*model.Asset{
			"images/chart.png":  {SrcPath: "images/chart.png"},
			"media/diagram.png": {SrcPath: "media/diagram.png"},
		},
	}
	collector := diag.NewCollector()
	md, renderResult := NewMarkdown(idx, host, sink, collector)

	var buf bytes.Buffer
	if err := md.Convert([]byte("# Intro\n\n![[Guide]]\n"), &buf); err != nil {
		t.Fatalf("Convert() error = %v", err)
	}

	html := buf.String()
	if strings.Count(html, `id="intro"`) != 1 {
		t.Fatalf("HTML = %q, want only the host heading to keep id=intro", html)
	}
	if !strings.Contains(html, `<h2 id="embed-1-intro">Intro</h2>`) {
		t.Fatalf("HTML = %q, want embedded heading id namespaced", html)
	}
	if !strings.Contains(html, `<a href="../../../reference/deep-dive/">Reference</a>`) {
		t.Fatalf("HTML = %q, want embedded wikilink rewritten relative to host output", html)
	}
	if strings.Contains(html, `<a href="../../reference/deep-dive/">Reference</a>`) {
		t.Fatalf("HTML = %q, want embedded wikilink to avoid child-note-relative hrefs", html)
	}
	if !strings.Contains(html, `<a href="#embed-1-intro">Back</a>`) {
		t.Fatalf("HTML = %q, want embedded self-fragment wikilink to target namespaced heading id", html)
	}
	if !strings.Contains(html, `<img src="../../../assets/chart.123.png" alt="Chart">`) {
		t.Fatalf("HTML = %q, want markdown image rewritten relative to host output", html)
	}
	if !strings.Contains(html, `<img src="../../../assets/diagram.456.png" alt="diagram" loading="lazy">`) {
		t.Fatalf("HTML = %q, want image embed rewritten relative to host output", html)
	}

	wantRegistered := []string{"images/chart.png", "media/diagram.png"}
	if !reflect.DeepEqual(sink.registered, wantRegistered) {
		t.Fatalf("registered = %#v, want %#v", sink.registered, wantRegistered)
	}
	if renderResult == nil {
		t.Fatal("renderResult = nil, want render-local result")
	}
	gotOutLinks := renderResult.OutLinks()
	if len(gotOutLinks) != 3 {
		t.Fatalf("len(renderResult.OutLinks()) = %d, want 3 embedded outlinks including direct embed target", len(gotOutLinks))
	}
	if gotOutLinks[0].ResolvedRelPath != reference.RelPath {
		t.Fatalf("renderResult.OutLinks()[0].ResolvedRelPath = %q, want %q", gotOutLinks[0].ResolvedRelPath, reference.RelPath)
	}
	if gotOutLinks[1].ResolvedRelPath != host.RelPath {
		t.Fatalf("renderResult.OutLinks()[1].ResolvedRelPath = %q, want %q", gotOutLinks[1].ResolvedRelPath, host.RelPath)
	}
	if gotOutLinks[2].ResolvedRelPath != guide.RelPath {
		t.Fatalf("renderResult.OutLinks()[2].ResolvedRelPath = %q, want %q", gotOutLinks[2].ResolvedRelPath, guide.RelPath)
	}
	for i := range guide.OutLinks {
		if guide.OutLinks[i].ResolvedRelPath != "" {
			t.Fatalf("guide.OutLinks[%d].ResolvedRelPath = %q, want embedded source note to remain unchanged", i, guide.OutLinks[i].ResolvedRelPath)
		}
	}
	if got := collector.Diagnostics(); len(got) != 0 {
		t.Fatalf("collector.Diagnostics() = %#v, want no diagnostics", got)
	}
}

func TestNewMarkdownDetectsEmbedCycles(t *testing.T) {
	t.Parallel()

	current := &model.Note{
		Slug:       "notes/note-a",
		RelPath:    "notes/note a.md",
		RawContent: []byte("![[Note B]]\n"),
		Embeds:     []model.EmbedRef{{Target: "Note B", Line: 1}},
	}
	target := &model.Note{
		Slug:       "notes/note-b",
		RelPath:    "notes/note b.md",
		RawContent: []byte("![[Note A]]\n"),
		Embeds:     []model.EmbedRef{{Target: "Note A", Line: 1}},
	}
	idx := &model.VaultIndex{
		Notes: map[string]*model.Note{
			current.RelPath: current,
			target.RelPath:  target,
		},
		NoteBySlug: map[string]*model.Note{
			current.Slug: current,
			target.Slug:  target,
		},
		NoteByName: map[string][]*model.Note{
			"note a": {current},
			"note b": {target},
		},
		AliasByName: map[string][]*model.Note{},
	}
	collector := diag.NewCollector()
	md, _ := NewMarkdown(idx, current, nil, collector)

	var buf bytes.Buffer
	if err := md.Convert(current.RawContent, &buf); err != nil {
		t.Fatalf("Convert() error = %v", err)
	}

	html := buf.String()
	if !strings.Contains(html, "Note A") {
		t.Fatalf("HTML = %q, want cyclic child embed to degrade to plain text", html)
	}

	got := collector.Diagnostics()
	if len(got) != 1 {
		t.Fatalf("collector.Diagnostics() = %#v, want 1 cycle warning", got)
	}
	if got[0].Kind != diag.KindUnsupportedSyntax || !strings.Contains(got[0].Message, "transclusion cycle") {
		t.Fatalf("collector.Diagnostics()[0] = %#v, want cycle warning", got[0])
	}
}

func TestNewMarkdownAllowsSameNoteSectionEmbeds(t *testing.T) {
	t.Parallel()

	rawContent := "# Host\n\n![[#Section Title]]\n\n## Section Title\n\n[[#Section Title|Self]]\n\n[[#Nested|Nested]]\n\nWanted.\n\n### Nested\n\nChild.\n"
	current := &model.Note{
		Slug:       "notes/current",
		RelPath:    "notes/current.md",
		RawContent: []byte(rawContent),
		Headings: []model.Heading{
			{Level: 1, Text: "Host", ID: "host"},
			{Level: 2, Text: "Section Title", ID: "section-title"},
			{Level: 3, Text: "Nested", ID: "nested"},
		},
		HeadingSections: map[string]model.SectionRange{
			"host":          sectionRangeForTest(t, rawContent, "# Host", "## Section Title"),
			"section-title": sectionRangeForTest(t, rawContent, "## Section Title", ""),
			"nested":        sectionRangeForTest(t, rawContent, "### Nested", ""),
		},
		OutLinks: []model.LinkRef{
			{RawTarget: "#Section Title", Display: "Self", Fragment: "Section Title", Line: 7, Offset: strings.Index(rawContent, "[[#Section Title|Self]]")},
			{RawTarget: "#Nested", Display: "Nested", Fragment: "Nested", Line: 9, Offset: strings.Index(rawContent, "[[#Nested|Nested]]")},
		},
		Embeds: []model.EmbedRef{{Target: "", Fragment: "Section Title", Line: 3, Offset: strings.Index(rawContent, "![[#Section Title]]")}},
	}
	idx := &model.VaultIndex{
		Notes: map[string]*model.Note{
			current.RelPath: current,
		},
		NoteBySlug: map[string]*model.Note{
			current.Slug: current,
		},
		NoteByName: map[string][]*model.Note{
			"current": {current},
		},
		AliasByName: map[string][]*model.Note{},
	}
	collector := diag.NewCollector()
	md, _ := NewMarkdown(idx, current, nil, collector)

	var buf bytes.Buffer
	if err := md.Convert(current.RawContent, &buf); err != nil {
		t.Fatalf("Convert() error = %v", err)
	}

	html := buf.String()
	if !strings.Contains(html, `<h2 id="embed-1-section-title">Section Title</h2>`) {
		t.Fatalf("HTML = %q, want same-note section embed to render inline with namespaced heading id", html)
	}
	if !strings.Contains(html, `<a href="#embed-1-section-title">Self</a>`) {
		t.Fatalf("HTML = %q, want embedded self-link to target namespaced heading id", html)
	}
	if !strings.Contains(html, `<a href="#embed-1-nested">Nested</a>`) {
		t.Fatalf("HTML = %q, want embedded nested heading link to target namespaced heading id", html)
	}
	if strings.Count(html, `<h2 id="section-title">Section Title</h2>`) != 1 {
		t.Fatalf("HTML = %q, want original section heading rendered once with its unprefixed id", html)
	}
	if got := collector.Diagnostics(); len(got) != 0 {
		t.Fatalf("collector.Diagnostics() = %#v, want no diagnostics", got)
	}
}

func TestNewMarkdownWarnsOnUnresolvedEmbeds(t *testing.T) {
	t.Parallel()

	current := &model.Note{
		Slug:    "notes/current",
		RelPath: "notes/current.md",
		Embeds: []model.EmbedRef{
			{Target: "Missing Note", Line: 1},
			{Target: "missing.png", IsImage: true, Line: 3},
		},
	}
	idx := &model.VaultIndex{
		Notes: map[string]*model.Note{
			current.RelPath: current,
		},
		NoteBySlug: map[string]*model.Note{
			current.Slug: current,
		},
		NoteByName: map[string][]*model.Note{
			"current": {current},
		},
		AliasByName: map[string][]*model.Note{},
		Assets:      map[string]*model.Asset{},
	}
	collector := diag.NewCollector()
	md, _ := NewMarkdown(idx, current, nil, collector)

	var buf bytes.Buffer
	if err := md.Convert([]byte("![[Missing Note]]\n\n![[missing.png]]\n"), &buf); err != nil {
		t.Fatalf("Convert() error = %v", err)
	}

	html := buf.String()
	for _, want := range []string{"Missing Note", "missing.png"} {
		if !strings.Contains(html, want) {
			t.Fatalf("HTML = %q, want unresolved embed fallback text %q", html, want)
		}
	}

	want := []diag.Diagnostic{
		{
			Severity: diag.SeverityWarning,
			Kind:     diag.KindDeadLink,
			Location: diag.Location{Path: current.RelPath, Line: 1},
			Message:  `note embed "Missing Note" could not be resolved; rendering as plain text`,
		},
		{
			Severity: diag.SeverityWarning,
			Kind:     diag.KindUnresolvedAsset,
			Location: diag.Location{Path: current.RelPath, Line: 3},
			Message:  `image embed "missing.png" could not be resolved to a vault asset; rendering as plain text`,
		},
	}
	if got := collector.Diagnostics(); !reflect.DeepEqual(got, want) {
		t.Fatalf("collector.Diagnostics() = %#v, want %#v", got, want)
	}
}

func TestNewMarkdownSectionEmbedDiagnosticsUseRenderedOccurrenceLine(t *testing.T) {
	t.Parallel()

	rawContent := "# Top\n\n![[Missing]]\n\n## Section Title\n\n![[Missing]]\n"
	firstOffset := strings.Index(rawContent, "![[Missing]]")
	secondOffset := strings.LastIndex(rawContent, "![[Missing]]")
	current := &model.Note{
		Slug:    "notes/current",
		RelPath: "notes/current.md",
		Embeds:  []model.EmbedRef{{Target: "Guide", Fragment: "Section Title", Line: 1}},
	}
	target := &model.Note{
		Slug:       "guides/guide",
		RelPath:    "guides/guide.md",
		RawContent: []byte(rawContent),
		Headings: []model.Heading{
			{Level: 2, Text: "Section Title", ID: "section-title"},
		},
		HeadingSections: map[string]model.SectionRange{
			"section-title": sectionRangeForTest(t, rawContent, "## Section Title", ""),
		},
		Embeds: []model.EmbedRef{
			{Target: "Missing", Line: 3, Offset: firstOffset},
			{Target: "Missing", Line: 7, Offset: secondOffset},
		},
	}
	idx := &model.VaultIndex{
		Notes: map[string]*model.Note{
			current.RelPath: current,
			target.RelPath:  target,
		},
		NoteBySlug: map[string]*model.Note{
			current.Slug: current,
			target.Slug:  target,
		},
		NoteByName: map[string][]*model.Note{
			"current": {current},
			"guide":   {target},
		},
		AliasByName: map[string][]*model.Note{},
	}
	collector := diag.NewCollector()
	md, _ := NewMarkdown(idx, current, nil, collector)

	var buf bytes.Buffer
	if err := md.Convert([]byte("![[Guide#Section Title]]\n"), &buf); err != nil {
		t.Fatalf("Convert() error = %v", err)
	}

	html := buf.String()
	if strings.Count(html, "Missing") != 1 {
		t.Fatalf("HTML = %q, want only the rendered section occurrence to fall back to plain text", html)
	}

	got := collector.Diagnostics()
	if len(got) != 1 {
		t.Fatalf("collector.Diagnostics() = %#v, want 1 diagnostic", got)
	}
	if got[0].Kind != diag.KindDeadLink || got[0].Location.Path != target.RelPath || got[0].Location.Line != 7 {
		t.Fatalf("collector.Diagnostics()[0] = %#v, want deadlink at rendered section occurrence", got[0])
	}
	if got[0].Message != `note embed "Missing" could not be resolved; rendering as plain text` {
		t.Fatalf("collector.Diagnostics()[0] = %#v, want repeated embed warning message", got[0])
	}
}

func TestNewMarkdownSectionEmbedUnsupportedFenceDiagnosticsUseOriginalSourceLine(t *testing.T) {
	t.Parallel()

	rawContent := "# Top\n\nIntro.\n\n## Section Title\n\nBefore.\n\n```dataview\nLIST FROM [[]]\n```\n\n## Later\n\nSkip.\n"
	current := &model.Note{
		Slug:    "notes/current",
		RelPath: "notes/current.md",
		Embeds:  []model.EmbedRef{{Target: "Guide", Fragment: "Section Title", Line: 1}},
	}
	target := &model.Note{
		Slug:          "guides/guide",
		RelPath:       "guides/guide.md",
		RawContent:    []byte(rawContent),
		BodyStartLine: 10,
		Headings: []model.Heading{
			{Level: 2, Text: "Section Title", ID: "section-title"},
			{Level: 2, Text: "Later", ID: "later"},
		},
		HeadingSections: map[string]model.SectionRange{
			"section-title": sectionRangeForTest(t, rawContent, "## Section Title", "## Later"),
			"later":         sectionRangeForTest(t, rawContent, "## Later", ""),
		},
	}
	idx := &model.VaultIndex{
		Notes: map[string]*model.Note{
			current.RelPath: current,
			target.RelPath:  target,
		},
		NoteBySlug: map[string]*model.Note{
			current.Slug: current,
			target.Slug:  target,
		},
		NoteByName: map[string][]*model.Note{
			"current": {current},
			"guide":   {target},
		},
		AliasByName: map[string][]*model.Note{},
	}
	collector := diag.NewCollector()
	md, _ := NewMarkdown(idx, current, nil, collector)

	var buf bytes.Buffer
	if err := md.Convert([]byte("![[Guide#Section Title]]\n"), &buf); err != nil {
		t.Fatalf("Convert() error = %v", err)
	}

	html := buf.String()
	if !strings.Contains(html, `class="unsupported-syntax unsupported-dataview"`) {
		t.Fatalf("HTML = %q, want Dataview fence degradation inside section embed", html)
	}

	want := []diag.Diagnostic{{
		Severity: diag.SeverityWarning,
		Kind:     diag.KindUnsupportedSyntax,
		Location: diag.Location{Path: target.RelPath, Line: 19},
		Message:  `dataview fenced code block is not supported; rendering as plain preformatted text`,
	}}
	if got := collector.Diagnostics(); !reflect.DeepEqual(got, want) {
		t.Fatalf("collector.Diagnostics() = %#v, want %#v", got, want)
	}
}

func TestNewMarkdownSectionEmbedsScopeRenderedMetadata(t *testing.T) {
	t.Parallel()

	rawContent := "# Intro\n\n[[Target Page#Other|Outside Link]]\n\n## Section Title\n\n[[#Section Title|Self]]\n\n[[#Included|Keep]]\n\n[[#Excluded|Drop]]\n\n[[Target Page|Section Link]]\n\n### Included\n\nBody\n\n## Excluded\n\nOutside\n"
	host := &model.Note{
		Slug:    "posts/host",
		RelPath: "notes/host.md",
		Embeds:  []model.EmbedRef{{Target: "Guide", Fragment: "Section Title", Line: 1}},
	}
	guide := &model.Note{
		Slug:       "guides/guide",
		RelPath:    "guides/guide.md",
		RawContent: []byte(rawContent),
		Headings: []model.Heading{
			{Level: 1, Text: "Intro", ID: "intro"},
			{Level: 2, Text: "Section Title", ID: "section-title"},
			{Level: 3, Text: "Included", ID: "included"},
			{Level: 2, Text: "Excluded", ID: "excluded"},
		},
		HeadingSections: map[string]model.SectionRange{
			"intro":         sectionRangeForTest(t, rawContent, "# Intro", "## Section Title"),
			"section-title": sectionRangeForTest(t, rawContent, "## Section Title", "## Excluded"),
			"included":      sectionRangeForTest(t, rawContent, "### Included", "## Excluded"),
			"excluded":      sectionRangeForTest(t, rawContent, "## Excluded", ""),
		},
		OutLinks: []model.LinkRef{
			{RawTarget: "Target Page#Other", Display: "Outside Link", Fragment: "Other", Line: 3, Offset: strings.Index(rawContent, "[[Target Page#Other|Outside Link]]")},
			{RawTarget: "#Section Title", Display: "Self", Fragment: "Section Title", Line: 7, Offset: strings.Index(rawContent, "[[#Section Title|Self]]")},
			{RawTarget: "#Included", Display: "Keep", Fragment: "Included", Line: 9, Offset: strings.Index(rawContent, "[[#Included|Keep]]")},
			{RawTarget: "#Excluded", Display: "Drop", Fragment: "Excluded", Line: 11, Offset: strings.Index(rawContent, "[[#Excluded|Drop]]")},
			{RawTarget: "Target Page", Display: "Section Link", Line: 13, Offset: strings.Index(rawContent, "[[Target Page|Section Link]]")},
		},
	}
	target := &model.Note{Slug: "reference/target-page", RelPath: "reference/target page.md", Headings: []model.Heading{{Level: 2, Text: "Other", ID: "other"}}}
	idx := &model.VaultIndex{
		Notes: map[string]*model.Note{
			host.RelPath:   host,
			guide.RelPath:  guide,
			target.RelPath: target,
		},
		NoteBySlug: map[string]*model.Note{
			host.Slug:   host,
			guide.Slug:  guide,
			target.Slug: target,
		},
		NoteByName: map[string][]*model.Note{
			"host":        {host},
			"guide":       {guide},
			"target page": {target},
		},
		AliasByName: map[string][]*model.Note{},
	}
	collector := diag.NewCollector()
	md, renderResult := NewMarkdown(idx, host, nil, collector)

	var buf bytes.Buffer
	if err := md.Convert([]byte("![[Guide#Section Title]]\n"), &buf); err != nil {
		t.Fatalf("Convert() error = %v", err)
	}

	html := buf.String()
	if !strings.Contains(html, `<a href="#embed-1-section-title">Self</a>`) {
		t.Fatalf("HTML = %q, want section self-link rewritten to namespaced heading id", html)
	}
	if !strings.Contains(html, `<a href="#embed-1-included">Keep</a>`) {
		t.Fatalf("HTML = %q, want in-section nested heading link rewritten to namespaced heading id", html)
	}
	if !strings.Contains(html, `<a href="../../reference/target-page/">Section Link</a>`) {
		t.Fatalf("HTML = %q, want in-section outlink rendered", html)
	}
	if !strings.Contains(html, "Drop") {
		t.Fatalf("HTML = %q, want missing section-local fragment label preserved as plain text", html)
	}
	if strings.Contains(html, `<a href="#embed-1-excluded">Drop</a>`) {
		t.Fatalf("HTML = %q, want out-of-section fragment link to remain unlinked", html)
	}

	gotOutLinks := renderResult.OutLinks()
	if len(gotOutLinks) != 5 {
		t.Fatalf("len(renderResult.OutLinks()) = %d, want 5 outlinks including the direct embed target", len(gotOutLinks))
	}
	wantRawTargets := []string{"#Section Title", "#Included", "#Excluded", "Target Page"}
	wantResolved := []string{host.RelPath, host.RelPath, "", target.RelPath}
	for i := range wantRawTargets {
		if gotOutLinks[i].RawTarget != wantRawTargets[i] {
			t.Fatalf("renderResult.OutLinks()[%d].RawTarget = %q, want %q", i, gotOutLinks[i].RawTarget, wantRawTargets[i])
		}
		if gotOutLinks[i].ResolvedRelPath != wantResolved[i] {
			t.Fatalf("renderResult.OutLinks()[%d].ResolvedRelPath = %q, want %q", i, gotOutLinks[i].ResolvedRelPath, wantResolved[i])
		}
	}
	if gotOutLinks[4].ResolvedRelPath != guide.RelPath {
		t.Fatalf("renderResult.OutLinks()[4].ResolvedRelPath = %q, want %q", gotOutLinks[4].ResolvedRelPath, guide.RelPath)
	}

	wantDiagnostics := []diag.Diagnostic{{
		Severity: diag.SeverityWarning,
		Kind:     diag.KindDeadLink,
		Location: diag.Location{Path: guide.RelPath, Line: 11},
		Message:  `wikilink "#Excluded" points to missing heading "Excluded" in "guides/guide.md"`,
	}}
	if got := collector.Diagnostics(); !reflect.DeepEqual(got, wantDiagnostics) {
		t.Fatalf("collector.Diagnostics() = %#v, want %#v", got, wantDiagnostics)
	}
	for i := range guide.OutLinks {
		if guide.OutLinks[i].ResolvedRelPath != "" {
			t.Fatalf("guide.OutLinks[%d].ResolvedRelPath = %q, want source note to remain unchanged", i, guide.OutLinks[i].ResolvedRelPath)
		}
	}
}

func TestNewMarkdownSectionEmbedsPreserveDuplicateHeadingIDs(t *testing.T) {
	t.Parallel()

	rawContent := "# Top\n\n## Duplicate\n\nOutside.\n\n## Section Title\n\n[[#Duplicate|Jump]]\n\n### Duplicate\n\nInside.\n"
	host := &model.Note{
		Slug:    "posts/host",
		RelPath: "notes/host.md",
		Embeds:  []model.EmbedRef{{Target: "Guide", Fragment: "Section Title", Line: 1}},
	}
	guide := &model.Note{
		Slug:       "guides/guide",
		RelPath:    "guides/guide.md",
		RawContent: []byte(rawContent),
		Headings: []model.Heading{
			{Level: 1, Text: "Top", ID: "top"},
			{Level: 2, Text: "Duplicate", ID: "duplicate"},
			{Level: 2, Text: "Section Title", ID: "section-title"},
			{Level: 3, Text: "Duplicate", ID: "duplicate-1"},
		},
		HeadingSections: map[string]model.SectionRange{
			"top":           sectionRangeForTest(t, rawContent, "# Top", "## Duplicate"),
			"duplicate":     sectionRangeForTest(t, rawContent, "## Duplicate", "## Section Title"),
			"section-title": sectionRangeForTest(t, rawContent, "## Section Title", ""),
			"duplicate-1":   sectionRangeForTest(t, rawContent, "### Duplicate", ""),
		},
		OutLinks: []model.LinkRef{{
			RawTarget: "#Duplicate",
			Display:   "Jump",
			Fragment:  "Duplicate",
			Line:      9,
			Offset:    strings.Index(rawContent, "[[#Duplicate|Jump]]"),
		}},
	}
	idx := &model.VaultIndex{
		Notes: map[string]*model.Note{
			host.RelPath:  host,
			guide.RelPath: guide,
		},
		NoteBySlug: map[string]*model.Note{
			host.Slug:  host,
			guide.Slug: guide,
		},
		NoteByName: map[string][]*model.Note{
			"host":  {host},
			"guide": {guide},
		},
		AliasByName: map[string][]*model.Note{},
	}
	collector := diag.NewCollector()
	md, _ := NewMarkdown(idx, host, nil, collector)

	var buf bytes.Buffer
	if err := md.Convert([]byte("![[Guide#Section Title]]\n"), &buf); err != nil {
		t.Fatalf("Convert() error = %v", err)
	}

	html := buf.String()
	if !strings.Contains(html, `<a href="#embed-1-duplicate-1">Jump</a>`) {
		t.Fatalf("HTML = %q, want embedded duplicate-heading link to target the pass-1 heading id", html)
	}
	if !strings.Contains(html, `<h3 id="embed-1-duplicate-1">Duplicate</h3>`) {
		t.Fatalf("HTML = %q, want embedded duplicate heading to keep its pass-1 deduplicated id", html)
	}
	if strings.Contains(html, `<h3 id="embed-1-duplicate">Duplicate</h3>`) {
		t.Fatalf("HTML = %q, want embedded duplicate heading id to stay aligned with pass-1 metadata", html)
	}
	if got := collector.Diagnostics(); len(got) != 0 {
		t.Fatalf("collector.Diagnostics() = %#v, want no diagnostics", got)
	}
}

func TestNewMarkdownLimitsEmbedDepth(t *testing.T) {
	t.Parallel()

	idx := &model.VaultIndex{
		Notes:       map[string]*model.Note{},
		NoteBySlug:  map[string]*model.Note{},
		NoteByName:  map[string][]*model.Note{},
		AliasByName: map[string][]*model.Note{},
	}

	for i := 0; i <= 11; i++ {
		name := fmt.Sprintf("Note%d", i)
		relPath := fmt.Sprintf("notes/%s.md", strings.ToLower(name))
		slug := fmt.Sprintf("notes/%s", strings.ToLower(name))
		rawContent := []byte("Terminal content\n")
		var embeds []model.EmbedRef
		if i < 11 {
			nextName := fmt.Sprintf("Note%d", i+1)
			rawContent = []byte(fmt.Sprintf("![[%s]]\n", nextName))
			embeds = []model.EmbedRef{{Target: nextName, Line: 1}}
		}

		note := &model.Note{
			Slug:       slug,
			RelPath:    relPath,
			RawContent: rawContent,
			Embeds:     embeds,
		}
		idx.Notes[relPath] = note
		idx.NoteBySlug[slug] = note
		idx.NoteByName[strings.ToLower(name)] = []*model.Note{note}
	}

	current := idx.Notes["notes/note0.md"]
	collector := diag.NewCollector()
	md, _ := NewMarkdown(idx, current, nil, collector)

	var buf bytes.Buffer
	if err := md.Convert(current.RawContent, &buf); err != nil {
		t.Fatalf("Convert() error = %v", err)
	}

	html := buf.String()
	if !strings.Contains(html, "Note11") {
		t.Fatalf("HTML = %q, want depth-limited embed to fall back to plain target text", html)
	}
	if strings.Contains(html, "Terminal content") {
		t.Fatalf("HTML = %q, want max-depth guard to stop before rendering deepest note", html)
	}

	gotDiagnostics := collector.Diagnostics()
	if len(gotDiagnostics) != 1 {
		t.Fatalf("collector.Diagnostics() = %#v, want 1 max-depth warning", gotDiagnostics)
	}
	if gotDiagnostics[0].Kind != diag.KindUnsupportedSyntax || gotDiagnostics[0].Location.Path != "notes/note10.md" || gotDiagnostics[0].Location.Line != 1 {
		t.Fatalf("collector.Diagnostics()[0] = %#v, want max-depth warning at the blocked embed site", gotDiagnostics[0])
	}
	if gotDiagnostics[0].Message != `embed "Note11" maximum embed depth of 10 exceeded; rendering as plain text` {
		t.Fatalf("collector.Diagnostics()[0] = %#v, want exact max-depth warning", gotDiagnostics[0])
	}
}

func collectHeadings(t *testing.T, doc gast.Node) []*gast.Heading {
	t.Helper()

	var headings []*gast.Heading
	err := gast.Walk(doc, func(node gast.Node, entering bool) (gast.WalkStatus, error) {
		if !entering {
			return gast.WalkContinue, nil
		}

		heading, ok := node.(*gast.Heading)
		if ok {
			headings = append(headings, heading)
		}

		return gast.WalkContinue, nil
	})
	if err != nil {
		t.Fatalf("Walk() error = %v", err)
	}

	return headings
}

func sectionRangeForTest(t *testing.T, source string, startMarker string, endMarker string) model.SectionRange {
	start := strings.Index(source, startMarker)
	if start < 0 {
		t.Fatalf("source missing start marker %q", startMarker)
	}

	end := len(source)
	if endMarker != "" {
		end = strings.Index(source, endMarker)
		if end < 0 {
			t.Fatalf("source missing end marker %q", endMarker)
		}
	}

	return model.SectionRange{StartOffset: start, EndOffset: end}
}

type recordingAssetSink struct {
	paths      map[string]string
	registered []string
}

func TestNewMarkdownRewritesStandardImagesFromDecodedAndAttachmentFolderAssets(t *testing.T) {
	t.Parallel()

	sink := &recordingAssetSink{
		paths: map[string]string{
			"images/My Chart.png":       "assets/My Chart.123.png",
			"assets/uploads/poster.png": "assets/poster.456.png",
		},
	}
	note := &model.Note{Slug: "posts/guide", RelPath: "notes/guide.md"}
	idx := &model.VaultIndex{
		AttachmentFolderPath: "assets/uploads",
		Assets: map[string]*model.Asset{
			"images/My Chart.png":       {SrcPath: "images/My Chart.png"},
			"assets/uploads/poster.png": {SrcPath: "assets/uploads/poster.png"},
		},
	}
	md, _ := NewMarkdown(idx, note, sink, diag.NewCollector())

	var buf bytes.Buffer
	source := []byte("![Chart](../images/My%20Chart.png?raw=1#frag)\n\n![Poster](poster.png)\n")
	if err := md.Convert(source, &buf); err != nil {
		t.Fatalf("Convert() error = %v", err)
	}

	html := buf.String()
	if !strings.Contains(html, `<img src="../../assets/My%20Chart.123.png?raw=1#frag" alt="Chart">`) {
		t.Fatalf("HTML = %q, want decoded Markdown image destination rewritten to indexed asset path", html)
	}
	if !strings.Contains(html, `<img src="../../assets/poster.456.png" alt="Poster" loading="lazy">`) {
		t.Fatalf("HTML = %q, want attachment-folder Markdown image destination rewritten to indexed asset path", html)
	}

	wantRegistered := []string{"images/My Chart.png", "assets/uploads/poster.png"}
	if !reflect.DeepEqual(sink.registered, wantRegistered) {
		t.Fatalf("registered = %#v, want %#v", sink.registered, wantRegistered)
	}
}

func (s *recordingAssetSink) Register(vaultRelPath string) string {
	s.registered = append(s.registered, vaultRelPath)
	if s.paths == nil {
		return ""
	}
	return s.paths[vaultRelPath]
}
