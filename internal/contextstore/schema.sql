-- Orion Context Store schema (or-xgj). The durable source of truth.
-- SQLite (WAL). Tracker (beads/GitHub) is a one-way projection, never truth.

CREATE TABLE IF NOT EXISTS projects (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    intent     TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS specs (
    id             TEXT PRIMARY KEY,
    project_id     TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    status         TEXT NOT NULL CHECK (status IN ('drafting','accepted','revised')),
    version        INTEGER NOT NULL DEFAULT 1,
    parent_spec_id TEXT REFERENCES specs(id),
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_specs_project ON specs(project_id);

-- Typed spec dimensions: one row per dimension per spec version.
CREATE TABLE IF NOT EXISTS spec_dimensions (
    id               TEXT PRIMARY KEY,
    spec_id          TEXT NOT NULL REFERENCES specs(id) ON DELETE CASCADE,
    dimension        TEXT NOT NULL CHECK (dimension IN
                       ('functional','scale','observability','oncall','data','slo','security','dependencies')),
    value_structured TEXT NOT NULL DEFAULT '{}',
    value_kind       TEXT NOT NULL CHECK (value_kind IN ('precise','fallback_preset','unresolved')),
    tier_required    INTEGER NOT NULL DEFAULT 0,
    resolved_at      TEXT,
    UNIQUE (spec_id, dimension)
);

CREATE TABLE IF NOT EXISTS decisions (
    id                TEXT PRIMARY KEY,
    project_id        TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    spec_id           TEXT REFERENCES specs(id),
    key               TEXT NOT NULL,
    value             TEXT NOT NULL DEFAULT '',
    security_relevant INTEGER NOT NULL DEFAULT 0,
    created_at        TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_decisions_project ON decisions(project_id);

CREATE TABLE IF NOT EXISTS epics (
    id               TEXT PRIMARY KEY,
    project_id       TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    spec_id          TEXT NOT NULL REFERENCES specs(id),
    title            TEXT NOT NULL,
    plan_approved_at TEXT,
    created_at       TEXT NOT NULL
);

-- proofs is declared before tasks is constrained by it (tasks.proof_id FK).
CREATE TABLE IF NOT EXISTS proofs (
    id                     TEXT PRIMARY KEY,
    task_id                TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    mode                   TEXT NOT NULL CHECK (mode IN ('behavioral','empirical','hazard')),
    verdict                TEXT NOT NULL CHECK (verdict IN ('Accept','Reject','Inconclusive')),
    mutation_score         REAL NOT NULL DEFAULT 0,
    empirical_pass_rate    REAL NOT NULL DEFAULT 0,
    hazard_controlled_count INTEGER NOT NULL DEFAULT 0,
    hazard_total_count     INTEGER NOT NULL DEFAULT 0,
    run_count              INTEGER NOT NULL DEFAULT 0,
    created_at             TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_proofs_task ON proofs(task_id);

CREATE TABLE IF NOT EXISTS tasks (
    id         TEXT PRIMARY KEY,
    epic_id    TEXT NOT NULL REFERENCES epics(id) ON DELETE CASCADE,
    title      TEXT NOT NULL,
    status     TEXT NOT NULL DEFAULT 'ready' CHECK (status IN
                 ('ready','in_progress','being_validated','proven','integrated','done')),
    file_scope TEXT NOT NULL DEFAULT '',
    proof_id   TEXT REFERENCES proofs(id),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_tasks_epic_status ON tasks(epic_id, status);

CREATE TABLE IF NOT EXISTS task_deps (
    task_id    TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    depends_on TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    PRIMARY KEY (task_id, depends_on)
);

CREATE TABLE IF NOT EXISTS task_attempts (
    id              TEXT PRIMARY KEY,
    task_id         TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    idempotency_key TEXT NOT NULL,
    evidence_claim  TEXT NOT NULL DEFAULT '{}',
    created_at      TEXT NOT NULL,
    UNIQUE (task_id, idempotency_key)
);

CREATE TABLE IF NOT EXISTS proof_obligations (
    id         TEXT PRIMARY KEY,
    task_id    TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    clause     TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS deliveries (
    id                 TEXT PRIMARY KEY,
    epic_id            TEXT NOT NULL REFERENCES epics(id) ON DELETE CASCADE,
    operating_envelope TEXT NOT NULL DEFAULT '{}',
    created_at         TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS escalations (
    id         TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    task_id    TEXT REFERENCES tasks(id),
    reason     TEXT NOT NULL,
    resolved   INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS failure_modes (
    id             TEXT PRIMARY KEY,
    project_id     TEXT,
    category       TEXT NOT NULL,
    component_type TEXT NOT NULL,
    symptom_class  TEXT NOT NULL,
    canonical_key  TEXT NOT NULL UNIQUE,
    created_at     TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS artifacts (
    id            TEXT PRIMARY KEY,
    task_id       TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    artifact_type TEXT NOT NULL,
    storage_path  TEXT NOT NULL,
    content_hash  TEXT NOT NULL,
    created_at    TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_artifacts_task ON artifacts(task_id);

CREATE TABLE IF NOT EXISTS polaris_context (
    id          TEXT PRIMARY KEY,
    project_id  TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    kind        TEXT NOT NULL,
    payload     TEXT NOT NULL DEFAULT '{}',
    fetched_at  TEXT NOT NULL,
    ttl_seconds INTEGER NOT NULL DEFAULT 0
);

-- One worktree per task, keyed by the beads issue id (its unique name). The
-- in-use set is reconciled from the filesystem (worktree.Reconcile); this record
-- makes a crash mid-create/mid-remove recoverable.
CREATE TABLE IF NOT EXISTS worktrees (
    issue_id   TEXT PRIMARY KEY,
    path       TEXT NOT NULL,
    branch     TEXT NOT NULL,
    status     TEXT NOT NULL DEFAULT 'active',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

-- The done-gate as a DB constraint (PRD: Core Data Model Hardening). A task may
-- only enter 'proven' or 'done' when it carries a proof_id whose verdict=Accept.
CREATE TRIGGER IF NOT EXISTS trg_tasks_done_gate_insert
BEFORE INSERT ON tasks
FOR EACH ROW
WHEN NEW.status IN ('proven','done')
  AND (NEW.proof_id IS NULL
       OR NOT EXISTS (SELECT 1 FROM proofs p WHERE p.id = NEW.proof_id AND p.verdict = 'Accept'))
BEGIN
    SELECT RAISE(ABORT, 'done-gate: task requires proof_id with verdict=Accept');
END;

CREATE TRIGGER IF NOT EXISTS trg_tasks_done_gate_update
BEFORE UPDATE ON tasks
FOR EACH ROW
WHEN NEW.status IN ('proven','done')
  AND (NEW.proof_id IS NULL
       OR NOT EXISTS (SELECT 1 FROM proofs p WHERE p.id = NEW.proof_id AND p.verdict = 'Accept'))
BEGIN
    SELECT RAISE(ABORT, 'done-gate: task requires proof_id with verdict=Accept');
END;
