package diag

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func TestCollectorClassifiesAndSortsDiagnostics(t *testing.T) {
	var collector Collector

	collector.Warningf(
		KindUnsupportedSyntax,
		Location{Path: "notes/zeta.md", Line: 9},
		"kept unsupported block as plain text",
	)
	collector.Errorf(
		KindDeadLink,
		Location{Path: "notes/alpha.md", Line: 12},
		"unresolved wikilink %q",
		"Missing Page",
	)
	collector.Warningf(
		KindUnresolvedAsset,
		Location{Path: "notes/alpha.md", Line: 3},
		"missing asset %q",
		"img.png",
	)

	if got := collector.Len(); got != 3 {
		t.Fatalf("Len() = %d, want 3", got)
	}
	if got := collector.WarningCount(); got != 2 {
		t.Fatalf("WarningCount() = %d, want 2", got)
	}
	if got := collector.ErrorCount(); got != 1 {
		t.Fatalf("ErrorCount() = %d, want 1", got)
	}
	if !collector.HasErrors() {
		t.Fatal("HasErrors() = false, want true")
	}

	got := collector.Diagnostics()
	want := []Diagnostic{
		{
			Severity: SeverityWarning,
			Kind:     KindUnresolvedAsset,
			Location: Location{Path: "notes/alpha.md", Line: 3},
			Message:  "missing asset \"img.png\"",
		},
		{
			Severity: SeverityError,
			Kind:     KindDeadLink,
			Location: Location{Path: "notes/alpha.md", Line: 12},
			Message:  "unresolved wikilink \"Missing Page\"",
		},
		{
			Severity: SeverityWarning,
			Kind:     KindUnsupportedSyntax,
			Location: Location{Path: "notes/zeta.md", Line: 9},
			Message:  "kept unsupported block as plain text",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Diagnostics() = %#v, want %#v", got, want)
	}

	got[0].Message = "mutated"
	again := collector.Diagnostics()
	if reflect.DeepEqual(got, again) {
		t.Fatal("Diagnostics() returned a slice backed by collector state")
	}

	if warnings := collector.Warnings(); len(warnings) != 2 {
		t.Fatalf("len(Warnings()) = %d, want 2", len(warnings))
	}
	if errors := collector.Errors(); len(errors) != 1 {
		t.Fatalf("len(Errors()) = %d, want 1", len(errors))
	}
}

func TestCollectorMergeIsConcurrentSafe(t *testing.T) {
	parent := NewCollector()

	const (
		collectors   = 24
		perCollector = 40
	)

	var wg sync.WaitGroup
	for i := range collectors {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			child := NewCollector()
			for j := 1; j <= perCollector; j++ {
				location := Location{
					Path: fmt.Sprintf("notes/%02d.md", i),
					Line: j,
				}
				if j%2 == 0 {
					child.Errorf(KindDeadLink, location, "broken link %d", j)
					continue
				}
				child.Warningf(KindUnsupportedSyntax, location, "fallback %d", j)
			}

			parent.Merge(child)
		}(i)
	}
	wg.Wait()

	if got := parent.Len(); got != collectors*perCollector {
		t.Fatalf("Len() = %d, want %d", got, collectors*perCollector)
	}
	if got := parent.WarningCount(); got != collectors*(perCollector/2) {
		t.Fatalf("WarningCount() = %d, want %d", got, collectors*(perCollector/2))
	}
	if got := parent.ErrorCount(); got != collectors*(perCollector/2) {
		t.Fatalf("ErrorCount() = %d, want %d", got, collectors*(perCollector/2))
	}

	diagnostics := parent.Diagnostics()
	if len(diagnostics) == 0 {
		t.Fatal("Diagnostics() returned no diagnostics")
	}
	if diagnostics[0].Location.Path != "notes/00.md" || diagnostics[0].Location.Line != 1 {
		t.Fatalf("first diagnostic = %#v, want notes/00.md:1", diagnostics[0])
	}
	if diagnostics[len(diagnostics)-1].Location.Path != "notes/23.md" || diagnostics[len(diagnostics)-1].Location.Line != perCollector {
		t.Fatalf("last diagnostic = %#v, want notes/23.md:%d", diagnostics[len(diagnostics)-1], perCollector)
	}
	if !parent.HasWarnings() {
		t.Fatal("HasWarnings() = false, want true")
	}
}
