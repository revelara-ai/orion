package polaris

import (
	"context"
	"fmt"
	"os"
	"syscall"
	"time"
)

// acquireRefreshLock serializes token refresh across processes with a lock
// file beside the credential (flock; single-use refresh tokens make the
// critical section load-bearing, see EnsureFreshToken). Lock infrastructure
// failures fail OPEN — a refresh race is likelier to recover than a wedged
// lock file is, and the caller still holds a refreshable credential.
func acquireRefreshLock(ctx context.Context, ts *TokenStore) (func(), error) {
	path := ts.Path() + ".lock"
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) // #nosec G304 -- adjacent to the credential file
	if err != nil {
		return func() {}, nil // fail open
	}
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				_ = f.Close()
			}, nil
		}
		if err != syscall.EWOULDBLOCK {
			_ = f.Close()
			return func() {}, nil // fail open on infra weirdness
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return func() {}, fmt.Errorf("cancelled waiting for the refresh lock: %w", ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}
