package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

const initConfigTemplate = "" +
	"# Obsite site configuration.\n" +
	"# Update these defaults before publishing.\n" +
	"\n" +
	"# baseURL must be the public site URL used for canonical links and sitemap entries.\n" +
	"baseURL: https://example.com/\n" +
	"\n" +
	"# title appears in page titles and templates.\n" +
	"title: My Obsite Site\n" +
	"\n" +
	"# author is used in templates and structured data.\n" +
	"author: Your Name\n" +
	"\n" +
	"# description is the site-wide SEO fallback.\n" +
	"description: Notes published with obsite.\n" +
	"\n" +
	"# language controls the default html lang value.\n" +
	"language: en\n" +
	"\n" +
	"# defaultPublish controls notes that omit frontmatter publish.\n" +
	"defaultPublish: true\n" +
	"\n" +
	"# search controls optional Pagefind indexing. Keep it disabled until tools/pagefind_extended is installed in this project.\n" +
	"search:\n" +
	"  enabled: false\n" +
	"  # pagefindPath points to the pagefind_extended executable used during build, relative to this obsite.yaml file.\n" +
	"  pagefindPath: tools/pagefind_extended\n" +
	"  # pagefindVersion must match the installed pagefind_extended version when search is enabled.\n" +
	"  pagefindVersion: 1.5.2\n" +
	"\n" +
	"# pagination controls list pages such as the homepage, tags, and folders.\n" +
	"pagination:\n" +
	"  pageSize: 20\n" +
	"\n" +
	"# sidebar enables the collapsible file tree navigation.\n" +
	"sidebar:\n" +
	"  enabled: false\n" +
	"\n" +
	"# popover enables async internal-link previews.\n" +
	"popover:\n" +
	"  enabled: false\n" +
	"\n" +
	"# related enables related-article blocks on note pages.\n" +
	"related:\n" +
	"  enabled: false\n" +
	"  count: 5\n" +
	"\n" +
	"# rss controls generation of the site feed.\n" +
	"rss:\n" +
	"  enabled: true\n" +
	"\n" +
	"# timeline controls the recent-notes page and optional homepage override.\n" +
	"timeline:\n" +
	"  enabled: false\n" +
	"  asHomepage: false\n" +
	"  path: notes\n" +
	"\n" +
	"# themes optionally declares named build-time themes. themes.<name>.root is resolved relative to this obsite.yaml file unless absolute.\n" +
	"# themes:\n" +
	"#   feature:\n" +
	"#     root: themes/feature\n" +
	"# defaultTheme selects one of the configured theme names when --theme is omitted.\n" +
	"# defaultTheme: feature\n" +
	"\n" +
	"# Obsite only auto-detects a global override stylesheet at <vault>/custom.css, loaded after the generated site stylesheet.\n" +
	"# Each selected theme root must provide every required HTML template.\n"

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
