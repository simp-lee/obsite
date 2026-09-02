package vault

import (
	"bytes"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	internalfsutil "github.com/simp-lee/obsite/internal/fsutil"
	"github.com/simp-lee/obsite/internal/model"
	"gopkg.in/yaml.v3"
)

const maxInt32 = int64(1<<31 - 1)

var strictDatePattern = regexp.MustCompile(`^(?:[0-9]{4}-[0-9]{2}-[0-9]{2}|[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]+)?(?:Z|[+-][0-9]{2}:[0-9]{2}))$`)
var strictSlugPattern = regexp.MustCompile(`^[\p{L}\p{N}_~-]+$`)
var strictOrderPattern = regexp.MustCompile(`^[0-9]+$`)

// StrictFrontmatterResult is the schema-safe source handoff used by the
// section planner. Unlike ParseFrontmatter, it never applies defaultPublish,
// keeps _index.md out of the article list, and rejects unknown fields.
type StrictFrontmatterResult struct {
	Sources     []model.PlannedSource
	Sections    []*model.SectionSource
	Articles    []*model.Note
	AllArticles []*model.Note
	Unpublished []*model.Note
}

// ParseStrictFrontmatter parses the complete vault source set according to the
// new section/article contract. It is deliberately separate from the old
// indexer handoff until the renderer is switched to the normalized plan.
func ParseStrictFrontmatter(scanResult ScanResult) (StrictFrontmatterResult, error) {
	result := StrictFrontmatterResult{
		Sources:     make([]model.PlannedSource, 0, len(scanResult.MarkdownFiles)),
		Sections:    make([]*model.SectionSource, 0),
		Articles:    make([]*model.Note, 0),
		AllArticles: make([]*model.Note, 0),
		Unpublished: make([]*model.Note, 0),
	}

	for _, relPath := range append([]string(nil), scanResult.MarkdownFiles...) {
		_, content, info, err := internalfsutil.ReadContainedRegularFile(scanResult.VaultPath, relPath)
		if err != nil {
			return StrictFrontmatterResult{}, fmt.Errorf("read %q: %w", relPath, err)
		}
		frontmatterData, body, bodyStartLine, present, err := splitFrontmatter(content)
		if err != nil {
			return StrictFrontmatterResult{}, fmt.Errorf("parse frontmatter %q: %w", relPath, err)
		}
		mapping, err := strictMapping(frontmatterData, present)
		if err != nil {
			return StrictFrontmatterResult{}, fmt.Errorf("parse frontmatter %q: %w", relPath, err)
		}
		if strings.EqualFold(path.Base(relPath), "_index.md") && path.Base(relPath) != "_index.md" {
			return StrictFrontmatterResult{}, fmt.Errorf("section source %q must use the exact filename _index.md", relPath)
		}
		if path.Base(relPath) == "_index.md" {
			section, err := decodeSectionSource(relPath, body, bodyStartLine, info, mapping)
			if err != nil {
				return StrictFrontmatterResult{}, fmt.Errorf("parse section %q: %w", relPath, err)
			}
			result.Sections = append(result.Sections, section)
			result.Sources = append(result.Sources, model.PlannedSource{RelPath: relPath, Section: section, Publish: section.Frontmatter.Publish != nil && *section.Frontmatter.Publish})
			continue
		}

		note, err := decodeStrictArticle(relPath, body, bodyStartLine, info, mapping)
		if err != nil {
			return StrictFrontmatterResult{}, fmt.Errorf("parse article %q: %w", relPath, err)
		}
		result.AllArticles = append(result.AllArticles, note)
		published := note.Frontmatter.Publish != nil && *note.Frontmatter.Publish
		result.Sources = append(result.Sources, model.PlannedSource{RelPath: relPath, Article: note, Publish: published})
		if published {
			result.Articles = append(result.Articles, note)
		} else {
			result.Unpublished = append(result.Unpublished, note)
		}
	}
	return result, nil
}

func strictMapping(data []byte, present bool) (map[string]*yaml.Node, error) {
	if !present {
		return nil, fmt.Errorf("frontmatter is required")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("multiple YAML documents are not supported")
	}
	if len(document.Content) == 0 || document.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("frontmatter must be a YAML mapping")
	}
	root := document.Content[0]
	fields := make(map[string]*yaml.Node, len(root.Content)/2)
	for index := 0; index+1 < len(root.Content); index += 2 {
		key, value := root.Content[index], root.Content[index+1]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
			return nil, fmt.Errorf("frontmatter key at line %d must be a string", key.Line)
		}
		if _, exists := fields[key.Value]; exists {
			return nil, fmt.Errorf("duplicate frontmatter key %q at line %d", key.Value, key.Line)
		}
		if value.Tag == "!!null" {
			return nil, fmt.Errorf("frontmatter field %q at line %d must not be null", key.Value, value.Line)
		}
		fields[key.Value] = value
	}
	return fields, nil
}

var articleFields = map[string]struct{}{
	"title": {}, "description": {}, "publish": {}, "date": {}, "updated": {},
	"tags": {}, "aliases": {}, "slug": {}, "type": {}, "order": {}, "author": {},
	"reviewed": {}, "status": {}, "audience": {}, "productVersion": {}, "series": {},
	"cover": {}, "banner": {}, "bannerAlt": {},
}

var sectionFields = map[string]struct{}{
	"title": {}, "description": {}, "publish": {}, "order": {}, "banner": {}, "bannerAlt": {},
}

func rejectUnknownFields(fields map[string]*yaml.Node, allowed map[string]struct{}) error {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		if _, ok := allowed[key]; !ok {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return nil
	}
	sort.Strings(keys)
	return fmt.Errorf("unknown frontmatter field %q at line %d", keys[0], fields[keys[0]].Line)
}

func decodeSectionSource(relPath string, body []byte, bodyStartLine int, info interface{ ModTime() time.Time }, fields map[string]*yaml.Node) (*model.SectionSource, error) {
	if err := rejectUnknownFields(fields, sectionFields); err != nil {
		return nil, err
	}
	title, err := requiredStrictString(fields, "title")
	if err != nil {
		return nil, err
	}
	publish, err := requiredStrictBool(fields, "publish")
	if err != nil {
		return nil, err
	}
	section := &model.SectionSource{
		RelPath: relPath, SectionPath: path.Dir(relPath), RawContent: append([]byte(nil), body...),
		BodyStartLine: bodyStartLine, LastModified: normalizeFilesystemTime(info.ModTime()),
		Frontmatter: model.SectionFrontmatter{Title: title, Publish: publish},
	}
	if section.SectionPath == "." {
		section.SectionPath = "."
	}
	if value, ok := fields["description"]; ok {
		section.Frontmatter.Description, err = strictString(value, "description", false)
		if err != nil {
			return nil, err
		}
	}
	if value, ok := fields["order"]; ok {
		section.Frontmatter.Order, err = strictOrder(value, "order")
		if err != nil {
			return nil, err
		}
	}
	if value, ok := fields["banner"]; ok {
		section.Frontmatter.Banner, err = strictString(value, "banner", false)
		if err != nil {
			return nil, err
		}
	}
	if value, ok := fields["bannerAlt"]; ok {
		section.Frontmatter.BannerAlt, err = strictString(value, "bannerAlt", true)
		if err != nil {
			return nil, err
		}
	}
	if (section.Frontmatter.Banner == "") != (section.Frontmatter.BannerAlt == "") {
		return nil, fmt.Errorf("banner and bannerAlt must be configured together")
	}
	return section, nil
}

func decodeStrictArticle(relPath string, body []byte, bodyStartLine int, info interface{ ModTime() time.Time }, fields map[string]*yaml.Node) (*model.Note, error) {
	if err := rejectUnknownFields(fields, articleFields); err != nil {
		return nil, err
	}
	title, err := requiredStrictString(fields, "title")
	if err != nil {
		return nil, err
	}
	publish, err := requiredStrictBool(fields, "publish")
	if err != nil {
		return nil, err
	}
	typeName, err := requiredStrictString(fields, "type")
	if err != nil {
		return nil, err
	}
	if typeName != "doc" && typeName != "post" && typeName != "page" {
		return nil, fmt.Errorf("type must be one of doc, post, or page")
	}
	note := &model.Note{RelPath: relPath, RawContent: append([]byte(nil), body...), BodyStartLine: bodyStartLine, LastModified: normalizeFilesystemTime(info.ModTime())}
	note.Frontmatter.Title, note.Frontmatter.Publish, note.Frontmatter.Type = title, publish, typeName
	if value, ok := fields["description"]; ok {
		note.Frontmatter.Description, err = strictString(value, "description", false)
		if err != nil {
			return nil, err
		}
	}
	if value, ok := fields["date"]; ok {
		note.Frontmatter.Date, err = strictTime(value, "date")
		if err != nil {
			return nil, err
		}
	}
	if typeName == "post" && note.Frontmatter.Date.IsZero() {
		return nil, fmt.Errorf("date is required for type=post")
	}
	if value, ok := fields["updated"]; ok {
		note.Frontmatter.Updated, err = strictTime(value, "updated")
		if err != nil {
			return nil, err
		}
	}
	if !note.Frontmatter.Updated.IsZero() && !note.Frontmatter.Date.IsZero() && note.Frontmatter.Updated.Before(note.Frontmatter.Date) {
		return nil, fmt.Errorf("updated must not be earlier than date")
	}
	if value, ok := fields["reviewed"]; ok {
		note.Frontmatter.Reviewed, err = strictTime(value, "reviewed")
		if err != nil {
			return nil, err
		}
	}
	if value, ok := fields["tags"]; ok {
		note.Frontmatter.Tags, err = strictStringList(value, "tags")
		if err != nil {
			return nil, err
		}
		note.Tags = append([]string(nil), note.Frontmatter.Tags...)
	}
	if value, ok := fields["aliases"]; ok {
		note.Frontmatter.Aliases, err = strictStringList(value, "aliases")
		if err != nil {
			return nil, err
		}
		note.Aliases = append([]string(nil), note.Frontmatter.Aliases...)
	}
	if value, ok := fields["slug"]; ok {
		note.Frontmatter.Slug, err = strictArticleSlug(value)
		if err != nil {
			return nil, err
		}
	}
	if value, ok := fields["order"]; ok {
		note.Frontmatter.Order, err = strictOrder(value, "order")
		if err != nil {
			return nil, err
		}
	}
	for _, field := range []string{"author", "status", "audience", "productVersion", "series", "cover", "banner", "bannerAlt"} {
		if value, ok := fields[field]; ok {
			text, textErr := strictString(value, field, true)
			if textErr != nil {
				return nil, textErr
			}
			switch field {
			case "author":
				note.Frontmatter.Author = text
			case "status":
				note.Frontmatter.Status = text
				if text != "stable" && text != "experimental" && text != "deprecated" {
					return nil, fmt.Errorf("status must be stable, experimental, or deprecated")
				}
			case "audience":
				note.Frontmatter.Audience = text
			case "productVersion":
				note.Frontmatter.ProductVersion = text
			case "series":
				note.Frontmatter.Series = text
			case "cover":
				note.Frontmatter.Cover = text
			case "banner":
				note.Frontmatter.Banner = text
			case "bannerAlt":
				note.Frontmatter.BannerAlt = text
			}
		}
	}
	if note.Frontmatter.Banner != "" && note.Frontmatter.BannerAlt == "" || note.Frontmatter.Banner == "" && note.Frontmatter.BannerAlt != "" {
		return nil, fmt.Errorf("banner and bannerAlt must be configured together")
	}
	if !note.Frontmatter.Updated.IsZero() {
		note.LastModified = note.Frontmatter.Updated
	}
	return note, nil
}

func requiredStrictString(fields map[string]*yaml.Node, name string) (string, error) {
	value, ok := fields[name]
	if !ok {
		return "", fmt.Errorf("%s is required", name)
	}
	return strictString(value, name, true)
}

func requiredStrictBool(fields map[string]*yaml.Node, name string) (*bool, error) {
	value, ok := fields[name]
	if !ok {
		return nil, fmt.Errorf("%s is required", name)
	}
	if value.Kind != yaml.ScalarNode || value.Tag != "!!bool" {
		return nil, fmt.Errorf("%s must be a boolean", name)
	}
	parsed, err := strconv.ParseBool(strings.ToLower(value.Value))
	if err != nil {
		return nil, fmt.Errorf("%s must be a boolean", name)
	}
	return &parsed, nil
}

func strictString(node *yaml.Node, name string, nonEmpty bool) (string, error) {
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return "", fmt.Errorf("%s must be a string", name)
	}
	value := strings.TrimSpace(node.Value)
	if nonEmpty && value == "" {
		return "", fmt.Errorf("%s must be non-empty", name)
	}
	return value, nil
}

func strictStringList(node *yaml.Node, name string) ([]string, error) {
	if node.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("%s must be a sequence of strings", name)
	}
	values := make([]string, 0, len(node.Content))
	seen := make(map[string]struct{}, len(node.Content))
	for _, item := range node.Content {
		value, err := strictString(item, name, true)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	return values, nil
}

func strictTime(node *yaml.Node, name string) (time.Time, error) {
	if node.Kind != yaml.ScalarNode || (node.Tag != "!!str" && node.Tag != "!!timestamp") || !strictDatePattern.MatchString(node.Value) {
		return time.Time{}, fmt.Errorf("%s must be RFC 3339 or YYYY-MM-DD", name)
	}
	if len(node.Value) == len("2006-01-02") {
		value, err := time.Parse("2006-01-02", node.Value)
		if err != nil {
			return time.Time{}, fmt.Errorf("%s must be RFC 3339 or YYYY-MM-DD", name)
		}
		return value.UTC(), nil
	}
	value, err := time.Parse(time.RFC3339Nano, node.Value)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s must be RFC 3339 or YYYY-MM-DD", name)
	}
	return value.UTC(), nil
}

func strictOrder(node *yaml.Node, name string) (*int, error) {
	if node.Kind != yaml.ScalarNode || node.Tag != "!!int" || !strictOrderPattern.MatchString(node.Value) {
		return nil, fmt.Errorf("%s must be a decimal integer from 0 to 2147483647", name)
	}
	value, err := strconv.ParseInt(node.Value, 10, 32)
	if err != nil || value < 0 || value > maxInt32 {
		return nil, fmt.Errorf("%s must be a decimal integer from 0 to 2147483647", name)
	}
	converted := int(value)
	return &converted, nil
}

func strictArticleSlug(node *yaml.Node) (string, error) {
	value, err := strictString(node, "slug", true)
	if err != nil {
		return "", err
	}
	if !strictSlugPattern.MatchString(value) {
		return "", fmt.Errorf("slug must be one non-empty Unicode path segment containing only letters, numbers, '-', '_' or '~'")
	}
	return value, nil
}
