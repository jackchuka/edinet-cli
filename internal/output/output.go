// Package output renders result sets as a terminal table, JSON, or CSV.
package output

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Format is a user-selected output format.
type Format string

// Supported output formats.
const (
	FormatTable Format = "table"
	FormatJSON  Format = "json"
	FormatCSV   Format = "csv"
)

// Formats lists the accepted -o values in a stable order.
func Formats() []string {
	return []string{string(FormatTable), string(FormatJSON), string(FormatCSV)}
}

// ParseFormat validates a -o value.
func ParseFormat(s string) (Format, error) {
	switch Format(strings.ToLower(s)) {
	case FormatTable:
		return FormatTable, nil
	case FormatJSON:
		return FormatJSON, nil
	case FormatCSV:
		return FormatCSV, nil
	default:
		return "", fmt.Errorf("unknown output format %q (want one of: %s)", s, strings.Join(Formats(), ", "))
	}
}

// Table is a rendered view of a result set.
//
// Max caps a column's display width, in terminal cells; 0 means unlimited. It
// applies only to the table format, where a 147-character document description
// would otherwise destroy the layout. JSON and CSV always carry full values.
type Table struct {
	Headers []string
	Rows    [][]string
	Max     []int
}

// Render writes the result set in the requested format. raw is marshalled
// directly for JSON, so the full API response survives the table's column
// selection and truncation.
func Render(w io.Writer, f Format, t Table, raw any) error {
	switch f {
	case FormatJSON:
		return renderJSON(w, raw)
	case FormatCSV:
		return renderCSV(w, t)
	default:
		return renderTable(w, t)
	}
}

func renderJSON(w io.Writer, raw any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(raw)
}

func renderCSV(w io.Writer, t Table) error {
	cw := csv.NewWriter(w)
	if len(t.Headers) > 0 {
		if err := cw.Write(t.Headers); err != nil {
			return err
		}
	}
	if err := cw.WriteAll(t.Rows); err != nil {
		return err
	}
	cw.Flush()
	return cw.Error()
}

func renderTable(w io.Writer, t Table) error {
	if len(t.Rows) == 0 && len(t.Headers) == 0 {
		return nil
	}

	cols := len(t.Headers)
	for _, r := range t.Rows {
		cols = max(cols, len(r))
	}

	// Clip first so column widths are measured against what is actually printed.
	cells := make([][]string, 0, len(t.Rows)+1)
	if len(t.Headers) > 0 {
		cells = append(cells, clipRow(t.Headers, cols, t.Max))
	}
	for _, r := range t.Rows {
		cells = append(cells, clipRow(r, cols, t.Max))
	}

	widths := make([]int, cols)
	for _, row := range cells {
		for i, c := range row {
			widths[i] = max(widths[i], Width(c))
		}
	}

	var b strings.Builder
	for _, row := range cells {
		for i, c := range row {
			b.WriteString(c)
			if i == cols-1 {
				continue // no trailing padding on the last column
			}
			b.WriteString(strings.Repeat(" ", widths[i]-Width(c)+2))
		}
		b.WriteByte('\n')
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func clipRow(row []string, cols int, maxes []int) []string {
	out := make([]string, cols)
	for i := range cols {
		var cell string
		if i < len(row) {
			cell = sanitize(row[i])
		}
		if i < len(maxes) && maxes[i] > 0 {
			cell = Truncate(cell, maxes[i])
		}
		out[i] = cell
	}
	return out
}

// sanitize keeps embedded newlines and tabs from breaking the grid. EDINET
// document descriptions occasionally contain them.
func sanitize(s string) string {
	if !strings.ContainsAny(s, "\n\r\t") {
		return s
	}
	r := strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", " ")
	return r.Replace(s)
}

// Truncate shortens s to at most width terminal cells, marking the cut with an
// ellipsis.
func Truncate(s string, width int) string {
	if Width(s) <= width || width <= 0 {
		return s
	}
	var b strings.Builder
	used := 0
	for _, r := range s {
		rw := runeWidth(r)
		if used+rw > width-1 {
			break
		}
		b.WriteRune(r)
		used += rw
	}
	return b.String() + "…"
}

// Width returns how many terminal cells s occupies. Japanese filer names are
// full-width, so counting runes would misalign every column after the first.
func Width(s string) int {
	n := 0
	for _, r := range s {
		n += runeWidth(r)
	}
	return n
}

func runeWidth(r rune) int {
	switch {
	case r < 0x1100:
		return 1
	case r >= 0x1100 && r <= 0x115F, // Hangul Jamo
		r >= 0x2E80 && r <= 0x303E, // CJK radicals, Kangxi, punctuation
		r >= 0x3041 && r <= 0x33FF, // Hiragana, Katakana, CJK compatibility
		r >= 0x3400 && r <= 0x4DBF, // CJK Extension A
		r >= 0x4E00 && r <= 0x9FFF, // CJK Unified Ideographs
		r >= 0xA000 && r <= 0xA4CF, // Yi
		r >= 0xAC00 && r <= 0xD7A3, // Hangul syllables
		r >= 0xF900 && r <= 0xFAFF, // CJK compatibility ideographs
		r >= 0xFE30 && r <= 0xFE6F, // CJK compatibility forms
		r >= 0xFF00 && r <= 0xFF60, // Fullwidth forms
		r >= 0xFFE0 && r <= 0xFFE6,
		r >= 0x1F300 && r <= 0x1F64F, // emoji
		r >= 0x1F900 && r <= 0x1F9FF,
		r >= 0x20000 && r <= 0x2FFFD, // CJK Extension B+
		r >= 0x30000 && r <= 0x3FFFD:
		return 2
	default:
		return 1
	}
}
