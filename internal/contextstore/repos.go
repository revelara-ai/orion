package contextstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrNotFound is returned when a requested row does not exist.
var ErrNotFound = errors.New("contextstore: not found")

// ── Domain types ─────────────────────────────────────────────────────────────

type Project struct {
	ID        string
	Name      string
	Intent    string
	CreatedAt string
	UpdatedAt string
}

type Spec struct {
	ID               string
	ProjectID        string
	Status           string // drafting | accepted | revised
	Version          int
	ParentSpecID     string
	Hash             string
	ResponseContract string // JSON
	Requirements     string // JSON array of spec.Requirement
	CreatedAt        string
	UpdatedAt        string
}

// Decision is a developer's answer to a required decision.
type Decision struct {
	Key       string
	Value     string
	ValueKind string // precise | fallback_preset
}

type Task struct {
	ID        string
	EpicID    string
	Title     string
	Status    string // ready|in_progress|being_validated|proven|integrated|done
	FileScope string
	ProofID   string
	CreatedAt string
	UpdatedAt string
}

// Attempt is a recorded task attempt with its idempotency key and the agent's
// untrusted evidence claim.
type Attempt struct {
	ID             string
	TaskID         string
	IdempotencyKey string
	EvidenceClaim  string
	CreatedAt      string
}

// Artifact references a file an agent produced for a task.
type Artifact struct {
	ID           string
	TaskID       string
	ArtifactType string
	StoragePath  string
	ContentHash  string
	CreatedAt    string
}

// Epic is an accepted spec's unit of delivery.
type Epic struct {
	ID        string
	ProjectID string
	SpecID    string
	Title     string
}

// Proof carries per-mode provenance and quantitative metrics so degradation is
// computable (PRD Core Data Model Hardening).
type Proof struct {
	ID                string
	TaskID            string
	Mode              string // behavioral | empirical | hazard | converged
	Verdict           string // Accept | Reject | Inconclusive
	MutationScore     float64
	EmpiricalPassRate float64
	HazardControlled  int
	HazardTotal       int
	RunCount          int
	Detail            string // mode-specific JSON (e.g. empirical {port_open,...})
	CreatedAt         string
}

// ── ProjectRepo ──────────────────────────────────────────────────────────────

type ProjectRepo struct{ tx *sql.Tx }

func (r *ProjectRepo) Create(ctx context.Context, name, intent string) (string, error) {
	id, now := newID(), nowRFC3339()
	_, err := r.tx.ExecContext(ctx,
		`INSERT INTO projects (id, name, intent, created_at, updated_at) VALUES (?,?,?,?,?)`,
		id, name, intent, now, now)
	if err != nil {
		return "", fmt.Errorf("create project: %w", err)
	}
	return id, nil
}

func (r *ProjectRepo) Get(ctx context.Context, id string) (Project, error) {
	var p Project
	err := r.tx.QueryRowContext(ctx,
		`SELECT id, name, intent, created_at, updated_at FROM projects WHERE id=?`, id).
		Scan(&p.ID, &p.Name, &p.Intent, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	return p, err
}

// Latest returns the most recently created project.
func (r *ProjectRepo) Latest(ctx context.Context) (Project, error) {
	var p Project
	err := r.tx.QueryRowContext(ctx,
		`SELECT id, name, intent, created_at, updated_at FROM projects ORDER BY created_at DESC, id DESC LIMIT 1`).
		Scan(&p.ID, &p.Name, &p.Intent, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	return p, err
}

func (r *ProjectRepo) List(ctx context.Context) ([]Project, error) {
	rows, err := r.tx.QueryContext(ctx,
		`SELECT id, name, intent, created_at, updated_at FROM projects ORDER BY created_at DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Name, &p.Intent, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ── SpecRepo ─────────────────────────────────────────────────────────────────

type SpecRepo struct{ tx *sql.Tx }

// CreateDraft creates a version-1 spec in 'drafting' status for a project.
func (r *SpecRepo) CreateDraft(ctx context.Context, projectID string) (string, error) {
	id, now := newID(), nowRFC3339()
	_, err := r.tx.ExecContext(ctx,
		`INSERT INTO specs (id, project_id, status, version, created_at, updated_at)
		 VALUES (?,?, 'drafting', 1, ?, ?)`, id, projectID, now, now)
	if err != nil {
		return "", fmt.Errorf("create spec: %w", err)
	}
	return id, nil
}

// SetStatus updates a spec's lifecycle status.
func (r *SpecRepo) SetStatus(ctx context.Context, id, status string) error {
	res, err := r.tx.ExecContext(ctx,
		`UPDATE specs SET status=?, updated_at=? WHERE id=?`, status, nowRFC3339(), id)
	if err != nil {
		return fmt.Errorf("set spec status: %w", err)
	}
	return mustAffectOne(res, "spec")
}

// SetAccepted marks a spec accepted and anchors it with its hash + the compiled
// ResponseContract.
func (r *SpecRepo) SetAccepted(ctx context.Context, id, hash, responseContract string) error {
	res, err := r.tx.ExecContext(ctx,
		`UPDATE specs SET status='accepted', spec_hash=?, response_contract=?, updated_at=? WHERE id=?`,
		hash, responseContract, nowRFC3339(), id)
	if err != nil {
		return fmt.Errorf("set spec accepted: %w", err)
	}
	return mustAffectOne(res, "spec")
}

const specCols = `id, project_id, status, version, COALESCE(parent_spec_id,''), spec_hash, response_contract, requirements, created_at, updated_at`

func scanSpec(sc interface{ Scan(...any) error }) (Spec, error) {
	var s Spec
	err := sc.Scan(&s.ID, &s.ProjectID, &s.Status, &s.Version, &s.ParentSpecID, &s.Hash, &s.ResponseContract, &s.Requirements, &s.CreatedAt, &s.UpdatedAt)
	return s, err
}

// SetRequirements overwrites the persisted requirements JSON on a draft spec.
func (r *SpecRepo) SetRequirements(ctx context.Context, id, requirementsJSON string) error {
	res, err := r.tx.ExecContext(ctx,
		`UPDATE specs SET requirements=?, updated_at=? WHERE id=?`, requirementsJSON, nowRFC3339(), id)
	if err != nil {
		return fmt.Errorf("set spec requirements: %w", err)
	}
	return mustAffectOne(res, "spec")
}

func (r *SpecRepo) Get(ctx context.Context, id string) (Spec, error) {
	s, err := scanSpec(r.tx.QueryRowContext(ctx, `SELECT `+specCols+` FROM specs WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Spec{}, ErrNotFound
	}
	return s, err
}

// LatestForProject returns the most recent spec for a project.
func (r *SpecRepo) LatestForProject(ctx context.Context, projectID string) (Spec, error) {
	s, err := scanSpec(r.tx.QueryRowContext(ctx,
		`SELECT `+specCols+` FROM specs WHERE project_id=? ORDER BY version DESC, created_at DESC LIMIT 1`, projectID))
	if errors.Is(err, sql.ErrNoRows) {
		return Spec{}, ErrNotFound
	}
	return s, err
}

func (r *SpecRepo) ForProject(ctx context.Context, projectID string) ([]Spec, error) {
	rows, err := r.tx.QueryContext(ctx, `SELECT `+specCols+` FROM specs WHERE project_id=? ORDER BY version`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Spec
	for rows.Next() {
		s, err := scanSpec(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ── DecisionRepo ─────────────────────────────────────────────────────────────

type DecisionRepo struct{ tx *sql.Tx }

// Create records a developer's answer to a required decision.
func (r *DecisionRepo) Create(ctx context.Context, projectID, specID, key, value, valueKind string, securityRelevant bool) (string, error) {
	id, now := newID(), nowRFC3339()
	sr := 0
	if securityRelevant {
		sr = 1
	}
	var specArg any
	if specID != "" {
		specArg = specID
	}
	_, err := r.tx.ExecContext(ctx,
		`INSERT INTO decisions (id, project_id, spec_id, key, value, value_kind, security_relevant, created_at)
		 VALUES (?,?,?,?,?,?,?,?)`, id, projectID, specArg, key, value, valueKind, sr, now)
	if err != nil {
		return "", fmt.Errorf("create decision: %w", err)
	}
	return id, nil
}

// ListForSpec returns the latest answer per key for a spec (last write wins).
func (r *DecisionRepo) ListForSpec(ctx context.Context, specID string) ([]Decision, error) {
	rows, err := r.tx.QueryContext(ctx,
		`SELECT key, value, value_kind FROM decisions WHERE spec_id=? ORDER BY created_at`, specID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	latest := map[string]Decision{}
	var order []string
	for rows.Next() {
		var d Decision
		if err := rows.Scan(&d.Key, &d.Value, &d.ValueKind); err != nil {
			return nil, err
		}
		if _, seen := latest[d.Key]; !seen {
			order = append(order, d.Key)
		}
		latest[d.Key] = d
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]Decision, 0, len(order))
	for _, k := range order {
		out = append(out, latest[k])
	}
	return out, nil
}

// ── SpecDimensionRepo ────────────────────────────────────────────────────────

type SpecDimensionRepo struct{ tx *sql.Tx }

// Upsert writes a typed spec dimension (one row per dimension per spec).
func (r *SpecDimensionRepo) Upsert(ctx context.Context, specID, dimension, valueStructured, valueKind string, tierRequired bool) error {
	tr := 0
	if tierRequired {
		tr = 1
	}
	id, now := newID(), nowRFC3339()
	_, err := r.tx.ExecContext(ctx,
		`INSERT INTO spec_dimensions (id, spec_id, dimension, value_structured, value_kind, tier_required, resolved_at)
		 VALUES (?,?,?,?,?,?,?)
		 ON CONFLICT(spec_id, dimension) DO UPDATE SET
		   value_structured=excluded.value_structured,
		   value_kind=excluded.value_kind,
		   tier_required=excluded.tier_required,
		   resolved_at=excluded.resolved_at`,
		id, specID, dimension, valueStructured, valueKind, tr, now)
	if err != nil {
		return fmt.Errorf("upsert spec dimension: %w", err)
	}
	return nil
}

// CountForSpec returns how many dimensions are persisted for a spec.
func (r *SpecDimensionRepo) CountForSpec(ctx context.Context, specID string) (int, error) {
	var n int
	err := r.tx.QueryRowContext(ctx, `SELECT count(*) FROM spec_dimensions WHERE spec_id=?`, specID).Scan(&n)
	return n, err
}

// ── EpicRepo ─────────────────────────────────────────────────────────────────

type EpicRepo struct{ tx *sql.Tx }

func (r *EpicRepo) Create(ctx context.Context, projectID, specID, title string) (string, error) {
	id, now := newID(), nowRFC3339()
	_, err := r.tx.ExecContext(ctx,
		`INSERT INTO epics (id, project_id, spec_id, title, created_at) VALUES (?,?,?,?,?)`,
		id, projectID, specID, title, now)
	if err != nil {
		return "", fmt.Errorf("create epic: %w", err)
	}
	return id, nil
}

func (r *EpicRepo) Get(ctx context.Context, id string) (Epic, error) {
	var e Epic
	err := r.tx.QueryRowContext(ctx,
		`SELECT id, project_id, spec_id, title FROM epics WHERE id=?`, id).
		Scan(&e.ID, &e.ProjectID, &e.SpecID, &e.Title)
	if errors.Is(err, sql.ErrNoRows) {
		return Epic{}, ErrNotFound
	}
	return e, err
}

// LatestForProject returns the most recent epic for a project.
func (r *EpicRepo) LatestForProject(ctx context.Context, projectID string) (Epic, error) {
	var e Epic
	err := r.tx.QueryRowContext(ctx,
		`SELECT id, project_id, spec_id, title FROM epics WHERE project_id=? ORDER BY created_at DESC LIMIT 1`, projectID).
		Scan(&e.ID, &e.ProjectID, &e.SpecID, &e.Title)
	if errors.Is(err, sql.ErrNoRows) {
		return Epic{}, ErrNotFound
	}
	return e, err
}

// ── ProofObligationRepo ──────────────────────────────────────────────────────

type ProofObligationRepo struct{ tx *sql.Tx }

func (r *ProofObligationRepo) Create(ctx context.Context, taskID, clause string) (string, error) {
	id, now := newID(), nowRFC3339()
	_, err := r.tx.ExecContext(ctx,
		`INSERT INTO proof_obligations (id, task_id, clause, created_at) VALUES (?,?,?,?)`, id, taskID, clause, now)
	if err != nil {
		return "", fmt.Errorf("create proof obligation: %w", err)
	}
	return id, nil
}

// ListForTask returns the obligation clauses for a task, in order.
func (r *ProofObligationRepo) ListForTask(ctx context.Context, taskID string) ([]string, error) {
	rows, err := r.tx.QueryContext(ctx,
		`SELECT clause FROM proof_obligations WHERE task_id=? ORDER BY created_at`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ── TaskRepo (the Task graph) ────────────────────────────────────────────────

type TaskRepo struct{ tx *sql.Tx }

func (r *TaskRepo) Create(ctx context.Context, epicID, title, fileScope string) (string, error) {
	id, now := newID(), nowRFC3339()
	_, err := r.tx.ExecContext(ctx,
		`INSERT INTO tasks (id, epic_id, title, status, file_scope, created_at, updated_at)
		 VALUES (?,?,?, 'ready', ?,?,?)`, id, epicID, title, fileScope, now, now)
	if err != nil {
		return "", fmt.Errorf("create task: %w", err)
	}
	return id, nil
}

// SetStatus transitions a task's status. The DB done-gate trigger rejects a
// proven/done transition unless proof_id references an Accept proof.
func (r *TaskRepo) SetStatus(ctx context.Context, id, status string) error {
	res, err := r.tx.ExecContext(ctx,
		`UPDATE tasks SET status=?, updated_at=? WHERE id=?`, status, nowRFC3339(), id)
	if err != nil {
		return fmt.Errorf("set task status: %w", err)
	}
	return mustAffectOne(res, "task")
}

// SetProofAndStatus attaches a proof and transitions status in one statement so
// the done-gate trigger sees both together.
func (r *TaskRepo) SetProofAndStatus(ctx context.Context, id, proofID, status string) error {
	res, err := r.tx.ExecContext(ctx,
		`UPDATE tasks SET proof_id=?, status=?, updated_at=? WHERE id=?`,
		proofID, status, nowRFC3339(), id)
	if err != nil {
		return fmt.Errorf("set task proof+status: %w", err)
	}
	return mustAffectOne(res, "task")
}

// AddDep records that task dependsOn another (the dependency DAG edge).
func (r *TaskRepo) AddDep(ctx context.Context, taskID, dependsOn string) error {
	_, err := r.tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO task_deps (task_id, depends_on) VALUES (?,?)`, taskID, dependsOn)
	if err != nil {
		return fmt.Errorf("add task dep: %w", err)
	}
	return nil
}

// ListByEpic returns an epic's tasks in creation order.
func (r *TaskRepo) ListByEpic(ctx context.Context, epicID string) ([]Task, error) {
	rows, err := r.tx.QueryContext(ctx,
		`SELECT id, epic_id, title, status, file_scope, COALESCE(proof_id,''), created_at, updated_at
		 FROM tasks WHERE epic_id=? ORDER BY created_at`, epicID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.EpicID, &t.Title, &t.Status, &t.FileScope, &t.ProofID, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// DepsOf returns the task ids a task depends on.
func (r *TaskRepo) DepsOf(ctx context.Context, taskID string) ([]string, error) {
	rows, err := r.tx.QueryContext(ctx, `SELECT depends_on FROM task_deps WHERE task_id=? ORDER BY depends_on`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (r *TaskRepo) Get(ctx context.Context, id string) (Task, error) {
	var t Task
	err := r.tx.QueryRowContext(ctx,
		`SELECT id, epic_id, title, status, file_scope, COALESCE(proof_id,''), created_at, updated_at
		 FROM tasks WHERE id=?`, id).
		Scan(&t.ID, &t.EpicID, &t.Title, &t.Status, &t.FileScope, &t.ProofID, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	return t, err
}

// ── AttemptRepo ──────────────────────────────────────────────────────────────

type AttemptRepo struct{ tx *sql.Tx }

// Create records a task attempt with an idempotency key (UNIQUE per task) so a
// restarted agent detects and skips an already-committed side effect.
func (r *AttemptRepo) Create(ctx context.Context, taskID, idempotencyKey string) (string, error) {
	return r.CreateWithClaim(ctx, taskID, idempotencyKey, "{}")
}

// CreateWithClaim records an attempt together with the agent's untrusted
// EvidenceClaim (stored verbatim as JSON). The claim is persisted as a claim,
// never as a verdict — the proof domain recomputes verdicts independently.
func (r *AttemptRepo) CreateWithClaim(ctx context.Context, taskID, idempotencyKey, evidenceClaimJSON string) (string, error) {
	if evidenceClaimJSON == "" {
		evidenceClaimJSON = "{}"
	}
	id, now := newID(), nowRFC3339()
	_, err := r.tx.ExecContext(ctx,
		`INSERT INTO task_attempts (id, task_id, idempotency_key, evidence_claim, created_at) VALUES (?,?,?,?,?)`,
		id, taskID, idempotencyKey, evidenceClaimJSON, now)
	if err != nil {
		return "", fmt.Errorf("create task attempt: %w", err)
	}
	return id, nil
}

// CountByTask returns the number of attempts recorded for a task.
func (r *AttemptRepo) CountByTask(ctx context.Context, taskID string) (int, error) {
	var n int
	err := r.tx.QueryRowContext(ctx, `SELECT count(*) FROM task_attempts WHERE task_id=?`, taskID).Scan(&n)
	return n, err
}

// HasAttempt reports whether an attempt with the given idempotency key already
// committed for a task — the check a restarted agent uses to skip an
// already-applied side effect.
func (r *AttemptRepo) HasAttempt(ctx context.Context, taskID, idempotencyKey string) (bool, error) {
	var n int
	err := r.tx.QueryRowContext(ctx,
		`SELECT count(*) FROM task_attempts WHERE task_id=? AND idempotency_key=?`, taskID, idempotencyKey).Scan(&n)
	return n > 0, err
}

// List returns a task's attempts in order.
func (r *AttemptRepo) List(ctx context.Context, taskID string) ([]Attempt, error) {
	rows, err := r.tx.QueryContext(ctx,
		`SELECT id, task_id, idempotency_key, evidence_claim, created_at FROM task_attempts WHERE task_id=? ORDER BY created_at`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Attempt
	for rows.Next() {
		var a Attempt
		if err := rows.Scan(&a.ID, &a.TaskID, &a.IdempotencyKey, &a.EvidenceClaim, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ── ProofRepo ────────────────────────────────────────────────────────────────

type ProofRepo struct{ tx *sql.Tx }

func (r *ProofRepo) Create(ctx context.Context, taskID string, p Proof) (string, error) {
	id, now := newID(), nowRFC3339()
	detail := p.Detail
	if detail == "" {
		detail = "{}"
	}
	_, err := r.tx.ExecContext(ctx,
		`INSERT INTO proofs
		   (id, task_id, mode, verdict, mutation_score, empirical_pass_rate,
		    hazard_controlled_count, hazard_total_count, run_count, detail, created_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		id, taskID, p.Mode, p.Verdict, p.MutationScore, p.EmpiricalPassRate,
		p.HazardControlled, p.HazardTotal, p.RunCount, detail, now)
	if err != nil {
		return "", fmt.Errorf("create proof: %w", err)
	}
	return id, nil
}

// GetByTaskMode returns the latest proof for a task in a given mode.
func (r *ProofRepo) GetByTaskMode(ctx context.Context, taskID, mode string) (Proof, error) {
	var p Proof
	err := r.tx.QueryRowContext(ctx,
		`SELECT id, task_id, mode, verdict, mutation_score, empirical_pass_rate,
		        hazard_controlled_count, hazard_total_count, run_count, detail, created_at
		 FROM proofs WHERE task_id=? AND mode=? ORDER BY created_at DESC LIMIT 1`, taskID, mode).
		Scan(&p.ID, &p.TaskID, &p.Mode, &p.Verdict, &p.MutationScore, &p.EmpiricalPassRate,
			&p.HazardControlled, &p.HazardTotal, &p.RunCount, &p.Detail, &p.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Proof{}, ErrNotFound
	}
	return p, err
}

// CountByTask returns the number of proofs recorded for a task.
func (r *ProofRepo) CountByTask(ctx context.Context, taskID string) (int, error) {
	var n int
	err := r.tx.QueryRowContext(ctx, `SELECT count(*) FROM proofs WHERE task_id=?`, taskID).Scan(&n)
	return n, err
}

// ── ArtifactRepo ─────────────────────────────────────────────────────────────

type ArtifactRepo struct{ tx *sql.Tx }

func (r *ArtifactRepo) Create(ctx context.Context, taskID, artifactType, storagePath, contentHash string) (string, error) {
	id, now := newID(), nowRFC3339()
	_, err := r.tx.ExecContext(ctx,
		`INSERT INTO artifacts (id, task_id, artifact_type, storage_path, content_hash, created_at)
		 VALUES (?,?,?,?,?,?)`, id, taskID, artifactType, storagePath, contentHash, now)
	if err != nil {
		return "", fmt.Errorf("create artifact: %w", err)
	}
	return id, nil
}

// ListByTask returns the artifacts a task produced (ancestor outputs for recall).
func (r *ArtifactRepo) ListByTask(ctx context.Context, taskID string) ([]Artifact, error) {
	rows, err := r.tx.QueryContext(ctx,
		`SELECT id, task_id, artifact_type, storage_path, content_hash, created_at FROM artifacts WHERE task_id=? ORDER BY created_at`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Artifact
	for rows.Next() {
		var a Artifact
		if err := rows.Scan(&a.ID, &a.TaskID, &a.ArtifactType, &a.StoragePath, &a.ContentHash, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ── FailureModeRepo ──────────────────────────────────────────────────────────

type FailureModeRepo struct{ tx *sql.Tx }

// Record inserts a failure mode, deduped by canonical_key (Story 30: never
// silently repeat a known failure). A repeat returns the existing id.
func (r *FailureModeRepo) Record(ctx context.Context, projectID, category, componentType, symptomClass string) (string, error) {
	key := CanonicalKey(category, componentType, symptomClass)
	id, now := newID(), nowRFC3339()
	var projArg any
	if projectID != "" {
		projArg = projectID
	}
	_, err := r.tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO failure_modes
		   (id, project_id, category, component_type, symptom_class, canonical_key, created_at)
		 VALUES (?,?,?,?,?,?,?)`,
		id, projArg, category, componentType, symptomClass, key, now)
	if err != nil {
		return "", fmt.Errorf("record failure mode: %w", err)
	}
	var existing string
	if err := r.tx.QueryRowContext(ctx,
		`SELECT id FROM failure_modes WHERE canonical_key=?`, key).Scan(&existing); err != nil {
		return "", fmt.Errorf("lookup failure mode: %w", err)
	}
	return existing, nil
}

// ── DeliveryRepo ─────────────────────────────────────────────────────────────

// Delivery is a recorded delivery with its operating envelope.
type Delivery struct {
	ID                string
	EpicID            string
	OperatingEnvelope string // JSON
	Runbook           string // JSON
	CreatedAt         string
}

type DeliveryRepo struct{ tx *sql.Tx }

func (r *DeliveryRepo) Create(ctx context.Context, epicID, operatingEnvelope, runbook string) (string, error) {
	if runbook == "" {
		runbook = "{}"
	}
	id, now := newID(), nowRFC3339()
	_, err := r.tx.ExecContext(ctx,
		`INSERT INTO deliveries (id, epic_id, operating_envelope, runbook, created_at) VALUES (?,?,?,?,?)`,
		id, epicID, operatingEnvelope, runbook, now)
	if err != nil {
		return "", fmt.Errorf("create delivery: %w", err)
	}
	return id, nil
}

// LatestForProject returns the most recent delivery for a project (via its epic).
func (r *DeliveryRepo) LatestForProject(ctx context.Context, projectID string) (Delivery, bool, error) {
	var d Delivery
	err := r.tx.QueryRowContext(ctx,
		`SELECT dl.id, dl.epic_id, dl.operating_envelope, dl.runbook, dl.created_at
		 FROM deliveries dl JOIN epics e ON dl.epic_id = e.id
		 WHERE e.project_id=? ORDER BY dl.created_at DESC LIMIT 1`, projectID).
		Scan(&d.ID, &d.EpicID, &d.OperatingEnvelope, &d.Runbook, &d.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Delivery{}, false, nil
	}
	if err != nil {
		return Delivery{}, false, err
	}
	return d, true, nil
}

// ── EscalationRepo ───────────────────────────────────────────────────────────

type EscalationRepo struct{ tx *sql.Tx }

func (r *EscalationRepo) Create(ctx context.Context, projectID, taskID, reason string) (string, error) {
	id, now := newID(), nowRFC3339()
	var taskArg any
	if taskID != "" {
		taskArg = taskID
	}
	_, err := r.tx.ExecContext(ctx,
		`INSERT INTO escalations (id, project_id, task_id, reason, resolved, created_at) VALUES (?,?,?,?,0,?)`,
		id, projectID, taskArg, reason, now)
	if err != nil {
		return "", fmt.Errorf("create escalation: %w", err)
	}
	return id, nil
}

// CountForProject returns how many escalations a project has.
func (r *EscalationRepo) CountForProject(ctx context.Context, projectID string) (int, error) {
	var n int
	err := r.tx.QueryRowContext(ctx, `SELECT count(*) FROM escalations WHERE project_id=?`, projectID).Scan(&n)
	return n, err
}

// ── PolarisContextRepo ───────────────────────────────────────────────────────

// PolarisCacheEntry is a cached Polaris payload (controls/knowledge/risks) with
// its freshness metadata.
type PolarisCacheEntry struct {
	Kind       string
	Payload    string
	FetchedAt  string
	TTLSeconds int
}

type PolarisContextRepo struct{ tx *sql.Tx }

// Upsert caches a Polaris payload for a project+kind (replace-by-kind).
func (r *PolarisContextRepo) Upsert(ctx context.Context, projectID, kind, payload string, ttlSeconds int) error {
	if _, err := r.tx.ExecContext(ctx, `DELETE FROM polaris_context WHERE project_id=? AND kind=?`, projectID, kind); err != nil {
		return fmt.Errorf("clear polaris cache: %w", err)
	}
	_, err := r.tx.ExecContext(ctx,
		`INSERT INTO polaris_context (id, project_id, kind, payload, fetched_at, ttl_seconds) VALUES (?,?,?,?,?,?)`,
		newID(), projectID, kind, payload, nowRFC3339(), ttlSeconds)
	if err != nil {
		return fmt.Errorf("cache polaris context: %w", err)
	}
	return nil
}

// Get returns the cached entry for a project+kind, if present.
func (r *PolarisContextRepo) Get(ctx context.Context, projectID, kind string) (PolarisCacheEntry, bool, error) {
	var e PolarisCacheEntry
	err := r.tx.QueryRowContext(ctx,
		`SELECT kind, payload, fetched_at, ttl_seconds FROM polaris_context WHERE project_id=? AND kind=? LIMIT 1`,
		projectID, kind).Scan(&e.Kind, &e.Payload, &e.FetchedAt, &e.TTLSeconds)
	if errors.Is(err, sql.ErrNoRows) {
		return PolarisCacheEntry{}, false, nil
	}
	if err != nil {
		return PolarisCacheEntry{}, false, err
	}
	return e, true, nil
}

// ── WorktreeRepo ─────────────────────────────────────────────────────────────

// WorktreeRecord is a persisted per-task worktree (keyed by issue id).
type WorktreeRecord struct {
	IssueID   string
	Path      string
	Branch    string
	Status    string // active | removing | gone
	CreatedAt string
	UpdatedAt string
}

type WorktreeRepo struct{ tx *sql.Tx }

// Upsert records (or updates) a worktree for an issue id.
func (r *WorktreeRepo) Upsert(ctx context.Context, issueID, path, branch, status string) error {
	now := nowRFC3339()
	_, err := r.tx.ExecContext(ctx,
		`INSERT INTO worktrees (issue_id, path, branch, status, created_at, updated_at)
		 VALUES (?,?,?,?,?,?)
		 ON CONFLICT(issue_id) DO UPDATE SET path=excluded.path, branch=excluded.branch,
		   status=excluded.status, updated_at=excluded.updated_at`,
		issueID, path, branch, status, now, now)
	if err != nil {
		return fmt.Errorf("upsert worktree: %w", err)
	}
	return nil
}

// SetStatus updates a worktree record's status.
func (r *WorktreeRepo) SetStatus(ctx context.Context, issueID, status string) error {
	_, err := r.tx.ExecContext(ctx, `UPDATE worktrees SET status=?, updated_at=? WHERE issue_id=?`,
		status, nowRFC3339(), issueID)
	return err
}

// Delete removes a worktree record.
func (r *WorktreeRepo) Delete(ctx context.Context, issueID string) error {
	_, err := r.tx.ExecContext(ctx, `DELETE FROM worktrees WHERE issue_id=?`, issueID)
	return err
}

func (r *WorktreeRepo) Get(ctx context.Context, issueID string) (WorktreeRecord, error) {
	var w WorktreeRecord
	err := r.tx.QueryRowContext(ctx,
		`SELECT issue_id, path, branch, status, created_at, updated_at FROM worktrees WHERE issue_id=?`, issueID).
		Scan(&w.IssueID, &w.Path, &w.Branch, &w.Status, &w.CreatedAt, &w.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return WorktreeRecord{}, ErrNotFound
	}
	return w, err
}

func (r *WorktreeRepo) List(ctx context.Context) ([]WorktreeRecord, error) {
	rows, err := r.tx.QueryContext(ctx,
		`SELECT issue_id, path, branch, status, created_at, updated_at FROM worktrees ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WorktreeRecord
	for rows.Next() {
		var w WorktreeRecord
		if err := rows.Scan(&w.IssueID, &w.Path, &w.Branch, &w.Status, &w.CreatedAt, &w.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// ── helpers ──────────────────────────────────────────────────────────────────

func mustAffectOne(res sql.Result, entity string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%s: %w", entity, ErrNotFound)
	}
	return nil
}
