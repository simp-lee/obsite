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
	internalserver "github.com/simp-lee/obsite/internal/server"
	"github.com/spf13/cobra"
)

const defaultConfigFilename = "obsite.yaml"

type previewServer interface {
	ListenAndServe() error
	EnableLiveReload()
	NotifyReload()
}

type commandDependencies struct {
	loadSiteInput        func(path string, overrides internalconfig.Overrides) (internalbuild.SiteInput, error)
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
	cmd := &cobra.Command{
		Use:           "obsite",
		Short:         "Generate and preview static sites from Obsidian vaults",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(
		newBuildCommand(deps),
		newServeCommand(deps),
		newInitCommand(),
	)

	return cmd
}

func requiredPathFlag(name string, value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("--%s is required", name)
	}

	return filepath.Clean(trimmed), nil
}
