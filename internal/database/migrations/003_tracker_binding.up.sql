-- TrackerBinding per SPEC §4.1.2.
--
-- A customer-configured tracker connected to a repo. A repo MAY have
-- multiple bindings (e.g. GitHub Issues + Linear for the same
-- repo). credentials_ref points at an encrypted vault entry the
-- adapter resolves at use time.
CREATE TABLE tracker_binding (
    id               uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id           uuid        NOT NULL,
    repo_id          uuid        NOT NULL REFERENCES connected_repo(id) ON DELETE CASCADE,
    kind             text        NOT NULL CHECK (kind IN ('github_issues','linear')),
    config           jsonb       NOT NULL DEFAULT '{}'::jsonb,
    credentials_ref  text        NOT NULL,
    enabled          boolean     NOT NULL DEFAULT true,
    auto_file        boolean     NOT NULL DEFAULT false,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_tracker_binding_repo_id ON tracker_binding (repo_id);
CREATE INDEX idx_tracker_binding_org_id  ON tracker_binding (org_id);

GRANT SELECT, INSERT, UPDATE, DELETE ON tracker_binding TO orion_api;

ALTER TABLE tracker_binding ENABLE ROW LEVEL SECURITY;
ALTER TABLE tracker_binding FORCE ROW LEVEL SECURITY;

CREATE POLICY tracker_binding_select_own ON tracker_binding
    FOR SELECT
    USING (org_id = current_setting('app.current_organization_id', true)::uuid);

CREATE POLICY tracker_binding_insert_own ON tracker_binding
    FOR INSERT
    WITH CHECK (org_id = current_setting('app.current_organization_id', true)::uuid);

CREATE POLICY tracker_binding_update_own ON tracker_binding
    FOR UPDATE
    USING (org_id = current_setting('app.current_organization_id', true)::uuid)
    WITH CHECK (org_id = current_setting('app.current_organization_id', true)::uuid);

CREATE POLICY tracker_binding_delete_own ON tracker_binding
    FOR DELETE
    USING (org_id = current_setting('app.current_organization_id', true)::uuid);
