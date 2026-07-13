package conductor

import (
	"context"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/memory"
	"github.com/revelara-ai/orion/pkg/llm"
)

// ruleProvider is a minimal llm.Provider returning a fixed distilled rule.
type ruleProvider struct{ rule string }

func (p ruleProvider) Name() string { return "fake" }
func (p ruleProvider) Chat(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{Content: []llm.ContentBlock{{Type: llm.BlockText, Text: p.rule}}}, nil
}
func (p ruleProvider) ChatStream(ctx context.Context, req llm.ChatRequest, _ func(string)) (*llm.ChatResponse, error) {
	return p.Chat(ctx, req)
}
func (p ruleProvider) Models(context.Context) ([]llm.ModelInfo, error) { return nil, nil }
func (p ruleProvider) Ping(context.Context) error                      { return nil }

func openMem(t *testing.T) *memory.Store {
	t.Helper()
	m, err := memory.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open memory: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m
}

// TestDistillAbsentUnlessFlagSet (or-gb1.4 acceptance): distilled rule items
// exist ONLY with ORION_MEMORY_DISTILL=1 — never in the default build path —
// and when present they are TrustGeneration + Candidate.
func TestDistillAbsentUnlessFlagSet(t *testing.T) {
	ctx := context.Background()
	traj := &buildTrajectory{Attempts: 2, Overcame: []string{"tz handling missing"}, ChangeSummary: "modified func handleTime"}
	t.Cleanup(func() { SetDistillProvider(nil) })
	SetDistillProvider(ruleProvider{rule: "Always parse the tz query parameter before formatting the time."})

	// Flag unset: nothing is written even with a provider wired.
	t.Setenv("ORION_MEMORY_DISTILL", "")
	memOff := openMem(t)
	distillRule(ctx, memOff, "T1", acceptReport(), traj)
	if cands, _ := memOff.ListCandidates(ctx); len(cands) != 0 {
		t.Fatalf("distillation must be absent unless the flag is set, found %d candidates", len(cands))
	}

	// Flag set: exactly one rule, generation-tier + candidate.
	t.Setenv("ORION_MEMORY_DISTILL", "1")
	memOn := openMem(t)
	distillRule(ctx, memOn, "T1", acceptReport(), traj)
	cands, err := memOn.ListCandidates(ctx)
	if err != nil || len(cands) != 1 {
		t.Fatalf("expected exactly one distilled candidate, got %d (err %v)", len(cands), err)
	}
	r := cands[0]
	if r.TrustTier != memory.TrustGeneration || !r.Candidate || r.Kind != memory.KindRule {
		t.Fatalf("a distilled rule must be TrustGeneration+Candidate kind=rule, got %+v", r)
	}
	if !strings.Contains(r.Content, "tz query parameter") {
		t.Fatalf("the rule content must carry the distilled text: %s", r.Content)
	}

	// First-attempt wins (nothing overcome) distill nothing — no trajectory, no rule.
	memFirst := openMem(t)
	distillRule(ctx, memFirst, "T2", acceptReport(), &buildTrajectory{Attempts: 1})
	if cands, _ := memFirst.ListCandidates(ctx); len(cands) != 0 {
		t.Fatalf("a first-attempt win has no trajectory to distill, found %d", len(cands))
	}
}
