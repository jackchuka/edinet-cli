// Package codelist resolves company names, tickers, and corporate numbers to
// EDINET codes.
//
// The EDINET API itself offers no company lookup at all: every query is by
// EDINET code. The mapping lives in a separate Shift-JIS ZIP published on the
// disclosure site, which this package downloads, decodes, and caches.
package codelist

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/transform"
)

// SourceURL is the FSA's permanent link to the EDINET code list.
const SourceURL = "https://disclosure2dl.edinet-fsa.go.jp/searchdocument/codelist/Edinetcode.zip"

// DefaultMaxAge is how long a cached list is used before it is refreshed. The
// list changes when companies are added or renamed, which is not urgent.
const DefaultMaxAge = 7 * 24 * time.Hour

// Entry is one filer in the EDINET code list.
type Entry struct {
	EdinetCode    string `json:"edinetCode"`
	Type          string `json:"type"`
	Listed        string `json:"listed"`
	Consolidated  string `json:"consolidated"`
	Capital       string `json:"capital"`
	FiscalYearEnd string `json:"fiscalYearEnd"`
	Name          string `json:"name"`
	NameEn        string `json:"nameEn"`
	NameKana      string `json:"nameKana"`
	Address       string `json:"address"`
	Industry      string `json:"industry"`
	SecCode       string `json:"secCode"`
	JCN           string `json:"jcn"`
}

// Ticker returns the 4-digit ticker for a listed company. EDINET stores a
// 5-digit securities code with a trailing 0 (Toyota is 72030, not 7203).
func (e Entry) Ticker() string {
	if len(e.SecCode) == 5 && strings.HasSuffix(e.SecCode, "0") {
		return e.SecCode[:4]
	}
	return e.SecCode
}

// Store manages the local copy of the code list.
type Store struct {
	Path       string
	URL        string
	HTTPClient *http.Client
}

// NewStore returns a Store backed by the user's cache directory.
func NewStore() (*Store, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return nil, fmt.Errorf("locating cache directory: %w", err)
	}
	return &Store{
		Path:       filepath.Join(dir, "edinet-cli", "codelist.json"),
		URL:        SourceURL,
		HTTPClient: &http.Client{Timeout: 120 * time.Second},
	}, nil
}

func (s *Store) url() string {
	if s.URL != "" {
		return s.URL
	}
	return SourceURL
}

func (s *Store) client() *http.Client {
	if s.HTTPClient != nil {
		return s.HTTPClient
	}
	return http.DefaultClient
}

// Age reports how long ago the cache was written. It returns false if there is
// no cache yet.
func (s *Store) Age() (time.Duration, bool) {
	fi, err := os.Stat(s.Path)
	if err != nil {
		return 0, false
	}
	return time.Since(fi.ModTime()), true
}

// Load returns the cached code list, downloading it if the cache is missing or
// older than maxAge. A maxAge of 0 uses DefaultMaxAge.
func (s *Store) Load(ctx context.Context, maxAge time.Duration) ([]Entry, error) {
	if maxAge == 0 {
		maxAge = DefaultMaxAge
	}
	if age, ok := s.Age(); ok && age < maxAge {
		entries, err := s.readCache()
		if err == nil {
			return entries, nil
		}
		// A corrupt cache is not worth surfacing; re-downloading fixes it.
	}
	return s.Sync(ctx)
}

// Sync downloads the code list and replaces the cache.
func (s *Store) Sync(ctx context.Context) ([]Entry, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url(), nil)
	if err != nil {
		return nil, err
	}
	res, err := s.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("downloading the EDINET code list: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("downloading the EDINET code list: HTTP %d", res.StatusCode)
	}

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("downloading the EDINET code list: %w", err)
	}

	entries, err := Parse(body)
	if err != nil {
		return nil, err
	}
	if err := s.writeCache(entries); err != nil {
		return nil, err
	}
	return entries, nil
}

func (s *Store) readCache() ([]Entry, error) {
	b, err := os.ReadFile(s.Path)
	if err != nil {
		return nil, err
	}
	var entries []Entry
	if err := json.Unmarshal(b, &entries); err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("cached code list is empty")
	}
	return entries, nil
}

func (s *Store) writeCache(entries []Entry) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return fmt.Errorf("creating cache directory: %w", err)
	}
	b, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	// Write via a temp file so an interrupted run cannot leave a half-written
	// cache that later looks valid.
	tmp := s.Path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("writing cache: %w", err)
	}
	if err := os.Rename(tmp, s.Path); err != nil {
		return fmt.Errorf("writing cache: %w", err)
	}
	return nil
}

// Parse decodes the code list ZIP.
//
// The CSV inside is Shift-JIS and carries a metadata line above the real header
// row, so it cannot be handed to encoding/csv as-is.
func Parse(zipBytes []byte) ([]Entry, error) {
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return nil, fmt.Errorf("reading the code list archive: %w", err)
	}

	var csvFile *zip.File
	for _, f := range zr.File {
		if strings.HasSuffix(strings.ToLower(f.Name), ".csv") {
			csvFile = f
			break
		}
	}
	if csvFile == nil {
		return nil, fmt.Errorf("no CSV found inside the code list archive")
	}

	rc, err := csvFile.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	decoded := transform.NewReader(rc, japanese.ShiftJIS.NewDecoder())
	r := csv.NewReader(decoded)
	r.FieldsPerRecord = -1
	r.LazyQuotes = true

	rows, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parsing the code list CSV: %w", err)
	}
	if len(rows) < 3 {
		return nil, fmt.Errorf("code list CSV has %d rows, expected a metadata row, a header row, and data", len(rows))
	}

	// Row 0 is metadata ("ダウンロードファイル作成日..."), row 1 is the header.
	const columns = 13
	entries := make([]Entry, 0, len(rows)-2)
	for _, row := range rows[2:] {
		if len(row) < columns {
			continue // trailing blank line, or a row EDINET truncated
		}
		e := Entry{
			EdinetCode:    strings.TrimSpace(row[0]),
			Type:          strings.TrimSpace(row[1]),
			Listed:        strings.TrimSpace(row[2]),
			Consolidated:  strings.TrimSpace(row[3]),
			Capital:       strings.TrimSpace(row[4]),
			FiscalYearEnd: strings.TrimSpace(row[5]),
			Name:          strings.TrimSpace(row[6]),
			NameEn:        strings.TrimSpace(row[7]),
			NameKana:      strings.TrimSpace(row[8]),
			Address:       strings.TrimSpace(row[9]),
			Industry:      strings.TrimSpace(row[10]),
			SecCode:       strings.TrimSpace(row[11]),
			JCN:           strings.TrimSpace(row[12]),
		}
		if e.EdinetCode == "" {
			continue
		}
		entries = append(entries, e)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("code list CSV contained no usable rows")
	}
	return entries, nil
}

// Search returns entries matching query, most precise match first.
//
// A query is tried as an EDINET code, a corporate number, and a ticker before
// falling back to a substring match on the Japanese, English, and kana names.
func Search(entries []Entry, query string) []Entry {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil
	}
	upper := strings.ToUpper(q)
	lower := strings.ToLower(q)

	var exact, prefix, contains []Entry
	for _, e := range entries {
		switch {
		case strings.EqualFold(e.EdinetCode, q),
			e.JCN != "" && e.JCN == q,
			e.SecCode != "" && e.SecCode == q,
			e.Ticker() != "" && e.Ticker() == q:
			exact = append(exact, e)
		case strings.HasPrefix(e.Name, q),
			strings.HasPrefix(strings.ToUpper(e.NameEn), upper),
			strings.HasPrefix(e.NameKana, q):
			prefix = append(prefix, e)
		case strings.Contains(e.Name, q),
			strings.Contains(strings.ToLower(e.NameEn), lower),
			strings.Contains(e.NameKana, q):
			contains = append(contains, e)
		}
	}
	return append(append(exact, prefix...), contains...)
}

// Resolve returns the single EDINET code a query identifies. It fails when the
// query is ambiguous, listing the candidates so the user can narrow it down.
func Resolve(entries []Entry, query string) (Entry, error) {
	matches := Search(entries, query)
	switch len(matches) {
	case 0:
		return Entry{}, fmt.Errorf("no company matches %q (try `edinet company search %s`)", query, query)
	case 1:
		return matches[0], nil
	}

	// One unambiguous winner is still possible if only one match is exact.
	if len(matches) > 1 && matches[0].Name == query {
		if matches[1].Name != query {
			return matches[0], nil
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%q matches %d companies:\n", query, len(matches))
	for i, e := range matches {
		if i == 5 {
			fmt.Fprintf(&b, "  ... and %d more\n", len(matches)-5)
			break
		}
		fmt.Fprintf(&b, "  %s  %s\n", e.EdinetCode, e.Name)
	}
	b.WriteString("narrow the query, or pass --edinet-code")
	return Entry{}, fmt.Errorf("%s", b.String())
}
