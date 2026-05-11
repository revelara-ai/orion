package oauth

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// stubAdapter implements TokenRefresher; tests fire the stored
// callback to simulate a rotation.
type stubAdapter struct {
	mu sync.Mutex
	cb func(accessToken, refreshToken string, expiry time.Time)
}

func (s *stubAdapter) SetTokenRefreshCallback(fn func(accessToken, refreshToken string, expiry time.Time)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cb = fn
}

// simulate a rotation. Calls the stored callback synchronously.
func (s *stubAdapter) rotate(access, refresh string, expiry time.Time) {
	s.mu.Lock()
	cb := s.cb
	s.mu.Unlock()
	if cb != nil {
		cb(access, refresh, expiry)
	}
}

// notARefresher: type assertion failure surface.
type notARefresher struct{}

func TestWireRefreshCallback_FiresPersist(t *testing.T) {
	adapter := &stubAdapter{}
	calls := 0
	persist := func(_ context.Context, access, refresh string, expiry time.Time) error {
		calls++
		if access != "new-access" || refresh != "new-refresh" {
			t.Errorf("unexpected tokens: %s %s", access, refresh)
		}
		return nil
	}
	if err := WireRefreshCallback(adapter, persist); err != nil {
		t.Fatalf("WireRefreshCallback: %v", err)
	}
	adapter.rotate("new-access", "new-refresh", time.Now().Add(time.Hour))
	if calls != 1 {
		t.Errorf("persist called %d times, want 1", calls)
	}
}

func TestWireRefreshCallback_RejectsNonRefresher(t *testing.T) {
	err := WireRefreshCallback(&notARefresher{}, func(context.Context, string, string, time.Time) error { return nil })
	if !errors.Is(err, ErrNoRefresher) {
		t.Errorf("err = %v, want ErrNoRefresher", err)
	}
}

func TestWireRefreshCallback_PersistFailureLogged(t *testing.T) {
	// Swap stderr for a buffer so we can assert.
	origStderr := stderr
	buf := &stderrBuf{}
	stderr = buf
	t.Cleanup(func() { stderr = origStderr })

	adapter := &stubAdapter{}
	persist := func(context.Context, string, string, time.Time) error {
		return errors.New("simulated db failure")
	}
	if err := WireRefreshCallback(adapter, persist); err != nil {
		t.Fatal(err)
	}
	adapter.rotate("a", "r", time.Now().Add(time.Hour))
	// Give the callback a moment (it's synchronous but reading the
	// buffer immediately should work).
	if !strings.Contains(buf.String(), "simulated db failure") {
		t.Errorf("expected stderr to contain failure log, got: %q", buf.String())
	}
}
