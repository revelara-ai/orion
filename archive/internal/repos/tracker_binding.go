package repos

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/revelara-ai/orion/internal/database"
)

// TrackerBindingRepo persists TrackerBinding rows. Uses *RLSPool;
// every query is org-scoped via ctx.
type TrackerBindingRepo struct {
	pool *database.RLSPool
}

// NewTrackerBindingRepo wraps an RLSPool.
func NewTrackerBindingRepo(p *database.RLSPool) *TrackerBindingRepo {
	return &TrackerBindingRepo{pool: p}
}

// Create inserts a new TrackerBinding row. RepoID must reference an
// existing connected_repo in the caller's org (the FK + RLS combine
// to enforce this).
func (r *TrackerBindingRepo) Create(ctx context.Context, in TrackerBinding) (*TrackerBinding, error) {
	if in.Kind == "" {
		return nil, fmt.Errorf("repos: kind required")
	}
	if in.CredentialsRef == "" {
		return nil, fmt.Errorf("repos: credentials_ref required")
	}
	if in.Config == nil {
		in.Config = map[string]any{}
	}
	configJSON, err := json.Marshal(in.Config)
	if err != nil {
		return nil, fmt.Errorf("repos: marshal config: %w", err)
	}
	const q = `
		INSERT INTO tracker_binding
		    (org_id, repo_id, kind, config, credentials_ref, enabled, auto_file)
		VALUES
		    (current_setting('app.current_organization_id')::uuid, $1, $2, $3, $4, $5, $6)
		RETURNING id, org_id, repo_id, kind, config, credentials_ref, enabled, auto_file, created_at, updated_at
	`
	row := r.pool.QueryRow(ctx, q, in.RepoID, string(in.Kind), configJSON, in.CredentialsRef, in.Enabled, in.AutoFile)
	out, err := scanBinding(row)
	if err != nil {
		return nil, fmt.Errorf("repos: create tracker_binding: %w", err)
	}
	return out, nil
}

// Get returns the row by id within the caller's RLS scope.
func (r *TrackerBindingRepo) Get(ctx context.Context, id uuid.UUID) (*TrackerBinding, error) {
	const q = `
		SELECT id, org_id, repo_id, kind, config, credentials_ref, enabled, auto_file, created_at, updated_at
		FROM tracker_binding
		WHERE id = $1
	`
	out, err := scanBinding(r.pool.QueryRow(ctx, q, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || err.Error() == "database: no rows in result" {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("repos: get tracker_binding: %w", err)
	}
	return out, nil
}

// ListByRepo returns all bindings for one connected repo.
func (r *TrackerBindingRepo) ListByRepo(ctx context.Context, repoID uuid.UUID) ([]TrackerBinding, error) {
	const q = `
		SELECT id, org_id, repo_id, kind, config, credentials_ref, enabled, auto_file, created_at, updated_at
		FROM tracker_binding
		WHERE repo_id = $1
		ORDER BY created_at
	`
	rows, err := r.pool.Query(ctx, q, repoID)
	if err != nil {
		return nil, fmt.Errorf("repos: list tracker_binding: %w", err)
	}
	defer rows.Close()
	var out []TrackerBinding
	for rows.Next() {
		b, err := scanBindingFromRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

// Delete removes a binding by id.
func (r *TrackerBindingRepo) Delete(ctx context.Context, id uuid.UUID) error {
	const q = `DELETE FROM tracker_binding WHERE id = $1`
	res, err := r.pool.Exec(ctx, q, id)
	if err != nil {
		return fmt.Errorf("repos: delete tracker_binding: %w", err)
	}
	if res.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// scanBinding scans a single pgx.Row into a TrackerBinding.
func scanBinding(row pgx.Row) (*TrackerBinding, error) {
	var b TrackerBinding
	var kind string
	var configJSON []byte
	if err := row.Scan(
		&b.ID, &b.OrgID, &b.RepoID, &kind, &configJSON,
		&b.CredentialsRef, &b.Enabled, &b.AutoFile, &b.CreatedAt, &b.UpdatedAt,
	); err != nil {
		return nil, err
	}
	b.Kind = TrackerKind(kind)
	if len(configJSON) > 0 {
		if err := json.Unmarshal(configJSON, &b.Config); err != nil {
			return nil, fmt.Errorf("unmarshal config: %w", err)
		}
	} else {
		b.Config = map[string]any{}
	}
	return &b, nil
}

// scanBindingFromRows scans the current row of a pgx.Rows iterator.
func scanBindingFromRows(rows pgx.Rows) (*TrackerBinding, error) {
	var b TrackerBinding
	var kind string
	var configJSON []byte
	if err := rows.Scan(
		&b.ID, &b.OrgID, &b.RepoID, &kind, &configJSON,
		&b.CredentialsRef, &b.Enabled, &b.AutoFile, &b.CreatedAt, &b.UpdatedAt,
	); err != nil {
		return nil, err
	}
	b.Kind = TrackerKind(kind)
	if len(configJSON) > 0 {
		if err := json.Unmarshal(configJSON, &b.Config); err != nil {
			return nil, fmt.Errorf("unmarshal config: %w", err)
		}
	} else {
		b.Config = map[string]any{}
	}
	return &b, nil
}
