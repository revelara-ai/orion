// Forked from github.com/revelara-ai/polaris/internal/connector/registry.go
// at SHA 78d5166b on 2026-05-11. Simplified to the OAuth-only callback
// wiring Orion needs; polaris's larger provider-factory hierarchy is
// out of scope. Pending consolidation per orion-13j.

package oauth

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrNoRefresher signals the caller tried to wire a refresh
// callback into an adapter that doesn't implement TokenRefresher.
var ErrNoRefresher = errors.New("oauth: adapter does not implement TokenRefresher")

// PersistFunc is the callback the registry installs. Given a new
// (accessToken, refreshToken, expiry) tuple, it persists them so
// the next process restart picks up the rotated token instead of
// the now-invalidated original.
//
// org and credentialsRef identify which encrypted_oauth_credential
// row to update. The registry constructs this closure when wiring
// an adapter; tests inject their own.
type PersistFunc func(ctx context.Context, accessToken, refreshToken string, expiresAt time.Time) error

// WireRefreshCallback type-asserts adapter into a TokenRefresher and
// calls SetTokenRefreshCallback with a wrapper that runs persist
// inside a fresh context (the original ctx is request-scoped and
// the rotation may happen on a different request).
//
// Returns ErrNoRefresher if adapter doesn't implement TokenRefresher
// — the caller should treat this as a programming error (every
// rotating-OAuth provider MUST implement TokenRefresher).
func WireRefreshCallback(adapter any, persist PersistFunc) error {
	tr, ok := adapter.(TokenRefresher)
	if !ok {
		return ErrNoRefresher
	}
	tr.SetTokenRefreshCallback(func(accessToken, refreshToken string, expiry time.Time) {
		// Detached context: refresh can happen on a request-scoped
		// ctx that's already cancelled by the time the callback
		// runs. Use Background with a generous deadline.
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := persist(ctx, accessToken, refreshToken, expiry); err != nil {
			// Polaris logs this; we surface to stderr in v1. A future
			// observability epic (E12) replaces stderr with structured
			// logs.
			_, _ = fmt.Fprintf(stderr, "oauth: token rotation persist failed: %v\n", err)
		}
	})
	return nil
}

// stderr is a variable so tests can swap it for an in-memory buffer.
// In production it points to os.Stderr; the package-level init in a
// _stderr.go would normally set it, but for v1 simplicity we declare
// it here and the production wire-up overrides as needed.
var stderr stderrWriter = newDefaultStderr()

type stderrWriter interface {
	Write(p []byte) (int, error)
}
