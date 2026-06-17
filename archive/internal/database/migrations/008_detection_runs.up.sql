-- detection_runs persists one row per LoopDriver tick (SPEC §15.2 phase 7).
-- Per-tick provenance, phase enum, and findings counters live here so the
-- self-referential-loop guard (§15.4) and customer-facing surface (§21.3)
-- can audit detection behavior without scanning findings each time.
--
-- detection_findings is the per-finding ledger linked to detection_runs.
-- Findings retain their dedup_signature so cross-tick dedup queries
-- (§8.3 / §15.2 phase 4) short-circuit re-emission on unchanged input.
-- Suppressed-or-deduped findings are recorded too (SPEC §15.2 phase 7)
-- so customers can audit Orion's decisions.

CREATE TABLE detection_runs (
    id                            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id                        uuid        NOT NULL,
    binding_id                    uuid        NOT NULL REFERENCES tracker_binding(id) ON DELETE CASCADE,
    mode                          text        NOT NULL,
    phase                         text        NOT NULL,
    quiescent                     boolean     NOT NULL DEFAULT false,
    findings_total                integer     NOT NULL DEFAULT 0,
    findings_new                  integer     NOT NULL DEFAULT 0,
    findings_deduped              integer     NOT NULL DEFAULT 0,
    findings_suppressed           integer     NOT NULL DEFAULT 0,
    orion_filed_processed         integer     NOT NULL DEFAULT 0,
    customer_filed_processed      integer     NOT NULL DEFAULT 0,
    polaris_prior_processed       integer     NOT NULL DEFAULT 0,
    started_at                    timestamptz NOT NULL DEFAULT now(),
    finished_at                   timestamptz NULL,
    error_message                 text        NULL,

    CONSTRAINT detection_runs_mode_check
        CHECK (mode IN ('full','incremental','post_merge')),
    CONSTRAINT detection_runs_phase_check
        CHECK (phase IN ('running','completed','quiescent','failed'))
);

ALTER TABLE detection_runs ENABLE ROW LEVEL SECURITY;
ALTER TABLE detection_runs FORCE ROW LEVEL SECURITY;

CREATE POLICY detection_runs_select ON detection_runs FOR SELECT
    USING (org_id = current_setting('app.current_organization_id', true)::uuid);

CREATE POLICY detection_runs_insert ON detection_runs FOR INSERT
    WITH CHECK (org_id = current_setting('app.current_organization_id', true)::uuid);

CREATE POLICY detection_runs_update ON detection_runs FOR UPDATE
    USING (org_id = current_setting('app.current_organization_id', true)::uuid)
    WITH CHECK (org_id = current_setting('app.current_organization_id', true)::uuid);

CREATE POLICY detection_runs_delete ON detection_runs FOR DELETE
    USING (org_id = current_setting('app.current_organization_id', true)::uuid);

GRANT SELECT, INSERT, UPDATE, DELETE ON detection_runs TO orion_api;

-- Indexes for the canonical queries:
--   per-binding listing (org + binding, newest first)
--   per-run finding fetch (run_id)
CREATE INDEX idx_detection_runs_org_binding_started
    ON detection_runs (org_id, binding_id, started_at DESC);


CREATE TABLE detection_findings (
    id               uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id           uuid        NOT NULL,
    run_id           uuid        NOT NULL REFERENCES detection_runs(id) ON DELETE CASCADE,
    slug             text        NOT NULL,
    title            text        NOT NULL,
    category         text        NOT NULL,
    confidence       text        NOT NULL,
    severity         text        NOT NULL,
    control_codes    text[]      NOT NULL DEFAULT ARRAY[]::text[],
    file_path        text        NOT NULL,
    line_no          integer     NOT NULL,
    fingerprint      text        NOT NULL,
    dedup_signature  text        NULL,
    suppressed       boolean     NOT NULL DEFAULT false,
    deduped          boolean     NOT NULL DEFAULT false,
    created_at       timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE detection_findings ENABLE ROW LEVEL SECURITY;
ALTER TABLE detection_findings FORCE ROW LEVEL SECURITY;

CREATE POLICY detection_findings_select ON detection_findings FOR SELECT
    USING (org_id = current_setting('app.current_organization_id', true)::uuid);

CREATE POLICY detection_findings_insert ON detection_findings FOR INSERT
    WITH CHECK (org_id = current_setting('app.current_organization_id', true)::uuid);

CREATE POLICY detection_findings_update ON detection_findings FOR UPDATE
    USING (org_id = current_setting('app.current_organization_id', true)::uuid)
    WITH CHECK (org_id = current_setting('app.current_organization_id', true)::uuid);

CREATE POLICY detection_findings_delete ON detection_findings FOR DELETE
    USING (org_id = current_setting('app.current_organization_id', true)::uuid);

GRANT SELECT, INSERT, UPDATE, DELETE ON detection_findings TO orion_api;

CREATE INDEX idx_detection_findings_run
    ON detection_findings (run_id);

-- Cross-tick dedup queries (§8.3 / §15.2 phase 4) look up by
-- (org_id, dedup_signature) to short-circuit re-emission.
CREATE INDEX idx_detection_findings_org_dedup
    ON detection_findings (org_id, dedup_signature)
    WHERE dedup_signature IS NOT NULL;
