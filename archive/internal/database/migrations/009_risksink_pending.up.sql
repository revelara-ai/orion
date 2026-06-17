-- risksink_pending queues SPEC §15.3 risk emissions that could not be
-- delivered to Polaris (API unreachable, 5xx, timeout). A future drain
-- job (E7 or a separate slice) reads rows older than retry_after and
-- re-attempts. Each row references the source DetectionFinding so the
-- drain can reconstruct the canonical risk payload without re-running
-- the scan.

CREATE TABLE risksink_pending (
    id                  uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id              uuid        NOT NULL,
    finding_id          uuid        NOT NULL REFERENCES detection_findings(id) ON DELETE CASCADE,
    polaris_endpoint    text        NOT NULL,
    payload             jsonb       NOT NULL,
    attempts            integer     NOT NULL DEFAULT 0,
    last_error          text        NULL,
    last_attempt_at     timestamptz NULL,
    retry_after         timestamptz NOT NULL,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE risksink_pending ENABLE ROW LEVEL SECURITY;
ALTER TABLE risksink_pending FORCE ROW LEVEL SECURITY;

CREATE POLICY risksink_pending_select ON risksink_pending FOR SELECT
    USING (org_id = current_setting('app.current_organization_id', true)::uuid);

CREATE POLICY risksink_pending_insert ON risksink_pending FOR INSERT
    WITH CHECK (org_id = current_setting('app.current_organization_id', true)::uuid);

CREATE POLICY risksink_pending_update ON risksink_pending FOR UPDATE
    USING (org_id = current_setting('app.current_organization_id', true)::uuid)
    WITH CHECK (org_id = current_setting('app.current_organization_id', true)::uuid);

CREATE POLICY risksink_pending_delete ON risksink_pending FOR DELETE
    USING (org_id = current_setting('app.current_organization_id', true)::uuid);

GRANT SELECT, INSERT, UPDATE, DELETE ON risksink_pending TO orion_api;

-- Drain job query: WHERE retry_after <= now() ORDER BY retry_after.
CREATE INDEX idx_risksink_pending_retry_after
    ON risksink_pending (retry_after);
