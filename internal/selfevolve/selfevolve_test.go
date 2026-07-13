package selfevolve

import (
	"fmt"
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

// attachPassingEval attaches minimal PASSING deterministic evidence to a
// candidate (or-gb1.5: promotion fails closed without it).
func attachPassingEval(t *testing.T, mem *memory.Store, candidateID string) {
	t.Helper()
	ev := fmt.Sprintf(`{"candidate_id":%q,"happy":[{"name":"h1","input":"in","output":"expected out","latency_ms":5,"predicate":{"kind":"contains","arg":"expected"}}],"latency_slo_ms":100}`, candidateID)
	if _, err := mem.Write(context.Background(), memory.Item{
		Tier: memory.MTM, Kind: EvalEvidenceKind, Content: ev, TrustTier: memory.TrustProof, Heat: 1.0,
	}); err != nil {
		t.Fatal(err)
	}
}

// evidenceForAll attaches passing evidence to every current candidate.
func evidenceForAll(t *testing.T, mem *memory.Store) {
	t.Helper()
	cands, err := mem.ListCandidates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cands {
		attachPassingEval(t, mem, c.ID)
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
	evidenceForAll(t, mem)

	skillsDir := t.TempDir()
	promoted, _, err := PromoteCandidates(ctx, mem, skillsDir)
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
	evidenceForAll(t, mem)

	skillsDir := t.TempDir()
	p1, _, _ := PromoteCandidates(ctx, mem, skillsDir)
	p2, _, _ := PromoteCandidates(ctx, mem, skillsDir)
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
	promoted, _, err := PromoteCandidates(ctx, mem, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(promoted) != 0 {
		t.Fatalf("no candidates should yield no promotions, got %d", len(promoted))
	}
}

// TestPromoteCandidatesFailsClosed (or-gb1.5 acceptance): NO SKILL.md is
// written for an eval-less or eval-failing candidate; a passing one promotes;
// rejections name the failing predicate.
func TestPromoteCandidatesFailsClosed(t *testing.T) {
	ctx := context.Background()
	mem, err := memory.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mem.Close() }()

	writeCandidate(t, mem, "candidate WITHOUT evidence — must fail closed")
	writeCandidate(t, mem, "candidate WITH failing evidence")
	writeCandidate(t, mem, "candidate WITH passing evidence")
	cands, err := mem.ListCandidates(ctx)
	if err != nil || len(cands) != 3 {
		t.Fatalf("candidates: %v %d", err, len(cands))
	}
	byContent := map[string]string{}
	for _, c := range cands {
		byContent[c.Content] = c.ID
	}
	failID := byContent["candidate WITH failing evidence"]
	passID := byContent["candidate WITH passing evidence"]
	writeEval := func(id, output string) {
		ev := fmt.Sprintf(`{"candidate_id":%q,"happy":[{"name":"h","input":"i","output":%q,"predicate":{"kind":"contains","arg":"good"}}]}`, id, output)
		if _, err := mem.Write(ctx, memory.Item{Tier: memory.MTM, Kind: EvalEvidenceKind, Content: ev, TrustTier: memory.TrustProof, Heat: 1.0}); err != nil {
			t.Fatal(err)
		}
	}
	writeEval(failID, "this output is bad")
	writeEval(passID, "this output is good")

	skillsDir := t.TempDir()
	promoted, rejected, err := PromoteCandidates(ctx, mem, skillsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(promoted) != 1 {
		t.Fatalf("exactly the passing candidate promotes, got %v", promoted)
	}
	if len(rejected) != 2 {
		t.Fatalf("the eval-less and eval-failing candidates must be rejected, got %+v", rejected)
	}
	var sawFailClosed, sawPredicate bool
	for _, r := range rejected {
		if strings.Contains(r.Reason, "no eval evidence") {
			sawFailClosed = true
		}
		if strings.Contains(r.Reason, `predicate contains("good") failed`) {
			sawPredicate = true
		}
	}
	if !sawFailClosed || !sawPredicate {
		t.Fatalf("rejections must name fail-closed and the failing predicate: %+v", rejected)
	}
	// NO SKILL.md exists for the rejected candidates.
	r := skill.New()
	_, _ = r.LoadDir(skillsDir, skill.TrustGeneration)
	if len(r.List()) != 1 {
		t.Fatalf("only the passing candidate may materialize, got %d skills", len(r.List()))
	}
}
