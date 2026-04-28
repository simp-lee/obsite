package slug

import (
	"errors"
	"reflect"
	"testing"
)

func stringPtr(value string) *string {
	return &value
}

func frontmatterValue(value *string) string {
	if value == nil {
		return "<nil>"
	}
	return *value
}

func TestGenerate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		frontmatterSlug *string
		relPath         string
		want            string
		wantErr         error
	}{
		{
			name:            "frontmatter slug takes precedence",
			frontmatterSlug: stringPtr("Custom  Slug__2026"),
			relPath:         "notes/Fallback Name.md",
			want:            "custom-slug-2026",
		},
		{
			name:    "filename fallback preserves unicode letters and digits",
			relPath: "notes/中文 笔记 2026.md",
			want:    "中文-笔记-2026",
		},
		{
			name:            "special and control characters are removed",
			frontmatterSlug: stringPtr("Hello!? #World[]@!$&'()*+,;=\x00_Name"),
			relPath:         "notes/ignored.md",
			want:            "hello-world-name",
		},
		{
			name:            "frontmatter slug normalizes decomposed unicode",
			frontmatterSlug: stringPtr("Cafe\u0301 Notes"),
			relPath:         "notes/ignored.md",
			want:            "café-notes",
		},
		{
			name:            "frontmatter slug normalizes composed unicode",
			frontmatterSlug: stringPtr("Café Notes"),
			relPath:         "notes/ignored.md",
			want:            "café-notes",
		},
		{
			name:            "frontmatter slug renormalizes after lowercasing uppercase decomposed unicode",
			frontmatterSlug: stringPtr("J\u030c Notes"),
			relPath:         "notes/ignored.md",
			want:            "ǰ-notes",
		},
		{
			name:            "frontmatter slug matches lowercase precomposed unicode",
			frontmatterSlug: stringPtr("ǰ Notes"),
			relPath:         "notes/ignored.md",
			want:            "ǰ-notes",
		},
		{
			name:            "frontmatter slug preserves combining marks required by hindi",
			frontmatterSlug: stringPtr("हिंदी नोट्स"),
			relPath:         "notes/ignored.md",
			want:            "हिंदी-नोट्स",
		},
		{
			name:            "frontmatter slug rejects isolated combining marks",
			frontmatterSlug: stringPtr("िं"),
			relPath:         "notes/ignored.md",
			wantErr:         ErrInvalidFrontmatterSlug,
		},
		{
			name:    "filename fallback normalizes decomposed unicode",
			relPath: "notes/Cafe\u0301 Notes.md",
			want:    "café-notes",
		},
		{
			name:    "filename fallback normalizes composed unicode",
			relPath: "notes/Café Notes.md",
			want:    "café-notes",
		},
		{
			name:    "filename fallback renormalizes after lowercasing uppercase decomposed unicode",
			relPath: "notes/J\u030c Notes.md",
			want:    "ǰ-notes",
		},
		{
			name:    "filename fallback matches lowercase precomposed unicode",
			relPath: "notes/ǰ Notes.md",
			want:    "ǰ-notes",
		},
		{
			name:            "invalid frontmatter slug is rejected",
			frontmatterSlug: stringPtr("!!!???[]{}"),
			relPath:         "notes/Fallback Name.md",
			wantErr:         ErrInvalidFrontmatterSlug,
		},
		{
			name:            "empty frontmatter slug is rejected",
			frontmatterSlug: stringPtr(""),
			relPath:         "notes/Fallback Name.md",
			wantErr:         ErrInvalidFrontmatterSlug,
		},
		{
			name:    "repeated separators collapse and trim",
			relPath: "notes/__Hello---World__.md",
			want:    "hello-world",
		},
		{
			name:    "filename with only underscores is rejected",
			relPath: "notes/__.md",
			wantErr: ErrInvalidFileSlug,
		},
		{
			name:    "filename with only hyphens is rejected",
			relPath: "notes/---.md",
			wantErr: ErrInvalidFileSlug,
		},
		{
			name:    "filename with only control characters is rejected",
			relPath: "notes/\x00\x1f\x7f.md",
			wantErr: ErrInvalidFileSlug,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := Generate(tt.frontmatterSlug, tt.relPath)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Generate(%q, %q) error = %v, want %v", frontmatterValue(tt.frontmatterSlug), tt.relPath, err, tt.wantErr)
			}
			if tt.wantErr != nil {
				return
			}

			if got != tt.want {
				t.Fatalf("Generate(%q, %q) = %q, want %q", frontmatterValue(tt.frontmatterSlug), tt.relPath, got, tt.want)
			}
		})
	}
}

func TestDetectConflicts(t *testing.T) {
	t.Parallel()

	candidates := []Candidate{
		{Source: "notes/zeta.md", Slug: "hello-world"},
		{Source: "notes/alpha.md", Slug: "你好"},
		{Source: "notes/beta.md", Slug: "hello-world"},
		{Source: "notes/gamma.md", Slug: "你好"},
		{Source: "notes/unique.md", Slug: "unique"},
		{Source: "notes/beta.md", Slug: "hello-world"},
	}

	gotConflicts, gotInvalid := DetectConflicts(candidates)
	wantConflicts := []Conflict{
		{Slug: "hello-world", Sources: []string{"notes/beta.md", "notes/zeta.md"}},
		{Slug: "你好", Sources: []string{"notes/alpha.md", "notes/gamma.md"}},
	}

	if !reflect.DeepEqual(gotConflicts, wantConflicts) {
		t.Fatalf("DetectConflicts() conflicts = %#v, want %#v", gotConflicts, wantConflicts)
	}
	if gotInvalid != nil {
		t.Fatalf("DetectConflicts() invalid = %#v, want nil", gotInvalid)
	}
}

func TestDetectConflictsReportsInvalidEmptySlugs(t *testing.T) {
	t.Parallel()

	candidates := []Candidate{
		{Source: "notes/valid.md", Slug: "valid"},
		{Source: "notes/blank.md", Slug: ""},
		{Source: "notes/space.md", Slug: "   "},
	}

	gotConflicts, gotInvalid := DetectConflicts(candidates)
	wantInvalid := []InvalidSlug{
		{Source: "notes/blank.md", Slug: ""},
		{Source: "notes/space.md", Slug: "   "},
	}

	if gotConflicts != nil {
		t.Fatalf("DetectConflicts() conflicts = %#v, want nil", gotConflicts)
	}
	if !reflect.DeepEqual(gotInvalid, wantInvalid) {
		t.Fatalf("DetectConflicts() invalid = %#v, want %#v", gotInvalid, wantInvalid)
	}
}

func TestDetectConflictsCanonicalizesUnicodeCase(t *testing.T) {
	t.Parallel()

	candidates := []Candidate{
		{Source: "notes/decomposed.md", Slug: "J\u030c"},
		{Source: "notes/precomposed.md", Slug: "ǰ"},
	}

	gotConflicts, gotInvalid := DetectConflicts(candidates)
	wantConflicts := []Conflict{{
		Slug:    "ǰ",
		Sources: []string{"notes/decomposed.md", "notes/precomposed.md"},
	}}

	if !reflect.DeepEqual(gotConflicts, wantConflicts) {
		t.Fatalf("DetectConflicts() conflicts = %#v, want %#v", gotConflicts, wantConflicts)
	}
	if gotInvalid != nil {
		t.Fatalf("DetectConflicts() invalid = %#v, want nil", gotInvalid)
	}
}
