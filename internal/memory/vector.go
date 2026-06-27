package memory

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
	"sort"

	"github.com/revelara-ai/orion/internal/embed"
)

// VecHit is one vector-search result: an item id and its cosine similarity to the query.
// NOTE: a hit carries no trust tier — vector search is trust-BLIND by design. The consumer
// (the context engine) MUST resolve each item's trust_tier and quarantine generation-tier
// hits before they could reach a proof prompt (trust invariant §9.1); or-hd3.8's fusion goes
// through that same trust-tiered path.
type VecHit struct {
	ID    string
	Score float32
}

// VectorIndex persists + searches item embeddings (or-hd3.7). The default impl is a pure-Go
// brute-force cosine scan over the memory DB; a sqlite-vec / ANN index can replace it behind
// this interface when scale demands it — the embedder and this index are the two swap points.
// Vectors are namespaced by embedderID so different-model (different-dimension) vectors are
// never compared — no wrong-dimension cosine.
type VectorIndex interface {
	// Upsert stores (or replaces) the single vector for itemID, tagged with embedderID.
	Upsert(ctx context.Context, itemID string, vec []float32, embedderID string) error
	// Search returns the top-k items by cosine similarity to query among vectors from
	// embedderID only (never cross-embedder / cross-dimension).
	Search(ctx context.Context, query []float32, embedderID string, k int) ([]VecHit, error)
	// PruneOrphans deletes vectors whose item no longer exists in memory_items.
	PruneOrphans(ctx context.Context) error
}

// bruteForceIndex is the pure-Go VectorIndex over the memory_vectors table.
type bruteForceIndex struct {
	db *sql.DB
}

func newBruteForceIndex(db *sql.DB) *bruteForceIndex { return &bruteForceIndex{db: db} }

func (b *bruteForceIndex) Upsert(ctx context.Context, itemID string, vec []float32, embedderID string) error {
	if _, err := b.db.ExecContext(ctx,
		`INSERT INTO memory_vectors (item_id, vector, embedder_id, dim) VALUES (?,?,?,?)
		 ON CONFLICT(item_id) DO UPDATE SET vector=excluded.vector, embedder_id=excluded.embedder_id, dim=excluded.dim`,
		itemID, encodeVec(vec), embedderID, len(vec)); err != nil {
		return fmt.Errorf("memory vector upsert: %w", err)
	}
	return nil
}

func (b *bruteForceIndex) Search(ctx context.Context, query []float32, embedderID string, k int) ([]VecHit, error) {
	if len(query) == 0 || k <= 0 {
		return nil, nil
	}
	rows, err := b.db.QueryContext(ctx,
		`SELECT item_id, vector FROM memory_vectors WHERE embedder_id=? AND dim=?`, embedderID, len(query))
	if err != nil {
		return nil, fmt.Errorf("memory vector search: %w", err)
	}
	var hits []VecHit
	for rows.Next() {
		var id string
		var blob []byte
		if err := rows.Scan(&id, &blob); err != nil {
			_ = rows.Close()
			return nil, err
		}
		v := decodeVec(blob)
		if len(v) != len(query) { // defensive: never compare mismatched dimensions
			continue
		}
		hits = append(hits, VecHit{ID: id, Score: embed.Cosine(query, v)})
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Highest similarity first; ID tie-break for deterministic output.
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].ID < hits[j].ID
	})
	if len(hits) > k {
		hits = hits[:k]
	}
	return hits, nil
}

func (b *bruteForceIndex) PruneOrphans(ctx context.Context) error {
	if _, err := b.db.ExecContext(ctx,
		`DELETE FROM memory_vectors WHERE item_id NOT IN (SELECT id FROM memory_items)`); err != nil {
		return fmt.Errorf("memory vector prune: %w", err)
	}
	return nil
}

// Reindex embeds every item lacking a vector from the ACTIVE embedder and stores it; a
// model/dim change (a new embedder ID) re-embeds all items (none match the new id), so a
// dim mismatch is resolved by re-embedding — never by wrong-dimension math. It runs as ONE
// batched pass and is deliberately NOT on the Write hot-path (an embed call is ~hundreds of
// ms). No-op if no embedder is configured. Returns the number of items (re-)embedded.
func (s *Store) Reindex(ctx context.Context) (int, error) {
	if s.emb == nil {
		return 0, nil
	}
	if err := s.vidx.PruneOrphans(ctx); err != nil {
		return 0, err
	}
	eid := s.emb.ID()
	rows, err := s.db.QueryContext(ctx,
		`SELECT mi.id, mi.content FROM memory_items mi
		 LEFT JOIN memory_vectors mv ON mv.item_id = mi.id AND mv.embedder_id = ?
		 WHERE mv.item_id IS NULL AND mi.candidate = 0`, eid)
	if err != nil {
		return 0, fmt.Errorf("memory reindex scan: %w", err)
	}
	var ids, contents []string
	for rows.Next() {
		var id, content string
		if err := rows.Scan(&id, &content); err != nil {
			_ = rows.Close()
			return 0, err
		}
		ids = append(ids, id)
		contents = append(contents, content)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	vecs, err := s.emb.EmbedDocuments(ctx, contents)
	if err != nil {
		return 0, fmt.Errorf("memory reindex embed: %w", err)
	}
	if len(vecs) != len(ids) {
		return 0, fmt.Errorf("memory reindex: embedder returned %d vectors for %d items", len(vecs), len(ids))
	}
	for i, id := range ids {
		if err := s.vidx.Upsert(ctx, id, vecs[i], eid); err != nil {
			return i, err
		}
	}
	return len(ids), nil
}

func encodeVec(v []float32) []byte {
	b := make([]byte, 4*len(v))
	for i, x := range v {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(x))
	}
	return b
}

func decodeVec(b []byte) []float32 {
	if len(b)%4 != 0 {
		return nil // corrupt/partial BLOB — drop it rather than silently truncate (Search's
		// dimension guard then skips it; no wrong-dimension math, no half-read vector)
	}
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
	}
	return v
}
