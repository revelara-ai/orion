package contextstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Goals is the goal-altitude artifact of a project (or-045a.2): the ratified
// goals/non-goals/success-criteria document, content-hashed like a ratified
// spec. Content is canonical JSON owned by the orchestrator.
type Goals struct {
	ProjectID string
	Content   string
	Hash      string
	Status    string // drafting | ratified
	CreatedAt string
	UpdatedAt string
}

// GoalsRepo persists one goals document per project.
type GoalsRepo struct{ tx *sql.Tx }

// Upsert stores/replaces the project's goals PROPOSAL (status drafting; any
// prior ratification hash is cleared — a re-proposal re-opens ratification).
func (r *GoalsRepo) Upsert(ctx context.Context, projectID, content string) error {
	now := nowRFC3339()
	_, err := r.tx.ExecContext(ctx,
		`INSERT INTO goals (project_id, content, hash, status, created_at, updated_at) VALUES (?,?,'','drafting',?,?)
		 ON CONFLICT(project_id) DO UPDATE SET content=excluded.content, hash='', status='drafting', updated_at=excluded.updated_at`,
		projectID, content, now, now)
	if err != nil {
		return fmt.Errorf("upsert goals: %w", err)
	}
	return nil
}

// Ratify marks the project's proposed goals as ratified with their content hash.
func (r *GoalsRepo) Ratify(ctx context.Context, projectID, hash string) error {
	res, err := r.tx.ExecContext(ctx,
		`UPDATE goals SET status='ratified', hash=?, updated_at=? WHERE project_id=?`,
		hash, nowRFC3339(), projectID)
	if err != nil {
		return fmt.Errorf("ratify goals: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Get returns the project's goals document (ErrNotFound when none proposed).
func (r *GoalsRepo) Get(ctx context.Context, projectID string) (Goals, error) {
	var g Goals
	err := r.tx.QueryRowContext(ctx,
		`SELECT project_id, content, hash, status, created_at, updated_at FROM goals WHERE project_id=?`, projectID).
		Scan(&g.ProjectID, &g.Content, &g.Hash, &g.Status, &g.CreatedAt, &g.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Goals{}, ErrNotFound
	}
	return g, err
}
