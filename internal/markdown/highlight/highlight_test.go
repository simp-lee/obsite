package highlight

import (
	"bytes"
	"testing"

	"github.com/yuin/goldmark"
	gast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

func TestRender(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "basic highlight",
			input: "==text==\n",
			want:  "<p><mark>text</mark></p>\n",
		},
		{
			name:  "ordinary equals stay plain text",
			input: "1 == 2 and a==b\n",
			want:  "<p>1 == 2 and a==b</p>\n",
		},
		{
			name:  "empty content is not highlighted",
			input: "====\n",
			want:  "<p>====</p>\n",
		},
		{
			name:  "mixed with surrounding text",
			input: "alpha ==beta== gamma\n",
			want:  "<p>alpha <mark>beta</mark> gamma</p>\n",
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

func TestParserBuildsHighlightNode(t *testing.T) {
	t.Parallel()

	md := goldmark.New(goldmark.WithExtensions(Extension))
	doc := md.Parser().Parse(text.NewReader([]byte("before ==marked== after\n")))

	var found *Mark
	err := gast.Walk(doc, func(node gast.Node, entering bool) (gast.WalkStatus, error) {
		if !entering {
			return gast.WalkContinue, nil
		}

		mark, ok := node.(*Mark)
		if !ok {
			return gast.WalkContinue, nil
		}

		found = mark
		return gast.WalkStop, nil
	})
	if err != nil {
		t.Fatalf("Walk() error = %v", err)
	}
	if found == nil {
		t.Fatal("expected parser to build a highlight node")
	}
	if found.Kind() != KindMark {
		t.Fatalf("Kind() = %v, want %v", found.Kind(), KindMark)
	}
	if found.FirstChild() == nil {
		t.Fatal("expected highlight node to contain inline children")
	}
}
