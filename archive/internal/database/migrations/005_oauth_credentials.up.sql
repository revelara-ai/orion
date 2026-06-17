-- Encrypted OAuth credentials per E2-3 (substrate for Linear in E2-4).
--
-- TrackerBinding.credentials_ref points at one row here via a
-- vault://oauth/<id> URI scheme. The token rotation flow (see
-- internal/oauth/registry.go WireRefreshCallback) updates the
-- encrypted_blob column whenever an adapter receives a new
-- refresh_token, keeping the persistent store in sync with the
-- in-memory adapter state.
CREATE TABLE encrypted_oauth_credential (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id          uuid        NOT NULL,
    provider        text        NOT NULL CHECK (provider IN ('linear','jira','notion','github')),
    encrypted_blob  text        NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_encrypted_oauth_credential_org ON encrypted_oauth_credential (org_id);
CREATE INDEX idx_encrypted_oauth_credential_provider ON encrypted_oauth_credential (org_id, provider);

GRANT SELECT, INSERT, UPDATE, DELETE ON encrypted_oauth_credential TO orion_api;

ALTER TABLE encrypted_oauth_credential ENABLE ROW LEVEL SECURITY;
ALTER TABLE encrypted_oauth_credential FORCE ROW LEVEL SECURITY;

CREATE POLICY encrypted_oauth_credential_select_own ON encrypted_oauth_credential
    FOR SELECT
    USING (org_id = current_setting('app.current_organization_id', true)::uuid);

CREATE POLICY encrypted_oauth_credential_insert_own ON encrypted_oauth_credential
    FOR INSERT
    WITH CHECK (org_id = current_setting('app.current_organization_id', true)::uuid);

CREATE POLICY encrypted_oauth_credential_update_own ON encrypted_oauth_credential
    FOR UPDATE
    USING (org_id = current_setting('app.current_organization_id', true)::uuid)
    WITH CHECK (org_id = current_setting('app.current_organization_id', true)::uuid);

CREATE POLICY encrypted_oauth_credential_delete_own ON encrypted_oauth_credential
    FOR DELETE
    USING (org_id = current_setting('app.current_organization_id', true)::uuid);
