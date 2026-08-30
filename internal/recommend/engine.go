package recommend

import (
	"fmt"
	"path"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/simp-lee/obsite/internal/model"
)

// EngineParameters contains internal ranking limits and testable worker control.
type EngineParameters struct {
	Features    FeatureParameters
	Content     ContentParameters
	Count       int
	WorkerCount int
}

// EngineDocument is the compact post-ranking state for one public document.
type EngineDocument struct {
	RelPath           string
	NormalizedRelPath string
	DisplayTitle      string
	Related           []RankedDocument
}

// EngineResult contains only compact documents and rankings; token/DF owners
// are not retained after BuildEngine returns.
type EngineResult struct {
	Documents []EngineDocument
}

type candidateScratch struct {
	seen    []bool
	touched []int
}

type pairScorer func(*FeatureIndex, *TagSignalIndex, *model.LinkGraph, int, int, ContentParameters) (PairScore, error)

// BuildEngine builds and scores the deterministic bounded recommendation index.
func BuildEngine(semantics []model.RelatedSemanticDocument, idx *model.VaultIndex, graph *model.LinkGraph, parameters EngineParameters) (*EngineResult, error) {
	return buildEngine(semantics, idx, graph, parameters, ScorePair)
}

func buildEngine(semantics []model.RelatedSemanticDocument, idx *model.VaultIndex, graph *model.LinkGraph, parameters EngineParameters, scorer pairScorer) (*EngineResult, error) {
	if len(semantics) < 2 {
		return &EngineResult{}, nil
	}
	if err := validateEngineParameters(parameters); err != nil {
		return nil, err
	}

	features, err := BuildFeatureIndex(semantics, parameters.Features)
	if err != nil {
		return nil, err
	}
	semantics = nil
	tags := BuildTagSignalIndex(features.Documents, idx)
	return rankFeatureIndex(features, tags, idx, graph, parameters, scorer)
}

func validateEngineParameters(parameters EngineParameters) error {
	if parameters.Count < 1 || parameters.Count > 20 {
		return fmt.Errorf("related count must be between 1 and 20")
	}
	if err := validateFeatureParameters(parameters.Features); err != nil {
		return err
	}
	return validateContentParameters(parameters.Content)
}

func rankFeatureIndex(features *FeatureIndex, tags *TagSignalIndex, idx *model.VaultIndex, graph *model.LinkGraph, parameters EngineParameters, scorer pairScorer) (*EngineResult, error) {
	if features == nil || len(features.Documents) < 2 {
		return &EngineResult{}, nil
	}

	documentCount := len(features.Documents)
	limit := parameters.Count
	if limit > documentCount-1 {
		limit = documentCount - 1
	}
	rankingDocuments := make([]rankingDocument, documentCount)
	docIDByPath := make(map[string]int, documentCount)
	for docID, document := range features.Documents {
		title := engineDisplayTitle(idx, document.RelPath)
		rankingDocuments[docID] = rankingDocument{
			normalizedPath: document.NormalizedRelPath,
			displayTitle:   title,
		}
		docIDByPath[document.RelPath] = docID
	}

	ranked := make([][]RankedDocument, documentCount)
	errorsByDoc := make([]error, documentCount)
	workerCount := normalizeEngineWorkerCount(parameters.WorkerCount, documentCount)
	runWorker := func(jobs <-chan int) {
		scratch := candidateScratch{
			seen:    make([]bool, documentCount),
			touched: make([]int, 0, documentCount),
		}
		for sourceDocID := range jobs {
			ranked[sourceDocID], errorsByDoc[sourceDocID] = rankSourceDocument(
				features,
				tags,
				graph,
				docIDByPath,
				rankingDocuments,
				sourceDocID,
				limit,
				parameters.Content,
				scorer,
				&scratch,
			)
		}
	}

	if workerCount == 1 {
		jobs := make(chan int, documentCount)
		for docID := 0; docID < documentCount; docID++ {
			jobs <- docID
		}
		close(jobs)
		runWorker(jobs)
	} else {
		jobs := make(chan int)
		var workers sync.WaitGroup
		for worker := 0; worker < workerCount; worker++ {
			workers.Add(1)
			go func() {
				defer workers.Done()
				runWorker(jobs)
			}()
		}
		for docID := 0; docID < documentCount; docID++ {
			jobs <- docID
		}
		close(jobs)
		workers.Wait()
	}

	for docID, err := range errorsByDoc {
		if err != nil {
			return nil, fmt.Errorf("rank related candidates for %q: %w", features.Documents[docID].RelPath, err)
		}
	}

	result := &EngineResult{Documents: make([]EngineDocument, documentCount)}
	for docID, document := range features.Documents {
		result.Documents[docID] = EngineDocument{
			RelPath:           document.RelPath,
			NormalizedRelPath: document.NormalizedRelPath,
			DisplayTitle:      rankingDocuments[docID].displayTitle,
			Related:           ranked[docID],
		}
	}
	return result, nil
}

func rankSourceDocument(
	features *FeatureIndex,
	tags *TagSignalIndex,
	graph *model.LinkGraph,
	docIDByPath map[string]int,
	documents []rankingDocument,
	sourceDocID int,
	limit int,
	parameters ContentParameters,
	scorer pairScorer,
	scratch *candidateScratch,
) ([]RankedDocument, error) {
	scratch.reset()
	source := features.Documents[sourceDocID]
	for _, feature := range source.Features {
		if feature.TermID < 0 || feature.TermID >= len(features.Postings) {
			continue
		}
		for _, posting := range features.Postings[feature.TermID] {
			scratch.touch(posting.DocID)
		}
	}
	if graph != nil {
		for _, candidatePath := range graph.Forward[source.RelPath] {
			if candidateDocID, ok := docIDByPath[candidatePath]; ok {
				scratch.touch(candidateDocID)
			}
		}
		for _, candidatePath := range graph.Backward[source.RelPath] {
			if candidateDocID, ok := docIDByPath[candidatePath]; ok {
				scratch.touch(candidateDocID)
			}
		}
	}
	if tags != nil && sourceDocID < len(tags.DocumentTags) {
		for _, tagID := range tags.DocumentTags[sourceDocID] {
			if tagID < 0 || tagID >= len(tags.Postings) {
				continue
			}
			for _, candidateDocID := range tags.Postings[tagID] {
				scratch.touch(candidateDocID)
			}
		}
	}

	sort.Ints(scratch.touched)
	topK := newTopKHeap(limit, documents)
	for _, candidateDocID := range scratch.touched {
		if candidateDocID == sourceDocID {
			continue
		}
		score, err := scorer(features, tags, graph, sourceDocID, candidateDocID, parameters)
		if err != nil {
			return nil, fmt.Errorf("score %q: %w", features.Documents[candidateDocID].RelPath, err)
		}
		if score.Final <= 0 {
			continue
		}
		topK.add(RankedDocument{DocID: candidateDocID, Score: score.Final}, limit)
	}

	sort.Slice(topK.items, func(left int, right int) bool {
		return rankedDocumentIsBetter(topK.items[left], topK.items[right], documents)
	})
	return append([]RankedDocument(nil), topK.items...), nil
}

func (s *candidateScratch) reset() {
	for _, docID := range s.touched {
		s.seen[docID] = false
	}
	s.touched = s.touched[:0]
}

func (s *candidateScratch) touch(docID int) {
	if s == nil || docID < 0 || docID >= len(s.seen) || s.seen[docID] {
		return
	}
	s.seen[docID] = true
	s.touched = append(s.touched, docID)
}

func normalizeEngineWorkerCount(requested int, documentCount int) int {
	if documentCount <= 0 {
		return 0
	}
	maxWorkers := runtime.GOMAXPROCS(0)
	if maxWorkers < 1 {
		maxWorkers = 1
	}
	if maxWorkers > 8 {
		maxWorkers = 8
	}
	if requested <= 0 || requested > maxWorkers {
		requested = maxWorkers
	}
	if requested < 1 {
		requested = 1
	}
	if requested > documentCount {
		requested = documentCount
	}
	return requested
}

func engineDisplayTitle(idx *model.VaultIndex, relPath string) string {
	if idx != nil {
		if note := idx.Notes[relPath]; note != nil {
			if title := strings.TrimSpace(note.Frontmatter.Title); title != "" {
				return title
			}
		}
	}
	base := path.Base(strings.ReplaceAll(relPath, `\`, "/"))
	if base == "." || base == "" || base == "/" {
		return ""
	}
	return strings.TrimSuffix(base, path.Ext(base))
}
