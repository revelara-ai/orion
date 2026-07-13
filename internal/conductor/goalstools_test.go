package conductor

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator"
)

// specRegistry builds the spec-tool registry over a store-backed conductor
// with an ACTIVE unclassified-game project (the dogfood shape).
func specRegistry(t *testing.T) (*orchestrator.Conductor, func(name, input string) (string, error)) {
	t.Helper()
	c := orchestrator.NewWithStore(openStore(t))
	if _, err := c.Submit(context.Background(), "I'd like to build a game like Arc Raiders, pure PvE. I expect this to be a large project."); err != nil {
		t.Fatal(err)
	}
	r := specTools(c, nil, &changeSession{}, nil)
	run := func(name, input string) (string, error) {
		tool, ok := r.Get(name)
		if !ok {
			t.Fatalf("tool %s not registered", name)
		}
		return tool.Run(context.Background(), json.RawMessage(input))
	}
	return c, run
}

// TestRecordAnswerAcceptsGrillKeys (or-045a.2 DONE-WHEN c): grill answers
// (grill.* keys) record without "not a spec decision key" errors and land in
// the spec's decision lineage; junk keys are still rejected.
func TestRecordAnswerAcceptsGrillKeys(t *testing.T) {
	c, run := specRegistry(t)
	out, err := run("record_answer", `{"key":"grill.audience","value":"co-op PvE players"}`)
	if err != nil {
		t.Fatalf("grill.* answers must record: %v", err)
	}
	if !strings.Contains(out, "recorded") {
		t.Fatalf("unexpected result: %q", out)
	}
	// It landed in the decision lineage.
	_, sp, err := c.Store().CurrentProjectSpec(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ds, err := c.Store().DecisionsForSpec(context.Background(), sp.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range ds {
		if d.Key == "grill.audience" && d.Value == "co-op PvE players" {
			found = true
		}
	}
	if !found {
		t.Fatalf("grill answer missing from the decision lineage: %+v", ds)
	}
	// Negative: a non-checklist, non-grill key is still rejected (the guard is
	// scoped, not removed).
	if _, err := run("record_answer", `{"key":"made_up_key","value":"x"}`); err == nil {
		t.Fatal("junk keys must still be rejected")
	}
}

// TestGoalsToolsRoundTrip (or-045a.2 DONE-WHEN b): propose_goals persists the
// draft, ratify_goals anchors it with a content hash, and ratifying with no
// draft is refused.
func TestGoalsToolsRoundTrip(t *testing.T) {
	_, run := specRegistry(t)

	if _, err := run("ratify_goals", `{}`); err == nil {
		t.Fatal("ratify_goals with no proposed draft must be refused")
	}
	out, err := run("propose_goals", `{"goals":["pure PvE co-op extraction"],"non_goals":["PvP"],"success_criteria":["sub-16ms p99 inference"]}`)
	if err != nil {
		t.Fatalf("propose_goals: %v", err)
	}
	if !strings.Contains(out, "PvE") || !strings.Contains(strings.ToLower(out), "review") {
		t.Fatalf("the proposal should render the doc for developer review, got %q", out)
	}
	out2, err := run("ratify_goals", `{}`)
	if err != nil {
		t.Fatalf("ratify_goals: %v", err)
	}
	if !strings.Contains(out2, "ratified") {
		t.Fatalf("unexpected ratify result: %q", out2)
	}
	// Negative: an empty proposal is refused at the tool boundary too.
	if _, err := run("propose_goals", `{"goals":[],"non_goals":[],"success_criteria":[]}`); err == nil {
		t.Fatal("an empty goals proposal must be refused")
	}
}
