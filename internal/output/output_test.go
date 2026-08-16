package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestWidthCountsFullWidthRunes(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"abc", 3},
		{"トヨタ", 6},
		{"トヨタ自動車株式会社", 20},
		{"S100AAAA", 8},
		{"有価証券報告書－第122期", 23}, // 9 full-width (incl. －) + 3 ASCII digits
	}
	for _, tt := range tests {
		if got := Width(tt.in); got != tt.want {
			t.Errorf("Width(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		in    string
		width int
		want  string
	}{
		{"abcdef", 10, "abcdef"},
		{"abcdef", 4, "abc…"},
		{"トヨタ自動車", 6, "トヨ…"}, // a third kana would put the ellipsis at cell 7
		{"トヨタ自動車", 100, "トヨタ自動車"},
	}
	for _, tt := range tests {
		if got := Truncate(tt.in, tt.width); got != tt.want {
			t.Errorf("Truncate(%q, %d) = %q, want %q", tt.in, tt.width, got, tt.want)
		}
		if got := Width(Truncate(tt.in, tt.width)); tt.width > 0 && got > tt.width {
			t.Errorf("Truncate(%q, %d) is %d cells wide, over the limit", tt.in, tt.width, got)
		}
	}
}

func TestParseFormat(t *testing.T) {
	for _, s := range []string{"table", "JSON", "csv"} {
		if _, err := ParseFormat(s); err != nil {
			t.Errorf("ParseFormat(%q): %v", s, err)
		}
	}
	if _, err := ParseFormat("yaml"); err == nil {
		t.Error("expected an error for an unsupported format")
	}
}

type doc struct {
	DocID  string `json:"docID"`
	Filer  string `json:"filerName"`
	DocTyp string `json:"docType"`
}

func docSpec() TableSpec[doc] {
	return TableSpec[doc]{
		Headers: []string{"DOCID", "FILER", "TYPE"},
		Row:     func(d doc) []string { return []string{d.DocID, d.Filer, d.DocTyp} },
	}
}

// Columns must line up when a cell contains CJK text, which is the normal case
// for EDINET filer names.
func TestTableAlignsCJKColumns(t *testing.T) {
	var buf bytes.Buffer
	recs := []doc{
		{"S100AAAA", "トヨタ自動車株式会社", "有価証券報告書"},
		{"S100BBBB", "Acme Inc.", "臨時報告書"},
	}
	if err := Render(&buf, FormatTable, docSpec(), recs); err != nil {
		t.Fatalf("Render: %v", err)
	}

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3:\n%s", len(lines), buf.String())
	}

	want := -1
	for _, line := range lines {
		idx := strings.LastIndex(line, "  ")
		col := Width(line[:idx+2])
		if want == -1 {
			want = col
			continue
		}
		if col != want {
			t.Errorf("last column starts at cell %d on %q, want %d", col, line, want)
		}
	}
}

func TestTableTruncatesToMax(t *testing.T) {
	var buf bytes.Buffer
	spec := docSpec()
	spec.Max = []int{0, 10, 0}
	recs := []doc{{"short", strings.Repeat("x", 100), "t"}}

	if err := Render(&buf, FormatTable, spec, recs); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(buf.String(), strings.Repeat("x", 11)) {
		t.Errorf("column was not truncated:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "…") {
		t.Errorf("truncation was not marked:\n%s", buf.String())
	}
}

// Newlines inside a value would otherwise split one row across two lines.
func TestTableSanitizesEmbeddedNewlines(t *testing.T) {
	var buf bytes.Buffer
	recs := []doc{{"x", "line1\nline2", "t"}}
	if err := Render(&buf, FormatTable, docSpec(), recs); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got := strings.Count(strings.TrimRight(buf.String(), "\n"), "\n"); got != 1 {
		t.Errorf("got %d newlines, want 1:\n%q", got, buf.String())
	}
}

// Max is a display concern; the machine-readable formats must never lose data.
func TestCSVKeepsFullValues(t *testing.T) {
	var buf bytes.Buffer
	long := strings.Repeat("x", 100)
	spec := docSpec()
	spec.Max = []int{0, 10, 0}

	if err := Render(&buf, FormatCSV, spec, []doc{{"x,y", long, "t"}}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, long) {
		t.Error("CSV output must not truncate; Max applies to the table format only")
	}
	if !strings.Contains(out, `"x,y"`) {
		t.Errorf("comma was not quoted:\n%s", out)
	}
}

// The two machine-readable formats are both derived from the struct, so a field
// can never appear in one and not the other.
func TestCSVHeaderMatchesJSONKeys(t *testing.T) {
	var csvBuf, jsonBuf bytes.Buffer
	recs := []doc{{"S100AAAA", "トヨタ", "有価証券報告書"}}

	if err := Render(&csvBuf, FormatCSV, docSpec(), recs); err != nil {
		t.Fatalf("csv: %v", err)
	}
	if err := Render(&jsonBuf, FormatJSON, docSpec(), recs); err != nil {
		t.Fatalf("json: %v", err)
	}

	header, _, _ := strings.Cut(csvBuf.String(), "\n")
	var decoded []map[string]any
	if err := json.Unmarshal(jsonBuf.Bytes(), &decoded); err != nil {
		t.Fatalf("decoding JSON output: %v", err)
	}

	for _, col := range strings.Split(strings.TrimSpace(header), ",") {
		if _, ok := decoded[0][col]; !ok {
			t.Errorf("CSV column %q has no matching JSON key", col)
		}
	}
	if len(decoded[0]) != len(strings.Split(strings.TrimSpace(header), ",")) {
		t.Errorf("JSON has %d keys but CSV has %s", len(decoded[0]), header)
	}
}

func TestJSONKeepsJapaneseReadable(t *testing.T) {
	var buf bytes.Buffer
	recs := []doc{{"S100AAAA", "トヨタ", "有価証券報告書"}}
	if err := Render(&buf, FormatJSON, docSpec(), recs); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `"docID": "S100AAAA"`) {
		t.Errorf("unexpected JSON:\n%s", out)
	}
	// Japanese must stay readable rather than being escaped to \uXXXX.
	if !strings.Contains(out, "トヨタ") {
		t.Errorf("JSON escaped non-ASCII text:\n%s", out)
	}
}

// A pipeline reading the output must not have to special-case "no results".
func TestEmptyResultSets(t *testing.T) {
	var jsonBuf, csvBuf bytes.Buffer

	if err := Render(&jsonBuf, FormatJSON, docSpec(), nil); err != nil {
		t.Fatalf("json: %v", err)
	}
	if got := strings.TrimSpace(jsonBuf.String()); got != "[]" {
		t.Errorf("empty JSON = %q, want []", got)
	}

	if err := Render(&csvBuf, FormatCSV, docSpec(), nil); err != nil {
		t.Fatalf("csv: %v", err)
	}
	if got := strings.TrimSpace(csvBuf.String()); got != "docID,filerName,docType" {
		t.Errorf("empty CSV = %q, want the header row", got)
	}
}

// json and csv must not accumulate the whole result set, so a batch larger than
// memory can still be written. Both wrap the writer in a bufio.Writer, so the
// assertion is that output appears once that buffer fills — not on the very
// first record.
func TestStreamsWriteIncrementally(t *testing.T) {
	for _, f := range []Format{FormatJSON, FormatCSV} {
		t.Run(string(f), func(t *testing.T) {
			var buf bytes.Buffer
			s := NewStream(&buf, f, docSpec())
			for i := range 2000 {
				rec := doc{DocID: fmt.Sprintf("S%06d", i), Filer: "トヨタ自動車株式会社"}
				if err := s.Write(rec); err != nil {
					t.Fatalf("Write: %v", err)
				}
			}
			if buf.Len() == 0 {
				t.Errorf("%s buffered 2000 records instead of writing through", f)
			}
			if err := s.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
			if !strings.Contains(buf.String(), "S001999") {
				t.Errorf("%s lost the last record", f)
			}
		})
	}
}

// The table format is the one that legitimately buffers, because column widths
// are not known until the last row.
func TestTableBuffersUntilClose(t *testing.T) {
	var buf bytes.Buffer
	s := NewStream(&buf, FormatTable, docSpec())
	if err := s.Write(doc{DocID: "S100AAAA"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("table wrote before Close: %q", buf.String())
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !strings.Contains(buf.String(), "S100AAAA") {
		t.Errorf("Close did not flush:\n%s", buf.String())
	}
}
