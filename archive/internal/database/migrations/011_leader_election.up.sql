-- orion_leadership tracks per-tenant Conductor leadership leases.
-- Per SPEC §14.2 each Conductor replica acquires
--   pg_try_advisory_lock(hashtextextended(tenant_id::text, 0))
-- and increments fencing_token on acquisition. State mutations carry
-- the token as a guard so a former leader's in-flight writes roll back
-- once a new leader has incremented the token.
--
-- NOT RLS-protected: this is a system-level cross-tenant table read
-- and written by the Conductor across every tenant it serves. The
-- Conductor connects with orion_api credentials and is trusted to scope
-- queries by tenant_id explicitly.

CREATE TABLE orion_leadership (
    tenant_id        uuid        PRIMARY KEY,
    fencing_token    bigint      NOT NULL DEFAULT 0,
    holder_id        text        NULL,
    lease_seconds    integer     NOT NULL DEFAULT 30,
    last_renewed_at  timestamptz NOT NULL DEFAULT now(),
    created_at       timestamptz NOT NULL DEFAULT now()
);

GRANT SELECT, INSERT, UPDATE, DELETE ON orion_leadership TO orion_api;
