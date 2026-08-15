package cli

import (
	"fmt"

	"github.com/jackchuka/edinet-cli/internal/version"
	"github.com/spf13/cobra"
)

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "version",
		Short:   "Print version information",
		Args:    cobra.NoArgs,
		Example: `  edinet version`,
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintln(cmd.OutOrStdout(), versionString())
		},
	}
}

func versionString() string {
	return fmt.Sprintf("edinet %s (commit: %s, built: %s)",
		version.Version, version.Commit, version.BuildDate)
}
