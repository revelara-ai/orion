package contextstore

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Run/phase survivability (or-v9f.16): the build pipeline tees every
// PhaseEvent here as the run progresses. The store is the truth a dying
// terminal cannot take with it — `orion conductor attach` tails these rows,
// and resume tooling reads the last persisted phase per cluster.

// RunEvent is one persisted build-phase transition.
type RunEvent struct {
	ID        int64
	ProjectID string
	RunID     string
	TaskID    string // empty for run-level (Decompose/Cluster/Integrate/Run) events
	Phase     string
	Status    string
	Detail    string
	CreatedAt string
}

// AppendRunEvent persists one phase transition. Single INSERT — safe from
// parallel clusters (the store's single connection serializes writers).
func (s *Store) AppendRunEvent(ctx context.Context, e RunEvent) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO run_events (project_id, run_id, task_id, phase, status, detail, created_at) VALUES (?,?,?,?,?,?,?)`,
		e.ProjectID, e.RunID, e.TaskID, e.Phase, e.Status, e.Detail, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

// ListRunEventsAfter returns events for a run with id > afterID, in order —
// the attach tail's read primitive.
func (s *Store) ListRunEventsAfter(ctx context.Context, runID string, afterID int64) ([]RunEvent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, run_id, task_id, phase, status, detail, created_at
		 FROM run_events WHERE run_id=? AND id>? ORDER BY id`, runID, afterID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []RunEvent
	for rows.Next() {
		var e RunEvent
		if err := rows.Scan(&e.ID, &e.ProjectID, &e.RunID, &e.TaskID, &e.Phase, &e.Status, &e.Detail, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// LatestRunID returns the most recent run recorded for a project.
func (s *Store) LatestRunID(ctx context.Context, projectID string) (string, bool, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT run_id FROM run_events WHERE project_id=? ORDER BY id DESC LIMIT 1`, projectID)
	var id string
	if err := row.Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	return id, true, nil
}

// ListRunIDs returns the project's most recent run ids, newest first — the
// drift comparison's axis (or-kzf.3).
func (s *Store) ListRunIDs(ctx context.Context, projectID string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT run_id, MAX(id) AS latest FROM run_events WHERE project_id=? GROUP BY run_id ORDER BY latest DESC LIMIT ?`,
		projectID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id string
		var latest int64
		if err := rows.Scan(&id, &latest); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
