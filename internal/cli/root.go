// Package cli wires the edinet command tree.
package cli

import (
	"errors"
	"os"

	"github.com/jackchuka/edinet-cli/internal/version"
	"github.com/spf13/cobra"
)

// globals holds flags shared by every command that talks to the EDINET API.
type globals struct {
	apiKey      string
	concurrency int
}

func (g *globals) key() (string, error) {
	if g.apiKey != "" {
		return g.apiKey, nil
	}
	if k := os.Getenv("EDINET_API_KEY"); k != "" {
		return k, nil
	}
	return "", errors.New("no API key: set EDINET_API_KEY or pass --api-key (get one at https://api.edinet-fsa.go.jp/api/auth/index.aspx)")
}

// NewRootCommand builds the edinet command tree.
func NewRootCommand() *cobra.Command {
	g := &globals{}

	cmd := &cobra.Command{
		Use:   "edinet",
		Short: "Search, download, and extract Japanese corporate filings from EDINET",
		Long: `edinet is a command-line client for the FSA's EDINET API v2.

Search filings by company name or ticker, download them in any of the formats
EDINET offers, and pull financial facts out of the XBRL-derived CSV bundles.

Requires a free API key from the FSA:
  https://api.edinet-fsa.go.jp/api/auth/index.aspx

Set it as EDINET_API_KEY, or pass --api-key.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version.Version,
	}

	// Keep `--version` and the `version` subcommand reporting the same string.
	cmd.SetVersionTemplate(versionString() + "\n")

	cmd.PersistentFlags().StringVar(&g.apiKey, "api-key", "", "EDINET API key (default $EDINET_API_KEY)")
	cmd.PersistentFlags().IntVar(&g.concurrency, "concurrency", 3, "max parallel requests when fetching a date range")

	cmd.AddCommand(
		newDocsCommand(g),
		newFactsCommand(g),
		newCompanyCommand(),
		newCodesCommand(),
		newVersionCommand(),
	)

	return cmd
}
