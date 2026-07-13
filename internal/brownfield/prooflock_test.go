package brownfield

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestProofLockSerializes (or-6wbl a): two acquirers never hold the lock at
// once; a cancelled waiter unblocks with a named error.
func TestProofLockSerializes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()

	rel1, err := acquireProofLock(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var second atomic.Bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		rel2, err := acquireProofLock(ctx)
		if err != nil {
			t.Error(err)
			return
		}
		second.Store(true)
		rel2()
	}()
	time.Sleep(400 * time.Millisecond)
	if second.Load() {
		t.Fatal("the second acquirer must wait while the first holds the lock")
	}
	rel1()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the waiter must acquire after release")
	}
	if !second.Load() {
		t.Fatal("the second acquirer must eventually hold the lock")
	}

	// A cancelled waiter surfaces the singleflight error.
	rel3, err := acquireProofLock(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rel3()
	cctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	if _, err := acquireProofLock(cctx); err == nil || !strings.Contains(err.Error(), "proof singleflight") {
		t.Fatalf("a cancelled waiter must error with the singleflight reason: %v", err)
	}
}

// TestScopedPerPackageRetryOnTimeout (or-6wbl b): a timed-out package is
// retried once solo (visible in progress); a plainly RED package is NOT
// retried; the overall verdict aggregates per-package results.
func TestScopedPerPackageRetryOnTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ORION_GATE_TEST_TIMEOUT", "1s")
	repo := newScopeRepo(t)
	// slow: sleeps past the 1s gate timeout → times out, twice.
	if err := os.MkdirAll(filepath.Join(repo, "slow"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "slow", "slow_test.go"),
		[]byte("package slow\n\nimport (\n\t\"testing\"\n\t\"time\"\n)\n\nfunc TestSlow(t *testing.T) { time.Sleep(3 * time.Second) }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitAdd := func() {
		_, _ = gitOutput(context.Background(), repo, "add", "-A")
		_, _ = gitOutput(context.Background(), repo, "-c", "user.name=T", "-c", "user.email=t@e.c", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "slow pkg")
	}
	gitAdd()

	var events []string
	prog := Progress(func(step, detail string) { events = append(events, step+": "+detail) })
	res, err := baselineScopedSkip(context.Background(), repo, []string{"./slow/...", "./b/..."}, nil, prog, "green-before")
	if err != nil {
		t.Fatal(err)
	}
	if res.Passed {
		t.Fatal("a package red after its solo retry must red the baseline")
	}
	joined := strings.Join(events, "\n")
	if !strings.Contains(joined, "timed out — retrying once solo") {
		t.Fatalf("the timeout retry must be visible in progress:\n%s", joined)
	}
	if strings.Count(joined, "./b/... timed out") != 0 {
		t.Fatal("the plainly-red package must NOT be retried as a timeout")
	}
}
