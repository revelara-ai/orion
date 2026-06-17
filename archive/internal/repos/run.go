package repos

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/revelara-ai/orion/internal/database"
)

// RunState mirrors the runs.status CHECK constraint per SPEC §7.1.
// Stored as text in the DB; the Go type prevents callers from
// constructing an invalid value at the source. The CHECK constraint
// is the second line of defense.
type RunState string

// Run states (SPEC §7.1).
const (
	RunCreated       RunState = "created"
	RunInventorying  RunState = "inventorying"
	RunScanning      RunState = "scanning"
	RunBacklogActive RunState = "backlog_active"
	RunDraining      RunState = "draining"
	RunCompleted     RunState = "completed"
	RunPaused        RunState = "paused"
	RunCancelled     RunState = "cancelled"
	RunFailed        RunState = "failed"
	RunConfigInvalid RunState = "config_invalid"
)

// Run mirrors the runs row.
type Run struct {
	ID          uuid.UUID
	OrgID       uuid.UUID
	RepoID      uuid.UUID
	Status      RunState
	SnapshotRef *string
	StartedAt   time.Time
	FinishedAt  *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// RunRepo persists Run rows. Uses *RLSPool so every query is
// auto-scoped by ctx's WithRLSContext.
type RunRepo struct {
	pool *database.RLSPool
}

// NewRunRepo wraps an RLSPool.
func NewRunRepo(p *database.RLSPool) *RunRepo {
	return &RunRepo{pool: p}
}

// Create inserts a new run. The org_id is taken from the caller's RLS
// context. Returns the inserted row with DB-assigned id and timestamps.
func (r *RunRepo) Create(ctx context.Context, in Run) (*Run, error) {
	if in.RepoID == uuid.Nil {
		return nil, fmt.Errorf("repos: RepoID required")
	}
	if in.Status == "" {
		in.Status = RunCreated
	}

	const q = `
		INSERT INTO runs (org_id, repo_id, status, snapshot_ref)
		VALUES (current_setting('app.current_organization_id')::uuid, $1, $2, $3)
		RETURNING id, org_id, repo_id, status, snapshot_ref, started_at, finished_at, created_at, updated_at
	`
	row := r.pool.QueryRow(ctx, q, in.RepoID, string(in.Status), in.SnapshotRef)
	var out Run
	var status string
	if err := row.Scan(
		&out.ID, &out.OrgID, &out.RepoID, &status, &out.SnapshotRef,
		&out.StartedAt, &out.FinishedAt, &out.CreatedAt, &out.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("repos: create run: %w", err)
	}
	out.Status = RunState(status)
	return &out, nil
}

// Get returns the run identified by id within the caller's RLS scope.
// Returns ErrNotFound when the row is hidden by RLS or absent.
func (r *RunRepo) Get(ctx context.Context, id uuid.UUID) (*Run, error) {
	const q = `
		SELECT id, org_id, repo_id, status, snapshot_ref, started_at, finished_at, created_at, updated_at
		FROM runs
		WHERE id = $1
	`
	row := r.pool.QueryRow(ctx, q, id)
	var out Run
	var status string
	if err := row.Scan(
		&out.ID, &out.OrgID, &out.RepoID, &status, &out.SnapshotRef,
		&out.StartedAt, &out.FinishedAt, &out.CreatedAt, &out.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("repos: get run: %w", err)
	}
	out.Status = RunState(status)
	return &out, nil
}

// UpdateStatus transitions a run's status. The DB's CHECK constraint
// rejects invalid values; this method does not enforce the state-machine
// transition order (the Conductor owns transition semantics in §7.1).
func (r *RunRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status RunState) error {
	const q = `
		UPDATE runs
		SET status = $2,
		    updated_at = now(),
		    finished_at = CASE
		        WHEN $2 IN ('completed', 'cancelled', 'failed', 'config_invalid') THEN now()
		        ELSE finished_at
		    END
		WHERE id = $1
	`
	res, err := r.pool.Exec(ctx, q, id, string(status))
	if err != nil {
		return fmt.Errorf("repos: update run status: %w", err)
	}
	if res.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListInState returns runs whose status is in the provided set, within
// the caller's RLS scope. Used by SPEC §7.4 #5 restart recovery to
// find runs the new leader must reconcile.
func (r *RunRepo) ListInState(ctx context.Context, states []RunState) ([]Run, error) {
	if len(states) == 0 {
		return nil, nil
	}
	strs := make([]string, 0, len(states))
	for _, s := range states {
		strs = append(strs, string(s))
	}
	const q = `
		SELECT id, org_id, repo_id, status, snapshot_ref, started_at, finished_at, created_at, updated_at
		FROM runs
		WHERE status = ANY($1)
		ORDER BY started_at
	`
	rows, err := r.pool.Query(ctx, q, strs)
	if err != nil {
		return nil, fmt.Errorf("repos: list runs in state: %w", err)
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		var run Run
		var status string
		if err := rows.Scan(
			&run.ID, &run.OrgID, &run.RepoID, &status, &run.SnapshotRef,
			&run.StartedAt, &run.FinishedAt, &run.CreatedAt, &run.UpdatedAt,
		); err != nil {
			return nil, err
		}
		run.Status = RunState(status)
		out = append(out, run)
	}
	return out, rows.Err()
}
