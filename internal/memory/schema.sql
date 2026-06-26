-- Orion memory store (or-6c9). Separate DB from the Context Store: this is the
-- cognitive layer (summaries, heat, retrieval), not authoritative facts.
CREATE TABLE IF NOT EXISTS memory_items (
    id               TEXT PRIMARY KEY,
    tier             TEXT NOT NULL CHECK (tier IN ('STM','MTM','LTM')),
    kind             TEXT NOT NULL,
    content          TEXT NOT NULL,
    content_hash     TEXT NOT NULL,
    pinned           INTEGER NOT NULL DEFAULT 0,
    security_relevant INTEGER NOT NULL DEFAULT 0,
    trust_tier       TEXT NOT NULL DEFAULT 'generation',
    heat             REAL NOT NULL DEFAULT 0,
    visit_count      INTEGER NOT NULL DEFAULT 0,
    created_at       TEXT NOT NULL,
    last_accessed_at TEXT NOT NULL,
    promotion_id     TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_memory_tier ON memory_items(tier, pinned, heat);
