package edinet

import (
	"encoding/json"
	"fmt"
)

// APIError is an error reported by EDINET itself.
//
// EDINET returns its own failures with an HTTP 200 status line and the real
// status buried in the response body, so a nil error from net/http means
// nothing on its own. Every response body is inspected before it is trusted.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	switch e.Status {
	case 400:
		return fmt.Sprintf("EDINET rejected the request (400): %s", e.Message)
	case 401:
		return "EDINET rejected the API key (401): check EDINET_API_KEY or --api-key"
	case 404:
		return fmt.Sprintf("EDINET has no such resource (404): %s", e.Message)
	case 429:
		return "EDINET is rate limiting (429): lower --concurrency and retry"
	case 500:
		return fmt.Sprintf("EDINET server error (500): %s", e.Message)
	default:
		return fmt.Sprintf("EDINET error (%d): %s", e.Status, e.Message)
	}
}

// Retryable reports whether resending the identical request could succeed.
func (e *APIError) Retryable() bool {
	return e.Status == 429 || e.Status >= 500
}

// errorBody covers both failure shapes EDINET emits: errors from the API layer
// arrive as a bare {"StatusCode":401,"message":...}, while errors from the
// application layer are nested under "metadata".
type errorBody struct {
	StatusCode *int   `json:"StatusCode"`
	Message    string `json:"message"`
	Metadata   *struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	} `json:"metadata"`
}

// apiErrorFrom returns an *APIError if body describes a failure, or nil if it
// describes success. A body that is not EDINET-shaped JSON yields an error too:
// silently treating it as success would hide maintenance pages.
func apiErrorFrom(body []byte) error {
	var e errorBody
	if err := json.Unmarshal(body, &e); err != nil {
		return fmt.Errorf("EDINET returned a response that is not JSON (is EDINET under maintenance?): %w", err)
	}

	if e.StatusCode != nil && *e.StatusCode != 200 {
		return &APIError{Status: *e.StatusCode, Message: e.Message}
	}

	if e.Metadata == nil {
		return fmt.Errorf("EDINET response had no metadata: %.200s", body)
	}

	var status int
	if _, err := fmt.Sscanf(e.Metadata.Status, "%d", &status); err != nil {
		return fmt.Errorf("EDINET returned an unreadable status %q", e.Metadata.Status)
	}
	if status != 200 {
		return &APIError{Status: status, Message: e.Metadata.Message}
	}
	return nil
}
