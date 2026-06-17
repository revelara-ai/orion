-- scope_requests is the audit ledger for every agent tool dispatch
-- (SPEC §11.3 + §4.1.12). The Conductor and the operator-facing
-- escalation review both read this table to evaluate scope creep.
-- Every tool call (allowed AND rejected) writes a row; the
-- rejection_reason column distinguishes the two paths.
--
-- Per-tenant scoped via RLS.

CREATE TABLE scope_requests (
    id                uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id            uuid        NOT NULL,
    run_id            uuid        NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    claim_id          uuid        NULL REFERENCES issue_claims(id) ON DELETE SET NULL,
    worker_session_id uuid        NULL REFERENCES worker_sessions(id) ON DELETE SET NULL,
    tool_name         text        NOT NULL,
    requested_scope   jsonb       NOT NULL DEFAULT '{}'::jsonb,
    granted_scope     jsonb       NULL,
    rejection_reason  text        NULL,
    decided_at        timestamptz NOT NULL DEFAULT now(),
    created_at        timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_scope_requests_run_tool   ON scope_requests (run_id, tool_name);
CREATE INDEX idx_scope_requests_rejections ON scope_requests (run_id) WHERE rejection_reason IS NOT NULL;

ALTER TABLE scope_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE scope_requests FORCE ROW LEVEL SECURITY;

CREATE POLICY scope_requests_select ON scope_requests FOR SELECT
    USING (org_id = current_setting('app.current_organization_id', true)::uuid);

CREATE POLICY scope_requests_insert ON scope_requests FOR INSERT
    WITH CHECK (org_id = current_setting('app.current_organization_id', true)::uuid);

CREATE POLICY scope_requests_update ON scope_requests FOR UPDATE
    USING (org_id = current_setting('app.current_organization_id', true)::uuid)
    WITH CHECK (org_id = current_setting('app.current_organization_id', true)::uuid);

CREATE POLICY scope_requests_delete ON scope_requests FOR DELETE
    USING (org_id = current_setting('app.current_organization_id', true)::uuid);

GRANT SELECT, INSERT, UPDATE, DELETE ON scope_requests TO orion_api;
