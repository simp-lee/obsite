package cli

import (
	"fmt"
	"os"
	"path/filepath"

	internalconfig "github.com/simp-lee/obsite/internal/config"
	"github.com/spf13/cobra"
)

var initConfigTemplate = internalconfig.InitialYAML()

func newInitCommand() *cobra.Command {
	var vaultPath string

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create an obsite.yaml template in a vault directory",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			trimmedVaultPath, err := requiredPathFlag("vault", vaultPath)
			if err != nil {
				return err
			}

			if err := os.MkdirAll(trimmedVaultPath, 0o755); err != nil {
				return fmt.Errorf("create vault directory %q: %w", trimmedVaultPath, err)
			}

			configPath := filepath.Join(trimmedVaultPath, defaultConfigFilename)
			if err := writeInitConfig(configPath); err != nil {
				return err
			}

			return nil
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&vaultPath, "vault", "", "Path to the Obsidian vault")
	_ = cmd.MarkFlagRequired("vault")

	return cmd
}

func writeInitConfig(configPath string) error {
	file, err := os.OpenFile(configPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("config file %q already exists", configPath)
		}

		return fmt.Errorf("create config file %q: %w", configPath, err)
	}

	if _, err := file.WriteString(initConfigTemplate); err != nil {
		_ = file.Close()
		_ = os.Remove(configPath)
		return fmt.Errorf("write config file %q: %w", configPath, err)
	}

	if err := file.Close(); err != nil {
		_ = os.Remove(configPath)
		return fmt.Errorf("close config file %q: %w", configPath, err)
	}

	return nil
}
