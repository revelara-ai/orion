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

-- Semantic-recall vectors (or-hd3.7). One vector per item, from the ACTIVE embedder; a
-- model/dim change re-embeds (ON CONFLICT replaces). embedder_id namespaces the vector so
-- different-model (different-dimension) vectors are never compared — no wrong-dim cosine.
-- Stored as a little-endian float32 BLOB. Pure-Go brute-force cosine search reads this; a
-- sqlite-vec/ANN index can replace the search behind the VectorIndex interface at scale.
CREATE TABLE IF NOT EXISTS memory_vectors (
    item_id      TEXT PRIMARY KEY,
    vector       BLOB NOT NULL,
    embedder_id  TEXT NOT NULL,
    dim          INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_vectors_embedder ON memory_vectors(embedder_id);
