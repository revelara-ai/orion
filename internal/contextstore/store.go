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
	return &Store{db: db, dir: dir}, nil
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

func (t *Tx) Projects() *ProjectRepo         { return &ProjectRepo{t.tx} }
func (t *Tx) Specs() *SpecRepo               { return &SpecRepo{t.tx} }
func (t *Tx) Epics() *EpicRepo               { return &EpicRepo{t.tx} }
func (t *Tx) Tasks() *TaskRepo               { return &TaskRepo{t.tx} }
func (t *Tx) Attempts() *AttemptRepo         { return &AttemptRepo{t.tx} }
func (t *Tx) Proofs() *ProofRepo             { return &ProofRepo{t.tx} }
func (t *Tx) Artifacts() *ArtifactRepo       { return &ArtifactRepo{t.tx} }
func (t *Tx) FailureModes() *FailureModeRepo { return &FailureModeRepo{t.tx} }

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
