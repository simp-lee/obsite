package build

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/simp-lee/obsite/internal/diag"
)

func recordCanvasDiagnostics(resourceFiles []string, collector *diag.Collector) {
	if collector == nil {
		return
	}

	for _, relPath := range resourceFiles {
		if !strings.EqualFold(filepath.Ext(relPath), ".canvas") {
			continue
		}
		collector.Warningf(diag.KindUnsupportedSyntax, diag.Location{Path: relPath}, "canvas files are skipped during site builds")
	}
}

func writeDiagnosticsSummary(writer io.Writer, collector *diag.Collector, buildErr error) error {
	if writer == nil {
		return nil
	}

	warnings := []diag.Diagnostic(nil)
	fatalDiagnostics := []diag.Diagnostic(nil)
	if collector != nil {
		warnings = collector.Warnings()
		fatalDiagnostics = collector.Errors()
	}

	var synthetic diagnosticBuildError
	includeBuildErr := buildErr != nil && !errors.As(buildErr, &synthetic)
	if len(warnings) == 0 && len(fatalDiagnostics) == 0 && !includeBuildErr {
		return nil
	}

	if len(warnings) > 0 {
		if _, err := fmt.Fprintf(writer, "Warnings (%d):\n", len(warnings)); err != nil {
			return err
		}
		for _, diagnostic := range warnings {
			if _, err := fmt.Fprintf(writer, "- %s\n", formatDiagnosticLine(diagnostic)); err != nil {
				return err
			}
		}
	}

	fatalCount := len(fatalDiagnostics)
	if includeBuildErr {
		fatalCount++
	}
	if fatalCount > 0 {
		if _, err := fmt.Fprintf(writer, "Fatal build errors (%d):\n", fatalCount); err != nil {
			return err
		}
		for _, diagnostic := range fatalDiagnostics {
			if _, err := fmt.Fprintf(writer, "- %s\n", formatDiagnosticLine(diagnostic)); err != nil {
				return err
			}
		}
		if includeBuildErr {
			if _, err := fmt.Fprintf(writer, "- build: %v\n", buildErr); err != nil {
				return err
			}
		}
	}

	return nil
}

func formatDiagnosticLine(diagnostic diag.Diagnostic) string {
	location := diagnostic.Location.Path
	if diagnostic.Location.Line > 0 {
		location = fmt.Sprintf("%s:%d", location, diagnostic.Location.Line)
	}
	if strings.TrimSpace(location) == "" {
		location = "build"
	}
	return fmt.Sprintf("%s [%s] %s", location, diagnostic.Kind, diagnostic.Message)
}
