package recommend

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"reflect"
	"testing"

	"github.com/simp-lee/obsite/internal/model"
)

const trimpathTokenizerChildEnv = "OBSITE_TRIMPATH_TOKENIZER_CHILD"

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

	want := math.Float64bits(bm25.Score(queryTerms, "candidate"))
	for iteration := 0; iteration < 256; iteration++ {
		got := math.Float64bits(bm25.Score(queryTerms, "candidate"))
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
		betaScore := recommender.Score(alpha, beta)
		gammaScore := recommender.Score(alpha, gamma)
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
		betaScore := recommender.Score(alpha, beta)
		aaronScore := recommender.Score(alpha, aaron)
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

func containsToken(tokens []string, want string) bool {
	for _, token := range tokens {
		if token == want {
			return true
		}
	}
	return false
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
