package vault

import (
	"fmt"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/simp-lee/obsite/internal/diag"
	"github.com/simp-lee/obsite/internal/markdown"
	"github.com/simp-lee/obsite/internal/markdown/comment"
	"github.com/simp-lee/obsite/internal/markdown/math"
	"github.com/simp-lee/obsite/internal/model"
	"github.com/simp-lee/obsite/internal/resourcepath"
	"github.com/simp-lee/obsite/internal/slug"
	"github.com/yuin/goldmark"
	gast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	gmhashtag "go.abhg.dev/goldmark/hashtag"
	gmwikilink "go.abhg.dev/goldmark/wikilink"
)

type indexBuildOptions struct {
	concurrency int
	onNoteStart func(*model.Note)
	onNoteDone  func(*model.Note)
}

type indexedNoteResult struct {
	note   *model.Note
	assets map[string]*model.Asset
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

	idx.SetAssets(idx.Assets)
	idx.SetResources(scanResult.ResourceFiles)
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
	note.HTMLContent = ""
	note.Summary = ""
	note.Headings = nil
	note.HeadingSections = nil
	note.OutLinks = nil
	note.Embeds = nil
	note.ImageRefs = nil
	note.HasMath = false
	note.HasMermaid = false

	assets := make(map[string]*model.Asset)
	root := parser.Parser().Parse(text.NewReader(note.RawContent))
	lineStarts := lineStartOffsets(note.RawContent)
	inlineTags := extractNoteMetadata(note, scanResult, assets, diagCollector, root, note.RawContent, lineStarts)
	note.Tags = mergeNoteTags(note.Tags, inlineTags)

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
			imageRef := extractImageRef(current, lineStarts, lineOffset)
			note.ImageRefs = append(note.ImageRefs, imageRef)
			rawDestination := imageRef.RawTarget
			lookup := lookupImageAssetPath(note, scanResult, rawDestination)
			if lookup.Path != "" {
				registerAsset(assets, lookup.Path)
			} else if len(lookup.Ambiguous) > 0 {
				recordAmbiguousMarkdownImage(diagCollector, note, rawDestination, lookup.Ambiguous, current, lineStarts, lineOffset)
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
		Display:   normalizeInlineText(wikilinkNodeText(source, node)),
		Fragment:  strings.TrimSpace(string(node.Fragment)),
		Line:      lineNumberForNode(node, lineStarts, lineOffset),
		Offset:    offset,
	}
}

func extractImageRef(node *gast.Image, lineStarts []int, lineOffset int) model.ImageRef {
	offset, _ := nodeStartOffset(node)

	return model.ImageRef{
		RawTarget: strings.TrimSpace(string(node.Destination)),
		Line:      lineNumberForNode(node, lineStarts, lineOffset),
		Offset:    offset,
	}
}

func extractEmbedRef(note *model.Note, scanResult ScanResult, node *gmwikilink.Node, source []byte, lineStarts []int, lineOffset int) model.EmbedRef {
	label := normalizeInlineText(wikilinkNodeText(source, node))
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

func wikilinkNodeText(source []byte, node gast.Node) string {
	var builder strings.Builder
	appendWikilinkNodeText(&builder, source, node)
	return builder.String()
}

func appendWikilinkNodeText(builder *strings.Builder, source []byte, node gast.Node) {
	if builder == nil || node == nil {
		return
	}

	switch current := node.(type) {
	case *gast.Text:
		_, _ = builder.Write(current.Value(source))
		if current.SoftLineBreak() || current.HardLineBreak() {
			_ = builder.WriteByte('\n')
		}
	case *gast.String:
		_, _ = builder.Write(current.Value)
	case *gast.RawHTML:
		_, _ = builder.Write(current.Segments.Value(source))
	case *gast.AutoLink:
		_, _ = builder.Write(current.Label(source))
	default:
		for child := node.FirstChild(); child != nil; child = child.NextSibling() {
			appendWikilinkNodeText(builder, source, child)
		}
	}
}

func normalizeSummaryWhitespace(value string) string {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return ""
	}
	return strings.Join(fields, " ")
}

func looksLikeImageEmbed(note *model.Note, scanResult ScanResult, target string) bool {
	lookup := lookupImageAssetPath(note, scanResult, target)
	return lookup.Path != "" && resourcepath.LooksLikeImage(lookup.Path)
}

func resolveImageAssetPath(note *model.Note, scanResult ScanResult, target string) string {
	return lookupImageAssetPath(note, scanResult, target).Path
}

func lookupImageAssetPath(note *model.Note, scanResult ScanResult, target string) model.PathLookupResult {
	return resourcepath.LookupPath(note, scanResult.AttachmentFolderPath, target, scanResult.LookupResourcePath)
}

func imageAssetCandidates(note *model.Note, scanResult ScanResult, target string) []string {
	return resourcepath.CandidatePathsWithAttachmentFolder(note, scanResult.AttachmentFolderPath, target)
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

func recordAmbiguousMarkdownImage(
	diagCollector *diag.Collector,
	note *model.Note,
	rawTarget string,
	ambiguous []string,
	node gast.Node,
	lineStarts []int,
	lineOffset int,
) {
	if diagCollector == nil || note == nil || len(ambiguous) == 0 {
		return
	}

	diagCollector.Warningf(
		diag.KindUnresolvedAsset,
		diag.Location{Path: note.RelPath, Line: lineNumberForNode(node, lineStarts, lineOffset)},
		"markdown image %q matched multiple publishable vault assets after canonical path normalization (%s); refusing canonical fallback",
		strings.TrimSpace(rawTarget),
		strings.Join(ambiguous, ", "),
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

func isMermaidFence(language []byte) bool {
	return strings.EqualFold(strings.TrimSpace(string(language)), "mermaid")
}
