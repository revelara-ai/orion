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

// TestOpenQuestionTools (or-045a.6 DONE-WHEN a): deferring an ambiguity via
// raise_open_question persists it (it does not vanish), list surfaces it, and
// resolve records the developer's decision — including by short id prefix.
func TestOpenQuestionTools(t *testing.T) {
	_, run := specRegistry(t)
	out, err := run("raise_open_question", `{"phase":"goals","question":"Mech-based or fantasy-based?","key":"grill.setting","severity":"blocking"}`)
	if err != nil {
		t.Fatalf("raise: %v", err)
	}
	if !strings.Contains(out, "raised") {
		t.Fatalf("unexpected raise output: %q", out)
	}
	listed, err := run("list_open_questions", `{}`)
	if err != nil || !strings.Contains(listed, "Mech-based or fantasy-based?") {
		t.Fatalf("list must surface the question: %q err=%v", listed, err)
	}
	// Resolve by the 8-char prefix shown in the listing.
	short := strings.Fields(strings.Split(listed, "\n")[1])[1]
	if _, err := run("resolve_open_question", `{"id":"`+short+`","resolution":"answered","value":"mech-based"}`); err != nil {
		t.Fatalf("resolve by prefix: %v", err)
	}
	if listed2, _ := run("list_open_questions", `{}`); !strings.Contains(listed2, "no open questions") {
		t.Fatalf("resolved question must leave the list: %q", listed2)
	}
	// Negative: junk resolution kind is refused at the tool boundary.
	if _, err := run("raise_open_question", `{"phase":"goals","question":"q2","severity":"blocking"}`); err != nil {
		t.Fatal(err)
	}
	l3, _ := run("list_open_questions", `{}`)
	id3 := strings.Fields(strings.Split(l3, "\n")[1])[1]
	if _, err := run("resolve_open_question", `{"id":"`+id3+`","resolution":"ignored","value":"x"}`); err == nil {
		t.Fatal("junk resolution kinds must be refused")
	}
}
