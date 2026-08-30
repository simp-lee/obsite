package relatedfixture

import (
	"reflect"
	"strings"
	"testing"
)

func TestPerformanceFixtureManifest(t *testing.T) {
	got, err := LoadManifest("../../recommend/testdata/performance/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	want := FrozenManifest()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("performance manifest = %#v, want frozen %#v", got, want)
	}
	for _, sample := range got.Samples {
		fixture, err := Generate(sample.Name, sample.Count)
		if err != nil {
			t.Fatal(err)
		}
		if digest := CorpusHash(fixture); digest != sample.SHA256 {
			t.Fatalf("sample %s-%d hash = %s, want %s", sample.Name, sample.Count, digest, sample.SHA256)
		}
	}

	mixed, err := Generate(CaseMixed, 20)
	if err != nil {
		t.Fatal(err)
	}
	languages := make(map[string]int)
	topics := make(map[string]int)
	for docID, document := range mixed.Semantics {
		if got := len(strings.Fields(document.Body)); got < 200 {
			t.Fatalf("mixed document %d tokens = %d, want at least 200", docID, got)
		}
		note := mixed.Index.Notes[document.RelPath]
		languages[note.Tags[1]]++
		topics[note.Tags[0]]++
	}
	if len(languages) != 4 || len(topics) < 5 {
		t.Fatalf("mixed balance languages/topics = %#v/%#v, want 4/5", languages, topics)
	}
}

func TestPerformanceAdversarialCases(t *testing.T) {
	fixtures := make(map[string]Fixture)
	for _, name := range []string{CaseSparsePosting, CaseTermCoverage49, CaseTagCoverage49, CaseRejectedContent} {
		fixture, err := Generate(name, 100)
		if err != nil {
			t.Fatalf("Generate(%s) error = %v", name, err)
		}
		if len(fixture.Semantics) != 100 || len(fixture.Index.Notes) != 100 {
			t.Fatalf("Generate(%s) sizes = %d/%d, want 100", name, len(fixture.Semantics), len(fixture.Index.Notes))
		}
		fixtures[name] = fixture
	}
	if got := len(strings.Fields(fixtures[CaseSparsePosting].Semantics[0].Body)); got != 2000 {
		t.Fatalf("sparse fixture tokens = %d, want 2000", got)
	}
	termMembers := 0
	for _, document := range fixtures[CaseTermCoverage49].Semantics {
		if strings.Contains(document.Body, "coverage-term-0") {
			termMembers++
		}
	}
	if termMembers != 49 {
		t.Fatalf("term coverage members = %d, want 49", termMembers)
	}
	tagMembers := 0
	for _, note := range fixtures[CaseTagCoverage49].Index.Notes {
		if len(note.Tags) == 6 {
			tagMembers++
		}
	}
	if tagMembers != 49 {
		t.Fatalf("tag coverage members = %d, want 49", tagMembers)
	}
}
