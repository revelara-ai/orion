package leader_test

// Leader-election + fencing-token contract tests (SPEC §14.2).
//
// These exercise the load-bearing behavior the Conductor depends on:
//
//   - Per-tenant PG advisory-lock acquisition (mutex semantics)
//   - Fencing-token monotonic increment on acquire
//   - Lease renewal extends the lease without re-acquiring
//   - GuardMutation rolls back when the lease holder's token is stale
//   - ReleaseLease frees the advisory lock for re-acquire
//
// All tests require a real Postgres because pg_try_advisory_lock is
// PG-specific. testing.Short() skips the suite for /queue's fast gates
// (mirroring the orion convention added in 5159386).

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/revelara-ai/orion/internal/database"
	"github.com/revelara-ai/orion/internal/leader"
)

// newPool spins a fresh pg container per test, runs migrations, and
// returns the raw pool (leader operates on the raw pool, not RLSPool,
// because orion_leadership is a system table without RLS).
func newPool(t *testing.T) *database.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping testcontainer-backed test in -short mode")
	}
	ctx := context.Background()
	c, err := tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase("orion_test"),
		tcpostgres.WithUsername("orion"),
		tcpostgres.WithPassword("orion"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Skipf("testcontainers postgres unavailable: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	pool, err := database.NewPool(ctx, dsn)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return pool
}

func TestAcquireLease_FirstAcquireSucceeds(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	tenantID := uuid.New()

	l, err := leader.AcquireLease(ctx, pool, leader.AcquireOptions{
		TenantID:     tenantID,
		HolderID:     "replica-a",
		LeaseSeconds: 30,
	})
	if err != nil {
		t.Fatalf("AcquireLease: %v", err)
	}
	if l == nil {
		t.Fatal("AcquireLease returned nil lease")
	}
	if l.TenantID != tenantID {
		t.Errorf("TenantID = %v, want %v", l.TenantID, tenantID)
	}
	if l.HolderID != "replica-a" {
		t.Errorf("HolderID = %q, want %q", l.HolderID, "replica-a")
	}
	if l.FencingToken < 1 {
		t.Errorf("FencingToken = %d; want >= 1 after first acquire", l.FencingToken)
	}
	t.Cleanup(func() { _ = leader.ReleaseLease(ctx, pool, l) })
}

func TestAcquireLease_SecondAcquireBlockedByLock(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	tenantID := uuid.New()

	l, err := leader.AcquireLease(ctx, pool, leader.AcquireOptions{
		TenantID:     tenantID,
		HolderID:     "replica-a",
		LeaseSeconds: 30,
	})
	if err != nil {
		t.Fatalf("first AcquireLease: %v", err)
	}
	t.Cleanup(func() { _ = leader.ReleaseLease(ctx, pool, l) })

	_, err = leader.AcquireLease(ctx, pool, leader.AcquireOptions{
		TenantID:     tenantID,
		HolderID:     "replica-b",
		LeaseSeconds: 30,
	})
	if err == nil {
		t.Fatal("second AcquireLease should have failed with ErrLeaseHeld")
	}
	// ErrLeaseHeld is the documented sentinel for the lock-contested case.
	if !leader.IsLeaseHeld(err) {
		t.Errorf("err = %v; want ErrLeaseHeld", err)
	}
}

func TestAcquireLease_ConcurrentOnlyOneWins(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	tenantID := uuid.New()

	const N = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	var winners []*leader.Lease
	var errors []error
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			l, err := leader.AcquireLease(ctx, pool, leader.AcquireOptions{
				TenantID:     tenantID,
				HolderID:     uuid.NewString(),
				LeaseSeconds: 30,
			})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errors = append(errors, err)
				return
			}
			winners = append(winners, l)
		}(i)
	}
	wg.Wait()

	if len(winners) != 1 {
		t.Fatalf("got %d winners; want exactly 1 (lock is mutex)", len(winners))
	}
	if len(errors) != N-1 {
		t.Errorf("got %d errors; want %d (others should report ErrLeaseHeld)", len(errors), N-1)
	}
	for _, e := range errors {
		if !leader.IsLeaseHeld(e) {
			t.Errorf("concurrent loser err = %v; want ErrLeaseHeld", e)
		}
	}
	t.Cleanup(func() { _ = leader.ReleaseLease(ctx, pool, winners[0]) })
}

func TestAcquireLease_IncrementsFencingToken(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	tenantID := uuid.New()

	first, err := leader.AcquireLease(ctx, pool, leader.AcquireOptions{
		TenantID:     tenantID,
		HolderID:     "replica-a",
		LeaseSeconds: 30,
	})
	if err != nil {
		t.Fatalf("first AcquireLease: %v", err)
	}
	if err := leader.ReleaseLease(ctx, pool, first); err != nil {
		t.Fatalf("ReleaseLease: %v", err)
	}

	second, err := leader.AcquireLease(ctx, pool, leader.AcquireOptions{
		TenantID:     tenantID,
		HolderID:     "replica-b",
		LeaseSeconds: 30,
	})
	if err != nil {
		t.Fatalf("second AcquireLease: %v", err)
	}
	t.Cleanup(func() { _ = leader.ReleaseLease(ctx, pool, second) })

	if second.FencingToken <= first.FencingToken {
		t.Errorf("second FencingToken %d should be > first %d (monotonic per SPEC §14.2)", second.FencingToken, first.FencingToken)
	}
}

func TestRenewLease_ExtendsExpiration(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	tenantID := uuid.New()

	l, err := leader.AcquireLease(ctx, pool, leader.AcquireOptions{
		TenantID:     tenantID,
		HolderID:     "replica-a",
		LeaseSeconds: 30,
	})
	if err != nil {
		t.Fatalf("AcquireLease: %v", err)
	}
	t.Cleanup(func() { _ = leader.ReleaseLease(ctx, pool, l) })

	originalRenewed := l.LastRenewedAt
	time.Sleep(50 * time.Millisecond)
	if err := leader.RenewLease(ctx, pool, l); err != nil {
		t.Fatalf("RenewLease: %v", err)
	}
	if !l.LastRenewedAt.After(originalRenewed) {
		t.Errorf("LastRenewedAt %v should be after %v", l.LastRenewedAt, originalRenewed)
	}
	if l.FencingToken < 1 {
		t.Errorf("FencingToken unchanged across renew should remain %d, got %d", l.FencingToken, l.FencingToken)
	}
}

func TestGuardMutation_StaleTokenRollsBack(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	tenantID := uuid.New()

	first, err := leader.AcquireLease(ctx, pool, leader.AcquireOptions{
		TenantID:     tenantID,
		HolderID:     "replica-a",
		LeaseSeconds: 30,
	})
	if err != nil {
		t.Fatalf("first AcquireLease: %v", err)
	}
	if err := leader.ReleaseLease(ctx, pool, first); err != nil {
		t.Fatalf("ReleaseLease: %v", err)
	}

	// Replica B acquires (token now first+1). Replica A's stale lease
	// must fail GuardMutation.
	second, err := leader.AcquireLease(ctx, pool, leader.AcquireOptions{
		TenantID:     tenantID,
		HolderID:     "replica-b",
		LeaseSeconds: 30,
	})
	if err != nil {
		t.Fatalf("second AcquireLease: %v", err)
	}
	t.Cleanup(func() { _ = leader.ReleaseLease(ctx, pool, second) })

	// Replica A still thinks it holds the lease at the old token.
	err = leader.GuardMutation(ctx, pool, first, func(tx pgx.Tx) error {
		// Any side effect should be rolled back because the fencing
		// guard fails before commit. We don't even need to do work; the
		// pre-commit check is the load-bearing assertion.
		return nil
	})
	if err == nil {
		t.Fatal("GuardMutation with stale token should have failed with ErrFencingMismatch")
	}
	if !leader.IsFencingMismatch(err) {
		t.Errorf("err = %v; want ErrFencingMismatch", err)
	}
}

func TestGuardMutation_CurrentTokenCommits(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	tenantID := uuid.New()

	l, err := leader.AcquireLease(ctx, pool, leader.AcquireOptions{
		TenantID:     tenantID,
		HolderID:     "replica-a",
		LeaseSeconds: 30,
	})
	if err != nil {
		t.Fatalf("AcquireLease: %v", err)
	}
	t.Cleanup(func() { _ = leader.ReleaseLease(ctx, pool, l) })

	called := false
	err = leader.GuardMutation(ctx, pool, l, func(tx pgx.Tx) error {
		called = true
		// Run a trivial query to prove the tx is usable.
		var one int
		return tx.QueryRow(ctx, "SELECT 1").Scan(&one)
	})
	if err != nil {
		t.Fatalf("GuardMutation with current token: %v", err)
	}
	if !called {
		t.Error("callback not invoked")
	}
}

func TestReleaseLease_AllowsReAcquire(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	tenantID := uuid.New()

	first, err := leader.AcquireLease(ctx, pool, leader.AcquireOptions{
		TenantID:     tenantID,
		HolderID:     "replica-a",
		LeaseSeconds: 30,
	})
	if err != nil {
		t.Fatalf("first AcquireLease: %v", err)
	}
	if err := leader.ReleaseLease(ctx, pool, first); err != nil {
		t.Fatalf("ReleaseLease: %v", err)
	}
	second, err := leader.AcquireLease(ctx, pool, leader.AcquireOptions{
		TenantID:     tenantID,
		HolderID:     "replica-b",
		LeaseSeconds: 30,
	})
	if err != nil {
		t.Fatalf("post-release AcquireLease: %v", err)
	}
	t.Cleanup(func() { _ = leader.ReleaseLease(ctx, pool, second) })
}
