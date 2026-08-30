package recommend

import (
	"container/heap"
	"fmt"
	"math"
	"path"
	"sort"
	"strings"

	"github.com/simp-lee/obsite/internal/model"
	"golang.org/x/text/unicode/norm"
)

const cosineRoundingTolerance = 1e-12

type fieldMask uint8

const (
	fieldTitle fieldMask = 1 << iota
	fieldAliases
	fieldHeadings
	fieldBody
)

// FeatureParameters contains the calibration-controlled feature limits.
type FeatureParameters struct {
	MaxFeatures int
	MaxDFRatio  float64
}

// Feature is one normalized sparse TF-IDF component.
type Feature struct {
	TermID int
	Weight float64
	Fields fieldMask
}

// Posting is one deterministic term-to-document sparse component.
type Posting struct {
	DocID  int
	Weight float64
}

// FeatureDocument contains the compact feature state retained after indexing.
type FeatureDocument struct {
	RelPath           string
	NormalizedRelPath string
	Features          []Feature
}

// FeatureIndex is the immutable deterministic TF-IDF index used for scoring.
type FeatureIndex struct {
	Documents []FeatureDocument
	Terms     []string
	CorpusDF  []int
	Postings  [][]Posting
}

type termFieldCounts struct {
	title    int
	aliases  int
	headings int
	body     int
}

type documentTermCounts struct {
	relPath           string
	normalizedRelPath string
	terms             map[string]termFieldCounts
}

type weightedTerm struct {
	termID int
	term   string
	weight float64
	fields fieldMask
}

type weightedTermHeap []weightedTerm

func (h weightedTermHeap) Len() int { return len(h) }

func (h weightedTermHeap) Less(left int, right int) bool {
	if h[left].weight != h[right].weight {
		return h[left].weight < h[right].weight
	}
	return h[left].term > h[right].term
}

func (h weightedTermHeap) Swap(left int, right int) {
	h[left], h[right] = h[right], h[left]
}

func (h *weightedTermHeap) Push(value any) {
	*h = append(*h, value.(weightedTerm))
}

func (h *weightedTermHeap) Pop() any {
	old := *h
	last := len(old) - 1
	value := old[last]
	*h = old[:last]
	return value
}

// BuildFeatureIndex tokenizes fields independently and builds deterministic
// dynamic TF-IDF features and postings.
func BuildFeatureIndex(documents []model.RelatedSemanticDocument, parameters FeatureParameters) (*FeatureIndex, error) {
	if err := validateFeatureParameters(parameters); err != nil {
		return nil, err
	}

	ordered := append([]model.RelatedSemanticDocument(nil), documents...)
	sort.Slice(ordered, func(left int, right int) bool {
		leftPath := normalizeFeatureRelPath(ordered[left].RelPath)
		rightPath := normalizeFeatureRelPath(ordered[right].RelPath)
		if leftPath != rightPath {
			return leftPath < rightPath
		}
		return ordered[left].RelPath < ordered[right].RelPath
	})

	counts := make([]documentTermCounts, len(ordered))
	documentFrequency := make(map[string]int)
	for docID, document := range ordered {
		normalizedPath := normalizeFeatureRelPath(document.RelPath)
		terms, err := tokenizeDocumentFields(document)
		if err != nil {
			return nil, fmt.Errorf("tokenize related semantic document %q: %w", document.RelPath, err)
		}
		counts[docID] = documentTermCounts{
			relPath:           document.RelPath,
			normalizedRelPath: normalizedPath,
			terms:             terms,
		}
		for term := range terms {
			documentFrequency[term]++
		}
	}

	terms := eligibleTerms(documentFrequency, len(ordered), parameters.MaxDFRatio)
	termIDs := make(map[string]int, len(terms))
	corpusDF := make([]int, len(terms))
	for termID, term := range terms {
		termIDs[term] = termID
		corpusDF[termID] = documentFrequency[term]
	}

	selected := make([][]weightedTerm, len(counts))
	selectedDF := make([]int, len(terms))
	for docID, document := range counts {
		winners := make(weightedTermHeap, 0, parameters.MaxFeatures)
		for term, fieldCounts := range document.terms {
			termID, ok := termIDs[term]
			if !ok {
				continue
			}
			weight := weightedTermFrequency(fieldCounts) * inverseDocumentFrequency(len(ordered), corpusDF[termID])
			if weight <= 0 {
				continue
			}
			candidate := weightedTerm{
				termID: termID,
				term:   term,
				weight: weight,
				fields: fieldPresence(fieldCounts),
			}
			if len(winners) < parameters.MaxFeatures {
				heap.Push(&winners, candidate)
				continue
			}
			if weightedTermIsBetter(candidate, winners[0]) {
				winners[0] = candidate
				heap.Fix(&winners, 0)
			}
		}

		candidates := append([]weightedTerm(nil), winners...)
		sort.Slice(candidates, func(left int, right int) bool {
			return weightedTermIsBetter(candidates[left], candidates[right])
		})
		selected[docID] = candidates
		for _, candidate := range candidates {
			selectedDF[candidate.termID]++
		}
	}

	index := &FeatureIndex{
		Documents: make([]FeatureDocument, len(counts)),
		Terms:     terms,
		CorpusDF:  corpusDF,
		Postings:  make([][]Posting, len(terms)),
	}
	for docID, document := range counts {
		features := make([]Feature, 0, len(selected[docID]))
		for _, candidate := range selected[docID] {
			if selectedDF[candidate.termID] <= 1 {
				continue
			}
			features = append(features, Feature{
				TermID: candidate.termID,
				Weight: candidate.weight,
				Fields: candidate.fields,
			})
		}
		sort.Slice(features, func(left int, right int) bool {
			return features[left].TermID < features[right].TermID
		})
		normalizeFeatures(features)

		index.Documents[docID] = FeatureDocument{
			RelPath:           document.relPath,
			NormalizedRelPath: document.normalizedRelPath,
			Features:          features,
		}
		for _, feature := range features {
			index.Postings[feature.TermID] = append(index.Postings[feature.TermID], Posting{
				DocID:  docID,
				Weight: feature.Weight,
			})
		}
	}

	return index, nil
}

func weightedTermIsBetter(left weightedTerm, right weightedTerm) bool {
	if left.weight != right.weight {
		return left.weight > right.weight
	}
	return left.term < right.term
}

func validateFeatureParameters(parameters FeatureParameters) error {
	switch parameters.MaxFeatures {
	case 32, 48, 64:
	default:
		return fmt.Errorf("max features must be one of 32, 48, or 64")
	}
	switch parameters.MaxDFRatio {
	case 0.40, 0.50, 0.60:
	default:
		return fmt.Errorf("max term DF ratio must be one of 0.40, 0.50, or 0.60")
	}
	return nil
}

func tokenizeDocumentFields(document model.RelatedSemanticDocument) (map[string]termFieldCounts, error) {
	counts := make(map[string]termFieldCounts)
	if err := addFieldTokens(counts, document.Title, fieldTitle); err != nil {
		return nil, err
	}
	for _, alias := range document.Aliases {
		if err := addFieldTokens(counts, alias, fieldAliases); err != nil {
			return nil, err
		}
	}
	for _, heading := range document.Headings {
		if err := addFieldTokens(counts, heading, fieldHeadings); err != nil {
			return nil, err
		}
	}
	if err := addFieldTokens(counts, document.Body, fieldBody); err != nil {
		return nil, err
	}
	return counts, nil
}

func addFieldTokens(counts map[string]termFieldCounts, text string, field fieldMask) error {
	tokens, err := Tokenize(text)
	if err != nil {
		return err
	}
	for _, term := range tokens {
		current := counts[term]
		switch field {
		case fieldTitle:
			current.title++
		case fieldAliases:
			current.aliases++
		case fieldHeadings:
			current.headings++
		case fieldBody:
			current.body++
		}
		counts[term] = current
	}
	return nil
}

func eligibleTerms(documentFrequency map[string]int, documentCount int, maxDFRatio float64) []string {
	terms := make([]string, 0, len(documentFrequency))
	for term, frequency := range documentFrequency {
		if frequency < 2 {
			continue
		}
		if documentCount >= 20 && float64(frequency)/float64(documentCount) > maxDFRatio {
			continue
		}
		terms = append(terms, term)
	}
	sort.Strings(terms)
	return terms
}

func weightedTermFrequency(counts termFieldCounts) float64 {
	return 3.0*saturatedCount(counts.title) +
		2.5*saturatedCount(counts.aliases) +
		2.0*saturatedCount(counts.headings) +
		1.0*saturatedCount(counts.body)
}

func saturatedCount(count int) float64 {
	if count <= 0 {
		return 0
	}
	return 1 + math.Log(float64(count))
}

func inverseDocumentFrequency(documentCount int, documentFrequency int) float64 {
	if documentCount <= 0 || documentFrequency <= 0 {
		return 0
	}
	return math.Log(1 + (float64(documentCount-documentFrequency)+0.5)/(float64(documentFrequency)+0.5))
}

func fieldPresence(counts termFieldCounts) fieldMask {
	var fields fieldMask
	if counts.title > 0 {
		fields |= fieldTitle
	}
	if counts.aliases > 0 {
		fields |= fieldAliases
	}
	if counts.headings > 0 {
		fields |= fieldHeadings
	}
	if counts.body > 0 {
		fields |= fieldBody
	}
	return fields
}

func normalizeFeatures(features []Feature) {
	squaredNorm := 0.0
	for _, feature := range features {
		squaredNorm += feature.Weight * feature.Weight
	}
	if squaredNorm == 0 {
		return
	}
	norm := math.Sqrt(squaredNorm)
	for index := range features {
		features[index].Weight /= norm
	}
}

// SparseCosine computes the deterministic dot product of termID-sorted vectors.
func SparseCosine(left []Feature, right []Feature) (float64, error) {
	leftIndex, rightIndex := 0, 0
	similarity := 0.0
	for leftIndex < len(left) && rightIndex < len(right) {
		switch {
		case left[leftIndex].TermID < right[rightIndex].TermID:
			leftIndex++
		case left[leftIndex].TermID > right[rightIndex].TermID:
			rightIndex++
		default:
			similarity += left[leftIndex].Weight * right[rightIndex].Weight
			leftIndex++
			rightIndex++
		}
	}

	if math.IsNaN(similarity) || math.IsInf(similarity, 0) {
		return 0, fmt.Errorf("cosine similarity is not finite: %v", similarity)
	}
	if similarity < 0 {
		if similarity >= -cosineRoundingTolerance {
			return 0, nil
		}
		return 0, fmt.Errorf("cosine similarity below zero tolerance: %.17g", similarity)
	}
	if similarity > 1 {
		if similarity <= 1+cosineRoundingTolerance {
			return 1, nil
		}
		return 0, fmt.Errorf("cosine similarity above one tolerance: %.17g", similarity)
	}
	return similarity, nil
}

func normalizeFeatureRelPath(relPath string) string {
	normalized := norm.NFKC.String(strings.ReplaceAll(strings.TrimSpace(relPath), `\`, "/"))
	normalized = strings.TrimPrefix(normalized, "./")
	cleaned := path.Clean(normalized)
	if cleaned == "." {
		return ""
	}
	return cleaned
}
