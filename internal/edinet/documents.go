package edinet

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"time"

	"golang.org/x/sync/errgroup"
)

// DateLayout is the date format EDINET accepts and emits.
const DateLayout = "2006-01-02"

// JST is the zone EDINET reports filing dates in.
//
// Japan has observed no daylight saving since 1951, so a fixed offset is exact
// and avoids depending on the host having a tzdata database.
var JST = time.FixedZone("JST", 9*60*60)

// Today returns the current filing date in Tokyo, which is the only date that
// means anything to EDINET. Deriving it from the host's local zone would ask
// for the wrong day from anywhere west of Japan, including UTC CI runners.
func Today() time.Time {
	return toJSTDay(time.Now())
}

// ParseDate reads a YYYY-MM-DD filing date as a Tokyo date.
func ParseDate(s string) (time.Time, error) {
	return time.ParseInLocation(DateLayout, s, JST)
}

// toJSTDay converts an instant to the calendar day it falls on in Tokyo.
func toJSTDay(t time.Time) time.Time {
	t = t.In(JST)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, JST)
}

// Document is one row of the 提出書類一覧 (submitted document list).
type Document struct {
	SeqNumber            int    `json:"seqNumber"`
	DocID                string `json:"docID"`
	EdinetCode           string `json:"edinetCode"`
	SecCode              string `json:"secCode"`
	JCN                  string `json:"JCN"`
	FilerName            string `json:"filerName"`
	FundCode             string `json:"fundCode"`
	OrdinanceCode        string `json:"ordinanceCode"`
	FormCode             string `json:"formCode"`
	DocTypeCode          string `json:"docTypeCode"`
	PeriodStart          string `json:"periodStart"`
	PeriodEnd            string `json:"periodEnd"`
	SubmitDateTime       string `json:"submitDateTime"`
	DocDescription       string `json:"docDescription"`
	IssuerEdinetCode     string `json:"issuerEdinetCode"`
	SubjectEdinetCode    string `json:"subjectEdinetCode"`
	SubsidiaryEdinetCode string `json:"subsidiaryEdinetCode"`
	CurrentReportReason  string `json:"currentReportReason"`
	ParentDocID          string `json:"parentDocID"`
	OpeDateTime          string `json:"opeDateTime"`
	WithdrawalStatus     string `json:"withdrawalStatus"`
	DocInfoEditStatus    string `json:"docInfoEditStatus"`
	DisclosureStatus     string `json:"disclosureStatus"`
	XbrlFlag             string `json:"xbrlFlag"`
	PdfFlag              string `json:"pdfFlag"`
	AttachDocFlag        string `json:"attachDocFlag"`
	EnglishDocFlag       string `json:"englishDocFlag"`
	CsvFlag              string `json:"csvFlag"`
	LegalStatus          string `json:"legalStatus"`

	// FileDate is the list date this document was returned under. EDINET does
	// not include it in the row, but it is the only way to tell which request a
	// row came from once a date range is merged.
	FileDate string `json:"fileDate"`
}

// Has reports whether the given file type is available for this document.
func (d Document) Has(ft FileType) bool {
	switch ft {
	case FileMain:
		return d.XbrlFlag == "1"
	case FilePDF:
		return d.PdfFlag == "1"
	case FileAttach:
		return d.AttachDocFlag == "1"
	case FileEnglish:
		return d.EnglishDocFlag == "1"
	case FileCSV:
		return d.CsvFlag == "1"
	default:
		return false
	}
}

// Withdrawn reports whether this row is a withdrawal notice or a document that
// has been withdrawn.
func (d Document) Withdrawn() bool {
	return d.WithdrawalStatus == "1" || d.WithdrawalStatus == "2"
}

// Viewable reports whether the document is still within its statutory or
// extended viewing period. Expired rows remain listed but cannot be downloaded.
func (d Document) Viewable() bool {
	return d.LegalStatus == "1" || d.LegalStatus == "2"
}

// Metadata is the envelope EDINET wraps every list response in.
type Metadata struct {
	Title     string `json:"title"`
	Parameter struct {
		Date string `json:"date"`
		Type string `json:"type"`
	} `json:"parameter"`
	Resultset struct {
		Count int `json:"count"`
	} `json:"resultset"`
	ProcessDateTime string `json:"processDateTime"`
	Status          string `json:"status"`
	Message         string `json:"message"`
}

type listResponse struct {
	Metadata Metadata   `json:"metadata"`
	Results  []Document `json:"results"`
}

// List returns every document filed on a single date.
//
// EDINET's list endpoint accepts exactly one date; there is no range query.
func (c *Client) List(ctx context.Context, date time.Time) ([]Document, Metadata, error) {
	if err := ValidateDate(date, time.Now()); err != nil {
		return nil, Metadata{}, err
	}

	day := date.Format(DateLayout)
	params := url.Values{}
	params.Set("date", day)
	params.Set("type", "2") // 2 = documents + metadata

	rawURL, err := c.endpoint("/api/v2/documents.json", params)
	if err != nil {
		return nil, Metadata{}, err
	}
	redacted := fmt.Sprintf("%s/api/v2/documents.json?date=%s&type=2", c.baseURL(), day)

	res, err := c.do(ctx, rawURL, redacted)
	if err != nil {
		return nil, Metadata{}, fmt.Errorf("listing documents for %s: %w", day, err)
	}
	if !isJSON(res.contentType) {
		return nil, Metadata{}, fmt.Errorf("listing documents for %s: EDINET returned %s instead of JSON (is EDINET under maintenance?)",
			day, res.contentType)
	}

	var parsed listResponse
	if err := json.Unmarshal(res.body, &parsed); err != nil {
		return nil, Metadata{}, fmt.Errorf("decoding document list for %s: %w", day, err)
	}

	for i := range parsed.Results {
		parsed.Results[i].FileDate = day
	}
	return parsed.Results, parsed.Metadata, nil
}

// ListRange returns every document filed between from and to inclusive.
//
// One HTTP request is made per day, up to concurrency at a time. Results are
// sorted by file date then sequence number so output is deterministic
// regardless of which request finished first.
func (c *Client) ListRange(ctx context.Context, from, to time.Time, concurrency int) ([]Document, error) {
	days, err := eachDay(from, to)
	if err != nil {
		return nil, err
	}
	if concurrency < 1 {
		concurrency = 1
	}

	perDay := make([][]Document, len(days))

	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(concurrency)
	for i, day := range days {
		g.Go(func() error {
			docs, _, err := c.List(ctx, day)
			if err != nil {
				return err
			}
			perDay[i] = docs
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}

	var all []Document
	for _, docs := range perDay {
		all = append(all, docs...)
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].FileDate != all[j].FileDate {
			return all[i].FileDate < all[j].FileDate
		}
		return all[i].SeqNumber < all[j].SeqNumber
	})
	return all, nil
}

func eachDay(from, to time.Time) ([]time.Time, error) {
	from, to = toJSTDay(from), toJSTDay(to)
	if to.Before(from) {
		return nil, fmt.Errorf("--from (%s) is after --to (%s)", from.Format(DateLayout), to.Format(DateLayout))
	}
	var days []time.Time
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		days = append(days, d)
	}
	return days, nil
}

// ValidateDate rejects dates EDINET will not serve, so an obvious mistake costs
// no network round trip and produces a clear message.
//
// Both arguments are interpreted as Tokyo dates: "today" in Osaka is still
// tomorrow in San Francisco, and it is Osaka's answer that EDINET honours.
func ValidateDate(date, now time.Time) error {
	date, now = toJSTDay(date), toJSTDay(now)
	if date.After(now) {
		return fmt.Errorf("date %s is in the future; EDINET only has filings up to today", date.Format(DateLayout))
	}
	oldest := now.AddDate(-RetentionYears, 0, 0)
	if date.Before(oldest) {
		return fmt.Errorf("date %s is beyond EDINET's %d-year retention (oldest available: %s)",
			date.Format(DateLayout), RetentionYears, oldest.Format(DateLayout))
	}
	return nil
}
