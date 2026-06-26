// Package memory is Orion's MemoryOS-style cognitive layer (or-6c9, PRD Memory &
// Context-Erosion Defense). It is distinct from the Context Store (which holds
// authoritative project FACTS): memory decides what the harness carries forward.
// Tiered STM/MTM/LTM with heat-based eviction, categorical anti-erosion PINS
// (spec + critical decisions never evicted), and a TRUST TIER on every item so a
// learned/summarized item can never smuggle instructions (poisoning defense
// shares the substrate with erosion defense).
//
// V2.0 persists tiers in a SEPARATE SQLite DB (WAL, 0600) — distinct from the
// Context Store. Vector (sqlite-vec) ANN retrieval is scaffolded; V2.0 retrieval
// is keyword+heat ranked.
package memory

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/revelara-ai/orion/internal/embed"
	_ "modernc.org/sqlite"
)

// Tier is a memory tier.
type Tier string

const (
	STM Tier = "STM" // current-task working set
	MTM Tier = "MTM" // run/session pages
	LTM Tier = "LTM" // durable cross-run patterns/procedures
)

// Trust tiers (per Security Requirements). A generation-domain item may never
// enter a proof prompt (Trust invariant 7).
const (
	TrustHuman      = "human"
	TrustProof      = "proof"
	TrustGeneration = "generation"
)

// Item kinds.
const (
	KindSpec      = "spec"
	KindDecision  = "decision"
	KindPage      = "page"
	KindSummary   = "summary"
	KindPattern   = "pattern"
	KindProcedure = "procedure"
	KindFailure   = "failure" // why a task failed (proof facts) / agent narrative (quarantined)
)

// Item is a memory item.
type Item struct {
	ID               string
	Tier             Tier
	Kind             string
	Content          string
	Hash             string
	Pinned           bool      // categorical anti-erosion pin (never evicted/summarized away)
	SecurityRelevant bool      // never lossy-summarized; retained as a full structured record
	TrustTier        string    // human | proof | generation
	Heat             float64   // base importance set at write
	VisitCount       int       // times retrieved-as-relevant (frequency signal)
	LastAccessed     time.Time // recency signal
}

// Heat model (or-hd3.3): effective heat = base importance decayed by recency since last
// access + a frequency boost (MemoryOS-style), computed lazily at query time. Tunable;
// config-driven weights are a later refinement.
const (
	heatRecencyTau = 7 * 24 * time.Hour // recency decay time constant
	heatFreqWeight = 0.5                // weight on log(1+visits)
)

// effectiveHeat is the live retention signal for ranking + eviction.
func effectiveHeat(baseHeat float64, lastAccessed time.Time, visits int, now time.Time) float64 {
	// Unknown recency (zero/unparseable timestamp) is treated as COLD, not recent: a row
	// with no usable last-access gets no recency credit, so a corrupt timestamp can't make
	// an item un-evictably hot.
	decay := 0.0
	if !lastAccessed.IsZero() {
		age := now.Sub(lastAccessed)
		if age < 0 {
			age = 0
		}
		decay = math.Exp(-float64(age) / float64(heatRecencyTau))
	}
	return baseHeat*decay + heatFreqWeight*math.Log1p(float64(visits))
}

// parseTS parses an RFC3339Nano timestamp, returning the zero time on error.
func parseTS(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

//go:embed schema.sql
var schemaSQL string

// Store is the memory store, backed by its own SQLite DB.
type Store struct {
	db   *sql.DB
	emb  embed.Embedder // active embedder for semantic recall (nil = keyword+heat only)
	vidx VectorIndex    // vector persistence + search (swap point: brute-force → sqlite-vec/ANN)
}

// SetEmbedder configures the active embedder for semantic recall (or-hd3.7). nil leaves the
// store in keyword+heat mode. Call Reindex after changing it to (re-)embed existing items.
func (s *Store) SetEmbedder(e embed.Embedder) { s.emb = e }

// Open opens (creating if needed) the memory store under dir/memory.db.
func Open(dir string) (*Store, error) {
	path := filepath.Join(dir, "memory.db")
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("memory open: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schemaSQL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("memory migrate: %w", err)
	}
	// Additive migrations for stores created before a later slice added a column (or-hd3.3
	// visit_count, or-hd3.4 security_relevant). A fresh DB already has them from schema.sql,
	// so probe the columns once and ALTER only what's missing — rather than ALTER-and-swallow
	// (which would run every Open and also hide genuine ALTER failures behind the expected
	// "duplicate column" error).
	existing := map[string]bool{}
	probe, err := db.Query(`SELECT name FROM pragma_table_info('memory_items')`)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("memory migrate probe: %w", err)
	}
	for probe.Next() {
		var name string
		if err := probe.Scan(&name); err != nil {
			_ = probe.Close()
			_ = db.Close()
			return nil, fmt.Errorf("memory migrate probe: %w", err)
		}
		existing[name] = true
	}
	if err := probe.Close(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("memory migrate probe: %w", err)
	}
	migrations := []struct{ col, ddl string }{
		{"visit_count", `ALTER TABLE memory_items ADD COLUMN visit_count INTEGER NOT NULL DEFAULT 0`},
		{"security_relevant", `ALTER TABLE memory_items ADD COLUMN security_relevant INTEGER NOT NULL DEFAULT 0`},
		{"promotion_id", `ALTER TABLE memory_items ADD COLUMN promotion_id TEXT NOT NULL DEFAULT ''`},
	}
	for _, m := range migrations {
		if existing[m.col] {
			continue
		}
		if _, err := db.Exec(m.ddl); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("memory migrate %s: %w", m.col, err)
		}
	}
	return &Store{db: db, vidx: newBruteForceIndex(db)}, nil
}

// Close closes the store.
func (s *Store) Close() error { return s.db.Close() }

// Write inserts (or replaces by content hash within a tier) a memory item.
func (s *Store) Write(ctx context.Context, it Item) (string, error) {
	if it.Hash == "" {
		sum := sha256.Sum256([]byte(it.Content))
		it.Hash = hex.EncodeToString(sum[:])
	}
	if it.TrustTier == "" {
		it.TrustTier = TrustGeneration
	}
	if it.Tier == "" {
		it.Tier = MTM
	}
	// Content-addressed id (or-hd3.6 review): the id is the content hash ALONE, not
	// hash+tier. Tier is a mutable column (Promote moves an item between tiers), so binding
	// the id to the tier would make a promoted item's id lie about its tier and could create
	// a same-content duplicate across tiers. One content ⇒ one item; its tier is whatever
	// column it currently holds.
	id := it.Hash[:16]
	pinned := 0
	if it.Pinned {
		pinned = 1
	}
	secRel := 0
	if it.SecurityRelevant {
		secRel = 1
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	// On conflict the id (= content hash + tier) means the SAME content already exists, so
	// kind/content/content_hash are unchanged. We refresh only the dynamic signals (heat,
	// recency). We deliberately do NOT update trust_tier, pinned, or security_relevant: a
	// later writer must never be able to re-classify an existing item's trust tier or
	// anti-erosion status — that would be a poisoning vector at the trust wall. Intentional
	// pinning uses Pin(); first-writer-wins for classification.
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO memory_items (id, tier, kind, content, content_hash, pinned, security_relevant, trust_tier, heat, created_at, last_accessed_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?)
		 ON CONFLICT(id) DO UPDATE SET heat=excluded.heat, last_accessed_at=excluded.last_accessed_at`,
		id, string(it.Tier), it.Kind, it.Content, it.Hash, pinned, secRel, it.TrustTier, it.Heat, now, now)
	if err != nil {
		return "", fmt.Errorf("memory write: %w", err)
	}
	return id, nil
}

// Pin marks an item as a categorical anti-erosion pin.
func (s *Store) Pin(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE memory_items SET pinned=1 WHERE id=?`, id)
	return err
}

// Retrieve returns items in the given tiers, ranked by relevance to query then
// heat. Pinned items always rank first (they are intent-anchoring). An empty
// query ranks purely by pin/heat. The query is matched case-insensitively.
func (s *Store) Retrieve(ctx context.Context, query string, tiers ...Tier) ([]Item, error) {
	if len(tiers) == 0 {
		tiers = []Tier{STM, MTM, LTM}
	}
	placeholders := make([]string, len(tiers))
	args := make([]any, len(tiers))
	for i, t := range tiers {
		placeholders[i] = "?"
		args[i] = string(t)
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, tier, kind, content, content_hash, pinned, security_relevant, trust_tier, heat, visit_count, last_accessed_at
		 FROM memory_items WHERE tier IN (`+strings.Join(placeholders, ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	var items []Item
	for rows.Next() {
		var it Item
		var pinned, secRel int
		var la string
		if err := rows.Scan(&it.ID, &it.Tier, &it.Kind, &it.Content, &it.Hash, &pinned, &secRel, &it.TrustTier, &it.Heat, &it.VisitCount, &la); err != nil {
			_ = rows.Close()
			return nil, err
		}
		it.Pinned = pinned == 1
		it.SecurityRelevant = secRel == 1
		it.LastAccessed = parseTS(la)
		items = append(items, it)
	}
	rowsErr := rows.Err()
	// Close the result set BEFORE the bump UPDATEs below. The store uses a single
	// connection (SetMaxOpenConns(1)); an open SELECT cursor would block any write on
	// that connection — a deadlock that the busy_timeout only turns into a slow failure.
	if cerr := rows.Close(); cerr != nil {
		return nil, cerr
	}
	if rowsErr != nil {
		return nil, rowsErr
	}

	now := time.Now().UTC()
	q := strings.ToLower(strings.TrimSpace(query))
	matched := func(it Item) bool { return q != "" && strings.Contains(strings.ToLower(it.Content), q) }
	// Tiered ranking: pins first, then query-relevance, then effective heat. A tiered order
	// (not fragile additive bonuses) keeps "pin > relevant > hot" true for ANY heat
	// magnitude; ID breaks ties so the ordering is deterministic.
	less := func(a, b Item) bool {
		if a.Pinned != b.Pinned {
			return a.Pinned
		}
		if am, bm := matched(a), matched(b); am != bm {
			return am
		}
		ha := effectiveHeat(a.Heat, a.LastAccessed, a.VisitCount, now)
		hb := effectiveHeat(b.Heat, b.LastAccessed, b.VisitCount, now)
		if ha != hb {
			return ha > hb
		}
		return a.ID < b.ID
	}
	sort.SliceStable(items, func(i, j int) bool { return less(items[i], items[j]) })

	// Recency/frequency feedback: an item surfaced as RELEVANT to a non-empty query is
	// "accessed" — bump visit_count + last_accessed so heat reflects use (the MemoryOS
	// loop). Pinned items are anti-erosion anchors, left untouched. The updates run in one
	// transaction (the single-conn pool + WAL would otherwise fsync per row), and the
	// in-memory items are updated so the returned slice matches the persisted state.
	if q != "" {
		nowStr := now.Format(time.RFC3339Nano)
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return nil, fmt.Errorf("memory retrieve bump: %w", err)
		}
		for i := range items {
			if !matched(items[i]) || items[i].Pinned {
				continue
			}
			if _, err := tx.ExecContext(ctx,
				`UPDATE memory_items SET visit_count=visit_count+1, last_accessed_at=? WHERE id=?`,
				nowStr, items[i].ID); err != nil {
				_ = tx.Rollback()
				return nil, fmt.Errorf("memory retrieve bump: %w", err)
			}
			items[i].VisitCount++
			items[i].LastAccessed = now
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("memory retrieve bump commit: %w", err)
		}
	}
	return items, nil
}

// summaryContent produces a deterministic, model-free summary string for a raw item: the
// content with whitespace collapsed and capped, prefixed with a provenance marker carrying
// the source content hash. Returns "" when there is nothing worth keeping (empty content).
func summaryContent(it Item) string {
	body := strings.Join(strings.Fields(it.Content), " ")
	if body == "" {
		return ""
	}
	const limit = 240
	if len(body) > limit {
		body = body[:limit]
	}
	return "[summary " + it.Hash[:8] + "] " + body
}

// summarizeForEviction is phase 1 of summarize-then-evict (or-hd3.4). It ranks the
// non-pinned, non-security items in a tier by effective heat and, for each one COLDER than
// the hottest `keep`, prepares it to be dropped: a RAW page is first written as a durable
// extractive Kind=summary (so its content survives the drop), while an item that is ALREADY
// a summary (or has empty content) is dropped directly — re-summarizing a summary would only
// nest markers and erode content, so the degradation path is full page -> summary -> gone.
// It returns the IDs to drop. Splitting phase 1 (here) from phase 2 (the drop in
// EvictToCapacity) makes the 2PC crash-safe: a crash after this returns — before the drop —
// leaves every raw page intact, because its summary is already durable. Re-running converges
// (summaries are never re-summarized) so the tier count stays bounded. Pinned and
// security_relevant items are excluded from the candidate set entirely (retained in full).
func (s *Store) summarizeForEviction(ctx context.Context, tier Tier, keep int) ([]string, error) {
	if keep < 0 {
		keep = 0
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, kind, content, content_hash, trust_tier, heat, visit_count, last_accessed_at
		 FROM memory_items WHERE tier=? AND pinned=0 AND security_relevant=0`, string(tier))
	if err != nil {
		return nil, fmt.Errorf("memory summarize scan: %w", err)
	}
	type cand struct {
		it  Item
		eff float64
	}
	now := time.Now().UTC()
	var cands []cand
	for rows.Next() {
		var it Item
		var la string
		if err := rows.Scan(&it.ID, &it.Kind, &it.Content, &it.Hash, &it.TrustTier, &it.Heat, &it.VisitCount, &la); err != nil {
			_ = rows.Close()
			return nil, err
		}
		it.Tier = tier
		it.LastAccessed = parseTS(la)
		cands = append(cands, cand{it, effectiveHeat(it.Heat, it.LastAccessed, it.VisitCount, now)})
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(cands) <= keep {
		return nil, nil
	}
	// Stable sort with an ID tie-break: which items survive at the keep boundary is
	// deterministic across runs.
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].eff != cands[j].eff {
			return cands[i].eff > cands[j].eff
		}
		return cands[i].it.ID < cands[j].it.ID
	})
	cold := cands[keep:]
	nowStr := now.Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("memory summarize: %w", err)
	}
	dropIDs := make([]string, 0, len(cold))
	for _, c := range cold {
		// Only RAW pages are summarized-before-drop. Existing summaries (already lossy) and
		// empty pages are dropped directly — no nesting, no erosion.
		if c.it.Kind != KindSummary {
			if content := summaryContent(c.it); content != "" {
				sum := sha256.Sum256([]byte(content))
				sumHash := hex.EncodeToString(sum[:])
				// Namespaced id ("sum_" prefix): a summary id can never collide with a raw
				// page id (raw ids are 16 hex chars), so phase 1 can never overwrite a raw
				// page that phase 2 then deletes.
				sumID := "sum_" + sumHash[:16]
				// A summary is never fresher than its source: inherit the raw's recency so it
				// ages out cleanly instead of looking artificially hot and starving raws.
				la := nowStr
				if !c.it.LastAccessed.IsZero() {
					la = c.it.LastAccessed.Format(time.RFC3339Nano)
				}
				// Trust tier is PRESERVED (a generation summary stays quarantined, never a
				// proof input).
				if _, err := tx.ExecContext(ctx,
					`INSERT INTO memory_items (id, tier, kind, content, content_hash, pinned, security_relevant, trust_tier, heat, created_at, last_accessed_at)
					 VALUES (?,?,?,?,?,0,0,?,?,?,?)
					 ON CONFLICT(id) DO UPDATE SET last_accessed_at=excluded.last_accessed_at`,
					sumID, string(tier), KindSummary, content, sumHash, c.it.TrustTier, c.it.Heat, nowStr, la); err != nil {
					_ = tx.Rollback()
					return nil, fmt.Errorf("memory summarize write: %w", err)
				}
			}
		}
		dropIDs = append(dropIDs, c.it.ID)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("memory summarize commit: %w", err)
	}
	return dropIDs, nil
}

// EvictToCapacity enforces a per-tier capacity via summarize-then-evict (or-hd3.4): the
// `keep` hottest non-pinned, non-security items stay in full; each colder RAW page is
// replaced by a durable extractive summary (phase 1) and only THEN dropped (phase 2), while
// colder summaries age out directly. Pinned and security_relevant items are never candidates
// — they are retained in full (anti-erosion + security retention). The 2PC ordering means a
// crash never hard-drops a raw page: its content always survives, at minimum as the
// already-committed summary. The tier count is bounded (cold summaries are dropped, not
// re-summarized into ever-growing nested markers).
func (s *Store) EvictToCapacity(ctx context.Context, tier Tier, keep int) error {
	dropIDs, err := s.summarizeForEviction(ctx, tier, keep)
	if err != nil {
		return err
	}
	if len(dropIDs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("memory evict: %w", err)
	}
	for _, id := range dropIDs {
		if _, err := tx.ExecContext(ctx, `DELETE FROM memory_items WHERE id=?`, id); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("memory evict: %w", err)
		}
	}
	return tx.Commit()
}

// Promotion thresholds (or-hd3.6): an MTM item earns durable LTM status once it is both hot
// and repeatedly useful. Config-driven tuning is a later refinement (like the heat weights).
const (
	promoteHeatThreshold = 1.5 // effective heat above which an item is durable
	promoteMinVisits     = 3   // and retrieved-as-relevant at least this often
)

// Promote moves qualifying MTM items to LTM — durable, within-project cross-run patterns.
// An item qualifies when its effective heat exceeds promoteHeatThreshold AND it has been
// retrieved-as-relevant at least promoteMinVisits times. Each promotion is tagged with the
// returned promotionID so the whole batch can be undone (ReversePromotion). Trust tier is
// PRESERVED — a promoted generation item stays quarantined and can never reach a proof
// prompt. The item id (content hash + original tier) is a stable opaque key and is left
// unchanged; only the tier column moves. Returns the promotionID and the count promoted.
func (s *Store) Promote(ctx context.Context) (string, int, error) {
	// Pinned items are anti-erosion anchors held in their tier on purpose — they are not
	// promotion candidates (moving them between tiers is needless churn).
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, heat, visit_count, last_accessed_at FROM memory_items WHERE tier=? AND pinned=0`, string(MTM))
	if err != nil {
		return "", 0, fmt.Errorf("memory promote scan: %w", err)
	}
	now := time.Now().UTC()
	var ids []string
	for rows.Next() {
		var id, la string
		var heat float64
		var vc int
		if err := rows.Scan(&id, &heat, &vc, &la); err != nil {
			_ = rows.Close()
			return "", 0, err
		}
		if vc >= promoteMinVisits && effectiveHeat(heat, parseTS(la), vc, now) > promoteHeatThreshold {
			ids = append(ids, id)
		}
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return "", 0, err
	}
	if len(ids) == 0 {
		return "", 0, nil
	}
	promotionID := "promo-" + now.Format("20060102T150405.000000000")
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", 0, fmt.Errorf("memory promote: %w", err)
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx,
			`UPDATE memory_items SET tier=?, promotion_id=? WHERE id=?`, string(LTM), promotionID, id); err != nil {
			_ = tx.Rollback()
			return "", 0, fmt.Errorf("memory promote: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return "", 0, fmt.Errorf("memory promote commit: %w", err)
	}
	return promotionID, len(ids), nil
}

// ReversePromotion undoes a promotion batch: every item tagged with promotionID is moved
// back to MTM and its tag cleared. Only MTM→LTM promotion exists, so reversal targets MTM.
func (s *Store) ReversePromotion(ctx context.Context, promotionID string) error {
	if promotionID == "" {
		return nil
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE memory_items SET tier=?, promotion_id='' WHERE promotion_id=? AND tier=?`,
		string(MTM), promotionID, string(LTM)); err != nil {
		return fmt.Errorf("memory reverse promotion: %w", err)
	}
	return nil
}

// Count returns the number of items in a tier (including pins).
func (s *Store) Count(ctx context.Context, tier Tier) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM memory_items WHERE tier=?`, string(tier)).Scan(&n)
	return n, err
}
