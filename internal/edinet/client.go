// Package edinet is a client for the FSA's EDINET API v2.
//
// The API has two quirks that shape this package. It reports failures with an
// HTTP 200 status line and the real status inside the response body, so every
// response is inspected rather than trusted. And its list endpoint accepts one
// date per request, so ranges are fanned out day by day.
package edinet

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// DefaultBaseURL is the EDINET API v2 host.
const DefaultBaseURL = "https://api.edinet-fsa.go.jp"

// RetentionYears is how far back EDINET serves filing data. Requests for older
// dates are rejected before they leave the process.
const RetentionYears = 10

// Client talks to the EDINET API v2.
type Client struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client

	// MaxRetries bounds retries of rate-limited and server-error responses.
	MaxRetries int
	// Backoff returns how long to wait before retry number n (0-based).
	Backoff func(n int) time.Duration
	// sleep is a seam for tests; nil means time.Sleep.
	sleep func(context.Context, time.Duration) error
}

// New returns a Client with defaults suited to a polite CLI. EDINET publishes
// no rate limit, only a 429 and an instruction to slow down, so the backoff is
// deliberately generous.
func New(apiKey string) *Client {
	return &Client{
		BaseURL:    DefaultBaseURL,
		APIKey:     apiKey,
		HTTPClient: &http.Client{Timeout: 120 * time.Second},
		MaxRetries: 4,
		Backoff:    func(n int) time.Duration { return time.Duration(1<<n) * time.Second },
	}
}

func (c *Client) baseURL() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return DefaultBaseURL
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

func (c *Client) wait(ctx context.Context, d time.Duration) error {
	if c.sleep != nil {
		return c.sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// endpoint builds a request URL, always attaching the subscription key.
func (c *Client) endpoint(path string, params url.Values) (string, error) {
	u, err := url.Parse(c.baseURL())
	if err != nil {
		return "", fmt.Errorf("invalid base URL %q: %w", c.baseURL(), err)
	}
	u.Path, err = url.JoinPath(u.Path, path)
	if err != nil {
		return "", err
	}
	if params == nil {
		params = url.Values{}
	}
	params.Set("Subscription-Key", c.APIKey)
	u.RawQuery = params.Encode()
	return u.String(), nil
}

// response is a raw EDINET response with its body already drained.
type response struct {
	contentType string
	body        []byte
}

// do issues a GET and retries responses that are worth retrying. The API key is
// never included in any error text, so failures are safe to log.
func (c *Client) do(ctx context.Context, rawURL, redactedURL string) (*response, error) {
	var lastErr error

	for attempt := 0; ; attempt++ {
		res, err := c.attempt(ctx, rawURL)
		if err == nil {
			return res, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		var apiErr *APIError
		retryable := errors.As(err, &apiErr) && apiErr.Retryable()
		if !retryable || attempt >= c.MaxRetries {
			if attempt > 0 {
				return nil, fmt.Errorf("GET %s failed after %d attempts: %w", redactedURL, attempt+1, err)
			}
			return nil, fmt.Errorf("GET %s: %w", redactedURL, err)
		}

		lastErr = err
		if err := c.wait(ctx, c.backoff(attempt)); err != nil {
			return nil, fmt.Errorf("GET %s: %w (last error: %v)", redactedURL, err, lastErr)
		}
	}
}

func (c *Client) backoff(n int) time.Duration {
	if c.Backoff != nil {
		return c.Backoff(n)
	}
	return time.Duration(1<<n) * time.Second
}

func (c *Client) attempt(ctx context.Context, rawURL string) (*response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)

	res, err := c.httpClient().Do(req)
	if err != nil {
		return nil, redactURLError(err)
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	contentType := res.Header.Get("Content-Type")

	// Any JSON body may be an error report, whatever the status line says.
	// Checking here rather than at the call site is what lets a 429 hidden
	// inside an HTTP 200 reach the retry loop.
	if isJSON(contentType) {
		if err := apiErrorFrom(body); err != nil {
			return nil, err
		}
	}

	// A non-200 status line is unusual (EDINET reports failures in the body)
	// but a proxy or maintenance page can still produce one.
	if res.StatusCode != http.StatusOK {
		return nil, &APIError{Status: res.StatusCode, Message: http.StatusText(res.StatusCode)}
	}

	return &response{contentType: contentType, body: body}, nil
}

const userAgent = "edinet-cli (+https://github.com/jackchuka/edinet-cli)"

// redactURLError strips the query string from transport errors, which would
// otherwise print the subscription key.
func redactURLError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		if u, perr := url.Parse(urlErr.URL); perr == nil {
			u.RawQuery = ""
			urlErr.URL = u.String()
		}
	}
	return err
}
