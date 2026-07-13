package conductor

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/pkg/llm"
)

// hazardRegistry builds the spec-tool registry over a large-scale project with
// RATIFIED GOALS (the propose_losses precondition), driven by prov.
func hazardRegistry(t *testing.T, prov llm.Provider) (*orchestrator.Conductor, func(name, input string) (string, error)) {
	t.Helper()
	c := orchestrator.NewWithStore(openStore(t))
	ctx := context.Background()
	if _, err := c.Submit(ctx, "I'd like to build a game like Arc Raiders, pure PvE. I expect this to be a large project."); err != nil {
		t.Fatal(err)
	}
	if err := c.ProposeGoals(ctx, orchestrator.GoalsDoc{Goals: []string{"uncanny RL mech movement"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RatifyGoals(ctx); err != nil {
		t.Fatal(err)
	}
	r := specTools(c, prov, &changeSession{}, nil)
	run := func(name, input string) (string, error) {
		tool, ok := r.Get(name)
		if !ok {
			t.Fatalf("tool %s not registered", name)
		}
		return tool.Run(context.Background(), json.RawMessage(input))
	}
	return c, run
}

// TestProposeAndRatifyLossesRoundTrip (or-045a.3 DONE-WHEN a): the LLM drafts
// losses from the ratified goals; the developer's confirmation ratifies them
// through the questionnaire; the model persists for the build's hazard gate.
func TestProposeAndRatifyLossesRoundTrip(t *testing.T) {
	draft := `{"losses":[{"id":"GL1","description":"players lose trust after an unfair mech glitch"}],` +
		`"scenarios":[{"id":"GS1","trigger":"inference exceeds the tick budget","sustaining_condition":"no IK fallback","loss":"GL1"}]}`
	prov := &fakeLLM{resp: []*llm.ChatResponse{tuResp("1", "report_hazards", draft)}}
	c, run := hazardRegistry(t, prov)

	out, err := run("propose_losses", `{}`)
	if err != nil {
		t.Fatalf("propose_losses: %v", err)
	}
	if !strings.Contains(out, "GL1") || !strings.Contains(out, "unfair mech glitch") {
		t.Fatalf("the draft must render the goal-derived losses for review, got %q", out)
	}

	out2, err := run("ratify_losses", draft)
	if err != nil {
		t.Fatalf("ratify_losses: %v", err)
	}
	if !strings.Contains(out2, "ratified") {
		t.Fatalf("unexpected ratify result: %q", out2)
	}
	// Negative: a scenario referencing an unknown loss is refused at the tool boundary.
	if _, err := run("ratify_losses", `{"losses":[{"id":"GL9","description":"x"}],"scenarios":[{"id":"S","trigger":"t","loss":"NOPE"}]}`); err == nil {
		t.Fatal("a scenario with an unknown loss ref must be refused")
	}
	_ = c
}

// Offline (nil provider): propose_losses degrades to guidance, never an error.
func TestProposeLossesOffline(t *testing.T) {
	_, run := hazardRegistry(t, nil)
	out, err := run("propose_losses", `{}`)
	if err != nil {
		t.Fatalf("offline propose_losses must not error: %v", err)
	}
	if !strings.Contains(out, "ratify_losses") {
		t.Fatalf("offline guidance should point at the manual path, got %q", out)
	}
}
