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

type BM25 struct {
	documents map[string]bm25Document
	documentN float64
	avgDocLen float64
	docFreq   map[string]int
	k1        float64
	b         float64
}

func newBM25(tokenLists map[string][]string) *BM25 {
	index := &BM25{
		documents: make(map[string]bm25Document, len(tokenLists)),
		docFreq:   make(map[string]int),
		documentN: float64(len(tokenLists)),
		k1:        defaultBM25K1,
		b:         defaultBM25B,
	}
	if len(tokenLists) == 0 {
		return index
	}

	totalLength := 0
	for docID, tokens := range tokenLists {
		termFreq := make(map[string]int, len(tokens))
		seen := make(map[string]struct{}, len(tokens))
		for _, token := range tokens {
			termFreq[token]++
			if _, ok := seen[token]; ok {
				continue
			}
			seen[token] = struct{}{}
			index.docFreq[token]++
		}

		length := len(tokens)
		index.documents[docID] = bm25Document{termFreq: termFreq, length: length}
		totalLength += length
	}

	index.avgDocLen = float64(totalLength) / float64(len(tokenLists))
	return index
}

func (b *BM25) Score(queryTerms map[string]int, candidateID string) float64 {
	if b == nil || len(queryTerms) == 0 || b.avgDocLen == 0 {
		return 0
	}

	doc, ok := b.documents[candidateID]
	if !ok || doc.length == 0 {
		return 0
	}

	norm := b.k1 * (1 - b.b + b.b*(float64(doc.length)/b.avgDocLen))
	score := 0.0
	for _, term := range sortedQueryTerms(queryTerms) {
		queryFreq := queryTerms[term]
		termFreq := doc.termFreq[term]
		if termFreq == 0 {
			continue
		}

		idf := b.inverseDocumentFrequency(term)
		numerator := float64(termFreq) * (b.k1 + 1)
		denominator := float64(termFreq) + norm
		score += idf * (numerator / denominator) * float64(queryFreq)
	}

	return score
}

func sortedQueryTerms(queryTerms map[string]int) []string {
	if len(queryTerms) == 0 {
		return nil
	}

	terms := make([]string, 0, len(queryTerms))
	for term := range queryTerms {
		terms = append(terms, term)
	}
	sort.Strings(terms)
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

func termFrequency(tokens []string) map[string]int {
	if len(tokens) == 0 {
		return nil
	}

	freq := make(map[string]int, len(tokens))
	for _, token := range tokens {
		freq[token]++
	}

	return freq
}
