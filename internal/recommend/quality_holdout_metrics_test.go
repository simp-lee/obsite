package recommend

import "testing"

type qualityMetricAggregate struct {
	NDCG                 float64
	NormalizedPrecision  float64
	RelevantCoverage     float64
	KnownRelevantHitRate float64
	Count                int
}

func (a *qualityMetricAggregate) add(result qualityMetricResult) {
	a.NDCG += result.NDCG
	a.NormalizedPrecision += result.NormalizedPrecision
	if result.TopFiveRelevant {
		a.RelevantCoverage++
	}
	if result.KnownRelevantHit {
		a.KnownRelevantHitRate++
	}
	a.Count++
}

func (a qualityMetricAggregate) mean() qualityMetricAggregate {
	if a.Count == 0 {
		return a
	}
	count := float64(a.Count)
	a.NDCG /= count
	a.NormalizedPrecision /= count
	a.RelevantCoverage /= count
	a.KnownRelevantHitRate /= count
	return a
}

func TestRelatedQualityHoldout(t *testing.T) {
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
	rankings := make(map[string][]string, len(result.Documents))
	for _, document := range result.Documents {
		sourceID := corpus.IDByPath[document.RelPath]
		for _, ranked := range document.Related {
			rankings[sourceID] = append(rankings[sourceID], corpus.IDByPath[result.Documents[ranked.DocID].RelPath])
		}
	}

	judgments := make(map[string]qualitySourceJudgment)
	holdoutSets := 0
	for _, reference := range manifest.LabelSets {
		if reference.Split != "holdout" {
			continue
		}
		holdoutSets++
		set, err := loadQualityJudgmentSet(reference)
		if err != nil {
			t.Fatal(err)
		}
		for _, judgment := range set.Sources {
			judgments[judgment.SourceID] = judgment
		}
	}
	if holdoutSets != 4 || len(judgments) != 40 {
		t.Fatalf("holdout label sets/sources = %d/%d, want 4/40", holdoutSets, len(judgments))
	}

	overall := qualityMetricAggregate{}
	languages := make(map[string]*qualityMetricAggregate)
	buckets := make(map[string]*qualityMetricAggregate)
	for sourceID, source := range corpus.SourceByID {
		judgment, ok := judgments[sourceID]
		if !ok {
			t.Fatalf("missing holdout judgment for %s", sourceID)
		}
		if judgment.None {
			continue
		}
		measured, err := computeQualityMetrics(rankings[sourceID], judgment)
		if err != nil {
			t.Fatal(err)
		}
		overall.add(measured)
		if languages[source.Language] == nil {
			languages[source.Language] = &qualityMetricAggregate{}
		}
		languages[source.Language].add(measured)
		for _, bucket := range source.Buckets {
			if buckets[bucket] == nil {
				buckets[bucket] = &qualityMetricAggregate{}
			}
			buckets[bucket].add(measured)
		}
	}

	overallMean := overall.mean()
	if overallMean.Count < 30 {
		t.Fatalf("relevant holdout sources = %d, want at least 30", overallMean.Count)
	}
	assertQualityThresholds(t, "overall", overallMean, 0.55, 0.60)
	if overallMean.RelevantCoverage < 0.70 {
		t.Errorf("overall Top-5 relevant-query coverage = %.4f, want >= 0.70", overallMean.RelevantCoverage)
	}
	if overallMean.KnownRelevantHitRate < 0.60 {
		t.Errorf("overall known-relevant Top-5 hit rate = %.4f, want >= 0.60", overallMean.KnownRelevantHitRate)
	}

	for _, language := range []string{"zh-hans", "zh-hant", "en", "mixed"} {
		aggregate := languages[language]
		if aggregate == nil || aggregate.Count < 7 {
			t.Fatalf("%s relevant holdout sources = %d, want at least 7", language, aggregateCount(aggregate))
		}
		assertQualityThresholds(t, "language/"+language, aggregate.mean(), 0.45, 0)
	}
	for _, bucket := range []string{"long-form", "multi-topic"} {
		aggregate := buckets[bucket]
		if aggregate == nil || aggregate.Count < 10 {
			t.Logf("bucket/%s sample count = %d; threshold not applicable", bucket, aggregateCount(aggregate))
			continue
		}
		assertQualityThresholds(t, "bucket/"+bucket, aggregate.mean(), 0.45, 0)
	}
}

func assertQualityThresholds(t *testing.T, name string, metrics qualityMetricAggregate, minimumNDCG float64, minimumPrecision float64) {
	t.Helper()
	t.Logf("%s: nDCG@5=%.4f normalized-P@5=%.4f coverage=%.4f known-hit=%.4f n=%d", name, metrics.NDCG, metrics.NormalizedPrecision, metrics.RelevantCoverage, metrics.KnownRelevantHitRate, metrics.Count)
	if metrics.NDCG < minimumNDCG {
		t.Errorf("%s nDCG@5 = %.4f, want >= %.2f", name, metrics.NDCG, minimumNDCG)
	}
	if minimumPrecision > 0 && metrics.NormalizedPrecision < minimumPrecision {
		t.Errorf("%s normalized Precision@5 = %.4f, want >= %.2f", name, metrics.NormalizedPrecision, minimumPrecision)
	}
}

func aggregateCount(aggregate *qualityMetricAggregate) int {
	if aggregate == nil {
		return 0
	}
	return aggregate.Count
}
