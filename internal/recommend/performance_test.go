package recommend

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"testing"

	"github.com/simp-lee/obsite/internal/model"
	"github.com/simp-lee/obsite/internal/testutil/relatedfixture"
)

const relatedRSSHelperEnv = "OBSITE_RELATED_RSS_HELPER"

func BenchmarkRelatedBuildWarm(b *testing.B) {
	cases := []struct {
		name  string
		kind  string
		count int
	}{
		{name: "mixed-500", kind: relatedfixture.CaseMixed, count: 500},
		{name: "mixed-1000", kind: relatedfixture.CaseMixed, count: 1000},
		{name: "mixed-5000", kind: relatedfixture.CaseMixed, count: 5000},
		{name: "sparse-posting-5000", kind: relatedfixture.CaseSparsePosting, count: 5000},
		{name: "term-49%-coverage-5000", kind: relatedfixture.CaseTermCoverage49, count: 5000},
		{name: "tag-49%-coverage-5000", kind: relatedfixture.CaseTagCoverage49, count: 5000},
		{name: "rejected-content-5000", kind: relatedfixture.CaseRejectedContent, count: 5000},
	}
	for _, current := range cases {
		b.Run(current.name, func(b *testing.B) {
			fixture, err := relatedfixture.Generate(current.kind, current.count)
			if err != nil {
				b.Fatal(err)
			}
			if _, err := Tokenize("分布式数据库一致性协议"); err != nil {
				b.Fatal(err)
			}
			parameters := ProductionEngineParameters(5, 0)
			b.ReportAllocs()
			b.ResetTimer()
			for iteration := 0; iteration < b.N; iteration++ {
				result, err := BuildEngine(fixture.Semantics, fixture.Index, fixture.Graph, parameters)
				if err != nil {
					b.Fatal(err)
				}
				runtime.KeepAlive(result)
			}
		})
	}
}

func TestPerformanceAdversarialBehavior(t *testing.T) {
	rejected, err := relatedfixture.Generate(relatedfixture.CaseRejectedContent, 100)
	if err != nil {
		t.Fatal(err)
	}
	features, err := BuildFeatureIndex(rejected.Semantics, ProductionEngineParameters(5, 1).Features)
	if err != nil {
		t.Fatal(err)
	}
	postingEntries := 0
	for _, posting := range features.Postings {
		postingEntries += len(posting)
	}
	if postingEntries == 0 {
		t.Fatal("rejected-content fixture generated no eligible posting candidates")
	}
	result, err := BuildEngine(rejected.Semantics, rejected.Index, rejected.Graph, ProductionEngineParameters(5, 1))
	if err != nil {
		t.Fatal(err)
	}
	for _, document := range result.Documents {
		if len(document.Related) != 0 {
			t.Fatalf("rejected-content document %s retained %#v", document.RelPath, document.Related)
		}
	}

	termCoverage, err := relatedfixture.Generate(relatedfixture.CaseTermCoverage49, 100)
	if err != nil {
		t.Fatal(err)
	}
	termFeatures, err := BuildFeatureIndex(termCoverage.Semantics, ProductionEngineParameters(5, 1).Features)
	if err != nil {
		t.Fatal(err)
	}
	for _, term := range termFeatures.Terms {
		if strings.HasPrefix(term, "coverage-term-") {
			t.Fatalf("49%% coverage term %q bypassed frozen 40%% cutoff", term)
		}
	}
}

func TestRSSHelperIsolation(t *testing.T) {
	if specification := os.Getenv(relatedRSSHelperEnv); specification != "" {
		kind, count, err := parseRelatedFixtureSpecification(specification)
		if err != nil {
			t.Fatal(err)
		}
		fixture, err := relatedfixture.Generate(kind, count)
		if err != nil {
			t.Fatal(err)
		}
		result, err := BuildEngine(fixture.Semantics, fixture.Index, fixture.Graph, ProductionEngineParameters(5, 0))
		if err != nil {
			t.Fatal(err)
		}
		runtime.KeepAlive(result)
		peak, err := readVmHWM()
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("RELATED_RSS %s %d\n", kind, peak)
		return
	}

	command := exec.Command(os.Args[0], "-test.run=^TestRSSHelperIsolation$", "-test.count=1")
	command.Env = append(os.Environ(), relatedRSSHelperEnv+"="+relatedfixture.CaseMixed+":20")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("RSS helper error = %v\n%s", err, output)
	}
	if strings.Count(string(output), "RELATED_RSS ") != 1 {
		t.Fatalf("RSS helper output = %q, want exactly one case", output)
	}
}

func TestMemoryProfileCheckpoints(t *testing.T) {
	previousProfileRate := runtime.MemProfileRate
	runtime.MemProfileRate = 1
	defer func() { runtime.MemProfileRate = previousProfileRate }()

	profileDir := os.Getenv("OBSITE_RELATED_PROFILE_DIR")
	if profileDir == "" {
		profileDir = t.TempDir()
	}
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		t.Fatal(err)
	}

	fixture, err := relatedfixture.Generate(relatedfixture.CaseMixed, 20)
	if err != nil {
		t.Fatal(err)
	}
	semantics := fixture.Semantics
	features, err := BuildFeatureIndex(semantics, ProductionEngineParameters(5, 1).Features)
	if err != nil {
		t.Fatal(err)
	}
	semantics = nil
	fixture.Semantics = nil
	runtime.GC()
	writeRelatedHeapProfile(t, profileDir, "post-tokenization.pprof")

	tags := BuildTagSignalIndex(features.Documents, fixture.Index)
	profiledDuringScoring := false
	profiledScorer := func(index *FeatureIndex, tagIndex *TagSignalIndex, graph *model.LinkGraph, sourceDocID int, candidateDocID int, parameters ContentParameters) (PairScore, error) {
		if !profiledDuringScoring {
			profiledDuringScoring = true
			runtime.GC()
			writeRelatedHeapProfile(t, profileDir, "during-scoring.pprof")
		}
		return ScorePair(index, tagIndex, graph, sourceDocID, candidateDocID, parameters)
	}
	ranking, err := rankFeatureIndex(features, tags, fixture.Index, fixture.Graph, ProductionEngineParameters(5, 1), profiledScorer)
	if err != nil {
		t.Fatal(err)
	}
	if !profiledDuringScoring {
		t.Fatal("scoring profile callback was not reached")
	}

	features = nil
	tags = nil
	runtime.GC()
	writeRelatedHeapProfile(t, profileDir, "page-output.pprof")
	runtime.KeepAlive(ranking)
	for _, name := range []string{"post-tokenization.pprof", "during-scoring.pprof", "page-output.pprof"} {
		info, err := os.Stat(filepath.Join(profileDir, name))
		if err != nil || info.Size() == 0 {
			t.Fatalf("profile %s missing/empty: %v", name, err)
		}
	}
}

func parseRelatedFixtureSpecification(value string) (string, int, error) {
	kind, countText, ok := strings.Cut(value, ":")
	if !ok {
		return "", 0, fmt.Errorf("invalid fixture specification %q", value)
	}
	count, err := strconv.Atoi(countText)
	if err != nil || count < 0 {
		return "", 0, fmt.Errorf("invalid fixture count %q", countText)
	}
	return kind, count, nil
}

func readVmHWM() (int64, error) {
	file, err := os.Open("/proc/self/status")
	if err != nil {
		return 0, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 && fields[0] == "VmHWM:" {
			return strconv.ParseInt(fields[1], 10, 64)
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	return 0, fmt.Errorf("VmHWM not found")
}

func writeRelatedHeapProfile(t *testing.T, directory string, name string) {
	t.Helper()
	file, err := os.Create(filepath.Join(directory, name))
	if err != nil {
		t.Fatal(err)
	}
	if err := pprof.WriteHeapProfile(file); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
