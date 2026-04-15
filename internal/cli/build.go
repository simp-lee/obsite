package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	internalbuild "github.com/simp-lee/obsite/internal/build"
	internalconfig "github.com/simp-lee/obsite/internal/config"
	"github.com/spf13/cobra"
)

func newBuildCommand(deps commandDependencies) *cobra.Command {
	var vaultPath string
	var outputPath string
	var configPath string
	var force bool

	cmd := &cobra.Command{
		Use:   "build",
		Short: "Build a static site from an Obsidian vault",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			trimmedVaultPath, err := requiredPathFlag("vault", vaultPath)
			if err != nil {
				return err
			}
			trimmedOutputPath, err := requiredPathFlag("output", outputPath)
			if err != nil {
				return err
			}
			normalizedVaultPath, err := internalbuild.NormalizeVaultPath(trimmedVaultPath)
			if err != nil {
				return err
			}

			resolvedConfigPath, err := resolveBuildConfigPath(normalizedVaultPath, configPath)
			if err != nil {
				return err
			}

			input, err := deps.loadSiteInput(resolvedConfigPath, internalconfig.Overrides{VaultPath: normalizedVaultPath})
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			if _, err := deps.buildSiteWithOptions(input, normalizedVaultPath, trimmedOutputPath, internalbuild.Options{Force: force, DiagnosticsWriter: cmd.ErrOrStderr()}); err != nil {
				return fmt.Errorf("build site: %w", err)
			}

			return nil
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&vaultPath, "vault", "", "Path to the Obsidian vault")
	flags.StringVar(&outputPath, "output", "", "Path to write the generated site")
	flags.StringVar(&configPath, "config", "", "Path to obsite.yaml (defaults to <vault>/obsite.yaml)")
	flags.BoolVar(&force, "force", false, "Ignore the incremental cache and rebuild all note pages")
	_ = cmd.MarkFlagRequired("vault")
	_ = cmd.MarkFlagRequired("output")

	return cmd
}

func resolveBuildConfigPath(vaultPath string, configPath string) (string, error) {
	trimmedConfigPath := strings.TrimSpace(configPath)
	if trimmedConfigPath != "" {
		return filepath.Clean(trimmedConfigPath), nil
	}

	defaultConfigPath := filepath.Join(vaultPath, defaultConfigFilename)
	if _, err := os.Stat(defaultConfigPath); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("default config file %q does not exist; add %s to the vault or pass --config", defaultConfigPath, defaultConfigFilename)
		}

		return "", fmt.Errorf("stat default config file %q: %w", defaultConfigPath, err)
	}

	return defaultConfigPath, nil
}
