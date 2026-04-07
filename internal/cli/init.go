package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

const initConfigTemplate = `# Obsite site configuration.
# Update these defaults before publishing.

# baseURL must be the public site URL used for canonical links and sitemap entries.
baseURL: https://example.com/

# title appears in page titles and templates.
title: My Obsite Site

# author is used in templates and structured data.
author: Your Name

# description is the site-wide SEO fallback.
description: Notes published with obsite.

# language controls the default html lang value.
language: en

# defaultPublish controls notes that omit frontmatter publish.
defaultPublish: true
`

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
