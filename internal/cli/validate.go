package cli

import (
	"fmt"

	internalanalyze "github.com/simp-lee/obsite/internal/analyze"
	"github.com/spf13/cobra"
)

func newValidateCommand() *cobra.Command {
	var vaultPath string
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate a vault without writing site output",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resolvedVault, err := resolveVaultPath(vaultPath)
			if err != nil {
				return err
			}
			result, analyzeErr := internalanalyze.Analyze(resolvedVault)
			if writeErr := internalanalyze.WriteDiagnostics(cmd.ErrOrStderr(), result.Diagnostics); writeErr != nil {
				return fmt.Errorf("write diagnostics: %w", writeErr)
			}
			if analyzeErr != nil {
				return analyzeErr
			}
			return internalanalyze.Failure(result.Diagnostics)
		},
	}
	cmd.Flags().StringVar(&vaultPath, "vault", "", "Path to the Obsidian vault")
	return cmd
}
