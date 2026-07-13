-- Orion Context Store schema (or-xgj). The durable source of truth.
-- SQLite (WAL). Tracker (beads/GitHub) is a one-way projection, never truth.

CREATE TABLE IF NOT EXISTS projects (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL,
    intent       TEXT NOT NULL,
    project_type TEXT NOT NULL DEFAULT 'http-service',
    -- single-active queue lifecycle (or-v9f.1): at most one 'active' project;
    -- 'queued' intents wait FIFO; 'delivered'/'abandoned' are terminal.
    status       TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('queued','active','delivered','abandoned')),
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL
);
-- parent_project_id (spec-of-specs, or-045a.4) is an ADDITIVE column applied
-- by the migration list in store.go Open, like scale and repo_target.

-- Ratified project goals (or-045a.2): the goal-altitude artifact a large
-- greenfield intake ratifies BEFORE the spec — goals/non-goals/success
-- criteria as canonical JSON, content-hashed like a ratified spec. One row
-- per project (the latest proposal; ratification anchors it).
CREATE TABLE IF NOT EXISTS goals (
    project_id TEXT PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
    content    TEXT NOT NULL,
    hash       TEXT NOT NULL DEFAULT '',
    status     TEXT NOT NULL CHECK (status IN ('drafting','ratified')),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

-- Open-question ledger (or-045a.6): unresolved ambiguities at ANY intake
-- altitude (goals/stpa/direction/spec) persist as first-class questions —
-- deferring one no longer lets it vanish. Resolution is only ever an explicit
-- answer or an approved assumption; blocking questions gate ratification.
CREATE TABLE IF NOT EXISTS open_questions (
    id         TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    phase      TEXT NOT NULL CHECK (phase IN ('goals','stpa','direction','spec')),
    origin     TEXT NOT NULL,
    key        TEXT NOT NULL DEFAULT '',
    question   TEXT NOT NULL,
    severity   TEXT NOT NULL CHECK (severity IN ('blocking','advisory')),
    status     TEXT NOT NULL CHECK (status IN ('open','answered','assumed')),
    resolution TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_open_questions_project ON open_questions(project_id, status);

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
                       ('functional','scale','observability','oncall','data','slo','security','dependencies','direction')),
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
    mode                   TEXT NOT NULL CHECK (mode IN ('behavioral','empirical','hazard','converged')),
    verdict                TEXT NOT NULL CHECK (verdict IN ('Accept','Reject','Inconclusive')),
    mutation_score         REAL NOT NULL DEFAULT 0,
    empirical_pass_rate    REAL NOT NULL DEFAULT 0,
    hazard_controlled_count INTEGER NOT NULL DEFAULT 0,
    hazard_total_count     INTEGER NOT NULL DEFAULT 0,
    run_count              INTEGER NOT NULL DEFAULT 0,
    detail                 TEXT NOT NULL DEFAULT '{}',
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
    id          TEXT PRIMARY KEY,
    project_id  TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    task_id     TEXT REFERENCES tasks(id),
    reason      TEXT NOT NULL,
    -- the inbox payload (or-v9f.4): what the human needs to decide — e.g. the
    -- failing task's causal analysis — plus how the decision was closed out.
    detail      TEXT NOT NULL DEFAULT '',
    resolution  TEXT NOT NULL DEFAULT '',
    resolved_at TEXT,
    resolved    INTEGER NOT NULL DEFAULT 0,
    created_at  TEXT NOT NULL
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

-- proof_memo (or-v9f.6): a cross-run, content-addressed proof cache. The proof
-- verdict is a deterministic function of (artifact bytes, contract, model);
-- spec_hash anchors the contract+model, content_hash the artifact, so an
-- identical (spec, artifact) reuses its full post-enforcement Report instead of
-- re-running the expensive behavioral+empirical+hazard proof. Re-running after
-- fixing an escalation thus skips proof for every cluster whose bytes are
-- unchanged — the practical mid-run resume for the synchronous run model. Not
-- FK'd to any run: it is a pure memo keyed by content, valid across runs.
CREATE TABLE IF NOT EXISTS proof_memo (
    spec_hash    TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    report_json  TEXT NOT NULL,
    created_at   TEXT NOT NULL,
    PRIMARY KEY (spec_hash, content_hash)
);

-- shadow_plans (or-809): the ModuleProposer runs in SHADOW alongside the oracle
-- decomposer; each shadow run records how the proposer's plan compares to the
-- template's (coverage superset, floor coverage, cluster-count non-regression).
-- The measured window over these rows is the cutover criterion; the proposer
-- affects nothing while they are collected.
CREATE TABLE IF NOT EXISTS shadow_plans (
    id                TEXT PRIMARY KEY,
    project_id        TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    spec_hash         TEXT NOT NULL,
    proposer_modules  INTEGER NOT NULL DEFAULT 0,
    oracle_modules    INTEGER NOT NULL DEFAULT 0,
    proposer_clusters INTEGER NOT NULL DEFAULT 0,
    oracle_clusters   INTEGER NOT NULL DEFAULT 0,
    superset_ok       INTEGER NOT NULL DEFAULT 0,
    floor_ok          INTEGER NOT NULL DEFAULT 0,
    coverage_gate_ok  INTEGER NOT NULL DEFAULT 0,
    missing           TEXT NOT NULL DEFAULT '[]',
    created_at        TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_shadow_plans_project ON shadow_plans(project_id);

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

-- Run/phase survivability (or-v9f.16): every build-phase event persists as the
-- run progresses, so a dying terminal loses NOTHING — attach is a store-tail
-- reader, resume tooling reads the last persisted phase per cluster.
CREATE TABLE IF NOT EXISTS run_events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id TEXT NOT NULL,
    run_id     TEXT NOT NULL,
    task_id    TEXT NOT NULL DEFAULT '',
    phase      TEXT NOT NULL,
    status     TEXT NOT NULL,
    detail     TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_run_events_run ON run_events(run_id, id);
CREATE INDEX IF NOT EXISTS idx_run_events_project ON run_events(project_id, id);

-- Spend ledger (or-v9f.28): persistent cumulative cost attribution. The
-- accountant seeds from this on run start, so budget ceilings evaluate
-- PROJECT spend across restarts, and status shows real numbers.
CREATE TABLE IF NOT EXISTS spend_ledger (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id  TEXT NOT NULL,
    run_id      TEXT NOT NULL DEFAULT '',
    role        TEXT NOT NULL,
    model_ref   TEXT NOT NULL DEFAULT '',
    tokens      INTEGER NOT NULL DEFAULT 0,
    dollars     REAL NOT NULL DEFAULT 0,
    recorded_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_spend_project ON spend_ledger(project_id);

-- Gold labels (or-gb1.8): every developer ratification captured as a Gold-tier
-- eval record at ZERO extra human effort — outcome + producer provenance
-- (which model/skill/checklist produced the artifact the human graded).
-- Capture only; consumers (SkillEval, autonomy ladder, longitudinal evals)
-- stay in or-lrr. Trust: human-provenance ground truth — control-domain
-- records, never proof inputs.
CREATE TABLE IF NOT EXISTS gold_labels (
    id                TEXT PRIMARY KEY,
    project_id        TEXT NOT NULL,
    ratification_kind TEXT NOT NULL, -- spec_ratification | assumption | escalation
    outcome           TEXT NOT NULL, -- accept | amend | reject | resolved
    spec_id           TEXT NOT NULL DEFAULT '',
    artifact_hash     TEXT NOT NULL DEFAULT '',
    model_id          TEXT NOT NULL DEFAULT 'unknown',
    producer_version  TEXT NOT NULL DEFAULT 'unknown',
    created_at        TEXT NOT NULL
);

-- Ratified STAMP baseline (or-06lr): the developer-confirmed UCAs a
-- brownfield change must PRESERVE. code_tokens (JSON array) are the concrete
-- code markers proving the control is present; the change-proof hazard gate
-- checks a CONTROLLED UCA's tokens survive the change. Disposition:
-- controlled | accepted-gap (only controlled gates).
CREATE TABLE IF NOT EXISTS ratified_ucas (
    id          TEXT PRIMARY KEY,
    project_id  TEXT NOT NULL,
    uca_id      TEXT NOT NULL,
    hazard      TEXT NOT NULL,
    disposition TEXT NOT NULL,
    code_tokens TEXT NOT NULL DEFAULT '[]',
    created_at  TEXT NOT NULL,
    UNIQUE(project_id, uca_id)
);
