// Package database provides Orion's Postgres harness: a pgx connection
// pool, an RLS-aware wrapper that enforces tenant isolation via PG
// session variables, and a numbered-SQL-file migration runner.
//
// **Pool selection rule** (mirrors polaris's pattern; see polaris
// memory `rls-pool-selection-rule`):
//
//   - PER-TENANT operations (handlers with auth middleware,
//     background jobs processing one org at a time, dispatcher
//     fan-out) → use *RLSPool. It auto-propagates
//     `current_setting('app.current_organization_id')` from ctx, and
//     fail-closes with ErrNoRLSContext when ctx is unset.
//
//   - LEGITIMATELY CROSS-TENANT system queries (migration scripts,
//     reaper jobs that scan all orgs) → use the raw *pgxpool.Pool
//     directly, with `SET LOCAL ROLE` to a privileged role inside a
//     tx. Keep the org filter explicit in SQL.
//
// Using the raw pool for per-tenant queries silently works in tests
// (no RLS policies enforced) and breaks in production (empty
// `current_setting` casts to uuid and returns SQLSTATE 22P02). The
// fail-closed behavior of RLSPool surfaces the missing context at
// the boundary instead of producing an opaque cast error in prod.
package database

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Sentinel errors.
var (
	// ErrNoRLSContext: RLSPool query attempted without RLS values in
	// ctx. Callers MUST seed via WithRLSContext() before calling
	// repository methods.
	ErrNoRLSContext = errors.New("database: no RLS context (call WithRLSContext)")
)

// rlsContextKey is the unexported context-key type used by
// WithRLSContext / readRLSContext. Typed (not raw string) so plain
// `ctx.Value("user_id")` lookups outside this package silently fail
// — the only correct way to populate is WithRLSContext.
type rlsContextKey struct{}

// rlsValues is the payload stored under rlsContextKey.
type rlsValues struct {
	UserID  string
	OrgID   uuid.UUID
	TeamIDs []uuid.UUID
}

// WithRLSContext returns a child ctx that RLSPool reads to populate
// PG session variables before each query.
//
// orgID is the per-tenant scope; the rest of the values are
// available for future SPEC §19.3 policies that scope by user or team.
// v1 policies only use org_id but the helper accepts the others so
// callers wire them once and the policy evolution doesn't require
// caller changes.
func WithRLSContext(ctx context.Context, userID string, orgID uuid.UUID, teamIDs []uuid.UUID) context.Context {
	return context.WithValue(ctx, rlsContextKey{}, rlsValues{
		UserID:  userID,
		OrgID:   orgID,
		TeamIDs: append([]uuid.UUID{}, teamIDs...),
	})
}

// readRLSContext returns the values seeded by WithRLSContext, or
// (zero, false) when ctx has none.
func readRLSContext(ctx context.Context) (rlsValues, bool) {
	v, ok := ctx.Value(rlsContextKey{}).(rlsValues)
	return v, ok
}

// Pool is the raw connection pool. Wraps pgxpool.Pool with helper
// constructors. Callers should NOT use this directly for per-tenant
// queries — use RLSPool. The raw pool is exported for system-level
// queries (migrations, reaper jobs) and tests.
type Pool struct {
	*pgxpool.Pool
}

// NewPool connects to Postgres at dsn and returns a Pool. The caller
// owns Close().
func NewPool(ctx context.Context, dsn string) (*Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("database: parse dsn: %w", err)
	}
	p, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("database: new pool: %w", err)
	}
	if err := p.Ping(ctx); err != nil {
		p.Close()
		return nil, fmt.Errorf("database: ping: %w", err)
	}
	return &Pool{Pool: p}, nil
}

// RLSPool wraps Pool so per-tenant repositories propagate RLS
// session variables from ctx. Every Query/QueryRow/Exec opens a
// tx, SETs the session variables, executes the caller's SQL, then
// commits.
//
// MUST be used by every repository that operates on RLS-protected
// tables (anything with an `org_id` column). Calls without a
// preceding WithRLSContext return ErrNoRLSContext.
type RLSPool struct {
	pool *Pool
}

// NewRLSPool wraps a Pool. The wrapped Pool's underlying pgxpool is
// still accessible via .Raw() for the rare cross-tenant case.
func NewRLSPool(p *Pool) *RLSPool {
	return &RLSPool{pool: p}
}

// Raw returns the underlying *pgxpool.Pool for legitimate
// cross-tenant queries. Callers MUST use SET LOCAL ROLE in a tx and
// keep the org filter explicit in SQL.
func (r *RLSPool) Raw() *pgxpool.Pool {
	return r.pool.Pool
}

// Query executes sql with args inside an RLS-scoped tx and returns
// the rows. Caller MUST call rows.Close() when done. Returns
// ErrNoRLSContext if ctx lacks WithRLSContext values.
//
// Read-path semantics: rows.Close() rolls back the surrounding tx.
// SET LOCAL ROLE + session vars are tx-scoped so rollback drops them
// cleanly. For DML (INSERT/UPDATE/DELETE … RETURNING) use QueryRow,
// which commits via singleRow.Scan.
func (r *RLSPool) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	tc, err := r.beginRLSTx(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tc.tx.Query(ctx, sql, args...)
	if err != nil {
		_ = tc.tx.Rollback(ctx)
		return nil, err
	}
	tc.Rows = rows
	return tc, nil
}

// QueryRow runs sql with args inside an RLS-scoped tx and returns
// the single row. The row's Scan commits the tx so INSERT/UPDATE/
// DELETE … RETURNING side-effects persist; failure rolls back.
func (r *RLSPool) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	tc, err := r.beginRLSTx(ctx)
	if err != nil {
		return errRow{err: err}
	}
	rows, err := tc.tx.Query(ctx, sql, args...)
	if err != nil {
		_ = tc.tx.Rollback(ctx)
		return errRow{err: err}
	}
	tc.Rows = rows
	return &singleRow{rows: tc}
}

// beginRLSTx opens a tx, applies SET LOCAL ROLE orion_api so the
// query runs as a non-superuser (RLS-enforced), and sets the session
// variables the RLS policies consult. Returns the wrapping
// rowsTxCloser with Rows unset; the caller fills it in.
func (r *RLSPool) beginRLSTx(ctx context.Context) (*rowsTxCloser, error) {
	v, ok := readRLSContext(ctx)
	if !ok {
		return nil, ErrNoRLSContext
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("database: begin tx: %w", err)
	}
	if err := setSessionVars(ctx, tx, v); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return &rowsTxCloser{tx: tx, ctx: ctx}, nil
}

// Exec runs sql with args inside an RLS-scoped tx and commits.
func (r *RLSPool) Exec(ctx context.Context, sql string, args ...any) (pgxResult, error) {
	tc, err := r.beginRLSTx(ctx)
	if err != nil {
		return pgxResult{}, err
	}
	tx := tc.tx
	tag, err := tx.Exec(ctx, sql, args...)
	if err != nil {
		_ = tx.Rollback(ctx)
		return pgxResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return pgxResult{}, fmt.Errorf("database: commit: %w", err)
	}
	return pgxResult{tag: tag}, nil
}

// pgxResult mirrors pgconn.CommandTag's surface so callers can read
// RowsAffected() without importing pgconn directly.
type pgxResult struct {
	tag pgxCommandTag
}

// pgxCommandTag is an interface that abstracts pgconn.CommandTag so
// the pool's Exec doesn't leak a pgconn import to callers.
type pgxCommandTag interface {
	RowsAffected() int64
}

// RowsAffected delegates.
func (r pgxResult) RowsAffected() int64 {
	if r.tag == nil {
		return 0
	}
	return r.tag.RowsAffected()
}

// setSessionVars sets the runtime role + RLS session variables for
// the current tx. The role switch is what makes RLS load-bearing
// when the underlying connection is a superuser (testcontainers,
// local dev). SET LOCAL ROLE is tx-scoped and reverts on
// commit/rollback. set_config(..., is_local=true) is the
// parameterizable form of SET LOCAL for the app.* variables.
func setSessionVars(ctx context.Context, tx pgx.Tx, v rlsValues) error {
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE orion_api`); err != nil {
		return fmt.Errorf("database: set role: %w", err)
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_organization_id', $1, true)", v.OrgID.String()); err != nil {
		return fmt.Errorf("database: set org_id: %w", err)
	}
	if v.UserID != "" {
		if _, err := tx.Exec(ctx, "SELECT set_config('app.current_user_id', $1, true)", v.UserID); err != nil {
			return fmt.Errorf("database: set user_id: %w", err)
		}
	}
	return nil
}
