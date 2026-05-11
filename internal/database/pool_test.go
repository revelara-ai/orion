package database

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// newPgContainer spins up an ephemeral postgres for one test. The
// container is registered via t.Cleanup so it tears down when the
// test exits (success or fail).
func newPgContainer(t *testing.T) string {
	t.Helper()
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
	t.Cleanup(func() {
		_ = c.Terminate(ctx)
	})
	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	return dsn
}

// TestRLSPoolFailsClosedWithoutContext is the load-bearing test for
// this package: a query without WithRLSContext MUST return
// ErrNoRLSContext. This is the polaris-pattern fail-closed
// invariant.
func TestRLSPoolFailsClosedWithoutContext(t *testing.T) {
	dsn := newPgContainer(t)
	ctx := context.Background()
	pool, err := NewPool(ctx, dsn)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Close()

	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	rls := NewRLSPool(pool)
	_, err = rls.Query(context.Background(), "SELECT 1")
	if !errors.Is(err, ErrNoRLSContext) {
		t.Errorf("Query without ctx: err = %v, want ErrNoRLSContext", err)
	}

	_, err = rls.Exec(context.Background(), "SELECT 1")
	if !errors.Is(err, ErrNoRLSContext) {
		t.Errorf("Exec without ctx: err = %v, want ErrNoRLSContext", err)
	}
}

// TestRLSPoolPropagatesOrgID verifies that WithRLSContext + a query
// against an RLS-enabled table sees only that org's rows.
func TestRLSPoolPropagatesOrgID(t *testing.T) {
	dsn := newPgContainer(t)
	ctx := context.Background()
	pool, err := NewPool(ctx, dsn)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Close()
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	orgA := uuid.New()
	orgB := uuid.New()

	// Seed: insert one connected_repo for orgA and one for orgB via
	// the raw pool with SET LOCAL ROLE-ish escape. We're testing the
	// RLS POLICIES here, not the repo code, so use raw INSERTs that
	// bypass RLS by setting the session variable manually for each.
	for _, org := range []uuid.UUID{orgA, orgB} {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('app.current_organization_id', $1, true)`, org.String()); err != nil {
			t.Fatalf("set_config: %v", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO connected_repo (org_id, provider, app_install_id, repo_full_name)
			VALUES ($1, 'github', 'install-1', 'org/repo')
		`, org); err != nil {
			t.Fatalf("insert: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit: %v", err)
		}
	}

	// Query as orgA — should see exactly 1 row.
	rls := NewRLSPool(pool)
	ctxA := WithRLSContext(ctx, "test-user", orgA, nil)
	rows, err := rls.Query(ctxA, `SELECT id FROM connected_repo`)
	if err != nil {
		t.Fatalf("Query as orgA: %v", err)
	}
	count := 0
	for rows.Next() {
		count++
	}
	rows.Close()
	if count != 1 {
		t.Errorf("orgA sees %d rows, want 1", count)
	}

	// Query as orgB — should also see exactly 1 row (its own).
	ctxB := WithRLSContext(ctx, "test-user", orgB, nil)
	rows, err = rls.Query(ctxB, `SELECT id FROM connected_repo`)
	if err != nil {
		t.Fatalf("Query as orgB: %v", err)
	}
	count = 0
	for rows.Next() {
		count++
	}
	rows.Close()
	if count != 1 {
		t.Errorf("orgB sees %d rows, want 1", count)
	}
}

// TestMigrateIsIdempotent: running Migrate twice doesn't blow up
// (no double-CREATE TABLE errors).
func TestMigrateIsIdempotent(t *testing.T) {
	dsn := newPgContainer(t)
	ctx := context.Background()
	pool, err := NewPool(ctx, dsn)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Close()

	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
}

// TestMigrateRecordsAppliedFiles: after Migrate, schema_migrations
// has one row per up.sql file.
func TestMigrateRecordsAppliedFiles(t *testing.T) {
	dsn := newPgContainer(t)
	ctx := context.Background()
	pool, err := NewPool(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	rows, err := pool.Query(ctx, `SELECT name FROM schema_migrations ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	if len(names) < 3 {
		t.Errorf("expected >= 3 applied migrations, got %d: %v", len(names), names)
	}
}
