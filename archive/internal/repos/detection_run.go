package repos

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/revelara-ai/orion/internal/database"
)

// ErrDetectionRunNotFound surfaces when GetByID misses.
var ErrDetectionRunNotFound = errors.New("repos: detection run not found")

// DetectionRunRepo persists SPEC §15.2 phase 7 run rows. Per-tenant
// surface: uses *database.RLSPool so org_id propagates from ctx.
type DetectionRunRepo struct {
	pool *database.RLSPool
}

// NewDetectionRunRepo wraps an RLSPool.
func NewDetectionRunRepo(p *database.RLSPool) *DetectionRunRepo {
	return &DetectionRunRepo{pool: p}
}

// Create inserts a new run row. OrgID and ID are assigned by the DB
// (org_id from RLS current_setting; id from gen_random_uuid()). The
// returned struct is the canonical post-insert shape.
func (r *DetectionRunRepo) Create(ctx context.Context, run DetectionRun) (DetectionRun, error) {
	const q = `
		INSERT INTO detection_runs (
			org_id,
			binding_id,
			mode,
			phase,
			quiescent,
			findings_total,
			findings_new,
			findings_deduped,
			findings_suppressed,
			orion_filed_processed,
			customer_filed_processed,
			polaris_prior_processed,
			finished_at,
			error_message,
			self_referential_warning
		) VALUES (
			current_setting('app.current_organization_id')::uuid,
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14
		)
		RETURNING id, org_id, started_at
	`
	row := r.pool.QueryRow(ctx, q,
		run.BindingID,
		string(run.Mode),
		string(run.Phase),
		run.Quiescent,
		run.FindingsTotal,
		run.FindingsNew,
		run.FindingsDeduped,
		run.FindingsSuppressed,
		run.OrionFiledProcessed,
		run.CustomerFiledProcessed,
		run.PolarisPriorProcessed,
		run.FinishedAt,
		run.ErrorMessage,
		run.SelfReferentialWarning,
	)
	if err := row.Scan(&run.ID, &run.OrgID, &run.StartedAt); err != nil {
		return DetectionRun{}, fmt.Errorf("repos: insert detection_run: %w", err)
	}
	return run, nil
}

// GetByID fetches one run by id within the RLS scope. Returns
// ErrDetectionRunNotFound if no row matches (either does not exist or
// is in another org).
func (r *DetectionRunRepo) GetByID(ctx context.Context, id uuid.UUID) (DetectionRun, error) {
	const q = `
		SELECT id, org_id, binding_id, mode, phase, quiescent,
		       findings_total, findings_new, findings_deduped, findings_suppressed,
		       orion_filed_processed, customer_filed_processed, polaris_prior_processed,
		       started_at, finished_at, error_message, self_referential_warning
		FROM detection_runs
		WHERE id = $1
	`
	row := r.pool.QueryRow(ctx, q, id)
	var run DetectionRun
	var mode, phase string
	err := row.Scan(
		&run.ID, &run.OrgID, &run.BindingID, &mode, &phase, &run.Quiescent,
		&run.FindingsTotal, &run.FindingsNew, &run.FindingsDeduped, &run.FindingsSuppressed,
		&run.OrionFiledProcessed, &run.CustomerFiledProcessed, &run.PolarisPriorProcessed,
		&run.StartedAt, &run.FinishedAt, &run.ErrorMessage, &run.SelfReferentialWarning,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return DetectionRun{}, ErrDetectionRunNotFound
	}
	if err != nil {
		return DetectionRun{}, fmt.Errorf("repos: get detection_run: %w", err)
	}
	run.Mode = DetectionRunMode(mode)
	run.Phase = DetectionPhase(phase)
	return run, nil
}

// ListByBinding returns runs for the binding, newest first. Limit
// caps the result set; pass 0 for the default of 50.
func (r *DetectionRunRepo) ListByBinding(ctx context.Context, bindingID uuid.UUID, limit int) ([]DetectionRun, error) {
	if limit <= 0 {
		limit = 50
	}
	const q = `
		SELECT id, org_id, binding_id, mode, phase, quiescent,
		       findings_total, findings_new, findings_deduped, findings_suppressed,
		       orion_filed_processed, customer_filed_processed, polaris_prior_processed,
		       started_at, finished_at, error_message, self_referential_warning
		FROM detection_runs
		WHERE binding_id = $1
		ORDER BY started_at DESC
		LIMIT $2
	`
	rows, err := r.pool.Query(ctx, q, bindingID, limit)
	if err != nil {
		return nil, fmt.Errorf("repos: list detection_runs: %w", err)
	}
	defer rows.Close()

	var out []DetectionRun
	for rows.Next() {
		var run DetectionRun
		var mode, phase string
		if err := rows.Scan(
			&run.ID, &run.OrgID, &run.BindingID, &mode, &phase, &run.Quiescent,
			&run.FindingsTotal, &run.FindingsNew, &run.FindingsDeduped, &run.FindingsSuppressed,
			&run.OrionFiledProcessed, &run.CustomerFiledProcessed, &run.PolarisPriorProcessed,
			&run.StartedAt, &run.FinishedAt, &run.ErrorMessage, &run.SelfReferentialWarning,
		); err != nil {
			return nil, fmt.Errorf("repos: scan detection_run: %w", err)
		}
		run.Mode = DetectionRunMode(mode)
		run.Phase = DetectionPhase(phase)
		out = append(out, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repos: iterate detection_runs: %w", err)
	}
	return out, nil
}

// CountByBinding returns how many runs exist for the binding within
// the RLS scope. Used by the §15.4 loopguard (first-3-runs
// suppression) and by quiescence framing on the UI.
func (r *DetectionRunRepo) CountByBinding(ctx context.Context, bindingID uuid.UUID) (int, error) {
	const q = `SELECT count(*) FROM detection_runs WHERE binding_id = $1`
	var n int
	if err := r.pool.QueryRow(ctx, q, bindingID).Scan(&n); err != nil {
		return 0, fmt.Errorf("repos: count detection_runs: %w", err)
	}
	return n, nil
}

// DetectionFindingRepo persists the per-finding ledger linked to a run.
// Per-tenant surface; uses *database.RLSPool.
type DetectionFindingRepo struct {
	pool *database.RLSPool
}

// NewDetectionFindingRepo wraps an RLSPool.
func NewDetectionFindingRepo(p *database.RLSPool) *DetectionFindingRepo {
	return &DetectionFindingRepo{pool: p}
}

// CreateBatch inserts all findings for a run in one statement (single
// RLS-scoped tx). The caller's RLS context determines org_id. Returns
// the inserted rows with their assigned IDs and timestamps in the same
// order as the input slice; on failure no rows are written so the
// run's ledger stays consistent.
//
// Implementation note: RLSPool exposes only Query/QueryRow/Exec, so a
// per-row tx loop would lose atomicity. We instead emit one INSERT
// with parallel arrays expanded via unnest() so all rows land in one
// tx with one round-trip.
func (r *DetectionFindingRepo) CreateBatch(ctx context.Context, findings []DetectionFinding) ([]DetectionFinding, error) {
	if len(findings) == 0 {
		return nil, nil
	}

	runIDs := make([]uuid.UUID, len(findings))
	slugs := make([]string, len(findings))
	titles := make([]string, len(findings))
	categories := make([]string, len(findings))
	confidences := make([]string, len(findings))
	severities := make([]string, len(findings))
	controlCodes := make([][]string, len(findings))
	filePaths := make([]string, len(findings))
	lineNos := make([]int32, len(findings))
	fingerprints := make([]string, len(findings))
	dedupSigs := make([]*string, len(findings))
	suppressed := make([]bool, len(findings))
	deduped := make([]bool, len(findings))

	for i, f := range findings {
		runIDs[i] = f.RunID
		slugs[i] = f.Slug
		titles[i] = f.Title
		categories[i] = f.Category
		confidences[i] = f.Confidence
		severities[i] = f.Severity
		codes := f.ControlCodes
		if codes == nil {
			codes = []string{}
		}
		controlCodes[i] = codes
		filePaths[i] = f.FilePath
		lineNos[i] = int32(f.LineNo) //nolint:gosec // G115: file line numbers from rvl-cli are bounded well below int32 max
		fingerprints[i] = f.Fingerprint
		dedupSigs[i] = f.DedupSignature
		suppressed[i] = f.Suppressed
		deduped[i] = f.Deduped
	}

	const q = `
		INSERT INTO detection_findings (
			org_id, run_id, slug, title, category, confidence, severity,
			control_codes, file_path, line_no, fingerprint, dedup_signature,
			suppressed, deduped
		)
		SELECT
			current_setting('app.current_organization_id')::uuid,
			r.run_id, r.slug, r.title, r.category, r.confidence, r.severity,
			r.control_codes, r.file_path, r.line_no, r.fingerprint, r.dedup_signature,
			r.suppressed, r.deduped
		FROM (
			SELECT *
			FROM unnest(
				$1::uuid[],
				$2::text[],
				$3::text[],
				$4::text[],
				$5::text[],
				$6::text[],
				$7::text[][],
				$8::text[],
				$9::int[],
				$10::text[],
				$11::text[],
				$12::bool[],
				$13::bool[]
			) WITH ORDINALITY AS u(
				run_id, slug, title, category, confidence, severity,
				control_codes, file_path, line_no, fingerprint, dedup_signature,
				suppressed, deduped, ord
			)
		) AS r
		RETURNING id, org_id, created_at
	`

	// unnest preserves array order; we requested WITH ORDINALITY so we
	// could ORDER BY ord if needed, but since RETURNING follows the
	// SELECT's row order the slice ordering is already deterministic.

	rows, err := r.pool.Query(ctx, q,
		runIDs, slugs, titles, categories, confidences, severities,
		controlCodes, filePaths, lineNos, fingerprints, dedupSigs,
		suppressed, deduped,
	)
	if err != nil {
		return nil, fmt.Errorf("repos: insert detection_findings: %w", err)
	}
	defer rows.Close()

	out := make([]DetectionFinding, 0, len(findings))
	i := 0
	for rows.Next() {
		f := findings[i]
		if err := rows.Scan(&f.ID, &f.OrgID, &f.CreatedAt); err != nil {
			return nil, fmt.Errorf("repos: scan detection_finding[%d]: %w", i, err)
		}
		out = append(out, f)
		i++
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repos: iterate detection_findings insert: %w", err)
	}
	if i != len(findings) {
		return nil, fmt.Errorf("repos: insert detection_findings: returned %d rows, expected %d", i, len(findings))
	}
	return out, nil
}

// ListByRun returns findings for one run, ordered by (file_path,
// line_no) so the caller can group by file deterministically.
func (r *DetectionFindingRepo) ListByRun(ctx context.Context, runID uuid.UUID) ([]DetectionFinding, error) {
	const q = `
		SELECT id, org_id, run_id, slug, title, category, confidence, severity,
		       control_codes, file_path, line_no, fingerprint, dedup_signature,
		       suppressed, deduped, created_at
		FROM detection_findings
		WHERE run_id = $1
		ORDER BY file_path, line_no
	`
	rows, err := r.pool.Query(ctx, q, runID)
	if err != nil {
		return nil, fmt.Errorf("repos: list detection_findings: %w", err)
	}
	defer rows.Close()

	var out []DetectionFinding
	for rows.Next() {
		var f DetectionFinding
		if err := rows.Scan(
			&f.ID, &f.OrgID, &f.RunID, &f.Slug, &f.Title, &f.Category, &f.Confidence, &f.Severity,
			&f.ControlCodes, &f.FilePath, &f.LineNo, &f.Fingerprint, &f.DedupSignature,
			&f.Suppressed, &f.Deduped, &f.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("repos: scan detection_finding: %w", err)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repos: iterate detection_findings: %w", err)
	}
	return out, nil
}

// ExistsByDedupSignature returns true if any finding within the RLS
// scope has the same dedup_signature. The cross-tick dedup short-
// circuit (§8.3 / §15.2 phase 4) uses this to decide whether to
// autofile a new finding or treat it as already-tracked. Returns false
// when signature is empty; an empty signature is the conservative
// "uniqueable" sentinel callers should treat as never-deduped.
func (r *DetectionFindingRepo) ExistsByDedupSignature(ctx context.Context, signature string) (bool, error) {
	if signature == "" {
		return false, nil
	}
	const q = `
		SELECT EXISTS(
			SELECT 1 FROM detection_findings
			WHERE dedup_signature = $1
		)
	`
	var exists bool
	if err := r.pool.QueryRow(ctx, q, signature).Scan(&exists); err != nil {
		return false, fmt.Errorf("repos: exists by dedup signature: %w", err)
	}
	return exists, nil
}
