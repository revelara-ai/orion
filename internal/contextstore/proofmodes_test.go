package contextstore

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// legacyProofsSchema is a pre-migration DB: the proofs.mode CHECK predates the
// 'diagnostics'/'new_behavior' modes AND has no `detail` column (added later by
// ALTER, so its ordinal ends up last — the migration must name columns).
const legacyProofsSchema = `
CREATE TABLE projects (id TEXT PRIMARY KEY, name TEXT NOT NULL, intent TEXT NOT NULL,
    project_type TEXT NOT NULL DEFAULT 'http-service', created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE specs (id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES projects(id),
    status TEXT NOT NULL, version INTEGER NOT NULL DEFAULT 1, created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE epics (id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES projects(id),
    spec_id TEXT NOT NULL REFERENCES specs(id), title TEXT NOT NULL, created_at TEXT NOT NULL);
CREATE TABLE proofs (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    mode TEXT NOT NULL CHECK (mode IN ('behavioral','empirical','hazard','converged')),
    verdict TEXT NOT NULL CHECK (verdict IN ('Accept','Reject','Inconclusive')),
    mutation_score REAL NOT NULL DEFAULT 0, empirical_pass_rate REAL NOT NULL DEFAULT 0,
    hazard_controlled_count INTEGER NOT NULL DEFAULT 0, hazard_total_count INTEGER NOT NULL DEFAULT 0,
    run_count INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL);
CREATE TABLE tasks (id TEXT PRIMARY KEY, epic_id TEXT NOT NULL REFERENCES epics(id),
    title TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'ready', file_scope TEXT NOT NULL DEFAULT '',
    proof_id TEXT REFERENCES proofs(id), created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
INSERT INTO projects VALUES ('p1','demo','build','http-service','t','t');
INSERT INTO specs VALUES ('s1','p1','accepted',1,'t','t');
INSERT INTO epics VALUES ('e1','p1','s1','epic','t');
INSERT INTO tasks VALUES ('tk1','e1','impl','proven','cmd/','pf1','t','t');
INSERT INTO proofs (id,task_id,mode,verdict,run_count,created_at) VALUES ('pf1','tk1','behavioral','Accept',1,'t');
`

func openRawLegacy(t *testing.T, dir string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, DBFile))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(legacyProofsSchema); err != nil {
		t.Fatalf("seed legacy schema: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestProofModesMigrationOnLegacyDB (or-r4v6): opening a DB whose proofs.mode
// CHECK predates 'diagnostics'/'new_behavior' rebuilds the table with the
// extended constraint — a diagnostics proof then inserts, existing rows and the
// task→proof FK survive, and re-opening is a no-op. This is the migration that
// makes deleting the database (the dogfood misfix) unnecessary.
func TestProofModesMigrationOnLegacyDB(t *testing.T) {
	dir := t.TempDir()
	openRawLegacy(t, dir)

	st, err := Open(dir) // runs the migrations, including the proofs rebuild
	if err != nil {
		t.Fatalf("open (migrate): %v", err)
	}
	raw := st.db

	// The CHECK now admits the new modes.
	var ddl string
	if err := raw.QueryRow(`SELECT sql FROM sqlite_master WHERE name='proofs'`).Scan(&ddl); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ddl, "'diagnostics'") || !strings.Contains(ddl, "'new_behavior'") {
		t.Fatalf("proofs.mode must admit diagnostics/new_behavior after migration:\n%s", ddl)
	}

	// A diagnostics proof — the exact write that crashed in the dogfood — inserts.
	if _, err := raw.Exec(`INSERT INTO proofs (id,task_id,mode,verdict,run_count,detail,created_at) VALUES ('pf2','tk1','diagnostics','Inconclusive',1,'{}','t')`); err != nil {
		t.Fatalf("a diagnostics proof must insert after the migration: %v", err)
	}

	// The pre-existing proof row survived, and the task still points at it.
	var mode, proofID string
	if err := raw.QueryRow(`SELECT mode FROM proofs WHERE id='pf1'`).Scan(&mode); err != nil || mode != "behavioral" {
		t.Fatalf("the legacy proof row must survive: mode=%q err=%v", mode, err)
	}
	if err := raw.QueryRow(`SELECT proof_id FROM tasks WHERE id='tk1'`).Scan(&proofID); err != nil || proofID != "pf1" {
		t.Fatalf("the task→proof FK must survive the rebuild: proof_id=%q err=%v", proofID, err)
	}
	// FK enforcement is back on after the rebuild.
	var fk int
	if err := raw.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("foreign_keys must be re-enabled after the migration: %d err=%v", fk, err)
	}
	_ = st.Close()

	// Idempotent: re-opening the migrated DB changes nothing and still admits diagnostics.
	st2, err := Open(dir)
	if err != nil {
		t.Fatalf("re-open: %v", err)
	}
	defer func() { _ = st2.Close() }()
	if _, err := st2.db.Exec(`INSERT INTO proofs (id,task_id,mode,verdict,run_count,detail,created_at) VALUES ('pf3','tk1','new_behavior','Accept',1,'{}','t')`); err != nil {
		t.Fatalf("re-open must remain migrated (new_behavior inserts): %v", err)
	}
	var n int
	if err := st2.db.QueryRow(`SELECT count(*) FROM proofs`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("all proof rows must be intact after re-open: n=%d err=%v", n, err)
	}
}
