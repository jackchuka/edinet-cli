// Package facts decodes the CSV bundle EDINET generates from a filing's XBRL.
//
// The bundle is a ZIP of files under XBRL_TO_CSV/. Despite the .csv extension
// they are tab-separated and encoded in UTF-16LE with a byte order mark, so
// handing one to encoding/csv unchanged yields nothing usable.
package facts

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

// Fact is one reported value from a filing.
type Fact struct {
	ElementID    string `json:"elementId"`
	Label        string `json:"label"`
	ContextID    string `json:"contextId"`
	RelativeYear string `json:"relativeYear"`
	Consolidated string `json:"consolidated"`
	Period       string `json:"period"`
	UnitID       string `json:"unitId"`
	Unit         string `json:"unit"`
	Value        string `json:"value"`

	// Source is the file within the bundle this fact came from, which
	// distinguishes the filing itself (jpcrp_*) from its audit report (jpaud_*).
	Source string `json:"source"`
}

// IsConsolidated reports whether this fact is a consolidated figure.
func (f Fact) IsConsolidated() bool { return strings.HasPrefix(f.Consolidated, "連結") }

// IsStandalone reports whether this fact is a non-consolidated figure.
func (f Fact) IsStandalone() bool { return strings.HasPrefix(f.Consolidated, "個別") }

// Japanese column headers used by the EDINET CSV bundle.
const (
	colElementID    = "要素ID"
	colLabel        = "項目名"
	colContextID    = "コンテキストID"
	colRelativeYear = "相対年度"
	colConsolidated = "連結・個別"
	colPeriod       = "期間・時点"
	colUnitID       = "ユニットID"
	colUnit         = "単位"
	colValue        = "値"
)

// ParseZip decodes every CSV in an EDINET CSV bundle.
func ParseZip(data []byte) ([]Fact, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("reading the CSV bundle: %w (was this downloaded with --file csv?)", err)
	}

	var names []string
	byName := map[string]*zip.File{}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || !strings.HasSuffix(strings.ToLower(f.Name), ".csv") {
			continue
		}
		names = append(names, f.Name)
		byName[f.Name] = f
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("no CSV files found in the bundle")
	}
	// Filing data (jpcrp_, jpsps_, ...) before the audit report (jpaud_), then
	// alphabetically, so output does not depend on ZIP ordering.
	sort.Slice(names, func(i, j int) bool {
		ai, aj := isAudit(names[i]), isAudit(names[j])
		if ai != aj {
			return aj
		}
		return names[i] < names[j]
	})

	var all []Fact
	for _, name := range names {
		rc, err := byName[name].Open()
		if err != nil {
			return nil, fmt.Errorf("opening %s: %w", name, err)
		}
		facts, err := parseCSV(rc, path.Base(name))
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", name, err)
		}
		all = append(all, facts...)
	}
	return all, nil
}

func isAudit(name string) bool {
	return strings.HasPrefix(strings.ToLower(path.Base(name)), "jpaud")
}

func parseCSV(r io.Reader, source string) ([]Fact, error) {
	// UseBOM honours the mark EDINET writes and falls back to little endian.
	dec := unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewDecoder()

	cr := csv.NewReader(transform.NewReader(r, dec))
	cr.Comma = '\t'
	cr.LazyQuotes = true    // text blocks contain unbalanced quotes
	cr.FieldsPerRecord = -1 // and occasional short rows

	header, err := cr.Read()
	if err != nil {
		if err == io.EOF {
			return nil, nil
		}
		return nil, err
	}

	idx := map[string]int{}
	for i, h := range header {
		idx[strings.TrimSpace(strings.TrimPrefix(h, "\ufeff"))] = i
	}
	if _, ok := idx[colElementID]; !ok {
		return nil, fmt.Errorf("no %q column found; headers were %q (expected a UTF-16LE tab-separated file)",
			colElementID, header)
	}

	get := func(row []string, col string) string {
		i, ok := idx[col]
		if !ok || i >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[i])
	}

	var facts []Fact
	for {
		row, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		f := Fact{
			ElementID:    get(row, colElementID),
			Label:        get(row, colLabel),
			ContextID:    get(row, colContextID),
			RelativeYear: get(row, colRelativeYear),
			Consolidated: get(row, colConsolidated),
			Period:       get(row, colPeriod),
			UnitID:       get(row, colUnitID),
			Unit:         get(row, colUnit),
			Value:        get(row, colValue),
			Source:       source,
		}
		if f.ElementID == "" {
			continue
		}
		facts = append(facts, f)
	}
	return facts, nil
}

// Filter narrows a fact set. Zero values mean "no constraint".
type Filter struct {
	// Grep matches the label or the value, case-insensitively.
	Grep string
	// Element matches the element ID, case-insensitively, as a substring so
	// "NetSales" finds "jpcrp_cor:NetSalesSummaryOfBusinessResults".
	Element string
	// Consolidated and Standalone select 連結 or 個別 rows.
	Consolidated bool
	Standalone   bool
	// Period matches 相対年度, e.g. "当期".
	Period string
	// NumericOnly drops narrative text blocks, keeping reported figures.
	NumericOnly bool
}

// Apply returns the facts matching f, preserving order.
func (fl Filter) Apply(facts []Fact) []Fact {
	grep := strings.ToLower(fl.Grep)
	element := strings.ToLower(fl.Element)

	var out []Fact
	for _, f := range facts {
		if grep != "" &&
			!strings.Contains(strings.ToLower(f.Label), grep) &&
			!strings.Contains(strings.ToLower(f.Value), grep) {
			continue
		}
		if element != "" && !strings.Contains(strings.ToLower(f.ElementID), element) {
			continue
		}
		if fl.Consolidated && !f.IsConsolidated() {
			continue
		}
		if fl.Standalone && !f.IsStandalone() {
			continue
		}
		if fl.Period != "" && !strings.Contains(f.RelativeYear, fl.Period) {
			continue
		}
		if fl.NumericOnly && !isNumeric(f.Value) {
			continue
		}
		out = append(out, f)
	}
	return out
}

// isNumeric reports whether a value is a reported figure rather than narrative
// text. A sign is only accepted in leading position, so dates such as
// "2026-03-31" are correctly excluded.
func isNumeric(s string) bool {
	s = strings.ReplaceAll(s, ",", "")
	s = strings.TrimPrefix(s, "-")
	s = strings.TrimPrefix(s, "+")
	if s == "" {
		return false
	}

	dots := 0
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
		case r == '.':
			dots++
			if dots > 1 {
				return false
			}
		default:
			return false
		}
	}
	return true
}
