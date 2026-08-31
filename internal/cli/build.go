package cli

import (
	"fmt"
	"strings"

	internalbuild "github.com/simp-lee/obsite/internal/build"
	internalfsutil "github.com/simp-lee/obsite/internal/fsutil"
	"github.com/spf13/cobra"
)

func newBuildCommand(deps commandDependencies) *cobra.Command {
	var vaultPath string
	var outputPath string
	var configPath string
	var theme string
	var force bool

	cmd := &cobra.Command{
		Use:   "build",
		Short: "Build a static site from an Obsidian vault",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(configPath) != "" {
				return fmt.Errorf("--config has been removed; Obsite reads <vault>/%s", defaultConfigFilename)
			}
			if strings.TrimSpace(theme) != "" {
				return fmt.Errorf("--theme has been removed")
			}
			trimmedVaultPath, err := requiredPathFlag("vault", vaultPath)
			if err != nil {
				return err
			}
			trimmedOutputPath, err := requiredPathFlag("output", outputPath)
			if err != nil {
				return err
			}
			boundary, err := internalfsutil.ResolveVaultOutput(trimmedVaultPath, trimmedOutputPath)
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
	flags.StringVar(&configPath, "config", "", "Path to obsite.yaml (defaults to <vault>/obsite.yaml)")
	flags.StringVar(&theme, "theme", "", "Named theme to select for this build")
	flags.BoolVar(&force, "force", false, "Ignore the incremental cache and rebuild all note pages")
	_ = cmd.MarkFlagRequired("vault")
	_ = cmd.MarkFlagRequired("output")

	return cmd
}
