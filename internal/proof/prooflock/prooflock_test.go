package prooflock

import (
	"context"
	"testing"
	"time"
)

// TestAcquireSerializesAndHonorsCtx (or-7y68): the machine-wide toolchain lock
// is exclusive — while held, a second Acquire blocks until its ctx expires
// (polite queueing, never a false verdict); after release, it succeeds.
func TestAcquireSerializesAndHonorsCtx(t *testing.T) {
	t.Setenv("ORION_PROOF_LOCK_DIR", t.TempDir())
	ctx := context.Background()

	release, err := Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// While held: a bounded second acquire must TIME OUT, not succeed.
	// (flock blocks across fds even in one process, so this is a real exclusivity probe.)
	short, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		r2, err2 := Acquire(short)
		if err2 == nil {
			r2()
		}
		done <- err2
	}()
	select {
	case err2 := <-done:
		if err2 == nil {
			t.Fatal("a second Acquire succeeded while the lock was held — the bound is broken")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the blocked Acquire ignored its ctx deadline")
	}

	// After release: acquire succeeds promptly.
	release()
	quick, cancel2 := context.WithTimeout(ctx, 2*time.Second)
	defer cancel2()
	r3, err := Acquire(quick)
	if err != nil {
		t.Fatalf("acquire after release must succeed: %v", err)
	}
	r3()
}

// A broken lock dir FAILS OPEN: proofs still run (serialization is lost, never
// the proof) — the or-6wbl posture.
func TestAcquireFailsOpen(t *testing.T) {
	t.Setenv("ORION_PROOF_LOCK_DIR", "/proc/definitely/not/writable")
	release, err := Acquire(context.Background())
	if err != nil {
		t.Fatalf("a broken lock dir must fail open, got %v", err)
	}
	release() // and the returned release must be safe to call
}
