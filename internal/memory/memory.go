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
)

// Item is a memory item.
type Item struct {
	ID           string
	Tier         Tier
	Kind         string
	Content      string
	Hash         string
	Pinned       bool      // categorical anti-erosion pin (never evicted/summarized away)
	TrustTier    string    // human | proof | generation
	Heat         float64   // base importance set at write
	VisitCount   int       // times retrieved-as-relevant (frequency signal)
	LastAccessed time.Time // recency signal
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
	db *sql.DB
}

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
	// Additive migration for stores created before the heat model (or-hd3.3): add
	// visit_count only when absent. A fresh DB already has it from schema.sql, so probe the
	// columns first rather than ALTER-and-swallow (which would run every Open and also hide
	// genuine ALTER failures behind the expected "duplicate column" error).
	hasVisitCount := false
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
		if name == "visit_count" {
			hasVisitCount = true
		}
	}
	if err := probe.Close(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("memory migrate probe: %w", err)
	}
	if !hasVisitCount {
		if _, err := db.Exec(`ALTER TABLE memory_items ADD COLUMN visit_count INTEGER NOT NULL DEFAULT 0`); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("memory migrate visit_count: %w", err)
		}
	}
	return &Store{db: db}, nil
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
	id := it.Hash[:16] + string(it.Tier)
	pinned := 0
	if it.Pinned {
		pinned = 1
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO memory_items (id, tier, kind, content, content_hash, pinned, trust_tier, heat, created_at, last_accessed_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?)
		 ON CONFLICT(id) DO UPDATE SET kind=excluded.kind, content=excluded.content,
		   pinned=excluded.pinned, trust_tier=excluded.trust_tier, heat=excluded.heat,
		   last_accessed_at=excluded.last_accessed_at`,
		id, string(it.Tier), it.Kind, it.Content, it.Hash, pinned, it.TrustTier, it.Heat, now, now)
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
		`SELECT id, tier, kind, content, content_hash, pinned, trust_tier, heat, visit_count, last_accessed_at
		 FROM memory_items WHERE tier IN (`+strings.Join(placeholders, ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	var items []Item
	for rows.Next() {
		var it Item
		var pinned int
		var la string
		if err := rows.Scan(&it.ID, &it.Tier, &it.Kind, &it.Content, &it.Hash, &pinned, &it.TrustTier, &it.Heat, &it.VisitCount, &la); err != nil {
			_ = rows.Close()
			return nil, err
		}
		it.Pinned = pinned == 1
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

// EvictToCapacity keeps at most `keep` NON-pinned items in a tier (the hottest),
// deleting the cold remainder. Pinned items are categorically excluded from the
// candidate set — they are never evicted regardless of pressure (anti-erosion).
func (s *Store) EvictToCapacity(ctx context.Context, tier Tier, keep int) error {
	if keep < 0 {
		keep = 0
	}
	// Rank non-pinned items by EFFECTIVE heat (base decayed by recency + frequency),
	// computed in Go since SQLite lacks exp/log; keep the hottest `keep`, delete the rest.
	// Pinned items are never in the candidate set (anti-erosion).
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, heat, visit_count, last_accessed_at FROM memory_items WHERE tier=? AND pinned=0`, string(tier))
	if err != nil {
		return fmt.Errorf("memory evict scan: %w", err)
	}
	type cand struct {
		id  string
		eff float64
	}
	now := time.Now().UTC()
	var cands []cand
	for rows.Next() {
		var id, la string
		var heat float64
		var vc int
		if err := rows.Scan(&id, &heat, &vc, &la); err != nil {
			_ = rows.Close()
			return err
		}
		cands = append(cands, cand{id, effectiveHeat(heat, parseTS(la), vc, now)})
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(cands) <= keep {
		return nil
	}
	// Stable sort with an ID tie-break: when items share an effective heat at the keep
	// boundary, which ones survive is deterministic across runs.
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].eff != cands[j].eff {
			return cands[i].eff > cands[j].eff
		}
		return cands[i].id < cands[j].id
	})
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("memory evict: %w", err)
	}
	for _, c := range cands[keep:] {
		if _, err := tx.ExecContext(ctx, `DELETE FROM memory_items WHERE id=?`, c.id); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("memory evict: %w", err)
		}
	}
	return tx.Commit()
}

// Count returns the number of items in a tier (including pins).
func (s *Store) Count(ctx context.Context, tier Tier) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM memory_items WHERE tier=?`, string(tier)).Scan(&n)
	return n, err
}
