package recommend

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"testing"
)

const calibrationSelectionRule = "maximize macro nDCG@5, normalized Precision@5, Top-5 relevant-query coverage, then known-relevant Top-5 hit rate; exact ties choose the lexicographically smallest tuple"

type calibrationParameterManifest struct {
	SchemaVersion           int                       `json:"schemaVersion"`
	SelectionRule           string                    `json:"selectionRule"`
	Winning                 calibrationParameterTuple `json:"winning"`
	Metrics                 calibrationMetrics        `json:"metrics"`
	CalibrationSourceSHA256 string                    `json:"calibrationSourceSha256"`
	CalibrationAssetSHA256  string                    `json:"calibrationAssetSha256"`
}

type calibrationParameterTuple struct {
	MaxFeatures        int     `json:"maxFeatures"`
	MaxTermDFRatio     float64 `json:"maxTermDFRatio"`
	MinContentCosine   float64 `json:"minContentCosine"`
	MaxSingleTermRatio float64 `json:"maxSingleTermDFRatio"`
}

type calibrationMetrics struct {
	NDCG                  float64 `json:"macroNDCGAt5"`
	NormalizedPrecision   float64 `json:"macroNormalizedPrecisionAt5"`
	RelevantQueryCoverage float64 `json:"top5RelevantQueryCoverage"`
	KnownRelevantHitRate  float64 `json:"knownRelevantTop5HitRate"`
	SourceCount           int     `json:"sourceCount"`
}

func TestCalibrationSelection(t *testing.T) {
	manifest, err := loadQualityManifest(qualityAssetRoot)
	if err != nil {
		t.Fatal(err)
	}
	got, err := selectCalibrationTuple(manifest)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("testdata/quality/parameters.json")
	if err != nil {
		t.Fatal(err)
	}
	var want calibrationParameterManifest
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	if want.SchemaVersion != 1 || want.SelectionRule != calibrationSelectionRule {
		t.Fatalf("parameter manifest schema/rule mismatch: %#v", want)
	}
	if got.Winning != want.Winning || got.CalibrationSourceSHA256 != want.CalibrationSourceSHA256 || got.CalibrationAssetSHA256 != want.CalibrationAssetSHA256 || !calibrationMetricsEqual(got.Metrics, want.Metrics) {
		t.Fatalf("calibration selection mismatch:\ngot  %#v\nwant %#v", got, want)
	}
}

func TestProductionParametersMatchManifest(t *testing.T) {
	data, err := os.ReadFile("testdata/quality/parameters.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest calibrationParameterManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	got := ProductionEngineParameters(5, 1)
	want := manifest.Winning
	if got.Features.MaxFeatures != want.MaxFeatures || got.Features.MaxDFRatio != want.MaxTermDFRatio || got.Content.MinCosine != want.MinContentCosine || got.Content.MaxSingleTermRatio != want.MaxSingleTermRatio {
		t.Fatalf("production tuple = %#v, want manifest %#v", got, want)
	}
}

func selectCalibrationTuple(manifest qualityManifest) (calibrationParameterManifest, error) {
	corpus, err := loadQualityRankingCorpus(manifest, "calibration")
	if err != nil {
		return calibrationParameterManifest{}, err
	}
	judgments := make(map[string]qualitySourceJudgment)
	for _, reference := range manifest.LabelSets {
		if reference.Split != "calibration" {
			continue
		}
		set, err := loadQualityJudgmentSet(reference)
		if err != nil {
			return calibrationParameterManifest{}, err
		}
		for _, judgment := range set.Sources {
			judgments[judgment.SourceID] = judgment
		}
	}
	if len(judgments) != len(corpus.Semantics) {
		return calibrationParameterManifest{}, fmt.Errorf("calibration judgments = %d, want %d sources", len(judgments), len(corpus.Semantics))
	}

	var best calibrationParameterManifest
	selected := false
	for _, parameters := range qualityParameterGrid() {
		result, err := BuildEngine(corpus.Semantics, corpus.Index, corpus.Graph, parameters)
		if err != nil {
			return calibrationParameterManifest{}, err
		}
		metrics := calibrationMetrics{SourceCount: len(result.Documents)}
		for _, document := range result.Documents {
			sourceID := corpus.IDByPath[document.RelPath]
			judgment, ok := judgments[sourceID]
			if !ok {
				return calibrationParameterManifest{}, fmt.Errorf("missing calibration judgment for %s", sourceID)
			}
			grades := make(map[string]struct{}, len(judgment.Candidates))
			for _, candidate := range judgment.Candidates {
				grades[candidate.CandidateID] = struct{}{}
			}
			ranking := make([]string, 0, len(document.Related))
			for _, ranked := range document.Related {
				candidateID := corpus.IDByPath[result.Documents[ranked.DocID].RelPath]
				if _, ok := grades[candidateID]; !ok {
					return calibrationParameterManifest{}, fmt.Errorf("tuple evaluates unjudged pair %s -> %s", sourceID, candidateID)
				}
				ranking = append(ranking, candidateID)
			}
			measured, err := computeQualityMetrics(ranking, judgment)
			if err != nil {
				return calibrationParameterManifest{}, err
			}
			metrics.NDCG += measured.NDCG
			metrics.NormalizedPrecision += measured.NormalizedPrecision
			if measured.TopFiveRelevant {
				metrics.RelevantQueryCoverage++
			}
			if measured.KnownRelevantHit {
				metrics.KnownRelevantHitRate++
			}
		}
		count := float64(metrics.SourceCount)
		metrics.NDCG /= count
		metrics.NormalizedPrecision /= count
		metrics.RelevantQueryCoverage /= count
		metrics.KnownRelevantHitRate /= count
		candidate := calibrationParameterManifest{
			SchemaVersion: 1,
			SelectionRule: calibrationSelectionRule,
			Winning: calibrationParameterTuple{
				MaxFeatures:        parameters.Features.MaxFeatures,
				MaxTermDFRatio:     parameters.Features.MaxDFRatio,
				MinContentCosine:   parameters.Content.MinCosine,
				MaxSingleTermRatio: parameters.Content.MaxSingleTermRatio,
			},
			Metrics:                 metrics,
			CalibrationSourceSHA256: calibrationSourceHash(manifest),
			CalibrationAssetSHA256:  calibrationAssetHash(manifest),
		}
		if !selected || calibrationMetricsBetter(candidate.Metrics, best.Metrics) {
			best = candidate
			selected = true
		}
	}
	if !selected {
		return calibrationParameterManifest{}, fmt.Errorf("calibration grid is empty")
	}
	return best, nil
}

func calibrationMetricsBetter(left calibrationMetrics, right calibrationMetrics) bool {
	if left.NDCG != right.NDCG {
		return left.NDCG > right.NDCG
	}
	if left.NormalizedPrecision != right.NormalizedPrecision {
		return left.NormalizedPrecision > right.NormalizedPrecision
	}
	if left.RelevantQueryCoverage != right.RelevantQueryCoverage {
		return left.RelevantQueryCoverage > right.RelevantQueryCoverage
	}
	return left.KnownRelevantHitRate > right.KnownRelevantHitRate
}

func calibrationMetricsEqual(left calibrationMetrics, right calibrationMetrics) bool {
	return left.SourceCount == right.SourceCount &&
		math.Float64bits(left.NDCG) == math.Float64bits(right.NDCG) &&
		math.Float64bits(left.NormalizedPrecision) == math.Float64bits(right.NormalizedPrecision) &&
		math.Float64bits(left.RelevantQueryCoverage) == math.Float64bits(right.RelevantQueryCoverage) &&
		math.Float64bits(left.KnownRelevantHitRate) == math.Float64bits(right.KnownRelevantHitRate)
}

func calibrationSourceHash(manifest qualityManifest) string {
	values := make([]string, 0)
	for _, source := range manifest.Sources {
		if source.Split == "calibration" {
			values = append(values, source.ID+"\x00"+source.SHA256)
		}
	}
	sort.Strings(values)
	return qualityValuesHash(values)
}

func calibrationAssetHash(manifest qualityManifest) string {
	values := make([]string, 0)
	for _, source := range manifest.Sources {
		if source.Split == "calibration" {
			values = append(values, "source\x00"+source.ID+"\x00"+source.SHA256)
		}
	}
	for _, reference := range manifest.LabelSets {
		if reference.Split == "calibration" {
			values = append(values, "labels\x00"+reference.Language+"\x00"+reference.SHA256)
		}
	}
	sort.Strings(values)
	return qualityValuesHash(values)
}

func qualityValuesHash(values []string) string {
	hasher := sha256.New()
	for _, value := range values {
		_, _ = hasher.Write([]byte(value))
		_, _ = hasher.Write([]byte{0})
	}
	return fmt.Sprintf("%x", hasher.Sum(nil))
}
