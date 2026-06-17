package repos

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/revelara-ai/orion/internal/database"
)

// RiskSinkPending is one queued risk emission that could not be
// delivered to Polaris in real time. The drain job (E7 / future slice)
// reads these rows and re-attempts; this slice persists them only.
type RiskSinkPending struct {
	ID              uuid.UUID
	OrgID           uuid.UUID
	FindingID       uuid.UUID
	PolarisEndpoint string
	Payload         []byte
	Attempts        int
	LastError       *string
	LastAttemptAt   *time.Time
	RetryAfter      time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// RiskSinkPendingRepo persists §15.3 risk emissions that failed to
// reach Polaris. Per-tenant; uses *database.RLSPool.
type RiskSinkPendingRepo struct {
	pool *database.RLSPool
}

// NewRiskSinkPendingRepo wraps an RLSPool.
func NewRiskSinkPendingRepo(p *database.RLSPool) *RiskSinkPendingRepo {
	return &RiskSinkPendingRepo{pool: p}
}

// Enqueue inserts a pending row. Caller owns the Payload bytes
// (typically the JSON body that failed to POST).
func (r *RiskSinkPendingRepo) Enqueue(ctx context.Context, e RiskSinkPending) (RiskSinkPending, error) {
	const q = `
		INSERT INTO risksink_pending (
			org_id,
			finding_id,
			polaris_endpoint,
			payload,
			attempts,
			last_error,
			last_attempt_at,
			retry_after
		) VALUES (
			current_setting('app.current_organization_id')::uuid,
			$1, $2, $3, $4, $5, $6, $7
		)
		RETURNING id, org_id, created_at, updated_at
	`
	row := r.pool.QueryRow(ctx, q,
		e.FindingID, e.PolarisEndpoint, e.Payload, e.Attempts,
		e.LastError, e.LastAttemptAt, e.RetryAfter,
	)
	if err := row.Scan(&e.ID, &e.OrgID, &e.CreatedAt, &e.UpdatedAt); err != nil {
		return RiskSinkPending{}, fmt.Errorf("repos: enqueue risksink_pending: %w", err)
	}
	return e, nil
}

// CountPending returns the queue depth within the RLS scope. Used by
// observability + the future drain job's loop guard.
func (r *RiskSinkPendingRepo) CountPending(ctx context.Context) (int, error) {
	const q = `SELECT count(*) FROM risksink_pending`
	var n int
	if err := r.pool.QueryRow(ctx, q).Scan(&n); err != nil {
		return 0, fmt.Errorf("repos: count risksink_pending: %w", err)
	}
	return n, nil
}
