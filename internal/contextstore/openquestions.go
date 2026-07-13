package contextstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// OpenQuestion is one persisted intake ambiguity (or-045a.6): a question the
// developer deferred or the conductor flagged, at goals/stpa/direction/spec
// altitude. It resolves ONLY through an explicit answer or an approved
// assumption — never silently.
type OpenQuestion struct {
	ID         string
	ProjectID  string
	Phase      string // goals | stpa | direction | spec
	Origin     string // grill | goals | direction | developer | …
	Key        string // optional decision key an answer records under
	Question   string
	Severity   string // blocking | advisory
	Status     string // open | answered | assumed
	Resolution string
	CreatedAt  string
	UpdatedAt  string
}

// OpenQuestionRepo persists the ledger.
type OpenQuestionRepo struct{ tx *sql.Tx }

// Create raises a question (status open).
func (r *OpenQuestionRepo) Create(ctx context.Context, q OpenQuestion) (string, error) {
	id, now := newID(), nowRFC3339()
	_, err := r.tx.ExecContext(ctx,
		`INSERT INTO open_questions (id, project_id, phase, origin, key, question, severity, status, resolution, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,?,'open','',?,?)`,
		id, q.ProjectID, q.Phase, q.Origin, q.Key, q.Question, q.Severity, now, now)
	if err != nil {
		return "", fmt.Errorf("raise open question: %w", err)
	}
	return id, nil
}

// ListOpen returns the project's OPEN questions, oldest first.
func (r *OpenQuestionRepo) ListOpen(ctx context.Context, projectID string) ([]OpenQuestion, error) {
	rows, err := r.tx.QueryContext(ctx,
		`SELECT id, project_id, phase, origin, key, question, severity, status, resolution, created_at, updated_at
		 FROM open_questions WHERE project_id=? AND status='open' ORDER BY created_at ASC, id ASC`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []OpenQuestion
	for rows.Next() {
		var q OpenQuestion
		if err := rows.Scan(&q.ID, &q.ProjectID, &q.Phase, &q.Origin, &q.Key, &q.Question, &q.Severity, &q.Status, &q.Resolution, &q.CreatedAt, &q.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// Get returns one question by id.
func (r *OpenQuestionRepo) Get(ctx context.Context, id string) (OpenQuestion, error) {
	var q OpenQuestion
	err := r.tx.QueryRowContext(ctx,
		`SELECT id, project_id, phase, origin, key, question, severity, status, resolution, created_at, updated_at
		 FROM open_questions WHERE id=?`, id).
		Scan(&q.ID, &q.ProjectID, &q.Phase, &q.Origin, &q.Key, &q.Question, &q.Severity, &q.Status, &q.Resolution, &q.CreatedAt, &q.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return OpenQuestion{}, ErrNotFound
	}
	return q, err
}

// Resolve marks a question answered|assumed with its resolution text. Only an
// OPEN question resolves (idempotence guard: resolving twice is an error).
func (r *OpenQuestionRepo) Resolve(ctx context.Context, id, status, resolution string) error {
	if status != "answered" && status != "assumed" {
		return fmt.Errorf("open question: status %q is not a resolution (answered|assumed)", status)
	}
	res, err := r.tx.ExecContext(ctx,
		`UPDATE open_questions SET status=?, resolution=?, updated_at=? WHERE id=? AND status='open'`,
		status, resolution, nowRFC3339(), id)
	if err != nil {
		return fmt.Errorf("resolve open question: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
