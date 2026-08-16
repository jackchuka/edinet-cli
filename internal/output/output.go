// Package output renders result sets as a terminal table, JSON, or CSV.
package output

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
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

// TableSpec describes the human-readable view of a record type.
//
// Max caps a column's display width, in terminal cells; 0 means unlimited. It
// applies only to the table format, where a 147-character document description
// would otherwise destroy the layout. JSON and CSV always carry full values.
type TableSpec[T any] struct {
	Headers []string
	Max     []int
	// Row renders one record as table cells. It is consulted for the table
	// format only, because json and csv are derived from the record itself.
	Row func(T) []string
}

// Stream writes records one at a time.
//
// json and csv write through, so a batch of filings never has to fit in memory
// at once. table buffers, because column widths cannot be known until the last
// row has been seen; that is acceptable because the table format is for
// interactive use, where result sets are small and --limit is available.
type Stream[T any] interface {
	Write(T) error
	Close() error
}

// NewStream returns a Stream writing f to w.
//
// json and csv buffer through bufio rather than writing a syscall per record,
// which matters once a batch runs to millions of rows. The buffer is fixed
// size, so the memory guarantee still holds.
func NewStream[T any](w io.Writer, f Format, spec TableSpec[T]) Stream[T] {
	switch f {
	case FormatJSON:
		return &jsonStream[T]{w: bufio.NewWriter(w)}
	case FormatCSV:
		return &csvStream[T]{w: csv.NewWriter(w)}
	default:
		return &tableStream[T]{w: w, spec: spec}
	}
}

// Render writes every record and closes the stream. It is the convenience form
// for callers that already hold the whole result set.
func Render[T any](w io.Writer, f Format, spec TableSpec[T], recs []T) error {
	s := NewStream(w, f, spec)
	for _, r := range recs {
		if err := s.Write(r); err != nil {
			return err
		}
	}
	return s.Close()
}

type jsonStream[T any] struct {
	w   *bufio.Writer
	n   int
	err error
}

func (s *jsonStream[T]) Write(rec T) error {
	if s.err != nil {
		return s.err
	}

	sep := ",\n"
	if s.n == 0 {
		sep = "[\n"
	}
	if _, s.err = io.WriteString(s.w, sep); s.err != nil {
		return s.err
	}

	b, err := marshalRecord(rec)
	if err != nil {
		s.err = err
		return err
	}
	if _, s.err = s.w.Write(b); s.err != nil {
		return s.err
	}
	s.n++
	return nil
}

func (s *jsonStream[T]) Close() error {
	if s.err != nil {
		return s.err
	}
	// An empty result must still decode as an array, so pipelines do not have
	// to special-case "no results".
	closing := "\n]\n"
	if s.n == 0 {
		closing = "[]\n"
	}
	if _, err := io.WriteString(s.w, closing); err != nil {
		return err
	}
	return s.w.Flush()
}

// marshalRecord renders one array element, indented to sit inside the array
// framing the stream writes around it.
func marshalRecord(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("  ", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return append([]byte("  "), bytes.TrimRight(buf.Bytes(), "\n")...), nil
}

type csvStream[T any] struct {
	w      *csv.Writer
	header bool
	err    error
}

func (s *csvStream[T]) Write(rec T) error {
	if s.err != nil {
		return s.err
	}
	if s.err = s.writeHeader(); s.err != nil {
		return s.err
	}
	s.err = s.w.Write(valuesOf(rec))
	return s.err
}

func (s *csvStream[T]) writeHeader() error {
	if s.header {
		return nil
	}
	s.header = true
	return s.w.Write(fieldsOf(reflect.TypeFor[T]()))
}

func (s *csvStream[T]) Close() error {
	if s.err != nil {
		return s.err
	}
	// Headers are written even with no rows, so the shape of the output does
	// not depend on whether anything matched.
	if err := s.writeHeader(); err != nil {
		return err
	}
	s.w.Flush()
	return s.w.Error()
}

type tableStream[T any] struct {
	w    io.Writer
	spec TableSpec[T]
	rows [][]string
}

func (s *tableStream[T]) Write(rec T) error {
	s.rows = append(s.rows, s.spec.Row(rec))
	return nil
}

func (s *tableStream[T]) Close() error {
	// A header with no rows under it is not a table, just noise above whatever
	// message the caller prints about the empty result; docs list and company
	// search never call Render at all in that case, so a streaming caller like
	// facts must reach the same output by skipping the render here instead.
	if len(s.rows) == 0 {
		return nil
	}
	return renderTable(s.w, s.spec.Headers, s.rows, s.spec.Max)
}

func renderTable(w io.Writer, headers []string, rows [][]string, maxes []int) error {
	cols := len(headers)
	for _, r := range rows {
		cols = max(cols, len(r))
	}

	// Clip first so column widths are measured against what is actually printed.
	cells := make([][]string, 0, len(rows)+1)
	if len(headers) > 0 {
		cells = append(cells, clipRow(headers, cols, maxes))
	}
	for _, r := range rows {
		cells = append(cells, clipRow(r, cols, maxes))
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
