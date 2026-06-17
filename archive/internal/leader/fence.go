package leader

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/revelara-ai/orion/internal/database"
)

// GuardMutation runs fn inside a transaction whose pre-commit check
// verifies that lease.FencingToken still matches the orion_leadership
// row. If the token has been advanced by another replica, the tx
// rolls back and the function returns ErrFencingMismatch.
//
// This is the only safe way for the Conductor to land a state
// mutation per SPEC §14.2: every write must carry the fencing-token
// guard so that a former leader's straggler writes do not corrupt
// the run state after handover.
//
// The fn callback receives the pgx.Tx so it can issue UPDATE / INSERT
// statements that share the same transaction with the fencing check.
// Errors returned by fn cause the tx to roll back and propagate to
// the caller.
//
// Implementation note: the fencing check runs BEFORE fn so callers do
// not waste work when the lease is already stale, and AGAIN at commit
// (implicit through the SELECT ... FOR UPDATE row lock held for the
// duration of the tx) so a concurrent AcquireLease cannot slip in
// after fn returns but before we commit.
func GuardMutation(ctx context.Context, pool *database.Pool, lease *Lease, fn func(tx pgx.Tx) error) error {
	if pool == nil {
		return errors.New("leader: nil pool")
	}
	if lease == nil {
		return errors.New("leader: nil lease")
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("leader: begin tx: %w", err)
	}
	defer func() {
		// Best-effort rollback in case fn returned an error or the
		// commit path errored. Safe to call after Commit; pgx makes it
		// a no-op.
		_ = tx.Rollback(ctx)
	}()

	// Lock the row for the duration of the tx so a concurrent
	// AcquireLease cannot change the fencing token until we commit.
	var currentToken int64
	err = tx.QueryRow(ctx,
		`SELECT fencing_token FROM orion_leadership WHERE tenant_id = $1 FOR UPDATE`,
		lease.TenantID,
	).Scan(&currentToken)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrFencingMismatch
		}
		return fmt.Errorf("leader: select fencing_token: %w", err)
	}
	if currentToken != lease.FencingToken {
		return ErrFencingMismatch
	}

	if err := fn(tx); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("leader: commit: %w", err)
	}
	return nil
}
