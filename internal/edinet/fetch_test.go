package edinet

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestFetchReturnsBinary(t *testing.T) {
	want := []byte("PK\x03\x04 pretend this is a zip")
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/api/v2/documents/S100AAAA" {
			t.Errorf("path = %q", got)
		}
		if got := r.URL.Query().Get("type"); got != "5" {
			t.Errorf("type = %q, want 5", got)
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(want)
	})

	f, err := c.Fetch(context.Background(), "S100AAAA", FileCSV)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !bytes.Equal(f.Data, want) {
		t.Errorf("body mismatch")
	}
	if got := f.Name(); got != "S100AAAA_csv.zip" {
		t.Errorf("Name() = %q, want S100AAAA_csv.zip", got)
	}
}

func TestFetchPDFNaming(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF-1.4"))
	})

	f, err := c.Fetch(context.Background(), "S100AAAA", FilePDF)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got := f.Name(); got != "S100AAAA_pdf.pdf" {
		t.Errorf("Name() = %q, want S100AAAA_pdf.pdf", got)
	}
}

// The document endpoint answers 200 with a JSON body when it fails, so
// Content-Type is the only signal that anything went wrong. Writing the body
// straight to disk would produce a "zip" containing an error message.
func TestFetchDetectsJSONErrorBody(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"metadata":{"title":"t","status":"404","message":"Not Found"}}`))
	})

	_, err := c.Fetch(context.Background(), "S100MISSING", FileMain)
	if err == nil {
		t.Fatal("expected an error, got a file")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 404 {
		t.Fatalf("want 404 APIError, got %v", err)
	}
}

func TestFetchRejectsEmptyBody(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
	})

	if _, err := c.Fetch(context.Background(), "S100AAAA", FileMain); err == nil {
		t.Fatal("expected an error for an empty body")
	}
}

func TestParseFileType(t *testing.T) {
	tests := map[string]FileType{
		"main":    FileMain,
		"PDF":     FilePDF,
		"attach":  FileAttach,
		"english": FileEnglish,
		"csv":     FileCSV,
	}
	for in, want := range tests {
		got, err := ParseFileType(in)
		if err != nil {
			t.Errorf("ParseFileType(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseFileType(%q) = %d, want %d", in, got, want)
		}
	}

	_, err := ParseFileType("xbrl")
	if err == nil {
		t.Fatal("expected an error for an unknown type")
	}
	if !strings.Contains(err.Error(), "main") {
		t.Errorf("error should list the valid types, got: %v", err)
	}
}
