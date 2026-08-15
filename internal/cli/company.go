package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/jackchuka/edinet-cli/internal/codelist"
	"github.com/jackchuka/edinet-cli/internal/output"
	"github.com/spf13/cobra"
)

// The code list comes from the public disclosure site rather than the API, so
// these commands work without an API key.
func newCompanyCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "company",
		Short: "Look up EDINET codes for companies",
		Long: `Resolve companies to EDINET codes.

The EDINET API has no company search: every filing is keyed by EDINET code. The
mapping comes from a code list published separately by the FSA, which is cached
locally and refreshed weekly.`,
	}
	cmd.AddCommand(newCompanySearchCommand(), newCompanySyncCommand())
	return cmd
}

func newCompanySearchCommand() *cobra.Command {
	var (
		format string
		limit  int
	)

	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Find a company by name, ticker, EDINET code, or corporate number",
		Example: `  edinet company search トヨタ
  edinet company search 7203
  edinet company search "sony group"
  edinet company search E02144 -o json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f, err := output.ParseFormat(format)
			if err != nil {
				return err
			}

			entries, err := loadCodeList(cmd.Context())
			if err != nil {
				return err
			}

			matches := codelist.Search(entries, args[0])
			if limit > 0 && len(matches) > limit {
				matches = matches[:limit]
			}
			if len(matches) == 0 && f == output.FormatTable {
				fmt.Fprintf(os.Stderr, "No company matches %q.\n", args[0])
				return nil
			}

			return output.Render(cmd.OutOrStdout(), f, companyTable(matches), matches)
		},
	}

	cmd.Flags().IntVar(&limit, "limit", 20, "stop after this many matches (0 = no limit)")
	addFormatFlag(cmd, &format)
	return cmd
}

func companyTable(entries []codelist.Entry) output.Table {
	t := output.Table{
		Headers: []string{"EDINET", "TICKER", "NAME", "INDUSTRY", "LISTED", "FYE"},
		Max:     []int{0, 0, 36, 16, 0, 0},
	}
	for _, e := range entries {
		t.Rows = append(t.Rows, []string{
			e.EdinetCode,
			e.Ticker(),
			e.Name,
			e.Industry,
			e.Listed,
			e.FiscalYearEnd,
		})
	}
	return t
}

func newCompanySyncCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "sync",
		Short:   "Refresh the cached EDINET code list",
		Args:    cobra.NoArgs,
		Example: `  edinet company sync`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := codelist.NewStore()
			if err != nil {
				return err
			}
			if age, ok := store.Age(); ok {
				fmt.Fprintf(os.Stderr, "Cache is %s old, refreshing...\n", age.Round(time.Minute))
			}
			entries, err := store.Sync(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Cached %d companies to %s\n", len(entries), store.Path)
			return nil
		},
	}
}
