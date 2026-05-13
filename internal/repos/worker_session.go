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

// WorkerSessionPhase mirrors the worker_sessions.phase CHECK constraint
// per SPEC §7.3.
type WorkerSessionPhase string

// Worker-session phases (SPEC §7.3).
const (
	WorkerPhasePreparingSandbox    WorkerSessionPhase = "preparing_sandbox"
	WorkerPhaseLoadingRunSnapshot  WorkerSessionPhase = "loading_run_snapshot"
	WorkerPhaseSynthesizingPatches WorkerSessionPhase = "synthesizing_patches"
	WorkerPhaseVerifyingPatches    WorkerSessionPhase = "verifying_patches"
	WorkerPhaseComposingPatches    WorkerSessionPhase = "composing_patches"
	WorkerPhaseOpeningPROrDraft    WorkerSessionPhase = "opening_pr_or_draft"
	WorkerPhaseSucceeded           WorkerSessionPhase = "succeeded"
	WorkerPhaseFailed              WorkerSessionPhase = "failed"
	WorkerPhaseTimedOut            WorkerSessionPhase = "timed_out"
	WorkerPhaseStalled             WorkerSessionPhase = "stalled"
	WorkerPhaseCancelled           WorkerSessionPhase = "cancelled"
)

// WorkerSession mirrors the worker_sessions row.
type WorkerSession struct {
	ID           uuid.UUID
	OrgID        uuid.UUID
	RunID        uuid.UUID
	ClaimID      uuid.UUID
	WorkspaceKey string
	Phase        WorkerSessionPhase
	LastEventAt  time.Time
	StartedAt    time.Time
	FinishedAt   *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// WorkerSessionInput parameterizes Create.
type WorkerSessionInput struct {
	RunID        uuid.UUID
	ClaimID      uuid.UUID
	WorkspaceKey string
	InitialPhase WorkerSessionPhase
}

// ErrWorkspaceKeyTaken is returned when a worker_sessions or
// worker_spawn_intents INSERT violates the UNIQUE workspace_key
// constraint. Per SPEC §11.1 the Conductor treats this as success
// (the prior leader already recorded the intent).
var ErrWorkspaceKeyTaken = errors.New("repos: workspace_key already exists")

// WorkerSessionRepo persists worker_sessions rows on the RLSPool.
type WorkerSessionRepo struct {
	pool *database.RLSPool
}

// NewWorkerSessionRepo wraps an RLSPool.
func NewWorkerSessionRepo(p *database.RLSPool) *WorkerSessionRepo {
	return &WorkerSessionRepo{pool: p}
}

// Create inserts a new worker session. The workspace_key UNIQUE
// constraint is the load-bearing dedup; on violation returns
// ErrWorkspaceKeyTaken so callers can treat retries as idempotent.
func (r *WorkerSessionRepo) Create(ctx context.Context, in WorkerSessionInput) (*WorkerSession, error) {
	if in.RunID == uuid.Nil {
		return nil, fmt.Errorf("repos: RunID required")
	}
	if in.ClaimID == uuid.Nil {
		return nil, fmt.Errorf("repos: ClaimID required")
	}
	if in.WorkspaceKey == "" {
		return nil, fmt.Errorf("repos: WorkspaceKey required")
	}
	if in.InitialPhase == "" {
		in.InitialPhase = WorkerPhasePreparingSandbox
	}
	const q = `
		INSERT INTO worker_sessions (org_id, run_id, claim_id, workspace_key, phase)
		VALUES (current_setting('app.current_organization_id')::uuid, $1, $2, $3, $4)
		RETURNING id, org_id, run_id, claim_id, workspace_key, phase, last_event_at, started_at, finished_at, created_at, updated_at
	`
	row := r.pool.QueryRow(ctx, q, in.RunID, in.ClaimID, in.WorkspaceKey, string(in.InitialPhase))
	var out WorkerSession
	var phase string
	if err := row.Scan(
		&out.ID, &out.OrgID, &out.RunID, &out.ClaimID, &out.WorkspaceKey, &phase,
		&out.LastEventAt, &out.StartedAt, &out.FinishedAt, &out.CreatedAt, &out.UpdatedAt,
	); err != nil {
		if isUniqueViolation(err) {
			return nil, ErrWorkspaceKeyTaken
		}
		return nil, fmt.Errorf("repos: create worker session: %w", err)
	}
	out.Phase = WorkerSessionPhase(phase)
	return &out, nil
}

// Get returns the worker session identified by id within the caller's
// RLS scope.
func (r *WorkerSessionRepo) Get(ctx context.Context, id uuid.UUID) (*WorkerSession, error) {
	const q = `
		SELECT id, org_id, run_id, claim_id, workspace_key, phase,
		       last_event_at, started_at, finished_at, created_at, updated_at
		FROM worker_sessions
		WHERE id = $1
	`
	row := r.pool.QueryRow(ctx, q, id)
	var out WorkerSession
	var phase string
	if err := row.Scan(
		&out.ID, &out.OrgID, &out.RunID, &out.ClaimID, &out.WorkspaceKey, &phase,
		&out.LastEventAt, &out.StartedAt, &out.FinishedAt, &out.CreatedAt, &out.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("repos: get worker session: %w", err)
	}
	out.Phase = WorkerSessionPhase(phase)
	return &out, nil
}

// UpdatePhase transitions a worker session's phase. Terminal phases
// (succeeded, failed, timed_out, stalled, cancelled) auto-populate
// finished_at. The DB CHECK constraint rejects invalid values.
func (r *WorkerSessionRepo) UpdatePhase(ctx context.Context, id uuid.UUID, phase WorkerSessionPhase) error {
	const q = `
		UPDATE worker_sessions
		SET phase = $2,
		    last_event_at = now(),
		    updated_at = now(),
		    finished_at = CASE
		        WHEN $2 IN ('succeeded', 'failed', 'timed_out', 'stalled', 'cancelled') THEN now()
		        ELSE finished_at
		    END
		WHERE id = $1
	`
	res, err := r.pool.Exec(ctx, q, id, string(phase))
	if err != nil {
		return fmt.Errorf("repos: update worker session phase: %w", err)
	}
	if res.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
