package repos

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/revelara-ai/orion/internal/database"
)

// Sentinel errors.
var (
	// ErrNotFound: row not in table for the given (id, RLS scope).
	ErrNotFound = errors.New("repos: not found")
)

// ConnectedRepoRepo persists ConnectedRepo rows. Uses *RLSPool so
// every query is auto-scoped by ctx's WithRLSContext.
type ConnectedRepoRepo struct {
	pool *database.RLSPool
}

// NewConnectedRepoRepo wraps an RLSPool.
func NewConnectedRepoRepo(p *database.RLSPool) *ConnectedRepoRepo {
	return &ConnectedRepoRepo{pool: p}
}

// Create inserts a new row. The org_id is taken from the caller's
// RLS context, NOT from the input — the RLS INSERT policy would
// reject any mismatch anyway. Returns the inserted row with the
// DB-assigned id and timestamps populated.
func (r *ConnectedRepoRepo) Create(ctx context.Context, in ConnectedRepo) (*ConnectedRepo, error) {
	if in.Provider == "" || in.AppInstallID == "" || in.RepoFullName == "" {
		return nil, fmt.Errorf("repos: provider, app_install_id, repo_full_name required")
	}
	if in.DefaultBranch == "" {
		in.DefaultBranch = "main"
	}
	if in.TrustMode == "" {
		in.TrustMode = TrustShadow
	}

	const q = `
		INSERT INTO connected_repo
		    (org_id, provider, app_install_id, repo_full_name, default_branch, service_path, enabled, trust_mode)
		VALUES
		    (current_setting('app.current_organization_id')::uuid, $1, $2, $3, $4, $5, $6, $7)
		RETURNING id, org_id, provider, app_install_id, repo_full_name, default_branch, service_path, enabled, trust_mode, created_at, updated_at
	`
	row := r.pool.QueryRow(ctx, q,
		in.Provider, in.AppInstallID, in.RepoFullName, in.DefaultBranch,
		in.ServicePath, in.Enabled, string(in.TrustMode),
	)
	var out ConnectedRepo
	var trust string
	if err := row.Scan(
		&out.ID, &out.OrgID, &out.Provider, &out.AppInstallID, &out.RepoFullName,
		&out.DefaultBranch, &out.ServicePath, &out.Enabled, &trust,
		&out.CreatedAt, &out.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("repos: create connected_repo: %w", err)
	}
	out.TrustMode = TrustMode(trust)
	return &out, nil
}

// Get returns the row identified by id within the caller's RLS
// scope. Returns ErrNotFound when the row doesn't exist OR exists
// for a different org (RLS hides it).
func (r *ConnectedRepoRepo) Get(ctx context.Context, id uuid.UUID) (*ConnectedRepo, error) {
	const q = `
		SELECT id, org_id, provider, app_install_id, repo_full_name, default_branch, service_path, enabled, trust_mode, created_at, updated_at
		FROM connected_repo
		WHERE id = $1
	`
	row := r.pool.QueryRow(ctx, q, id)
	var out ConnectedRepo
	var trust string
	if err := row.Scan(
		&out.ID, &out.OrgID, &out.Provider, &out.AppInstallID, &out.RepoFullName,
		&out.DefaultBranch, &out.ServicePath, &out.Enabled, &trust,
		&out.CreatedAt, &out.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) || err.Error() == "database: no rows in result" {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("repos: get connected_repo: %w", err)
	}
	out.TrustMode = TrustMode(trust)
	return &out, nil
}

// ListByOrg returns all rows in the caller's RLS scope. The org_id
// is implicit via RLS; the method name reflects intent.
func (r *ConnectedRepoRepo) ListByOrg(ctx context.Context) ([]ConnectedRepo, error) {
	const q = `
		SELECT id, org_id, provider, app_install_id, repo_full_name, default_branch, service_path, enabled, trust_mode, created_at, updated_at
		FROM connected_repo
		ORDER BY created_at
	`
	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("repos: list connected_repo: %w", err)
	}
	defer rows.Close()
	var out []ConnectedRepo
	for rows.Next() {
		var c ConnectedRepo
		var trust string
		if err := rows.Scan(
			&c.ID, &c.OrgID, &c.Provider, &c.AppInstallID, &c.RepoFullName,
			&c.DefaultBranch, &c.ServicePath, &c.Enabled, &trust,
			&c.CreatedAt, &c.UpdatedAt,
		); err != nil {
			return nil, err
		}
		c.TrustMode = TrustMode(trust)
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpdateTrustMode transitions an existing row to a new trust mode.
// SPEC §6.4 trust ladder transitions are validated by the caller
// (Epic 5 owns the state machine); this method just persists.
func (r *ConnectedRepoRepo) UpdateTrustMode(ctx context.Context, id uuid.UUID, mode TrustMode) error {
	const q = `UPDATE connected_repo SET trust_mode = $2, updated_at = now() WHERE id = $1`
	res, err := r.pool.Exec(ctx, q, id, string(mode))
	if err != nil {
		return fmt.Errorf("repos: update trust_mode: %w", err)
	}
	if res.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes a row. Cascade-deletes its TrackerBindings via the FK.
func (r *ConnectedRepoRepo) Delete(ctx context.Context, id uuid.UUID) error {
	const q = `DELETE FROM connected_repo WHERE id = $1`
	res, err := r.pool.Exec(ctx, q, id)
	if err != nil {
		return fmt.Errorf("repos: delete connected_repo: %w", err)
	}
	if res.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
