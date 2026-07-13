// Package prooflock is the machine-wide toolchain singleflight for proof
// executions (or-6wbl, extended by or-7y68). Two concurrent go-toolchain runs
// over generated code false-red each other under load (2026-07-08 contention;
// the or-3ik goroutine dumps showed exactly this stampede) — queueing is
// strictly better than a false verdict. One toolchain exec runs at a time per
// machine, serialized on a flock. Both the brownfield regression gates and the
// greenfield proof execs (proofexec) take THIS lock, so the two domains
// mutually exclude — previously each side only serialized against itself.
//
// Fail-open by design: a missing/unwritable lock dir loses the serialization,
// never the proof.
package prooflock

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// lockDir resolves where the lock file lives: $ORION_PROOF_LOCK_DIR (tests),
// else ~/.orion.
func lockDir() (string, error) {
	if d := os.Getenv("ORION_PROOF_LOCK_DIR"); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".orion"), nil
}

// Acquire blocks (politely, honoring ctx) until this process holds the
// machine-wide toolchain lock; the returned release MUST be called. Lock-file
// failures fail OPEN with a safe no-op release — losing serialization must
// never block a proof.
func Acquire(ctx context.Context) (release func(), err error) {
	dir, derr := lockDir()
	if derr != nil {
		return func() {}, nil // fail open: no home, no lock, proofs still run
	}
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
			return func() {}, fmt.Errorf("proof singleflight: %w while waiting for the machine-wide toolchain lock", ctx.Err())
		case <-time.After(250 * time.Millisecond):
		}
	}
}
