package build

import (
	"strings"

	"github.com/simp-lee/obsite/internal/model"
	"github.com/simp-lee/obsite/internal/recommend"
)

const maxRelatedArticleTags = 8

// preparePreRenderRelatedRanking consumes pass-1 source semantics and the
// source-only graph, returning only compact document IDs/scores.
func preparePreRenderRelatedRanking(
	semanticOwner *[]model.RelatedSemanticDocument,
	idx *model.VaultIndex,
	sourceGraph *model.LinkGraph,
	parameters recommend.EngineParameters,
) (*recommend.EngineResult, error) {
	return recommend.BuildEngineFromSemanticOwner(semanticOwner, idx, sourceGraph, parameters)
}

func materializeRelatedArticlesByPath(cfg model.SiteConfig, idx *model.VaultIndex, ranking *recommend.EngineResult, summaryByPath map[string]string) (map[string][]model.RelatedArticle, map[string]string) {
	if !cfg.Related.Enabled || idx == nil {
		return map[string][]model.RelatedArticle{}, map[string]string{}
	}

	rankedByPath := make(map[string][]recommend.RankedDocument)
	if ranking != nil {
		for _, document := range ranking.Documents {
			rankedByPath[document.RelPath] = document.Related
		}
	}
	articlesByPath := make(map[string][]model.RelatedArticle, len(idx.Notes))
	signatures := make(map[string]string, len(idx.Notes))
	for _, note := range allPublicNotes(idx) {
		if note == nil || strings.TrimSpace(note.RelPath) == "" {
			continue
		}
		currentRelPath := notePageRelPath(note)
		articles := make([]model.RelatedArticle, 0, len(rankedByPath[note.RelPath]))
		for _, ranked := range rankedByPath[note.RelPath] {
			if ranking == nil || ranked.DocID < 0 || ranked.DocID >= len(ranking.Documents) {
				continue
			}
			candidate := idx.Notes[ranking.Documents[ranked.DocID].RelPath]
			if candidate == nil || candidate.RelPath == note.RelPath || strings.TrimSpace(candidate.Slug) == "" {
				continue
			}
			articles = append(articles, materializeRelatedArticle(currentRelPath, idx, candidate, noteSummary(candidate, summaryByPath), ranked.Score))
		}
		articlesByPath[note.RelPath] = articles
		signatures[note.RelPath] = buildRelatedDerivedSignature(articles)
	}
	return articlesByPath, signatures
}

func materializeRelatedArticle(
	currentRelPath string,
	idx *model.VaultIndex,
	candidate *model.Note,
	summary string,
	score float64,
) model.RelatedArticle {
	if candidate == nil {
		return model.RelatedArticle{}
	}

	tags := buildTagLinksLimited(currentRelPath, idx, candidate.Tags, maxRelatedArticleTags)
	return model.RelatedArticle{
		Title:   noteDisplayTitle(candidate),
		URL:     relativePageURL(currentRelPath, candidate.Slug, true),
		Summary: summary,
		Score:   score,
		Tags:    tags,
	}
}
