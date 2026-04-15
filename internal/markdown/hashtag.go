package markdown

import (
	"path"
	"path/filepath"
	"strings"

	"github.com/simp-lee/obsite/internal/model"
	internalslug "github.com/simp-lee/obsite/internal/slug"
	gmhashtag "go.abhg.dev/goldmark/hashtag"
)

type renderHashtagResolver struct {
	index      *model.VaultIndex
	outputNote *model.Note
}

func newRenderHashtagResolver(idx *model.VaultIndex, outputNote *model.Note) gmhashtag.Resolver {
	if idx == nil {
		return nil
	}

	return renderHashtagResolver{index: idx, outputNote: outputNote}
}

func (r renderHashtagResolver) ResolveHashtag(node *gmhashtag.Node) ([]byte, error) {
	if node == nil || r.index == nil {
		return nil, nil
	}

	tagName := normalizeRenderedHashtag(string(node.Tag))
	if tagName == "" {
		return nil, nil
	}

	tag := r.index.Tags[tagName]
	if tag == nil || strings.TrimSpace(tag.Slug) == "" {
		return nil, nil
	}

	href := relativeDirectoryURLToNoteOutput(r.outputNote, tag.Slug)
	if href == "" {
		return nil, nil
	}

	return []byte(href), nil
}

func normalizeRenderedHashtag(value string) string {
	trimmed := strings.TrimSpace(strings.TrimPrefix(value, "#"))
	if trimmed == "" {
		return ""
	}

	rawSegments := strings.Split(trimmed, "/")
	segments := make([]string, 0, len(rawSegments))
	for _, segment := range rawSegments {
		normalized, ok := normalizeRenderedHashtagSegment(segment)
		if !ok {
			return ""
		}
		segments = append(segments, normalized)
	}
	if len(segments) == 0 {
		return ""
	}

	return strings.Join(segments, "/")
}

func normalizeRenderedHashtagSegment(segment string) (string, bool) {
	segment = strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(segment)), " "))
	if segment == "" {
		return "", false
	}

	normalized, err := internalslug.Generate(&segment, "")
	if err != nil {
		return "", false
	}
	return normalized, true
}

func relativeDirectoryURLToNoteOutput(note *model.Note, siteRelPath string) string {
	normalized := normalizeHashtagSitePath(siteRelPath)
	if normalized == "" {
		return ""
	}

	relativePath, err := filepath.Rel(hashtagNoteOutputDir(note), normalized)
	if err != nil {
		relativePath = normalized
	}

	clean := filepath.ToSlash(relativePath)
	if clean == "" || clean == "." {
		return "./"
	}
	if !strings.HasSuffix(clean, "/") {
		clean += "/"
	}
	return clean
}

func hashtagNoteOutputDir(note *model.Note) string {
	if note == nil {
		return "."
	}

	slug := strings.Trim(strings.ReplaceAll(note.Slug, "\\", "/"), "/")
	if slug == "" {
		return "."
	}

	return path.Clean(slug)
}

func normalizeHashtagSitePath(value string) string {
	cleaned := strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	cleaned = strings.TrimPrefix(cleaned, "/")
	if cleaned == "" {
		return ""
	}

	cleaned = path.Clean(cleaned)
	if cleaned == "." {
		return ""
	}

	return cleaned
}
