package repos

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/revelara-ai/orion/internal/database"
)

// ClaimState mirrors the issue_claims.state CHECK constraint per
// SPEC §7.2. Distinct from the older ClaimStatus enum (which is the
// column type on normalized_issue, a different table).
type ClaimState string

// Issue-claim states (SPEC §7.2).
const (
	ClaimUnclaimedState          ClaimState = "unclaimed"
	ClaimClaimedState            ClaimState = "claimed"
	ClaimDispatchedState         ClaimState = "dispatched"
	ClaimInProgressState         ClaimState = "in_progress"
	ClaimPROpenState             ClaimState = "pr_open"
	ClaimReconcilingState        ClaimState = "reconciling"
	ClaimReleasedState           ClaimState = "released"
	ClaimEscalatedState          ClaimState = "escalated"
	ClaimSupersededState         ClaimState = "superseded"
	ClaimHumanReviewState        ClaimState = "human_review"
	ClaimPostMergeIncidentState  ClaimState = "post_merge_incident"
	ClaimReEvaluationQueuedState ClaimState = "re_evaluation_queued"
	ClaimReDispatchedState       ClaimState = "re_dispatched"
)

// Claim mirrors the issue_claims row.
type Claim struct {
	ID              uuid.UUID
	OrgID           uuid.UUID
	RunID           uuid.UUID
	IssueExternalID string
	State           ClaimState
	ClaimedAt       *time.Time
	FencingToken    *int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// ClaimInput parameterizes Claim. The fencing_token records which
// leader's lease did the claim, so a former leader's straggler writes
// can be detected and rolled back per SPEC §14.2.
type ClaimInput struct {
	RunID           uuid.UUID
	IssueExternalID string
	FencingToken    int64
	InitialState    ClaimState
}

// ErrAlreadyClaimed is returned when an INSERT into issue_claims
// violates the UNIQUE (org_id, issue_external_id) constraint per
// SPEC §7.4 #1 idempotency. Callers MUST treat this as the canonical
// "another replica already claimed this" signal.
var ErrAlreadyClaimed = errors.New("repos: issue is already claimed for this org")

// ClaimRepo persists issue_claims rows. Uses *RLSPool so every query
// is auto-scoped by ctx's WithRLSContext.
type ClaimRepo struct {
	pool *database.RLSPool
}

// NewClaimRepo wraps an RLSPool.
func NewClaimRepo(p *database.RLSPool) *ClaimRepo {
	return &ClaimRepo{pool: p}
}

// Claim attempts to insert a new claim row. On UNIQUE violation
// (someone else already claimed the same external_id under this org)
// returns ErrAlreadyClaimed.
//
// The cap check + spawn intent transaction promised by SPEC §7.4 #1
// is NOT in this method; orion-e43 layers those over the claim by
// wrapping this call inside a wider transaction together with the
// WorkerSession insert. Today this method ships the claim semantics
// only.
func (c *ClaimRepo) Claim(ctx context.Context, in ClaimInput) (*Claim, error) {
	if in.RunID == uuid.Nil {
		return nil, fmt.Errorf("repos: RunID required")
	}
	if in.IssueExternalID == "" {
		return nil, fmt.Errorf("repos: IssueExternalID required")
	}
	if in.InitialState == "" {
		in.InitialState = ClaimClaimedState
	}

	const q = `
		INSERT INTO issue_claims (org_id, run_id, issue_external_id, state, claimed_at, fencing_token)
		VALUES (current_setting('app.current_organization_id')::uuid, $1, $2, $3, now(), $4)
		RETURNING id, org_id, run_id, issue_external_id, state, claimed_at, fencing_token, created_at, updated_at
	`
	row := c.pool.QueryRow(ctx, q, in.RunID, in.IssueExternalID, string(in.InitialState), in.FencingToken)
	var out Claim
	var state string
	if err := row.Scan(
		&out.ID, &out.OrgID, &out.RunID, &out.IssueExternalID, &state,
		&out.ClaimedAt, &out.FencingToken, &out.CreatedAt, &out.UpdatedAt,
	); err != nil {
		if isUniqueViolation(err) {
			return nil, ErrAlreadyClaimed
		}
		return nil, fmt.Errorf("repos: claim issue: %w", err)
	}
	out.State = ClaimState(state)
	return &out, nil
}

// Get returns the claim identified by id within the caller's RLS scope.
func (c *ClaimRepo) Get(ctx context.Context, id uuid.UUID) (*Claim, error) {
	const q = `
		SELECT id, org_id, run_id, issue_external_id, state, claimed_at, fencing_token, created_at, updated_at
		FROM issue_claims
		WHERE id = $1
	`
	row := c.pool.QueryRow(ctx, q, id)
	var out Claim
	var state string
	if err := row.Scan(
		&out.ID, &out.OrgID, &out.RunID, &out.IssueExternalID, &state,
		&out.ClaimedAt, &out.FencingToken, &out.CreatedAt, &out.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("repos: get claim: %w", err)
	}
	out.State = ClaimState(state)
	return &out, nil
}

// UpdateState transitions a claim's state. The DB CHECK constraint
// rejects invalid values; the state-machine transition order is
// enforced by the Conductor (SPEC §7.2), not by this repo.
func (c *ClaimRepo) UpdateState(ctx context.Context, id uuid.UUID, state ClaimState) error {
	const q = `
		UPDATE issue_claims
		SET state = $2, updated_at = now()
		WHERE id = $1
	`
	res, err := c.pool.Exec(ctx, q, id, string(state))
	if err != nil {
		return fmt.Errorf("repos: update claim state: %w", err)
	}
	if res.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// isUniqueViolation reports whether err is a PG unique_violation
// (sqlstate 23505) regardless of how pgx wraps it. We use a string
// match rather than a typed error check because the RLSPool wraps
// errors through tx commits.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "23505") || strings.Contains(s, "duplicate key value violates unique constraint")
}
