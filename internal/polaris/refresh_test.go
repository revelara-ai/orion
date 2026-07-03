package polaris

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The client_id used to obtain a token must be PERSISTED on the token — a refresh
// grant needs it, and with dynamic client registration (no ORION_WORKOS_CLIENT_ID)
// it is the ONLY record of the client the refresh token is bound to.
func TestAuthorizePersistsClientID(t *testing.T) {
	mcp, _ := mockWorkOS(t)
	cfg := OAuthConfig{MCPEndpoint: mcp, ClientID: "client-1", OpenBrowser: browserThatRedirects(t, mcp, "c", false)}
	tok, err := cfg.Authorize(context.Background())
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if tok.ClientID != "client-1" {
		t.Errorf("authorized token ClientID = %q, want client-1", tok.ClientID)
	}
}

// A refreshed token must carry the client_id forward (the token endpoint doesn't echo
// it) so the NEXT refresh still works.
func TestRefreshPersistsClientID(t *testing.T) {
	mcp, _ := mockWorkOS(t)
	tok, err := OAuthConfig{MCPEndpoint: mcp, ClientID: "client-1"}.Refresh(context.Background(), "rt-old")
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if tok.ClientID != "client-1" {
		t.Errorf("refreshed token ClientID = %q, want client-1", tok.ClientID)
	}
}

// Token.Expired: a zero ExpiresAt is treated as non-expiring; a past ExpiresAt is
// expired; and the skew makes a token that is about to expire count as expired so we
// refresh slightly early rather than mid-request.
func TestTokenExpired(t *testing.T) {
	base := time.Unix(1_000_000, 0)
	cases := []struct {
		name      string
		expiresAt int64
		want      bool
	}{
		{"zero never expires", 0, false},
		{"well in the future", base.Add(time.Hour).Unix(), false},
		{"already past", base.Add(-time.Minute).Unix(), true},
		{"within skew counts as expired", base.Add(5 * time.Second).Unix(), true},
	}
	for _, c := range cases {
		if got := (Token{ExpiresAt: c.expiresAt}).Expired(base); got != c.want {
			t.Errorf("%s: Expired = %v, want %v", c.name, got, c.want)
		}
	}
}

// EnsureFreshToken refreshes an EXPIRED access token via its refresh_token, persists
// the rotated result (new access + refresh token, preserved client_id), and reports
// refreshed=true. A still-valid token is returned untouched.
func TestEnsureFreshTokenRefreshesExpired(t *testing.T) {
	mcp, forms := mockWorkOS(t)
	dir := t.TempDir()
	ts, err := NewTokenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(2_000_000, 0)
	// Cache an already-expired token that CAN refresh (has refresh token + client id).
	if err := ts.Save(Token{AccessToken: "stale", RefreshToken: "rt-old", ClientID: "client-1", BaseURL: mcp, ExpiresAt: now.Add(-time.Minute).Unix()}); err != nil {
		t.Fatal(err)
	}
	tok, refreshed, err := EnsureFreshToken(context.Background(), ts, mcp, now)
	if err != nil {
		t.Fatalf("ensure fresh: %v", err)
	}
	if !refreshed {
		t.Fatal("expected a refresh to have happened")
	}
	if tok.AccessToken != "at-xyz" {
		t.Errorf("access token = %q, want the refreshed at-xyz", tok.AccessToken)
	}
	if tok.ClientID != "client-1" {
		t.Errorf("client id not preserved across refresh: %q", tok.ClientID)
	}
	// The refresh grant carried the OLD refresh token.
	if f := (*forms)[0]; f.Get("grant_type") != "refresh_token" || f.Get("refresh_token") != "rt-old" {
		t.Errorf("refresh form = %v", f)
	}
	// The rotated token was persisted.
	if saved, _, _ := ts.Load(); saved.AccessToken != "at-xyz" {
		t.Errorf("refreshed token not persisted, saved access = %q", saved.AccessToken)
	}
}

// EnsureFreshToken must NOT touch the network for a token that is still valid.
func TestEnsureFreshTokenLeavesValidAlone(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits++ }))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	ts, _ := NewTokenStore(dir)
	now := time.Unix(3_000_000, 0)
	_ = ts.Save(Token{AccessToken: "good", RefreshToken: "rt", ClientID: "c", BaseURL: srv.URL, ExpiresAt: now.Add(time.Hour).Unix()})
	tok, refreshed, err := EnsureFreshToken(context.Background(), ts, srv.URL, now)
	if err != nil || refreshed {
		t.Fatalf("valid token should not refresh: refreshed=%v err=%v", refreshed, err)
	}
	if tok.AccessToken != "good" || hits != 0 {
		t.Errorf("valid token touched the network (hits=%d) or changed (%q)", hits, tok.AccessToken)
	}
}

// An expired token with no way to refresh (old credential predating client_id
// persistence) returns an actionable error telling the user to re-login.
func TestEnsureFreshTokenUnrefreshableReports(t *testing.T) {
	dir := t.TempDir()
	ts, _ := NewTokenStore(dir)
	now := time.Unix(4_000_000, 0)
	_ = ts.Save(Token{AccessToken: "stale", RefreshToken: "rt", ClientID: "", BaseURL: "https://api.revelara.ai/mcp", ExpiresAt: now.Add(-time.Minute).Unix()})
	_, refreshed, err := EnsureFreshToken(context.Background(), ts, "https://api.revelara.ai/mcp", now)
	if refreshed {
		t.Fatal("must not claim a refresh without a client id")
	}
	if err == nil || !containsAny(err.Error(), "login") {
		t.Fatalf("want an actionable 'login' error, got %v", err)
	}
}
