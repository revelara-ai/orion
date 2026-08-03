package contextstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// FogNote is one parked known-unknown (or-kjcm.5): an area where a decision is
// coming but can't be phrased as a sharp question yet. Deliberately coarser
// than an open question — no key, no severity, no mode — and advisory by
// construction: fog never gates ratification. It leaves the ledger only by
// GRADUATING into one or more open questions (once an answer sharpens it) or
// by being dismissed.
type FogNote struct {
	ID         string
	ProjectID  string
	Note       string
	Status     string // open | graduated | dismissed
	Resolution string // graduation pointer or dismissal reason
	CreatedAt  string
	UpdatedAt  string
}

// FogRepo persists the parked areas.
type FogRepo struct{ tx *sql.Tx }

// Fog returns the fog repo bound to this transaction.
func (t *Tx) Fog() *FogRepo { return &FogRepo{tx: t.tx} }

// Create parks a note (status open).
func (r *FogRepo) Create(ctx context.Context, projectID, note string) (string, error) {
	id, now := newID(), nowRFC3339()
	_, err := r.tx.ExecContext(ctx,
		`INSERT INTO fog (id, project_id, note, status, resolution, created_at, updated_at)
		 VALUES (?,?,?,'open','',?,?)`, id, projectID, note, now, now)
	if err != nil {
		return "", fmt.Errorf("park fog: %w", err)
	}
	return id, nil
}

// ListOpen returns the project's parked notes, oldest first.
func (r *FogRepo) ListOpen(ctx context.Context, projectID string) ([]FogNote, error) {
	rows, err := r.tx.QueryContext(ctx,
		`SELECT id, project_id, note, status, resolution, created_at, updated_at
		 FROM fog WHERE project_id=? AND status='open' ORDER BY created_at ASC, id ASC`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []FogNote
	for rows.Next() {
		var f FogNote
		if err := rows.Scan(&f.ID, &f.ProjectID, &f.Note, &f.Status, &f.Resolution, &f.CreatedAt, &f.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// Get returns one note by id.
func (r *FogRepo) Get(ctx context.Context, id string) (FogNote, error) {
	var f FogNote
	err := r.tx.QueryRowContext(ctx,
		`SELECT id, project_id, note, status, resolution, created_at, updated_at FROM fog WHERE id=?`, id).
		Scan(&f.ID, &f.ProjectID, &f.Note, &f.Status, &f.Resolution, &f.CreatedAt, &f.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return FogNote{}, ErrNotFound
	}
	return f, err
}

// Resolve marks an OPEN note graduated|dismissed with its resolution text
// (the graduated question ids, or the dismissal reason). Resolving twice is
// an error — the same idempotence guard the question ledger has.
func (r *FogRepo) Resolve(ctx context.Context, id, status, resolution string) error {
	if status != "graduated" && status != "dismissed" {
		return fmt.Errorf("fog: status %q is not a resolution (graduated|dismissed)", status)
	}
	res, err := r.tx.ExecContext(ctx,
		`UPDATE fog SET status=?, resolution=?, updated_at=? WHERE id=? AND status='open'`,
		status, resolution, nowRFC3339(), id)
	if err != nil {
		return fmt.Errorf("resolve fog: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
