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

// SpawnIntent mirrors the worker_spawn_intents row. Per SPEC §7.4 #6
// the intent is recorded in the SAME transaction as the ClaimRepo.Claim
// (orion-e48 wraps both); this slice provides the persistence layer.
type SpawnIntent struct {
	ID             uuid.UUID
	OrgID          uuid.UUID
	ClaimID        uuid.UUID
	WorkspaceKey   string
	PodNamespace   string
	PodNamePlanned string
	CreatedAt      time.Time
}

// SpawnIntentInput parameterizes Record.
type SpawnIntentInput struct {
	ClaimID        uuid.UUID
	WorkspaceKey   string
	PodNamespace   string
	PodNamePlanned string
}

// SpawnIntentRepo persists worker_spawn_intents rows on the RLSPool.
type SpawnIntentRepo struct {
	pool *database.RLSPool
}

// NewSpawnIntentRepo wraps an RLSPool.
func NewSpawnIntentRepo(p *database.RLSPool) *SpawnIntentRepo {
	return &SpawnIntentRepo{pool: p}
}

// Record inserts a new spawn intent. UNIQUE workspace_key violation
// returns ErrWorkspaceKeyTaken (shared with WorkerSessionRepo) so the
// caller can treat retry-after-crash as a no-op per §7.4 #6.
func (r *SpawnIntentRepo) Record(ctx context.Context, in SpawnIntentInput) (*SpawnIntent, error) {
	if in.ClaimID == uuid.Nil {
		return nil, fmt.Errorf("repos: ClaimID required")
	}
	if in.WorkspaceKey == "" {
		return nil, fmt.Errorf("repos: WorkspaceKey required")
	}
	if in.PodNamespace == "" || in.PodNamePlanned == "" {
		return nil, fmt.Errorf("repos: PodNamespace and PodNamePlanned required")
	}
	const q = `
		INSERT INTO worker_spawn_intents (org_id, claim_id, workspace_key, pod_namespace, pod_name_planned)
		VALUES (current_setting('app.current_organization_id')::uuid, $1, $2, $3, $4)
		RETURNING id, org_id, claim_id, workspace_key, pod_namespace, pod_name_planned, created_at
	`
	row := r.pool.QueryRow(ctx, q, in.ClaimID, in.WorkspaceKey, in.PodNamespace, in.PodNamePlanned)
	var out SpawnIntent
	if err := row.Scan(
		&out.ID, &out.OrgID, &out.ClaimID, &out.WorkspaceKey,
		&out.PodNamespace, &out.PodNamePlanned, &out.CreatedAt,
	); err != nil {
		if isUniqueViolation(err) {
			return nil, ErrWorkspaceKeyTaken
		}
		return nil, fmt.Errorf("repos: record spawn intent: %w", err)
	}
	return &out, nil
}

// Get returns the spawn intent identified by id within the caller's
// RLS scope.
func (r *SpawnIntentRepo) Get(ctx context.Context, id uuid.UUID) (*SpawnIntent, error) {
	const q = `
		SELECT id, org_id, claim_id, workspace_key, pod_namespace, pod_name_planned, created_at
		FROM worker_spawn_intents
		WHERE id = $1
	`
	row := r.pool.QueryRow(ctx, q, id)
	var out SpawnIntent
	if err := row.Scan(
		&out.ID, &out.OrgID, &out.ClaimID, &out.WorkspaceKey,
		&out.PodNamespace, &out.PodNamePlanned, &out.CreatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("repos: get spawn intent: %w", err)
	}
	return &out, nil
}

// GetByWorkspaceKey looks up a spawn intent by its UNIQUE workspace_key.
// Used by recovery paths that have the workspace_key but not the row id.
func (r *SpawnIntentRepo) GetByWorkspaceKey(ctx context.Context, key string) (*SpawnIntent, error) {
	const q = `
		SELECT id, org_id, claim_id, workspace_key, pod_namespace, pod_name_planned, created_at
		FROM worker_spawn_intents
		WHERE workspace_key = $1
	`
	row := r.pool.QueryRow(ctx, q, key)
	var out SpawnIntent
	if err := row.Scan(
		&out.ID, &out.OrgID, &out.ClaimID, &out.WorkspaceKey,
		&out.PodNamespace, &out.PodNamePlanned, &out.CreatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("repos: get spawn intent by workspace_key: %w", err)
	}
	return &out, nil
}
