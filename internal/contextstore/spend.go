package contextstore

import (
	"context"
	"time"
)

// Spend ledger (or-v9f.28): write-through rows from the budget accountant.

// AppendSpend records one attributed spend row.
func (s *Store) AppendSpend(ctx context.Context, projectID, runID, role, modelRef string, tokens int, dollars float64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO spend_ledger (project_id, run_id, role, model_ref, tokens, dollars, recorded_at) VALUES (?,?,?,?,?,?,?)`,
		projectID, runID, role, modelRef, tokens, dollars, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

// SumSpend returns a project's cumulative tokens + dollars.
func (s *Store) SumSpend(ctx context.Context, projectID string) (int, float64, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(tokens),0), COALESCE(SUM(dollars),0) FROM spend_ledger WHERE project_id=?`, projectID)
	var tokens int
	var dollars float64
	err := row.Scan(&tokens, &dollars)
	return tokens, dollars, err
}

// SpendRow is one role/model aggregation line.
type SpendRow struct {
	Role, ModelRef string
	Tokens         int
	Dollars        float64
}

// SpendByRole aggregates a project's ledger by role+model, biggest first.
func (s *Store) SpendByRole(ctx context.Context, projectID string) ([]SpendRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT role, model_ref, COALESCE(SUM(tokens),0), COALESCE(SUM(dollars),0)
		 FROM spend_ledger WHERE project_id=? GROUP BY role, model_ref ORDER BY SUM(dollars) DESC, SUM(tokens) DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []SpendRow
	for rows.Next() {
		var r SpendRow
		if err := rows.Scan(&r.Role, &r.ModelRef, &r.Tokens, &r.Dollars); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DominantModelByRun returns each run's most-token-heavy model_ref — the
// stratification key for the longitudinal harness eval (or-gb1.2).
func (s *Store) DominantModelByRun(ctx context.Context, projectID string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT run_id, model_ref, SUM(tokens) AS t FROM spend_ledger WHERE project_id=? GROUP BY run_id, model_ref ORDER BY run_id, t ASC`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var runID, model string
		var tokens int
		if err := rows.Scan(&runID, &model, &tokens); err != nil {
			return nil, err
		}
		out[runID] = model // ascending token order → the last write per run wins (dominant)
	}
	return out, rows.Err()
}
