-- autofile_counts tracks the §8.7 per-run + per-24h caps. Each row
-- is one filed issue with provenance (run_id + filed_at) so callers
-- can compute both counters without a separate aggregate table.
-- Filed issues themselves still live in normalized_issue; this is a
-- pure audit trail for the cap math.
CREATE TABLE autofile_counts (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id          uuid        NOT NULL,
    run_id          text        NOT NULL,
    issue_id        uuid        NULL REFERENCES normalized_issue(id) ON DELETE SET NULL,
    pattern         text        NOT NULL,
    filed_at        timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE autofile_counts ENABLE ROW LEVEL SECURITY;
ALTER TABLE autofile_counts FORCE ROW LEVEL SECURITY;

CREATE POLICY autofile_counts_select ON autofile_counts FOR SELECT
    USING (org_id = current_setting('app.current_organization_id', true)::uuid);

CREATE POLICY autofile_counts_insert ON autofile_counts FOR INSERT
    WITH CHECK (org_id = current_setting('app.current_organization_id', true)::uuid);

CREATE POLICY autofile_counts_update ON autofile_counts FOR UPDATE
    USING (org_id = current_setting('app.current_organization_id', true)::uuid)
    WITH CHECK (org_id = current_setting('app.current_organization_id', true)::uuid);

CREATE POLICY autofile_counts_delete ON autofile_counts FOR DELETE
    USING (org_id = current_setting('app.current_organization_id', true)::uuid);

GRANT SELECT, INSERT, UPDATE, DELETE ON autofile_counts TO orion_api;

-- Indexes for the two counter queries:
--   per-run: WHERE org_id = $1 AND run_id = $2
--   per-24h: WHERE org_id = $1 AND filed_at >= now() - interval '24h'
CREATE INDEX idx_autofile_counts_org_run ON autofile_counts (org_id, run_id);
CREATE INDEX idx_autofile_counts_org_filed ON autofile_counts (org_id, filed_at DESC);
