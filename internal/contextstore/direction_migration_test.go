package contextstore

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// TestDirectionDimensionMigration (or-045a.5): a DB created BEFORE the
// 'direction' dimension existed (old CHECK vocabulary) is rebuilt on Open —
// a direction row is accepted afterwards and pre-existing rows survive.
func TestDirectionDimensionMigration(t *testing.T) {
	dir := t.TempDir()
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(dir, DBFile))
	if err != nil {
		t.Fatal(err)
	}
	// The pre-direction shape: the old 8-dimension CHECK.
	if _, err := raw.Exec(`
CREATE TABLE projects (id TEXT PRIMARY KEY, name TEXT NOT NULL, intent TEXT NOT NULL, project_type TEXT NOT NULL DEFAULT 'http-service', status TEXT NOT NULL DEFAULT 'active', created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE specs (id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES projects(id), status TEXT NOT NULL, version INTEGER NOT NULL DEFAULT 1, parent_spec_id TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE spec_dimensions (
    id TEXT PRIMARY KEY,
    spec_id TEXT NOT NULL REFERENCES specs(id) ON DELETE CASCADE,
    dimension TEXT NOT NULL CHECK (dimension IN ('functional','scale','observability','oncall','data','slo','security','dependencies')),
    value_structured TEXT NOT NULL DEFAULT '{}',
    value_kind TEXT NOT NULL CHECK (value_kind IN ('precise','fallback_preset','unresolved')),
    tier_required INTEGER NOT NULL DEFAULT 0,
    resolved_at TEXT,
    UNIQUE (spec_id, dimension)
);
INSERT INTO projects VALUES ('p1','n','i','http-service','active','2026','2026');
INSERT INTO specs VALUES ('s1','p1','drafting',1,NULL,'2026','2026');
INSERT INTO spec_dimensions VALUES ('d1','s1','functional','{}','precise',0,NULL);`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := Open(dir) // the rebuild migration runs here
	if err != nil {
		t.Fatalf("open pre-direction DB: %v", err)
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	// A direction row is now accepted (the old CHECK would refuse it)…
	if err := st.WithTx(ctx, func(tx *Tx) error {
		return tx.SpecDimensions().Upsert(ctx, "s1", "direction", `{"wire_protocol":"grpc"}`, "precise", false)
	}); err != nil {
		t.Fatalf("direction row must be accepted after migration: %v", err)
	}
	// …and the pre-existing row survived the rebuild.
	var kept int
	if err := st.WithTx(ctx, func(tx *Tx) error {
		return tx.tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM spec_dimensions WHERE dimension='functional' AND id='d1'`).Scan(&kept)
	}); err != nil {
		t.Fatal(err)
	}
	if kept != 1 {
		t.Fatalf("the rebuild must preserve existing rows, kept=%d", kept)
	}
}
