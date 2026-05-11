-- preflight_cache stores LLM pre-flight assessments per
-- (issue_id, body_signature) per SPEC §8.5 rule 2 so we don't pay
-- LLM cost on every ingest tick. body_signature is sha256(title +
-- "\n" + description) so an issue body change invalidates the cache
-- automatically.
CREATE TABLE preflight_cache (
    org_id          uuid        NOT NULL,
    issue_id        uuid        NOT NULL REFERENCES normalized_issue(id) ON DELETE CASCADE,
    body_signature  text        NOT NULL,
    decision        text        NOT NULL CHECK (decision IN ('in_scope','out_of_scope')),
    reason          text        NOT NULL DEFAULT '',
    decided_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (issue_id, body_signature)
);

ALTER TABLE preflight_cache ENABLE ROW LEVEL SECURITY;
ALTER TABLE preflight_cache FORCE ROW LEVEL SECURITY;

CREATE POLICY preflight_cache_select ON preflight_cache FOR SELECT
    USING (org_id = current_setting('app.current_organization_id', true)::uuid);

CREATE POLICY preflight_cache_insert ON preflight_cache FOR INSERT
    WITH CHECK (org_id = current_setting('app.current_organization_id', true)::uuid);

CREATE POLICY preflight_cache_update ON preflight_cache FOR UPDATE
    USING (org_id = current_setting('app.current_organization_id', true)::uuid)
    WITH CHECK (org_id = current_setting('app.current_organization_id', true)::uuid);

CREATE POLICY preflight_cache_delete ON preflight_cache FOR DELETE
    USING (org_id = current_setting('app.current_organization_id', true)::uuid);

GRANT SELECT, INSERT, UPDATE, DELETE ON preflight_cache TO orion_api;
