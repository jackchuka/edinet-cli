package cli

import (
	"fmt"
	"os"

	"github.com/jackchuka/edinet-cli/internal/edinet"
	"github.com/jackchuka/edinet-cli/internal/facts"
	"github.com/jackchuka/edinet-cli/internal/output"
	"github.com/spf13/cobra"
)

func newFactsCommand(g *globals) *cobra.Command {
	var (
		filter    facts.Filter
		localFile string
		limit     int
		format    string
	)

	cmd := &cobra.Command{
		Use:   "facts [docID]",
		Short: "Extract reported values from a filing's CSV bundle",
		Long: `Extract facts from the CSV bundle EDINET generates from a filing's XBRL.

The bundle is a ZIP of UTF-16LE tab-separated files, which this command decodes
for you. Pass a document ID to download it, or --file to read a bundle already
on disk.

Only filings whose 'csv' flag is set have a bundle; check with:
  edinet docs list --has csv`,
		Example: `  # Revenue lines from an annual report
  edinet facts S100AAAA --grep 売上高

  # Consolidated figures for the current period, as CSV
  edinet facts S100AAAA --consolidated --period 当期 --numeric -o csv

  # A specific XBRL element
  edinet facts S100AAAA --element NetSales

  # Re-read a bundle already downloaded
  edinet facts --file ./S100AAAA_csv.zip -o json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f, err := output.ParseFormat(format)
			if err != nil {
				return err
			}
			if len(args) == 0 && localFile == "" {
				return fmt.Errorf("give a document ID, or --file to read a downloaded bundle")
			}
			if len(args) > 0 && localFile != "" {
				return fmt.Errorf("give either a document ID or --file, not both")
			}

			var data []byte
			if localFile != "" {
				if data, err = os.ReadFile(localFile); err != nil {
					return err
				}
			} else {
				client, err := g.client()
				if err != nil {
					return err
				}
				file, err := client.Fetch(cmd.Context(), args[0], edinet.FileCSV)
				if err != nil {
					return err
				}
				data = file.Data
			}

			all, err := facts.ParseZip(data)
			if err != nil {
				return err
			}

			matched := filter.Apply(all)
			if limit > 0 && len(matched) > limit {
				matched = matched[:limit]
			}
			if len(matched) == 0 && f == output.FormatTable {
				fmt.Fprintf(os.Stderr, "No facts matched (the bundle holds %d).\n", len(all))
				return nil
			}

			return output.Render(cmd.OutOrStdout(), f, factsTable(matched), matched)
		},
	}

	fl := cmd.Flags()
	fl.StringVar(&localFile, "file", "", "read a downloaded CSV bundle instead of fetching one")
	fl.StringVar(&filter.Grep, "grep", "", "keep facts whose label or value contains this text")
	fl.StringVar(&filter.Element, "element", "", "keep facts whose XBRL element ID contains this text")
	fl.BoolVar(&filter.Consolidated, "consolidated", false, "keep 連結 (consolidated) facts only")
	fl.BoolVar(&filter.Standalone, "standalone", false, "keep 個別 (non-consolidated) facts only")
	fl.StringVar(&filter.Period, "period", "", "keep facts from this relative period, e.g. 当期")
	fl.BoolVar(&filter.NumericOnly, "numeric", false, "drop narrative text blocks")
	fl.IntVar(&limit, "limit", 0, "stop after this many facts (0 = no limit)")
	addFormatFlag(cmd, &format)

	return cmd
}

func factsTable(fs []facts.Fact) output.Table {
	t := output.Table{
		Headers: []string{"ELEMENT", "LABEL", "PERIOD", "SCOPE", "VALUE", "UNIT"},
		Max:     []int{44, 28, 0, 0, 40, 0},
	}
	for _, f := range fs {
		t.Rows = append(t.Rows, []string{
			f.ElementID,
			f.Label,
			f.RelativeYear,
			f.Consolidated,
			f.Value,
			f.Unit,
		})
	}
	return t
}
