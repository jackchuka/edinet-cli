package cli

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackchuka/edinet-cli/internal/codelist"
	"github.com/jackchuka/edinet-cli/internal/edinet"
	"github.com/jackchuka/edinet-cli/internal/facts"
	"github.com/jackchuka/edinet-cli/internal/output"
	"github.com/spf13/cobra"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

func TestDateRange(t *testing.T) {
	now := time.Date(2026, 8, 15, 10, 30, 0, 0, time.UTC)

	tests := []struct {
		name             string
		date, from, to   string
		wantFrom, wantTo string
		wantErr          string
	}{
		{name: "no flags means today", wantFrom: "2026-08-15", wantTo: "2026-08-15"},
		{name: "single date", date: "2026-04-01", wantFrom: "2026-04-01", wantTo: "2026-04-01"},
		{name: "explicit range", from: "2026-04-01", to: "2026-06-30", wantFrom: "2026-04-01", wantTo: "2026-06-30"},
		{name: "from alone runs to today", from: "2026-08-10", wantFrom: "2026-08-10", wantTo: "2026-08-15"},
		{name: "to alone is rejected", to: "2026-08-10", wantErr: "--from"},
		{name: "date with range is rejected", date: "2026-08-01", from: "2026-08-02", wantErr: "cannot be combined"},
		{name: "malformed date", date: "2026/08/01", wantErr: "YYYY-MM-DD"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			from, to, err := dateRange(tt.date, tt.from, tt.to, now)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected an error mentioning %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error %q should mention %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := from.Format(edinet.DateLayout); got != tt.wantFrom {
				t.Errorf("from = %s, want %s", got, tt.wantFrom)
			}
			if got := to.Format(edinet.DateLayout); got != tt.wantTo {
				t.Errorf("to = %s, want %s", got, tt.wantTo)
			}
		})
	}
}

// The default day must be Tokyo's, not the caller's. At 23:00 UTC on the 15th
// it is already the 16th in Japan, and that is the day EDINET is filing under.
func TestDateRangeDefaultsToTokyoToday(t *testing.T) {
	now := time.Date(2026, 8, 15, 23, 0, 0, 0, time.UTC)

	from, to, err := dateRange("", "", "", now)
	if err != nil {
		t.Fatalf("dateRange: %v", err)
	}
	if got := from.Format(edinet.DateLayout); got != "2026-08-16" {
		t.Errorf("from = %s, want 2026-08-16 (today in Tokyo)", got)
	}
	if got := to.Format(edinet.DateLayout); got != "2026-08-16" {
		t.Errorf("to = %s, want 2026-08-16 (today in Tokyo)", got)
	}

	// The same instant expressed in a US zone must give the same Tokyo day.
	la, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Skipf("no tzdata available: %v", err)
	}
	from, _, err = dateRange("", "", "", now.In(la))
	if err != nil {
		t.Fatalf("dateRange: %v", err)
	}
	if got := from.Format(edinet.DateLayout); got != "2026-08-16" {
		t.Errorf("from = %s in Los Angeles, want 2026-08-16", got)
	}
}

// A date the user types is a Tokyo date, and must survive validation on the day
// they type it.
func TestDateRangeFlagsAreTokyoDates(t *testing.T) {
	now := time.Date(2026, 8, 15, 23, 0, 0, 0, time.UTC) // 2026-08-16 in Tokyo

	from, to, err := dateRange("2026-08-16", "", "", now)
	if err != nil {
		t.Fatalf("dateRange: %v", err)
	}
	if err := edinet.ValidateDate(from, now); err != nil {
		t.Errorf("--date 2026-08-16 must be accepted at this instant: %v", err)
	}
	if !from.Equal(to) {
		t.Errorf("--date should produce a single day, got %v to %v", from, to)
	}
}

func TestTickerTrimsTrailingZero(t *testing.T) {
	tests := map[string]string{
		"72030": "7203",
		"67580": "6758",
		"":      "",
		"1234":  "1234",
	}
	for in, want := range tests {
		if got := ticker(in); got != want {
			t.Errorf("ticker(%q) = %q, want %q", in, got, want)
		}
	}
}

// People type the 4-digit ticker; EDINET stores 5 digits.
func TestSecCodeMatches(t *testing.T) {
	if !secCodeMatches("72030", "7203") {
		t.Error("a 4-digit ticker should match its 5-digit securities code")
	}
	if !secCodeMatches("72030", "72030") {
		t.Error("an exact securities code should match")
	}
	if secCodeMatches("", "7203") {
		t.Error("an unlisted filer should not match a ticker")
	}
	if secCodeMatches("72030", "6758") {
		t.Error("a different ticker must not match")
	}
}

func TestResolveDocTypes(t *testing.T) {
	got, err := resolveDocTypes([]string{"yuho", "180"})
	if err != nil {
		t.Fatalf("resolveDocTypes: %v", err)
	}
	if !got["120"] || !got["180"] {
		t.Errorf("got %v, want the 120 and 180 codes", got)
	}

	if _, err := resolveDocTypes([]string{"nonsense"}); err == nil {
		t.Fatal("expected an error for an unknown type")
	}
}

func TestAvailableFiles(t *testing.T) {
	d := edinet.Document{XbrlFlag: "1", PdfFlag: "1", CsvFlag: "1"}
	if got := availableFiles(d); got != "main,pdf,csv" {
		t.Errorf("availableFiles = %q, want main,pdf,csv", got)
	}
	if got := availableFiles(edinet.Document{}); got != "" {
		t.Errorf("availableFiles = %q, want empty", got)
	}
}

func TestHasAll(t *testing.T) {
	d := edinet.Document{XbrlFlag: "1", CsvFlag: "1"}
	if !hasAll(d, []edinet.FileType{edinet.FileMain, edinet.FileCSV}) {
		t.Error("expected both requested types to be available")
	}
	if hasAll(d, []edinet.FileType{edinet.FilePDF}) {
		t.Error("a missing type must exclude the document")
	}
	if !hasAll(d, nil) {
		t.Error("no constraint should keep everything")
	}
}

// A malicious or malformed archive must not write outside the target directory.
func TestSafeJoinRejectsTraversal(t *testing.T) {
	root := t.TempDir()

	if _, err := safeJoin(root, "XBRL_TO_CSV/report.csv"); err != nil {
		t.Errorf("a normal entry was rejected: %v", err)
	}
	for _, name := range []string{"../escape.txt", "a/../../escape.txt", "/etc/passwd"} {
		if _, err := safeJoin(root, name); err == nil {
			t.Errorf("safeJoin accepted %q, which escapes the destination", name)
		}
	}
}

func TestExtractZip(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("XBRL_TO_CSV/report.csv")
	if err != nil {
		t.Fatalf("creating zip entry: %v", err)
	}
	if _, err := w.Write([]byte("data")); err != nil {
		t.Fatalf("writing zip entry: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("closing zip: %v", err)
	}

	dir := t.TempDir()
	file := &edinet.File{DocID: "S100AAAA", Type: edinet.FileCSV, Data: buf.Bytes()}
	if err := extractZip(file, dir, false); err != nil {
		t.Fatalf("extractZip: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "S100AAAA", "XBRL_TO_CSV", "report.csv"))
	if err != nil {
		t.Fatalf("reading extracted file: %v", err)
	}
	if string(got) != "data" {
		t.Errorf("extracted content = %q", got)
	}

	// A second extraction must not silently overwrite.
	if err := extractZip(file, dir, false); err == nil {
		t.Error("expected an error when the destination already exists")
	}
	if err := extractZip(file, dir, true); err != nil {
		t.Errorf("--force should overwrite: %v", err)
	}
}

func TestExtractRejectsPDF(t *testing.T) {
	file := &edinet.File{DocID: "S100AAAA", Type: edinet.FilePDF, Data: []byte("%PDF")}
	if err := extractZip(file, t.TempDir(), false); err == nil {
		t.Fatal("expected an error: a PDF is not an archive")
	}
}

func TestSaveFileRefusesToClobber(t *testing.T) {
	dir := t.TempDir()
	file := &edinet.File{DocID: "S100AAAA", Type: edinet.FilePDF, Data: []byte("%PDF")}

	if err := saveFile(file, dir, false); err != nil {
		t.Fatalf("saveFile: %v", err)
	}
	if err := saveFile(file, dir, false); err == nil {
		t.Error("expected an error when the file already exists")
	}
	if err := saveFile(file, dir, true); err != nil {
		t.Errorf("--force should overwrite: %v", err)
	}
}

func TestMissingAPIKeyIsExplained(t *testing.T) {
	t.Setenv("EDINET_API_KEY", "")

	g := &globals{}
	_, err := g.key()
	if err == nil {
		t.Fatal("expected an error when no key is configured")
	}
	for _, want := range []string{"EDINET_API_KEY", "--api-key"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}

	g.apiKey = "explicit"
	if got, err := g.key(); err != nil || got != "explicit" {
		t.Errorf("--api-key should win: %q, %v", got, err)
	}
}

func TestAPIKeyFromEnvironment(t *testing.T) {
	t.Setenv("EDINET_API_KEY", "from-env")
	g := &globals{}
	got, err := g.key()
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	if got != "from-env" {
		t.Errorf("key = %q, want from-env", got)
	}
}

// Every runnable command needs a description and a worked example, since the
// domain vocabulary (docID, EDINET code, 書類種別コード) is not guessable.
func TestCommandsAreDocumented(t *testing.T) {
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			walk(sub)
		}
		if !c.Runnable() {
			return
		}
		if c.Short == "" {
			t.Errorf("%s has no short description", c.CommandPath())
		}
		if c.Example == "" {
			t.Errorf("%s has no example", c.CommandPath())
		}
	}
	walk(NewRootCommand())
}

// `--version` and the `version` subcommand must not drift apart.
func TestVersionOutputsMatch(t *testing.T) {
	run := func(args ...string) string {
		root := NewRootCommand()
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return out.String()
	}

	sub := run("version")
	flag := run("--version")
	if sub != flag {
		t.Errorf("`version` printed %q but `--version` printed %q", sub, flag)
	}
	for _, want := range []string{"edinet", "commit:", "built:"} {
		if !strings.Contains(sub, want) {
			t.Errorf("version output %q is missing %q", sub, want)
		}
	}
}

func TestDocIDFromPath(t *testing.T) {
	tests := map[string]string{
		"S100AAAA_csv.zip":             "S100AAAA",
		"./downloads/S100BBBB_csv.zip": "S100BBBB",
		"renamed.zip":                  "",
		"S100AAAA_main.zip":            "",
		"":                             "",
	}
	for in, want := range tests {
		if got := docIDFromPath(in); got != want {
			t.Errorf("docIDFromPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReadDocIDs(t *testing.T) {
	in := "S100AAAA\n\n# a comment\nS100BBBB  \n\t\nS100CCCC\n"
	got, err := readDocIDs(strings.NewReader(in))
	if err != nil {
		t.Fatalf("readDocIDs: %v", err)
	}
	want := []string{"S100AAAA", "S100BBBB", "S100CCCC"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("id %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestFactsSources(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		files   []string
		stdin   string
		want    []factsSource
		wantErr string
	}{
		{
			name: "document ids as arguments",
			args: []string{"S100AAAA", "S100BBBB"},
			want: []factsSource{{DocID: "S100AAAA"}, {DocID: "S100BBBB"}},
		},
		{
			name:  "dash reads document ids from stdin",
			args:  []string{"-"},
			stdin: "S100AAAA\nS100BBBB\n",
			want:  []factsSource{{DocID: "S100AAAA"}, {DocID: "S100BBBB"}},
		},
		{
			name:  "--file with glob-expanded arguments",
			args:  []string{"./renamed.zip"},
			files: []string{"./S100AAAA_csv.zip"},
			want: []factsSource{
				{DocID: "S100AAAA", Path: "./S100AAAA_csv.zip"},
				{DocID: "", Path: "./renamed.zip"},
			},
		},
		{
			name:    "no input at all",
			wantErr: "document ID",
		},
		{
			name:    "stdin cannot be mixed with --file",
			args:    []string{"-"},
			files:   []string{"a.zip"},
			wantErr: "--file",
		},
		{
			name:    "dash cannot be mixed with document ids",
			args:    []string{"-", "S100AAAA"},
			wantErr: "on its own",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := factsSources(tt.args, tt.files, strings.NewReader(tt.stdin))
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected an error mentioning %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error %q should mention %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("source %d = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// Output order must not depend on which download finished first, or a pipeline
// cannot diff two runs.
func TestFetchOrderedPreservesInputOrder(t *testing.T) {
	sources := make([]factsSource, 20)
	for i := range sources {
		sources[i] = factsSource{DocID: fmt.Sprintf("S%03d", i)}
	}

	load := func(_ context.Context, src factsSource) ([]facts.Row, error) {
		// Later documents finish first, so a naive implementation would emit
		// them out of order.
		n := 0
		_, _ = fmt.Sscanf(src.DocID, "S%03d", &n)
		time.Sleep(time.Duration(20-n) * time.Millisecond)
		return []facts.Row{{Filing: facts.Filing{DocID: src.DocID}}}, nil
	}

	var got []string
	emit := func(rows []facts.Row) error {
		for _, r := range rows {
			got = append(got, r.DocID)
		}
		return nil
	}

	failed, err := fetchOrdered(context.Background(), sources, 4, load, emit, func(factsSource, error) {})
	if err != nil {
		t.Fatalf("fetchOrdered: %v", err)
	}
	if failed != 0 {
		t.Errorf("failed = %d, want 0", failed)
	}
	for i := range sources {
		if got[i] != sources[i].DocID {
			t.Fatalf("position %d = %q, want %q (output is out of order)", i, got[i], sources[i].DocID)
		}
	}
}

// A single unreadable filing must not discard the rest of a long batch.
func TestFetchOrderedSkipsFailures(t *testing.T) {
	sources := []factsSource{{DocID: "A"}, {DocID: "B"}, {DocID: "C"}}

	load := func(_ context.Context, src factsSource) ([]facts.Row, error) {
		if src.DocID == "B" {
			return nil, errors.New("viewing period has expired")
		}
		return []facts.Row{{Filing: facts.Filing{DocID: src.DocID}}}, nil
	}

	var got, skipped []string
	emit := func(rows []facts.Row) error {
		for _, r := range rows {
			got = append(got, r.DocID)
		}
		return nil
	}
	onError := func(src factsSource, _ error) { skipped = append(skipped, src.DocID) }

	failed, err := fetchOrdered(context.Background(), sources, 2, load, emit, onError)
	if err != nil {
		t.Fatalf("fetchOrdered: %v", err)
	}
	if failed != 1 {
		t.Errorf("failed = %d, want 1", failed)
	}
	if len(got) != 2 || got[0] != "A" || got[1] != "C" {
		t.Errorf("emitted %v, want [A C]", got)
	}
	if len(skipped) != 1 || skipped[0] != "B" {
		t.Errorf("skipped %v, want [B]", skipped)
	}
}

// Results are held by blocked workers rather than queued, so a batch larger
// than memory can still be streamed.
func TestFetchOrderedBoundsHeldResults(t *testing.T) {
	const concurrency = 3
	sources := make([]factsSource, 50)
	for i := range sources {
		sources[i] = factsSource{DocID: fmt.Sprintf("S%03d", i)}
	}

	var mu sync.Mutex
	held, peak := 0, 0

	load := func(_ context.Context, src factsSource) ([]facts.Row, error) {
		mu.Lock()
		held++
		if held > peak {
			peak = held
		}
		mu.Unlock()
		return []facts.Row{{Filing: facts.Filing{DocID: src.DocID}}}, nil
	}
	emit := func([]facts.Row) error {
		mu.Lock()
		held--
		mu.Unlock()
		time.Sleep(time.Millisecond) // a slow consumer must not let results pile up
		return nil
	}

	if _, err := fetchOrdered(context.Background(), sources, concurrency, load, emit, func(factsSource, error) {}); err != nil {
		t.Fatalf("fetchOrdered: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	// concurrency workers may each hold a finished result, plus the one being
	// emitted. Anything beyond that means results are queueing up.
	if peak > concurrency+1 {
		t.Errorf("held %d results at once, want at most %d", peak, concurrency+1)
	}
}

// A --limit cutoff cancels remaining downloads on purpose. A worker whose
// in-flight load observes that cancellation must never surface as a load
// failure -- the cancellation is the caller's own doing, not a broken
// filing. Index 0 is deliberately slow so every other worker finishes and
// blocks trying to send its (successful) result before index 0 -- and hence
// the cancel -- arrives, mirroring "later documents finish first". Repeated
// over many trials and concurrency levels, this is a regression guard for
// fetchOrdered's ctx.Err() check at the receive site.
func TestFetchOrderedLimitCancelIsNotAFailure(t *testing.T) {
	const (
		limit       = 1
		concurrency = 16
		trials      = 50
	)

	for trial := 0; trial < trials; trial++ {
		sources := make([]factsSource, concurrency*2)
		for i := range sources {
			sources[i] = factsSource{DocID: fmt.Sprintf("S%03d", i)}
		}

		load := func(ctx context.Context, src factsSource) ([]facts.Row, error) {
			if src.DocID == sources[0].DocID {
				time.Sleep(2 * time.Millisecond)
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return []facts.Row{{Filing: facts.Filing{DocID: src.DocID}}}, nil
		}

		ctx, cancel := context.WithCancel(context.Background())

		written := 0
		emit := func(rows []facts.Row) error {
			written += len(rows)
			if written >= limit {
				cancel() // mirrors runFacts's --limit cutoff
			}
			return nil
		}

		var mu sync.Mutex
		var skipped []string
		onError := func(src factsSource, err error) {
			mu.Lock()
			skipped = append(skipped, fmt.Sprintf("%s: %v", src.DocID, err))
			mu.Unlock()
		}

		failed, err := fetchOrdered(ctx, sources, concurrency, load, emit, onError)
		cancel()
		if failed != 0 {
			t.Fatalf("trial %d: failed = %d, want 0 (a limit cutoff is not a load failure): %v", trial, failed, skipped)
		}
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("trial %d: err = %v, want nil or context.Canceled", trial, err)
		}
	}
}

// An interrupt (or any cancellation that is not emit's own --limit cutoff)
// must be surfaced rather than silently discarded, even after part of the
// batch already succeeded. cancel is called deterministically from emit
// instead of on a timer, so the test cannot flake on scheduling.
func TestFetchOrderedInterruptIsSurfaced(t *testing.T) {
	sources := make([]factsSource, 10)
	for i := range sources {
		sources[i] = factsSource{DocID: fmt.Sprintf("S%03d", i)}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	load := func(ctx context.Context, src factsSource) ([]facts.Row, error) {
		select {
		case <-time.After(2 * time.Millisecond):
			return []facts.Row{{Filing: facts.Filing{DocID: src.DocID}}}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	var mu sync.Mutex
	emitted := 0
	emit := func(rows []facts.Row) error {
		mu.Lock()
		emitted += len(rows)
		n := emitted
		mu.Unlock()
		if n == 3 {
			cancel() // an interrupt arriving mid-batch, not a --limit cutoff
		}
		return nil
	}

	failed, err := fetchOrdered(ctx, sources, 2, load, emit, func(factsSource, error) {})
	if err == nil {
		t.Fatal("expected fetchOrdered to surface the cancellation rather than returning nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if failed != 0 {
		t.Errorf("failed = %d, want 0: an interrupt is not a load failure either", failed)
	}
}

// A parent context cancelled for a reason other than --limit (an interrupt,
// or the caller's own context dying) must fail runFacts rather than exit 0
// with truncated output. errors.Is(err, context.Canceled) is exactly the
// predicate main.go uses to decide whether to print the error, so asserting
// it here is checking the same contract main.go relies on, not assuming it.
func TestFactsInterruptedRunIsNotSwallowed(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "S100AAAA_csv.zip")
	if err := os.WriteFile(good, testBundle(t), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	root := NewRootCommand()
	var out, errBuf bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errBuf)
	root.SetArgs([]string{"facts", "--file", good, "-o", "csv"})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // simulate an interrupt that arrived before the run could finish

	err := root.ExecuteContext(ctx)
	if err == nil {
		t.Fatal("expected a non-nil error: an interrupted run must not exit 0")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if strings.Contains(err.Error(), "filings failed") {
		t.Errorf("an interrupt must not be reported as filing failures: %v", err)
	}
}

// A partial failure must still print every successful row, and must still be
// visible to a script through the exit code.
func TestFactsPartialFailureIsReported(t *testing.T) {
	dir := t.TempDir()

	good := filepath.Join(dir, "S100AAAA_csv.zip")
	if err := os.WriteFile(good, testBundle(t), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	missing := filepath.Join(dir, "S100BBBB_csv.zip")

	root := NewRootCommand()
	var out, errBuf bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errBuf)
	root.SetArgs([]string{"facts", "--file", missing, "--file", good, "-o", "csv"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected a non-nil error so the process exits non-zero")
	}
	// The denominator is deliberately gone: --limit can stop a run before every
	// source is attempted, which would make len(sources) an overstatement.
	if !strings.Contains(err.Error(), "1 filings failed") {
		t.Errorf("error = %v, want a count of failures", err)
	}
	if !strings.Contains(out.String(), "S100AAAA") {
		t.Errorf("the readable bundle's rows are missing:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "45095325000000") {
		t.Errorf("expected the fixture's reported value:\n%s", out.String())
	}
	// One stderr line per failed filing is the property that makes a partial
	// failure visible to someone watching the run, not just to a script
	// checking the exit code. The "N of M failed" summary is deliberately not
	// asserted here on errBuf: root's SilenceErrors means it is never printed
	// by the command itself, only carried in the returned error (checked
	// above) for main.go to print once; printing it again from here would
	// double it up on a real run.
	if !strings.Contains(errBuf.String(), missing) {
		t.Errorf("stderr should name the failed filing %q:\n%s", missing, errBuf.String())
	}
}

// A missing API key must be reported once for the whole run, not once per
// source -- g.client() used to be called inside every worker, so a 200-filing
// batch with no key configured produced 200 identical failure lines.
func TestFactsMissingAPIKeyFailsOnce(t *testing.T) {
	t.Setenv("EDINET_API_KEY", "")

	root := NewRootCommand()
	var out, errBuf bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errBuf)
	root.SetArgs([]string{"facts", "S100AAAA", "S100BBBB", "S100CCCC", "-o", "csv"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected an error when no API key is configured")
	}
	if !strings.Contains(err.Error(), "no API key") {
		t.Errorf("error = %v, want the missing-key message", err)
	}
	if n := strings.Count(err.Error(), "no API key"); n != 1 {
		t.Errorf("\"no API key\" appears %d times in %q, want exactly 1", n, err)
	}
	// The key is resolved before any source is attempted, so no per-filing
	// skip line should ever reach stderr.
	if errBuf.Len() != 0 {
		t.Errorf("stderr should be empty for a client-resolution failure: %q", errBuf.String())
	}
	if out.Len() != 0 {
		t.Errorf("no rows should have been written: %q", out.String())
	}
}

// --file reads bundles already on disk, so a run using only --file must not
// need an API key at all.
func TestFactsFileOnlyRunNeedsNoAPIKey(t *testing.T) {
	t.Setenv("EDINET_API_KEY", "")

	dir := t.TempDir()
	good := filepath.Join(dir, "S100AAAA_csv.zip")
	if err := os.WriteFile(good, testBundle(t), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	root := NewRootCommand()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"facts", "--file", good, "-o", "csv"})

	if err := root.Execute(); err != nil {
		t.Fatalf("a --file-only run must not require an API key: %v", err)
	}
	if !strings.Contains(out.String(), "45095325000000") {
		t.Errorf("expected the fixture's reported value:\n%s", out.String())
	}
}

// Empty stdin on `-` is the normal shape of "nothing matched" from an
// upstream `docs list` (a quiet weekend, for instance), not an error -- a
// stateless filter maps empty input to empty output.
func TestFactsEmptyStdinIsEmptyOutputNotAnError(t *testing.T) {
	tests := []struct {
		format string
		check  func(t *testing.T, out string)
	}{
		{format: "json", check: func(t *testing.T, out string) {
			if strings.TrimSpace(out) != "[]" {
				t.Errorf("json output = %q, want []", out)
			}
		}},
		{format: "csv", check: func(t *testing.T, out string) {
			lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
			if len(lines) != 1 {
				t.Errorf("csv output should be a bare header line, got %d lines:\n%s", len(lines), out)
			}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.format, func(t *testing.T) {
			root := NewRootCommand()
			var out, errBuf bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&errBuf)
			root.SetIn(strings.NewReader(""))
			root.SetArgs([]string{"facts", "-", "-o", tt.format})

			if err := root.Execute(); err != nil {
				t.Fatalf("empty stdin must not fail: %v", err)
			}
			tt.check(t, out.String())
		})
	}
}

// A filter that matches nothing must not print a bare header row in the table
// format -- docs list and company search both stay silent on stdout in the
// same situation, and facts had regressed against that convention.
func TestFactsTableWithNoMatchesPrintsNoHeader(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "S100AAAA_csv.zip")
	if err := os.WriteFile(good, testBundle(t), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	root := NewRootCommand()
	var out, errBuf bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errBuf)
	root.SetArgs([]string{"facts", "--file", good, "--element", "ZZZZZ"})

	if err := root.Execute(); err != nil {
		t.Fatalf("an empty match set is not a failure: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("table format must not print a bare header when nothing matched:\n%q", out.String())
	}
	if !strings.Contains(errBuf.String(), "No facts matched") {
		t.Errorf("expected the no-matches message on stderr:\n%s", errBuf.String())
	}
}

// testBundle builds the byte layout EDINET actually ships: a ZIP of UTF-16LE
// tab-separated files with a byte order mark.
func testBundle(t *testing.T) []byte {
	t.Helper()

	const header = "要素ID\t項目名\tコンテキストID\t相対年度\t連結・個別\t期間・時点\tユニットID\t単位\t値\n"
	content := header +
		"jpdei_cor:EDINETCodeDEI\tEDINETコード\tFilingDateInstant\t当期\t\t時点\t\t\tE02144\n" +
		"jpdei_cor:SecurityCodeDEI\t証券コード\tFilingDateInstant\t当期\t\t時点\t\t\t72030\n" +
		"jpcrp_cor:NetSales\t売上高\tCurrentYearDuration\t当期\t連結\t期間\tJPY\t円\t45095325000000\n"

	enc := unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewEncoder()
	encoded, _, err := transform.Bytes(enc, []byte(content))
	if err != nil {
		t.Fatalf("encoding fixture to UTF-16LE: %v", err)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("XBRL_TO_CSV/jpcrp030000-asr-001_E02144-000_2026-03-31_01_2026-06-20.csv")
	if err != nil {
		t.Fatalf("creating zip entry: %v", err)
	}
	if _, err := w.Write(encoded); err != nil {
		t.Fatalf("writing zip entry: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("closing zip: %v", err)
	}
	return buf.Bytes()
}

// The join key must not depend on which machine-readable format was asked for.
// docs list -o csv once trimmed secCode to 4 digits while -o json kept 5.
func TestMachineFormatsAgreeOnFields(t *testing.T) {
	t.Run("documents", func(t *testing.T) {
		recs := []edinet.Document{{DocID: "S100AAAA", SecCode: "72030", FilerName: "トヨタ"}}
		assertFormatParity(t, docsSpec(), recs)
	})
	t.Run("facts", func(t *testing.T) {
		recs := []facts.Row{{
			Filing: facts.Filing{DocID: "S100AAAA", SecCode: "72030"},
			Fact:   facts.Fact{ElementID: "jpcrp_cor:NetSales", Value: "45095325000000"},
		}}
		assertFormatParity(t, factsSpec(), recs)
	})
	t.Run("company", func(t *testing.T) {
		recs := []codelist.Entry{{EdinetCode: "E02144", SecCode: "72030", Name: "トヨタ自動車株式会社"}}
		assertFormatParity(t, companySpec(), recs)
	})
	// codeSpec's records carry no secCode at all; assertFormatParity must not
	// assume every record type has a join key to check.
	t.Run("codes", func(t *testing.T) {
		recs := []CodeEntry{{Table: "docType", Key: "120", Value: "有価証券報告書"}}
		assertFormatParity(t, codeSpec(), recs)
	})
}

func assertFormatParity[T any](t *testing.T, spec output.TableSpec[T], recs []T) {
	t.Helper()

	var csvBuf, jsonBuf bytes.Buffer
	if err := output.Render(&csvBuf, output.FormatCSV, spec, recs); err != nil {
		t.Fatalf("csv: %v", err)
	}
	if err := output.Render(&jsonBuf, output.FormatJSON, spec, recs); err != nil {
		t.Fatalf("json: %v", err)
	}

	var decoded []map[string]any
	if err := json.Unmarshal(jsonBuf.Bytes(), &decoded); err != nil {
		t.Fatalf("decoding JSON output: %v", err)
	}

	header, _, _ := strings.Cut(csvBuf.String(), "\n")
	cols := strings.Split(strings.TrimSpace(header), ",")
	if len(cols) != len(decoded[0]) {
		t.Errorf("CSV has %d columns but JSON has %d keys\ncsv: %s", len(cols), len(decoded[0]), header)
	}
	for _, col := range cols {
		if _, ok := decoded[0][col]; !ok {
			t.Errorf("CSV column %q has no matching JSON key", col)
		}
	}

	// secCode is the join key J-Quants shares, and it must survive as 5 digits
	// -- but not every record type carries one (codeSpec's rows do not), so
	// the check only applies when the field is actually present.
	got, hasSecCode := decoded[0]["secCode"]
	if hasSecCode && got != "72030" {
		t.Errorf("secCode = %v in JSON, want the unmodified 5-digit code", got)
	}
	if hasSecCode && !strings.Contains(csvBuf.String(), "72030") {
		t.Errorf("CSV lost the 5-digit securities code:\n%s", csvBuf.String())
	}
}

func TestRootCommandWiring(t *testing.T) {
	root := NewRootCommand()

	want := []string{"docs", "facts", "company", "codes", "version"}
	have := map[string]bool{}
	for _, c := range root.Commands() {
		have[c.Name()] = true
	}
	for _, name := range want {
		if !have[name] {
			t.Errorf("root command is missing %q", name)
		}
	}

	// Unknown flags must fail rather than being silently ignored.
	root.SetArgs([]string{"docs", "list", "--nonexistent"})
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	if err := root.Execute(); err == nil {
		t.Error("expected an error for an unknown flag")
	}
}
