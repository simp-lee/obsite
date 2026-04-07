package vault

import (
	"fmt"
	stdhtml "html"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	internalasset "github.com/simp-lee/obsite/internal/asset"
	"github.com/simp-lee/obsite/internal/diag"
	"github.com/simp-lee/obsite/internal/markdown"
	"github.com/simp-lee/obsite/internal/markdown/comment"
	"github.com/simp-lee/obsite/internal/markdown/math"
	"github.com/simp-lee/obsite/internal/model"
	"github.com/simp-lee/obsite/internal/slug"
	"github.com/yuin/goldmark"
	gast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	gmhashtag "go.abhg.dev/goldmark/hashtag"
	gmwikilink "go.abhg.dev/goldmark/wikilink"
)

const summaryRuneLimit = 150

type indexBuildOptions struct {
	concurrency int
	onNoteStart func(*model.Note)
	onNoteDone  func(*model.Note)
}

type indexedNoteResult struct {
	note   *model.Note
	assets map[string]*model.Asset
}

// BuildIndex completes pass 1 by assigning slugs, parsing AST metadata, and
// assembling the immutable VaultIndex handoff for later resolution and render steps.
func BuildIndex(scanResult ScanResult, frontmatterResult FrontmatterResult, diagCollector *diag.Collector) (*model.VaultIndex, error) {
	return buildIndexWithOptions(scanResult, frontmatterResult, diagCollector, indexBuildOptions{concurrency: 1})
}

// BuildIndexWithConcurrency applies bounded per-note concurrency during pass 1
// while preserving the shared parser and immutable VaultIndex handoff.
func BuildIndexWithConcurrency(scanResult ScanResult, frontmatterResult FrontmatterResult, diagCollector *diag.Collector, concurrency int) (*model.VaultIndex, error) {
	return buildIndexWithOptions(scanResult, frontmatterResult, diagCollector, indexBuildOptions{concurrency: concurrency})
}

func buildIndexWithOptions(scanResult ScanResult, frontmatterResult FrontmatterResult, diagCollector *diag.Collector, options indexBuildOptions) (*model.VaultIndex, error) {
	idx := &model.VaultIndex{
		AttachmentFolderPath: scanResult.AttachmentFolderPath,
		Notes:                make(map[string]*model.Note, len(frontmatterResult.PublicNotes)),
		NoteBySlug:           make(map[string]*model.Note, len(frontmatterResult.PublicNotes)),
		NoteByName:           make(map[string][]*model.Note),
		AliasByName:          make(map[string][]*model.Note),
		Tags:                 make(map[string]*model.Tag),
		Assets:               make(map[string]*model.Asset),
		Unpublished:          cloneUnpublishedLookup(frontmatterResult.Unpublished),
	}

	if err := assignSlugs(frontmatterResult.PublicNotes, diagCollector); err != nil {
		return nil, err
	}

	parser := markdown.NewParser(diagCollector)
	indexedNotes := indexPublicNotes(frontmatterResult.PublicNotes, scanResult, parser, diagCollector, options)
	for _, indexed := range indexedNotes {
		note := indexed.note
		if note == nil {
			continue
		}

		idx.Notes[note.RelPath] = note
		idx.NoteBySlug[note.Slug] = note
		idx.NoteByName[noteLookupName(note.RelPath)] = append(idx.NoteByName[noteLookupName(note.RelPath)], note)
		for _, alias := range note.Aliases {
			lookup := aliasLookupName(alias)
			if lookup == "" {
				continue
			}
			appendUnpublishedLookup(idx.AliasByName, lookup, note)
		}
		mergeIndexedAssets(idx.Assets, indexed.assets)
	}

	idx.Tags = buildTagIndex(frontmatterResult.PublicNotes)

	return idx, nil
}

func indexPublicNotes(
	notes []*model.Note,
	scanResult ScanResult,
	parser goldmark.Markdown,
	diagCollector *diag.Collector,
	options indexBuildOptions,
) []indexedNoteResult {
	if len(notes) == 0 {
		return nil
	}

	results := make([]indexedNoteResult, len(notes))
	workerCount := normalizeIndexConcurrency(options.concurrency, len(notes))
	if workerCount <= 1 {
		for index, note := range notes {
			results[index] = buildIndexedNoteResult(note, scanResult, parser, diagCollector, options)
		}
		return results
	}

	jobs := make(chan int)
	var workers sync.WaitGroup
	for worker := 0; worker < workerCount; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				results[index] = buildIndexedNoteResult(notes[index], scanResult, parser, diagCollector, options)
			}
		}()
	}

	for index := range notes {
		jobs <- index
	}
	close(jobs)
	workers.Wait()

	return results
}

func buildIndexedNoteResult(
	note *model.Note,
	scanResult ScanResult,
	parser goldmark.Markdown,
	diagCollector *diag.Collector,
	options indexBuildOptions,
) indexedNoteResult {
	if note == nil {
		return indexedNoteResult{}
	}

	if options.onNoteStart != nil {
		options.onNoteStart(note)
	}
	if options.onNoteDone != nil {
		defer options.onNoteDone(note)
	}

	note.RawContent = cloneBytes(comment.Strip(note.RawContent))
	note.Headings = nil
	note.HeadingSections = nil
	note.OutLinks = nil
	note.Embeds = nil
	note.HasMath = false
	note.HasMermaid = false

	assets := make(map[string]*model.Asset)
	root := parser.Parser().Parse(text.NewReader(note.RawContent))
	lineStarts := lineStartOffsets(note.RawContent)
	inlineTags := extractNoteMetadata(note, scanResult, assets, diagCollector, root, note.RawContent, lineStarts)
	note.Tags = mergeNoteTags(note.Tags, inlineTags)
	note.Summary = buildSummary(root, note.RawContent)

	return indexedNoteResult{note: note, assets: assets}
}

func normalizeIndexConcurrency(concurrency int, total int) int {
	if total <= 0 {
		return 0
	}
	if concurrency <= 0 {
		concurrency = runtime.NumCPU()
		if concurrency <= 0 {
			concurrency = 1
		}
	}
	if concurrency > total {
		return total
	}
	return concurrency
}

func mergeIndexedAssets(dst map[string]*model.Asset, src map[string]*model.Asset) {
	if len(src) == 0 {
		return
	}

	for srcPath, asset := range src {
		if srcPath == "" || asset == nil {
			continue
		}

		existing := dst[srcPath]
		if existing == nil {
			dst[srcPath] = &model.Asset{
				SrcPath:  srcPath,
				DstPath:  asset.DstPath,
				RefCount: asset.RefCount,
			}
			continue
		}

		existing.RefCount += asset.RefCount
		if existing.DstPath == "" {
			existing.DstPath = asset.DstPath
		}
	}
}

func assignSlugs(notes []*model.Note, diagCollector *diag.Collector) error {
	candidates := make([]slug.Candidate, 0, len(notes))
	for _, note := range notes {
		if note == nil {
			continue
		}

		var frontmatterSlug *string
		if note.Frontmatter.Slug != "" {
			frontmatterSlug = &note.Frontmatter.Slug
		}

		generated, err := slug.Generate(frontmatterSlug, note.RelPath)
		if err != nil {
			return fmt.Errorf("generate slug for %q: %w", note.RelPath, err)
		}

		note.Slug = generated
		candidates = append(candidates, slug.Candidate{Source: note.RelPath, Slug: generated})
	}

	conflicts, invalid := slug.DetectConflicts(candidates)
	if len(invalid) > 0 {
		return fmt.Errorf("invalid slug for %q", invalid[0].Source)
	}
	if len(conflicts) == 0 {
		return nil
	}

	for _, conflict := range conflicts {
		for _, source := range conflict.Sources {
			if diagCollector != nil {
				diagCollector.Errorf(diag.KindSlugConflict, diag.Location{Path: source}, "slug %q conflicts with %s", conflict.Slug, strings.Join(conflict.Sources, ", "))
			}
		}
	}

	first := conflicts[0]
	return fmt.Errorf("slug conflict for %q across %s", first.Slug, strings.Join(first.Sources, ", "))
}

func extractNoteMetadata(
	note *model.Note,
	scanResult ScanResult,
	assets map[string]*model.Asset,
	diagCollector *diag.Collector,
	root gast.Node,
	source []byte,
	lineStarts []int,
) []string {
	inlineTags := make([]string, 0)
	lineOffset := 0
	if note != nil && note.BodyStartLine > 1 {
		lineOffset = note.BodyStartLine - 1
	}

	_ = gast.Walk(root, func(node gast.Node, entering bool) (gast.WalkStatus, error) {
		if !entering {
			return gast.WalkContinue, nil
		}

		switch current := node.(type) {
		case *gast.Heading:
			note.Headings = append(note.Headings, model.Heading{
				Level: current.Level,
				Text:  markdown.VisibleHeadingText(current, source),
				ID:    headingID(current),
			})
		case *gmhashtag.Node:
			if tagName := normalizeTag(string(current.Tag)); tagName != "" {
				inlineTags = append(inlineTags, tagName)
			}
		case *gmwikilink.Node:
			if current.Embed {
				embedRef := extractEmbedRef(note, scanResult, current, source, lineStarts, lineOffset)
				note.Embeds = append(note.Embeds, embedRef)
				if embedRef.IsImage {
					if assetPath := resolveImageAssetPath(note, scanResult, embedRef.Target); assetPath != "" {
						registerAsset(assets, assetPath)
					}
				}
			} else {
				note.OutLinks = append(note.OutLinks, extractLinkRef(current, source, lineStarts, lineOffset))
			}
		case *gast.Image:
			rawDestination := string(current.Destination)
			if assetPath := resolveImageAssetPath(note, scanResult, rawDestination); assetPath != "" {
				registerAsset(assets, assetPath)
			} else {
				recordUnresolvedMarkdownImage(diagCollector, note, scanResult, rawDestination, current, lineStarts, lineOffset)
			}
			return gast.WalkSkipChildren, nil
		case *gast.FencedCodeBlock:
			if isMermaidFence(current.Language(source)) {
				note.HasMermaid = true
			}
		case *math.InlineMath, *math.DisplayMath:
			note.HasMath = true
		}

		return gast.WalkContinue, nil
	})

	note.HeadingSections = buildHeadingSections(root, source)

	return inlineTags
}

func extractLinkRef(node *gmwikilink.Node, source []byte, lineStarts []int, lineOffset int) model.LinkRef {
	offset, _ := nodeStartOffset(node)

	return model.LinkRef{
		RawTarget: composeRawTarget(string(node.Target), string(node.Fragment)),
		Display:   normalizeInlineText(string(node.Text(source))),
		Fragment:  strings.TrimSpace(string(node.Fragment)),
		Line:      lineNumberForNode(node, lineStarts, lineOffset),
		Offset:    offset,
	}
}

func extractEmbedRef(note *model.Note, scanResult ScanResult, node *gmwikilink.Node, source []byte, lineStarts []int, lineOffset int) model.EmbedRef {
	label := normalizeInlineText(string(node.Text(source)))
	target := strings.TrimSpace(string(node.Target))
	fragment := strings.TrimSpace(string(node.Fragment))
	isImage := looksLikeImageEmbed(note, scanResult, target)
	offset, _ := nodeStartOffset(node)

	width := 0
	if isImage {
		if parsed, err := strconv.Atoi(strings.TrimSpace(label)); err == nil && parsed > 0 {
			width = parsed
		}
	}

	return model.EmbedRef{
		Target:   target,
		Fragment: fragment,
		IsImage:  isImage,
		Width:    width,
		Line:     lineNumberForNode(node, lineStarts, lineOffset),
		Offset:   offset,
	}
}

func buildSummary(root gast.Node, source []byte) string {
	collector := newSummaryCollector()

	_ = gast.Walk(root, func(node gast.Node, entering bool) (gast.WalkStatus, error) {
		if entering {
			switch current := node.(type) {
			case *gast.Image:
				return gast.WalkSkipChildren, nil
			case *gast.CodeBlock, *gast.FencedCodeBlock:
				return gast.WalkSkipChildren, nil
			case *gast.HTMLBlock:
				collector.appendHTMLBlock(string(current.Text(source)))
				collector.space()
				return gast.WalkSkipChildren, nil
			case *gast.RawHTML:
				collector.applyRawHTML(string(current.Segments.Value(source)))
			case *gast.Text:
				collector.appendText(string(current.Value(source)))
				if current.SoftLineBreak() || current.HardLineBreak() {
					collector.space()
				}
			case *gast.String:
				collector.appendText(string(current.Value))
			case *gmhashtag.Node:
				collector.appendText("#" + string(current.Tag))
			case *math.InlineMath:
				collector.appendText(string(current.Literal))
			case *math.DisplayMath:
				collector.appendText(string(current.Literal))
				collector.space()
				return gast.WalkSkipChildren, nil
			}
		} else if node.Type() == gast.TypeBlock && node.Kind() != gast.KindDocument {
			collector.space()
		}

		return gast.WalkContinue, nil
	})

	return truncateSummary(normalizeSummaryWhitespace(collector.String()), summaryRuneLimit)
}

func registerAsset(assets map[string]*model.Asset, vaultRelPath string) {
	if vaultRelPath == "" {
		return
	}

	asset := assets[vaultRelPath]
	if asset == nil {
		asset = &model.Asset{SrcPath: vaultRelPath}
		assets[vaultRelPath] = asset
	}
	asset.RefCount++
}

func headingID(heading *gast.Heading) string {
	if heading == nil {
		return ""
	}

	value, ok := heading.AttributeString("id")
	if !ok {
		return ""
	}

	switch current := value.(type) {
	case []byte:
		return string(current)
	case string:
		return current
	default:
		return fmt.Sprint(current)
	}
}

type headingSectionEntry struct {
	level int
	id    string
	start int
}

func buildHeadingSections(root gast.Node, source []byte) map[string]model.SectionRange {
	entries := make([]headingSectionEntry, 0)

	_ = gast.Walk(root, func(node gast.Node, entering bool) (gast.WalkStatus, error) {
		if !entering {
			return gast.WalkContinue, nil
		}

		heading, ok := node.(*gast.Heading)
		if !ok {
			return gast.WalkContinue, nil
		}

		id := headingID(heading)
		start, ok := nodeStartOffset(heading)
		if !ok || id == "" {
			return gast.WalkContinue, nil
		}

		entries = append(entries, headingSectionEntry{
			level: heading.Level,
			id:    id,
			start: start,
		})

		return gast.WalkContinue, nil
	})

	if len(entries) == 0 {
		return nil
	}

	sections := make(map[string]model.SectionRange, len(entries))
	for i, entry := range entries {
		end := len(source)
		for j := i + 1; j < len(entries); j++ {
			if entries[j].level <= entry.level {
				end = entries[j].start
				break
			}
		}
		if end < entry.start {
			end = entry.start
		}

		sections[entry.id] = model.SectionRange{
			StartOffset: entry.start,
			EndOffset:   end,
		}
	}

	return sections
}

func composeRawTarget(target string, fragment string) string {
	target = strings.TrimSpace(target)
	fragment = strings.TrimSpace(fragment)
	if fragment == "" {
		return target
	}
	if target == "" {
		return "#" + fragment
	}
	return target + "#" + fragment
}

func normalizeInlineText(value string) string {
	return normalizeSummaryWhitespace(value)
}

func normalizeSummaryWhitespace(value string) string {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return ""
	}
	return strings.Join(fields, " ")
}

func truncateSummary(value string, limit int) string {
	value = strings.TrimSpace(value)
	if value == "" || limit <= 0 {
		return ""
	}
	if utf8.RuneCountInString(value) <= limit {
		return value
	}

	lastBoundary := 0
	firstBoundaryAfterLimit := 0
	runeCount := 0
	for index, r := range value {
		runeCount++
		end := index + utf8.RuneLen(r)
		if isSummaryBoundaryRune(r) {
			if runeCount <= limit {
				lastBoundary = end
			} else {
				firstBoundaryAfterLimit = end
				break
			}
		}

		if runeCount == limit && lastBoundary > 0 {
			return strings.TrimSpace(value[:lastBoundary])
		}
	}

	if lastBoundary > 0 {
		return strings.TrimSpace(value[:lastBoundary])
	}
	if firstBoundaryAfterLimit > 0 {
		return strings.TrimSpace(value[:firstBoundaryAfterLimit])
	}

	return value
}

func isSummaryBoundaryRune(r rune) bool {
	if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
		return true
	}
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
}

func looksLikeImageEmbed(note *model.Note, scanResult ScanResult, target string) bool {
	assetPath := resolveImageAssetPath(note, scanResult, target)
	return assetPath != "" && internalasset.HasImageExtension(assetPath)
}

func resolveImageAssetPath(note *model.Note, scanResult ScanResult, target string) string {
	return internalasset.ResolvePath(note, scanResult.AttachmentFolderPath, target, scanResult.HasResource)
}

func imageAssetCandidates(note *model.Note, scanResult ScanResult, target string) []string {
	return internalasset.CandidatePaths(note, scanResult.AttachmentFolderPath, target)
}

func recordUnresolvedMarkdownImage(
	diagCollector *diag.Collector,
	note *model.Note,
	scanResult ScanResult,
	rawTarget string,
	node gast.Node,
	lineStarts []int,
	lineOffset int,
) {
	if diagCollector == nil || note == nil || !shouldDiagnoseMarkdownImageTarget(note, scanResult, rawTarget) {
		return
	}

	diagCollector.Warningf(
		diag.KindUnresolvedAsset,
		diag.Location{Path: note.RelPath, Line: lineNumberForNode(node, lineStarts, lineOffset)},
		"markdown image %q could not be resolved to a publishable vault asset",
		strings.TrimSpace(rawTarget),
	)
}

func shouldDiagnoseMarkdownImageTarget(note *model.Note, scanResult ScanResult, rawTarget string) bool {
	for _, candidate := range imageAssetCandidates(note, scanResult, rawTarget) {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" || candidate == "." || candidate == ".." {
			continue
		}
		if strings.HasPrefix(candidate, "../") {
			continue
		}
		return true
	}

	return false
}

func lineStartOffsets(source []byte) []int {
	starts := []int{0}
	for index, b := range source {
		if b == '\n' && index+1 <= len(source) {
			starts = append(starts, index+1)
		}
	}
	return starts
}

func lineNumberForNode(node gast.Node, lineStarts []int, lineOffset int) int {
	start, ok := nodeStartOffset(node)
	if !ok {
		return 0
	}

	line := sort.Search(len(lineStarts), func(index int) bool {
		return lineStarts[index] > start
	})
	if line == 0 {
		return 1 + lineOffset
	}
	return line + lineOffset
}

func nodeStartOffset(node gast.Node) (int, bool) {
	if node == nil {
		return 0, false
	}

	switch current := node.(type) {
	case *gast.Text:
		return current.Segment.Start, true
	}

	if pos := node.Pos(); pos >= 0 {
		return pos, true
	}

	if node.Type() == gast.TypeBlock {
		if lines := node.Lines(); lines != nil && lines.Len() > 0 {
			return lines.At(0).Start, true
		}
	}
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		if start, ok := nodeStartOffset(child); ok {
			return start, true
		}
	}
	return 0, false
}

func cloneUnpublishedLookup(lookup model.UnpublishedLookup) model.UnpublishedLookup {
	cloned := model.UnpublishedLookup{
		Notes:       make(map[string]*model.Note, len(lookup.Notes)),
		NoteByName:  make(map[string][]*model.Note, len(lookup.NoteByName)),
		AliasByName: make(map[string][]*model.Note, len(lookup.AliasByName)),
	}

	for key, note := range lookup.Notes {
		cloned.Notes[key] = note
	}
	for key, notes := range lookup.NoteByName {
		cloned.NoteByName[key] = append([]*model.Note(nil), notes...)
	}
	for key, notes := range lookup.AliasByName {
		cloned.AliasByName[key] = append([]*model.Note(nil), notes...)
	}

	return cloned
}

func cloneBytes(src []byte) []byte {
	if len(src) == 0 {
		return nil
	}
	cloned := make([]byte, len(src))
	copy(cloned, src)
	return cloned
}

type summaryCollector struct {
	builder      strings.Builder
	pendingSpace bool
	hiddenTags   []string
}

func newSummaryCollector() *summaryCollector {
	return &summaryCollector{}
}

func (c *summaryCollector) String() string {
	return c.builder.String()
}

func (c *summaryCollector) space() {
	if c.builder.Len() == 0 {
		return
	}
	c.pendingSpace = true
}

func (c *summaryCollector) appendText(value string) {
	if c == nil || c.hidden() {
		return
	}
	if value == "" {
		return
	}
	if c.pendingSpace && c.builder.Len() > 0 {
		c.builder.WriteByte(' ')
	}
	c.pendingSpace = false
	c.builder.WriteString(value)
}

func (c *summaryCollector) appendHTMLBlock(fragment string) {
	if c == nil {
		return
	}
	if textValue := visibleHTMLText(fragment); textValue != "" {
		c.appendText(textValue)
	}
}

func (c *summaryCollector) applyRawHTML(fragment string) {
	if c == nil || fragment == "" {
		return
	}
	for _, token := range parseHTMLTokens(fragment) {
		c.applyHTMLToken(token)
	}
}

func (c *summaryCollector) applyHTMLToken(token htmlTagToken) {
	if token.name == "" {
		return
	}

	if token.name != "script" && token.name != "style" {
		if !c.hidden() && htmlTagBreaksText(token) {
			c.space()
		}
		return
	}
	if token.closing {
		for index := len(c.hiddenTags) - 1; index >= 0; index-- {
			if c.hiddenTags[index] != token.name {
				continue
			}
			c.hiddenTags = append(c.hiddenTags[:index], c.hiddenTags[index+1:]...)
			return
		}
		return
	}
	if token.selfClosing {
		return
	}
	c.hiddenTags = append(c.hiddenTags, token.name)
}

func (c *summaryCollector) hidden() bool {
	return len(c.hiddenTags) > 0
}

func visibleHTMLText(fragment string) string {
	if fragment == "" {
		return ""
	}

	collector := newSummaryCollector()
	hiddenTags := make([]string, 0)
	for index := 0; index < len(fragment); {
		open := strings.IndexByte(fragment[index:], '<')
		if open < 0 {
			if len(hiddenTags) == 0 {
				collector.appendText(stdhtml.UnescapeString(fragment[index:]))
			}
			break
		}
		open += index
		if open > index && len(hiddenTags) == 0 {
			collector.appendText(stdhtml.UnescapeString(fragment[index:open]))
		}

		next, token, ok := nextHTMLTagToken(fragment, open)
		if !ok {
			if len(hiddenTags) == 0 {
				collector.appendText(stdhtml.UnescapeString(fragment[open : open+1]))
			}
			index = open + 1
			continue
		}

		if token.name == "script" || token.name == "style" {
			if token.closing {
				for tagIndex := len(hiddenTags) - 1; tagIndex >= 0; tagIndex-- {
					if hiddenTags[tagIndex] != token.name {
						continue
					}
					hiddenTags = append(hiddenTags[:tagIndex], hiddenTags[tagIndex+1:]...)
					break
				}
			} else if !token.selfClosing {
				hiddenTags = append(hiddenTags, token.name)
			}
		}
		if len(hiddenTags) == 0 && htmlTagBreaksText(token) {
			collector.space()
		}

		index = next
	}

	return normalizeSummaryWhitespace(collector.String())
}

type htmlTagToken struct {
	name        string
	closing     bool
	selfClosing bool
}

var htmlTextBoundaryTags = map[string]struct{}{
	"address":    {},
	"article":    {},
	"aside":      {},
	"blockquote": {},
	"br":         {},
	"caption":    {},
	"dd":         {},
	"div":        {},
	"dl":         {},
	"dt":         {},
	"figcaption": {},
	"figure":     {},
	"footer":     {},
	"form":       {},
	"h1":         {},
	"h2":         {},
	"h3":         {},
	"h4":         {},
	"h5":         {},
	"h6":         {},
	"header":     {},
	"hr":         {},
	"li":         {},
	"main":       {},
	"nav":        {},
	"ol":         {},
	"p":          {},
	"pre":        {},
	"section":    {},
	"table":      {},
	"tbody":      {},
	"td":         {},
	"tfoot":      {},
	"th":         {},
	"thead":      {},
	"tr":         {},
	"ul":         {},
}

func htmlTagBreaksText(token htmlTagToken) bool {
	_, ok := htmlTextBoundaryTags[token.name]
	return ok
}

func parseHTMLTokens(fragment string) []htmlTagToken {
	tokens := make([]htmlTagToken, 0)
	for index := 0; index < len(fragment); {
		open := strings.IndexByte(fragment[index:], '<')
		if open < 0 {
			break
		}
		open += index
		next, token, ok := nextHTMLTagToken(fragment, open)
		if !ok {
			index = open + 1
			continue
		}
		tokens = append(tokens, token)
		index = next
	}
	return tokens
}

func nextHTMLTagToken(fragment string, start int) (int, htmlTagToken, bool) {
	if start < 0 || start >= len(fragment) || fragment[start] != '<' {
		return 0, htmlTagToken{}, false
	}
	if strings.HasPrefix(fragment[start:], "<!--") {
		end := strings.Index(fragment[start+4:], "-->")
		if end < 0 {
			return len(fragment), htmlTagToken{}, true
		}
		return start + 4 + end + 3, htmlTagToken{}, true
	}

	end, ok := findTagEnd(fragment, start+1)
	if !ok {
		return 0, htmlTagToken{}, false
	}

	inner := strings.TrimSpace(fragment[start+1 : end])
	if inner == "" || inner[0] == '!' || inner[0] == '?' {
		return end + 1, htmlTagToken{}, true
	}

	token := htmlTagToken{}
	if inner[0] == '/' {
		token.closing = true
		inner = strings.TrimSpace(inner[1:])
	}
	if strings.HasSuffix(inner, "/") {
		token.selfClosing = true
		inner = strings.TrimSpace(inner[:len(inner)-1])
	}
	if inner == "" {
		return end + 1, token, true
	}

	nameEnd := 0
	for nameEnd < len(inner) {
		r, size := utf8.DecodeRuneInString(inner[nameEnd:])
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == ':' || r == '-' || r == '_') {
			break
		}
		nameEnd += size
	}
	if nameEnd == 0 {
		return end + 1, token, true
	}

	token.name = strings.ToLower(inner[:nameEnd])
	return end + 1, token, true
}

func findTagEnd(fragment string, start int) (int, bool) {
	quote := rune(0)
	for index := start; index < len(fragment); index++ {
		current := rune(fragment[index])
		switch {
		case quote != 0 && current == quote:
			quote = 0
		case quote == 0 && (current == '\'' || current == '"'):
			quote = current
		case quote == 0 && current == '>':
			return index, true
		}
	}
	return 0, false
}

func isMermaidFence(language []byte) bool {
	return strings.EqualFold(strings.TrimSpace(string(language)), "mermaid")
}
