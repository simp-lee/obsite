// Package recommend computes static related-article rankings for public notes.
package recommend

import (
	"fmt"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/simp-lee/obsite/internal/model"
)

const (
	sharedTagBoostWeight = 0.75
	mutualWikilinkBoost  = 1.25
)

// RankedNote contains one related-note candidate and its final ranking score.
type RankedNote struct {
	Note  *model.Note
	Score float64
}

type documentProfile struct {
	note       *model.Note
	queryTerms []bm25QueryTerm
	tags       map[string]struct{}
	candidates []string
}

// Recommender holds the prebuilt corpus and note metadata used for ranking.
type Recommender struct {
	idx    *model.VaultIndex
	graph  *model.LinkGraph
	bm25   *BM25
	docMap map[string]documentProfile
	paths  []string
	mutual map[string]map[string]struct{}
}

// Build computes the top-N related notes for every public note in the index.
func Build(idx *model.VaultIndex, graph *model.LinkGraph, limit int) (map[string][]RankedNote, error) {
	if idx == nil || len(idx.Notes) == 0 || limit <= 0 {
		return map[string][]RankedNote{}, nil
	}

	recommender, err := New(idx, graph)
	if err != nil {
		return nil, err
	}

	relatedByIndex := make([][]RankedNote, len(recommender.paths))
	workerCount := buildWorkerCount(len(recommender.paths))
	if workerCount == 1 {
		for pathIndex, relPath := range recommender.paths {
			note := idx.Notes[relPath]
			if note == nil {
				continue
			}
			relatedByIndex[pathIndex] = recommender.Related(note, limit)
		}
	} else {
		jobs := make(chan int)
		var waitGroup sync.WaitGroup
		for worker := 0; worker < workerCount; worker++ {
			waitGroup.Add(1)
			go func() {
				defer waitGroup.Done()
				for pathIndex := range jobs {
					relPath := recommender.paths[pathIndex]
					note := idx.Notes[relPath]
					if note == nil {
						continue
					}
					relatedByIndex[pathIndex] = recommender.Related(note, limit)
				}
			}()
		}

		for pathIndex := range recommender.paths {
			jobs <- pathIndex
		}
		close(jobs)
		waitGroup.Wait()
	}

	relatedByPath := make(map[string][]RankedNote, len(recommender.paths))
	for pathIndex, relPath := range recommender.paths {
		if idx.Notes[relPath] == nil {
			continue
		}
		relatedByPath[relPath] = relatedByIndex[pathIndex]
	}

	return relatedByPath, nil
}

// New constructs a reusable recommender over the current public-note corpus.
func New(idx *model.VaultIndex, graph *model.LinkGraph) (*Recommender, error) {
	if idx == nil || len(idx.Notes) == 0 {
		return &Recommender{
			idx:    idx,
			graph:  graph,
			bm25:   newBM25(nil, nil),
			docMap: map[string]documentProfile{},
		}, nil
	}

	paths := sortedPublicNotePaths(idx)
	tokenLists := make(map[string][]string, len(paths))
	docMap := make(map[string]documentProfile, len(paths))
	for _, relPath := range paths {
		note := idx.Notes[relPath]
		if note == nil {
			continue
		}

		tokens, err := Tokenize(string(note.RawContent))
		if err != nil {
			return nil, fmt.Errorf("tokenize %q: %w", relPath, err)
		}

		tokenLists[relPath] = tokens
		tags := tagSet(note.Tags)
		docMap[relPath] = documentProfile{
			note:       note,
			queryTerms: queryTerms(tokens),
			tags:       tags,
		}
	}

	bm25 := newBM25(paths, tokenLists)
	mutual := mutualWikilinkSet(paths, graph)
	tagPaths := sharedTagPathIndex(paths, docMap)
	for _, relPath := range paths {
		profile, ok := docMap[relPath]
		if !ok {
			continue
		}
		profile.candidates = buildCandidatePaths(profile, bm25, tagPaths, mutual)
		docMap[relPath] = profile
	}

	return &Recommender{
		idx:    idx,
		graph:  graph,
		bm25:   bm25,
		docMap: docMap,
		paths:  paths,
		mutual: mutual,
	}, nil
}

// Related ranks other public notes for a given source note.
func (r *Recommender) Related(note *model.Note, limit int) []RankedNote {
	if r == nil || note == nil || limit <= 0 {
		return nil
	}

	queryProfile, ok := r.docMap[note.RelPath]
	if !ok {
		return nil
	}

	capacity := len(r.paths) - 1
	if capacity > limit {
		capacity = limit
	}
	if capacity < 0 {
		capacity = 0
	}
	ranked := make([]RankedNote, 0, capacity)
	for _, candidatePath := range r.relatedCandidatePaths(queryProfile) {
		if candidatePath == note.RelPath {
			continue
		}

		candidateProfile, ok := r.docMap[candidatePath]
		if !ok || candidateProfile.note == nil {
			continue
		}

		score := r.scoreProfiles(queryProfile, candidateProfile)
		if score <= 0 {
			continue
		}

		ranked = insertRankedNote(ranked, RankedNote{Note: candidateProfile.note, Score: score}, limit)
	}

	return ranked
}

func (r *Recommender) scoreProfiles(queryProfile documentProfile, candidateProfile documentProfile) float64 {
	if r == nil || queryProfile.note == nil || candidateProfile.note == nil {
		return 0
	}

	score := r.bm25.scoreQueryTerms(queryProfile.queryTerms, candidateProfile.note.RelPath)
	if score <= 0 {
		return 0
	}

	score += sharedTagBoost(queryProfile.tags, candidateProfile.tags)
	if hasMutualWikilink(r.mutual, queryProfile.note.RelPath, candidateProfile.note.RelPath) {
		score += mutualWikilinkBoost
	}

	return score
}

func (r *Recommender) relatedCandidatePaths(queryProfile documentProfile) []string {
	if r == nil || queryProfile.note == nil || len(queryProfile.candidates) == 0 {
		return nil
	}

	return queryProfile.candidates
}

func insertRankedNote(ranked []RankedNote, candidate RankedNote, limit int) []RankedNote {
	if limit <= 0 {
		return ranked
	}

	insertAt := len(ranked)
	for index, existing := range ranked {
		if compareRankedNotes(candidate, existing) > 0 {
			insertAt = index
			break
		}
	}
	if len(ranked) == limit && insertAt == len(ranked) {
		return ranked
	}

	ranked = append(ranked, RankedNote{})
	copy(ranked[insertAt+1:], ranked[insertAt:])
	ranked[insertAt] = candidate
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}

	return ranked
}

func compareRankedNotes(left RankedNote, right RankedNote) int {
	if left.Score != right.Score {
		if left.Score > right.Score {
			return 1
		}
		return -1
	}

	leftKey := rankedNoteSortKey(left.Note)
	rightKey := rankedNoteSortKey(right.Note)
	if leftKey != rightKey {
		if leftKey < rightKey {
			return 1
		}
		return -1
	}

	leftTitle := rankedNoteDisplayTitle(left.Note)
	rightTitle := rankedNoteDisplayTitle(right.Note)
	if leftTitle < rightTitle {
		return 1
	}
	if leftTitle > rightTitle {
		return -1
	}

	return 0
}

func sortedPublicNotePaths(idx *model.VaultIndex) []string {
	if idx == nil {
		return nil
	}

	paths := make([]string, 0, len(idx.Notes))
	for relPath := range idx.Notes {
		paths = append(paths, relPath)
	}
	sort.Strings(paths)
	return paths
}

func buildWorkerCount(pathCount int) int {
	if pathCount <= 1 {
		return 1
	}

	workerCount := runtime.GOMAXPROCS(0)
	if workerCount < 1 {
		return 1
	}
	if workerCount > pathCount {
		return pathCount
	}

	return workerCount
}

func sharedTagPathIndex(paths []string, docMap map[string]documentProfile) map[string][]string {
	if len(paths) == 0 {
		return nil
	}

	tagPaths := make(map[string][]string)
	for _, relPath := range paths {
		profile, ok := docMap[relPath]
		if !ok {
			continue
		}
		for tag := range profile.tags {
			tagPaths[tag] = append(tagPaths[tag], relPath)
		}
	}
	if len(tagPaths) == 0 {
		return nil
	}

	return tagPaths
}

func buildCandidatePaths(profile documentProfile, bm25 *BM25, tagPaths map[string][]string, mutual map[string]map[string]struct{}) []string {
	if profile.note == nil {
		return nil
	}

	estimate := 0
	if bm25 != nil {
		estimate += bm25.candidateEstimate(profile.queryTerms)
	}
	if estimate == 0 {
		return nil
	}

	candidates := make(map[string]struct{}, estimate)
	if bm25 != nil {
		bm25.collectCandidateIDs(profile.queryTerms, candidates)
	}
	delete(candidates, profile.note.RelPath)
	if len(candidates) == 0 {
		return nil
	}

	paths := make([]string, 0, len(candidates))
	for candidatePath := range candidates {
		paths = append(paths, candidatePath)
	}
	sort.Strings(paths)

	return paths
}

func tagSet(tags []string) map[string]struct{} {
	if len(tags) == 0 {
		return nil
	}

	set := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		trimmed := strings.TrimSpace(tag)
		if trimmed == "" {
			continue
		}
		set[trimmed] = struct{}{}
	}
	return set
}

func sharedTagBoost(left map[string]struct{}, right map[string]struct{}) float64 {
	if len(left) == 0 || len(right) == 0 {
		return 0
	}

	intersection := 0
	union := len(left)
	for tag := range right {
		if _, ok := left[tag]; ok {
			intersection++
			continue
		}
		union++
	}
	if intersection == 0 || union == 0 {
		return 0
	}

	return (float64(intersection) / float64(union)) * sharedTagBoostWeight
}

func hasMutualWikilink(mutual map[string]map[string]struct{}, source string, candidate string) bool {
	if len(mutual) == 0 {
		return false
	}

	candidates := mutual[source]
	if len(candidates) == 0 {
		return false
	}

	_, ok := candidates[candidate]
	return ok
}

func mutualWikilinkSet(paths []string, graph *model.LinkGraph) map[string]map[string]struct{} {
	if len(paths) == 0 || graph == nil || len(graph.Forward) == 0 {
		return nil
	}

	publicPaths := make(map[string]struct{}, len(paths))
	for _, relPath := range paths {
		publicPaths[relPath] = struct{}{}
	}

	forwardSets := make(map[string]map[string]struct{}, len(paths))
	for _, source := range paths {
		targets := graph.Forward[source]
		if len(targets) == 0 {
			continue
		}

		set := make(map[string]struct{}, len(targets))
		for _, target := range targets {
			if target == source {
				continue
			}
			if _, ok := publicPaths[target]; !ok {
				continue
			}
			set[target] = struct{}{}
		}
		if len(set) == 0 {
			continue
		}
		forwardSets[source] = set
	}

	mutual := make(map[string]map[string]struct{})
	for source, targets := range forwardSets {
		for target := range targets {
			reciprocal := forwardSets[target]
			if len(reciprocal) == 0 {
				continue
			}
			if _, ok := reciprocal[source]; !ok {
				continue
			}
			if mutual[source] == nil {
				mutual[source] = make(map[string]struct{})
			}
			mutual[source][target] = struct{}{}
		}
	}
	if len(mutual) == 0 {
		return nil
	}

	return mutual
}

func rankedNoteSortKey(note *model.Note) string {
	if note == nil {
		return ""
	}

	if trimmed := strings.TrimSpace(note.RelPath); trimmed != "" {
		return trimmed
	}

	return strings.TrimSpace(note.Slug)
}

func rankedNoteDisplayTitle(note *model.Note) string {
	if note == nil {
		return ""
	}
	if trimmed := strings.TrimSpace(note.Frontmatter.Title); trimmed != "" {
		return trimmed
	}
	if trimmed := strings.TrimSpace(note.Slug); trimmed != "" {
		return trimmed
	}
	return strings.TrimSpace(note.RelPath)
}
