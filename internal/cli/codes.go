package cli

import (
	"fmt"

	"github.com/jackchuka/edinet-cli/internal/codes"
	"github.com/jackchuka/edinet-cli/internal/output"
	"github.com/spf13/cobra"
)

func newCodesCommand() *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:   "codes",
		Short: "Print the EDINET document type and ordinance code tables",
		Long: `Print the code tables used by --type.

Document types can be given to --type either by code (120) or by the alias
listed here (yuho).`,
		Example: `  edinet codes
  edinet codes -o json | jq '.docTypes'`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f, err := output.ParseFormat(format)
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()

			if f != output.FormatTable {
				return output.Render(w, f, codeTable(), map[string]any{
					"docTypes":   codes.DocTypes,
					"ordinances": codes.Ordinances,
					"aliases":    aliasMap(),
				})
			}

			fmt.Fprintln(w, "Document types (--type)")
			if err := output.Render(w, f, pairTable("CODE", "NAME", codes.SortedDocTypes()), nil); err != nil {
				return err
			}

			fmt.Fprintln(w, "\nAliases")
			if err := output.Render(w, f, pairTable("ALIAS", "CODE", codes.Aliases()), nil); err != nil {
				return err
			}

			fmt.Fprintln(w, "\nOrdinances (ordinanceCode)")
			return output.Render(w, f, pairTable("CODE", "NAME", codes.SortedOrdinances()), nil)
		},
	}

	addFormatFlag(cmd, &format)
	return cmd
}

func pairTable(h1, h2 string, pairs [][2]string) output.Table {
	t := output.Table{Headers: []string{h1, h2}}
	for _, p := range pairs {
		t.Rows = append(t.Rows, []string{p[0], p[1]})
	}
	return t
}

// codeTable is the flat view used for CSV output, where the three tables have
// to share one shape.
func codeTable() output.Table {
	t := output.Table{Headers: []string{"TABLE", "KEY", "VALUE"}}
	for _, p := range codes.SortedDocTypes() {
		t.Rows = append(t.Rows, []string{"docType", p[0], p[1]})
	}
	for _, p := range codes.Aliases() {
		t.Rows = append(t.Rows, []string{"alias", p[0], p[1]})
	}
	for _, p := range codes.SortedOrdinances() {
		t.Rows = append(t.Rows, []string{"ordinance", p[0], p[1]})
	}
	return t
}

func aliasMap() map[string]string {
	m := map[string]string{}
	for _, p := range codes.Aliases() {
		m[p[0]] = p[1]
	}
	return m
}
