// Package leader implements per-tenant Conductor leader election with
// fencing tokens (SPEC §14.2).
//
// Each Conductor replica calls AcquireLease(tenantID) to attempt
// leadership for a tenant. The implementation uses
// pg_try_advisory_lock(hashtextextended(tenant_id::text, 0)) which is
// session-scoped: the lock is held by the underlying connection until
// the connection is released or the session ends. AcquireLease holds
// onto its connection for the lease's lifetime so the advisory lock
// stays in effect across calls.
//
// On a successful acquire the fencing_token in orion_leadership is
// incremented monotonically. GuardMutation wraps any state-mutation
// transaction with a pre-commit check that the lease holder's token
// still matches the row in orion_leadership. If it does not (another
// replica has since acquired), the transaction rolls back with
// ErrFencingMismatch and the former leader's in-flight write is
// discarded.
//
// This package is consumed by:
//
//   - orion-e48 (Conductor.Start): one AcquireLease per tenant served
//   - orion-e42 onward (state mutations): every write wraps in
//     GuardMutation so split-brain writes cannot land
package leader

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/revelara-ai/orion/internal/database"
)

// Sentinel errors.
var (
	// ErrLeaseHeld means another replica currently holds the advisory
	// lock for the requested tenant. Callers should back off and retry
	// later (or give up; multi-replica deployments accept this as the
	// happy-path "I'm not the leader for this tenant" signal).
	ErrLeaseHeld = errors.New("leader: tenant lease is already held by another replica")

	// ErrFencingMismatch means the lease holder's fencing_token no
	// longer matches the row in orion_leadership. The lease has been
	// revoked (another replica acquired). Callers MUST treat their
	// in-flight work as discarded.
	ErrFencingMismatch = errors.New("leader: fencing token mismatch; lease revoked")
)

// IsLeaseHeld reports whether err is or wraps ErrLeaseHeld.
func IsLeaseHeld(err error) bool { return errors.Is(err, ErrLeaseHeld) }

// IsFencingMismatch reports whether err is or wraps ErrFencingMismatch.
func IsFencingMismatch(err error) bool { return errors.Is(err, ErrFencingMismatch) }

// AcquireOptions parameterizes AcquireLease.
type AcquireOptions struct {
	// TenantID is the per-tenant scope (SPEC §14.1).
	TenantID uuid.UUID
	// HolderID identifies the replica attempting the acquire. Recorded
	// on the orion_leadership row for operator-facing diagnostics.
	HolderID string
	// LeaseSeconds is the advisory-lock TTL. Default 30 if zero.
	LeaseSeconds int
}

// Lease is an acquired per-tenant Conductor lease. The struct holds
// the underlying connection that owns the session-scoped advisory
// lock; the lock is released only when ReleaseLease is called.
//
// Callers MUST NOT copy a Lease; the connection ownership is non-copy.
type Lease struct {
	TenantID      uuid.UUID
	HolderID      string
	FencingToken  int64
	LeaseSeconds  int
	LastRenewedAt time.Time

	// conn is the pgxpool.Conn that called pg_try_advisory_lock. The
	// lock is session-scoped, so this same conn must be used for
	// pg_advisory_unlock (in ReleaseLease).
	conn *pgxpool.Conn
}

// AcquireLease attempts to take leadership for opts.TenantID. On
// success the returned *Lease holds the underlying connection that
// owns the session-scoped advisory lock; the caller MUST call
// ReleaseLease when done. On contention returns ErrLeaseHeld; the
// caller's connection is released back to the pool automatically.
func AcquireLease(ctx context.Context, pool *database.Pool, opts AcquireOptions) (*Lease, error) {
	if pool == nil {
		return nil, errors.New("leader: nil pool")
	}
	if opts.TenantID == uuid.Nil {
		return nil, errors.New("leader: TenantID is zero")
	}
	leaseSeconds := opts.LeaseSeconds
	if leaseSeconds <= 0 {
		leaseSeconds = 30
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("leader: acquire conn: %w", err)
	}

	var locked bool
	row := conn.QueryRow(ctx,
		`SELECT pg_try_advisory_lock(hashtextextended($1::text, 0))`,
		opts.TenantID.String(),
	)
	if err := row.Scan(&locked); err != nil {
		conn.Release()
		return nil, fmt.Errorf("leader: pg_try_advisory_lock: %w", err)
	}
	if !locked {
		conn.Release()
		return nil, ErrLeaseHeld
	}

	// Upsert + increment fencing_token. The advisory lock prevents
	// concurrent leaders, so the UPSERT race is benign: only one
	// holder is in this code path at a time per tenant.
	var token int64
	var renewedAt time.Time
	err = conn.QueryRow(ctx, `
		INSERT INTO orion_leadership (tenant_id, fencing_token, holder_id, lease_seconds, last_renewed_at)
		VALUES ($1, 1, $2, $3, now())
		ON CONFLICT (tenant_id) DO UPDATE
		SET fencing_token = orion_leadership.fencing_token + 1,
		    holder_id = EXCLUDED.holder_id,
		    lease_seconds = EXCLUDED.lease_seconds,
		    last_renewed_at = now()
		RETURNING fencing_token, last_renewed_at
	`, opts.TenantID, opts.HolderID, leaseSeconds).Scan(&token, &renewedAt)
	if err != nil {
		_, _ = conn.Exec(ctx, `SELECT pg_advisory_unlock(hashtextextended($1::text, 0))`, opts.TenantID.String())
		conn.Release()
		return nil, fmt.Errorf("leader: upsert orion_leadership: %w", err)
	}

	return &Lease{
		TenantID:      opts.TenantID,
		HolderID:      opts.HolderID,
		FencingToken:  token,
		LeaseSeconds:  leaseSeconds,
		LastRenewedAt: renewedAt,
		conn:          conn,
	}, nil
}

// RenewLease bumps last_renewed_at on the orion_leadership row,
// extending the lease without re-acquiring the advisory lock. The
// advisory lock is session-scoped and remains held as long as the
// lease's underlying connection is alive.
//
// If the fencing_token has been advanced by another replica (e.g. an
// operator manually intervened), RenewLease returns ErrFencingMismatch
// and the lease should be treated as revoked.
func RenewLease(ctx context.Context, pool *database.Pool, lease *Lease) error {
	if lease == nil || lease.conn == nil {
		return errors.New("leader: nil lease or already released")
	}
	var renewedAt time.Time
	row := lease.conn.QueryRow(ctx, `
		UPDATE orion_leadership
		SET last_renewed_at = now()
		WHERE tenant_id = $1 AND fencing_token = $2
		RETURNING last_renewed_at
	`, lease.TenantID, lease.FencingToken)
	if err := row.Scan(&renewedAt); err != nil {
		// pgx returns ErrNoRows when zero rows matched.
		return ErrFencingMismatch
	}
	// The pool argument is reserved for future renewal logic that
	// might use a fresh connection (e.g. health checks). Today the
	// renewal stays on the lease's session-scoped connection so the
	// advisory lock isn't lost.
	_ = pool
	lease.LastRenewedAt = renewedAt
	return nil
}

// ReleaseLease unlocks the advisory lock and returns the underlying
// connection to the pool. Safe to call multiple times; the second and
// subsequent calls are no-ops.
//
// ReleaseLease does NOT clear the orion_leadership row. The row stays
// as the historical record of the last leader and its token. A
// subsequent AcquireLease will increment from there.
func ReleaseLease(ctx context.Context, pool *database.Pool, lease *Lease) error {
	if lease == nil || lease.conn == nil {
		return nil
	}
	defer func() {
		lease.conn.Release()
		lease.conn = nil
	}()
	_, err := lease.conn.Exec(ctx,
		`SELECT pg_advisory_unlock(hashtextextended($1::text, 0))`,
		lease.TenantID.String(),
	)
	if err != nil {
		return fmt.Errorf("leader: pg_advisory_unlock: %w", err)
	}
	_ = pool
	return nil
}
