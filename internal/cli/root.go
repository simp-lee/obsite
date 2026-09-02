// Package cli provides the Obsite command-line entrypoint.
package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	internalbuild "github.com/simp-lee/obsite/internal/build"
	internalconfig "github.com/simp-lee/obsite/internal/config"
	internalfsutil "github.com/simp-lee/obsite/internal/fsutil"
	internalserver "github.com/simp-lee/obsite/internal/server"
	"github.com/spf13/cobra"
)

const defaultConfigFilename = internalconfig.Filename

type previewServer interface {
	ListenAndServe() error
	EnableLiveReload()
	NotifyReload()
}

type commandDependencies struct {
	loadSiteInput        func(resolvedVault string) (internalbuild.SiteInput, error)
	buildSiteWithOptions func(input internalbuild.SiteInput, vaultPath string, outputPath string, options internalbuild.Options) (*internalbuild.BuildResult, error)
	newPreviewServer     func(outputPath string, port int) (previewServer, error)
	newFileWatcher       func() (fileWatcher, error)
}

// Execute is the single CLI entrypoint used by main.
func Execute(args []string) error {
	return executeWithDeps(args, defaultCommandDependencies(), os.Stdout, os.Stderr)
}

func executeWithDeps(args []string, deps commandDependencies, stdout io.Writer, stderr io.Writer) error {
	root := newRootCommand(deps)
	if stdout != nil {
		root.SetOut(stdout)
	}
	if stderr != nil {
		root.SetErr(stderr)
	}
	root.SetArgs(args)

	return root.Execute()
}

func defaultCommandDependencies() commandDependencies {
	return commandDependencies{
		loadSiteInput:        internalbuild.LoadSiteInput,
		buildSiteWithOptions: internalbuild.BuildWithOptions,
		newPreviewServer: func(outputPath string, port int) (previewServer, error) {
			return internalserver.New(outputPath, port)
		},
		newFileWatcher: newFSNotifyWatcher,
	}
}

func newRootCommand(deps commandDependencies) *cobra.Command {
	var showVersion bool
	cmd := &cobra.Command{
		Use:           "obsite",
		Short:         "Generate and preview static sites from Obsidian vaults",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if showVersion {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), formatVersion())
				return err
			}
			return cmd.Help()
		},
	}
	cmd.Flags().BoolVar(&showVersion, "version", false, "Print the Obsite version")

	cmd.AddCommand(
		newBuildCommand(deps),
		newServeCommand(deps),
		newInitCommand(),
		newValidateCommand(),
		newVersionCommand(),
	)

	return cmd
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the Obsite version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), formatVersion())
			return err
		},
	}
}

func resolveVaultPath(value string) (string, error) {
	vaultPath := strings.TrimSpace(value)
	if vaultPath == "" {
		vaultPath = "."
	}
	return internalfsutil.ResolveVaultPath(filepath.Clean(vaultPath))
}

func resolveVaultOutputPaths(vaultValue string, outputValue string) (internalfsutil.VaultOutputBoundary, error) {
	vaultPath, err := resolveVaultPath(vaultValue)
	if err != nil {
		return internalfsutil.VaultOutputBoundary{}, err
	}
	outputPath := strings.TrimSpace(outputValue)
	if outputPath == "" {
		outputPath = filepath.Join(vaultPath, "public")
	}
	return internalfsutil.ResolveVaultOutput(vaultPath, outputPath)
}
