package build

import (
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
	var semantics []model.RelatedSemanticDocument
	if semanticOwner != nil {
		semantics = *semanticOwner
		*semanticOwner = nil
	}
	return recommend.BuildEngine(semantics, idx, sourceGraph, parameters)
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

	tags := buildTagLinks(currentRelPath, idx, candidate.Tags)
	if len(tags) > maxRelatedArticleTags {
		tags = tags[:maxRelatedArticleTags]
	}
	return model.RelatedArticle{
		Title:   noteDisplayTitle(candidate),
		URL:     relativePageURL(currentRelPath, candidate.Slug, true),
		Summary: summary,
		Score:   score,
		Tags:    tags,
	}
}
