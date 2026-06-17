// Forked from github.com/revelara-ai/polaris/internal/connector/connector.go
// at SHA 78d5166b on 2026-05-11. Pending consolidation per orion-13j.
//
// Trimmed to the OAuth-only surface Orion needs for Linear (E2-4)
// and future OAuth-rotation-aware providers. polaris's larger
// Connector + DocSyncer + IssueExporter + EventSource hierarchy is
// out of scope here — Orion uses its own internal/trackers.TrackerAdapter
// for the same role.

// Package oauth provides the OAuth2 + rotating-refresh-token
// persistence substrate that polaris's connector framework solved
// once and Orion needs for Linear (E2-4) and future OAuth providers.
//
// The load-bearing contract: TokenRefresher.SetTokenRefreshCallback.
// Adapters that perform in-band token refresh MUST invoke the
// callback whenever they receive a new refresh_token. The Registry
// (registry.go) wires a callback that encrypts + persists the new
// token to the database, so the next refresh attempt 24h later
// doesn't 400 with "invalid_grant: refresh token revoked".
//
// See polaris's `oauth-token-refresh-callback` memory for the
// failure mode and the
// `atlassian-oauth-refresh-tokens-are-rotating-each-refresh` memory
// for why this matters specifically for JIRA/Atlassian; Linear
// historically didn't rotate (but the contract is the same shape
// so we wire it regardless).
package oauth

import (
	"context"
	"time"
)

// OAuthTokens holds tokens returned by an OAuth callback. Extra
// carries provider-specific fields the callback handler persists
// alongside the tokens (e.g. Linear's workspace_id).
//
// These fields ARE secrets in the strict sense; gosec G117 flags
// the names. The token-stored-at-rest path uses the encrypted
// table (encrypted_oauth_credential) so plaintext is only held
// transiently in this struct in-memory.
type OAuthTokens struct {
	AccessToken  string         `json:"access_token"`            //#nosec G117 -- in-memory bearer; persisted only via encrypted credentials.Manager
	RefreshToken string         `json:"refresh_token,omitempty"` //#nosec G117 -- in-memory bearer; persisted only via encrypted credentials.Manager
	ExpiresAt    *time.Time     `json:"expires_at,omitempty"`
	Scope        string         `json:"scope,omitempty"`
	Extra        map[string]any `json:"extra,omitempty"`
}

// OAuthConnector is implemented by adapters that complete an OAuth
// dance (authorize URL + callback exchange). Linear (E2-4) implements
// this for its OAuth2 install flow.
type OAuthConnector interface {
	// AuthorizeURL returns the provider's OAuth authorize URL with the
	// given state token. The caller redirects the user here.
	AuthorizeURL(state string) string

	// HandleCallback exchanges the OAuth code for tokens. Returns the
	// freshly-issued OAuthTokens; the caller persists via the
	// credentials Manager.
	HandleCallback(ctx context.Context, code string) (*OAuthTokens, error)
}

// TokenRefresher is implemented by adapters that perform in-band
// OAuth refresh and need to surface rotated tokens back to the
// persistent store. The Registry (registry.go) wires this
// automatically so providers like Linear (and future rotating
// providers like JIRA, Notion) never silently lose the new
// refresh_token.
type TokenRefresher interface {
	SetTokenRefreshCallback(fn func(accessToken, refreshToken string, expiry time.Time))
}
