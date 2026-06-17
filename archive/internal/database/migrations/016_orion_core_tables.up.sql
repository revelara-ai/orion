CREATE SCHEMA IF NOT EXISTS orion;

-- orion.repos: connected GitHub repos, app install IDs, status.
CREATE TABLE orion.repos (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id          uuid        NOT NULL,
    repo_full_name  text        NOT NULL,
    app_install_id  text        NOT NULL,
    status          text        NOT NULL DEFAULT 'active',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_orion_repos_org_id ON orion.repos (org_id);
CREATE UNIQUE INDEX uniq_orion_repos_org_repo ON orion.repos (org_id, repo_full_name);

-- orion.runs: each run is a unit of work with status, started_at, finished_at, org_id, repo_id.
CREATE TABLE orion.runs (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id          uuid        NOT NULL,
    repo_id         uuid        NOT NULL, -- Reference to orion.repos.id
    status          text        NOT NULL, -- e.g., 'pending', 'running', 'completed', 'failed', 'cancelled'
    started_at      timestamptz NOT NULL DEFAULT now(),
    finished_at     timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fk_orion_run_repo FOREIGN KEY (repo_id) REFERENCES orion.repos(id) ON DELETE CASCADE
);

CREATE INDEX idx_orion_runs_org_id ON orion.runs (org_id);
CREATE INDEX idx_orion_runs_repo_id ON orion.runs (repo_id);

-- orion.architectural_models: JSONB blob keyed by run_id.
CREATE TABLE orion.architectural_models (
    run_id          uuid        PRIMARY KEY REFERENCES orion.runs(id) ON DELETE CASCADE,
    model           jsonb       NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now()
);

-- orion.constraints: SLO Fabric per run.
CREATE TABLE orion.constraints (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id          uuid        NOT NULL REFERENCES orion.runs(id) ON DELETE CASCADE,
    constraints     jsonb       NOT NULL, -- JSON representation of the SLO fabric
    created_at      timestamptz NOT NULL DEFAULT now()
);

-- orion.harnesses: harness config per run.
CREATE TABLE orion.harnesses (
    run_id          uuid        PRIMARY KEY REFERENCES orion.runs(id) ON DELETE CASCADE,
    config          jsonb       NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now()
);

-- orion.candidate_patches: pre-verification patches.
CREATE TABLE orion.candidate_patches (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id          uuid        NOT NULL REFERENCES orion.runs(id) ON DELETE CASCADE,
    patch           text        NOT NULL, -- The diff/content
    target_path     text        NOT NULL,
    control_id      text,       -- Reference to a Polaris control if applicable
    created_at      timestamptz NOT NULL DEFAULT now()
);

-- orion.accepted_patches: post-verification patches that ship.
CREATE TABLE orion.accepted_patches (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id          uuid        NOT NULL REFERENCES orion.runs(id) ON DELETE CASCADE,
    patch           text        NOT NULL,
    target_path     text        NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now()
);

-- orion.runs_metrics: verification metrics per run.
CREATE TABLE orion.runs_metrics (
    run_id          uuid        PRIMARY KEY REFERENCES orion.runs(id) ON DELETE CASCADE,
    metrics         jsonb       NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now()
);

-- RLS for orion schema
ALTER TABLE orion.repos ENABLE ROW LEVEL SECURITY;
ALTER TABLE orion.runs ENABLE ROW LEVEL SECURITY;
ALTER TABLE orion.architectural_models ENABLE ROW LEVEL SECURITY;
ALTER TABLE orion.constraints ENABLE ROW LEVEL SECURITY;
ALTER TABLE orion.harnesses ENABLE ROW LEVEL SECURITY;
ALTER TABLE orion.candidate_patches ENABLE ROW LEVEL SECURITY;
ALTER TABLE orion.accepted_patches ENABLE ROW LEVEL SECURITY;
ALTER TABLE orion.runs_metrics ENABLE ROW LEVEL SECURITY;

-- Common RLS policy pattern for org-scoped tables
CREATE POLICY repos_select_own ON orion.repos FOR SELECT USING (org_id = current_setting('app.current_organization_id', true)::uuid);
CREATE POLICY repos_insert_own ON orion.repos FOR INSERT WITH CHECK (org_id = current_setting('app.current_organization_id', true)::uuid);
CREATE POLICY repos_update_own ON orion.repos FOR UPDATE USING (org_id = current_setting('app.current_organization_id', true)::uuid)
    WITH CHECK (org_id = current_setting('app.current_organization_id', true)::uuid);

CREATE POLICY runs_select_own ON orion.runs FOR SELECT USING (org_id = current_setting('app.current_organization_id', true)::uuid);
CREATE POLICY runs_insert_own ON orion.runs FOR INSERT WITH CHECK (org_id = current_setting('app.current_organization_id', true)::uuid);

-- Grant permissions to the api role
GRANT USAGE ON SCHEMA orion TO orion_api;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA orion TO orion_api;
