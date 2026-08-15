package codelist

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/transform"
)

// buildFixture produces a ZIP shaped like Edinetcode.zip: one CSV, encoded in
// Shift-JIS, with a metadata line above the header row.
func buildFixture(t *testing.T) []byte {
	t.Helper()

	const csvUTF8 = `ダウンロードファイル作成日,2026/08/15,,,,,,,,,,,
ＥＤＩＮＥＴコード,提出者種別,上場区分,連結の有無,資本金,決算日,提出者名,提出者名（英字）,提出者名（ヨミ）,所在地,提出者業種,証券コード,提出者法人番号
E02144,内国法人・組合,上場,有,635401000000,3月31日,トヨタ自動車株式会社,TOYOTA MOTOR CORPORATION,トヨタジドウシャ,愛知県豊田市,輸送用機器,72030,1180301018771
E01777,内国法人・組合,上場,有,258740000000,3月31日,ソニーグループ株式会社,SONY GROUP CORPORATION,ソニーグループ,東京都港区,電気機器,67580,7010001027733
E00001,内国法人・組合,非上場,無,100000000,12月31日,架空商事株式会社,KAKU SHOJI CO.,LTD.,カクウショウジ,東京都千代田区,卸売業,,9999999999999
`

	sjis, _, err := transform.Bytes(japanese.ShiftJIS.NewEncoder(), []byte(csvUTF8))
	if err != nil {
		t.Fatalf("encoding fixture to Shift-JIS: %v", err)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("EdinetcodeDlInfo.csv")
	if err != nil {
		t.Fatalf("creating zip entry: %v", err)
	}
	if _, err := w.Write(sjis); err != nil {
		t.Fatalf("writing zip entry: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("closing zip: %v", err)
	}
	return buf.Bytes()
}

func TestParseDecodesShiftJIS(t *testing.T) {
	entries, err := Parse(buildFixture(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3 (metadata and header rows must be skipped)", len(entries))
	}

	toyota := entries[0]
	if toyota.EdinetCode != "E02144" {
		t.Errorf("edinetCode = %q", toyota.EdinetCode)
	}
	if toyota.Name != "トヨタ自動車株式会社" {
		t.Errorf("name = %q; Shift-JIS decoding failed", toyota.Name)
	}
	if toyota.NameEn != "TOYOTA MOTOR CORPORATION" {
		t.Errorf("nameEn = %q", toyota.NameEn)
	}
	if toyota.JCN != "1180301018771" {
		t.Errorf("jcn = %q", toyota.JCN)
	}
	if toyota.Industry != "輸送用機器" {
		t.Errorf("industry = %q", toyota.Industry)
	}
}

// EDINET stores a 5-digit securities code; people type the 4-digit ticker.
func TestTicker(t *testing.T) {
	tests := []struct {
		secCode string
		want    string
	}{
		{"72030", "7203"},
		{"67580", "6758"},
		{"", ""},
		{"1234", "1234"},
	}
	for _, tt := range tests {
		if got := (Entry{SecCode: tt.secCode}).Ticker(); got != tt.want {
			t.Errorf("Ticker(%q) = %q, want %q", tt.secCode, got, tt.want)
		}
	}
}

func TestSearch(t *testing.T) {
	entries, err := Parse(buildFixture(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	tests := []struct {
		name  string
		query string
		want  string // expected EDINET code of the first match
		count int
	}{
		{"edinet code", "E02144", "E02144", 1},
		{"edinet code lowercase", "e02144", "E02144", 1},
		{"4-digit ticker", "7203", "E02144", 1},
		{"5-digit sec code", "72030", "E02144", 1},
		{"corporate number", "7010001027733", "E01777", 1},
		{"japanese name prefix", "トヨタ", "E02144", 1},
		{"japanese substring", "自動車", "E02144", 1},
		{"english name", "sony", "E01777", 1},
		{"kana", "ソニー", "E01777", 1},
		{"common suffix matches several", "株式会社", "E02144", 3},
		{"no match", "does-not-exist", "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Search(entries, tt.query)
			if len(got) != tt.count {
				t.Fatalf("got %d matches, want %d", len(got), tt.count)
			}
			if tt.count > 0 && got[0].EdinetCode != tt.want {
				t.Errorf("first match = %s, want %s", got[0].EdinetCode, tt.want)
			}
		})
	}
}

func TestResolveRejectsAmbiguity(t *testing.T) {
	entries, err := Parse(buildFixture(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	e, err := Resolve(entries, "7203")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if e.EdinetCode != "E02144" {
		t.Errorf("resolved to %s", e.EdinetCode)
	}

	_, err = Resolve(entries, "株式会社")
	if err == nil {
		t.Fatal("an ambiguous query must not silently pick a company")
	}
	if !strings.Contains(err.Error(), "E02144") {
		t.Errorf("ambiguity error should list candidates, got: %v", err)
	}

	if _, err := Resolve(entries, "nonexistent"); err == nil {
		t.Fatal("expected an error for no matches")
	}
}

func TestStoreCachesAndReuses(t *testing.T) {
	fixture := buildFixture(t)
	var downloads int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downloads++
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(fixture)
	}))
	defer srv.Close()

	s := &Store{
		Path:       filepath.Join(t.TempDir(), "codelist.json"),
		URL:        srv.URL,
		HTTPClient: srv.Client(),
	}

	first, err := s.Load(context.Background(), time.Hour)
	if err != nil {
		t.Fatalf("first Load: %v", err)
	}
	if len(first) != 3 {
		t.Fatalf("got %d entries", len(first))
	}
	if downloads != 1 {
		t.Fatalf("made %d downloads, want 1", downloads)
	}

	// A fresh cache must be reused rather than re-downloaded.
	second, err := s.Load(context.Background(), time.Hour)
	if err != nil {
		t.Fatalf("second Load: %v", err)
	}
	if downloads != 1 {
		t.Errorf("made %d downloads, want the cache to be reused", downloads)
	}
	if len(second) != len(first) || second[0].Name != first[0].Name {
		t.Error("cached entries differ from downloaded entries")
	}

	// An expired cache must be refreshed.
	if _, err := s.Load(context.Background(), time.Nanosecond); err != nil {
		t.Fatalf("third Load: %v", err)
	}
	if downloads != 2 {
		t.Errorf("made %d downloads, want an expired cache to refresh", downloads)
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	if _, err := Parse([]byte("not a zip")); err == nil {
		t.Fatal("expected an error for a non-zip payload")
	}
}
