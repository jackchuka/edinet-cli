package cli

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackchuka/edinet-cli/internal/edinet"
	"github.com/spf13/cobra"
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
