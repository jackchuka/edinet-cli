package output

import (
	"bytes"
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

// Columns must line up when a cell contains CJK text, which is the normal case
// for EDINET filer names.
func TestTableAlignsCJKColumns(t *testing.T) {
	var buf bytes.Buffer
	tbl := Table{
		Headers: []string{"DOCID", "FILER", "TYPE"},
		Rows: [][]string{
			{"S100AAAA", "トヨタ自動車株式会社", "有価証券報告書"},
			{"S100BBBB", "Acme Inc.", "臨時報告書"},
		},
	}
	if err := Render(&buf, FormatTable, tbl, nil); err != nil {
		t.Fatalf("Render: %v", err)
	}

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3:\n%s", len(lines), buf.String())
	}

	// The final column must start at the same cell on every line.
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
	tbl := Table{
		Headers: []string{"A", "B"},
		Rows:    [][]string{{"short", strings.Repeat("x", 100)}},
		Max:     []int{0, 10},
	}
	if err := Render(&buf, FormatTable, tbl, nil); err != nil {
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
	tbl := Table{
		Headers: []string{"A", "B"},
		Rows:    [][]string{{"x", "line1\nline2"}},
	}
	if err := Render(&buf, FormatTable, tbl, nil); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got := strings.Count(strings.TrimRight(buf.String(), "\n"), "\n"); got != 1 {
		t.Errorf("got %d newlines, want 1:\n%q", got, buf.String())
	}
}

func TestCSVKeepsFullValues(t *testing.T) {
	var buf bytes.Buffer
	long := strings.Repeat("x", 100)
	tbl := Table{
		Headers: []string{"A", "B"},
		Rows:    [][]string{{"x,y", long}},
		Max:     []int{0, 10},
	}
	if err := Render(&buf, FormatCSV, tbl, nil); err != nil {
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

func TestJSONRendersRawValue(t *testing.T) {
	var buf bytes.Buffer
	raw := []map[string]string{{"docID": "S100AAAA", "filerName": "トヨタ"}}
	if err := Render(&buf, FormatJSON, Table{}, raw); err != nil {
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
