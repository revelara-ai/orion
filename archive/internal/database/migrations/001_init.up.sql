-- Initial extensions + the two-role split that makes RLS load-bearing.
--
-- Per polaris's pattern (see polaris memory `rls-pool-selection-rule`):
-- the connecting user is typically a superuser (testcontainers, local
-- dev, gke-managed CNPG bootstrap) and superusers bypass RLS even with
-- FORCE. The fix is a separate runtime role `orion_api` that has no
-- BYPASSRLS and no SUPERUSER. Every RLSPool query opens a tx, issues
-- `SET LOCAL ROLE orion_api`, then runs the caller's SQL. The session
-- the migration runs on stays as the original (superuser) connection,
-- which is what we want for DDL.
CREATE EXTENSION IF NOT EXISTS pgcrypto;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'orion_api') THEN
        CREATE ROLE orion_api NOLOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE;
    END IF;
END
$$;
