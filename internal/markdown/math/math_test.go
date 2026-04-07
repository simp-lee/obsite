package math

import (
	"bytes"
	"testing"

	"github.com/simp-lee/obsite/internal/model"
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
			name:  "inline math",
			input: "Inline $a < b$ test.\n",
			want:  "<p>Inline <span class=\"math math-inline\">$a &lt; b$</span> test.</p>\n",
		},
		{
			name: "block math",
			input: "$$\n" +
				"\\frac{1}{2} < x\n" +
				"$$\n",
			want: "<div class=\"math math-display\">$$\n" +
				"\\frac{1}{2} &lt; x\n" +
				"$$</div>\n",
		},
		{
			name:  "ordinary dollar signs stay plain text",
			input: "Price is $5 and $10 today.\n",
			want:  "<p>Price is $5 and $10 today.</p>\n",
		},
		{
			name:  "same line display math",
			input: "$$E = mc^2$$\n",
			want:  "<div class=\"math math-display\">$$E = mc^2$$</div>\n",
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

func TestParseMarksDocumentAndPreservesRawLatex(t *testing.T) {
	t.Parallel()

	md := goldmark.New(goldmark.WithExtensions(Extension))
	doc := md.Parser().Parse(text.NewReader([]byte("inline $\\alpha < \\beta$\n\n$$\n\\sum_{i=1}^{n} i\n$$\n"))).(*gast.Document)

	if !HasMath(doc) {
		t.Fatal("expected HasMath() to report true")
	}

	note := model.Note{}
	if got := MarkNoteHasMath(&note, doc); !got {
		t.Fatal("expected MarkNoteHasMath() to return true")
	}
	if !note.HasMath {
		t.Fatal("expected note.HasMath to be set")
	}
	if got := MarkNoteHasMath(nil, doc); !got {
		t.Fatal("expected MarkNoteHasMath(nil, doc) to report true")
	}

	metaValue, ok := doc.Meta()[DocumentMetaHasMath]
	if !ok {
		t.Fatalf("expected document meta %q to be set", DocumentMetaHasMath)
	}
	flag, ok := metaValue.(bool)
	if !ok || !flag {
		t.Fatalf("document meta %q = %#v, want true", DocumentMetaHasMath, metaValue)
	}

	var inline *InlineMath
	var display *DisplayMath
	err := gast.Walk(doc, func(node gast.Node, entering bool) (gast.WalkStatus, error) {
		if !entering {
			return gast.WalkContinue, nil
		}

		switch current := node.(type) {
		case *InlineMath:
			inline = current
		case *DisplayMath:
			display = current
		}
		return gast.WalkContinue, nil
	})
	if err != nil {
		t.Fatalf("Walk() error = %v", err)
	}

	if inline == nil {
		t.Fatal("expected inline math node")
	}
	if got := string(inline.Literal); got != "\\alpha < \\beta" {
		t.Fatalf("inline literal = %q, want %q", got, "\\alpha < \\beta")
	}

	if display == nil {
		t.Fatal("expected display math node")
	}
	if got := string(display.Literal); got != "\n\\sum_{i=1}^{n} i\n" {
		t.Fatalf("display literal = %q, want %q", got, "\n\\sum_{i=1}^{n} i\n")
	}
}

func TestMarkNoteHasMathClearsFlagWhenASTContainsNoMath(t *testing.T) {
	t.Parallel()

	md := goldmark.New(goldmark.WithExtensions(Extension))
	doc := md.Parser().Parse(text.NewReader([]byte("plain text only\n"))).(*gast.Document)

	note := model.Note{HasMath: true}
	if got := MarkNoteHasMath(&note, doc); got {
		t.Fatal("expected MarkNoteHasMath() to return false")
	}
	if note.HasMath {
		t.Fatal("expected note.HasMath to be cleared")
	}
	if _, ok := doc.Meta()[DocumentMetaHasMath]; ok {
		t.Fatalf("did not expect document meta %q on a non-math document", DocumentMetaHasMath)
	}
}
