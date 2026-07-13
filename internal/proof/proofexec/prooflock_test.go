package proofexec

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/revelara-ai/orion/internal/proof/prooflock"
)

// TestRunToolQueuesOnTheMachineLock (or-7y68): every proof toolchain exec
// takes the machine-wide singleflight BEFORE running — while another suite
// holds it, RunTool waits politely and surfaces the lock timeout (queueing,
// never a stampede). If the acquire were skipped, the exec would proceed to
// the sandbox and fail (or run) with a DIFFERENT error — the assertion on the
// singleflight message pins the bound itself.
func TestRunToolQueuesOnTheMachineLock(t *testing.T) {
	t.Setenv("ORION_PROOF_LOCK_DIR", t.TempDir())

	release, err := prooflock.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	short, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, _, _, rerr := RunTool(short, t.TempDir(), "go", "version")
	if rerr == nil {
		t.Fatal("RunTool ran while the machine-wide toolchain lock was held — the bound is broken")
	}
	if !strings.Contains(rerr.Error(), "singleflight") {
		t.Fatalf("the failure must be the polite lock wait, got: %v", rerr)
	}

	// Negative (no over-gating): with the lock FREE, RunTool must get PAST the
	// lock — any failure now must NOT be the singleflight (on a sandbox-less
	// machine it may fail with the sandbox refusal; that is out of scope here).
	release()
	free, cancel2 := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel2()
	if _, _, _, rerr := RunTool(free, t.TempDir(), "go", "version"); rerr != nil && strings.Contains(rerr.Error(), "singleflight") {
		t.Fatalf("with the lock free, RunTool must not report a lock wait: %v", rerr)
	}
}
