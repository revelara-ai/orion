// Forked from polaris/internal/connector/providers/linear/linear.go
// at SHA 78d5166b on 2026-05-11. The token-rotation guts (refreshIfNeeded,
// postOAuthToken, tokenResponse) are kept; the GraphQL transport is
// replaced with internal/oauth.Exec so the substrate is shared with
// future GraphQL providers. Pending consolidation per orion-13j.

package linear

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/revelara-ai/orion/internal/oauth"
	"github.com/revelara-ai/orion/internal/trackers"
)

const (
	defaultAPIURL = "https://api.linear.app/graphql"
	// defaultTokenURL is Linear's well-known OAuth token endpoint.
	defaultTokenURL = "https://api.linear.app/oauth/token" //#nosec G101 -- well-known OAuth endpoint URL, not a credential
)

// linearClient is the per-binding worker. It holds the binding's
// current tokens, a refresh mutex, and the persistence callback the
// Adapter wires before invoking client methods.
//
// Implements oauth.TokenRefresher (via SetTokenRefreshCallback) so
// callers can use oauth.WireRefreshCallback directly on a client if
// they prefer the registry-style API.
type linearClient struct {
	apiURL       string
	tokenURL     string
	httpClient   *http.Client
	clientID     string //#nosec G117 -- OAuth app credential, not a bearer
	clientSecret string //#nosec G117 -- OAuth app credential, not a bearer

	tokenMu        sync.Mutex
	accessToken    string //#nosec G117 -- in-memory bearer; durable store is encrypted_oauth_credential.encrypted_blob
	refreshToken   string //#nosec G117 -- in-memory bearer; durable store is encrypted_oauth_credential.encrypted_blob
	tokenExpiry    time.Time
	onTokenRefresh func(accessToken, refreshToken string, expiry time.Time)
}

// SetTokenRefreshCallback satisfies oauth.TokenRefresher; the Adapter
// wires this before each call when persistFactory is set.
func (c *linearClient) SetTokenRefreshCallback(fn func(string, string, time.Time)) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	c.onTokenRefresh = fn
}

// defaultClientFactory builds a linearClient from a TrackerBinding.
// Expects binding.Credentials.OAuth2{Access,Refresh}Token,
// ExpiresAt, and Extra["client_id"]+["client_secret"]. The endpoint
// URLs default to Linear's production values; tests pass overrides
// via binding.Config["_api_url"] and ["_token_url"].
func defaultClientFactory(binding trackers.TrackerBinding) (*linearClient, error) {
	clientID := binding.Credentials.Extra["client_id"]
	clientSecret := binding.Credentials.Extra["client_secret"]
	if clientID == "" || clientSecret == "" {
		return nil, fmt.Errorf("%w: credentials.extra.client_id and client_secret required", trackers.ErrInvalidBinding)
	}
	apiURL := defaultAPIURL
	if override, ok := binding.Config["_api_url"].(string); ok && override != "" {
		apiURL = override
	}
	tokenURL := defaultTokenURL
	if override, ok := binding.Config["_token_url"].(string); ok && override != "" {
		tokenURL = override
	}
	return &linearClient{
		apiURL:       apiURL,
		tokenURL:     tokenURL,
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		clientID:     clientID,
		clientSecret: clientSecret,
		accessToken:  binding.Credentials.OAuth2AccessToken,
		refreshToken: binding.Credentials.OAuth2RefreshToken,
		tokenExpiry:  binding.Credentials.ExpiresAt,
	}, nil
}

// graphql refreshes the access token if needed, then posts the
// query via oauth.Exec. The refresh path fires onTokenRefresh on
// success (with the new tokens + expiry) so the Adapter's persist
// callback can write the rotated values to the credential store.
func (c *linearClient) graphql(ctx context.Context, query string, variables map[string]any) (map[string]any, error) {
	if err := c.refreshIfNeeded(ctx); err != nil {
		return nil, err
	}
	c.tokenMu.Lock()
	access := c.accessToken
	c.tokenMu.Unlock()
	return oauth.Exec(ctx, oauth.GraphQLExecOptions{
		Endpoint:    c.apiURL,
		BearerToken: access,
		HTTPClient:  c.httpClient,
	}, query, variables)
}

// refreshIfNeeded refreshes the access token in place when it's
// within 60s of expiring. Concurrent-safe via tokenMu. The 60s
// window matches Linear's recommendation.
func (c *linearClient) refreshIfNeeded(ctx context.Context) error {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	if c.tokenExpiry.IsZero() || time.Now().Before(c.tokenExpiry.Add(-60*time.Second)) {
		return nil
	}
	if c.refreshToken == "" {
		return fmt.Errorf("linear: access token expired and no refresh_token on hand")
	}
	form := url.Values{}
	form.Set("refresh_token", c.refreshToken)
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", c.clientID)
	form.Set("client_secret", c.clientSecret)
	tr, err := c.postOAuthToken(ctx, form)
	if err != nil {
		return fmt.Errorf("linear: refresh token: %w", err)
	}
	c.accessToken = tr.AccessToken
	if tr.RefreshToken != "" {
		c.refreshToken = tr.RefreshToken
	}
	if tr.ExpiresIn > 0 {
		c.tokenExpiry = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	}
	if c.onTokenRefresh != nil {
		c.onTokenRefresh(c.accessToken, c.refreshToken, c.tokenExpiry)
	}
	return nil
}

// tokenResponse mirrors Linear's /oauth/token JSON shape.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`  //#nosec G117 -- in-memory bearer, durable store is encrypted
	RefreshToken string `json:"refresh_token"` //#nosec G117 -- in-memory bearer, durable store is encrypted
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
}

// postOAuthToken POSTs a form-urlencoded body to the token endpoint.
func (c *linearClient) postOAuthToken(ctx context.Context, form url.Values) (*tokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient.Do(req) //#nosec G107,G704 -- tokenURL is operator-configured (defaults to Linear's well-known endpoint); body is form-encoded auth payload we built
	if err != nil {
		return nil, fmt.Errorf("post token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, string(body))
	}
	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, fmt.Errorf("decode token response: %w", err)
	}
	return &tr, nil
}

// stderrWriter returns the stderr-like writer used for diagnostic
// output. Mirrors the oauth package pattern.
func stderrWriter() io.Writer {
	return os.Stderr
}
