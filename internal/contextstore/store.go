// Package contextstore is Orion's durable source of truth (or-xgj), backed by
// SQLite in WAL mode. It holds the full V2 structured context — projects, specs
// (+ typed dimensions), decisions, the Epic/Task DAG, attempts, proof
// obligations, proofs, deliveries, escalations, failure modes, artifacts, and
// cached Polaris context — behind per-aggregate repositories that share a
// transaction boundary. A tracker (beads/GitHub) is a one-way projection of the
// task subset, never the source of truth.
//
// Manifesto: the Context Store is the durable source of truth; the done-gate is
// a DB constraint (a task cannot reach proven/done without a proof_id whose
// verdict is Accept), not merely orchestrator code.
package contextstore

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the pure-Go sqlite driver
)

//go:embed schema.sql
var schemaSQL string

// DBFile is the Context Store filename inside the data dir.
const DBFile = "orion.db"

// Store is the Context Store handle. Safe for concurrent use.
type Store struct {
	db  *sql.DB
	dir string
}

// Open opens (creating if needed) the Context Store under dir, enabling WAL,
// foreign keys, and a busy timeout, then applies the schema. Crash-safe writes
// depend on WAL + transactional commits.
//
// Concurrency contract (or-v9f.22): transactions BEGIN immediate (_txlock), so
// writers serialize ACROSS PROCESSES through SQLite's busy_timeout — two orion
// processes on one store never race a read-modify-write to a snapshot-upgrade
// error, and application invariants held inside WithTx (single-active project,
// queue promotion) hold machine-wide. In-process access is additionally
// serialized by the single connection (SetMaxOpenConns(1)); WAL readers are
// unaffected by the reserved write lock.
func Open(dir string) (*Store, error) {
	path := filepath.Join(dir, DBFile)
	dsn := "file:" + path + "?_txlock=immediate&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("contextstore open: %w", err)
	}
	// modernc/sqlite tolerates concurrent readers; a single writer connection
	// keeps WAL writes serialized and the pragmas sticky.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schemaSQL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("contextstore migrate: %w", err)
	}
	for _, m := range []struct{ table, col, decl string }{
		{"task_attempts", "evidence_claim", "TEXT NOT NULL DEFAULT '{}'"},
		{"specs", "spec_hash", "TEXT NOT NULL DEFAULT ''"},
		{"specs", "response_contract", "TEXT NOT NULL DEFAULT '{}'"},
		{"specs", "requirements", "TEXT NOT NULL DEFAULT '[]'"},
		{"decisions", "value_kind", "TEXT NOT NULL DEFAULT 'precise'"},
		{"proofs", "detail", "TEXT NOT NULL DEFAULT '{}'"},
		{"deliveries", "runbook", "TEXT NOT NULL DEFAULT '{}'"},
		{"projects", "project_type", "TEXT NOT NULL DEFAULT 'http-service'"},
		{"projects", "scale", "TEXT NOT NULL DEFAULT 'standard'"},
		{"projects", "repo_target", "TEXT NOT NULL DEFAULT ''"},
		// or-045a.4 spec-of-specs: '' = flat project; set = sub-spec child of
		// that parent project (roll-up delivery gate lives in SetStatus).
		{"projects", "parent_project_id", "TEXT NOT NULL DEFAULT ''"},
		{"escalations", "detail", "TEXT NOT NULL DEFAULT ''"},
		{"escalations", "resolution", "TEXT NOT NULL DEFAULT ''"},
		{"escalations", "resolved_at", "TEXT"},
		// or-7et.2 slice 1: the epic snapshots the spec HASH it was decomposed
		// from (spec_id is insufficient — in-place re-ratification keeps the id).
		// '' = pre-migration epic, grandfathered by the staleness guard.
		{"epics", "spec_hash", "TEXT NOT NULL DEFAULT ''"},
		// or-7et.2 slice 2: reconciliation marks tasks whose covered spec surface
		// changed — proof memo reuse is BYPASSED for them (fresh re-proof).
		{"tasks", "reproof_required", "INTEGER NOT NULL DEFAULT 0"},
	} {
		if _, err := ensureColumn(db, m.table, m.col, m.decl); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("contextstore migrate columns: %w", err)
		}
	}
	// or-045a.5: spec_dimensions' dimension CHECK gained 'direction'. SQLite
	// cannot ALTER a CHECK constraint — rebuild the table once for DBs created
	// before the direction dimension existed (detected via the stored DDL).
	if err := ensureDirectionDimension(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("contextstore migrate spec_dimensions: %w", err)
	}
	// projects.status (or-v9f.1): the backfill must run ONLY when the column is
	// first added to a pre-queue DB — the latest project was the implicit work
	// item, so it stays active and every earlier (already-orphaned) project is
	// closed out. Re-running it later would clobber a legitimate queue state
	// where the active project is older than a delivered one.
	added, err := ensureColumn(db, "projects", "status",
		"TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('queued','active','delivered','abandoned'))")
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("contextstore migrate columns: %w", err)
	}
	if added {
		if _, err := db.Exec(`UPDATE projects SET status='abandoned' WHERE id NOT IN
			(SELECT id FROM projects ORDER BY created_at DESC, id DESC LIMIT 1)`); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("contextstore backfill project status: %w", err)
		}
	}
	return &Store{db: db, dir: dir}, nil
}

// ensureColumn adds a column to a table if it does not already exist (additive
// migration for stores created by an earlier schema version), reporting whether
// it added it — one-time backfills key off that. SQLite has no "ADD COLUMN IF
// NOT EXISTS", so we probe PRAGMA table_info first.
func ensureColumn(db *sql.DB, table, column, decl string) (bool, error) {
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return false, err
	}
	found := false
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt any
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			_ = rows.Close()
			return false, err
		}
		if name == column {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return false, err
	}
	_ = rows.Close()
	if found {
		return false, nil
	}
	_, err = db.Exec("ALTER TABLE " + table + " ADD COLUMN " + column + " " + decl)
	return err == nil, err
}

// Close checkpoints the WAL and closes the database.
func (s *Store) Close() error {
	_, _ = s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	return s.db.Close()
}

// Dir returns the data directory backing this store.
func (s *Store) Dir() string { return s.dir }

// WithTx runs fn inside a single transaction: it commits on nil, rolls back on
// any error or panic. This is the shared transaction boundary all repositories
// write through — the atomic unit for crash-safe writes.
func (s *Store) WithTx(ctx context.Context, fn func(*Tx) error) (err error) {
	sqlTx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = sqlTx.Rollback()
		}
	}()
	if err := fn(&Tx{tx: sqlTx}); err != nil {
		return err
	}
	if err := sqlTx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	committed = true
	return nil
}

// view runs fn in a read transaction (rolled back at the end).
func (s *Store) view(ctx context.Context, fn func(*Tx) error) error {
	sqlTx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return fmt.Errorf("begin read tx: %w", err)
	}
	defer func() { _ = sqlTx.Rollback() }()
	return fn(&Tx{tx: sqlTx})
}

// Tx is the transaction-scoped accessor for the per-aggregate repositories. All
// repos returned from one Tx share the same underlying transaction.
type Tx struct{ tx *sql.Tx }

func (t *Tx) Projects() *ProjectRepo                 { return &ProjectRepo{t.tx} }
func (t *Tx) Specs() *SpecRepo                       { return &SpecRepo{t.tx} }
func (t *Tx) SpecDimensions() *SpecDimensionRepo     { return &SpecDimensionRepo{t.tx} }
func (t *Tx) Decisions() *DecisionRepo               { return &DecisionRepo{t.tx} }
func (t *Tx) Epics() *EpicRepo                       { return &EpicRepo{t.tx} }
func (t *Tx) ProofObligations() *ProofObligationRepo { return &ProofObligationRepo{t.tx} }
func (t *Tx) Tasks() *TaskRepo                       { return &TaskRepo{t.tx} }
func (t *Tx) Attempts() *AttemptRepo                 { return &AttemptRepo{t.tx} }
func (t *Tx) Proofs() *ProofRepo                     { return &ProofRepo{t.tx} }
func (t *Tx) Artifacts() *ArtifactRepo               { return &ArtifactRepo{t.tx} }
func (t *Tx) FailureModes() *FailureModeRepo         { return &FailureModeRepo{t.tx} }
func (t *Tx) Worktrees() *WorktreeRepo               { return &WorktreeRepo{t.tx} }
func (t *Tx) PolarisContext() *PolarisContextRepo    { return &PolarisContextRepo{t.tx} }
func (t *Tx) Deliveries() *DeliveryRepo              { return &DeliveryRepo{t.tx} }
func (t *Tx) Escalations() *EscalationRepo           { return &EscalationRepo{t.tx} }
func (t *Tx) Goals() *GoalsRepo                      { return &GoalsRepo{t.tx} }
func (t *Tx) OpenQuestions() *OpenQuestionRepo       { return &OpenQuestionRepo{t.tx} }
func (t *Tx) GoldLabels() *GoldLabelRepo             { return &GoldLabelRepo{t.tx} }
func (t *Tx) RatifiedUCAs() *RatifiedUCARepo         { return &RatifiedUCARepo{t.tx} }

// ── Store-level read helpers (read-model over the repositories) ──────────────

// Project loads a project by id.
func (s *Store) Project(ctx context.Context, id string) (Project, error) {
	var p Project
	err := s.view(ctx, func(tx *Tx) error {
		var e error
		p, e = tx.Projects().Get(ctx, id)
		return e
	})
	return p, err
}

// ListProjects returns all projects (newest first).
func (s *Store) ListProjects(ctx context.Context) ([]Project, error) {
	var out []Project
	err := s.view(ctx, func(tx *Tx) error {
		var e error
		out, e = tx.Projects().List(ctx)
		return e
	})
	return out, err
}

// SpecsForProject returns the specs belonging to a project.
func (s *Store) SpecsForProject(ctx context.Context, projectID string) ([]Spec, error) {
	var out []Spec
	err := s.view(ctx, func(tx *Tx) error {
		var e error
		out, e = tx.Specs().ForProject(ctx, projectID)
		return e
	})
	return out, err
}

// AttemptCount returns how many attempts have been recorded for a task.
func (s *Store) AttemptCount(ctx context.Context, taskID string) (int, error) {
	var n int
	err := s.view(ctx, func(tx *Tx) error {
		var e error
		n, e = tx.Attempts().CountByTask(ctx, taskID)
		return e
	})
	return n, err
}

// ProofCount returns how many proofs have been recorded for a task. Dispatch
// must never create a proof (the EvidenceClaim is not a verdict).
func (s *Store) ProofCount(ctx context.Context, taskID string) (int, error) {
	var n int
	err := s.view(ctx, func(tx *Tx) error {
		var e error
		n, e = tx.Proofs().CountByTask(ctx, taskID)
		return e
	})
	return n, err
}

// ShadowRecord is one shadow-mode ModuleProposer comparison against the oracle
// decomposer (or-809): how the proposer's plan compares to the template's. The
// measured window over these rows drives the eventual cutover decision.
type ShadowRecord struct {
	SpecHash         string
	ProposerModules  int
	OracleModules    int
	ProposerClusters int
	OracleClusters   int
	SupersetOK       bool
	FloorOK          bool
	CoverageGateOK   bool
	Missing          []string
}

// RecordShadowPlan persists a shadow ModuleProposer comparison (or-809).
// Best-effort by convention — callers ignore the error so a shadow write never
// fails a build.
func (s *Store) RecordShadowPlan(ctx context.Context, projectID string, r ShadowRecord) error {
	missing, _ := json.Marshal(r.Missing)
	return s.WithTx(ctx, func(tx *Tx) error {
		_, e := tx.tx.ExecContext(ctx,
			`INSERT INTO shadow_plans (id, project_id, spec_hash, proposer_modules, oracle_modules,
			 proposer_clusters, oracle_clusters, superset_ok, floor_ok, coverage_gate_ok, missing, created_at)
			 VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
			newID(), projectID, r.SpecHash, r.ProposerModules, r.OracleModules,
			r.ProposerClusters, r.OracleClusters, b2i(r.SupersetOK), b2i(r.FloorOK), b2i(r.CoverageGateOK),
			string(missing), nowRFC3339())
		return e
	})
}

// ShadowPlans returns the recorded shadow comparisons for a project, newest
// first — the measured window the cutover criterion reads (or-809).
func (s *Store) ShadowPlans(ctx context.Context, projectID string) ([]ShadowRecord, error) {
	var out []ShadowRecord
	err := s.view(ctx, func(tx *Tx) error {
		rows, e := tx.tx.QueryContext(ctx,
			`SELECT spec_hash, proposer_modules, oracle_modules, proposer_clusters, oracle_clusters,
			 superset_ok, floor_ok, coverage_gate_ok, missing FROM shadow_plans
			 WHERE project_id=? ORDER BY created_at DESC, id DESC`, projectID)
		if e != nil {
			return e
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var r ShadowRecord
			var sup, fl, cov int
			var missing string
			if e := rows.Scan(&r.SpecHash, &r.ProposerModules, &r.OracleModules, &r.ProposerClusters,
				&r.OracleClusters, &sup, &fl, &cov, &missing); e != nil {
				return e
			}
			r.SupersetOK, r.FloorOK, r.CoverageGateOK = sup != 0, fl != 0, cov != 0
			_ = json.Unmarshal([]byte(missing), &r.Missing)
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, err
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ProofMemoGet returns the persisted post-enforcement proof Report JSON for an
// (artifact, spec) pair, or ok=false when none is memoized (or-v9f.6).
func (s *Store) ProofMemoGet(ctx context.Context, specHash, contentHash string) (reportJSON string, ok bool, err error) {
	err = s.view(ctx, func(tx *Tx) error {
		e := tx.tx.QueryRowContext(ctx,
			`SELECT report_json FROM proof_memo WHERE spec_hash=? AND content_hash=?`,
			specHash, contentHash).Scan(&reportJSON)
		if errors.Is(e, sql.ErrNoRows) {
			return nil
		}
		if e == nil {
			ok = true
		}
		return e
	})
	return reportJSON, ok, err
}

// ProofMemoPut records a proof Report for an (artifact, spec) pair so a later run
// with the identical bytes skips the expensive proof (or-v9f.6). Idempotent
// upsert — a re-proof of the same pair simply refreshes the stored report.
func (s *Store) ProofMemoPut(ctx context.Context, specHash, contentHash, reportJSON string) error {
	return s.WithTx(ctx, func(tx *Tx) error {
		_, e := tx.tx.ExecContext(ctx,
			`INSERT INTO proof_memo (spec_hash, content_hash, report_json, created_at) VALUES (?,?,?,?)
			 ON CONFLICT(spec_hash, content_hash) DO UPDATE SET report_json=excluded.report_json, created_at=excluded.created_at`,
			specHash, contentHash, reportJSON, nowRFC3339())
		return e
	})
}

// CopyProofMemos re-keys every proof memo from one spec anchor to another
// (or-7et.2 slice 2): after a spec amendment, byte-identical artifacts keep
// their proven verdicts under the NEW hash. Tasks whose covered surface
// changed bypass the memo entirely via tasks.reproof_required, so this copy
// can never launder a stale verdict onto an affected task.
func (s *Store) CopyProofMemos(ctx context.Context, fromSpecHash, toSpecHash string) error {
	return s.WithTx(ctx, func(tx *Tx) error {
		_, e := tx.tx.ExecContext(ctx,
			`INSERT INTO proof_memo (spec_hash, content_hash, report_json, created_at)
			 SELECT ?, content_hash, report_json, ? FROM proof_memo WHERE spec_hash=?
			 ON CONFLICT(spec_hash, content_hash) DO NOTHING`,
			toSpecHash, nowRFC3339(), fromSpecHash)
		return e
	})
}

// CurrentProjectSpec returns the ACTIVE project and its latest spec — the single
// in-flight work item. Creation order no longer decides it (or-v9f.1): a newer
// queued intent waits its turn rather than silently orphaning the one in flight.
// Returns ErrNotFound if nothing has been submitted (or the queue is drained).
func (s *Store) CurrentProjectSpec(ctx context.Context) (Project, Spec, error) {
	var p Project
	var sp Spec
	err := s.view(ctx, func(tx *Tx) error {
		var e error
		p, e = tx.Projects().Active(ctx)
		if e != nil {
			return e
		}
		sp, e = tx.Specs().LatestForProject(ctx, p.ID)
		return e
	})
	return p, sp, err
}

// LastDeliveredProjectSpec resolves the most recently DELIVERED project and its
// latest spec. A delivered project has left the active slot (or-v9f.1) so
// CurrentProjectSpec no longer sees it, but its proven code is still on disk —
// this lets read/report paths (e.g. `show_code`) answer "where is the code" after
// delivery. Returns ErrNotFound when nothing has been delivered.
func (s *Store) LastDeliveredProjectSpec(ctx context.Context) (Project, Spec, error) {
	var p Project
	var sp Spec
	err := s.view(ctx, func(tx *Tx) error {
		var e error
		p, e = tx.Projects().LatestByStatus(ctx, "delivered")
		if e != nil {
			return e
		}
		sp, e = tx.Specs().LatestForProject(ctx, p.ID)
		return e
	})
	return p, sp, err
}

// CurrentOrLastDeliveredProjectSpec resolves the project the developer is currently
// looking at for READ/REPORT paths: the active project+spec if one is in flight,
// otherwise the most recently delivered one. After Accept, delivery moves a project
// out of the active slot (or-v9f.1), so a strict CurrentProjectSpec would report "no
// current project" for the very code just built. Mutation/lifecycle paths keep using
// CurrentProjectSpec (strict active) — only read paths (plan show, deliver show,
// show_code) fall back here. Returns ErrNotFound when nothing is active or delivered.
func (s *Store) CurrentOrLastDeliveredProjectSpec(ctx context.Context) (Project, Spec, error) {
	var p Project
	var sp Spec
	err := s.view(ctx, func(tx *Tx) error {
		p2, e := tx.Projects().Active(ctx)
		if errors.Is(e, ErrNotFound) {
			p2, e = tx.Projects().LatestByStatus(ctx, "delivered")
		}
		if e != nil {
			return e
		}
		p = p2
		sp, e = tx.Specs().LatestForProject(ctx, p.ID)
		return e
	})
	return p, sp, err
}

// QueuedProjects returns the intent queue in FIFO order.
func (s *Store) QueuedProjects(ctx context.Context) ([]Project, error) {
	var out []Project
	err := s.view(ctx, func(tx *Tx) error {
		var e error
		out, e = tx.Projects().ListByStatus(ctx, "queued")
		return e
	})
	return out, err
}

// ActivateNextQueued promotes the FIFO head of the intent queue to active. It is
// a no-op returning (current, false) while an active project exists — the single-
// active invariant is enforced here, not trusted to callers. With an empty queue
// and no active project it returns ErrNotFound.
func (s *Store) ActivateNextQueued(ctx context.Context) (Project, bool, error) {
	var p Project
	promoted := false
	err := s.WithTx(ctx, func(tx *Tx) error {
		active, e := tx.Projects().Active(ctx)
		if e == nil {
			p = active
			return nil
		}
		if !errors.Is(e, ErrNotFound) {
			return e
		}
		next, e := tx.Projects().OldestQueued(ctx)
		if e != nil {
			return e
		}
		if e := tx.Projects().SetStatus(ctx, next.ID, "active"); e != nil {
			return e
		}
		next.Status = "active"
		p, promoted = next, true
		return nil
	})
	return p, promoted, err
}

// DecisionsForSpec returns the latest answer per key for a spec.
func (s *Store) DecisionsForSpec(ctx context.Context, specID string) ([]Decision, error) {
	var out []Decision
	err := s.view(ctx, func(tx *Tx) error {
		var e error
		out, e = tx.Decisions().ListForSpec(ctx, specID)
		return e
	})
	return out, err
}

// FactBundle is the durable, anchor-verified context the Context Store can
// reconstruct for a task — the FACTS (spec, decisions, prior attempts, ancestor
// artifacts) an agent needs to resume after a crash/restart. The context-engine
// later budgets and adds cognition on top; this layer never invents anything.
type FactBundle struct {
	Task      Task
	Project   Project
	Spec      Spec
	Decisions []Decision
	Attempts  []Attempt
	Artifacts []Artifact
}

// Recall rebuilds the FactBundle for a task from the durable store. It is the
// mechanism that makes agents resumable: a restarted agent calls Recall and
// continues without re-asking the developer (Story 31 / acceptance resumability).
func (s *Store) Recall(ctx context.Context, taskID string) (FactBundle, error) {
	var fb FactBundle
	err := s.view(ctx, func(tx *Tx) error {
		task, err := tx.Tasks().Get(ctx, taskID)
		if err != nil {
			return err
		}
		epic, err := tx.Epics().Get(ctx, task.EpicID)
		if err != nil {
			return err
		}
		project, err := tx.Projects().Get(ctx, epic.ProjectID)
		if err != nil {
			return err
		}
		spec, err := tx.Specs().Get(ctx, epic.SpecID)
		if err != nil {
			return err
		}
		decisions, err := tx.Decisions().ListForSpec(ctx, spec.ID)
		if err != nil {
			return err
		}
		attempts, err := tx.Attempts().List(ctx, taskID)
		if err != nil {
			return err
		}
		artifacts, err := tx.Artifacts().ListByTask(ctx, taskID)
		if err != nil {
			return err
		}
		fb = FactBundle{Task: task, Project: project, Spec: spec, Decisions: decisions, Attempts: attempts, Artifacts: artifacts}
		return nil
	})
	return fb, err
}

// ProofByTaskMode returns the latest proof for a task in a given mode.
func (s *Store) ProofByTaskMode(ctx context.Context, taskID, mode string) (Proof, error) {
	var p Proof
	err := s.view(ctx, func(tx *Tx) error {
		var e error
		p, e = tx.Proofs().GetByTaskMode(ctx, taskID, mode)
		return e
	})
	return p, err
}

// Task loads a task by id.
func (s *Store) Task(ctx context.Context, id string) (Task, error) {
	var t Task
	err := s.view(ctx, func(tx *Tx) error {
		var e error
		t, e = tx.Tasks().Get(ctx, id)
		return e
	})
	return t, err
}

// ── helpers ─────────────────────────────────────────────────────────────────

// newID returns a random 128-bit hex identifier.
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failure is catastrophic; fall back to time so we never emit
		// an empty id (callers treat ids as non-empty).
		return fmt.Sprintf("ts-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// CanonicalKey is the deterministic dedup key for a failure mode — a hash over
// {category, component_type, symptom_class} (PRD Story 30).
func CanonicalKey(category, componentType, symptomClass string) string {
	h := sha256.Sum256([]byte(category + "\x00" + componentType + "\x00" + symptomClass))
	return hex.EncodeToString(h[:])
}

func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// TrackRecord is the earned-autonomy ladder's evidence (or-v9f.30, the
// minimal or-lrr slice): the count of CONSECUTIVE deliveries for a project
// since its last reset event. Any escalation row resets the ladder — and the
// paths that matter all FILE one (a failed task, a bar refusal, a red-button
// block each escalate), so "deliveries after the newest escalation" is the
// whole reset semantic: autonomy is re-earned, never grandfathered.
func (s *Store) TrackRecord(ctx context.Context, projectID string) (int, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM deliveries dl
		 JOIN epics e ON dl.epic_id = e.id
		 WHERE e.project_id = ?
		   AND dl.created_at > COALESCE(
		       (SELECT MAX(created_at) FROM escalations WHERE project_id = ?), '')`,
		projectID, projectID)
	var n int
	err := row.Scan(&n)
	return n, err
}

// ListGoldLabels returns a project's captured ratification labels, newest
// first — the read surface for future SkillEval/longitudinal consumers.
func (s *Store) ListGoldLabels(ctx context.Context, projectID string) ([]GoldLabel, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, ratification_kind, outcome, spec_id, artifact_hash, model_id, producer_version, created_at
		 FROM gold_labels WHERE project_id=? ORDER BY created_at DESC, id DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []GoldLabel
	for rows.Next() {
		var g GoldLabel
		if err := rows.Scan(&g.ID, &g.ProjectID, &g.RatificationKind, &g.Outcome, &g.SpecID, &g.ArtifactHash, &g.ModelID, &g.ProducerVersion, &g.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// ensureDirectionDimension rebuilds spec_dimensions when its dimension CHECK
// predates the 'direction' vocabulary (or-045a.5): SQLite cannot alter a CHECK,
// so the one-time migration recreates the table with the extended constraint
// and copies every row. Idempotent — a DB whose DDL already names 'direction'
// is untouched.
func ensureDirectionDimension(db *sql.DB) error {
	var ddl string
	err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='spec_dimensions'`).Scan(&ddl)
	if err != nil {
		return err // the table always exists (schemaSQL ran first)
	}
	if strings.Contains(ddl, "'direction'") {
		return nil
	}
	_, err = db.Exec(`
		CREATE TABLE spec_dimensions_new (
		    id               TEXT PRIMARY KEY,
		    spec_id          TEXT NOT NULL REFERENCES specs(id) ON DELETE CASCADE,
		    dimension        TEXT NOT NULL CHECK (dimension IN
		                       ('functional','scale','observability','oncall','data','slo','security','dependencies','direction')),
		    value_structured TEXT NOT NULL DEFAULT '{}',
		    value_kind       TEXT NOT NULL CHECK (value_kind IN ('precise','fallback_preset','unresolved')),
		    tier_required    INTEGER NOT NULL DEFAULT 0,
		    resolved_at      TEXT,
		    UNIQUE (spec_id, dimension)
		);
		INSERT INTO spec_dimensions_new SELECT * FROM spec_dimensions;
		DROP TABLE spec_dimensions;
		ALTER TABLE spec_dimensions_new RENAME TO spec_dimensions;`)
	return err
}
