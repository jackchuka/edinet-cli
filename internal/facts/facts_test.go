package facts

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"

	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

// encodeUTF16LE produces the byte layout EDINET actually ships: UTF-16LE with a
// leading byte order mark.
func encodeUTF16LE(t *testing.T, s string) []byte {
	t.Helper()
	enc := unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewEncoder()
	b, _, err := transform.Bytes(enc, []byte(s))
	if err != nil {
		t.Fatalf("encoding fixture to UTF-16LE: %v", err)
	}
	return b
}

const header = "要素ID\t項目名\tコンテキストID\t相対年度\t連結・個別\t期間・時点\tユニットID\t単位\t値\n"

func buildBundle(t *testing.T) []byte {
	t.Helper()

	main := header +
		"jpcrp_cor:NetSales\t売上高\tCurrentYearDuration_ConsolidatedMember\t当期\t連結\t期間\tJPY\t円\t45095325000000\n" +
		"jpcrp_cor:NetSales\t売上高\tPrior1YearDuration_ConsolidatedMember\t前期\t連結\t期間\tJPY\t円\t37154298000000\n" +
		"jpcrp_cor:NetSales\t売上高\tCurrentYearDuration_NonConsolidatedMember\t当期\t個別\t期間\tJPY\t円\t15000000000000\n" +
		"jpcrp_cor:NumberOfEmployees\t従業員数\tCurrentYearInstant\t当期\t連結\t時点\tpure\t人\t380793\n" +
		"jpcrp_cor:BusinessRisksTextBlock\t事業等のリスク\tCurrentYearDuration\t当期\t\t期間\t\t\t<p>リスクの説明です。</p>\n"

	audit := header +
		"jpaud_cor:OpinionHeading\t監査意見\tCurrentYearDuration\t当期\t連結\t期間\t\t\t無限定適正意見\n"

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range map[string]string{
		"XBRL_TO_CSV/jpaud_202603-asr-001_E02144-000_2026-03-31_01_2026-06-20.csv": audit,
		"XBRL_TO_CSV/jpcrp030000-asr-001_E02144-000_2026-03-31_01_2026-06-20.csv":  main,
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("creating zip entry: %v", err)
		}
		if _, err := w.Write(encodeUTF16LE(t, content)); err != nil {
			t.Fatalf("writing zip entry: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("closing zip: %v", err)
	}
	return buf.Bytes()
}

func TestParseZipDecodesUTF16TSV(t *testing.T) {
	got, err := ParseZip(buildBundle(t))
	if err != nil {
		t.Fatalf("ParseZip: %v", err)
	}
	if len(got) != 6 {
		t.Fatalf("got %d facts, want 6", len(got))
	}

	first := got[0]
	if first.ElementID != "jpcrp_cor:NetSales" {
		t.Errorf("elementID = %q", first.ElementID)
	}
	if first.Label != "売上高" {
		t.Errorf("label = %q; UTF-16LE decoding failed", first.Label)
	}
	if first.Value != "45095325000000" {
		t.Errorf("value = %q", first.Value)
	}
	if first.Unit != "円" {
		t.Errorf("unit = %q", first.Unit)
	}
	if !strings.HasPrefix(first.Source, "jpcrp") {
		t.Errorf("source = %q; filing data should sort before the audit report", first.Source)
	}

	// The audit report must come last regardless of ZIP entry order.
	if !strings.HasPrefix(got[len(got)-1].Source, "jpaud") {
		t.Errorf("last fact came from %q, want the audit report", got[len(got)-1].Source)
	}
}

func TestParseZipRejectsNonBundle(t *testing.T) {
	if _, err := ParseZip([]byte("not a zip")); err == nil {
		t.Fatal("expected an error for a non-zip payload")
	}
}

// A UTF-8 bundle would decode to mojibake rather than failing outright, so the
// missing 要素ID column is what has to be caught.
func TestParseCSVRejectsWrongEncoding(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("XBRL_TO_CSV/jpcrp.csv")
	_, _ = w.Write([]byte(header + "a\tb\tc\td\te\tf\tg\th\ti\n")) // UTF-8, not UTF-16
	_ = zw.Close()

	_, err := ParseZip(buf.Bytes())
	if err == nil {
		t.Fatal("expected an error for a wrongly encoded bundle")
	}
	if !strings.Contains(err.Error(), "要素ID") {
		t.Errorf("error should name the missing column, got: %v", err)
	}
}

func TestFilter(t *testing.T) {
	all, err := ParseZip(buildBundle(t))
	if err != nil {
		t.Fatalf("ParseZip: %v", err)
	}

	tests := []struct {
		name   string
		filter Filter
		want   int
	}{
		{"no constraint", Filter{}, 6},
		{"grep label", Filter{Grep: "売上高"}, 3},
		{"grep is case-insensitive on values", Filter{Grep: "リスク"}, 1},
		{"element substring", Filter{Element: "NetSales"}, 3},
		{"element is case-insensitive", Filter{Element: "netsales"}, 3},
		{"consolidated only", Filter{Element: "NetSales", Consolidated: true}, 2},
		{"standalone only", Filter{Element: "NetSales", Standalone: true}, 1},
		{"current period", Filter{Element: "NetSales", Period: "当期"}, 2},
		{"numeric only drops text blocks", Filter{NumericOnly: true}, 4},
		{"combined", Filter{Element: "NetSales", Consolidated: true, Period: "当期"}, 1},
		{"no match", Filter{Grep: "存在しない項目"}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := len(tt.filter.Apply(all)); got != tt.want {
				t.Errorf("got %d facts, want %d", got, tt.want)
			}
		})
	}
}

func TestIsNumeric(t *testing.T) {
	numeric := []string{"123", "-4.5", "1,234", "+7"}
	for _, s := range numeric {
		if !isNumeric(s) {
			t.Errorf("isNumeric(%q) = false, want true", s)
		}
	}
	notNumeric := []string{"", "無限定適正意見", "<p>text</p>", "2026-03-31"}
	for _, s := range notNumeric {
		if isNumeric(s) {
			t.Errorf("isNumeric(%q) = true, want false", s)
		}
	}
}

// buildBundleWithDEI mirrors what EDINET actually ships: the filing's own CSV
// carries a jpdei_cor block identifying the document, and the audit report
// carries its own. Map iteration order is randomized here, but ParseZip
// deterministically normalizes the order before parsing, so this test depends
// on that normalization, not on physical ZIP entry order.
func buildBundleWithDEI(t *testing.T, dei string) []byte {
	t.Helper()

	main := header + dei +
		"jpcrp_cor:NetSales\t売上高\tCurrentYearDuration\t当期\t連結\t期間\tJPY\t円\t45095325000000\n"

	audit := header +
		"jpdei_cor:EDINETCodeDEI\tEDINETコード\tFilingDateInstant\t当期\t\t時点\t\t\tE99999\n" +
		"jpaud_cor:OpinionHeading\t監査意見\tCurrentYearDuration\t当期\t連結\t期間\t\t\t無限定適正意見\n"

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range map[string]string{
		"XBRL_TO_CSV/jpaud_202603-asr-001_E02144-000_2026-03-31_01_2026-06-20.csv": audit,
		"XBRL_TO_CSV/jpcrp030000-asr-001_E02144-000_2026-03-31_01_2026-06-20.csv":  main,
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("creating zip entry: %v", err)
		}
		if _, err := w.Write(encodeUTF16LE(t, content)); err != nil {
			t.Fatalf("writing zip entry: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("closing zip: %v", err)
	}
	return buf.Bytes()
}

func deiRow(element, value string) string {
	return element + "\t項目\tFilingDateInstant\t当期\t\t時点\t\t\t" + value + "\n"
}

func TestDEI(t *testing.T) {
	listed := deiRow("jpdei_cor:EDINETCodeDEI", "E02144") +
		deiRow("jpdei_cor:SecurityCodeDEI", "72030") +
		deiRow("jpdei_cor:FilerNameInJapaneseDEI", "トヨタ自動車株式会社") +
		deiRow("jpdei_cor:CurrentFiscalYearStartDateDEI", "2025-04-01") +
		deiRow("jpdei_cor:CurrentFiscalYearEndDateDEI", "2026-03-31") +
		deiRow("jpdei_cor:DocumentTypeDEI", "有価証券報告書") +
		deiRow("jpdei_cor:AccountingStandardsDEI", "IFRS")

	// A半期報告書 reports a period end that is not the fiscal year end. Folding
	// them into one field would make the two indistinguishable.
	semi := listed + deiRow("jpdei_cor:CurrentPeriodEndDateDEI", "2025-09-30")

	// Investment corporations and unlisted filers have no securities code.
	unlisted := deiRow("jpdei_cor:EDINETCodeDEI", "E12345") +
		deiRow("jpdei_cor:FilerNameInJapaneseDEI", "非上場株式会社") +
		deiRow("jpdei_cor:CurrentFiscalYearEndDateDEI", "2026-03-31")

	tests := []struct {
		name string
		dei  string
		want Filing
	}{
		{
			name: "annual report",
			dei:  listed,
			want: Filing{
				EdinetCode:         "E02144",
				SecCode:            "72030",
				FilerName:          "トヨタ自動車株式会社",
				FiscalYearStart:    "2025-04-01",
				FiscalYearEnd:      "2026-03-31",
				PeriodEnd:          "2026-03-31",
				DocumentType:       "有価証券報告書",
				AccountingStandard: "IFRS",
			},
		},
		{
			name: "semiannual report keeps period end distinct",
			dei:  semi,
			want: Filing{
				EdinetCode:         "E02144",
				SecCode:            "72030",
				FilerName:          "トヨタ自動車株式会社",
				FiscalYearStart:    "2025-04-01",
				FiscalYearEnd:      "2026-03-31",
				PeriodEnd:          "2025-09-30",
				DocumentType:       "有価証券報告書",
				AccountingStandard: "IFRS",
			},
		},
		{
			name: "unlisted filer has no securities code",
			dei:  unlisted,
			want: Filing{
				EdinetCode:    "E12345",
				FilerName:     "非上場株式会社",
				FiscalYearEnd: "2026-03-31",
				PeriodEnd:     "2026-03-31",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			all, err := ParseZip(buildBundleWithDEI(t, tt.dei))
			if err != nil {
				t.Fatalf("ParseZip: %v", err)
			}
			got := DEI(all)
			if got != tt.want {
				t.Errorf("DEI() = %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

// The audit report carries its own DEI block describing the auditor's document.
// Taking values from it would mislabel the filing.
func TestDEIIgnoresAuditReport(t *testing.T) {
	dei := deiRow("jpdei_cor:EDINETCodeDEI", "E02144")
	all, err := ParseZip(buildBundleWithDEI(t, dei))
	if err != nil {
		t.Fatalf("ParseZip: %v", err)
	}
	if got := DEI(all).EdinetCode; got != "E02144" {
		t.Errorf("edinetCode = %q, want E02144 (E99999 comes from the audit report)", got)
	}
}

// TestDEIIgnoresAuditReportDirect calls DEI directly with audit facts placed
// before filing facts, bypassing ParseZip's sort. This test will fail if the
// isAudit(fact.Source) check in DEI is removed, verifying that the audit-skip
// rule is actually being enforced (not just happening to work due to sort order).
func TestDEIIgnoresAuditReportDirect(t *testing.T) {
	all := []Fact{
		// Audit fact comes first, with conflicting EDINET code
		{
			ElementID: "jpdei_cor:EDINETCodeDEI",
			Value:     "E99999",
			Source:    "jpaud_202603-asr-001_E02144-000_2026-03-31_01_2026-06-20.csv",
		},
		// Filing fact comes second, with correct EDINET code
		{
			ElementID: "jpdei_cor:EDINETCodeDEI",
			Value:     "E02144",
			Source:    "jpcrp030000-asr-001_E02144-000_2026-03-31_01_2026-06-20.csv",
		},
	}

	got := DEI(all)
	if got.EdinetCode != "E02144" {
		t.Errorf("EdinetCode = %q, want E02144 (audit fact with E99999 should be skipped)", got.EdinetCode)
	}
}
