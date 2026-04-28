package recommend

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"reflect"
	"sync"
	"testing"

	"github.com/go-ego/gse"
	"github.com/simp-lee/obsite/internal/model"
)

const (
	trimpathTokenizerChildEnv = "OBSITE_TRIMPATH_TOKENIZER_CHILD"
	lazyTokenizerChildEnv     = "OBSITE_LAZY_TOKENIZER_CHILD"
)

func TestTokenizeSupportsEnglishCJKAndMixedText(t *testing.T) {
	t.Parallel()

	english, err := Tokenize("Static site generator")
	if err != nil {
		t.Fatalf("Tokenize(english) error = %v", err)
	}
	if !reflect.DeepEqual(english, []string{"static", "site", "generator"}) {
		t.Fatalf("Tokenize(english) = %#v, want %#v", english, []string{"static", "site", "generator"})
	}

	cjk, err := Tokenize("你好世界")
	if err != nil {
		t.Fatalf("Tokenize(cjk) error = %v", err)
	}
	if len(cjk) == 0 {
		t.Fatal("Tokenize(cjk) returned no tokens")
	}
	for _, singleRune := range []string{"你", "好", "世", "界"} {
		if containsToken(cjk, singleRune) {
			t.Fatalf("Tokenize(cjk) = %#v, want gse segmentation instead of rune-by-rune output", cjk)
		}
	}

	mixed, err := Tokenize("Static 你好世界 generator")
	if err != nil {
		t.Fatalf("Tokenize(mixed) error = %v", err)
	}
	if !containsToken(mixed, "static") || !containsToken(mixed, "generator") {
		t.Fatalf("Tokenize(mixed) = %#v, want Latin whitespace tokens preserved", mixed)
	}
	if !containsCJKToken(mixed) {
		t.Fatalf("Tokenize(mixed) = %#v, want at least one CJK token", mixed)
	}
}

func TestTokenizeKeepsWhitespaceDelimitedEngineeringTermsIntact(t *testing.T) {
	t.Parallel()

	got, err := Tokenize("Node.JS C++ snake_case kebab-case path/to/file")
	if err != nil {
		t.Fatalf("Tokenize(engineering terms) error = %v", err)
	}

	want := []string{"node.js", "c++", "snake_case", "kebab-case", "path/to/file"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Tokenize(engineering terms) = %#v, want %#v", got, want)
	}
}

func TestTokenizeSupportsEmbeddedDictionaryUnderTrimpath(t *testing.T) {
	if os.Getenv(trimpathTokenizerChildEnv) == "1" {
		cjk, err := Tokenize("你好世界")
		if err != nil {
			t.Fatalf("Tokenize(cjk) error = %v", err)
		}
		if !containsCJKToken(cjk) {
			t.Fatalf("Tokenize(cjk) = %#v, want at least one CJK token", cjk)
		}
		return
	}

	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd() error = %v", err)
	}

	cmd := exec.Command("go", "test", "-trimpath", ".", "-run", "^TestTokenizeSupportsEmbeddedDictionaryUnderTrimpath$", "-count=1")
	cmd.Dir = workingDir
	cmd.Env = append(os.Environ(), trimpathTokenizerChildEnv+"=1")

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go test -trimpath tokenizer regression failed: %v\n%s", err, output)
	}
}

func TestTokenizeDefersDictionaryLoadingForLatinOnlyInput(t *testing.T) {
	if os.Getenv(lazyTokenizerChildEnv) == "1" {
		defaultSegmenterOnce = sync.Once{}
		defaultSegmenter = gse.Segmenter{}
		defaultSegmenterErr = nil

		originalLoader := loadEmbeddedSegmenter
		defer func() {
			loadEmbeddedSegmenter = originalLoader
		}()

		loadCalls := 0
		loadEmbeddedSegmenter = func(segmenter *gse.Segmenter) error {
			loadCalls++
			segmenter.SkipLog = true
			return segmenter.LoadDictEmbed()
		}

		latin, err := Tokenize("Static site generator release pipeline")
		if err != nil {
			t.Fatalf("Tokenize(latin) error = %v", err)
		}
		if !reflect.DeepEqual(latin, []string{"static", "site", "generator", "release", "pipeline"}) {
			t.Fatalf("Tokenize(latin) = %#v, want Latin tokens without gse dependency", latin)
		}
		if loadCalls != 0 {
			t.Fatalf("loadEmbeddedSegmenter calls after Latin-only Tokenize = %d, want %d", loadCalls, 0)
		}

		if _, err := Tokenize("你好世界"); err != nil {
			t.Fatalf("Tokenize(cjk) error = %v", err)
		}
		if loadCalls != 1 {
			t.Fatalf("loadEmbeddedSegmenter calls after first CJK Tokenize = %d, want %d", loadCalls, 1)
		}

		if _, err := Tokenize("静态站点 生成器"); err != nil {
			t.Fatalf("Tokenize(second cjk) error = %v", err)
		}
		if loadCalls != 1 {
			t.Fatalf("loadEmbeddedSegmenter calls after repeated CJK Tokenize = %d, want %d", loadCalls, 1)
		}
		return
	}

	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd() error = %v", err)
	}

	cmd := exec.Command("go", "test", ".", "-run", "^TestTokenizeDefersDictionaryLoadingForLatinOnlyInput$", "-count=1")
	cmd.Dir = workingDir
	cmd.Env = append(os.Environ(), lazyTokenizerChildEnv+"=1")

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go test tokenizer lazy-load regression failed: %v\n%s", err, output)
	}
}

func TestBuildRanksByBM25AcrossLanguages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		query string
		match string
		other string
	}{
		{
			name:  "english",
			query: "static site generator build pipeline",
			match: "static site generator release pipeline",
			other: "kitchen recipe sourdough bread",
		},
		{
			name:  "cjk",
			query: "静态站点生成器 构建 流程",
			match: "静态站点生成器 发布 流程",
			other: "厨房 食谱 烘焙 面包",
		},
		{
			name:  "mixed",
			query: "static 站点 generator 发布",
			match: "static 站点 generator 构建",
			other: "travel itinerary seaside journal",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			idx := testRecommendIndex(
				testRecommendNote("notes/alpha.md", "alpha", "Alpha", tt.query),
				testRecommendNote("notes/beta.md", "beta", "Beta", tt.match),
				testRecommendNote("notes/gamma.md", "gamma", "Gamma", tt.other),
			)

			relatedByPath, err := Build(idx, &model.LinkGraph{Forward: map[string][]string{}, Backward: map[string][]string{}}, 1)
			if err != nil {
				t.Fatalf("Build() error = %v", err)
			}

			related := relatedByPath["notes/alpha.md"]
			if len(related) != 1 || related[0].Note == nil || related[0].Note.RelPath != "notes/beta.md" {
				t.Fatalf("Build()[alpha] = %#v, want beta to rank first", related)
			}
		})
	}
}

func TestBM25ScoreStableAcrossRepeatedCalls(t *testing.T) {
	t.Parallel()

	queryTerms := map[string]int{
		"dominant": 10_000_000_000_000_000,
	}
	termFreq := map[string]int{
		"dominant": 1,
	}
	docFreq := map[string]int{
		"dominant": 1,
	}
	for index := 0; index < 64; index++ {
		term := fmt.Sprintf("term-%02d", index)
		queryTerms[term] = 1
		termFreq[term] = 1
		docFreq[term] = 1
	}

	bm25 := &BM25{
		documents: map[string]bm25Document{
			"candidate": {
				termFreq: termFreq,
				length:   len(termFreq),
			},
		},
		documentN: 2,
		avgDocLen: float64(len(termFreq)),
		docFreq:   docFreq,
		k1:        defaultBM25K1,
		b:         defaultBM25B,
	}

	want := math.Float64bits(scoreBM25ForTest(bm25, queryTerms, "candidate"))
	for iteration := 0; iteration < 256; iteration++ {
		got := math.Float64bits(scoreBM25ForTest(bm25, queryTerms, "candidate"))
		if got != want {
			t.Fatalf("Score() bits changed between repeated calls: got %#x, want %#x", got, want)
		}
	}
}

func TestRecommenderBoostsSharedTagsAndMutualLinks(t *testing.T) {
	t.Parallel()

	t.Run("shared tag boost", func(t *testing.T) {
		idx := testRecommendIndex(
			testRecommendNote("notes/alpha.md", "alpha", "Alpha", "static site generator", "go"),
			testRecommendNote("notes/beta.md", "beta", "Beta", "static site generator"),
			testRecommendNote("notes/gamma.md", "gamma", "Gamma", "static site generator", "go"),
		)

		recommender, err := New(idx, &model.LinkGraph{Forward: map[string][]string{}, Backward: map[string][]string{}})
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}

		alpha := idx.Notes["notes/alpha.md"]
		beta := idx.Notes["notes/beta.md"]
		gamma := idx.Notes["notes/gamma.md"]
		betaScore := scoreRelatedPairForTest(recommender, alpha, beta)
		gammaScore := scoreRelatedPairForTest(recommender, alpha, gamma)
		if gammaScore <= betaScore {
			t.Fatalf("Score(alpha, gamma) = %v, want greater than Score(alpha, beta) = %v", gammaScore, betaScore)
		}

		related := recommender.Related(alpha, 2)
		if len(related) != 2 || related[0].Note == nil || related[0].Note.RelPath != "notes/gamma.md" {
			t.Fatalf("Related(alpha) = %#v, want gamma to outrank beta via shared-tag boost", related)
		}
		if related[0].Score != gammaScore {
			t.Fatalf("Related(alpha)[0].Score = %v, want Score(alpha, gamma) = %v", related[0].Score, gammaScore)
		}
	})

	t.Run("mutual wikilink boost", func(t *testing.T) {
		idx := testRecommendIndex(
			testRecommendNote("notes/alpha.md", "alpha", "Alpha", "shared term cluster"),
			testRecommendNote("notes/aaron.md", "aaron", "Aaron", "shared term cluster"),
			testRecommendNote("notes/beta.md", "beta", "Beta", "shared term cluster"),
		)

		graph := &model.LinkGraph{
			Forward: map[string][]string{
				"notes/alpha.md": {"notes/beta.md"},
				"notes/beta.md":  {"notes/alpha.md"},
			},
			Backward: map[string][]string{
				"notes/alpha.md": {"notes/beta.md"},
				"notes/beta.md":  {"notes/alpha.md"},
			},
		}

		recommender, err := New(idx, graph)
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}

		alpha := idx.Notes["notes/alpha.md"]
		beta := idx.Notes["notes/beta.md"]
		aaron := idx.Notes["notes/aaron.md"]
		betaScore := scoreRelatedPairForTest(recommender, alpha, beta)
		aaronScore := scoreRelatedPairForTest(recommender, alpha, aaron)
		if betaScore <= aaronScore {
			t.Fatalf("Score(alpha, beta) = %v, want greater than Score(alpha, aaron) = %v", betaScore, aaronScore)
		}

		related := recommender.Related(alpha, 2)
		if len(related) != 2 || related[0].Note == nil || related[0].Note.RelPath != "notes/beta.md" {
			t.Fatalf("Related(alpha) = %#v, want beta to outrank aaron via mutual-wikilink boost", related)
		}
		if related[0].Score != betaScore {
			t.Fatalf("Related(alpha)[0].Score = %v, want Score(alpha, beta) = %v", related[0].Score, betaScore)
		}
	})
}

func TestRecommenderRequiresPositiveBM25ForStructuralBoosts(t *testing.T) {
	t.Parallel()

	t.Run("shared tags alone do not recall unrelated notes", func(t *testing.T) {
		idx := testRecommendIndex(
			testRecommendNote("notes/alpha.md", "alpha", "Alpha", "common consensus quorum leases", "systems"),
			testRecommendNote("notes/beta.md", "beta", "Beta", "common lexical overlap"),
			testRecommendNote("notes/gamma.md", "gamma", "Gamma", "sourdough starter hydration crumb", "systems"),
			testRecommendNote("notes/delta.md", "delta", "Delta", "common fallback term"),
		)

		recommender, err := New(idx, &model.LinkGraph{Forward: map[string][]string{}, Backward: map[string][]string{}})
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}

		alpha := idx.Notes["notes/alpha.md"]
		beta := idx.Notes["notes/beta.md"]
		gamma := idx.Notes["notes/gamma.md"]
		betaScore := scoreRelatedPairForTest(recommender, alpha, beta)
		gammaScore := scoreRelatedPairForTest(recommender, alpha, gamma)
		if betaScore <= 0 {
			t.Fatalf("Score(alpha, beta) = %v, want positive BM25 baseline for weak lexical match", betaScore)
		}
		if gammaScore != 0 {
			t.Fatalf("Score(alpha, gamma) = %v, want zero without BM25 baseline", gammaScore)
		}

		related := recommender.Related(alpha, 2)
		if got := relatedRelPaths(related); !reflect.DeepEqual(got, []string{"notes/beta.md", "notes/delta.md"}) {
			t.Fatalf("Related(alpha) paths = %#v, want %#v", got, []string{"notes/beta.md", "notes/delta.md"})
		}
	})

	t.Run("mutual wikilinks alone do not recall unrelated notes", func(t *testing.T) {
		idx := testRecommendIndex(
			testRecommendNote("notes/alpha.md", "alpha", "Alpha", "orchard pruning espalier trellis"),
			testRecommendNote("notes/beta.md", "beta", "Beta", "vector databases embedding recall latency"),
		)

		graph := &model.LinkGraph{
			Forward: map[string][]string{
				"notes/alpha.md": {"notes/beta.md"},
				"notes/beta.md":  {"notes/alpha.md"},
			},
			Backward: map[string][]string{
				"notes/alpha.md": {"notes/beta.md"},
				"notes/beta.md":  {"notes/alpha.md"},
			},
		}

		recommender, err := New(idx, graph)
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}

		alpha := idx.Notes["notes/alpha.md"]
		beta := idx.Notes["notes/beta.md"]
		if got := scoreRelatedPairForTest(recommender, alpha, beta); got != 0 {
			t.Fatalf("Score(alpha, beta) = %v, want zero without BM25 baseline", got)
		}
		if got := relatedRelPaths(recommender.Related(alpha, 1)); len(got) != 0 {
			t.Fatalf("Related(alpha) paths = %#v, want no zero-BM25 recall", got)
		}
	})
}

func TestRecommenderCandidateSetExcludesBoostOnlyDocs(t *testing.T) {
	t.Parallel()

	idx := testRecommendIndex(
		testRecommendNote("notes/alpha.md", "alpha", "Alpha", "distributed consensus lease quorum", "systems"),
		testRecommendNote("notes/beta.md", "beta", "Beta", "distributed quorum failover", "systems"),
		testRecommendNote("notes/gamma.md", "gamma", "Gamma", "sourdough starter hydration crumb", "systems"),
		testRecommendNote("notes/delta.md", "delta", "Delta", "vector database embedding latency"),
	)

	graph := &model.LinkGraph{
		Forward: map[string][]string{
			"notes/alpha.md": {"notes/delta.md"},
			"notes/delta.md": {"notes/alpha.md"},
		},
		Backward: map[string][]string{
			"notes/alpha.md": {"notes/delta.md"},
			"notes/delta.md": {"notes/alpha.md"},
		},
	}

	recommender, err := New(idx, graph)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	queryProfile := recommender.docMap["notes/alpha.md"]
	if got := recommender.relatedCandidatePaths(queryProfile); !reflect.DeepEqual(got, []string{"notes/beta.md"}) {
		t.Fatalf("relatedCandidatePaths(alpha) = %#v, want only BM25 candidates", got)
	}
}

func TestBuildDeterministicAcrossRepeatedCalls(t *testing.T) {
	t.Parallel()

	idx, graph := buildBenchmarkRecommendFixture(64)
	baseline, err := Build(idx, graph, 5)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	want := relatedByPathRelPaths(baseline)

	for iteration := 0; iteration < 32; iteration++ {
		got, err := Build(idx, graph, 5)
		if err != nil {
			t.Fatalf("Build() iteration %d error = %v", iteration, err)
		}
		if gotPaths := relatedByPathRelPaths(got); !reflect.DeepEqual(gotPaths, want) {
			t.Fatalf("Build() iteration %d paths = %#v, want %#v", iteration, gotPaths, want)
		}
	}
}

func TestRecommenderOrdersTiesDeterministically(t *testing.T) {
	t.Parallel()

	idx := testRecommendIndex(
		testRecommendNote("notes/query.md", "query", "Query", "shared term cluster"),
		testRecommendNote("notes/aaron.md", "aaron", "Aaron", "shared term cluster"),
		testRecommendNote("notes/beta.md", "beta", "Beta", "shared term cluster"),
	)

	recommender, err := New(idx, &model.LinkGraph{Forward: map[string][]string{}, Backward: map[string][]string{}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	for iteration := 0; iteration < 64; iteration++ {
		related := recommender.Related(idx.Notes["notes/query.md"], 2)
		if got := relatedRelPaths(related); !reflect.DeepEqual(got, []string{"notes/aaron.md", "notes/beta.md"}) {
			t.Fatalf("Related(query) paths = %#v, want %#v", got, []string{"notes/aaron.md", "notes/beta.md"})
		}
	}
}

func BenchmarkRelatedStructuralCandidateFanout(b *testing.B) {
	idx, graph := buildBoostOnlyFanoutFixture(3000, 16)
	recommender, err := New(idx, graph)
	if err != nil {
		b.Fatalf("New() error = %v", err)
	}

	query := idx.Notes["notes/query.md"]
	if query == nil {
		b.Fatal("benchmark query note missing")
	}

	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		related := recommender.Related(query, 8)
		if len(related) != 8 {
			b.Fatalf("Related(query) len = %d, want %d", len(related), 8)
		}
	}
}

func BenchmarkRelatedLargeMixedCorpus(b *testing.B) {
	idx, graph := buildBenchmarkRecommendFixture(3000)
	recommender, err := New(idx, graph)
	if err != nil {
		b.Fatalf("New() error = %v", err)
	}

	query := idx.Notes["notes/note-0000.md"]
	if query == nil {
		b.Fatal("benchmark query note missing")
	}

	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		related := recommender.Related(query, 8)
		if len(related) != 8 {
			b.Fatalf("Related(query) len = %d, want %d", len(related), 8)
		}
	}
}

func BenchmarkBuildLargeMixedCorpus(b *testing.B) {
	idx, graph := buildBenchmarkRecommendFixture(5000)

	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		relatedByPath, err := Build(idx, graph, 8)
		if err != nil {
			b.Fatalf("Build() error = %v", err)
		}
		if len(relatedByPath) != len(idx.Notes) {
			b.Fatalf("Build() related note count = %d, want %d", len(relatedByPath), len(idx.Notes))
		}
		if got := len(relatedByPath["notes/note-0000.md"]); got != 8 {
			b.Fatalf("Build()[note-0000] len = %d, want %d", got, 8)
		}
	}
}

func buildBenchmarkRecommendNotes(count int) []*model.Note {
	topics := []struct {
		tag     string
		content string
	}{
		{tag: "systems", content: "distributed systems quorum replica lease failover 站点 构建 发布"},
		{tag: "search", content: "bm25 retrieval ranking tokenizer inverted index 推荐 相关文章"},
		{tag: "writing", content: "editorial workflow draft revision evergreen notes 写作 发布"},
		{tag: "ops", content: "incident response latency tracing rollback alerting 运维 监控"},
		{tag: "product", content: "roadmap discovery positioning adoption feedback 产品 规划"},
	}

	notes := make([]*model.Note, 0, count)
	for index := 0; index < count; index++ {
		topic := topics[index%len(topics)]
		relPath := fmt.Sprintf("notes/note-%04d.md", index)
		slug := fmt.Sprintf("note-%04d", index)
		title := fmt.Sprintf("Benchmark Note %04d", index)
		content := fmt.Sprintf(
			"%s cohort%d sibling%d shard%d lexicon%d",
			topic.content,
			index%64,
			index%17,
			(index/len(topics))%13,
			(index/len(topics))%29,
		)
		notes = append(notes, testRecommendNote(relPath, slug, title, content, topic.tag, fmt.Sprintf("cohort-%02d", index%64)))
	}

	return notes
}

func buildBoostOnlyFanoutFixture(boostOnlyCount int, bm25MatchCount int) (*model.VaultIndex, *model.LinkGraph) {
	notes := make([]*model.Note, 0, 1+bm25MatchCount+(boostOnlyCount*2))
	query := testRecommendNote("notes/query.md", "query", "Query", "distributed consensus lease quorum", "systems")
	notes = append(notes, query)

	graph := &model.LinkGraph{
		Forward:  map[string][]string{"notes/query.md": {}},
		Backward: map[string][]string{"notes/query.md": {}},
	}

	for index := 0; index < bm25MatchCount; index++ {
		relPath := fmt.Sprintf("notes/match-%04d.md", index)
		notes = append(notes, testRecommendNote(
			relPath,
			fmt.Sprintf("match-%04d", index),
			fmt.Sprintf("Match %04d", index),
			fmt.Sprintf("distributed consensus lease quorum shard%d", index),
			"systems",
		))
	}

	for index := 0; index < boostOnlyCount; index++ {
		relPath := fmt.Sprintf("notes/tag-only-%04d.md", index)
		notes = append(notes, testRecommendNote(
			relPath,
			fmt.Sprintf("tag-only-%04d", index),
			fmt.Sprintf("Tag Only %04d", index),
			fmt.Sprintf("sourdough starter hydration crumb%d", index),
			"systems",
		))
	}

	for index := 0; index < boostOnlyCount; index++ {
		relPath := fmt.Sprintf("notes/link-only-%04d.md", index)
		notes = append(notes, testRecommendNote(
			relPath,
			fmt.Sprintf("link-only-%04d", index),
			fmt.Sprintf("Link Only %04d", index),
			fmt.Sprintf("vector database embedding latency%d", index),
		))
		graph.Forward["notes/query.md"] = append(graph.Forward["notes/query.md"], relPath)
		graph.Forward[relPath] = []string{"notes/query.md"}
		graph.Backward["notes/query.md"] = append(graph.Backward["notes/query.md"], relPath)
		graph.Backward[relPath] = []string{"notes/query.md"}
	}

	return testRecommendIndex(notes...), graph
}

func buildBenchmarkRecommendFixture(count int) (*model.VaultIndex, *model.LinkGraph) {
	notes := buildBenchmarkRecommendNotes(count)
	idx := testRecommendIndex(notes...)
	graph := &model.LinkGraph{
		Forward:  make(map[string][]string, len(notes)),
		Backward: make(map[string][]string, len(notes)),
	}

	pathsByTopic := make(map[string][]string)
	for _, note := range notes {
		if note == nil || len(note.Tags) == 0 {
			continue
		}
		pathsByTopic[note.Tags[0]] = append(pathsByTopic[note.Tags[0]], note.RelPath)
	}

	for _, paths := range pathsByTopic {
		for index := 0; index+1 < len(paths); index += 2 {
			left := paths[index]
			right := paths[index+1]
			graph.Forward[left] = append(graph.Forward[left], right)
			graph.Forward[right] = append(graph.Forward[right], left)
			graph.Backward[left] = append(graph.Backward[left], right)
			graph.Backward[right] = append(graph.Backward[right], left)
		}
	}

	return idx, graph
}

func testRecommendIndex(notes ...*model.Note) *model.VaultIndex {
	idx := &model.VaultIndex{Notes: make(map[string]*model.Note, len(notes))}
	for _, note := range notes {
		if note == nil {
			continue
		}
		idx.Notes[note.RelPath] = note
	}
	return idx
}

func testRecommendNote(relPath string, slug string, title string, content string, tags ...string) *model.Note {
	return &model.Note{
		RelPath:    relPath,
		Slug:       slug,
		RawContent: []byte(content),
		Tags:       append([]string(nil), tags...),
		Frontmatter: model.Frontmatter{
			Title: title,
		},
	}
}

func scoreBM25ForTest(bm25 *BM25, query map[string]int, candidateID string) float64 {
	if bm25 == nil {
		return 0
	}

	return bm25.scoreQueryTerms(sortedQueryTerms(query), candidateID)
}

func scoreRelatedPairForTest(recommender *Recommender, queryNote *model.Note, candidateNote *model.Note) float64 {
	if recommender == nil || queryNote == nil || candidateNote == nil {
		return 0
	}

	queryProfile, ok := recommender.docMap[queryNote.RelPath]
	if !ok {
		return 0
	}
	candidateProfile, ok := recommender.docMap[candidateNote.RelPath]
	if !ok {
		return 0
	}

	return recommender.scoreProfiles(queryProfile, candidateProfile)
}

func containsToken(tokens []string, want string) bool {
	for _, token := range tokens {
		if token == want {
			return true
		}
	}
	return false
}

func relatedByPathRelPaths(rankedByPath map[string][]RankedNote) map[string][]string {
	if len(rankedByPath) == 0 {
		return map[string][]string{}
	}

	paths := make(map[string][]string, len(rankedByPath))
	for relPath, ranked := range rankedByPath {
		paths[relPath] = relatedRelPaths(ranked)
	}

	return paths
}

func containsCJKToken(tokens []string) bool {
	for _, token := range tokens {
		for _, r := range token {
			if !isCJKRune(r) {
				continue
			}
			return true
		}
	}
	return false
}

func relatedRelPaths(items []RankedNote) []string {
	paths := make([]string, 0, len(items))
	for _, item := range items {
		if item.Note == nil {
			continue
		}
		paths = append(paths, item.Note.RelPath)
	}
	return paths
}
