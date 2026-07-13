package conductor

import (
	"context"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/memory"
	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
	"github.com/revelara-ai/orion/internal/proof"
	"github.com/revelara-ai/orion/internal/proof/truthalign"
	"github.com/revelara-ai/orion/pkg/llm"
)

func distillMem(t *testing.T) *memory.Store {
	t.Helper()
	mem, err := memory.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mem.Close() })
	return mem
}

func candidateRules(t *testing.T, mem *memory.Store) []memory.Item {
	t.Helper()
	items, err := mem.ListCandidates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var rules []memory.Item
	for _, it := range items {
		if it.Kind == memory.KindRule {
			rules = append(rules, it)
		}
	}
	return rules
}

// Residual 1: the end-of-run bookend distills ONE cross-task rule from the
// run's struggle signal; a clean run distills nothing; the flag gates it.
func TestRunDistillBookend(t *testing.T) {
	mem := distillMem(t)
	ctx := context.Background()
	prov := &fakeLLM{resp: []*llm.ChatResponse{endTurn("Always regenerate mocks after interface changes.")}}
	SetDistillProvider(prov)
	t.Cleanup(func() { SetDistillProvider(nil) })
	t.Setenv("ORION_MEMORY_DISTILL", "1")

	struggled := []taskResult{
		{TaskID: "t1", Verdict: "Accept", Attempts: 3, FailureAnalysis: "mock drift on t1"},
		{TaskID: "t2", Verdict: "Accept", Attempts: 2, FailureAnalysis: "mock drift on t2"},
	}
	distillRunRule(ctx, mem, struggled)
	rules := candidateRules(t, mem)
	if len(rules) != 1 || !strings.Contains(rules[0].Content, "run-distilled rule:") {
		t.Fatalf("bookend must write ONE run-level candidate: %+v", rules)
	}
	if rules[0].TrustTier != memory.TrustGeneration || !rules[0].Candidate {
		t.Fatalf("containment broken: %+v", rules[0])
	}
	// The prompt must carry BOTH tasks (cross-task, not per-task).
	var seen string
	for _, m := range prov.lastReq.Messages {
		for _, b := range m.Content {
			seen += b.Text
		}
	}
	if !strings.Contains(seen, "t1") || !strings.Contains(seen, "t2") {
		t.Fatalf("bookend prompt must span the run's tasks: %s", seen)
	}

	// Clean run → no signal → nothing written.
	mem2 := distillMem(t)
	distillRunRule(ctx, mem2, []taskResult{{TaskID: "t3", Verdict: "Accept", Attempts: 1}})
	if got := candidateRules(t, mem2); len(got) != 0 {
		t.Fatalf("clean run must distill nothing: %+v", got)
	}

	// Flag off → nothing, even with struggle.
	t.Setenv("ORION_MEMORY_DISTILL", "")
	mem3 := distillMem(t)
	distillRunRule(ctx, mem3, struggled)
	if got := candidateRules(t, mem3); got != nil {
		t.Fatalf("distill must stay opt-in: %+v", got)
	}
}

// Residual 3: a committed change that overcame failed attempts distills a
// change-level rule from its digest trajectory.
func TestChangeDistillRule(t *testing.T) {
	mem := distillMem(t)
	ctx := context.Background()
	prov := &fakeLLM{resp: []*llm.ChatResponse{endTurn("Run go mod tidy before proving dependency changes.")}}
	SetDistillProvider(prov)
	t.Cleanup(func() { SetDistillProvider(nil) })
	t.Setenv("ORION_MEMORY_DISTILL", "1")

	distillChangeRule(ctx, mem, "bump the sqlite driver", []string{"attempt 1: undefined symbol", "attempt 2: missing go.sum entry"})
	rules := candidateRules(t, mem)
	if len(rules) != 1 || !strings.Contains(rules[0].Content, "change-distilled rule:") {
		t.Fatalf("change distill must write one candidate: %+v", rules)
	}
	// No digests (first-attempt success) → nothing to distill.
	mem2 := distillMem(t)
	distillChangeRule(ctx, mem2, "trivial", nil)
	if got := candidateRules(t, mem2); len(got) != 0 {
		t.Fatalf("no-struggle change must distill nothing: %+v", got)
	}
}

// Residual 2: the proven-outcome memory item carries the spec-slice SHAPE.
func TestOutcomeCarriesSpecSlice(t *testing.T) {
	mem := distillMem(t)
	ctx := context.Background()
	es := spec.ExecutableSpec{
		Intent: "an inventory service for widgets",
		ResponseContract: spec.ResponseContract{
			ContentType: "application/xml", Route: "/widgets",
			Cases: []spec.BehavioralCase{
				{Request: spec.RequestShape{Method: "GET", Path: "/widgets"}},
				{Kind: spec.KindUnit, Unit: &spec.UnitCase{Pkg: "store", Steps: []spec.UnitStep{{Call: "Put()", Want: "1"}}}},
			},
		},
	}
	slice := specSliceDigest(es, orchestrator.PlanTask{ID: "t1", ProofObligation: "widgets are listable"})
	for _, must := range []string{"inventory service", "widgets are listable", "format=xml", "1 http", "1 unit"} {
		if !strings.Contains(slice, must) {
			t.Fatalf("slice digest missing %q: %s", must, slice)
		}
	}
	rep := proof.Report{Outcome: truthalign.Outcome{Verdict: truthalign.Accept}}
	if err := rememberOutcome(ctx, mem, "t1", rep, &buildTrajectory{}, slice); err != nil {
		t.Fatal(err)
	}
	items, err := mem.ListByKind(ctx, memory.KindPattern)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || !strings.Contains(items[0].Content, "spec slice: intent=an inventory service") {
		t.Fatalf("outcome item must carry the slice: %+v", items)
	}
}
