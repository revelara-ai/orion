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

// NormalizedIssueRepo persists rows from SPEC §4.1.7. Every write
// is org-scoped via *database.RLSPool; the RLS policies on
// normalized_issue enforce isolation.
type NormalizedIssueRepo struct {
	pool *database.RLSPool
}

// NewNormalizedIssueRepo wraps an RLSPool.
func NewNormalizedIssueRepo(p *database.RLSPool) *NormalizedIssueRepo {
	return &NormalizedIssueRepo{pool: p}
}

// Upsert is the ingestion-driver entry point (E2-6). It does
// INSERT ... ON CONFLICT (external_id) DO UPDATE, returning the
// resulting row's id and timestamps.
//
// Only the fields the ingestion driver knows about are written/
// updated: title, description, priority, state, labels, polaris_risk_id,
// external_url, last_synced_at. eligibility, dedup_signature, and
// claim_status are NOT touched on update so downstream slices'
// computed values don't get clobbered.
func (r *NormalizedIssueRepo) Upsert(ctx context.Context, in NormalizedIssue) (*NormalizedIssue, error) {
	if in.ExternalID == "" || in.RepoID == uuid.Nil || in.TrackerBindingID == uuid.Nil {
		return nil, fmt.Errorf("repos: external_id, repo_id, tracker_binding_id required")
	}
	if in.State == "" {
		in.State = StateOpen
	}
	if in.LastSyncedAt.IsZero() {
		in.LastSyncedAt = time.Now().UTC()
	}
	if in.Labels == nil {
		in.Labels = []string{}
	}
	const q = `
		INSERT INTO normalized_issue
		    (org_id, repo_id, tracker_binding_id, external_id, external_url,
		     title, description, priority, state, labels, polaris_risk_id,
		     orion_filed, last_synced_at)
		VALUES
		    (current_setting('app.current_organization_id')::uuid,
		     $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (external_id) DO UPDATE SET
		    external_url   = EXCLUDED.external_url,
		    title          = EXCLUDED.title,
		    description    = EXCLUDED.description,
		    priority       = EXCLUDED.priority,
		    state          = EXCLUDED.state,
		    labels         = EXCLUDED.labels,
		    polaris_risk_id = EXCLUDED.polaris_risk_id,
		    last_synced_at = EXCLUDED.last_synced_at,
		    updated_at     = now()
		RETURNING id, org_id, repo_id, tracker_binding_id, external_id, external_url,
		    title, description, priority, state, labels, polaris_risk_id, orion_filed,
		    claim_status, eligibility, dedup_signature, last_synced_at, created_at, updated_at
	`
	row := r.pool.QueryRow(ctx, q,
		in.RepoID, in.TrackerBindingID, in.ExternalID, in.ExternalURL,
		in.Title, in.Description, in.Priority, string(in.State), in.Labels, in.PolarisRiskID,
		in.OrionFiled, in.LastSyncedAt,
	)
	return scanNormalizedIssue(row)
}

// Get returns the row by id within the caller's RLS scope.
func (r *NormalizedIssueRepo) Get(ctx context.Context, id uuid.UUID) (*NormalizedIssue, error) {
	const q = `
		SELECT id, org_id, repo_id, tracker_binding_id, external_id, external_url,
		       title, description, priority, state, labels, polaris_risk_id, orion_filed,
		       claim_status, eligibility, dedup_signature, last_synced_at, created_at, updated_at
		FROM normalized_issue
		WHERE id = $1
	`
	out, err := scanNormalizedIssue(r.pool.QueryRow(ctx, q, id))
	if err != nil {
		if isNoRows(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("repos: get normalized_issue: %w", err)
	}
	return out, nil
}

// GetByExternalID looks up by SPEC §4.2 external_id format.
func (r *NormalizedIssueRepo) GetByExternalID(ctx context.Context, externalID string) (*NormalizedIssue, error) {
	const q = `
		SELECT id, org_id, repo_id, tracker_binding_id, external_id, external_url,
		       title, description, priority, state, labels, polaris_risk_id, orion_filed,
		       claim_status, eligibility, dedup_signature, last_synced_at, created_at, updated_at
		FROM normalized_issue
		WHERE external_id = $1
	`
	out, err := scanNormalizedIssue(r.pool.QueryRow(ctx, q, externalID))
	if err != nil {
		if isNoRows(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("repos: get by external_id: %w", err)
	}
	return out, nil
}

// ListByRepoOptions filters ListByRepo.
type ListByRepoOptions struct {
	// Eligibility, if non-nil, restricts to rows with that eligibility.
	Eligibility *Eligibility

	// ClaimStatus, if non-nil, restricts to rows with that claim status.
	ClaimStatus *ClaimStatus

	// Limit caps result size. 0 = unlimited.
	Limit int
}

// ListByRepo returns all normalized_issue rows for repoID within the
// caller's RLS scope, optionally filtered.
func (r *NormalizedIssueRepo) ListByRepo(ctx context.Context, repoID uuid.UUID, opts ListByRepoOptions) ([]NormalizedIssue, error) {
	q := `
		SELECT id, org_id, repo_id, tracker_binding_id, external_id, external_url,
		       title, description, priority, state, labels, polaris_risk_id, orion_filed,
		       claim_status, eligibility, dedup_signature, last_synced_at, created_at, updated_at
		FROM normalized_issue
		WHERE repo_id = $1
	`
	args := []any{repoID}
	if opts.Eligibility != nil {
		q += " AND eligibility = $2"
		args = append(args, string(*opts.Eligibility))
	}
	if opts.ClaimStatus != nil {
		// Use $3 if Eligibility was set, else $2.
		idx := len(args) + 1
		q += fmt.Sprintf(" AND claim_status = $%d", idx)
		args = append(args, string(*opts.ClaimStatus))
	}
	q += " ORDER BY created_at"
	if opts.Limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", opts.Limit)
	}
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("repos: list normalized_issue: %w", err)
	}
	defer rows.Close()
	var out []NormalizedIssue
	for rows.Next() {
		ni, err := scanNormalizedIssueFromRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *ni)
	}
	return out, rows.Err()
}

// UpdateEligibility persists the eligibility evaluator's verdict (E2-8).
func (r *NormalizedIssueRepo) UpdateEligibility(ctx context.Context, id uuid.UUID, e Eligibility) error {
	const q = `UPDATE normalized_issue SET eligibility = $2, updated_at = now() WHERE id = $1`
	res, err := r.pool.Exec(ctx, q, id, string(e))
	if err != nil {
		return fmt.Errorf("repos: update eligibility: %w", err)
	}
	if res.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateClaimStatus persists a claim-state transition (E4 owns the
// state machine).
func (r *NormalizedIssueRepo) UpdateClaimStatus(ctx context.Context, id uuid.UUID, s ClaimStatus) error {
	const q = `UPDATE normalized_issue SET claim_status = $2, updated_at = now() WHERE id = $1`
	res, err := r.pool.Exec(ctx, q, id, string(s))
	if err != nil {
		return fmt.Errorf("repos: update claim_status: %w", err)
	}
	if res.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateDedupSignature persists E2-7's computed signature.
func (r *NormalizedIssueRepo) UpdateDedupSignature(ctx context.Context, id uuid.UUID, signature string) error {
	const q = `UPDATE normalized_issue SET dedup_signature = $2, updated_at = now() WHERE id = $1`
	res, err := r.pool.Exec(ctx, q, id, signature)
	if err != nil {
		return fmt.Errorf("repos: update dedup_signature: %w", err)
	}
	if res.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// NextEligible returns the top eligible issue for the given repo
// per SPEC §8.6 priority order. Returns ErrNotFound when no
// eligible issue exists. The ordering matches
// internal/backlog.Compare so the in-memory sort and the SQL
// top-1 stay consistent.
func (r *NormalizedIssueRepo) NextEligible(ctx context.Context, repoID uuid.UUID) (*NormalizedIssue, error) {
	const q = `
		SELECT id, org_id, repo_id, tracker_binding_id, external_id, external_url,
		       title, description, priority, state, labels, polaris_risk_id, orion_filed,
		       claim_status, eligibility, dedup_signature, last_synced_at, created_at, updated_at
		FROM normalized_issue
		WHERE repo_id = $1
		  AND eligibility = 'eligible'
		  AND claim_status = 'unclaimed'
		  AND state = 'open'
		ORDER BY
		    COALESCE(priority, 32767),
		    created_at ASC,
		    external_id ASC
		LIMIT 1
	`
	row := r.pool.QueryRow(ctx, q, repoID)
	got, err := scanNormalizedIssue(row)
	if err != nil {
		if isNoRows(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("repos: next eligible: %w", err)
	}
	return got, nil
}

// MaxLastSyncedAt returns the most recent last_synced_at for issues
// from the given binding within the caller's RLS scope. Returns the
// zero time (and nil error) when no rows exist — the backlog
// ingestion driver (E2-6) treats that as "no prior syncs" and asks
// the adapter to FetchCandidates without a since cursor.
func (r *NormalizedIssueRepo) MaxLastSyncedAt(ctx context.Context, bindingID uuid.UUID) (time.Time, error) {
	const q = `
		SELECT COALESCE(MAX(last_synced_at), 'epoch'::timestamptz)
		FROM normalized_issue
		WHERE tracker_binding_id = $1
	`
	var t time.Time
	if err := r.pool.QueryRow(ctx, q, bindingID).Scan(&t); err != nil {
		return time.Time{}, fmt.Errorf("repos: max last_synced_at: %w", err)
	}
	// COALESCE-to-epoch yields a sentinel; convert it back to zero
	// so callers can compare with t.IsZero().
	epoch := time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)
	if t.Equal(epoch) {
		return time.Time{}, nil
	}
	return t, nil
}

// ExistsOrionFiledByDedup is the autofile gate (E2-10): true if an
// orion-filed issue with this dedup_signature already exists for
// the caller's org.
func (r *NormalizedIssueRepo) ExistsOrionFiledByDedup(ctx context.Context, signature string) (bool, error) {
	const q = `
		SELECT EXISTS (
			SELECT 1 FROM normalized_issue
			WHERE dedup_signature = $1 AND orion_filed = true
		)
	`
	var exists bool
	if err := r.pool.QueryRow(ctx, q, signature).Scan(&exists); err != nil {
		return false, fmt.Errorf("repos: exists orion-filed by dedup: %w", err)
	}
	return exists, nil
}

// scanNormalizedIssue scans a single pgx.Row.
func scanNormalizedIssue(row pgx.Row) (*NormalizedIssue, error) {
	var n NormalizedIssue
	var state string
	var claim string
	var elig *string
	if err := row.Scan(
		&n.ID, &n.OrgID, &n.RepoID, &n.TrackerBindingID, &n.ExternalID, &n.ExternalURL,
		&n.Title, &n.Description, &n.Priority, &state, &n.Labels, &n.PolarisRiskID, &n.OrionFiled,
		&claim, &elig, &n.DedupSignature, &n.LastSyncedAt, &n.CreatedAt, &n.UpdatedAt,
	); err != nil {
		return nil, err
	}
	n.State = NormalizedState(state)
	n.ClaimStatus = ClaimStatus(claim)
	if elig != nil {
		e := Eligibility(*elig)
		n.Eligibility = &e
	}
	return &n, nil
}

// scanNormalizedIssueFromRows scans the current row of an iterator.
func scanNormalizedIssueFromRows(rows pgx.Rows) (*NormalizedIssue, error) {
	var n NormalizedIssue
	var state string
	var claim string
	var elig *string
	if err := rows.Scan(
		&n.ID, &n.OrgID, &n.RepoID, &n.TrackerBindingID, &n.ExternalID, &n.ExternalURL,
		&n.Title, &n.Description, &n.Priority, &state, &n.Labels, &n.PolarisRiskID, &n.OrionFiled,
		&claim, &elig, &n.DedupSignature, &n.LastSyncedAt, &n.CreatedAt, &n.UpdatedAt,
	); err != nil {
		return nil, err
	}
	n.State = NormalizedState(state)
	n.ClaimStatus = ClaimStatus(claim)
	if elig != nil {
		e := Eligibility(*elig)
		n.Eligibility = &e
	}
	return &n, nil
}

// isNoRows is true for pgx's no-rows error OR our wrapper's
// "no rows in result" message (from database.singleRow.Scan).
func isNoRows(err error) bool {
	if errors.Is(err, pgx.ErrNoRows) {
		return true
	}
	return err != nil && err.Error() == "database: no rows in result"
}
