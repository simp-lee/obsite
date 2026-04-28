package wikilink

import (
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/simp-lee/obsite/internal/diag"
	"github.com/simp-lee/obsite/internal/markdown/headingid"
	"github.com/simp-lee/obsite/internal/model"
	"github.com/simp-lee/obsite/internal/resourcepath"
	"github.com/simp-lee/obsite/internal/slug"
	gmwikilink "go.abhg.dev/goldmark/wikilink"
)

const (
	kindAmbiguousWikilink   diag.Kind = "ambiguous_wikilink"
	kindUnpublishedWikilink diag.Kind = "unpublished_wikilink"
)

// VaultResolver resolves Obsidian wikilinks against the immutable pass-1 index.
type VaultResolver struct {
	Index           *model.VaultIndex
	CurrentNote     *model.Note
	OutputNote      *model.Note
	HeadingIDPrefix string
	Diag            *diag.Collector
	outLinks        []model.LinkRef

	nextOutLink int
}

// LookupResult describes a resolved note target without applying render-time side effects.
type LookupResult struct {
	Note            *model.Note
	FragmentID      string
	Ambiguous       []string
	CanvasResource  bool
	Unpublished     bool
	MissingFragment bool
}

// NewRenderVaultResolver builds a render-time resolver with separate source and output-note contexts.
func NewRenderVaultResolver(idx *model.VaultIndex, sourceNote *model.Note, outputNote *model.Note, headingIDPrefix string, diagCollector *diag.Collector) *VaultResolver {
	if outputNote == nil {
		outputNote = sourceNote
	}

	return &VaultResolver{
		Index:           idx,
		CurrentNote:     sourceNote,
		OutputNote:      outputNote,
		HeadingIDPrefix: headingIDPrefix,
		Diag:            diagCollector,
		outLinks:        cloneOutLinks(sourceNote),
	}
}

// OutLinks returns the render-local resolved outlink state for the current note.
func (r *VaultResolver) OutLinks() []model.LinkRef {
	if r == nil || len(r.outLinks) == 0 {
		return nil
	}

	return append([]model.LinkRef(nil), r.outLinks...)
}

// ResolveWikilink implements go.abhg.dev/goldmark/wikilink.Resolver.
func (r *VaultResolver) ResolveWikilink(node *gmwikilink.Node) ([]byte, error) {
	if node == nil {
		return nil, nil
	}

	rawTarget := composeRawTarget(string(node.Target), string(node.Fragment))
	sourceRef := r.consumeOutLink(node.Embed, rawTarget)

	target := strings.TrimSpace(string(node.Target))
	fragment := strings.TrimSpace(string(node.Fragment))
	lookup := LookupTarget(r.Index, r.CurrentNote, target, fragment)
	if len(lookup.Ambiguous) > 1 {
		r.recordAmbiguous(rawTarget, sourceRef, lookup.Note, lookup.Ambiguous)
	}

	switch {
	case lookup.Note == nil && lookup.CanvasResource:
		if len(lookup.Ambiguous) > 0 {
			r.recordAmbiguousCanvas(rawTarget, sourceRef, lookup.Ambiguous)
		} else {
			r.recordUnsupportedCanvas(rawTarget, sourceRef)
		}
		return nil, nil
	case lookup.Note == nil:
		r.recordDeadLink(rawTarget, sourceRef)
		return nil, nil
	case lookup.Unpublished:
		r.recordUnpublished(rawTarget, sourceRef, lookup.Note)
		return nil, nil
	case lookup.MissingFragment:
		r.recordMissingFragment(rawTarget, sourceRef, lookup.Note, fragment)
		return nil, nil
	default:
		r.markResolved(sourceRef, lookup.Note)
		return []byte(buildNoteHref(r.OutputNote, r.CurrentNote, lookup.Note, lookup.FragmentID, r.HeadingIDPrefix)), nil
	}
}

// LookupTarget resolves a note target using the same best-match rules as the render-time resolver.
func LookupTarget(idx *model.VaultIndex, current *model.Note, target string, fragment string) LookupResult {
	resolver := &VaultResolver{Index: idx, CurrentNote: current}
	return resolver.lookup(strings.TrimSpace(target), strings.TrimSpace(fragment))
}

type resolutionResult struct {
	note      *model.Note
	ambiguous []string
}

type rankedCandidate struct {
	note         *model.Note
	distance     int
	sharedPrefix int
}

func (r *VaultResolver) resolvePublic(target string) resolutionResult {
	if r == nil || r.Index == nil {
		return resolutionResult{}
	}

	filenameCandidates := uniqueNotes(r.Index.NoteByName[noteLookupKey(target)])
	if len(filenameCandidates) == 1 {
		return resolutionResult{note: filenameCandidates[0]}
	}

	aliasCandidates := uniqueNotes(r.Index.AliasByName[aliasLookupKey(target)])
	if len(aliasCandidates) == 1 {
		return resolutionResult{note: aliasCandidates[0]}
	}

	if result := resolveCandidateSet(r.CurrentNote, filenameCandidates); result.note != nil {
		return result
	}

	return resolveCandidateSet(r.CurrentNote, aliasCandidates)
}

func (r *VaultResolver) resolveUnpublished(target string) resolutionResult {
	if r == nil || r.Index == nil {
		return resolutionResult{}
	}

	filenameCandidates := uniqueNotes(r.Index.Unpublished.NoteByName[noteLookupKey(target)])
	if len(filenameCandidates) == 1 {
		return resolutionResult{note: filenameCandidates[0]}
	}

	aliasCandidates := uniqueNotes(r.Index.Unpublished.AliasByName[aliasLookupKey(target)])
	if len(aliasCandidates) == 1 {
		return resolutionResult{note: aliasCandidates[0]}
	}

	if result := resolveCandidateSet(r.CurrentNote, filenameCandidates); result.note != nil {
		return result
	}

	return resolveCandidateSet(r.CurrentNote, aliasCandidates)
}

func (r *VaultResolver) exactPublicPathMatch(target string) *model.Note {
	if r == nil || r.Index == nil {
		return nil
	}
	return exactPathMatch(target, r.Index.Notes)
}

func (r *VaultResolver) exactUnpublishedPathMatch(target string) *model.Note {
	if r == nil || r.Index == nil {
		return nil
	}
	return exactPathMatch(target, r.Index.Unpublished.Notes)
}

func (r *VaultResolver) lookup(target string, fragment string) LookupResult {
	if target == "" {
		if r == nil || r.CurrentNote == nil {
			return LookupResult{}
		}
		return finalizeLookup(resolutionResult{note: r.CurrentNote}, fragment)
	}

	if explicitPathTarget(target) {
		if note := r.exactPublicPathMatch(target); note != nil {
			return finalizeLookup(resolutionResult{note: note}, fragment)
		}
		if note := r.exactUnpublishedPathMatch(target); note != nil {
			result := finalizeLookup(resolutionResult{note: note}, fragment)
			result.Unpublished = true
			return result
		}
		return canvasLookupResult(r.Index, r.CurrentNote, target)
	}

	if result := r.resolvePublic(target); result.note != nil {
		return finalizeLookup(result, fragment)
	}

	if result := r.resolveUnpublished(target); result.note != nil {
		lookup := finalizeLookup(result, fragment)
		lookup.Unpublished = true
		return lookup
	}

	return canvasLookupResult(r.Index, r.CurrentNote, target)
}

func finalizeLookup(result resolutionResult, fragment string) LookupResult {
	lookup := LookupResult{
		Note:      result.note,
		Ambiguous: append([]string(nil), result.ambiguous...),
	}
	if result.note == nil {
		return lookup
	}

	fragmentID, ok := resolveFragmentID(result.note, fragment)
	if !ok {
		lookup.MissingFragment = strings.TrimSpace(fragment) != ""
		return lookup
	}

	lookup.FragmentID = fragmentID
	return lookup
}

func resolveCandidateSet(current *model.Note, candidates []*model.Note) resolutionResult {
	filtered := uniqueNotes(candidates)
	if len(filtered) == 0 {
		return resolutionResult{}
	}
	if len(filtered) == 1 {
		return resolutionResult{note: filtered[0]}
	}

	ranked := make([]rankedCandidate, 0, len(filtered))
	for _, note := range filtered {
		ranked = append(ranked, rankCandidate(current, note))
	}

	sort.Slice(ranked, func(i, j int) bool {
		left := ranked[i]
		right := ranked[j]

		if left.distance != right.distance {
			return left.distance < right.distance
		}
		if left.sharedPrefix != right.sharedPrefix {
			return left.sharedPrefix > right.sharedPrefix
		}
		return left.note.RelPath < right.note.RelPath
	})

	result := resolutionResult{note: ranked[0].note}
	for _, candidate := range ranked {
		if candidate.distance != ranked[0].distance || candidate.sharedPrefix != ranked[0].sharedPrefix {
			break
		}
		result.ambiguous = append(result.ambiguous, candidate.note.RelPath)
	}

	if len(result.ambiguous) == 1 {
		result.ambiguous = nil
	}

	return result
}

func uniqueNotes(candidates []*model.Note) []*model.Note {
	if len(candidates) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(candidates))
	filtered := make([]*model.Note, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate == nil {
			continue
		}
		if _, ok := seen[candidate.RelPath]; ok {
			continue
		}
		seen[candidate.RelPath] = struct{}{}
		filtered = append(filtered, candidate)
	}

	return filtered
}

func rankCandidate(current *model.Note, candidate *model.Note) rankedCandidate {
	candidatePath := normalizeVaultPath(candidate.RelPath)
	if current == nil {
		return rankedCandidate{
			note:         candidate,
			distance:     pathSegmentCount(candidatePath),
			sharedPrefix: 0,
		}
	}

	currentDir := noteSourceDir(current)
	relativePath, err := filepath.Rel(currentDir, candidatePath)
	if err != nil {
		relativePath = candidatePath
	}

	return rankedCandidate{
		note:         candidate,
		distance:     pathSegmentCount(filepath.ToSlash(relativePath)),
		sharedPrefix: sharedPathPrefixDepth(currentDir, path.Dir(candidatePath)),
	}
}

func exactPathMatch(target string, notes map[string]*model.Note) *model.Note {
	for _, candidate := range candidatePaths(target) {
		if note := notes[candidate]; note != nil {
			return note
		}
	}

	canonicalCandidates := canonicalCandidatePaths(target)
	if len(canonicalCandidates) == 0 {
		return nil
	}

	var matched *model.Note
	for _, note := range notes {
		if note == nil {
			continue
		}
		if _, ok := canonicalCandidates[model.CanonicalResourceLookupPath(note.RelPath)]; !ok {
			continue
		}
		if matched != nil && matched.RelPath != note.RelPath {
			return nil
		}
		matched = note
	}

	return matched
}

func canonicalCandidatePaths(target string) map[string]struct{} {
	paths := candidatePaths(target)
	if len(paths) == 0 {
		return nil
	}

	canonical := make(map[string]struct{}, len(paths))
	for _, candidate := range paths {
		key := model.CanonicalResourceLookupPath(candidate)
		if key == "" {
			continue
		}
		canonical[key] = struct{}{}
	}
	if len(canonical) == 0 {
		return nil
	}

	return canonical
}

func candidatePaths(target string) []string {
	normalized := normalizeVaultPath(target)
	if normalized == "" {
		return nil
	}

	paths := []string{normalized}
	if !strings.HasSuffix(strings.ToLower(normalized), ".md") {
		paths = append(paths, normalized+".md")
	}

	return paths
}

func explicitPathTarget(target string) bool {
	normalized := strings.TrimSpace(strings.ReplaceAll(target, `\`, "/"))
	return strings.Contains(normalized, "/")
}

func noteLookupKey(target string) string {
	base := path.Base(normalizeVaultPath(target))
	if strings.EqualFold(path.Ext(base), ".md") {
		base = base[:len(base)-len(path.Ext(base))]
	}
	return slug.Canonicalize(strings.TrimSpace(base))
}

func aliasLookupKey(target string) string {
	return slug.Canonicalize(strings.TrimSpace(target))
}

func buildNoteHref(output *model.Note, source *model.Note, target *model.Note, fragment string, headingIDPrefix string) string {
	if target == nil {
		return ""
	}
	if source != nil && source.RelPath == target.RelPath && fragment != "" {
		if headingIDPrefix != "" {
			return "#" + headingIDPrefix + fragment
		}
		return "#" + fragment
	}
	if output != nil && output.RelPath == target.RelPath {
		if fragment != "" {
			return "#" + fragment
		}
		return "./"
	}
	if source != nil && source.RelPath == target.RelPath {
		return buildOutputHref(output, target)
	}

	href := buildOutputHref(output, target)
	if fragment != "" {
		href += "#" + fragment
	}

	return href
}

// BuildNoteHref exposes the render-time href builder without mutating resolver state.
func BuildNoteHref(output *model.Note, source *model.Note, target *model.Note, fragment string, headingIDPrefix string) string {
	return buildNoteHref(output, source, target, fragment, headingIDPrefix)
}

func buildOutputHref(output *model.Note, target *model.Note) string {
	href := relativeToNoteOutput(output, target.Slug)
	if href == "" || href == "." {
		return "./"
	}
	return href + "/"
}

func relativeToNoteOutput(note *model.Note, siteRelPath string) string {
	normalized := normalizeSitePath(siteRelPath)
	if normalized == "" {
		return ""
	}

	relativePath, err := filepath.Rel(noteOutputDir(note), normalized)
	if err != nil {
		return normalized
	}

	return filepath.ToSlash(relativePath)
}

func noteOutputDir(note *model.Note) string {
	if note == nil {
		return "."
	}

	slug := strings.Trim(strings.ReplaceAll(note.Slug, `\`, "/"), "/")
	if slug == "" {
		return "."
	}

	return path.Clean(slug)
}

func noteSourceDir(note *model.Note) string {
	if note == nil {
		return "."
	}

	relPath := normalizeVaultPath(note.RelPath)
	if relPath == "" {
		return "."
	}

	return path.Dir(relPath)
}

func normalizeSitePath(value string) string {
	cleaned := strings.TrimSpace(strings.ReplaceAll(value, `\`, "/"))
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

func normalizeVaultPath(value string) string {
	cleaned := strings.TrimSpace(strings.ReplaceAll(value, `\`, "/"))
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

func pathSegmentCount(value string) int {
	cleaned := path.Clean(strings.TrimSpace(strings.ReplaceAll(value, `\`, "/")))
	if cleaned == "" || cleaned == "." {
		return 0
	}
	return len(strings.Split(cleaned, "/"))
}

func sharedPathPrefixDepth(left string, right string) int {
	leftParts := splitPathParts(left)
	rightParts := splitPathParts(right)

	depth := 0
	for depth < len(leftParts) && depth < len(rightParts) {
		if leftParts[depth] != rightParts[depth] {
			break
		}
		depth++
	}

	return depth
}

func splitPathParts(value string) []string {
	cleaned := normalizeVaultPath(value)
	if cleaned == "" || cleaned == "." {
		return nil
	}
	return strings.Split(cleaned, "/")
}

func resolveFragmentID(note *model.Note, fragment string) (string, bool) {
	fragment = normalizeHeadingWhitespace(fragment)
	if fragment == "" {
		return "", true
	}

	if note == nil {
		return "", false
	}

	canonicalFragment := headingid.CanonicalText(fragment)
	normalizedID := normalizeHeadingID(fragment)

	for _, heading := range note.Headings {
		if headingid.CanonicalText(strings.TrimSpace(heading.ID)) == canonicalFragment {
			return heading.ID, true
		}
	}
	for _, heading := range note.Headings {
		if headingid.CanonicalText(heading.Text) == canonicalFragment {
			return heading.ID, true
		}
	}
	for _, heading := range note.Headings {
		if normalizeHeadingID(strings.TrimSpace(heading.ID)) == normalizedID {
			return heading.ID, true
		}
	}

	return "", false
}

func normalizeHeadingID(value string) string {
	return headingid.Normalize(value)
}

func normalizeHeadingWhitespace(value string) string {
	return headingid.NormalizeWhitespace(value)
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

// IsCanvasTarget reports whether a wikilink target refers to an Obsidian canvas file.
func IsCanvasTarget(target string) bool {
	normalized := strings.TrimSpace(strings.ReplaceAll(target, `\`, "/"))
	return strings.EqualFold(path.Ext(normalized), ".canvas")
}

func canvasLookupResult(idx *model.VaultIndex, current *model.Note, target string) LookupResult {
	lookup := lookupCanvasResource(idx, current, target)
	if lookup.Path != "" || len(lookup.Ambiguous) > 0 {
		return LookupResult{CanvasResource: true, Ambiguous: lookup.Ambiguous}
	}

	return LookupResult{}
}

func lookupCanvasResource(idx *model.VaultIndex, current *model.Note, target string) model.PathLookupResult {
	if idx == nil || !IsCanvasTarget(target) {
		return model.PathLookupResult{}
	}

	lookup := resourcepath.LookupPath(current, idx.AttachmentFolderPath, target, idx.LookupResourcePath)
	if lookup.Path != "" || len(lookup.Ambiguous) > 0 {
		return lookup
	}

	normalized := strings.TrimSpace(strings.ReplaceAll(target, `\`, "/"))
	normalized = strings.TrimPrefix(normalized, "/")
	if normalized == "" {
		return model.PathLookupResult{}
	}
	if strings.Contains(normalized, "/") {
		if strings.HasPrefix(normalized, "./") || strings.HasPrefix(normalized, "../") {
			return model.PathLookupResult{}
		}
		return idx.LookupResourcePath(normalized)
	}

	return idx.LookupResourceBaseName(normalized)
}

func (r *VaultResolver) consumeOutLink(embed bool, rawTarget string) *model.LinkRef {
	if r == nil || embed || r.CurrentNote == nil {
		return nil
	}

	normalized := strings.TrimSpace(rawTarget)
	for i := r.nextOutLink; i < len(r.outLinks); i++ {
		if !strings.EqualFold(strings.TrimSpace(r.outLinks[i].RawTarget), normalized) {
			continue
		}
		r.nextOutLink = i + 1
		return &r.outLinks[i]
	}

	return nil
}

func (r *VaultResolver) markResolved(ref *model.LinkRef, note *model.Note) {
	if ref == nil || note == nil {
		return
	}
	ref.ResolvedRelPath = note.RelPath
}

func (r *VaultResolver) recordDeadLink(rawTarget string, ref *model.LinkRef) {
	if r == nil || r.Diag == nil {
		return
	}

	r.Diag.Warningf(diag.KindDeadLink, r.location(ref), "wikilink %q could not be resolved", rawTarget)
}

func (r *VaultResolver) recordUnsupportedCanvas(rawTarget string, ref *model.LinkRef) {
	if r == nil || r.Diag == nil {
		return
	}

	r.Diag.Warningf(diag.KindUnsupportedSyntax, r.location(ref), "wikilink %q targets unsupported canvas content; rendering as plain text", rawTarget)
}

func (r *VaultResolver) recordAmbiguousCanvas(rawTarget string, ref *model.LinkRef, candidates []string) {
	if r == nil || r.Diag == nil || len(candidates) == 0 {
		return
	}

	r.Diag.Warningf(diag.KindUnsupportedSyntax, r.location(ref), "wikilink %q matched multiple canvas resources after canonical lookup (%s); refusing canonical fallback and rendering as plain text", rawTarget, strings.Join(candidates, ", "))
}

func (r *VaultResolver) recordUnpublished(rawTarget string, ref *model.LinkRef, note *model.Note) {
	if r == nil || r.Diag == nil || note == nil {
		return
	}

	r.Diag.Warningf(kindUnpublishedWikilink, r.location(ref), "wikilink %q points to unpublished note %q; rendering as plain text", rawTarget, note.RelPath)
}

func (r *VaultResolver) recordMissingFragment(rawTarget string, ref *model.LinkRef, note *model.Note, fragment string) {
	if r == nil || r.Diag == nil || note == nil {
		return
	}

	missing := normalizeHeadingWhitespace(fragment)
	if missing == "" {
		r.recordDeadLink(rawTarget, ref)
		return
	}

	r.Diag.Warningf(diag.KindDeadLink, r.location(ref), "wikilink %q points to missing heading %q in %q", rawTarget, missing, note.RelPath)
}

func (r *VaultResolver) recordAmbiguous(rawTarget string, ref *model.LinkRef, chosen *model.Note, candidates []string) {
	if r == nil || r.Diag == nil || chosen == nil || len(candidates) == 0 {
		return
	}

	r.Diag.Warningf(kindAmbiguousWikilink, r.location(ref), "wikilink %q matched multiple notes at the same path distance (%s); choosing %q", rawTarget, strings.Join(candidates, ", "), chosen.RelPath)
}

func (r *VaultResolver) location(ref *model.LinkRef) diag.Location {
	location := diag.Location{}
	if r != nil && r.CurrentNote != nil {
		location.Path = r.CurrentNote.RelPath
	}
	if ref != nil {
		location.Line = ref.Line
	}
	return location
}

func cloneOutLinks(note *model.Note) []model.LinkRef {
	if note == nil || len(note.OutLinks) == 0 {
		return nil
	}

	return append([]model.LinkRef(nil), note.OutLinks...)
}
