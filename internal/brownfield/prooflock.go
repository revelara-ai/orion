package brownfield

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Machine-wide proof singleflight (or-6wbl a): two concurrent regression
// gates false-redded each other (2026-07-08 contention) — queueing is
// strictly better than a false verdict. One suite runs at a time per
// machine, serialized on a flock in ~/.orion.

// acquireProofLock blocks (politely, honoring ctx) until this process holds
// the machine-wide suite lock; the returned release MUST be called.
// Lock-file failures fail OPEN with a nil release-safe func — a missing
// homedir must not block proofs, it just loses the serialization.
func acquireProofLock(ctx context.Context) (release func(), err error) {
	home, herr := os.UserHomeDir()
	if herr != nil {
		return func() {}, nil // fail open: no home, no lock, proofs still run
	}
	dir := filepath.Join(home, ".orion")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return func() {}, nil
	}
	f, ferr := os.OpenFile(filepath.Join(dir, "proof.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if ferr != nil {
		return func() {}, nil
	}
	for {
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
			return func() {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				_ = f.Close()
			}, nil
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return func() {}, fmt.Errorf("proof singleflight: %w while waiting for the machine-wide suite lock", ctx.Err())
		case <-time.After(250 * time.Millisecond):
		}
	}
}
