package recommend

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const qualityAssetRoot = "testdata/quality"

type qualityManifest struct {
	SchemaVersion       int                           `json:"schemaVersion"`
	Sources             []qualitySource               `json:"sources"`
	LabelSets           []qualityLabelSetReference    `json:"labelSets"`
	HoldoutAdjudication *qualityAdjudicationReference `json:"holdoutAdjudication,omitempty"`
}

type qualitySource struct {
	ID         string            `json:"id"`
	Title      string            `json:"title"`
	Split      string            `json:"split"`
	Language   string            `json:"language"`
	Path       string            `json:"path"`
	Buckets    []string          `json:"buckets"`
	SHA256     string            `json:"sha256"`
	Author     string            `json:"author"`
	Provenance qualityProvenance `json:"provenance"`
	Review     qualityReview     `json:"review"`
}

type qualityProvenance struct {
	Source  string `json:"source"`
	License string `json:"license"`
}

type qualityReview struct {
	Reviewed bool   `json:"reviewed"`
	Reviewer string `json:"reviewer"`
	Notes    string `json:"notes"`
}

type qualityLabelSetReference struct {
	Split    string `json:"split"`
	Language string `json:"language"`
	Path     string `json:"path"`
	SHA256   string `json:"sha256"`
}

type qualityAdjudicationReference struct {
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	PairCount int    `json:"pairCount"`
}

type qualityAdjudicationRecord struct {
	SchemaVersion int                         `json:"schemaVersion"`
	Method        string                      `json:"method"`
	Rubric        map[string]string           `json:"rubric"`
	ReviewedAt    string                      `json:"reviewedAt"`
	Reviews       []qualityAdjudicationReview `json:"reviews"`
}

type qualityAdjudicationReview struct {
	Language string                   `json:"language"`
	Reviewer string                   `json:"reviewer"`
	Pairs    []qualityAdjudicatedPair `json:"pairs"`
}

type qualityAdjudicatedPair struct {
	SourceID    string `json:"sourceId"`
	CandidateID string `json:"candidateId"`
	Grade       int    `json:"grade"`
	Rationale   string `json:"rationale"`
}

type qualityJudgmentReview struct {
	Reviewer          string `json:"reviewer"`
	Record            string `json:"record"`
	Method            string `json:"method"`
	ReviewedPairCount int    `json:"reviewedPairCount"`
}

type qualityJudgmentSet struct {
	Split              string                  `json:"split"`
	Language           string                  `json:"language"`
	PoolKind           string                  `json:"poolKind"`
	GridTupleCount     int                     `json:"gridTupleCount,omitempty"`
	AdjudicationReview *qualityJudgmentReview  `json:"adjudicationReview,omitempty"`
	Sources            []qualitySourceJudgment `json:"sources"`
}

type qualitySourceJudgment struct {
	SourceID        string                     `json:"sourceId"`
	None            bool                       `json:"none"`
	KnownRelevant   []string                   `json:"knownRelevant"`
	ProductionTop10 []string                   `json:"productionTop10,omitempty"`
	Candidates      []qualityCandidateJudgment `json:"candidates"`
}

type qualityCandidateJudgment struct {
	CandidateID string   `json:"candidateId"`
	Grade       int      `json:"grade"`
	Discovery   []string `json:"discovery"`
}

type qualityMetricResult struct {
	NDCG                float64
	NormalizedPrecision float64
	TopFiveRelevant     bool
	KnownRelevantHit    bool
}

func TestQualityAssetSchema(t *testing.T) {
	manifest, err := loadQualityManifest(qualityAssetRoot)
	if err != nil {
		t.Fatalf("loadQualityManifest(repository assets) error = %v", err)
	}
	if err := validateQualityManifest(qualityAssetRoot, manifest); err != nil {
		t.Fatalf("validateQualityManifest(repository assets) error = %v", err)
	}

	root := t.TempDir()
	writeQualityTestFile(t, root, "calibration/en/source.md", "# Retry Budgets\n\nA reviewed article about bounded retries and backoff.\n")
	writeQualityTestFile(t, root, "calibration/en/peer.md", "# Backoff Design\n\nA reviewed peer article about retry timing.\n")
	sources := []qualitySource{
		qualityTestSource(t, root, "cal-en-source", "Retry Budgets", "calibration", "en", "calibration/en/source.md", "author-a", "reviewer-b"),
		qualityTestSource(t, root, "cal-en-peer", "Backoff Design", "calibration", "en", "calibration/en/peer.md", "author-a", "reviewer-b"),
	}
	judgments := qualityJudgmentSet{
		Split: "calibration", Language: "en", PoolKind: "calibration-grid", GridTupleCount: 81,
		Sources: []qualitySourceJudgment{{
			SourceID: "cal-en-source", KnownRelevant: []string{"cal-en-peer"},
			Candidates: []qualityCandidateJudgment{{CandidateID: "cal-en-peer", Grade: 2, Discovery: []string{"search"}}},
		}},
	}
	writeQualityJSON(t, root, "calibration/labels-en.json", judgments)
	manifest = qualityManifest{
		SchemaVersion: 1,
		Sources:       sources,
		LabelSets: []qualityLabelSetReference{{
			Split: "calibration", Language: "en", Path: "calibration/labels-en.json",
			SHA256: qualityFileHash(t, filepath.Join(root, "calibration/labels-en.json")),
		}},
	}
	if err := validateQualityManifest(root, manifest); err != nil {
		t.Fatalf("validateQualityManifest(valid fixture) error = %v", err)
	}

	badHash := manifest
	badHash.Sources = append([]qualitySource(nil), manifest.Sources...)
	badHash.Sources[0].SHA256 = strings.Repeat("0", 64)
	if err := validateQualityManifest(root, badHash); err == nil {
		t.Fatal("validateQualityManifest(hash mismatch) error = nil")
	}
	badReview := manifest
	badReview.Sources = append([]qualitySource(nil), manifest.Sources...)
	badReview.Sources[0].Review.Reviewer = badReview.Sources[0].Author
	if err := validateQualityManifest(root, badReview); err == nil {
		t.Fatal("validateQualityManifest(non-independent review) error = nil")
	}

	sourceIndex := map[string]qualitySource{sources[0].ID: sources[0], sources[1].ID: sources[1]}
	badNone := judgments
	badNone.Sources = append([]qualitySourceJudgment(nil), judgments.Sources...)
	badNone.Sources[0].None = true
	if err := validateQualityJudgments(badNone, sourceIndex); err == nil {
		t.Fatal("validateQualityJudgments(none/positive conflict) error = nil")
	}
	badKnown := judgments
	badKnown.Sources = append([]qualitySourceJudgment(nil), judgments.Sources...)
	badKnown.Sources[0].KnownRelevant = []string{"missing"}
	if err := validateQualityJudgments(badKnown, sourceIndex); err == nil {
		t.Fatal("validateQualityJudgments(missing known-relevant grade) error = nil")
	}

	holdoutSet := qualityJudgmentSet{
		Split: "holdout", Language: "en", PoolKind: "holdout-active",
		AdjudicationReview: &qualityJudgmentReview{Reviewer: "blind-reviewer", Record: "holdout/review.json", Method: "article-content-only", ReviewedPairCount: 1},
		Sources:            []qualitySourceJudgment{{SourceID: "source", Candidates: []qualityCandidateJudgment{{CandidateID: "candidate", Grade: 1}}}},
	}
	holdoutRecord := qualityAdjudicationRecord{Reviews: []qualityAdjudicationReview{{
		Language: "en", Reviewer: "blind-reviewer", Pairs: []qualityAdjudicatedPair{{SourceID: "source", CandidateID: "candidate", Grade: 2, Rationale: "same core problem"}},
	}}}
	if err := validateHoldoutAdjudicationBinding(holdoutSet, qualityAdjudicationReference{Path: "holdout/review.json", PairCount: 1}, holdoutRecord); err == nil {
		t.Fatal("validateHoldoutAdjudicationBinding(grade mismatch) error = nil")
	}
}

func TestQualityCorpus(t *testing.T) {
	manifest, err := loadQualityManifest(qualityAssetRoot)
	if err != nil {
		t.Fatalf("loadQualityManifest() error = %v", err)
	}
	if err := validateQualityManifest(qualityAssetRoot, manifest); err != nil {
		t.Fatalf("validateQualityManifest() error = %v", err)
	}

	languageCounts := make(map[string]int)
	for _, source := range manifest.Sources {
		languageCounts[source.Language]++
	}
	for _, language := range []string{"zh-hans", "zh-hant", "en", "mixed"} {
		if languageCounts[language] == 0 {
			continue
		}
		language := language
		t.Run(language, func(t *testing.T) {
			validateQualityCorpusLanguage(t, manifest, language)
		})
	}
	if len(manifest.Sources) >= 80 {
		t.Run("complete", func(t *testing.T) {
			for _, language := range []string{"zh-hans", "zh-hant", "en", "mixed"} {
				if languageCounts[language] < 20 {
					t.Fatalf("language %s has %d sources, want at least 20", language, languageCounts[language])
				}
			}
		})
	}
}

func TestQualityMetricFormulas(t *testing.T) {
	judgment := qualitySourceJudgment{
		SourceID:      "source",
		KnownRelevant: []string{"a", "c"},
		Candidates: []qualityCandidateJudgment{
			{CandidateID: "a", Grade: 2, Discovery: []string{"search"}},
			{CandidateID: "b", Grade: 0, Discovery: []string{"production-top10"}},
			{CandidateID: "c", Grade: 1, Discovery: []string{"theme"}},
			{CandidateID: "d", Grade: 0, Discovery: []string{"production-top10"}},
			{CandidateID: "e", Grade: 0, Discovery: []string{"production-top10"}},
		},
	}
	result, err := computeQualityMetrics([]string{"c", "b", "a", "d", "e"}, judgment)
	if err != nil {
		t.Fatalf("computeQualityMetrics() error = %v", err)
	}
	actualDCG := 1.0 + 3.0/math.Log2(4)
	idealDCG := 3.0 + 1.0/math.Log2(3)
	if want := actualDCG / idealDCG; math.Abs(result.NDCG-want) > 1e-15 {
		t.Fatalf("NDCG = %.17g, want %.17g", result.NDCG, want)
	}
	if result.NormalizedPrecision != 1 || !result.TopFiveRelevant || !result.KnownRelevantHit {
		t.Fatalf("metric result = %#v, want normalized precision/coverage/hit", result)
	}

	if _, err := computeQualityMetrics([]string{"b"}, qualitySourceJudgment{
		SourceID: "none", None: true,
		Candidates: []qualityCandidateJudgment{{CandidateID: "b", Grade: 0, Discovery: []string{"search"}}},
	}); err == nil {
		t.Fatal("computeQualityMetrics(zero relevant denominator) error = nil")
	}
}

func validateQualityCorpusLanguage(t *testing.T, manifest qualityManifest, language string) {
	t.Helper()

	splitCounts := map[string]int{"calibration": 0, "holdout": 0}
	bucketsBySplit := map[string]map[string]bool{
		"calibration": {},
		"holdout":     {},
	}
	hashes := make(map[string]string)
	for _, source := range manifest.Sources {
		if source.Language != language {
			continue
		}
		splitCounts[source.Split]++
		for _, bucket := range source.Buckets {
			bucketsBySplit[source.Split][bucket] = true
		}
		if previousID, duplicate := hashes[source.SHA256]; duplicate {
			t.Errorf("sources %s and %s have identical content hash %s", previousID, source.ID, source.SHA256)
		}
		hashes[source.SHA256] = source.ID

		assetPath, err := qualityAssetPath(qualityAssetRoot, source.Path)
		if err != nil {
			t.Errorf("qualityAssetPath(%s) error = %v", source.ID, err)
			continue
		}
		data, err := os.ReadFile(assetPath)
		if err != nil {
			t.Errorf("ReadFile(%s) error = %v", source.ID, err)
			continue
		}
		content := string(data)
		if len(data) < 300 || !strings.Contains(content, "# "+source.Title) || !strings.Contains(content, "\n## ") {
			t.Errorf("source %s lacks natural Markdown article structure", source.ID)
		}
		if containsString(source.Buckets, "no-tag") && strings.Contains(content, "\ntags:") {
			t.Errorf("source %s is marked no-tag but contains tags frontmatter", source.ID)
		}
		if containsString(source.Buckets, "no-link") && strings.Contains(content, "[[") {
			t.Errorf("source %s is marked no-link but contains a wikilink", source.ID)
		}
	}

	requiredBuckets := []string{"long-form", "multi-topic", "no-tag", "no-link", "moc", "engineering"}
	for split, count := range splitCounts {
		if count < 10 {
			t.Errorf("%s/%s source count = %d, want at least 10", split, language, count)
		}
		for _, bucket := range requiredBuckets {
			if !bucketsBySplit[split][bucket] {
				t.Errorf("%s/%s lacks required %s source", split, language, bucket)
			}
		}
	}
}

func loadQualityManifest(root string) (qualityManifest, error) {
	data, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		return qualityManifest{}, err
	}
	var manifest qualityManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return qualityManifest{}, err
	}
	return manifest, nil
}

func validateQualityManifest(root string, manifest qualityManifest) error {
	if manifest.SchemaVersion != 1 {
		return fmt.Errorf("quality manifest schemaVersion = %d, want 1", manifest.SchemaVersion)
	}
	sources := make(map[string]qualitySource, len(manifest.Sources))
	paths := make(map[string]struct{}, len(manifest.Sources))
	for _, source := range manifest.Sources {
		if err := validateQualitySource(root, source); err != nil {
			return err
		}
		if _, exists := sources[source.ID]; exists {
			return fmt.Errorf("duplicate quality source ID %q", source.ID)
		}
		if _, exists := paths[source.Path]; exists {
			return fmt.Errorf("duplicate quality source path %q", source.Path)
		}
		sources[source.ID] = source
		paths[source.Path] = struct{}{}
	}

	adjudication, err := loadQualityAdjudication(root, manifest.HoldoutAdjudication, sources)
	if err != nil {
		return err
	}

	labelKeys := make(map[string]struct{}, len(manifest.LabelSets))
	for _, reference := range manifest.LabelSets {
		if !validQualitySplit(reference.Split) || !validQualityLanguage(reference.Language) {
			return fmt.Errorf("invalid quality label split/language %q/%q", reference.Split, reference.Language)
		}
		key := reference.Split + "/" + reference.Language
		if _, exists := labelKeys[key]; exists {
			return fmt.Errorf("duplicate quality label set %q", key)
		}
		labelKeys[key] = struct{}{}
		labelPath, err := qualityAssetPath(root, reference.Path)
		if err != nil {
			return err
		}
		if err := validateQualityFileHash(labelPath, reference.SHA256); err != nil {
			return err
		}
		data, err := os.ReadFile(labelPath)
		if err != nil {
			return err
		}
		var judgments qualityJudgmentSet
		if err := json.Unmarshal(data, &judgments); err != nil {
			return err
		}
		if judgments.Split != reference.Split || judgments.Language != reference.Language {
			return fmt.Errorf("quality label metadata mismatch for %q", reference.Path)
		}
		if err := validateQualityJudgments(judgments, sources); err != nil {
			return err
		}
		if reference.Split == "holdout" {
			if adjudication == nil || manifest.HoldoutAdjudication == nil {
				return fmt.Errorf("holdout label set %q lacks a blinded adjudication record", reference.Path)
			}
			if err := validateHoldoutAdjudicationBinding(judgments, *manifest.HoldoutAdjudication, *adjudication); err != nil {
				return err
			}
		} else if judgments.AdjudicationReview != nil {
			return fmt.Errorf("calibration label set %q unexpectedly has holdout adjudication metadata", reference.Path)
		}
	}
	return nil
}

func loadQualityAdjudication(root string, reference *qualityAdjudicationReference, sources map[string]qualitySource) (*qualityAdjudicationRecord, error) {
	if reference == nil {
		return nil, nil
	}
	assetPath, err := qualityAssetPath(root, reference.Path)
	if err != nil {
		return nil, err
	}
	if err := validateQualityFileHash(assetPath, reference.SHA256); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(assetPath)
	if err != nil {
		return nil, err
	}
	var record qualityAdjudicationRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, err
	}
	if record.SchemaVersion != 1 || strings.TrimSpace(record.Method) == "" || strings.TrimSpace(record.ReviewedAt) == "" {
		return nil, fmt.Errorf("holdout adjudication record metadata is incomplete")
	}
	for _, grade := range []string{"0", "1", "2"} {
		if strings.TrimSpace(record.Rubric[grade]) == "" {
			return nil, fmt.Errorf("holdout adjudication rubric is missing grade %s", grade)
		}
	}

	pairCount := 0
	seenLanguages := make(map[string]struct{}, len(record.Reviews))
	seenPairs := make(map[string]struct{})
	for _, review := range record.Reviews {
		if !validQualityLanguage(review.Language) || strings.TrimSpace(review.Reviewer) == "" {
			return nil, fmt.Errorf("invalid holdout adjudication reviewer for %q", review.Language)
		}
		if _, exists := seenLanguages[review.Language]; exists {
			return nil, fmt.Errorf("duplicate holdout adjudication language %q", review.Language)
		}
		seenLanguages[review.Language] = struct{}{}
		for _, pair := range review.Pairs {
			source, sourceOK := sources[pair.SourceID]
			candidate, candidateOK := sources[pair.CandidateID]
			if !sourceOK || !candidateOK || source.Split != "holdout" || candidate.Split != "holdout" || source.Language != review.Language || pair.SourceID == pair.CandidateID {
				return nil, fmt.Errorf("invalid holdout adjudication pair %q -> %q", pair.SourceID, pair.CandidateID)
			}
			if pair.Grade < 0 || pair.Grade > 2 || strings.TrimSpace(pair.Rationale) == "" {
				return nil, fmt.Errorf("incomplete holdout adjudication for %q -> %q", pair.SourceID, pair.CandidateID)
			}
			key := pair.SourceID + "\x00" + pair.CandidateID
			if _, exists := seenPairs[key]; exists {
				return nil, fmt.Errorf("duplicate holdout adjudication pair %q -> %q", pair.SourceID, pair.CandidateID)
			}
			seenPairs[key] = struct{}{}
			pairCount++
		}
	}
	if pairCount != reference.PairCount || pairCount == 0 {
		return nil, fmt.Errorf("holdout adjudication pair count = %d, want %d", pairCount, reference.PairCount)
	}
	return &record, nil
}

func validateHoldoutAdjudicationBinding(set qualityJudgmentSet, reference qualityAdjudicationReference, record qualityAdjudicationRecord) error {
	if set.AdjudicationReview == nil {
		return fmt.Errorf("holdout %s labels lack adjudication metadata", set.Language)
	}
	var review *qualityAdjudicationReview
	for index := range record.Reviews {
		if record.Reviews[index].Language == set.Language {
			review = &record.Reviews[index]
			break
		}
	}
	if review == nil {
		return fmt.Errorf("holdout %s labels lack a matching adjudication review", set.Language)
	}
	metadata := set.AdjudicationReview
	if metadata.Reviewer != review.Reviewer || metadata.Record != reference.Path || strings.TrimSpace(metadata.Method) == "" || metadata.ReviewedPairCount != len(review.Pairs) {
		return fmt.Errorf("holdout %s adjudication metadata does not match its record", set.Language)
	}
	grades := make(map[string]int)
	for _, judgment := range set.Sources {
		for _, candidate := range judgment.Candidates {
			grades[judgment.SourceID+"\x00"+candidate.CandidateID] = candidate.Grade
		}
	}
	for _, pair := range review.Pairs {
		key := pair.SourceID + "\x00" + pair.CandidateID
		grade, ok := grades[key]
		if !ok || grade != pair.Grade {
			return fmt.Errorf("holdout adjudication grade mismatch for %q -> %q", pair.SourceID, pair.CandidateID)
		}
	}
	return nil
}

func validateQualitySource(root string, source qualitySource) error {
	if strings.TrimSpace(source.ID) == "" || strings.TrimSpace(source.Title) == "" {
		return fmt.Errorf("quality source ID/title is required")
	}
	if !validQualitySplit(source.Split) || !validQualityLanguage(source.Language) {
		return fmt.Errorf("invalid quality source split/language for %q", source.ID)
	}
	if strings.TrimSpace(source.Author) == "" || !source.Review.Reviewed || strings.TrimSpace(source.Review.Reviewer) == "" || strings.TrimSpace(source.Review.Notes) == "" {
		return fmt.Errorf("quality source %q lacks completed semantic review", source.ID)
	}
	if source.Author == source.Review.Reviewer {
		return fmt.Errorf("quality source %q review is not independent", source.ID)
	}
	if strings.TrimSpace(source.Provenance.Source) == "" || strings.TrimSpace(source.Provenance.License) == "" {
		return fmt.Errorf("quality source %q lacks provenance/license", source.ID)
	}
	seenBuckets := make(map[string]struct{}, len(source.Buckets))
	for _, bucket := range source.Buckets {
		if !validQualityBucket(bucket) {
			return fmt.Errorf("quality source %q has invalid bucket %q", source.ID, bucket)
		}
		if _, exists := seenBuckets[bucket]; exists {
			return fmt.Errorf("quality source %q repeats bucket %q", source.ID, bucket)
		}
		seenBuckets[bucket] = struct{}{}
	}
	assetPath, err := qualityAssetPath(root, source.Path)
	if err != nil {
		return err
	}
	return validateQualityFileHash(assetPath, source.SHA256)
}

func validateQualityJudgments(set qualityJudgmentSet, sources map[string]qualitySource) error {
	if set.PoolKind != "calibration-grid" && set.PoolKind != "holdout-active" {
		return fmt.Errorf("invalid quality pool kind %q", set.PoolKind)
	}
	if set.PoolKind == "calibration-grid" && set.GridTupleCount != 81 {
		return fmt.Errorf("calibration grid tuple count = %d, want 81", set.GridTupleCount)
	}
	seenSources := make(map[string]struct{}, len(set.Sources))
	for _, judgment := range set.Sources {
		source, ok := sources[judgment.SourceID]
		if !ok || source.Split != set.Split || source.Language != set.Language {
			return fmt.Errorf("invalid judged source %q", judgment.SourceID)
		}
		if _, exists := seenSources[judgment.SourceID]; exists {
			return fmt.Errorf("duplicate judged source %q", judgment.SourceID)
		}
		seenSources[judgment.SourceID] = struct{}{}
		grades := make(map[string]int, len(judgment.Candidates))
		for _, candidate := range judgment.Candidates {
			candidateSource, ok := sources[candidate.CandidateID]
			if !ok || candidateSource.Split != set.Split || candidate.CandidateID == judgment.SourceID {
				return fmt.Errorf("invalid candidate %q for %q", candidate.CandidateID, judgment.SourceID)
			}
			if candidate.Grade < 0 || candidate.Grade > 2 || len(candidate.Discovery) == 0 {
				return fmt.Errorf("invalid candidate grade/discovery for %q -> %q", judgment.SourceID, candidate.CandidateID)
			}
			if _, exists := grades[candidate.CandidateID]; exists {
				return fmt.Errorf("duplicate candidate %q for %q", candidate.CandidateID, judgment.SourceID)
			}
			for _, discovery := range candidate.Discovery {
				if !validQualityDiscovery(discovery) {
					return fmt.Errorf("invalid discovery %q", discovery)
				}
			}
			grades[candidate.CandidateID] = candidate.Grade
		}
		for _, candidateID := range judgment.KnownRelevant {
			if grades[candidateID] < 1 {
				return fmt.Errorf("known-relevant %q for %q lacks positive grade", candidateID, judgment.SourceID)
			}
		}
		positive := 0
		for _, grade := range grades {
			if grade >= 1 {
				positive++
			}
		}
		if judgment.None && (positive > 0 || len(judgment.KnownRelevant) > 0) {
			return fmt.Errorf("none judgment for %q conflicts with positive candidate", judgment.SourceID)
		}
		if !judgment.None && positive == 0 {
			return fmt.Errorf("judgment for %q has zero relevant denominator without none", judgment.SourceID)
		}
	}
	return nil
}

func computeQualityMetrics(ranking []string, judgment qualitySourceJudgment) (qualityMetricResult, error) {
	grades := make(map[string]int, len(judgment.Candidates))
	relevantGrades := make([]int, 0, len(judgment.Candidates))
	for _, candidate := range judgment.Candidates {
		grades[candidate.CandidateID] = candidate.Grade
		if candidate.Grade >= 1 {
			relevantGrades = append(relevantGrades, candidate.Grade)
		}
	}
	if len(relevantGrades) == 0 {
		return qualityMetricResult{}, fmt.Errorf("quality source %q has zero relevant denominator", judgment.SourceID)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(relevantGrades)))
	idealDCG := 0.0
	for rank, grade := range relevantGrades {
		if rank >= 5 {
			break
		}
		idealDCG += qualityGain(grade) / math.Log2(float64(rank+2))
	}
	if idealDCG == 0 {
		return qualityMetricResult{}, fmt.Errorf("quality source %q has zero IDCG", judgment.SourceID)
	}

	dcg := 0.0
	relevantTopFive := 0
	known := make(map[string]struct{}, len(judgment.KnownRelevant))
	for _, candidateID := range judgment.KnownRelevant {
		known[candidateID] = struct{}{}
	}
	knownHit := false
	for rank, candidateID := range ranking {
		if rank >= 5 {
			break
		}
		grade := grades[candidateID]
		dcg += qualityGain(grade) / math.Log2(float64(rank+2))
		if grade >= 1 {
			relevantTopFive++
		}
		if _, ok := known[candidateID]; ok {
			knownHit = true
		}
	}
	precision := float64(relevantTopFive) / 5.0
	oraclePrecision := float64(min(5, len(relevantGrades))) / 5.0
	if oraclePrecision == 0 {
		return qualityMetricResult{}, fmt.Errorf("quality source %q has zero oracle precision", judgment.SourceID)
	}
	return qualityMetricResult{
		NDCG:                dcg / idealDCG,
		NormalizedPrecision: precision / oraclePrecision,
		TopFiveRelevant:     relevantTopFive > 0,
		KnownRelevantHit:    knownHit,
	}, nil
}

func qualityGain(grade int) float64 {
	return math.Pow(2, float64(grade)) - 1
}

func validQualitySplit(value string) bool {
	return value == "calibration" || value == "holdout"
}

func validQualityLanguage(value string) bool {
	switch value {
	case "zh-hans", "zh-hant", "en", "mixed":
		return true
	default:
		return false
	}
}

func validQualityBucket(value string) bool {
	switch value {
	case "long-form", "multi-topic", "no-tag", "no-link", "moc", "engineering":
		return true
	default:
		return false
	}
}

func validQualityDiscovery(value string) bool {
	switch value {
	case "production-top10", "theme", "moc", "search":
		return true
	default:
		return false
	}
}

func qualityAssetPath(root string, relPath string) (string, error) {
	cleaned := filepath.Clean(strings.TrimSpace(relPath))
	if cleaned == "." || cleaned == "" || filepath.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid quality asset path %q", relPath)
	}
	return filepath.Join(root, cleaned), nil
}

func validateQualityFileHash(path string, want string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(want) != 64 {
		return fmt.Errorf("quality asset %q has malformed SHA-256", path)
	}
	if _, err := hex.DecodeString(want); err != nil {
		return fmt.Errorf("quality asset %q has malformed SHA-256", path)
	}
	got := fmt.Sprintf("%x", sha256.Sum256(data))
	if got != want {
		return fmt.Errorf("quality asset %q SHA-256 = %s, want %s", path, got, want)
	}
	return nil
}

func writeQualityTestFile(t *testing.T, root string, relPath string, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeQualityJSON(t *testing.T, root string, relPath string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeQualityTestFile(t, root, relPath, string(data)+"\n")
}

func qualityTestSource(t *testing.T, root string, id string, title string, split string, language string, relPath string, author string, reviewer string) qualitySource {
	t.Helper()
	return qualitySource{
		ID: id, Title: title, Split: split, Language: language, Path: relPath,
		Buckets: []string{"engineering"}, SHA256: qualityFileHash(t, filepath.Join(root, filepath.FromSlash(relPath))), Author: author,
		Provenance: qualityProvenance{Source: "original", License: "project-license"},
		Review:     qualityReview{Reviewed: true, Reviewer: reviewer, Notes: "Independently reviewed for natural structure and semantic clarity."},
	}
}

func qualityFileHash(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(data))
}
