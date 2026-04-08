// Package recommend computes static related-article rankings for public notes.
package recommend

import (
	"fmt"
	"sort"
	"strings"

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
	queryTerms map[string]int
	tags       map[string]struct{}
}

// Recommender holds the prebuilt corpus and note metadata used for ranking.
type Recommender struct {
	idx    *model.VaultIndex
	graph  *model.LinkGraph
	bm25   *BM25
	docMap map[string]documentProfile
	paths  []string
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

	relatedByPath := make(map[string][]RankedNote, len(recommender.paths))
	for _, relPath := range recommender.paths {
		note := idx.Notes[relPath]
		if note == nil {
			continue
		}
		relatedByPath[relPath] = recommender.Related(note, limit)
	}

	return relatedByPath, nil
}

// New constructs a reusable recommender over the current public-note corpus.
func New(idx *model.VaultIndex, graph *model.LinkGraph) (*Recommender, error) {
	if idx == nil || len(idx.Notes) == 0 {
		return &Recommender{
			idx:    idx,
			graph:  graph,
			bm25:   newBM25(nil),
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
		docMap[relPath] = documentProfile{
			note:       note,
			queryTerms: termFrequency(tokens),
			tags:       tagSet(note.Tags),
		}
	}

	return &Recommender{
		idx:    idx,
		graph:  graph,
		bm25:   newBM25(tokenLists),
		docMap: docMap,
		paths:  paths,
	}, nil
}

// Score returns the unified relatedness score for one query note and candidate note.
func (r *Recommender) Score(queryNote *model.Note, candidateNote *model.Note) float64 {
	if r == nil || queryNote == nil || candidateNote == nil {
		return 0
	}
	if strings.TrimSpace(queryNote.RelPath) == "" || strings.TrimSpace(candidateNote.RelPath) == "" {
		return 0
	}
	if queryNote.RelPath == candidateNote.RelPath {
		return 0
	}

	queryProfile, ok := r.docMap[queryNote.RelPath]
	if !ok {
		return 0
	}

	candidateProfile, ok := r.docMap[candidateNote.RelPath]
	if !ok {
		return 0
	}

	return r.scoreProfiles(queryProfile, candidateProfile)
}

// Related ranks other public notes for a given source note.
func (r *Recommender) Related(note *model.Note, limit int) []RankedNote {
	if r == nil || note == nil || limit <= 0 {
		return nil
	}

	if _, ok := r.docMap[note.RelPath]; !ok {
		return nil
	}

	ranked := make([]RankedNote, 0, len(r.paths))
	for _, candidatePath := range r.paths {
		if candidatePath == note.RelPath {
			continue
		}

		candidateProfile, ok := r.docMap[candidatePath]
		if !ok || candidateProfile.note == nil {
			continue
		}

		score := r.Score(note, candidateProfile.note)
		if score <= 0 {
			continue
		}

		ranked = append(ranked, RankedNote{Note: candidateProfile.note, Score: score})
	}

	sort.SliceStable(ranked, func(i int, j int) bool {
		if ranked[i].Score != ranked[j].Score {
			return ranked[i].Score > ranked[j].Score
		}

		leftKey := rankedNoteSortKey(ranked[i].Note)
		rightKey := rankedNoteSortKey(ranked[j].Note)
		if leftKey != rightKey {
			return leftKey < rightKey
		}

		return rankedNoteDisplayTitle(ranked[i].Note) < rankedNoteDisplayTitle(ranked[j].Note)
	})

	if len(ranked) > limit {
		ranked = ranked[:limit]
	}

	return ranked
}

func (r *Recommender) scoreProfiles(queryProfile documentProfile, candidateProfile documentProfile) float64 {
	if r == nil || queryProfile.note == nil || candidateProfile.note == nil {
		return 0
	}

	score := r.bm25.Score(queryProfile.queryTerms, candidateProfile.note.RelPath)
	score += sharedTagBoost(queryProfile.tags, candidateProfile.tags)
	if hasMutualWikilink(r.graph, queryProfile.note.RelPath, candidateProfile.note.RelPath) {
		score += mutualWikilinkBoost
	}
	if score <= 0 {
		return 0
	}

	return score
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

func hasMutualWikilink(graph *model.LinkGraph, source string, candidate string) bool {
	if graph == nil {
		return false
	}

	return containsString(graph.Forward[source], candidate) && containsString(graph.Forward[candidate], source)
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
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
