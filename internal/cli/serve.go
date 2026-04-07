package cli

import (
	"fmt"

	internalserver "github.com/simp-lee/obsite/internal/server"
	"github.com/spf13/cobra"
)

func newServeCommand(deps commandDependencies) *cobra.Command {
	var outputPath string
	var port int

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve a generated site for local preview",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			trimmedOutputPath, err := requiredPathFlag("output", outputPath)
			if err != nil {
				return err
			}

			srv, err := deps.newPreviewServer(trimmedOutputPath, port)
			if err != nil {
				return fmt.Errorf("create preview server: %w", err)
			}

			if err := srv.ListenAndServe(); err != nil {
				return fmt.Errorf("listen and serve: %w", err)
			}

			return nil
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&outputPath, "output", "", "Path to the generated site output")
	flags.IntVar(&port, "port", 0, fmt.Sprintf("Port for the local preview server (default %d)", internalserver.DefaultPort))
	_ = cmd.MarkFlagRequired("output")

	return cmd
}
