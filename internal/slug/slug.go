package slug

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

var (
	// ErrInvalidFrontmatterSlug reports an explicit frontmatter slug that normalizes to empty.
	ErrInvalidFrontmatterSlug = errors.New("frontmatter slug normalizes to empty")
	// ErrInvalidFileSlug reports a file stem that normalizes to empty.
	ErrInvalidFileSlug = errors.New("file stem normalizes to empty")
	// ErrInvalidArticleSlug reports a slug that is not one permitted URL segment.
	ErrInvalidArticleSlug = errors.New("article slug is not a valid URL path segment")
	// ErrInvalidNumericPrefix reports an overflowing filename order prefix.
	ErrInvalidNumericPrefix = errors.New("filename numeric prefix is out of range")
)

// Candidate identifies a source path and the slug assigned to it.
type Candidate struct {
	Source string
	Slug   string
}

// Conflict reports a slug used by multiple sources.
type Conflict struct {
	Slug    string
	Sources []string
}

// InvalidSlug reports a source whose slug is empty or whitespace-only.
type InvalidSlug struct {
	Source string
	Slug   string
}

// Canonicalize returns the shared lowercase NFC form used by slug generation
// and note or alias lookup keys.
func Canonicalize(input string) string {
	return canonicalize(input)
}

// Generate returns the normalized slug for a note.
//
// If frontmatterSlug is provided, it is authoritative. Otherwise the file stem
// from relPath is used. Inputs that normalize to empty are rejected.
func Generate(frontmatterSlug *string, relPath string) (string, error) {
	if frontmatterSlug != nil {
		normalized := normalize(*frontmatterSlug)
		if normalized == "" {
			return "", fmt.Errorf("%w: %q", ErrInvalidFrontmatterSlug, *frontmatterSlug)
		}
		return normalized, nil
	}

	normalized := normalize(fileStem(relPath))
	if normalized == "" {
		return "", fmt.Errorf("%w: %q", ErrInvalidFileSlug, relPath)
	}

	return normalized, nil
}

// DetectConflicts returns stable lists of slug conflicts and invalid empty slugs.
func DetectConflicts(candidates []Candidate) ([]Conflict, []InvalidSlug) {
	if len(candidates) == 0 {
		return nil, nil
	}

	sourcesBySlug := make(map[string]map[string]struct{}, len(candidates))
	invalidBySource := make(map[string]InvalidSlug)
	for _, candidate := range candidates {
		canonicalSlug := canonicalize(candidate.Slug)
		if strings.TrimSpace(canonicalSlug) == "" {
			invalidBySource[candidate.Source] = InvalidSlug(candidate)
			continue
		}

		sources := sourcesBySlug[canonicalSlug]
		if sources == nil {
			sources = make(map[string]struct{})
			sourcesBySlug[canonicalSlug] = sources
		}
		sources[candidate.Source] = struct{}{}
	}

	var conflicts []Conflict
	for slugValue, sources := range sourcesBySlug {
		if len(sources) < 2 {
			continue
		}

		conflictSources := make([]string, 0, len(sources))
		for source := range sources {
			conflictSources = append(conflictSources, source)
		}
		sort.Strings(conflictSources)

		conflicts = append(conflicts, Conflict{
			Slug:    slugValue,
			Sources: conflictSources,
		})
	}

	sort.Slice(conflicts, func(i, j int) bool {
		return conflicts[i].Slug < conflicts[j].Slug
	})

	var invalid []InvalidSlug
	if len(invalidBySource) > 0 {
		invalid = make([]InvalidSlug, 0, len(invalidBySource))
		for _, candidate := range invalidBySource {
			invalid = append(invalid, candidate)
		}
	}

	if len(invalid) > 0 {
		sort.Slice(invalid, func(i, j int) bool {
			return invalid[i].Source < invalid[j].Source
		})
	}

	return conflicts, invalid
}

// GenerateArticleSegment implements the section publisher's route rule. An
// explicit slug is validated as-is after NFKC normalization; otherwise the
// basename loses one leading numeric prefix only when it is followed by the
// documented separator.
func GenerateArticleSegment(explicit *string, relPath string) (string, error) {
	if explicit != nil {
		value := norm.NFKC.String(strings.TrimSpace(*explicit))
		if value == "" || !validArticleSegment(value) {
			return "", fmt.Errorf("%w: %q", ErrInvalidArticleSlug, *explicit)
		}
		return value, nil
	}
	base := path.Base(strings.ReplaceAll(relPath, "\\", "/"))
	base = strings.TrimSuffix(base, path.Ext(base))
	if base == "" {
		return "", fmt.Errorf("%w: %q", ErrInvalidFileSlug, relPath)
	}
	if prefix, separator, err := NumericPrefix(base); err != nil {
		return "", err
	} else if separator {
		base = base[len(prefix):]
		base = strings.TrimLeft(base, "-_ .")
	}
	base = norm.NFKC.String(base)
	if strings.TrimSpace(base) == "" {
		return "", fmt.Errorf("%w: %q", ErrInvalidFileSlug, relPath)
	}
	return base, nil
}

// NumericPrefix reports the leading decimal prefix including its separator
// and whether the separator was one of the allowed filename separators.
func NumericPrefix(value string) (prefix string, separator bool, err error) {
	index := 0
	for index < len(value) && value[index] >= '0' && value[index] <= '9' {
		index++
	}
	if index == 0 || index == len(value) || !strings.ContainsRune("-_ .", rune(value[index])) {
		return "", false, nil
	}
	if _, parseErr := strconv.ParseInt(value[:index], 10, 32); parseErr != nil {
		return "", false, fmt.Errorf("%w: %q", ErrInvalidNumericPrefix, value[:index])
	}
	return value[:index+1], true, nil
}

func validArticleSegment(value string) bool {
	for _, r := range value {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), r == '-', r == '_', r == '~':
		default:
			return false
		}
	}
	return value != ""
}

func fileStem(relPath string) string {
	base := path.Base(strings.ReplaceAll(relPath, "\\", "/"))
	ext := path.Ext(base)
	if ext == "" {
		return base
	}
	return strings.TrimSuffix(base, ext)
}

func normalize(input string) string {
	var builder strings.Builder
	lastHyphen := false
	canAppendMark := false

	for _, r := range canonicalize(input) {
		switch {
		case isASCIIControl(r):
			canAppendMark = false
			continue
		case unicode.IsSpace(r) || r == '_' || r == '-':
			if lastHyphen {
				continue
			}
			builder.WriteRune('-')
			lastHyphen = true
			canAppendMark = false
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			builder.WriteRune(r)
			lastHyphen = false
			canAppendMark = true
		case unicode.IsMark(r):
			if !canAppendMark {
				continue
			}
			builder.WriteRune(r)
			lastHyphen = false
		default:
			canAppendMark = false
			continue
		}
	}

	return strings.Trim(builder.String(), "-")
}

func canonicalize(input string) string {
	return norm.NFC.String(strings.ToLower(norm.NFC.String(input)))
}

func isASCIIControl(r rune) bool {
	return (r >= 0 && r < 0x20) || r == 0x7f
}
