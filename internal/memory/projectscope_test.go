package memory

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// TestProjectScopeIsolatesReads (or-gb1.6): project A's scoped items never
// appear under project B's scope; explicitly-global ('') items appear
// everywhere; the unscoped (admin) view sees all.
func TestProjectScopeIsolatesReads(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	s.ForProject("projA")
	if _, err := s.Write(ctx, Item{Tier: MTM, Kind: KindDecision, Content: "Decided constraints: module a-svc; routes /a", TrustTier: TrustProof, Heat: 1.0}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Write(ctx, Item{Tier: MTM, Kind: KindPattern, Content: "a global reliability pattern", TrustTier: TrustProof, Heat: 1.0, ProjectID: ""}); err != nil {
		t.Fatal(err)
	}
	// Explicit '' stays global — but Write stamps the scope when ProjectID is
	// empty, so a global write needs an UNSCOPED store handle.
	s.ForProject("")
	if _, err := s.Write(ctx, Item{Tier: MTM, Kind: KindPattern, Content: "an explicitly generalized pattern", TrustTier: TrustProof, Heat: 1.0}); err != nil {
		t.Fatal(err)
	}

	s.ForProject("projB")
	items, err := s.Retrieve(ctx, "constraints module routes pattern", MTM)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.Content == "Decided constraints: module a-svc; routes /a" {
			t.Fatalf("project A's scoped decision leaked into project B's recall: %+v", it)
		}
	}
	var sawGlobal bool
	for _, it := range items {
		if it.Content == "an explicitly generalized pattern" {
			sawGlobal = true
		}
	}
	if !sawGlobal {
		t.Fatal("explicitly-global items must stay visible to every project")
	}

	// Unscoped admin view sees everything.
	s.ForProject("")
	all, err := s.Retrieve(ctx, "constraints module routes pattern", MTM)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) < 2 {
		t.Fatalf("the unscoped view must see all projects, got %d", len(all))
	}
}

// TestProjectScopePreservedByPromotion (or-gb1.6): MTM→LTM promotion keeps
// project_id — 'within-project only' is real, not vacuous.
func TestProjectScopePreservedByPromotion(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	s.ForProject("projA")
	id, err := s.Write(ctx, Item{Tier: MTM, Kind: KindPattern, Content: "hot pattern", TrustTier: TrustProof, Heat: 5.0})
	if err != nil {
		t.Fatal(err)
	}
	// Eligibility: >= promoteMinVisits retrieved-as-relevant accesses.
	for i := 0; i < promoteMinVisits+1; i++ {
		if err := s.RecordAccess(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := s.Promote(ctx); err != nil {
		t.Fatal(err)
	}
	it, ok, err := s.Get(ctx, id)
	if err != nil || !ok {
		t.Fatalf("promoted item missing: %v", err)
	}
	if it.ProjectID != "projA" {
		t.Fatalf("promotion must preserve project_id, got %q (tier %s)", it.ProjectID, it.Tier)
	}
}

// TestLegacyDBMigratesProjectColumn (or-gb1.6): a pre-column DB opens, gains
// project_id via the probe-then-ALTER path, and legacy rows read as global.
func TestLegacyDBMigratesProjectColumn(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	// Build a legacy DB without the project_id column.
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE memory_items (
		id TEXT PRIMARY KEY, tier TEXT NOT NULL, kind TEXT NOT NULL, content TEXT NOT NULL,
		content_hash TEXT NOT NULL, pinned INTEGER NOT NULL DEFAULT 0,
		security_relevant INTEGER NOT NULL DEFAULT 0, trust_tier TEXT NOT NULL DEFAULT 'generation',
		heat REAL NOT NULL DEFAULT 0, visit_count INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL, last_accessed_at TEXT NOT NULL,
		promotion_id TEXT NOT NULL DEFAULT '', candidate INTEGER NOT NULL DEFAULT 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO memory_items (id, tier, kind, content, content_hash, created_at, last_accessed_at)
		VALUES ('legacy1','MTM','pattern','a legacy fact','h1','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()

	s, err := Open(dir)
	if err != nil {
		t.Fatalf("legacy DB must open + migrate: %v", err)
	}
	defer func() { _ = s.Close() }()
	s.ForProject("projX")
	items, err := s.Retrieve(ctx, "legacy fact", MTM)
	if err != nil {
		t.Fatal(err)
	}
	var seen bool
	for _, it := range items {
		if it.ID == "legacy1" {
			seen = true
		}
	}
	if !seen {
		t.Fatal("legacy rows ('' project) must read as global under any scope")
	}
}

// TestIdenticalContentAcrossProjectsGetsOwnRows (or-gb1.6): the content hash
// is project-salted — two projects writing IDENTICAL content get separate
// rows, so project B's write can neither refresh nor be hidden by project
// A's row (the id-collision leak).
func TestIdenticalContentAcrossProjectsGetsOwnRows(t *testing.T) {
	ctx := context.Background()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	s.ForProject("projA")
	idA, err := s.Write(ctx, Item{Tier: MTM, Kind: KindPattern, Content: "shared wording, different projects", TrustTier: TrustProof, Heat: 1.0})
	if err != nil {
		t.Fatal(err)
	}
	s.ForProject("projB")
	idB, err := s.Write(ctx, Item{Tier: MTM, Kind: KindPattern, Content: "shared wording, different projects", TrustTier: TrustProof, Heat: 1.0})
	if err != nil {
		t.Fatal(err)
	}
	if idA == idB {
		t.Fatal("identical content in two projects must yield two rows (project-salted ids)")
	}
	items, err := s.Retrieve(ctx, "shared wording", MTM)
	if err != nil {
		t.Fatal(err)
	}
	var own bool
	for _, it := range items {
		if it.ID == idB {
			own = true
		}
		if it.ID == idA {
			t.Fatal("project A's row must not surface under project B's scope")
		}
	}
	if !own {
		t.Fatal("project B must see its OWN row for the shared wording")
	}
}

// TestRetrieveTokenizedTermOverlap (or-gb1.7 acceptance 1): a multi-word
// intent sentence ranks an item sharing 2-3 TERMS above an unrelated but
// hotter item — whole-query substring matching never fired here.
func TestRetrieveTokenizedTermOverlap(t *testing.T) {
	ctx := context.Background()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	if _, err := s.Write(ctx, Item{Tier: MTM, Kind: KindPattern, TrustTier: TrustProof, Heat: 0.5,
		Content: "Proven task handler (verdict Accept): timezone conversion for the time service"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Write(ctx, Item{Tier: MTM, Kind: KindPattern, TrustTier: TrustProof, Heat: 5.0,
		Content: "completely unrelated database migration note"}); err != nil {
		t.Fatal(err)
	}

	items, err := s.Retrieve(ctx, "Build an HTTP time service that converts timezone on request", MTM)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) < 2 {
		t.Fatalf("want both items, got %d", len(items))
	}
	if !strings.Contains(items[0].Content, "timezone conversion") {
		t.Fatalf("the term-overlapping item must outrank the hotter unrelated one, got first: %q", items[0].Content)
	}

	// Empty query: pure pin/heat order — the hotter unrelated item leads.
	byHeat, err := s.Retrieve(ctx, "", MTM)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(byHeat[0].Content, "unrelated database") {
		t.Fatalf("an empty query must stay pure heat order, got first: %q", byHeat[0].Content)
	}
}
