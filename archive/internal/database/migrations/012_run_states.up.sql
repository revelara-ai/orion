-- runs is the durable per-tenant orchestration record (SPEC §7.1).
-- One row per Run, lifecycle transitions per the state diagram in §7.1.
-- Survives Conductor restart and leader handover; recovery code (§7.4 #5)
-- reads runs in non-terminal states and reconciles via the Lookout.

CREATE TABLE runs (
    id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id       uuid        NOT NULL,
    repo_id      uuid        NOT NULL REFERENCES connected_repo(id) ON DELETE CASCADE,
    status       text        NOT NULL DEFAULT 'created'
                              CHECK (status IN (
                                  'created', 'inventorying', 'scanning',
                                  'backlog_active', 'draining', 'completed',
                                  'paused', 'cancelled', 'failed', 'config_invalid'
                              )),
    snapshot_ref text        NULL,
    started_at   timestamptz NOT NULL DEFAULT now(),
    finished_at  timestamptz NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_runs_org_status ON runs (org_id, status);

ALTER TABLE runs ENABLE ROW LEVEL SECURITY;
ALTER TABLE runs FORCE ROW LEVEL SECURITY;

CREATE POLICY runs_select ON runs FOR SELECT
    USING (org_id = current_setting('app.current_organization_id', true)::uuid);

CREATE POLICY runs_insert ON runs FOR INSERT
    WITH CHECK (org_id = current_setting('app.current_organization_id', true)::uuid);

CREATE POLICY runs_update ON runs FOR UPDATE
    USING (org_id = current_setting('app.current_organization_id', true)::uuid)
    WITH CHECK (org_id = current_setting('app.current_organization_id', true)::uuid);

CREATE POLICY runs_delete ON runs FOR DELETE
    USING (org_id = current_setting('app.current_organization_id', true)::uuid);

GRANT SELECT, INSERT, UPDATE, DELETE ON runs TO orion_api;
