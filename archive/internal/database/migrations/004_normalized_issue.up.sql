-- NormalizedIssue per SPEC §4.1.7.
--
-- Every tracker issue Orion has seen gets a row here. Backlog
-- ingestion (E2-6) writes via INSERT ... ON CONFLICT (external_id)
-- DO UPDATE. Dedup signature (E2-7), eligibility (E2-8), priority
-- ordering (E2-9), and autofile gate (E2-10) all read from this
-- table.
CREATE TABLE normalized_issue (
    id                  uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id              uuid        NOT NULL,
    repo_id             uuid        NOT NULL REFERENCES connected_repo(id) ON DELETE CASCADE,
    tracker_binding_id  uuid        NOT NULL REFERENCES tracker_binding(id) ON DELETE CASCADE,
    external_id         text        NOT NULL,
    external_url        text        NOT NULL,
    title               text        NOT NULL,
    description         text        NOT NULL DEFAULT '',
    priority            smallint    NULL CHECK (priority IS NULL OR (priority >= 0 AND priority <= 4)),
    state               text        NOT NULL DEFAULT 'open'
                                    CHECK (state IN ('open','in_progress','blocked','closed','cancelled')),
    labels              text[]      NOT NULL DEFAULT '{}',
    polaris_risk_id     uuid        NULL,
    orion_filed         boolean     NOT NULL DEFAULT false,
    claim_status        text        NOT NULL DEFAULT 'unclaimed'
                                    CHECK (claim_status IN ('unclaimed','claimed','in_session','released')),
    eligibility         text        NULL
                                    CHECK (eligibility IS NULL OR eligibility IN
                                        ('eligible','ineligible_pattern','ineligible_path','ineligible_label',
                                         'ineligible_branch','ineligible_blocked','ineligible_suppressed',
                                         'ineligible_trust_mode')),
    dedup_signature     text        NULL,
    last_synced_at      timestamptz NOT NULL DEFAULT now(),
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);

-- external_id is globally unique (provider+repo+number identifies one
-- upstream issue everywhere).
CREATE UNIQUE INDEX uniq_normalized_issue_external_id ON normalized_issue (external_id);

-- Eligibility queries from E2-8 (paginated, filtered to one repo).
CREATE INDEX idx_normalized_issue_repo_elig ON normalized_issue (org_id, repo_id, eligibility);

-- Claim-queue queries from E4 Conductor.
CREATE INDEX idx_normalized_issue_repo_claim ON normalized_issue (org_id, repo_id, claim_status);

-- Incremental ingest from E2-6 uses since=last_synced_at per binding.
CREATE INDEX idx_normalized_issue_binding_sync ON normalized_issue (tracker_binding_id, last_synced_at);

-- E2-10 autofile UNIQUE gate: prevent double-filing an orion-filed
-- issue for the same dedup_signature within an org. Partial index so
-- existing human-filed or risk-mapped issues don't block auto-file
-- against the same signature.
CREATE UNIQUE INDEX uniq_normalized_issue_orion_filed_dedup
    ON normalized_issue (org_id, dedup_signature)
    WHERE orion_filed = true AND dedup_signature IS NOT NULL;

-- Lookups by dedup_signature for E2-7 + E2-10.
CREATE INDEX idx_normalized_issue_dedup ON normalized_issue (org_id, dedup_signature)
    WHERE dedup_signature IS NOT NULL;

GRANT SELECT, INSERT, UPDATE, DELETE ON normalized_issue TO orion_api;

ALTER TABLE normalized_issue ENABLE ROW LEVEL SECURITY;
ALTER TABLE normalized_issue FORCE ROW LEVEL SECURITY;

CREATE POLICY normalized_issue_select_own ON normalized_issue
    FOR SELECT
    USING (org_id = current_setting('app.current_organization_id', true)::uuid);

CREATE POLICY normalized_issue_insert_own ON normalized_issue
    FOR INSERT
    WITH CHECK (org_id = current_setting('app.current_organization_id', true)::uuid);

CREATE POLICY normalized_issue_update_own ON normalized_issue
    FOR UPDATE
    USING (org_id = current_setting('app.current_organization_id', true)::uuid)
    WITH CHECK (org_id = current_setting('app.current_organization_id', true)::uuid);

CREATE POLICY normalized_issue_delete_own ON normalized_issue
    FOR DELETE
    USING (org_id = current_setting('app.current_organization_id', true)::uuid);
