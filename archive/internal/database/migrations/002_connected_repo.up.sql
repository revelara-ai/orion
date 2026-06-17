-- ConnectedRepo per SPEC §4.1.1.
--
-- One row per customer-installed repository under Orion's GitHub
-- App. Per-tenant scoped via org_id; RLS policies use
-- current_setting('app.current_organization_id'). Runtime queries
-- arrive via the orion_api role (see 001_init).
CREATE TABLE connected_repo (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id          uuid        NOT NULL,
    provider        text        NOT NULL CHECK (provider IN ('github')),
    app_install_id  text        NOT NULL,
    repo_full_name  text        NOT NULL,
    default_branch  text        NOT NULL DEFAULT 'main',
    service_path    text,
    enabled         boolean     NOT NULL DEFAULT true,
    trust_mode      text        NOT NULL DEFAULT 'shadow'
                                CHECK (trust_mode IN ('shadow','draft','staging','full')),
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_connected_repo_org_id          ON connected_repo (org_id);
CREATE UNIQUE INDEX uniq_connected_repo_org_repo ON connected_repo (org_id, repo_full_name);

-- Grant the runtime role rights to operate on this table.
GRANT SELECT, INSERT, UPDATE, DELETE ON connected_repo TO orion_api;

-- Row-level security per SPEC §19.3. FORCE applies the policies to
-- the table owner too, but the runtime app connects as orion_api
-- (via SET LOCAL ROLE inside RLSPool's tx); orion_api lacks
-- BYPASSRLS so policies always apply for runtime queries.
ALTER TABLE connected_repo ENABLE ROW LEVEL SECURITY;
ALTER TABLE connected_repo FORCE ROW LEVEL SECURITY;

CREATE POLICY connected_repo_select_own ON connected_repo
    FOR SELECT
    USING (org_id = current_setting('app.current_organization_id', true)::uuid);

CREATE POLICY connected_repo_insert_own ON connected_repo
    FOR INSERT
    WITH CHECK (org_id = current_setting('app.current_organization_id', true)::uuid);

CREATE POLICY connected_repo_update_own ON connected_repo
    FOR UPDATE
    USING (org_id = current_setting('app.current_organization_id', true)::uuid)
    WITH CHECK (org_id = current_setting('app.current_organization_id', true)::uuid);

CREATE POLICY connected_repo_delete_own ON connected_repo
    FOR DELETE
    USING (org_id = current_setting('app.current_organization_id', true)::uuid);
