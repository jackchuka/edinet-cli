package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackchuka/edinet-cli/internal/codelist"
	"github.com/jackchuka/edinet-cli/internal/codes"
	"github.com/jackchuka/edinet-cli/internal/edinet"
	"github.com/jackchuka/edinet-cli/internal/output"
	"github.com/spf13/cobra"
)

func newDocsCommand(g *globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "docs",
		Short: "Search and download filings",
	}
	cmd.AddCommand(newDocsListCommand(g), newDocsGetCommand(g))
	return cmd
}

type listOptions struct {
	date, from, to    string
	company           string
	edinetCode        string
	secCode           string
	docTypes          []string
	has               []string
	includeWithdrawn  bool
	includeUnviewable bool
	limit             int
	format            string
}

func newDocsListCommand(g *globals) *cobra.Command {
	opts := &listOptions{}

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List filings submitted on a date or over a date range",
		Long: `List filings from EDINET.

EDINET's list endpoint accepts a single date, so a range is fetched one day at a
time; --concurrency controls how many of those requests run in parallel. All
filtering happens locally, because the API offers none.`,
		Example: `  # What was filed today
  edinet docs list

  # Annual reports filed in a quarter
  edinet docs list --from 2026-04-01 --to 2026-06-30 --type yuho

  # Everything Toyota filed last month, as JSON
  edinet docs list --from 2026-07-01 --to 2026-07-31 --company トヨタ -o json

  # Filings that ship a CSV bundle, by ticker
  edinet docs list --date 2026-08-14 --sec-code 7203 --has csv`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDocsList(cmd.Context(), g, opts, cmd.OutOrStdout())
		},
	}

	f := cmd.Flags()
	f.StringVar(&opts.date, "date", "", "single filing date (YYYY-MM-DD, default today)")
	f.StringVar(&opts.from, "from", "", "start of a filing date range (YYYY-MM-DD)")
	f.StringVar(&opts.to, "to", "", "end of a filing date range (YYYY-MM-DD, default today)")
	f.StringVar(&opts.company, "company", "", "company name, ticker, EDINET code, or corporate number")
	f.StringVar(&opts.edinetCode, "edinet-code", "", "exact EDINET code, e.g. E02144")
	f.StringVar(&opts.secCode, "sec-code", "", "securities code / ticker, e.g. 7203")
	f.StringSliceVar(&opts.docTypes, "type", nil, "document type code or alias, e.g. yuho or 120 (repeatable)")
	f.StringSliceVar(&opts.has, "has", nil, fmt.Sprintf("only filings offering these files: %s", strings.Join(edinet.FileTypeNames(), ", ")))
	f.BoolVar(&opts.includeWithdrawn, "include-withdrawn", false, "include withdrawn filings and withdrawal notices")
	f.BoolVar(&opts.includeUnviewable, "include-expired", false, "include filings whose viewing period has expired")
	f.IntVar(&opts.limit, "limit", 0, "stop after this many results (0 = no limit)")
	addFormatFlag(cmd, &opts.format)

	return cmd
}

func runDocsList(ctx context.Context, g *globals, opts *listOptions, w io.Writer) error {
	format, err := output.ParseFormat(opts.format)
	if err != nil {
		return err
	}

	from, to, err := dateRange(opts.date, opts.from, opts.to, time.Now())
	if err != nil {
		return err
	}

	wantTypes, err := resolveDocTypes(opts.docTypes)
	if err != nil {
		return err
	}
	wantFiles, err := resolveFileTypes(opts.has)
	if err != nil {
		return err
	}

	// Resolving a company name needs the code list, which is fetched before any
	// API call so a typo does not cost a range of requests.
	var companyCodes map[string]bool
	if opts.company != "" {
		entries, err := loadCodeList(ctx)
		if err != nil {
			return err
		}
		matches := codelist.Search(entries, opts.company)
		if len(matches) == 0 {
			return fmt.Errorf("no company matches %q (try `edinet company search %s`)", opts.company, opts.company)
		}
		companyCodes = make(map[string]bool, len(matches))
		for _, e := range matches {
			companyCodes[e.EdinetCode] = true
		}
	}

	client, err := g.client()
	if err != nil {
		return err
	}

	docs, err := client.ListRange(ctx, from, to, g.concurrency)
	if err != nil {
		return err
	}

	filtered := docs[:0:0]
	for _, d := range docs {
		if !opts.includeWithdrawn && d.Withdrawn() {
			continue
		}
		if !opts.includeUnviewable && !d.Viewable() {
			continue
		}
		if opts.edinetCode != "" && !strings.EqualFold(d.EdinetCode, opts.edinetCode) {
			continue
		}
		if opts.secCode != "" && !secCodeMatches(d.SecCode, opts.secCode) {
			continue
		}
		if companyCodes != nil && !companyCodes[d.EdinetCode] &&
			!strings.Contains(d.FilerName, opts.company) {
			continue
		}
		if len(wantTypes) > 0 && !wantTypes[d.DocTypeCode] {
			continue
		}
		if !hasAll(d, wantFiles) {
			continue
		}
		filtered = append(filtered, d)
		if opts.limit > 0 && len(filtered) >= opts.limit {
			break
		}
	}

	if len(filtered) == 0 && format == output.FormatTable {
		fmt.Fprintln(os.Stderr, "No filings matched.")
		return nil
	}

	return output.Render(w, format, docsSpec(), filtered)
}

func docsSpec() output.TableSpec[edinet.Document] {
	return output.TableSpec[edinet.Document]{
		Headers: []string{"DOCID", "SUBMITTED", "EDINET", "TICKER", "FILER", "TYPE", "DESCRIPTION", "FILES"},
		Max:     []int{0, 0, 0, 0, 24, 0, 40, 0},
		Row: func(d edinet.Document) []string {
			return []string{
				d.DocID,
				d.SubmitDateTime,
				d.EdinetCode,
				ticker(d.SecCode),
				d.FilerName,
				codes.DocTypeName(d.DocTypeCode),
				d.DocDescription,
				availableFiles(d),
			}
		},
	}
}

func availableFiles(d edinet.Document) string {
	var got []string
	for _, name := range edinet.FileTypeNames() {
		ft, err := edinet.ParseFileType(name)
		if err != nil {
			continue
		}
		if d.Has(ft) {
			got = append(got, name)
		}
	}
	return strings.Join(got, ",")
}

// ticker trims EDINET's 5-digit securities code to the 4 digits people use.
func ticker(secCode string) string {
	if len(secCode) == 5 && strings.HasSuffix(secCode, "0") {
		return secCode[:4]
	}
	return secCode
}

func secCodeMatches(docSecCode, want string) bool {
	if docSecCode == "" {
		return false
	}
	return docSecCode == want || ticker(docSecCode) == want
}

func resolveDocTypes(in []string) (map[string]bool, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make(map[string]bool, len(in))
	for _, s := range in {
		code, err := codes.ResolveDocType(s)
		if err != nil {
			return nil, err
		}
		out[code] = true
	}
	return out, nil
}

func resolveFileTypes(in []string) ([]edinet.FileType, error) {
	var out []edinet.FileType
	for _, s := range in {
		ft, err := edinet.ParseFileType(s)
		if err != nil {
			return nil, err
		}
		out = append(out, ft)
	}
	return out, nil
}

func hasAll(d edinet.Document, want []edinet.FileType) bool {
	for _, ft := range want {
		if !d.Has(ft) {
			return false
		}
	}
	return true
}

func newDocsGetCommand(g *globals) *cobra.Command {
	var (
		fileType string
		outDir   string
		extract  bool
		toStdout bool
		force    bool
	)

	cmd := &cobra.Command{
		Use:   "get <docID>",
		Short: "Download a filing",
		Long: `Download one rendition of a filing.

  main     submitted document, audit report, and XBRL (zip)
  pdf      the PDF shown on the EDINET viewer
  attach   substitute and attached documents (zip)
  english  English-language filings (zip)
  csv      XBRL rendered to CSV (zip) — see also 'edinet facts'`,
		Example: `  edinet docs get S100AAAA
  edinet docs get S100AAAA --file pdf
  edinet docs get S100AAAA --file csv --out ./downloads --extract
  edinet docs get S100AAAA --file pdf --stdout > report.pdf`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ft, err := edinet.ParseFileType(fileType)
			if err != nil {
				return err
			}
			client, err := g.client()
			if err != nil {
				return err
			}

			file, err := client.Fetch(cmd.Context(), args[0], ft)
			if err != nil {
				return err
			}

			if toStdout {
				_, err := cmd.OutOrStdout().Write(file.Data)
				return err
			}
			if extract {
				return extractZip(file, outDir, force)
			}
			return saveFile(file, outDir, force)
		},
	}

	f := cmd.Flags()
	f.StringVarP(&fileType, "file", "f", "main", fmt.Sprintf("which files to download: %s", joinOr(edinet.FileTypeNames())))
	f.StringVar(&outDir, "out", ".", "directory to write to")
	f.BoolVar(&extract, "extract", false, "unpack the archive instead of saving it")
	f.BoolVar(&toStdout, "stdout", false, "write the file to stdout")
	f.BoolVar(&force, "force", false, "overwrite existing files")

	return cmd
}

func saveFile(file *edinet.File, dir string, force bool) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	dest := filepath.Join(dir, file.Name())
	if !force {
		if _, err := os.Stat(dest); err == nil {
			return fmt.Errorf("%s already exists (pass --force to overwrite)", dest)
		}
	}
	if err := os.WriteFile(dest, file.Data, 0o644); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "Saved", dest)
	return nil
}
