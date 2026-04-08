package vault

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/simp-lee/obsite/internal/model"
)

func TestParseFrontmatterParsesKnownFieldsAndPreservesExtra(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	writeVaultFile(t, vaultPath, "notes/post.md", `---
title: Launch Notes
description: First public post
date: 2026-04-05
tags:
  - go
  - notes
aliases: Launch
publish: true
slug: custom-launch
layout: journal
---
# Hello
`)

	scanResult, err := Scan(vaultPath)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	got, err := ParseFrontmatter(scanResult, model.SiteConfig{DefaultPublish: true})
	if err != nil {
		t.Fatalf("ParseFrontmatter() error = %v", err)
	}

	if len(got.PublicNotes) != 1 {
		t.Fatalf("len(PublicNotes) = %d, want 1", len(got.PublicNotes))
	}
	if len(got.Unpublished.Notes) != 0 {
		t.Fatalf("len(Unpublished.Notes) = %d, want 0", len(got.Unpublished.Notes))
	}

	note := got.PublicNotes[0]
	if note.RelPath != "notes/post.md" {
		t.Fatalf("RelPath = %q, want %q", note.RelPath, "notes/post.md")
	}
	if string(note.RawContent) != "# Hello\n" {
		t.Fatalf("RawContent = %q, want %q", string(note.RawContent), "# Hello\n")
	}
	if note.BodyStartLine != 13 {
		t.Fatalf("BodyStartLine = %d, want %d", note.BodyStartLine, 13)
	}
	if note.Frontmatter.Title != "Launch Notes" {
		t.Fatalf("Frontmatter.Title = %q, want %q", note.Frontmatter.Title, "Launch Notes")
	}
	if note.Frontmatter.Description != "First public post" {
		t.Fatalf("Frontmatter.Description = %q, want %q", note.Frontmatter.Description, "First public post")
	}

	wantDate := time.Date(2026, time.April, 5, 0, 0, 0, 0, time.UTC)
	if !note.Frontmatter.Date.Equal(wantDate) {
		t.Fatalf("Frontmatter.Date = %v, want %v", note.Frontmatter.Date, wantDate)
	}

	wantTags := []string{"go", "notes"}
	if !reflect.DeepEqual(note.Frontmatter.Tags, wantTags) {
		t.Fatalf("Frontmatter.Tags = %#v, want %#v", note.Frontmatter.Tags, wantTags)
	}
	if !reflect.DeepEqual(note.Tags, wantTags) {
		t.Fatalf("Tags = %#v, want %#v", note.Tags, wantTags)
	}

	wantAliases := []string{"Launch"}
	if !reflect.DeepEqual(note.Frontmatter.Aliases, wantAliases) {
		t.Fatalf("Frontmatter.Aliases = %#v, want %#v", note.Frontmatter.Aliases, wantAliases)
	}
	if !reflect.DeepEqual(note.Aliases, wantAliases) {
		t.Fatalf("Aliases = %#v, want %#v", note.Aliases, wantAliases)
	}

	if note.Frontmatter.Publish == nil || !*note.Frontmatter.Publish {
		t.Fatalf("Frontmatter.Publish = %v, want true", note.Frontmatter.Publish)
	}
	if note.Publish == nil || !*note.Publish {
		t.Fatalf("Publish = %v, want true", note.Publish)
	}
	if note.Frontmatter.Slug != "custom-launch" {
		t.Fatalf("Frontmatter.Slug = %q, want %q", note.Frontmatter.Slug, "custom-launch")
	}
	if gotExtra := note.Frontmatter.Extra["layout"]; gotExtra != "journal" {
		t.Fatalf("Frontmatter.Extra[layout] = %#v, want %q", gotExtra, "journal")
	}
}

func TestParseFrontmatterCapturesSourceFileLastModifiedInUTC(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	relPath := "notes/post.md"
	writeVaultFile(t, vaultPath, relPath, "---\ntitle: Launch Notes\n---\n# Hello\n")

	sourcePath := filepath.Join(vaultPath, filepath.FromSlash(relPath))
	rawLastModified := time.Date(2026, time.April, 5, 14, 15, 16, 987654321, time.FixedZone("UTC+8", 8*60*60))
	if err := os.Chtimes(sourcePath, rawLastModified, rawLastModified); err != nil {
		t.Fatalf("Chtimes(%q) error = %v", sourcePath, err)
	}

	scanResult, err := Scan(vaultPath)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	got, err := ParseFrontmatter(scanResult, model.SiteConfig{DefaultPublish: true})
	if err != nil {
		t.Fatalf("ParseFrontmatter() error = %v", err)
	}

	if len(got.PublicNotes) != 1 {
		t.Fatalf("len(PublicNotes) = %d, want 1", len(got.PublicNotes))
	}

	note := got.PublicNotes[0]
	if !note.Frontmatter.Updated.IsZero() {
		t.Fatalf("Frontmatter.Updated = %v, want zero time", note.Frontmatter.Updated)
	}
	wantLastModified := rawLastModified.UTC().Truncate(time.Second)
	if !note.LastModified.Equal(wantLastModified) {
		t.Fatalf("LastModified = %v, want %v", note.LastModified, wantLastModified)
	}
}

func TestParseFrontmatterParsesUpdatedFieldUsingSupportedDateFormats(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		frontUpdated string
		want         time.Time
	}{
		{
			name:         "iso8601 with timezone",
			frontUpdated: "2026-04-05T09:30:45+08:00",
			want:         time.Date(2026, time.April, 5, 1, 30, 45, 0, time.UTC),
		},
		{
			name:         "iso8601 minute precision with timezone offset",
			frontUpdated: "2026-04-05T09:30+08:00",
			want:         time.Date(2026, time.April, 5, 1, 30, 0, 0, time.UTC),
		},
		{
			name:         "iso8601 minute precision with z suffix",
			frontUpdated: "2026-04-05T09:30Z",
			want:         time.Date(2026, time.April, 5, 9, 30, 0, 0, time.UTC),
		},
		{
			name:         "common local datetime",
			frontUpdated: "2026-04-05 09:30:45",
			want:         time.Date(2026, time.April, 5, 9, 30, 45, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			vaultPath := t.TempDir()
			writeVaultFile(t, vaultPath, "notes/updated.md", "---\nupdated: "+tt.frontUpdated+"\npublish: true\n---\nbody\n")

			scanResult, err := Scan(vaultPath)
			if err != nil {
				t.Fatalf("Scan() error = %v", err)
			}

			got, err := ParseFrontmatter(scanResult, model.SiteConfig{DefaultPublish: true})
			if err != nil {
				t.Fatalf("ParseFrontmatter() error = %v", err)
			}

			if len(got.PublicNotes) != 1 {
				t.Fatalf("len(PublicNotes) = %d, want 1", len(got.PublicNotes))
			}

			note := got.PublicNotes[0]
			if !note.Frontmatter.Updated.Equal(tt.want) {
				t.Fatalf("Frontmatter.Updated = %v, want %v", note.Frontmatter.Updated, tt.want)
			}
		})
	}
}

func TestParseFrontmatterUpdatedOverridesSourceFileLastModified(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	relPath := "notes/post.md"
	writeVaultFile(t, vaultPath, relPath, "---\nupdated: 2026-04-07T09:30:45+08:00\npublish: true\n---\nbody\n")

	sourcePath := filepath.Join(vaultPath, filepath.FromSlash(relPath))
	rawLastModified := time.Date(2026, time.April, 5, 14, 15, 16, 0, time.UTC)
	if err := os.Chtimes(sourcePath, rawLastModified, rawLastModified); err != nil {
		t.Fatalf("Chtimes(%q) error = %v", sourcePath, err)
	}

	scanResult, err := Scan(vaultPath)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	got, err := ParseFrontmatter(scanResult, model.SiteConfig{DefaultPublish: true})
	if err != nil {
		t.Fatalf("ParseFrontmatter() error = %v", err)
	}

	if len(got.PublicNotes) != 1 {
		t.Fatalf("len(PublicNotes) = %d, want 1", len(got.PublicNotes))
	}

	note := got.PublicNotes[0]
	wantUpdated := time.Date(2026, time.April, 7, 1, 30, 45, 0, time.UTC)
	if !note.Frontmatter.Updated.Equal(wantUpdated) {
		t.Fatalf("Frontmatter.Updated = %v, want %v", note.Frontmatter.Updated, wantUpdated)
	}
	if !note.LastModified.Equal(wantUpdated) {
		t.Fatalf("LastModified = %v, want updated time %v", note.LastModified, wantUpdated)
	}
}

func TestParseFrontmatterDateRemainsIndependentFromLastModified(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	relPath := "notes/post.md"
	writeVaultFile(t, vaultPath, relPath, "---\ndate: 2026-04-01\npublish: true\n---\nbody\n")

	sourcePath := filepath.Join(vaultPath, filepath.FromSlash(relPath))
	rawLastModified := time.Date(2026, time.April, 6, 18, 45, 12, 0, time.FixedZone("UTC+8", 8*60*60))
	if err := os.Chtimes(sourcePath, rawLastModified, rawLastModified); err != nil {
		t.Fatalf("Chtimes(%q) error = %v", sourcePath, err)
	}

	scanResult, err := Scan(vaultPath)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	got, err := ParseFrontmatter(scanResult, model.SiteConfig{DefaultPublish: true})
	if err != nil {
		t.Fatalf("ParseFrontmatter() error = %v", err)
	}

	if len(got.PublicNotes) != 1 {
		t.Fatalf("len(PublicNotes) = %d, want 1", len(got.PublicNotes))
	}

	note := got.PublicNotes[0]
	wantDate := time.Date(2026, time.April, 1, 0, 0, 0, 0, time.UTC)
	if !note.Frontmatter.Date.Equal(wantDate) {
		t.Fatalf("Frontmatter.Date = %v, want %v", note.Frontmatter.Date, wantDate)
	}
	if !note.Frontmatter.Updated.IsZero() {
		t.Fatalf("Frontmatter.Updated = %v, want zero time", note.Frontmatter.Updated)
	}
	wantLastModified := rawLastModified.UTC().Truncate(time.Second)
	if !note.LastModified.Equal(wantLastModified) {
		t.Fatalf("LastModified = %v, want filesystem time %v", note.LastModified, wantLastModified)
	}
	if note.LastModified.Equal(note.Frontmatter.Date) {
		t.Fatalf("LastModified = %v, want distinct from Frontmatter.Date %v", note.LastModified, note.Frontmatter.Date)
	}
}

func TestParseFrontmatterKeepsPublishFalseNotesInUnpublishedLookup(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	writeVaultFile(t, vaultPath, "notes/guide.md", `---
title: Private Guide
aliases:
  - Docs
publish: false
---
secret
`)

	scanResult, err := Scan(vaultPath)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	got, err := ParseFrontmatter(scanResult, model.SiteConfig{DefaultPublish: true})
	if err != nil {
		t.Fatalf("ParseFrontmatter() error = %v", err)
	}

	if len(got.PublicNotes) != 0 {
		t.Fatalf("len(PublicNotes) = %d, want 0", len(got.PublicNotes))
	}

	note := got.Unpublished.Notes["notes/guide.md"]
	if note == nil {
		t.Fatal("Unpublished.Notes[notes/guide.md] = nil, want note")
	}
	if note.Publish == nil || *note.Publish {
		t.Fatalf("Publish = %v, want false", note.Publish)
	}
	if string(note.RawContent) != "secret\n" {
		t.Fatalf("RawContent = %q, want %q", string(note.RawContent), "secret\n")
	}

	byName := got.Unpublished.NoteByName["guide"]
	if len(byName) != 1 || byName[0] != note {
		t.Fatalf("Unpublished.NoteByName[guide] = %#v, want [%p]", byName, note)
	}

	byAlias := got.Unpublished.AliasByName["docs"]
	if len(byAlias) != 1 || byAlias[0] != note {
		t.Fatalf("Unpublished.AliasByName[docs] = %#v, want [%p]", byAlias, note)
	}
}

func TestParseFrontmatterDefaultPublishFalseRequiresExplicitPublish(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	writeVaultFile(t, vaultPath, "notes/draft.md", `---
title: Draft
---
hidden
`)
	writeVaultFile(t, vaultPath, "notes/public.md", `---
title: Public
publish: true
---
visible
`)

	scanResult, err := Scan(vaultPath)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	got, err := ParseFrontmatter(scanResult, model.SiteConfig{DefaultPublish: false})
	if err != nil {
		t.Fatalf("ParseFrontmatter() error = %v", err)
	}

	if len(got.PublicNotes) != 1 {
		t.Fatalf("len(PublicNotes) = %d, want 1", len(got.PublicNotes))
	}
	if got.PublicNotes[0].RelPath != "notes/public.md" {
		t.Fatalf("PublicNotes[0].RelPath = %q, want %q", got.PublicNotes[0].RelPath, "notes/public.md")
	}
	if _, ok := got.Unpublished.Notes["notes/draft.md"]; !ok {
		t.Fatal("Unpublished.Notes missing notes/draft.md")
	}
	if _, ok := got.Unpublished.Notes["notes/public.md"]; ok {
		t.Fatal("Unpublished.Notes unexpectedly contains notes/public.md")
	}
}

func TestParseFrontmatterHandlesDocumentWithoutFrontmatter(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	writeVaultFile(t, vaultPath, "notes/plain.md", "# Plain\n\nBody\n")

	scanResult, err := Scan(vaultPath)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	got, err := ParseFrontmatter(scanResult, model.SiteConfig{DefaultPublish: true})
	if err != nil {
		t.Fatalf("ParseFrontmatter() error = %v", err)
	}

	if len(got.PublicNotes) != 1 {
		t.Fatalf("len(PublicNotes) = %d, want 1", len(got.PublicNotes))
	}

	note := got.PublicNotes[0]
	if string(note.RawContent) != "# Plain\n\nBody\n" {
		t.Fatalf("RawContent = %q, want %q", string(note.RawContent), "# Plain\n\nBody\n")
	}
	if note.BodyStartLine != 1 {
		t.Fatalf("BodyStartLine = %d, want %d", note.BodyStartLine, 1)
	}
	if note.Publish != nil {
		t.Fatalf("Publish = %v, want nil", note.Publish)
	}
	if note.Frontmatter.Publish != nil {
		t.Fatalf("Frontmatter.Publish = %v, want nil", note.Frontmatter.Publish)
	}
	if note.Frontmatter.Title != "" {
		t.Fatalf("Frontmatter.Title = %q, want empty", note.Frontmatter.Title)
	}
	if len(note.Frontmatter.Extra) != 0 {
		t.Fatalf("len(Frontmatter.Extra) = %d, want 0", len(note.Frontmatter.Extra))
	}
}

func TestParseFrontmatterStripsBOMWhenDocumentHasNoFrontmatter(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	writeVaultFile(t, vaultPath, "notes/bom.md", "\ufeff# Heading\n\nBody\n")

	scanResult, err := Scan(vaultPath)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	got, err := ParseFrontmatter(scanResult, model.SiteConfig{DefaultPublish: true})
	if err != nil {
		t.Fatalf("ParseFrontmatter() error = %v", err)
	}

	if len(got.PublicNotes) != 1 {
		t.Fatalf("len(PublicNotes) = %d, want 1", len(got.PublicNotes))
	}

	note := got.PublicNotes[0]
	if string(note.RawContent) != "# Heading\n\nBody\n" {
		t.Fatalf("RawContent = %q, want %q", string(note.RawContent), "# Heading\n\nBody\n")
	}
	if strings.HasPrefix(string(note.RawContent), "\ufeff") {
		t.Fatalf("RawContent = %q, want BOM stripped", string(note.RawContent))
	}
	if !reflect.DeepEqual(note.Frontmatter, model.Frontmatter{}) {
		t.Fatalf("Frontmatter = %#v, want zero value", note.Frontmatter)
	}
	if note.Publish != nil {
		t.Fatalf("Publish = %v, want nil", note.Publish)
	}
	if note.Frontmatter.Publish != nil {
		t.Fatalf("Frontmatter.Publish = %v, want nil", note.Frontmatter.Publish)
	}
	if note.Frontmatter.Extra != nil {
		t.Fatalf("Frontmatter.Extra = %#v, want nil", note.Frontmatter.Extra)
	}
}

func TestParseFrontmatterAcceptsLocalISO8601DateTimes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		frontDate string
		want      time.Time
	}{
		{
			name:      "minute precision",
			frontDate: "2026-04-05T09:30",
			want:      time.Date(2026, time.April, 5, 9, 30, 0, 0, time.UTC),
		},
		{
			name:      "second precision",
			frontDate: "2026-04-05T09:30:45",
			want:      time.Date(2026, time.April, 5, 9, 30, 45, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			vaultPath := t.TempDir()
			writeVaultFile(t, vaultPath, "notes/date.md", "---\ndate: "+tt.frontDate+"\npublish: true\n---\nbody\n")

			scanResult, err := Scan(vaultPath)
			if err != nil {
				t.Fatalf("Scan() error = %v", err)
			}

			got, err := ParseFrontmatter(scanResult, model.SiteConfig{DefaultPublish: true})
			if err != nil {
				t.Fatalf("ParseFrontmatter() error = %v", err)
			}

			if len(got.PublicNotes) != 1 {
				t.Fatalf("len(PublicNotes) = %d, want 1", len(got.PublicNotes))
			}

			note := got.PublicNotes[0]
			if !note.Frontmatter.Date.Equal(tt.want) {
				t.Fatalf("Frontmatter.Date = %v, want %v", note.Frontmatter.Date, tt.want)
			}
		})
	}
}

func TestParseFrontmatterSupportsScalarAndSequenceTagsAndAliases(t *testing.T) {
	t.Parallel()

	vaultPath := t.TempDir()
	writeVaultFile(t, vaultPath, "notes/forms.md", `---
tags: writing
aliases: [Quick Start, Guide]
---
content
`)

	scanResult, err := Scan(vaultPath)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	got, err := ParseFrontmatter(scanResult, model.SiteConfig{DefaultPublish: true})
	if err != nil {
		t.Fatalf("ParseFrontmatter() error = %v", err)
	}

	note := got.PublicNotes[0]
	if !reflect.DeepEqual(note.Tags, []string{"writing"}) {
		t.Fatalf("Tags = %#v, want %#v", note.Tags, []string{"writing"})
	}
	if !reflect.DeepEqual(note.Aliases, []string{"Quick Start", "Guide"}) {
		t.Fatalf("Aliases = %#v, want %#v", note.Aliases, []string{"Quick Start", "Guide"})
	}
}

func TestParseFrontmatterBoundaryRegressions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		files    map[string]string
		cfg      model.SiteConfig
		checkErr func(*testing.T, error)
		check    func(*testing.T, FrontmatterResult)
	}{
		{
			name: "leading thematic break stays plain markdown when body is not frontmatter-shaped",
			files: map[string]string{
				"notes/rule.md": `---
# Heading

Body
`,
			},
			cfg: model.SiteConfig{DefaultPublish: true},
			check: func(t *testing.T, got FrontmatterResult) {
				t.Helper()
				if len(got.PublicNotes) != 1 {
					t.Fatalf("len(PublicNotes) = %d, want 1", len(got.PublicNotes))
				}
				if len(got.Unpublished.Notes) != 0 {
					t.Fatalf("len(Unpublished.Notes) = %d, want 0", len(got.Unpublished.Notes))
				}

				note := got.PublicNotes[0]
				if note.RelPath != "notes/rule.md" {
					t.Fatalf("RelPath = %q, want %q", note.RelPath, "notes/rule.md")
				}
				if string(note.RawContent) != "---\n# Heading\n\nBody\n" {
					t.Fatalf("RawContent = %q, want %q", string(note.RawContent), "---\n# Heading\n\nBody\n")
				}
				if !reflect.DeepEqual(note.Frontmatter, model.Frontmatter{}) {
					t.Fatalf("Frontmatter = %#v, want zero value", note.Frontmatter)
				}
				if note.Publish != nil {
					t.Fatalf("Publish = %v, want nil", note.Publish)
				}
			},
		},
		{
			name: "yaml-shaped opening block without closing delimiter returns error",
			files: map[string]string{
				"notes/private.md": `---
title: Private
publish: false
description: |
  hidden
`,
			},
			cfg: model.SiteConfig{DefaultPublish: true},
			checkErr: func(t *testing.T, err error) {
				t.Helper()
				if err == nil {
					t.Fatal("ParseFrontmatter() error = nil, want error")
				}
				if !strings.Contains(err.Error(), "notes/private.md") {
					t.Fatalf("ParseFrontmatter() error = %q, want path context", err)
				}
				if !strings.Contains(err.Error(), "missing closing frontmatter delimiter") {
					t.Fatalf("ParseFrontmatter() error = %q, want missing closing delimiter message", err)
				}
			},
		},
		{
			name: "indented block scalar delimiters stay in frontmatter content",
			files: map[string]string{
				"notes/private.md": `---
title: Private
description: |
  intro
  ---
  ...
  tail
publish: false
---
secret
`,
			},
			cfg: model.SiteConfig{DefaultPublish: true},
			check: func(t *testing.T, got FrontmatterResult) {
				t.Helper()
				if len(got.PublicNotes) != 0 {
					t.Fatalf("len(PublicNotes) = %d, want 0", len(got.PublicNotes))
				}

				note := got.Unpublished.Notes["notes/private.md"]
				if note == nil {
					t.Fatal("Unpublished.Notes[notes/private.md] = nil, want note")
				}
				if note.Publish == nil || *note.Publish {
					t.Fatalf("Publish = %v, want false", note.Publish)
				}
				if note.Frontmatter.Description != "intro\n---\n...\ntail" {
					t.Fatalf("Frontmatter.Description = %q, want %q", note.Frontmatter.Description, "intro\n---\n...\ntail")
				}
				if string(note.RawContent) != "secret\n" {
					t.Fatalf("RawContent = %q, want %q", string(note.RawContent), "secret\n")
				}
			},
		},
		{
			name: "invalid yaml after opening delimiter returns parse error",
			files: map[string]string{
				"notes/broken.md": `---
title: Broken
publish: false
tags: [one
---
secret
`,
			},
			cfg: model.SiteConfig{DefaultPublish: true},
			checkErr: func(t *testing.T, err error) {
				t.Helper()
				if err == nil {
					t.Fatal("ParseFrontmatter() error = nil, want error")
				}
				if !strings.Contains(err.Error(), "notes/broken.md") {
					t.Fatalf("ParseFrontmatter() error = %q, want path context", err)
				}
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			vaultPath := t.TempDir()
			for relPath, content := range tt.files {
				writeVaultFile(t, vaultPath, relPath, content)
			}

			scanResult, err := Scan(vaultPath)
			if err != nil {
				t.Fatalf("Scan() error = %v", err)
			}

			got, err := ParseFrontmatter(scanResult, tt.cfg)
			if tt.checkErr != nil {
				tt.checkErr(t, err)
				if len(got.PublicNotes) != 0 {
					t.Fatalf("len(PublicNotes) = %d, want 0 on error", len(got.PublicNotes))
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseFrontmatter() error = %v", err)
			}
			tt.check(t, got)
		})
	}
}
