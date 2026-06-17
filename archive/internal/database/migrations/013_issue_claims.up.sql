-- issue_claims is the durable per-tenant claim ledger (SPEC §7.2, §7.4 #1).
-- One row per (org_id, issue_external_id); the UNIQUE constraint is the
-- load-bearing idempotency guard so concurrent Conductor replicas (or a
-- retry after handover) cannot double-claim the same backlog issue.
-- Per §7.4 #6 the claim, cap check, and spawn-intent live in one tx; the
-- repo layer ships only the claim semantics here; cap-check + spawn-intent
-- are layered by orion-e43 (WorkerSession + SpawnIntent).

CREATE TABLE issue_claims (
    id                uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id            uuid        NOT NULL,
    run_id            uuid        NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    issue_external_id text        NOT NULL,
    state             text        NOT NULL DEFAULT 'unclaimed'
                                   CHECK (state IN (
                                       'unclaimed', 'claimed', 'dispatched',
                                       'in_progress', 'pr_open', 'reconciling',
                                       'released', 'escalated', 'superseded',
                                       'human_review', 'post_merge_incident',
                                       're_evaluation_queued', 're_dispatched'
                                   )),
    claimed_at        timestamptz NULL,
    fencing_token     bigint      NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, issue_external_id)
);

CREATE INDEX idx_issue_claims_run_state ON issue_claims (run_id, state);

ALTER TABLE issue_claims ENABLE ROW LEVEL SECURITY;
ALTER TABLE issue_claims FORCE ROW LEVEL SECURITY;

CREATE POLICY issue_claims_select ON issue_claims FOR SELECT
    USING (org_id = current_setting('app.current_organization_id', true)::uuid);

CREATE POLICY issue_claims_insert ON issue_claims FOR INSERT
    WITH CHECK (org_id = current_setting('app.current_organization_id', true)::uuid);

CREATE POLICY issue_claims_update ON issue_claims FOR UPDATE
    USING (org_id = current_setting('app.current_organization_id', true)::uuid)
    WITH CHECK (org_id = current_setting('app.current_organization_id', true)::uuid);

CREATE POLICY issue_claims_delete ON issue_claims FOR DELETE
    USING (org_id = current_setting('app.current_organization_id', true)::uuid);

GRANT SELECT, INSERT, UPDATE, DELETE ON issue_claims TO orion_api;
