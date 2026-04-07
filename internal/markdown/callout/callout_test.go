package callout

import (
	"bytes"
	"testing"

	"github.com/yuin/goldmark"
)

func TestRender(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name: "basic callout",
			input: "> [!note] Note title\n" +
				"> Body text\n",
			want: "<div class=\"callout callout-note\">\n" +
				"<div class=\"callout-title\">Note title</div>\n" +
				"<p>Body text</p>\n" +
				"</div>\n",
		},
		{
			name: "different type",
			input: "> [!warning] Heads up\n" +
				"> Pay attention.\n",
			want: "<div class=\"callout callout-warning\">\n" +
				"<div class=\"callout-title\">Heads up</div>\n" +
				"<p>Pay attention.</p>\n" +
				"</div>\n",
		},
		{
			name: "collapsed callout uses details",
			input: "> [!tip]- Fold me\n" +
				"> Hidden text\n",
			want: "<details class=\"callout callout-tip\">\n" +
				"<summary class=\"callout-title\">Fold me</summary>\n" +
				"<p>Hidden text</p>\n" +
				"</details>\n",
		},
		{
			name: "open callout rewrites following paragraphs and lists into body",
			input: "> [!note]+ Expanded\n" +
				"> First paragraph\n" +
				">\n" +
				"> Second paragraph\n" +
				">\n" +
				"> - first item\n" +
				"> - second item\n",
			want: "<details class=\"callout callout-note\" open>\n" +
				"<summary class=\"callout-title\">Expanded</summary>\n" +
				"<p>First paragraph</p>\n" +
				"<p>Second paragraph</p>\n" +
				"<ul>\n" +
				"<li>first item</li>\n" +
				"<li>second item</li>\n" +
				"</ul>\n" +
				"</details>\n",
		},
		{
			name: "collapsed callout without title uses readable fallback summary",
			input: "> [!tip]-\n" +
				"> Hidden text\n",
			want: "<details class=\"callout callout-tip\">\n" +
				"<summary class=\"callout-title\">Tip</summary>\n" +
				"<p>Hidden text</p>\n" +
				"</details>\n",
		},
		{
			name: "non collapsible callout without title uses readable fallback title",
			input: "> [!tip]\n" +
				"> Visible text\n",
			want: "<div class=\"callout callout-tip\">\n" +
				"<div class=\"callout-title\">Tip</div>\n" +
				"<p>Visible text</p>\n" +
				"</div>\n",
		},
		{
			name: "nested callout",
			input: "> [!info] Outer\n" +
				"> Intro\n" +
				"> > [!warning] Inner\n" +
				"> > Deep\n",
			want: "<div class=\"callout callout-info\">\n" +
				"<div class=\"callout-title\">Outer</div>\n" +
				"<p>Intro</p>\n" +
				"<div class=\"callout callout-warning\">\n" +
				"<div class=\"callout-title\">Inner</div>\n" +
				"<p>Deep</p>\n" +
				"</div>\n" +
				"</div>\n",
		},
		{
			name: "plain nested blockquote stays inside callout body",
			input: "> [!info] Outer\n" +
				"> Intro\n" +
				">\n" +
				"> > quoted detail\n",
			want: "<div class=\"callout callout-info\">\n" +
				"<div class=\"callout-title\">Outer</div>\n" +
				"<p>Intro</p>\n" +
				"<blockquote>\n" +
				"<p>quoted detail</p>\n" +
				"</blockquote>\n" +
				"</div>\n",
		},
		{
			name: "blockquote with later marker stays blockquote",
			input: "> quoted\n" +
				">\n" +
				"> [!note] still quoted\n",
			want: "<blockquote>\n" +
				"<p>quoted</p>\n" +
				"<p>[!note] still quoted</p>\n" +
				"</blockquote>\n",
		},
		{
			name: "adjacent callouts stay separate around plain blockquote content",
			input: "> [!note] First\n" +
				"> Alpha\n" +
				"\n" +
				"> quoted\n" +
				">\n" +
				"> still quoted\n" +
				"\n" +
				"> [!tip] Second\n" +
				"> Beta\n",
			want: "<div class=\"callout callout-note\">\n" +
				"<div class=\"callout-title\">First</div>\n" +
				"<p>Alpha</p>\n" +
				"</div>\n" +
				"<blockquote>\n" +
				"<p>quoted</p>\n" +
				"<p>still quoted</p>\n" +
				"</blockquote>\n" +
				"<div class=\"callout callout-tip\">\n" +
				"<div class=\"callout-title\">Second</div>\n" +
				"<p>Beta</p>\n" +
				"</div>\n",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			md := goldmark.New(goldmark.WithExtensions(Extension))

			var buf bytes.Buffer
			if err := md.Convert([]byte(tt.input), &buf); err != nil {
				t.Fatalf("Convert() error = %v", err)
			}

			if got := buf.String(); got != tt.want {
				t.Fatalf("Convert() = %q, want %q", got, tt.want)
			}
		})
	}
}
