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
  edinet codes -o json | jq '.[] | select(.table == "alias")'`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f, err := output.ParseFormat(format)
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()

			if f != output.FormatTable {
				return output.Render(w, f, codeSpec(), codeEntries())
			}

			fmt.Fprintln(w, "Document types (--type)")
			if err := output.Render(w, f, pairSpec("CODE", "NAME"), codes.SortedDocTypes()); err != nil {
				return err
			}

			fmt.Fprintln(w, "\nAliases")
			if err := output.Render(w, f, pairSpec("ALIAS", "CODE"), codes.Aliases()); err != nil {
				return err
			}

			fmt.Fprintln(w, "\nOrdinances (ordinanceCode)")
			return output.Render(w, f, pairSpec("CODE", "NAME"), codes.SortedOrdinances())
		},
	}

	addFormatFlag(cmd, &format)
	return cmd
}

// CodeEntry is one row of the reference tables. The three tables share a shape
// so that -o json and -o csv describe the same records; the table format still
// prints them as three labelled sections.
type CodeEntry struct {
	Table string `json:"table"`
	Key   string `json:"key"`
	Value string `json:"value"`
}

func codeEntries() []CodeEntry {
	var out []CodeEntry
	for _, p := range codes.SortedDocTypes() {
		out = append(out, CodeEntry{"docType", p[0], p[1]})
	}
	for _, p := range codes.Aliases() {
		out = append(out, CodeEntry{"alias", p[0], p[1]})
	}
	for _, p := range codes.SortedOrdinances() {
		out = append(out, CodeEntry{"ordinance", p[0], p[1]})
	}
	return out
}

func codeSpec() output.TableSpec[CodeEntry] {
	return output.TableSpec[CodeEntry]{
		Headers: []string{"TABLE", "KEY", "VALUE"},
		Row:     func(e CodeEntry) []string { return []string{e.Table, e.Key, e.Value} },
	}
}

// pairSpec renders a two-column reference table. Only the table format uses it,
// so no json tags are needed on [2]string.
func pairSpec(h1, h2 string) output.TableSpec[[2]string] {
	return output.TableSpec[[2]string]{
		Headers: []string{h1, h2},
		Row:     func(p [2]string) []string { return []string{p[0], p[1]} },
	}
}
