package polaris

import (
	"context"
	"fmt"
	"time"
)

// tokenSkew refreshes a token slightly BEFORE its stated expiry, so a token that is
// about to lapse is renewed rather than being rejected mid-request. WorkOS AuthKit
// access tokens live only ~5 minutes, so this path runs often by design.
const tokenSkew = 30 * time.Second

// Expired reports whether the access token is at (or within tokenSkew of) its expiry.
// A zero ExpiresAt means unknown / non-expiring and is treated as never expired.
func (t Token) Expired(now time.Time) bool {
	if t.ExpiresAt == 0 {
		return false
	}
	return !now.Before(time.Unix(t.ExpiresAt, 0).Add(-tokenSkew))
}

// EnsureFreshToken returns a usable token for the MCP endpoint, refreshing an expired
// (or nearly-expired) access token via its refresh token and PERSISTING the rotated
// result. now is injected for testability.
//
// It reports refreshed=true only when it actually obtained a new token. It returns an
// error only when a refresh was REQUIRED but could not be performed or failed — in
// particular, an expired token with no client id (an old credential predating client-
// id persistence) yields an actionable "run login" error. A still-valid token, or the
// absence of any credential, is returned untouched with a nil error.
func EnsureFreshToken(ctx context.Context, ts *TokenStore, endpoint string, now time.Time) (Token, bool, error) {
	tok, ok, err := ts.Load()
	if err != nil {
		return Token{}, false, err
	}
	if !ok || tok.AccessToken == "" {
		return tok, false, nil // nothing cached — caller runs without the surface
	}
	if !tok.Expired(now) {
		return tok, false, nil // still valid — no network
	}
	if tok.RefreshToken == "" || tok.ClientID == "" {
		return tok, false, fmt.Errorf("revelara.ai session expired and cannot be refreshed automatically — run /mcp login")
	}

	fresh, rerr := OAuthConfig{MCPEndpoint: endpoint, ClientID: tok.ClientID}.Refresh(ctx, tok.RefreshToken)
	if rerr != nil {
		return tok, false, fmt.Errorf("revelara.ai token refresh failed (run /mcp login): %w", rerr)
	}
	// Carry forward fields the token endpoint does not echo back, and keep the old
	// refresh token if the server did not rotate one.
	fresh.ClientID = tok.ClientID
	if fresh.RefreshToken == "" {
		fresh.RefreshToken = tok.RefreshToken
	}
	if fresh.Org == "" {
		fresh.Org = tok.Org
	}
	if fresh.BaseURL == "" {
		fresh.BaseURL = tok.BaseURL
	}
	// Best-effort persist: even if the write fails we still hand back the usable token
	// for THIS turn (the next turn simply refreshes again).
	_ = ts.Save(fresh)
	return fresh, true, nil
}
