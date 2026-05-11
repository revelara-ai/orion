package repos

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/revelara-ai/orion/internal/database"
)

// PreflightDecision is the cached pre-flight outcome from the LLM.
type PreflightDecision string

// Preflight decisions.
const (
	// PreflightInScope: the issue is "in-scope-for-code-change" per
	// SPEC §8.4 rule 8 — Orion may attempt synthesis.
	PreflightInScope PreflightDecision = "in_scope"

	// PreflightOutOfScope: the issue requires human-only judgment
	// (design tradeoff, discussion, ambiguous spec) and should not
	// be claimed.
	PreflightOutOfScope PreflightDecision = "out_of_scope"
)

// PreflightCacheEntry is one cached row.
type PreflightCacheEntry struct {
	OrgID         uuid.UUID
	IssueID       uuid.UUID
	BodySignature string
	Decision      PreflightDecision
	Reason        string
	DecidedAt     time.Time
}

// PreflightCacheRepo persists pre-flight assessments per
// (issue_id, body_signature). RLS-enforced via *database.RLSPool.
type PreflightCacheRepo struct {
	pool *database.RLSPool
}

// NewPreflightCacheRepo wraps an RLSPool.
func NewPreflightCacheRepo(p *database.RLSPool) *PreflightCacheRepo {
	return &PreflightCacheRepo{pool: p}
}

// Get returns the cached decision for (issueID, bodySignature) or
// ErrNotFound when no row exists.
func (r *PreflightCacheRepo) Get(ctx context.Context, issueID uuid.UUID, bodySignature string) (*PreflightCacheEntry, error) {
	const q = `
		SELECT org_id, issue_id, body_signature, decision, reason, decided_at
		FROM preflight_cache
		WHERE issue_id = $1 AND body_signature = $2
	`
	row := r.pool.QueryRow(ctx, q, issueID, bodySignature)
	var e PreflightCacheEntry
	var dec string
	if err := row.Scan(&e.OrgID, &e.IssueID, &e.BodySignature, &dec, &e.Reason, &e.DecidedAt); err != nil {
		if isNoRows(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("repos: get preflight: %w", err)
	}
	e.Decision = PreflightDecision(dec)
	return &e, nil
}

// Set inserts (or updates) the cache row.
func (r *PreflightCacheRepo) Set(ctx context.Context, entry PreflightCacheEntry) error {
	const q = `
		INSERT INTO preflight_cache
		    (org_id, issue_id, body_signature, decision, reason)
		VALUES
		    (current_setting('app.current_organization_id')::uuid,
		     $1, $2, $3, $4)
		ON CONFLICT (issue_id, body_signature) DO UPDATE SET
		    decision = EXCLUDED.decision,
		    reason = EXCLUDED.reason,
		    decided_at = now()
	`
	_, err := r.pool.Exec(ctx, q, entry.IssueID, entry.BodySignature, string(entry.Decision), entry.Reason)
	if err != nil {
		return fmt.Errorf("repos: set preflight: %w", err)
	}
	return nil
}
