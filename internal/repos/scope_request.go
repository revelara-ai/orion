package repos

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/revelara-ai/orion/internal/database"
)

// ScopeRequest mirrors the scope_requests row. One row is recorded
// per agent tool dispatch (both allowed and rejected) per SPEC §11.3.
type ScopeRequest struct {
	ID              uuid.UUID
	OrgID           uuid.UUID
	RunID           uuid.UUID
	ClaimID         *uuid.UUID
	WorkerSessionID *uuid.UUID
	ToolName        string
	RequestedScope  map[string]any
	GrantedScope    map[string]any
	RejectionReason *string
	DecidedAt       time.Time
	CreatedAt       time.Time
}

// ScopeRequestInput parameterizes Record.
type ScopeRequestInput struct {
	RunID           uuid.UUID
	ClaimID         *uuid.UUID
	WorkerSessionID *uuid.UUID
	ToolName        string
	RequestedScope  map[string]any
	GrantedScope    map[string]any
	RejectionReason *string
}

// ScopeRequestRepo persists scope_requests rows on the RLSPool.
type ScopeRequestRepo struct {
	pool *database.RLSPool
}

// NewScopeRequestRepo wraps an RLSPool.
func NewScopeRequestRepo(p *database.RLSPool) *ScopeRequestRepo {
	return &ScopeRequestRepo{pool: p}
}

// Record inserts a new scope-request audit row. Every agent tool
// dispatch MUST call this (both allowed and rejected) so the operator
// review surface has a complete ledger.
func (r *ScopeRequestRepo) Record(ctx context.Context, in ScopeRequestInput) (*ScopeRequest, error) {
	if in.RunID == uuid.Nil {
		return nil, fmt.Errorf("repos: RunID required")
	}
	if in.ToolName == "" {
		return nil, fmt.Errorf("repos: ToolName required")
	}
	if in.RequestedScope == nil {
		in.RequestedScope = map[string]any{}
	}
	reqJSON, err := json.Marshal(in.RequestedScope)
	if err != nil {
		return nil, fmt.Errorf("repos: marshal requested_scope: %w", err)
	}
	var grantedJSON []byte
	if in.GrantedScope != nil {
		b, err := json.Marshal(in.GrantedScope)
		if err != nil {
			return nil, fmt.Errorf("repos: marshal granted_scope: %w", err)
		}
		grantedJSON = b
	}
	const q = `
		INSERT INTO scope_requests
		    (org_id, run_id, claim_id, worker_session_id, tool_name, requested_scope, granted_scope, rejection_reason)
		VALUES
		    (current_setting('app.current_organization_id')::uuid, $1, $2, $3, $4, $5, $6, $7)
		RETURNING id, org_id, run_id, claim_id, worker_session_id, tool_name, requested_scope, granted_scope, rejection_reason, decided_at, created_at
	`
	row := r.pool.QueryRow(ctx, q,
		in.RunID, in.ClaimID, in.WorkerSessionID, in.ToolName,
		reqJSON, grantedJSON, in.RejectionReason,
	)
	var out ScopeRequest
	var reqBuf, grantedBuf []byte
	if err := row.Scan(
		&out.ID, &out.OrgID, &out.RunID, &out.ClaimID, &out.WorkerSessionID,
		&out.ToolName, &reqBuf, &grantedBuf, &out.RejectionReason,
		&out.DecidedAt, &out.CreatedAt,
	); err != nil {
		return nil, fmt.Errorf("repos: record scope_request: %w", err)
	}
	if err := json.Unmarshal(reqBuf, &out.RequestedScope); err != nil {
		return nil, fmt.Errorf("repos: unmarshal requested_scope: %w", err)
	}
	if grantedBuf != nil {
		if err := json.Unmarshal(grantedBuf, &out.GrantedScope); err != nil {
			return nil, fmt.Errorf("repos: unmarshal granted_scope: %w", err)
		}
	}
	return &out, nil
}

// Get returns the scope_request identified by id within the caller's
// RLS scope.
func (r *ScopeRequestRepo) Get(ctx context.Context, id uuid.UUID) (*ScopeRequest, error) {
	const q = `
		SELECT id, org_id, run_id, claim_id, worker_session_id,
		       tool_name, requested_scope, granted_scope, rejection_reason,
		       decided_at, created_at
		FROM scope_requests
		WHERE id = $1
	`
	row := r.pool.QueryRow(ctx, q, id)
	var out ScopeRequest
	var reqBuf, grantedBuf []byte
	if err := row.Scan(
		&out.ID, &out.OrgID, &out.RunID, &out.ClaimID, &out.WorkerSessionID,
		&out.ToolName, &reqBuf, &grantedBuf, &out.RejectionReason,
		&out.DecidedAt, &out.CreatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("repos: get scope_request: %w", err)
	}
	_ = json.Unmarshal(reqBuf, &out.RequestedScope)
	if grantedBuf != nil {
		_ = json.Unmarshal(grantedBuf, &out.GrantedScope)
	}
	return &out, nil
}

// ListByWorkerSession returns all scope_requests for a worker session,
// chronological order. The Lookout uses this to surface scope creep
// in real time; the escalation review reads it after termination.
func (r *ScopeRequestRepo) ListByWorkerSession(ctx context.Context, wsID uuid.UUID) ([]ScopeRequest, error) {
	const q = `
		SELECT id, org_id, run_id, claim_id, worker_session_id,
		       tool_name, requested_scope, granted_scope, rejection_reason,
		       decided_at, created_at
		FROM scope_requests
		WHERE worker_session_id = $1
		ORDER BY decided_at
	`
	rows, err := r.pool.Query(ctx, q, wsID)
	if err != nil {
		return nil, fmt.Errorf("repos: list scope_requests: %w", err)
	}
	defer rows.Close()
	var out []ScopeRequest
	for rows.Next() {
		var sr ScopeRequest
		var reqBuf, grantedBuf []byte
		if err := rows.Scan(
			&sr.ID, &sr.OrgID, &sr.RunID, &sr.ClaimID, &sr.WorkerSessionID,
			&sr.ToolName, &reqBuf, &grantedBuf, &sr.RejectionReason,
			&sr.DecidedAt, &sr.CreatedAt,
		); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(reqBuf, &sr.RequestedScope)
		if grantedBuf != nil {
			_ = json.Unmarshal(grantedBuf, &sr.GrantedScope)
		}
		out = append(out, sr)
	}
	return out, rows.Err()
}
