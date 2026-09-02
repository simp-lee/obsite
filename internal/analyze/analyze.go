// Package analyze provides the read-only validation handoff shared by the
// validate command and strict builds.
package analyze

import (
	"fmt"
	"io"

	diagnostic "github.com/simp-lee/obsite/internal/diag"
	"github.com/simp-lee/obsite/internal/siteplan"
)

// Result is the complete read-only analysis result. No output path is opened or
// modified by this package.
type Result struct {
	Plan        *siteplan.Result
	Diagnostics []diagnostic.Diagnostic
}

// Analyze runs the strict configuration, source, section, route, collection,
// and version checks before publication.
func Analyze(vaultPath string) (Result, error) {
	planned, err := siteplan.Build(vaultPath)
	if planned == nil {
		collector := diagnostic.NewCollector()
		if err != nil {
			collector.Errorf(diagnostic.KindSchema, diagnostic.Location{Path: vaultPath}, "%v", err)
		}
		return Result{Diagnostics: collector.Diagnostics()}, err
	}
	return Result{Plan: planned, Diagnostics: append([]diagnostic.Diagnostic(nil), planned.Diagnostics...)}, err
}

// HasFailures reports whether either severity makes the selected operation
// invalid. validate and strict build both use this policy.
func HasFailures(diagnostics []diagnostic.Diagnostic) bool { return len(diagnostics) != 0 }

// WriteDiagnostics renders the stable human-readable diagnostic format.
func WriteDiagnostics(writer io.Writer, diagnostics []diagnostic.Diagnostic) error {
	if writer == nil {
		return nil
	}
	for _, item := range diagnostics {
		line := item.Location.Line
		location := item.Location.Path
		if line > 0 {
			location = fmt.Sprintf("%s:%d", location, line)
		}
		if item.Field != "" {
			location += " [field=" + item.Field + "]"
		}
		if item.Target != "" {
			location += " [target=" + item.Target + "]"
		}
		if _, err := fmt.Fprintf(writer, "%s %s %s: %s\n", item.Severity, item.Kind, location, item.Message); err != nil {
			return err
		}
	}
	return nil
}

// Failure turns a non-empty diagnostic set into an operation error.
func Failure(diagnostics []diagnostic.Diagnostic) error {
	if len(diagnostics) == 0 {
		return nil
	}
	var errorsCount, warningsCount int
	for _, item := range diagnostics {
		switch item.Severity {
		case diagnostic.SeverityError:
			errorsCount++
		case diagnostic.SeverityWarning:
			warningsCount++
		}
	}
	return fmt.Errorf("validation failed with %d error(s) and %d warning(s)", errorsCount, warningsCount)
}
