package recommend

import (
	"fmt"
	"reflect"
	"testing"
)

func TestHoldoutJudgments(t *testing.T) {
	manifest, err := loadQualityManifest(qualityAssetRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateQualityManifest(qualityAssetRoot, manifest); err != nil {
		t.Fatal(err)
	}
	corpus, err := loadQualityRankingCorpus(manifest, "holdout")
	if err != nil {
		t.Fatal(err)
	}
	result, err := BuildEngine(corpus.Semantics, corpus.Index, corpus.Graph, ProductionEngineParameters(10, 1))
	if err != nil {
		t.Fatal(err)
	}
	production := make(map[string][]string, len(result.Documents))
	for _, document := range result.Documents {
		sourceID := corpus.IDByPath[document.RelPath]
		for _, ranked := range document.Related {
			production[sourceID] = append(production[sourceID], corpus.IDByPath[result.Documents[ranked.DocID].RelPath])
		}
	}

	holdoutSets := 0
	totalRelevantSources := 0
	for _, language := range []string{"zh-hans", "zh-hant", "en", "mixed"} {
		reference, ok := qualityLabelReference(manifest, "holdout", language)
		if !ok {
			continue
		}
		holdoutSets++
		language := language
		t.Run(language, func(t *testing.T) {
			set, err := loadQualityJudgmentSet(reference)
			if err != nil {
				t.Fatal(err)
			}
			relevant, err := validateHoldoutJudgmentCoverage(set, corpus, production, language)
			if err != nil {
				t.Fatal(err)
			}
			totalRelevantSources += relevant
		})
	}
	if holdoutSets == 4 {
		t.Run("complete", func(t *testing.T) {
			if totalRelevantSources < 30 {
				t.Fatalf("holdout relevant source count = %d, want at least 30", totalRelevantSources)
			}
		})
	}
}

func validateHoldoutJudgmentCoverage(set qualityJudgmentSet, corpus qualityRankingCorpus, production map[string][]string, language string) (int, error) {
	if set.PoolKind != "holdout-active" || set.Split != "holdout" || set.Language != language {
		return 0, fmt.Errorf("invalid holdout judgment header for %s", language)
	}
	judgments := make(map[string]qualitySourceJudgment, len(set.Sources))
	for _, judgment := range set.Sources {
		judgments[judgment.SourceID] = judgment
	}
	wantSources := 0
	relevantSources := 0
	for sourceID, source := range corpus.SourceByID {
		if source.Language != language {
			continue
		}
		wantSources++
		judgment, ok := judgments[sourceID]
		if !ok {
			return 0, fmt.Errorf("missing holdout judgment for %s", sourceID)
		}
		if !reflect.DeepEqual(judgment.ProductionTop10, production[sourceID]) {
			return 0, fmt.Errorf("holdout production pool mismatch for %s", sourceID)
		}
		candidates := make(map[string]qualityCandidateJudgment, len(judgment.Candidates))
		independent := false
		positive := false
		for _, candidate := range judgment.Candidates {
			candidates[candidate.CandidateID] = candidate
			if candidate.Grade >= 1 {
				positive = true
			}
			for _, discovery := range candidate.Discovery {
				if discovery != "production-top10" {
					independent = true
				}
			}
		}
		for _, candidateID := range production[sourceID] {
			candidate, ok := candidates[candidateID]
			if !ok || !qualityStringContains(candidate.Discovery, "production-top10") {
				return 0, fmt.Errorf("holdout pool %s omits production candidate %s", sourceID, candidateID)
			}
		}
		if !independent {
			return 0, fmt.Errorf("holdout source %s lacks active independent discovery", sourceID)
		}
		if positive {
			relevantSources++
		}
	}
	if len(set.Sources) != wantSources {
		return 0, fmt.Errorf("holdout %s judgment count = %d, want %d", language, len(set.Sources), wantSources)
	}
	if relevantSources < 7 {
		return 0, fmt.Errorf("holdout %s relevant source count = %d, want at least 7", language, relevantSources)
	}
	return relevantSources, nil
}
