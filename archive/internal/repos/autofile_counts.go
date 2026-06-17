package repos

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/revelara-ai/orion/internal/database"
)

// AutoFileEntry is one row of the §8.7 cap audit trail.
type AutoFileEntry struct {
	ID      uuid.UUID
	OrgID   uuid.UUID
	RunID   string
	IssueID *uuid.UUID
	Pattern string
	FiledAt time.Time
}

// AutoFileCountsRepo is the cap-math surface for SPEC §8.7.
type AutoFileCountsRepo struct {
	pool *database.RLSPool
}

// NewAutoFileCountsRepo wraps an RLSPool.
func NewAutoFileCountsRepo(p *database.RLSPool) *AutoFileCountsRepo {
	return &AutoFileCountsRepo{pool: p}
}

// Record inserts an autofile audit row. Callers MUST invoke this
// AFTER adapter.Create succeeds so the cap math counts only
// successfully-filed issues.
func (r *AutoFileCountsRepo) Record(ctx context.Context, e AutoFileEntry) error {
	const q = `
		INSERT INTO autofile_counts
		    (org_id, run_id, issue_id, pattern)
		VALUES
		    (current_setting('app.current_organization_id')::uuid,
		     $1, $2, $3)
	`
	_, err := r.pool.Exec(ctx, q, e.RunID, e.IssueID, e.Pattern)
	if err != nil {
		return fmt.Errorf("repos: record autofile: %w", err)
	}
	return nil
}

// CountByRun returns the number of issues filed under the given
// run_id within the caller's RLS scope.
func (r *AutoFileCountsRepo) CountByRun(ctx context.Context, runID string) (int, error) {
	const q = `SELECT COUNT(*) FROM autofile_counts WHERE run_id = $1`
	var n int
	if err := r.pool.QueryRow(ctx, q, runID).Scan(&n); err != nil {
		return 0, fmt.Errorf("repos: count by run: %w", err)
	}
	return n, nil
}

// CountInWindow returns the count of filed issues within the trailing
// `window` (e.g. 24h) within the caller's RLS scope.
func (r *AutoFileCountsRepo) CountInWindow(ctx context.Context, window time.Duration) (int, error) {
	const q = `
		SELECT COUNT(*)
		FROM autofile_counts
		WHERE filed_at >= now() - $1::interval
	`
	var n int
	if err := r.pool.QueryRow(ctx, q, fmt.Sprintf("%d seconds", int(window.Seconds()))).Scan(&n); err != nil {
		return 0, fmt.Errorf("repos: count in window: %w", err)
	}
	return n, nil
}
