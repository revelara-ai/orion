package selfevolve

import (
	"context"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/memory"
	"github.com/revelara-ai/orion/internal/skill"
)

func writeCandidate(t *testing.T, mem *memory.Store, content string) {
	t.Helper()
	if _, err := mem.Write(context.Background(), memory.Item{
		Tier:      memory.LTM,
		Kind:      memory.KindProcedure,
		Content:   content,
		TrustTier: memory.TrustGeneration,
		Candidate: true,
	}); err != nil {
		t.Fatal(err)
	}
}

// TestPromoteCandidatesCreatesDiscoverableSkill (or-qau): a proof-passed candidate is promoted
// to a generation-tier skill that the registry then discovers — closing the loop.
func TestPromoteCandidatesCreatesDiscoverableSkill(t *testing.T) {
	ctx := context.Background()
	mem, err := memory.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mem.Close() }()
	writeCandidate(t, mem, "candidate procedure from proven task T1: approach converged 3 modes")

	skillsDir := t.TempDir()
	promoted, err := PromoteCandidates(ctx, mem, skillsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(promoted) != 1 {
		t.Fatalf("expected 1 promotion, got %d", len(promoted))
	}

	r := skill.New()
	if _, err := r.LoadDir(skillsDir, skill.TrustGeneration); err != nil {
		t.Fatal(err)
	}
	s, ok := r.Get(promoted[0])
	if !ok {
		t.Fatalf("promoted skill %q is not discoverable", promoted[0])
	}
	if s.Trust != skill.TrustGeneration {
		t.Fatalf("a promoted skill must be generation trust (quarantined from proof), got %s", s.Trust)
	}
	if !strings.Contains(s.Body, "self-evolved") {
		t.Fatalf("promoted skill should carry provenance: %q", s.Body)
	}
}

// TestPromoteCandidatesIdempotent: re-promoting overwrites the same skill (stable id-derived
// name), never duplicates.
func TestPromoteCandidatesIdempotent(t *testing.T) {
	ctx := context.Background()
	mem, err := memory.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mem.Close() }()
	writeCandidate(t, mem, "a learned procedure")

	skillsDir := t.TempDir()
	p1, _ := PromoteCandidates(ctx, mem, skillsDir)
	p2, _ := PromoteCandidates(ctx, mem, skillsDir)
	if len(p1) != 1 || len(p2) != 1 || p1[0] != p2[0] {
		t.Fatalf("promotion not idempotent: %v then %v", p1, p2)
	}
	r := skill.New()
	_, _ = r.LoadDir(skillsDir, skill.TrustGeneration)
	if len(r.List()) != 1 {
		t.Fatalf("idempotent promotion should yield exactly 1 skill, got %d", len(r.List()))
	}
}

// TestPromoteNoCandidates: with no candidates, nothing is promoted (and it is not an error).
func TestPromoteNoCandidates(t *testing.T) {
	ctx := context.Background()
	mem, err := memory.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mem.Close() }()
	promoted, err := PromoteCandidates(ctx, mem, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(promoted) != 0 {
		t.Fatalf("no candidates should yield no promotions, got %d", len(promoted))
	}
}
