// Adapted from polaris/internal/connector/providers/linear/linear.go
// graphql() helper at SHA 78d5166b on 2026-05-11. Extracted from the
// Linear adapter's private method into a reusable package-level Exec
// so Orion's Linear adapter (E2-4) and future GraphQL adapters share
// transport. Pending consolidation per orion-13j.

package oauth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// graphqlRequest is the standard GraphQL HTTP body shape.
type graphqlRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables,omitempty"`
}

// graphqlResponse wraps the data envelope + errors array.
type graphqlResponse struct {
	Data   map[string]any `json:"data"`
	Errors []graphqlError `json:"errors,omitempty"`
}

// graphqlError is the standard GraphQL error shape.
type graphqlError struct {
	Message    string         `json:"message"`
	Path       []any          `json:"path,omitempty"`
	Extensions map[string]any `json:"extensions,omitempty"`
}

// GraphQLExecOptions configures Exec.
type GraphQLExecOptions struct {
	// Endpoint is the absolute URL (e.g. https://api.linear.app/graphql).
	Endpoint string

	// BearerToken is sent as Authorization: Bearer <token>.
	BearerToken string //#nosec G117 -- in-memory bearer; never serialized to disk by this package

	// HTTPClient overrides the default http.DefaultClient. Tests
	// inject one pointed at an httptest.Server.
	HTTPClient *http.Client

	// ExtraHeaders are appended verbatim (e.g. User-Agent).
	ExtraHeaders map[string]string
}

// Exec posts a GraphQL query and returns the data envelope. Returns
// an error if the HTTP request fails, the response status is non-2xx,
// JSON decoding fails, or the response contains a GraphQL errors
// array (the first error's Message is the wrapped error).
func Exec(ctx context.Context, opts GraphQLExecOptions, query string, variables map[string]any) (map[string]any, error) {
	if opts.Endpoint == "" {
		return nil, fmt.Errorf("oauth: graphql endpoint required")
	}
	if opts.BearerToken == "" {
		return nil, fmt.Errorf("oauth: graphql bearer token required")
	}
	body, err := json.Marshal(graphqlRequest{Query: query, Variables: variables})
	if err != nil {
		return nil, fmt.Errorf("oauth: graphql marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, opts.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("oauth: graphql request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+opts.BearerToken)
	for k, v := range opts.ExtraHeaders {
		req.Header.Set(k, v)
	}
	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req) //#nosec G107,G704 -- endpoint is operator-configured per provider; body is JSON we built
	if err != nil {
		return nil, fmt.Errorf("oauth: graphql do: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("oauth: graphql read body: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("oauth: graphql status %d: %s", resp.StatusCode, truncate(string(respBody), 200))
	}
	var gql graphqlResponse
	if err := json.Unmarshal(respBody, &gql); err != nil {
		return nil, fmt.Errorf("oauth: graphql unmarshal: %w", err)
	}
	if len(gql.Errors) > 0 {
		return nil, fmt.Errorf("oauth: graphql errors: %s", gql.Errors[0].Message)
	}
	return gql.Data, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
