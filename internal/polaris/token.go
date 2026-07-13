// Package polaris is Orion's bidirectional Polaris client (or-bzn, PRD Polaris
// API Contract). V2.0 implements the auth surface (login/logout/status) and
// credential storage. The full client is generated from the Polaris OpenAPI spec
// (oapi-codegen) in production so the contract never drifts; this hand-written
// auth slice is the V2.0 starting point.
//
// Security (PRD): the platform token lives in the OS keychain where available,
// else an encrypted/0600 file — NEVER in the Context Store (agents/recall touch
// it) and NEVER reachable from inside the sandbox.
package polaris

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Token is the stored credential.
type Token struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	BaseURL      string `json:"base_url"`
	Org          string `json:"org,omitempty"`
	// ClientID is the OAuth client the token was issued to. It MUST be persisted: a
	// refresh grant requires it, and under dynamic client registration (no
	// ORION_WORKOS_CLIENT_ID) it is the only record of the client the refresh token is
	// bound to. Without it an expired token can never be refreshed silently.
	ClientID  string `json:"client_id,omitempty"`
	ExpiresAt int64  `json:"expires_at,omitempty"` // unix seconds; 0 = unknown/non-expiring
}

// TokenStore persists the credential to a 0600 file, deliberately SEPARATE from
// the Context Store DB. The credentials directory must live outside any path the
// sandbox binds, so the token is unreachable from inside the sandbox.
type TokenStore struct {
	dir string
}

// NewTokenStore stores credentials under dir (created 0700). Callers pass a
// directory that is NOT the worktree and NOT bound into the sandbox.
func NewTokenStore(dir string) (*TokenStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("polaris: credentials dir: %w", err)
	}
	return &TokenStore{dir: dir}, nil
}

// Path is the credential file path.
func (s *TokenStore) Path() string { return filepath.Join(s.dir, "credentials.json") }

// Save writes the token with 0600 perms (owner read/write only).
func (s *TokenStore) Save(t Token) error {
	b, err := json.Marshal(t) // #nosec G117 -- this IS the credential store (0600 file, outside sandbox binds)
	if err != nil {
		return err
	}
	// Write 0600 atomically (temp + rename) so a crash never leaves a partial creds file.
	tmp := s.Path() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("polaris: write token: %w", err)
	}
	if err := os.Rename(tmp, s.Path()); err != nil {
		return fmt.Errorf("polaris: persist token: %w", err)
	}
	return nil
}

// Load reads the stored token. Returns ok=false when no credential is stored.
func (s *TokenStore) Load() (Token, bool, error) {
	b, err := os.ReadFile(s.Path())
	if os.IsNotExist(err) {
		return Token{}, false, nil
	}
	if err != nil {
		return Token{}, false, fmt.Errorf("polaris: read token: %w", err)
	}
	var t Token
	if err := json.Unmarshal(b, &t); err != nil {
		return Token{}, false, fmt.Errorf("polaris: parse token: %w", err)
	}
	return t, t.AccessToken != "", nil
}

// Clear erases the cached credential (orion logout).
func (s *TokenStore) Clear() error {
	err := os.Remove(s.Path())
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
