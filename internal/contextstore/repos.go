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
	ID           string
	ProjectID    string
	Status       string // drafting | accepted | revised
	Version      int
	ParentSpecID string
	CreatedAt    string
	UpdatedAt    string
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

// Proof carries per-mode provenance and quantitative metrics so degradation is
// computable (PRD Core Data Model Hardening).
type Proof struct {
	ID                string
	TaskID            string
	Mode              string // behavioral | empirical | hazard
	Verdict           string // Accept | Reject | Inconclusive
	MutationScore     float64
	EmpiricalPassRate float64
	HazardControlled  int
	HazardTotal       int
	RunCount          int
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

func (r *SpecRepo) ForProject(ctx context.Context, projectID string) ([]Spec, error) {
	rows, err := r.tx.QueryContext(ctx,
		`SELECT id, project_id, status, version, COALESCE(parent_spec_id,''), created_at, updated_at
		 FROM specs WHERE project_id=? ORDER BY version`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Spec
	for rows.Next() {
		var s Spec
		if err := rows.Scan(&s.ID, &s.ProjectID, &s.Status, &s.Version, &s.ParentSpecID, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
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

// ── ProofRepo ────────────────────────────────────────────────────────────────

type ProofRepo struct{ tx *sql.Tx }

func (r *ProofRepo) Create(ctx context.Context, taskID string, p Proof) (string, error) {
	id, now := newID(), nowRFC3339()
	_, err := r.tx.ExecContext(ctx,
		`INSERT INTO proofs
		   (id, task_id, mode, verdict, mutation_score, empirical_pass_rate,
		    hazard_controlled_count, hazard_total_count, run_count, created_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?)`,
		id, taskID, p.Mode, p.Verdict, p.MutationScore, p.EmpiricalPassRate,
		p.HazardControlled, p.HazardTotal, p.RunCount, now)
	if err != nil {
		return "", fmt.Errorf("create proof: %w", err)
	}
	return id, nil
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
