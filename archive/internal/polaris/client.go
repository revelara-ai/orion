package polaris

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client is the read-only Polaris API client.
type Client struct {
	cfg  Config
	http *http.Client
}

// NewClient validates cfg and returns a Client.
func NewClient(cfg Config) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Client{
		cfg: cfg,
		http: &http.Client{
			Timeout: cfg.effectiveTimeout(),
		},
	}, nil
}

// ListControlsOptions filters the catalog query.
type ListControlsOptions struct {
	// Categories restricts results to these category names. Empty = all.
	Categories []string

	// Limit caps result size. 0 = use Polaris's default.
	Limit int

	// Page is the 1-indexed page number. 0 = first page.
	Page int
}

// ListControls fetches the controls catalog from Polaris with retries on
// transient 5xx and network failures. Returns a snapshotted
// ControlsCatalog whose contents do not change after this call returns.
func (c *Client) ListControls(ctx context.Context, opts ListControlsOptions) (*ControlsCatalog, error) {
	q := url.Values{}
	if len(opts.Categories) > 0 {
		q.Set("categories", strings.Join(opts.Categories, ","))
	}
	if opts.Limit > 0 {
		q.Set("limit", strconv.Itoa(opts.Limit))
	}
	if opts.Page > 0 {
		q.Set("page", strconv.Itoa(opts.Page))
	}

	endpoint := strings.TrimRight(c.cfg.BaseURL, "/") + "/api/v1/controls"
	if encoded := q.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}

	body, err := c.doWithRetry(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}

	var resp ListControlsResponse
	if jerr := json.Unmarshal(body, &resp); jerr != nil {
		return nil, fmt.Errorf("polaris: parse ListControls response: %w", jerr)
	}

	return &ControlsCatalog{
		Controls:   resp.Controls,
		Total:      resp.Total,
		SnapshotAt: time.Now().UTC().Format(time.RFC3339),
	}, nil
}

// doWithRetry performs an HTTP request with exponential backoff on 5xx
// responses and network errors. Returns the response body on success.
func (c *Client) doWithRetry(ctx context.Context, method, endpoint string, body io.Reader) ([]byte, error) {
	max := c.cfg.effectiveMaxRetries()
	delay := c.cfg.effectiveBaseDelay()

	var lastErr error
	for attempt := 0; attempt < max; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
			delay *= 2
		}

		req, err := http.NewRequestWithContext(ctx, method, endpoint, body) //#nosec G107 -- endpoint is built from cfg.BaseURL (operator-trusted) plus a static path
		if err != nil {
			return nil, fmt.Errorf("polaris: build request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
		req.Header.Set("Accept", "application/json")

		resp, doErr := c.http.Do(req) //#nosec G107,G704 -- req URL built from cfg.BaseURL (operator-trusted env value), not user input
		if doErr != nil {
			lastErr = doErr
			continue // retry on network error
		}

		respBody, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}

		switch {
		case resp.StatusCode >= 200 && resp.StatusCode < 300:
			return respBody, nil
		case resp.StatusCode >= 500:
			lastErr = fmt.Errorf("%w: status %d (%s)", ErrUnexpectedStatus, resp.StatusCode, truncateBody(respBody))
			continue // retry on 5xx
		default:
			// 3xx/4xx are not retried.
			return nil, fmt.Errorf("%w: status %d (%s)", ErrUnexpectedStatus, resp.StatusCode, truncateBody(respBody))
		}
	}

	return nil, fmt.Errorf("%w: %v", ErrPolarisUnreachable, lastErr)
}

func truncateBody(b []byte) string {
	const maxLen = 200
	s := string(b)
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}
