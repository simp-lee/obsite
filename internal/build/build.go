package build

import (
	"fmt"
	"io"

	internalanalyze "github.com/simp-lee/obsite/internal/analyze"
	"github.com/simp-lee/obsite/internal/diag"
	"github.com/simp-lee/obsite/internal/model"
)

// BuildResult is the public build summary returned to the CLI.
type BuildResult struct {
	OutputPath   string
	Index        *model.VaultIndex
	Graph        *model.LinkGraph
	Assets       map[string]*model.Asset
	Diagnostics  []diag.Diagnostic
	NotePages    int
	TagPages     int
	WarningCount int
	ErrorCount   int
}

// Options controls the strict section-based build entry point.
type Options struct {
	Strict            bool
	DiagnosticsWriter io.Writer
	// Concurrency bounds independent Markdown indexing and recommendation
	// workers. A non-positive value uses the production default.
	Concurrency int
}

// BuildWithOptions analyzes the vault once and publishes the resulting
// canonical section plan. Normal builds continue after warnings; strict builds
// reject both warnings and errors before opening a staging publisher.
func BuildWithOptions(vaultPath, outputPath string, options Options) (*BuildResult, error) {
	analysis, analyzeErr := internalanalyze.AnalyzeWithOutputAndConcurrency(vaultPath, outputPath, options.Concurrency)
	if writeErr := internalanalyze.WriteDiagnostics(options.DiagnosticsWriter, analysis.Diagnostics); writeErr != nil {
		return nil, fmt.Errorf("write diagnostics: %w", writeErr)
	}
	result := buildResultFromAnalysis(analysis.Diagnostics)
	if analyzeErr != nil || result.ErrorCount > 0 || options.Strict && result.WarningCount > 0 {
		if analyzeErr != nil {
			return result, analyzeErr
		}
		return result, internalanalyze.Failure(analysis.Diagnostics)
	}
	strictResult, buildErr := buildStrictSite(analysis.Plan, vaultPath, outputPath, options.DiagnosticsWriter, options.Strict, options.Concurrency)
	if strictResult == nil {
		strictResult = &BuildResult{}
	}
	strictResult.Diagnostics = append(result.Diagnostics, strictResult.Diagnostics...)
	strictResult.ErrorCount += result.ErrorCount
	strictResult.WarningCount += result.WarningCount
	return strictResult, buildErr
}

func buildResultFromAnalysis(diagnostics []diag.Diagnostic) *BuildResult {
	result := &BuildResult{Diagnostics: append([]diag.Diagnostic(nil), diagnostics...)}
	for _, diagnostic := range diagnostics {
		switch diagnostic.Severity {
		case diag.SeverityError:
			result.ErrorCount++
		case diag.SeverityWarning:
			result.WarningCount++
		}
	}
	return result
}
