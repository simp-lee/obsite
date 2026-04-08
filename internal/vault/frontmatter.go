package vault

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/simp-lee/obsite/internal/model"
	"gopkg.in/yaml.v3"
)

var utf8BOM = []byte{0xef, 0xbb, 0xbf}

var errMissingClosingFrontmatterDelimiter = errors.New("missing closing frontmatter delimiter")

// FrontmatterResult is the Step 12 handoff for later slugging and AST indexing.
// PublicNotes retains the notes allowed into the public site, while Unpublished
// keeps the minimal resolver-facing metadata for filtered notes.
type FrontmatterResult struct {
	PublicNotes []*model.Note
	Unpublished model.UnpublishedLookup
}

// ParseFrontmatter reads each scanned Markdown file, extracts YAML frontmatter
// when present, and partitions notes according to publish/defaultPublish.
func ParseFrontmatter(scanResult ScanResult, cfg model.SiteConfig) (FrontmatterResult, error) {
	result := FrontmatterResult{
		PublicNotes: make([]*model.Note, 0, len(scanResult.MarkdownFiles)),
		Unpublished: model.UnpublishedLookup{
			Notes:       make(map[string]*model.Note),
			NoteByName:  make(map[string][]*model.Note),
			AliasByName: make(map[string][]*model.Note),
		},
	}

	for _, relPath := range scanResult.MarkdownFiles {
		sourcePath := pathForScanResult(scanResult, relPath)
		lastModified, err := sourceLastModified(sourcePath)
		if err != nil {
			return FrontmatterResult{}, fmt.Errorf("stat %q: %w", sourcePath, err)
		}

		content, err := os.ReadFile(sourcePath)
		if err != nil {
			return FrontmatterResult{}, fmt.Errorf("read %q: %w", sourcePath, err)
		}

		frontmatterData, body, bodyStartLine, hasFrontmatter, err := splitFrontmatter(content)
		if err != nil {
			return FrontmatterResult{}, fmt.Errorf("parse frontmatter %q: %w", relPath, err)
		}
		frontmatter, err := parseFrontmatter(frontmatterData, hasFrontmatter)
		if err != nil {
			return FrontmatterResult{}, fmt.Errorf("parse frontmatter %q: %w", relPath, err)
		}
		effectiveLastModified := lastModified
		if !frontmatter.Updated.IsZero() {
			effectiveLastModified = frontmatter.Updated
		}

		note := &model.Note{
			RelPath:       relPath,
			Frontmatter:   frontmatter,
			LastModified:  effectiveLastModified,
			Aliases:       cloneStrings(frontmatter.Aliases),
			Tags:          cloneStrings(frontmatter.Tags),
			Publish:       frontmatter.Publish,
			RawContent:    body,
			BodyStartLine: bodyStartLine,
		}

		if shouldPublish(frontmatter.Publish, cfg.DefaultPublish) {
			result.PublicNotes = append(result.PublicNotes, note)
			continue
		}

		result.Unpublished.Notes[note.RelPath] = note
		appendUnpublishedLookup(result.Unpublished.NoteByName, noteLookupName(note.RelPath), note)
		for _, alias := range note.Aliases {
			appendUnpublishedLookup(result.Unpublished.AliasByName, aliasLookupName(alias), note)
		}
	}

	return result, nil
}

func pathForScanResult(scanResult ScanResult, relPath string) string {
	if scanResult.VaultPath == "" {
		return relPath
	}
	return filepath.Join(scanResult.VaultPath, filepath.FromSlash(relPath))
}

func splitFrontmatter(content []byte) ([]byte, []byte, int, bool, error) {
	start := 0
	if bytes.HasPrefix(content, utf8BOM) {
		start = len(utf8BOM)
	}

	firstLine, next, ok := readLine(content, start)
	if !ok || !isFrontmatterDelimiter(firstLine) {
		return nil, content[start:], 1, false, nil
	}

	frontmatterStart := next
	lineStart := next
	lineNumber := 2
	for {
		line, nextLine, lineOK := readLine(content, lineStart)
		if !lineOK {
			if isLikelyFrontmatterBlock(content[frontmatterStart:], false) {
				return nil, nil, 0, false, errMissingClosingFrontmatterDelimiter
			}
			return nil, content[start:], 1, false, nil
		}
		if isFrontmatterClosingDelimiter(line) {
			block := content[frontmatterStart:lineStart]
			if isLikelyFrontmatterBlock(block, true) {
				return block, content[nextLine:], lineNumber + 1, true, nil
			}
			return nil, content[start:], 1, false, nil
		}
		if nextLine >= len(content) {
			if isLikelyFrontmatterBlock(content[frontmatterStart:], false) {
				return nil, nil, 0, false, errMissingClosingFrontmatterDelimiter
			}
			return nil, content[start:], 1, false, nil
		}
		lineStart = nextLine
		lineNumber++
	}
}

func readLine(content []byte, start int) ([]byte, int, bool) {
	if start > len(content) {
		return nil, 0, false
	}
	if start == len(content) {
		return nil, start, false
	}

	lineEnd := start
	for lineEnd < len(content) && content[lineEnd] != '\n' {
		lineEnd++
	}

	next := lineEnd
	if next < len(content) && content[next] == '\n' {
		next++
	}

	return bytes.TrimSuffix(content[start:lineEnd], []byte{'\r'}), next, true
}

func isFrontmatterDelimiter(line []byte) bool {
	return bytes.Equal(line, []byte("---"))
}

func isFrontmatterClosingDelimiter(line []byte) bool {
	return bytes.Equal(line, []byte("---")) || bytes.Equal(line, []byte("..."))
}

func isLikelyFrontmatterBlock(block []byte, closed bool) bool {
	if len(bytes.TrimSpace(block)) == 0 {
		return closed
	}

	var document yaml.Node
	if err := yaml.Unmarshal(block, &document); err == nil {
		if len(document.Content) == 0 {
			return false
		}
		return document.Content[0].Kind == yaml.MappingNode
	}

	line, ok := firstNonBlankLine(block)
	if !ok {
		return false
	}

	return isLikelyYAMLMappingLine(bytes.TrimSpace(line))
}

func firstNonBlankLine(content []byte) ([]byte, bool) {
	lineStart := 0
	for {
		line, next, ok := readLine(content, lineStart)
		if !ok {
			return nil, false
		}
		if len(bytes.TrimSpace(line)) != 0 {
			return line, true
		}
		if next >= len(content) {
			return nil, false
		}
		lineStart = next
	}
}

func isLikelyYAMLMappingLine(line []byte) bool {
	colon := bytes.IndexByte(line, ':')
	if colon <= 0 {
		return false
	}
	if colon == len(line)-1 {
		return true
	}

	switch line[colon+1] {
	case ' ', '\t', '[', '{', '|', '>', '&', '*', '!', '\'', '"':
		return true
	default:
		return false
	}
}

func parseFrontmatter(data []byte, present bool) (model.Frontmatter, error) {
	if !present {
		return model.Frontmatter{}, nil
	}

	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return model.Frontmatter{}, err
	}

	if len(document.Content) == 0 {
		return model.Frontmatter{}, nil
	}

	root := document.Content[0]
	if root.Kind != yaml.MappingNode {
		return model.Frontmatter{}, fmt.Errorf("frontmatter must be a YAML mapping")
	}

	frontmatter := model.Frontmatter{}
	for index := 0; index+1 < len(root.Content); index += 2 {
		keyNode := root.Content[index]
		valueNode := root.Content[index+1]
		key := strings.TrimSpace(keyNode.Value)

		switch key {
		case "title":
			value, err := decodeStringField(key, valueNode)
			if err != nil {
				return model.Frontmatter{}, err
			}
			frontmatter.Title = value
		case "description":
			value, err := decodeStringField(key, valueNode)
			if err != nil {
				return model.Frontmatter{}, err
			}
			frontmatter.Description = value
		case "date":
			value, err := decodeTimeField(key, valueNode)
			if err != nil {
				return model.Frontmatter{}, err
			}
			frontmatter.Date = value
		case "updated":
			value, err := decodeTimeField(key, valueNode)
			if err != nil {
				return model.Frontmatter{}, err
			}
			frontmatter.Updated = value
		case "tags":
			value, err := decodeStringListField(key, valueNode)
			if err != nil {
				return model.Frontmatter{}, err
			}
			frontmatter.Tags = value
		case "aliases":
			value, err := decodeStringListField(key, valueNode)
			if err != nil {
				return model.Frontmatter{}, err
			}
			frontmatter.Aliases = value
		case "publish":
			value, err := decodeBoolField(key, valueNode)
			if err != nil {
				return model.Frontmatter{}, err
			}
			frontmatter.Publish = value
		case "slug":
			value, err := decodeStringField(key, valueNode)
			if err != nil {
				return model.Frontmatter{}, err
			}
			frontmatter.Slug = value
		default:
			if frontmatter.Extra == nil {
				frontmatter.Extra = make(map[string]any)
			}
			var extraValue any
			if err := valueNode.Decode(&extraValue); err != nil {
				return model.Frontmatter{}, fmt.Errorf("decode %q: %w", key, err)
			}
			frontmatter.Extra[key] = extraValue
		}
	}

	frontmatter.Title = strings.TrimSpace(frontmatter.Title)
	frontmatter.Description = strings.TrimSpace(frontmatter.Description)
	frontmatter.Slug = strings.TrimSpace(frontmatter.Slug)

	return frontmatter, nil
}

func decodeStringField(name string, node *yaml.Node) (string, error) {
	if isNullNode(node) {
		return "", nil
	}

	var value string
	if err := node.Decode(&value); err != nil {
		return "", fmt.Errorf("decode %q: %w", name, err)
	}
	return strings.TrimSpace(value), nil
}

func decodeStringListField(name string, node *yaml.Node) ([]string, error) {
	if isNullNode(node) {
		return nil, nil
	}

	switch node.Kind {
	case yaml.SequenceNode:
		values := make([]string, 0, len(node.Content))
		seen := make(map[string]struct{}, len(node.Content))
		for _, item := range node.Content {
			value, err := decodeStringField(name, item)
			if err != nil {
				return nil, err
			}
			if value == "" {
				continue
			}
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			values = append(values, value)
		}
		return values, nil
	default:
		value, err := decodeStringField(name, node)
		if err != nil {
			return nil, err
		}
		if value == "" {
			return nil, nil
		}
		return []string{value}, nil
	}
}

func decodeBoolField(name string, node *yaml.Node) (*bool, error) {
	if isNullNode(node) {
		return nil, nil
	}

	var value bool
	if err := node.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode %q: %w", name, err)
	}
	return &value, nil
}

func decodeTimeField(name string, node *yaml.Node) (time.Time, error) {
	if isNullNode(node) {
		return time.Time{}, nil
	}

	var parsed time.Time
	if err := node.Decode(&parsed); err == nil {
		return parsed.UTC(), nil
	}

	raw, err := decodeStringField(name, node)
	if err != nil {
		return time.Time{}, err
	}
	if raw == "" {
		return time.Time{}, nil
	}

	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02",
		"2006-01-02T15:04Z07:00",
		"2006-01-02T15:04",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04:05Z07:00",
		"2006-01-02T15:04:05Z07:00",
	} {
		parsed, err = time.Parse(layout, raw)
		if err == nil {
			return parsed.UTC(), nil
		}
	}

	return time.Time{}, fmt.Errorf("decode %q: unsupported date value %q", name, raw)
}

func isNullNode(node *yaml.Node) bool {
	if node == nil {
		return true
	}
	return node.Tag == "!!null" || (node.Kind == yaml.ScalarNode && strings.EqualFold(strings.TrimSpace(node.Value), "null"))
}

func shouldPublish(publish *bool, defaultPublish bool) bool {
	if publish != nil {
		return *publish
	}
	return defaultPublish
}

func appendUnpublishedLookup(index map[string][]*model.Note, key string, note *model.Note) {
	if key == "" || note == nil {
		return
	}

	existing := index[key]
	for _, candidate := range existing {
		if candidate == note {
			return
		}
	}
	index[key] = append(existing, note)
}

func noteLookupName(relPath string) string {
	base := path.Base(strings.ReplaceAll(relPath, `\`, "/"))
	if ext := path.Ext(base); ext != "" {
		base = strings.TrimSuffix(base, ext)
	}
	return strings.ToLower(base)
}

func aliasLookupName(alias string) string {
	return strings.ToLower(strings.TrimSpace(alias))
}

func cloneStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	cloned := make([]string, len(values))
	copy(cloned, values)
	return cloned
}

func sourceLastModified(filePath string) (time.Time, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		return time.Time{}, err
	}

	return normalizeFilesystemTime(info.ModTime()), nil
}

func normalizeFilesystemTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Time{}
	}

	return value.Round(0).UTC().Truncate(time.Second)
}
