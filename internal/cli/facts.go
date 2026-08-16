package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jackchuka/edinet-cli/internal/edinet"
	"github.com/jackchuka/edinet-cli/internal/facts"
	"github.com/jackchuka/edinet-cli/internal/output"
	"github.com/spf13/cobra"
)

func newFactsCommand(g *globals) *cobra.Command {
	var (
		filter facts.Filter
		files  []string
		limit  int
		format string
	)

	cmd := &cobra.Command{
		Use:   "facts [docID...]",
		Short: "Extract reported values from filings' CSV bundles",
		Long: `Extract facts from the CSV bundles EDINET generates from filings' XBRL.

The bundle is a ZIP of UTF-16LE tab-separated files, which this command decodes
for you. Pass document IDs to download them, - to read IDs from stdin, or --file
to read bundles already on disk.

When --file is given, positional arguments are read as further file paths
rather than document IDs, so 'edinet facts --file ./dl/*.zip' works with the
shell doing the glob expansion.

Every row in the json and csv output carries the filing it came from, so results
can be joined against other data sources without a second lookup. The table
format keeps its narrower human-readable columns and buffers every row to align
them, so prefer -o csv or -o json for large batches.

Only filings whose 'csv' flag is set have a bundle; check with:
  edinet docs list --has csv`,
		Example: `  # Revenue lines from an annual report
  edinet facts S100AAAA --grep 売上高

  # Every annual report filed in a month, as one CSV
  edinet docs list --from 2026-06-01 --to 2026-06-30 --type yuho --has csv -o json \
    | jq -r '.[].docID' \
    | edinet facts - --element NetSales --consolidated -o csv

  # Bundles already downloaded
  edinet facts --file ./downloads/*_csv.zip -o json`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			f, err := output.ParseFormat(format)
			if err != nil {
				return err
			}
			sources, err := factsSources(args, files, cmd.InOrStdin())
			if err != nil {
				return err
			}
			return runFacts(cmd, g, sources, filter, f, limit)
		},
	}

	fl := cmd.Flags()
	fl.StringArrayVar(&files, "file", nil, "read a downloaded CSV bundle instead of fetching one (repeatable)")
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

// factsSource is one bundle to read. Path is empty when the bundle has to be
// downloaded; DocID is empty when a local file was renamed away from the name
// `docs get` writes.
type factsSource struct {
	DocID string
	Path  string
}

// factsSources turns the command line into an ordered list of bundles.
//
// When --file is given, positional arguments are read as further paths rather
// than document IDs. That is what makes `--file ./dl/*.zip` work: the shell
// expands the glob into arguments, and requiring one --file per path would
// break the most natural way to write it.
func factsSources(args, files []string, stdin io.Reader) ([]factsSource, error) {
	if len(files) > 0 {
		if slices.Contains(args, "-") {
			return nil, fmt.Errorf("--file reads local bundles; it cannot be combined with - (stdin)")
		}
		paths := append(append([]string{}, files...), args...)
		sources := make([]factsSource, 0, len(paths))
		for _, p := range paths {
			sources = append(sources, factsSource{DocID: docIDFromPath(p), Path: p})
		}
		return sources, nil
	}

	if slices.Contains(args, "-") {
		if len(args) > 1 {
			return nil, fmt.Errorf("- reads document IDs from stdin and must be given on its own")
		}
		ids, err := readDocIDs(stdin)
		if err != nil {
			return nil, err
		}
		// A stateless filter maps empty input to empty output rather than an
		// error: empty stdin is the normal shape of "nothing matched" from an
		// upstream `docs list`, e.g. over a weekend with no filings.
		sources := make([]factsSource, 0, len(ids))
		for _, id := range ids {
			sources = append(sources, factsSource{DocID: id})
		}
		return sources, nil
	}

	if len(args) == 0 {
		return nil, fmt.Errorf("give a document ID, - to read them from stdin, or --file to read a downloaded bundle")
	}

	sources := make([]factsSource, 0, len(args))
	for _, id := range args {
		sources = append(sources, factsSource{DocID: id})
	}
	return sources, nil
}

// readDocIDs reads newline-separated document IDs, skipping blank lines and
// comments so a saved list stays annotatable.
func readDocIDs(r io.Reader) ([]string, error) {
	var ids []string
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		ids = append(ids, line)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading document IDs from stdin: %w", err)
	}
	return ids, nil
}

// docIDFromPath recovers the document ID from a bundle saved by `docs get`,
// which names its files <docID>_csv.zip. A renamed bundle yields an empty ID
// rather than a guessed one, because a wrong ID on every row is worse than a
// blank column.
func docIDFromPath(p string) string {
	id, ok := strings.CutSuffix(filepath.Base(p), "_csv.zip")
	if !ok {
		return ""
	}
	return id
}

// loadFacts reads one bundle and labels every fact with the filing it came
// from. client is unused (and may be nil) when src.Path is set; the caller
// resolves it once up front rather than per source, see needsClient.
func loadFacts(ctx context.Context, client *edinet.Client, src factsSource, filter facts.Filter) ([]facts.Row, error) {
	var data []byte
	if src.Path != "" {
		var err error
		if data, err = os.ReadFile(src.Path); err != nil {
			return nil, err
		}
	} else {
		file, err := client.Fetch(ctx, src.DocID, edinet.FileCSV)
		if err != nil {
			return nil, err
		}
		data = file.Data
	}

	all, err := facts.ParseZip(data)
	if err != nil {
		return nil, err
	}

	filing := facts.DEI(all)
	filing.DocID = src.DocID

	matched := filter.Apply(all)
	rows := make([]facts.Row, 0, len(matched))
	for _, f := range matched {
		rows = append(rows, facts.Row{Filing: filing, Fact: f})
	}
	return rows, nil
}

// fetchOrdered runs load over sources with at most concurrency in flight and
// calls emit for each result in input order.
//
// The result channels are deliberately unbuffered. A worker that has finished
// blocks until emit consumes its rows, and while it blocks it holds its
// concurrency slot, so the rows held in memory are capped at roughly
// `concurrency` filings rather than the whole batch. A slow leading document
// stalls the workers behind it; that is the price of a bounded footprint, and
// EDINET's own rate limiting makes a deeper queue pointless anyway.
//
// Indices are handed out in ascending order, so the lowest index not yet
// emitted is always held by some worker and the ordered receive cannot deadlock.
func fetchOrdered(
	ctx context.Context,
	sources []factsSource,
	concurrency int,
	load func(context.Context, factsSource) ([]facts.Row, error),
	emit func([]facts.Row) error,
	onError func(factsSource, error),
) (int, error) {
	if concurrency < 1 {
		concurrency = 1
	}

	// Derived so that an early return (emit failing, for instance) always
	// cancels the workers before this function gives up its stack frame. A
	// caller that never cancels its own ctx would otherwise leave workers
	// parked forever on results[i] <-.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type result struct {
		rows []facts.Row
		err  error
	}

	results := make([]chan result, len(sources))
	for i := range results {
		results[i] = make(chan result)
	}

	queue := make(chan int)
	go func() {
		defer close(queue)
		for i := range sources {
			select {
			case queue <- i:
			case <-ctx.Done():
				return
			}
		}
	}()

	for range concurrency {
		go func() {
			for i := range queue {
				rows, err := load(ctx, sources[i])
				select {
				case results[i] <- result{rows: rows, err: err}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	failed := 0
	for i := range sources {
		select {
		case r := <-results[i]:
			// Once ctx is cancelled, discard whatever this receive produced --
			// success or failure alike -- and hand the cancellation to the
			// caller, because the ordered loop cannot continue past a
			// cancelled context anyway.
			if ctx.Err() != nil {
				return failed, ctx.Err()
			}
			if r.err != nil {
				onError(sources[i], r.err)
				failed++
				continue
			}
			if err := emit(r.rows); err != nil {
				return failed, err
			}
		case <-ctx.Done():
			return failed, ctx.Err()
		}
	}
	return failed, nil
}

func runFacts(cmd *cobra.Command, g *globals, sources []factsSource, filter facts.Filter, f output.Format, limit int) error {
	// Resolved once up front, and only when a source actually needs it, so a
	// --file-only run works with no API key at all and a keyless multi-source
	// run reports the missing key once rather than once per filing.
	var client *edinet.Client
	if needsClient(sources) {
		var err error
		if client, err = g.client(); err != nil {
			return err
		}
	}

	// Cancelling on --limit stops workers that are still downloading filings
	// whose rows would be discarded anyway.
	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	stream := output.NewStream(cmd.OutOrStdout(), f, factsSpec())
	written := 0
	// limitReached distinguishes emit's own cancel (expected: --limit was
	// hit, so remaining downloads are stopped on purpose) from every other
	// reason ctx might die (an interrupt, or the parent context ending),
	// which must still fail the run. The two cannot be told apart from the
	// returned error alone, since both surface as context.Canceled. emit runs
	// synchronously on this goroutine -- fetchOrdered's ordered-receive loop
	// calls it directly -- so this plain bool needs no synchronization.
	limitReached := false

	emit := func(rows []facts.Row) error {
		for _, r := range rows {
			if limit > 0 && written >= limit {
				limitReached = true
				cancel()
				return nil
			}
			if err := stream.Write(r); err != nil {
				return err
			}
			written++
		}
		return nil
	}

	onError := func(src factsSource, err error) {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v (skipped)\n", sourceLabel(src), err)
	}

	load := func(ctx context.Context, src factsSource) ([]facts.Row, error) {
		return loadFacts(ctx, client, src, filter)
	}

	failed, ferr := fetchOrdered(ctx, sources, g.concurrency, load, emit, onError)

	// A limit-triggered cancel is expected, and fetchOrdered's own guard
	// already keeps it from inflating failed; anything else must still fail
	// the run rather than silently return a truncated result as if it were
	// complete, but only after flushing what already streamed.
	if ferr != nil && !limitReached {
		cerr := stream.Close()
		return errors.Join(ferr, cerr)
	}
	if err := stream.Close(); err != nil {
		return err
	}

	if written == 0 && failed == 0 && f == output.FormatTable {
		fmt.Fprintln(cmd.ErrOrStderr(), "No facts matched.")
	}
	// Successful rows have already been written in full; the exit code is what
	// keeps a script from mistaking a partial result for a complete one. The
	// count omits a denominator because --limit can stop the run before every
	// source is attempted, making len(sources) an overstatement.
	if failed > 0 {
		return fmt.Errorf("%d filings failed", failed)
	}
	return nil
}

// needsClient reports whether any source has to be downloaded, so a
// --file-only run never demands an API key it does not use.
func needsClient(sources []factsSource) bool {
	for _, s := range sources {
		if s.Path == "" {
			return true
		}
	}
	return false
}

// sourceLabel names a source the way the user gave it.
func sourceLabel(src factsSource) string {
	if src.Path != "" {
		return src.Path
	}
	return src.DocID
}

func factsSpec() output.TableSpec[facts.Row] {
	return output.TableSpec[facts.Row]{
		Headers: []string{"ELEMENT", "LABEL", "PERIOD", "SCOPE", "VALUE", "UNIT"},
		Max:     []int{44, 28, 0, 0, 40, 0},
		Row: func(r facts.Row) []string {
			return []string{r.ElementID, r.Label, r.RelativeYear, r.Consolidated, r.Value, r.Unit}
		},
	}
}
