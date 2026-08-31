package cli

import (
	"fmt"

	internalbuild "github.com/simp-lee/obsite/internal/build"
	"github.com/spf13/cobra"
)

func newBuildCommand(deps commandDependencies) *cobra.Command {
	var vaultPath string
	var outputPath string
	var force bool

	cmd := &cobra.Command{
		Use:   "build",
		Short: "Build a static site from an Obsidian vault",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			boundary, err := resolveVaultOutputPaths(vaultPath, outputPath)
			if err != nil {
				return err
			}

			input, err := deps.loadSiteInput(boundary.VaultPath)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			if _, err := deps.buildSiteWithOptions(input, boundary.VaultPath, boundary.OutputPath, internalbuild.Options{Force: force, DiagnosticsWriter: cmd.ErrOrStderr()}); err != nil {
				return fmt.Errorf("build site: %w", err)
			}

			return nil
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&vaultPath, "vault", "", "Path to the Obsidian vault")
	flags.StringVar(&outputPath, "output", "", "Path to write the generated site")
	flags.BoolVar(&force, "force", false, "Ignore the incremental cache and rebuild all note pages")

	return cmd
}
