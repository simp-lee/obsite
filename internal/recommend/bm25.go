package recommend

import (
	"math"
	"sort"
	"strings"
)

const (
	defaultBM25K1 = 1.2
	defaultBM25B  = 0.75
)

type bm25Document struct {
	termFreq map[string]int
	length   int
}

type bm25QueryTerm struct {
	term string
	freq int
}

type BM25 struct {
	documents map[string]bm25Document
	postings  map[string][]string
	documentN float64
	avgDocLen float64
	docFreq   map[string]int
	k1        float64
	b         float64
}

func newBM25(paths []string, tokenLists map[string][]string) *BM25 {
	index := &BM25{
		documents: make(map[string]bm25Document, len(tokenLists)),
		postings:  make(map[string][]string),
		docFreq:   make(map[string]int),
		documentN: float64(len(tokenLists)),
		k1:        defaultBM25K1,
		b:         defaultBM25B,
	}
	if len(paths) == 0 || len(tokenLists) == 0 {
		return index
	}

	totalLength := 0
	for _, docID := range paths {
		tokens, ok := tokenLists[docID]
		if !ok {
			continue
		}

		termFreq := make(map[string]int, len(tokens))
		seen := make(map[string]struct{}, len(tokens))
		for _, token := range tokens {
			termFreq[token]++
			if _, ok := seen[token]; ok {
				continue
			}
			seen[token] = struct{}{}
			index.docFreq[token]++
			index.postings[token] = append(index.postings[token], docID)
		}

		length := len(tokens)
		index.documents[docID] = bm25Document{termFreq: termFreq, length: length}
		totalLength += length
	}

	index.avgDocLen = float64(totalLength) / float64(len(tokenLists))
	return index
}

func (b *BM25) scoreQueryTerms(queryTerms []bm25QueryTerm, candidateID string) float64 {
	if b == nil || len(queryTerms) == 0 || b.avgDocLen == 0 {
		return 0
	}

	doc, ok := b.documents[candidateID]
	if !ok || doc.length == 0 {
		return 0
	}

	norm := b.k1 * (1 - b.b + b.b*(float64(doc.length)/b.avgDocLen))
	score := 0.0
	for _, queryTerm := range queryTerms {
		termFreq := doc.termFreq[queryTerm.term]
		if termFreq == 0 {
			continue
		}

		idf := b.inverseDocumentFrequency(queryTerm.term)
		numerator := float64(termFreq) * (b.k1 + 1)
		denominator := float64(termFreq) + norm
		score += idf * (numerator / denominator) * float64(queryTerm.freq)
	}

	return score
}

func (b *BM25) collectCandidateIDs(queryTerms []bm25QueryTerm, dst map[string]struct{}) {
	if b == nil || len(queryTerms) == 0 || dst == nil {
		return
	}

	for _, queryTerm := range queryTerms {
		for _, candidateID := range b.postings[queryTerm.term] {
			dst[candidateID] = struct{}{}
		}
	}
}

func (b *BM25) candidateEstimate(queryTerms []bm25QueryTerm) int {
	if b == nil || len(queryTerms) == 0 {
		return 0
	}

	estimate := 0
	for _, queryTerm := range queryTerms {
		estimate += len(b.postings[queryTerm.term])
	}
	return estimate
}

func sortedQueryTerms(queryTerms map[string]int) []bm25QueryTerm {
	if len(queryTerms) == 0 {
		return nil
	}

	terms := make([]bm25QueryTerm, 0, len(queryTerms))
	for term, freq := range queryTerms {
		terms = append(terms, bm25QueryTerm{term: term, freq: freq})
	}
	sort.Slice(terms, func(i int, j int) bool {
		return terms[i].term < terms[j].term
	})
	return terms
}

func (b *BM25) inverseDocumentFrequency(term string) float64 {
	if b == nil || strings.TrimSpace(term) == "" || b.documentN == 0 {
		return 0
	}

	docFreq := float64(b.docFreq[term])
	if docFreq == 0 {
		return 0
	}

	return math.Log(1 + ((b.documentN - docFreq + 0.5) / (docFreq + 0.5)))
}

func queryTerms(tokens []string) []bm25QueryTerm {
	if len(tokens) == 0 {
		return nil
	}

	freq := make(map[string]int, len(tokens))
	for _, token := range tokens {
		freq[token]++
	}

	return sortedQueryTerms(freq)
}
