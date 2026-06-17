// Package github authenticates as a GitHub App, clones repos into
// sandbox workspaces, creates branches with hardcoded or synthesized
// content, and opens pull requests.
//
// v1 implements the thinnest path needed for the Epic 1 round-trip:
// JWT-based App auth, install-token exchange, branch + commit + push via
// git subprocess, REST PR creation, and PR-comment posting. Subsequent
// epics layer real synthesis output, statistical verification reports,
// and reproduction-bundle attachment on top of this scaffolding.
package github

import (
	"bytes"
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// DefaultAPIBaseURL is GitHub's REST API root for SaaS GitHub.
const DefaultAPIBaseURL = "https://api.github.com"

// AppConfig holds the credentials needed to act as a GitHub App
// installation. PrivateKeyPEM is the App's downloaded RSA private key.
type AppConfig struct {
	AppID          int64
	InstallationID int64
	PrivateKeyPEM  []byte
	APIBaseURL     string
	HTTPClient     *http.Client
}

// Validate checks that AppConfig has the fields required to mint an
// installation token. It does NOT verify the credentials work; that
// requires a live API call.
func (c AppConfig) Validate() error {
	if c.AppID == 0 {
		return errors.New("github: AppID required")
	}
	if c.InstallationID == 0 {
		return errors.New("github: InstallationID required")
	}
	if len(c.PrivateKeyPEM) == 0 {
		return errors.New("github: PrivateKeyPEM required")
	}
	return nil
}

// App authenticates as a GitHub App installation. It mints short-lived
// JWTs from the App's private key and exchanges them for installation
// access tokens. Tokens are cached until 30s before expiry.
type App struct {
	cfg AppConfig
	key *rsa.PrivateKey

	cachedToken  string
	cachedExpiry time.Time
}

// NewApp parses the private key and returns a ready App.
func NewApp(cfg AppConfig) (*App, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if cfg.APIBaseURL == "" {
		cfg.APIBaseURL = DefaultAPIBaseURL
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	key, err := jwt.ParseRSAPrivateKeyFromPEM(cfg.PrivateKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("github: parse private key: %w", err)
	}
	return &App{cfg: cfg, key: key}, nil
}

// AppJWT mints a short-lived (10-minute) RS256-signed App JWT. Per
// GitHub docs, JWTs may not have iat in the future and exp may be at
// most 10 minutes ahead of iat; we set iat 60s in the past as clock
// skew tolerance.
func (a *App) AppJWT(now time.Time) (string, error) {
	claims := jwt.RegisteredClaims{
		IssuedAt:  jwt.NewNumericDate(now.Add(-60 * time.Second)),
		ExpiresAt: jwt.NewNumericDate(now.Add(9 * time.Minute)),
		Issuer:    fmt.Sprintf("%d", a.cfg.AppID),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := tok.SignedString(a.key)
	if err != nil {
		return "", fmt.Errorf("github: sign App JWT: %w", err)
	}
	return signed, nil
}

// InstallationToken returns a valid installation access token, minting a
// fresh one if the cached token is missing or near expiry.
func (a *App) InstallationToken(ctx context.Context) (string, error) {
	if a.cachedToken != "" && time.Until(a.cachedExpiry) > 30*time.Second {
		return a.cachedToken, nil
	}
	jwtStr, err := a.AppJWT(time.Now())
	if err != nil {
		return "", err
	}
	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", a.cfg.APIBaseURL, a.cfg.InstallationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+jwtStr)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := a.cfg.HTTPClient.Do(req) //#nosec G107,G704 -- url is built from cfg.APIBaseURL (operator-trusted) plus a static path
	if err != nil {
		return "", fmt.Errorf("github: installation token request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("github: installation token: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("github: decode installation token: %w", err)
	}
	if out.Token == "" {
		return "", errors.New("github: installation token response empty")
	}
	a.cachedToken = out.Token
	a.cachedExpiry = out.ExpiresAt
	return out.Token, nil
}

// doJSON sends a JSON request authenticated with the App installation
// token and decodes the JSON response into out (which may be nil).
func (a *App) doJSON(ctx context.Context, method, url string, body any, out any) error {
	token, err := a.InstallationToken(ctx)
	if err != nil {
		return err
	}
	var reqBody io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("github: encode request: %w", err)
		}
		reqBody = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.cfg.HTTPClient.Do(req) //#nosec G107,G704 -- url is built from cfg.APIBaseURL (operator-trusted) plus a static repos/owner/repo path
	if err != nil {
		return fmt.Errorf("github: %s %s: %w", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("github: %s %s: status %d: %s", method, url, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("github: decode response: %w", err)
		}
	}
	return nil
}
