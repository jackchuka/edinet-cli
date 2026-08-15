package edinet

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	c := New("test-key")
	c.BaseURL = srv.URL
	c.MaxRetries = 3
	c.Backoff = func(int) time.Duration { return 0 }
	c.sleep = func(context.Context, time.Duration) error { return nil }
	return c
}

const listOK = `{
  "metadata": {
    "title": "提出された書類を把握するためのAPI",
    "parameter": {"date": "2026-08-14", "type": "2"},
    "resultset": {"count": 2},
    "processDateTime": "2026-08-14 13:01",
    "status": "200",
    "message": "OK"
  },
  "results": [
    {"seqNumber": 1, "docID": "S100AAAA", "edinetCode": "E02144", "secCode": "72030",
     "JCN": "1180301018771", "filerName": "トヨタ自動車株式会社", "docTypeCode": "120",
     "ordinanceCode": "010", "formCode": "030000", "periodStart": "2025-04-01",
     "periodEnd": "2026-03-31", "submitDateTime": "2026-08-14 09:00",
     "docDescription": "有価証券報告書－第122期", "xbrlFlag": "1", "pdfFlag": "1",
     "csvFlag": "1", "englishDocFlag": "0", "attachDocFlag": "0",
     "withdrawalStatus": "0", "docInfoEditStatus": "0", "disclosureStatus": "0",
     "legalStatus": "1"},
    {"seqNumber": 2, "docID": "S100BBBB", "edinetCode": "E01234", "secCode": null,
     "JCN": "9999999999999", "filerName": "架空ファンド", "docTypeCode": "180",
     "ordinanceCode": "030", "xbrlFlag": "0", "pdfFlag": "1", "csvFlag": "0",
     "withdrawalStatus": "0", "legalStatus": "0"}
  ]
}`

func TestListParsesResults(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("date"); got != "2026-08-14" {
			t.Errorf("date = %q, want 2026-08-14", got)
		}
		if got := r.URL.Query().Get("type"); got != "2" {
			t.Errorf("type = %q, want 2", got)
		}
		if got := r.URL.Query().Get("Subscription-Key"); got != "test-key" {
			t.Errorf("Subscription-Key = %q, want test-key", got)
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(listOK))
	})

	docs, meta, err := c.List(context.Background(), date(t, "2026-08-14"))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("got %d documents, want 2", len(docs))
	}
	if meta.Resultset.Count != 2 {
		t.Errorf("count = %d, want 2", meta.Resultset.Count)
	}
	if docs[0].FilerName != "トヨタ自動車株式会社" {
		t.Errorf("filerName = %q", docs[0].FilerName)
	}
	// A null secCode must decode to empty, not fail the whole response.
	if docs[1].SecCode != "" {
		t.Errorf("secCode = %q, want empty", docs[1].SecCode)
	}
	// FileDate is synthesized so merged ranges stay attributable.
	if docs[0].FileDate != "2026-08-14" {
		t.Errorf("fileDate = %q, want 2026-08-14", docs[0].FileDate)
	}
	if !docs[0].Has(FileCSV) || docs[1].Has(FileCSV) {
		t.Error("csv availability misread")
	}
	if docs[0].Viewable() == docs[1].Viewable() {
		t.Error("legalStatus misread: expected only the first to be viewable")
	}
}

// EDINET reports failures with an HTTP 200 status line, so the body must be
// checked. This is the single most common way to get an EDINET client wrong.
func TestListErrorInsideHTTP200(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"metadata":{"title":"t","status":"404","message":"Not Found"}}`))
	})

	_, _, err := c.List(context.Background(), date(t, "2026-08-14"))
	if err == nil {
		t.Fatal("expected an error for a 404 delivered inside HTTP 200")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not *APIError: %v", err)
	}
	if apiErr.Status != 404 {
		t.Errorf("status = %d, want 404", apiErr.Status)
	}
}

func TestListInvalidKey(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(`{"StatusCode":401,"message":"Access denied due to invalid subscription key."}`))
	})

	_, _, err := c.List(context.Background(), date(t, "2026-08-14"))
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 401 {
		t.Fatalf("want 401 APIError, got %v", err)
	}
	if !strings.Contains(err.Error(), "EDINET_API_KEY") {
		t.Errorf("401 message should tell the user where to fix the key, got: %v", err)
	}
}

func TestRetriesRateLimit(t *testing.T) {
	var calls atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if calls.Add(1) < 3 {
			_, _ = w.Write([]byte(`{"StatusCode":429,"message":"Too Many Requests"}`))
			return
		}
		_, _ = w.Write([]byte(listOK))
	})

	docs, _, err := c.List(context.Background(), date(t, "2026-08-14"))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(docs) != 2 {
		t.Errorf("got %d documents after retry, want 2", len(docs))
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("made %d calls, want 3", got)
	}
}

func TestDoesNotRetryClientError(t *testing.T) {
	var calls atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(`{"metadata":{"title":"t","status":"400","message":"Bad Request"}}`))
	})

	if _, _, err := c.List(context.Background(), date(t, "2026-08-14")); err == nil {
		t.Fatal("expected error")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("made %d calls, want 1 (400 is not retryable)", got)
	}
}

func TestErrorsNeverLeakAPIKey(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(`{"metadata":{"title":"t","status":"500","message":"Internal Server Error"}}`))
	})
	c.APIKey = "super-secret-key"

	_, _, err := c.List(context.Background(), date(t, "2026-08-14"))
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "super-secret-key") {
		t.Fatalf("error text leaked the API key: %v", err)
	}
}

func TestMaintenancePageIsNotSuccess(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body>Sorry, EDINET is under maintenance.</body></html>`))
	})

	if _, _, err := c.List(context.Background(), date(t, "2026-08-14")); err == nil {
		t.Fatal("an HTML maintenance page must not be reported as success")
	}
}

func TestListRangeIsOrderedAndBounded(t *testing.T) {
	var (
		mu      sync.Mutex
		inFloat int
		peak    int
	)
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		inFloat++
		if inFloat > peak {
			peak = inFloat
		}
		mu.Unlock()
		time.Sleep(5 * time.Millisecond)
		mu.Lock()
		inFloat--
		mu.Unlock()

		day := r.URL.Query().Get("date")
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(`{"metadata":{"title":"t","status":"200","message":"OK","resultset":{"count":1}},
			"results":[{"seqNumber":1,"docID":"S1` + strings.ReplaceAll(day, "-", "") + `"}]}`))
	})

	docs, err := c.ListRange(context.Background(), date(t, "2026-08-01"), date(t, "2026-08-07"), 2)
	if err != nil {
		t.Fatalf("ListRange: %v", err)
	}
	if len(docs) != 7 {
		t.Fatalf("got %d documents, want 7", len(docs))
	}
	for i := 1; i < len(docs); i++ {
		if docs[i-1].FileDate >= docs[i].FileDate {
			t.Fatalf("results not sorted by file date: %q then %q", docs[i-1].FileDate, docs[i].FileDate)
		}
	}
	if peak > 2 {
		t.Errorf("peak concurrency %d exceeded the limit of 2", peak)
	}
}

func TestListRangeRejectsBackwardsRange(t *testing.T) {
	c := New("k")
	_, err := c.ListRange(context.Background(), date(t, "2026-08-07"), date(t, "2026-08-01"), 2)
	if err == nil || !strings.Contains(err.Error(), "--from") {
		t.Fatalf("want a from/to ordering error, got %v", err)
	}
}

func TestValidateDate(t *testing.T) {
	now := date(t, "2026-08-15")
	tests := []struct {
		name    string
		in      string
		wantErr string
	}{
		{"today", "2026-08-15", ""},
		{"yesterday", "2026-08-14", ""},
		{"edge of retention", "2016-08-15", ""},
		{"tomorrow", "2026-08-16", "future"},
		{"too old", "2016-08-14", "retention"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDate(date(t, tt.in), now)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tt.wantErr != "" && err == nil:
				t.Fatalf("expected an error mentioning %q", tt.wantErr)
			case tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr):
				t.Fatalf("error %q should mention %q", err, tt.wantErr)
			}
		})
	}
}

// EDINET files against Tokyo dates. Late evening UTC is already the next day in
// Japan, and that day is both requestable and not "in the future".
func TestValidateDateUsesTokyoTime(t *testing.T) {
	// 2026-08-15 23:00 UTC is 2026-08-16 08:00 in Tokyo.
	now := time.Date(2026, 8, 15, 23, 0, 0, 0, time.UTC)

	if err := ValidateDate(date(t, "2026-08-16"), now); err != nil {
		t.Errorf("2026-08-16 is today in Tokyo and must be accepted: %v", err)
	}
	if err := ValidateDate(date(t, "2026-08-17"), now); err == nil {
		t.Error("2026-08-17 is still tomorrow in Tokyo and must be rejected")
	}

	// The reverse: early morning UTC is the same Tokyo day, not the previous one.
	morning := time.Date(2026, 8, 16, 1, 0, 0, 0, time.UTC)
	if err := ValidateDate(date(t, "2026-08-16"), morning); err != nil {
		t.Errorf("2026-08-16 must be accepted at 01:00 UTC: %v", err)
	}
}

func TestTodayIsATokyoDate(t *testing.T) {
	got := Today()
	if _, offset := got.Zone(); offset != 9*60*60 {
		t.Errorf("Today() is in a zone offset by %ds, want JST (+32400s)", offset)
	}
	if h, m, s := got.Clock(); h != 0 || m != 0 || s != 0 {
		t.Errorf("Today() = %v, want midnight", got)
	}

	want := time.Now().In(JST).Format(DateLayout)
	if got.Format(DateLayout) != want {
		t.Errorf("Today() = %s, want %s", got.Format(DateLayout), want)
	}
}

func TestParseDateIsTokyoLocal(t *testing.T) {
	d, err := ParseDate("2026-08-16")
	if err != nil {
		t.Fatalf("ParseDate: %v", err)
	}
	if _, offset := d.Zone(); offset != 9*60*60 {
		t.Errorf("ParseDate returned zone offset %ds, want JST", offset)
	}
	if got := d.Format(DateLayout); got != "2026-08-16" {
		t.Errorf("ParseDate round-tripped to %s", got)
	}
	if _, err := ParseDate("2026/08/16"); err == nil {
		t.Error("expected an error for a malformed date")
	}
}

// Ranges must be enumerated in Tokyo days, so a UTC-midnight boundary cannot
// drop or duplicate a day.
func TestEachDayCountsTokyoDays(t *testing.T) {
	from := time.Date(2026, 8, 1, 22, 0, 0, 0, time.UTC) // 2026-08-02 07:00 JST
	to := time.Date(2026, 8, 4, 22, 0, 0, 0, time.UTC)   // 2026-08-05 07:00 JST

	days, err := eachDay(from, to)
	if err != nil {
		t.Fatalf("eachDay: %v", err)
	}
	want := []string{"2026-08-02", "2026-08-03", "2026-08-04", "2026-08-05"}
	if len(days) != len(want) {
		t.Fatalf("got %d days, want %d", len(days), len(want))
	}
	for i, d := range days {
		if got := d.Format(DateLayout); got != want[i] {
			t.Errorf("day %d = %s, want %s", i, got, want[i])
		}
	}
}

func date(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse(DateLayout, s)
	if err != nil {
		t.Fatalf("bad test date %q: %v", s, err)
	}
	return d
}
