-- worker_sessions and worker_spawn_intents implement SPEC §7.3 + §7.4 #1/#6.
--
-- worker_sessions: one row per running worker. Phase tracks the §7.3
-- lifecycle from preparing_sandbox through succeeded / failed / timed_out
-- / stalled / cancelled. workspace_key is UNIQUE so the Conductor cannot
-- double-spawn even across leader handover.
--
-- worker_spawn_intents: recorded in the SAME transaction as the
-- ClaimRepo.Claim (orion-e48 wraps both). The UNIQUE workspace_key here
-- is the load-bearing safety: even if the Conductor crashes after recording
-- the intent but before calling K8sPodCreator, a subsequent retry sees the
-- same row and the pod creator handles AlreadyExists as success.

CREATE TABLE worker_sessions (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id          uuid        NOT NULL,
    run_id          uuid        NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    claim_id        uuid        NOT NULL REFERENCES issue_claims(id) ON DELETE CASCADE,
    workspace_key   text        NOT NULL UNIQUE,
    phase           text        NOT NULL DEFAULT 'preparing_sandbox'
                                 CHECK (phase IN (
                                     'preparing_sandbox', 'loading_run_snapshot',
                                     'synthesizing_patches', 'verifying_patches',
                                     'composing_patches', 'opening_pr_or_draft',
                                     'succeeded', 'failed', 'timed_out',
                                     'stalled', 'cancelled'
                                 )),
    last_event_at   timestamptz NOT NULL DEFAULT now(),
    started_at      timestamptz NOT NULL DEFAULT now(),
    finished_at     timestamptz NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_worker_sessions_run_phase ON worker_sessions (run_id, phase);

ALTER TABLE worker_sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE worker_sessions FORCE ROW LEVEL SECURITY;

CREATE POLICY worker_sessions_select ON worker_sessions FOR SELECT
    USING (org_id = current_setting('app.current_organization_id', true)::uuid);

CREATE POLICY worker_sessions_insert ON worker_sessions FOR INSERT
    WITH CHECK (org_id = current_setting('app.current_organization_id', true)::uuid);

CREATE POLICY worker_sessions_update ON worker_sessions FOR UPDATE
    USING (org_id = current_setting('app.current_organization_id', true)::uuid)
    WITH CHECK (org_id = current_setting('app.current_organization_id', true)::uuid);

CREATE POLICY worker_sessions_delete ON worker_sessions FOR DELETE
    USING (org_id = current_setting('app.current_organization_id', true)::uuid);

GRANT SELECT, INSERT, UPDATE, DELETE ON worker_sessions TO orion_api;

CREATE TABLE worker_spawn_intents (
    id                uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id            uuid        NOT NULL,
    claim_id          uuid        NOT NULL REFERENCES issue_claims(id) ON DELETE CASCADE,
    workspace_key     text        NOT NULL UNIQUE,
    pod_namespace     text        NOT NULL,
    pod_name_planned  text        NOT NULL,
    created_at        timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_worker_spawn_intents_claim ON worker_spawn_intents (claim_id);

ALTER TABLE worker_spawn_intents ENABLE ROW LEVEL SECURITY;
ALTER TABLE worker_spawn_intents FORCE ROW LEVEL SECURITY;

CREATE POLICY worker_spawn_intents_select ON worker_spawn_intents FOR SELECT
    USING (org_id = current_setting('app.current_organization_id', true)::uuid);

CREATE POLICY worker_spawn_intents_insert ON worker_spawn_intents FOR INSERT
    WITH CHECK (org_id = current_setting('app.current_organization_id', true)::uuid);

CREATE POLICY worker_spawn_intents_update ON worker_spawn_intents FOR UPDATE
    USING (org_id = current_setting('app.current_organization_id', true)::uuid)
    WITH CHECK (org_id = current_setting('app.current_organization_id', true)::uuid);

CREATE POLICY worker_spawn_intents_delete ON worker_spawn_intents FOR DELETE
    USING (org_id = current_setting('app.current_organization_id', true)::uuid);

GRANT SELECT, INSERT, UPDATE, DELETE ON worker_spawn_intents TO orion_api;
