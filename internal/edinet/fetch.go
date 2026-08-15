package edinet

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// FileType selects which rendition of a document to download.
type FileType int

// File types accepted by the document endpoint's type parameter.
const (
	FileMain    FileType = 1 // 提出本文書及び監査報告書 + XBRL (zip)
	FilePDF     FileType = 2 // PDF as shown on the EDINET viewer
	FileAttach  FileType = 3 // 代替書面・添付文書 (zip)
	FileEnglish FileType = 4 // English filings (zip)
	FileCSV     FileType = 5 // XBRL rendered to CSV (zip)
)

var fileTypeNames = map[FileType]string{
	FileMain:    "main",
	FilePDF:     "pdf",
	FileAttach:  "attach",
	FileEnglish: "english",
	FileCSV:     "csv",
}

func (f FileType) String() string {
	if n, ok := fileTypeNames[f]; ok {
		return n
	}
	return fmt.Sprintf("FileType(%d)", int(f))
}

// Ext returns the file extension the API delivers for this type.
func (f FileType) Ext() string {
	if f == FilePDF {
		return ".pdf"
	}
	return ".zip"
}

// FileTypeNames lists the accepted --file values in a stable order.
func FileTypeNames() []string {
	return []string{"main", "pdf", "attach", "english", "csv"}
}

// ParseFileType maps a --file value to its API code.
func ParseFileType(s string) (FileType, error) {
	for ft, name := range fileTypeNames {
		if strings.EqualFold(s, name) {
			return ft, nil
		}
	}
	return 0, fmt.Errorf("unknown file type %q (want one of: %s)", s, strings.Join(FileTypeNames(), ", "))
}

// File is a downloaded document.
type File struct {
	DocID       string
	Type        FileType
	ContentType string
	Data        []byte
}

// Name is a reasonable filename for saving this download.
func (f File) Name() string {
	return fmt.Sprintf("%s_%s%s", f.DocID, f.Type, f.Type.Ext())
}

// Fetch downloads one rendition of a document.
//
// The document endpoint returns HTTP 200 whether it succeeded or not: on
// success the body is the file, on failure it is a JSON error. Content-Type is
// the only reliable discriminator, so it drives the branch here rather than the
// status code.
func (c *Client) Fetch(ctx context.Context, docID string, ft FileType) (*File, error) {
	if docID == "" {
		return nil, fmt.Errorf("no document ID given")
	}

	params := url.Values{}
	params.Set("type", fmt.Sprint(int(ft)))

	rawURL, err := c.endpoint("/api/v2/documents/"+url.PathEscape(docID), params)
	if err != nil {
		return nil, err
	}
	redacted := fmt.Sprintf("%s/api/v2/documents/%s?type=%d", c.baseURL(), docID, int(ft))

	res, err := c.do(ctx, rawURL, redacted)
	if err != nil {
		return nil, fmt.Errorf("fetching %s (%s): %w", docID, ft, err)
	}

	// JSON reaching this point described no error, but it is still not a
	// document — writing it to disk would produce a corrupt file.
	if isJSON(res.contentType) {
		return nil, fmt.Errorf("fetching %s (%s): EDINET returned JSON instead of a file: %.200s", docID, ft, res.body)
	}

	if len(res.body) == 0 {
		return nil, fmt.Errorf("fetching %s (%s): EDINET returned an empty file", docID, ft)
	}

	return &File{DocID: docID, Type: ft, ContentType: res.contentType, Data: res.body}, nil
}

func isJSON(contentType string) bool {
	return strings.Contains(strings.ToLower(contentType), "application/json")
}
