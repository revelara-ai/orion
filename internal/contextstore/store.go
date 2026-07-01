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
	"errors"
	"fmt"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
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
func Open(dir string) (*Store, error) {
	path := filepath.Join(dir, DBFile)
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"
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
		{"escalations", "detail", "TEXT NOT NULL DEFAULT ''"},
		{"escalations", "resolution", "TEXT NOT NULL DEFAULT ''"},
		{"escalations", "resolved_at", "TEXT"},
	} {
		if _, err := ensureColumn(db, m.table, m.col, m.decl); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("contextstore migrate columns: %w", err)
		}
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
