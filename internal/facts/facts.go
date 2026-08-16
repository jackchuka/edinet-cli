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

// Filing identifies the document a fact came from.
//
// Every value comes from the bundle's own jpdei_cor block, so labelling a row
// costs no extra request. SecCode is EDINET's 5-digit securities code with its
// trailing zero, left unmodified because that is the form J-Quants uses as a
// join key; the 4-digit ticker is a display concern.
type Filing struct {
	DocID      string `json:"docId"`
	EdinetCode string `json:"edinetCode"`
	SecCode    string `json:"secCode"`
	FilerName  string `json:"filerName"`

	FiscalYearStart string `json:"fiscalYearStart"`
	FiscalYearEnd   string `json:"fiscalYearEnd"`
	// PeriodEnd is the end of the period being reported on. It equals
	// FiscalYearEnd in an annual report but not in a semiannual one, so the two
	// are kept apart: folded together, a mixed result set could not be joined
	// against a financial time series without guessing.
	PeriodEnd string `json:"periodEnd"`

	DocumentType string `json:"documentType"`
	// AccountingStandard is Japan GAAP, IFRS, or US GAAP. Element IDs differ
	// across standards and this tool deliberately keeps no mapping table, so
	// this is passed through for callers to branch on themselves.
	AccountingStandard string `json:"accountingStandard"`
}

// Row is one reported value together with the filing it came from. The embedded
// structs are flattened by encoding/json, so every output row carries its own
// join keys.
type Row struct {
	Filing
	Fact
}

// DEI element IDs identifying the filing itself.
const (
	deiEdinetCode         = "jpdei_cor:EDINETCodeDEI"
	deiSecurityCode       = "jpdei_cor:SecurityCodeDEI"
	deiFilerName          = "jpdei_cor:FilerNameInJapaneseDEI"
	deiFiscalYearStart    = "jpdei_cor:CurrentFiscalYearStartDateDEI"
	deiFiscalYearEnd      = "jpdei_cor:CurrentFiscalYearEndDateDEI"
	deiPeriodEnd          = "jpdei_cor:CurrentPeriodEndDateDEI"
	deiDocumentType       = "jpdei_cor:DocumentTypeDEI"
	deiAccountingStandard = "jpdei_cor:AccountingStandardsDEI"
)

// DEI assembles the filing identity from a parsed bundle.
//
// Facts from the audit report are skipped: jpaud_* files carry their own DEI
// block describing the auditor's document rather than the filing.
func DEI(all []Fact) Filing {
	var f Filing
	into := map[string]*string{
		deiEdinetCode:         &f.EdinetCode,
		deiSecurityCode:       &f.SecCode,
		deiFilerName:          &f.FilerName,
		deiFiscalYearStart:    &f.FiscalYearStart,
		deiFiscalYearEnd:      &f.FiscalYearEnd,
		deiPeriodEnd:          &f.PeriodEnd,
		deiDocumentType:       &f.DocumentType,
		deiAccountingStandard: &f.AccountingStandard,
	}

	for _, fact := range all {
		if fact.Value == "" || isAudit(fact.Source) {
			continue
		}
		if p, ok := into[fact.ElementID]; ok && *p == "" {
			*p = fact.Value
		}
	}

	// Annual reports omit the period end because it is the fiscal year end.
	if f.PeriodEnd == "" {
		f.PeriodEnd = f.FiscalYearEnd
	}
	return f
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
