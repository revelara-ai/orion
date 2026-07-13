package polaris

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client talks to the Polaris auth endpoints.
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

// NewClient returns a client for a Polaris base URL with a bounded timeout
// (every external call has a per-call timeout, per Harness Reliability).
func NewClient(baseURL string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTP:    &http.Client{Timeout: 15 * time.Second},
	}
}

// Identity is the authenticated principal (GET /api/v1/auth/me).
type Identity struct {
	Email string `json:"email"`
	Org   string `json:"org"`
}

// Login exchanges credentials for a token (POST /api/v1/auth/login).
func (c *Client) Login(ctx context.Context, username, password string) (Token, error) {
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/v1/auth/login", bytes.NewReader(body))
	if err != nil {
		return Token{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Token{}, fmt.Errorf("polaris login: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<14))
		return Token{}, fmt.Errorf("polaris login: status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var out struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Token{}, fmt.Errorf("polaris login: decode: %w", err)
	}
	if out.AccessToken == "" {
		return Token{}, fmt.Errorf("polaris login: no access token in response")
	}
	return Token{AccessToken: out.AccessToken, RefreshToken: out.RefreshToken, BaseURL: c.BaseURL}, nil
}

// Me verifies a token and returns the identity (GET /api/v1/auth/me).
func (c *Client) Me(ctx context.Context, accessToken string) (Identity, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/api/v1/auth/me", nil)
	if err != nil {
		return Identity{}, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Identity{}, fmt.Errorf("polaris me: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Identity{}, fmt.Errorf("polaris me: status %d", resp.StatusCode)
	}
	var id Identity
	if err := json.NewDecoder(resp.Body).Decode(&id); err != nil {
		return Identity{}, fmt.Errorf("polaris me: decode: %w", err)
	}
	return id, nil
}

// Logout invalidates the server session (POST /api/v1/auth/logout); best-effort.
func (c *Client) Logout(ctx context.Context, accessToken string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/v1/auth/logout", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("polaris logout: %w", err)
	}
	_ = resp.Body.Close()
	return nil
}
