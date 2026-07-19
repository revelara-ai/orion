package conductor

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/acp"
	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/pkg/llm"
)

// deadLLM fails every call the way a degraded provider does, and counts calls
// so the test can prove the breaker stopped the burn.
type deadLLM struct {
	fakeLLM
	calls int
}

func (d *deadLLM) Chat(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
	d.calls++
	return nil, errors.New("529 overloaded")
}
func (d *deadLLM) ChatStream(ctx context.Context, req llm.ChatRequest, _ func(string)) (*llm.ChatResponse, error) {
	return d.Chat(ctx, req)
}

// TestOrionAgentBreakerOpensAndRefusesTurns (or-mvr.2 acceptance): after N
// consecutive provider-failed turns the loop OPENS the breaker and escalates to
// the human; the next turn is refused WITHOUT a provider call (no budget burn
// on a dead dependency). The threshold is configurable via env.
func TestOrionAgentBreakerOpensAndRefusesTurns(t *testing.T) {
	t.Setenv("ORION_LOOP_BREAKER_TURNS", "2")
	oc := orchestrator.NewWithStore(openStore(t))
	prov := &deadLLM{}
	agent := NewOrionAgent(prov, oc, RoleTemplate{Project: "demo"})

	var out strings.Builder
	turn := func(text string) {
		_, err := agent.Prompt(context.Background(), "s1", text,
			func(u acp.Update) { out.WriteString(u.Text + "\n") },
			func(acp.PermissionRequest) (acp.PermissionResult, error) { return acp.PermissionResult{Outcome: "granted"}, nil })
		if err != nil {
			t.Fatalf("prompt must not hard-error (escalation is a message): %v", err)
		}
	}

	turn("hello") // bad turn 1
	if agent.breaker.Open() {
		t.Fatal("breaker must not open below the configured threshold")
	}
	turn("hello again") // bad turn 2 -> opens
	if !agent.breaker.Open() {
		t.Fatal("breaker must open after 2 consecutive provider-failed turns (ORION_LOOP_BREAKER_TURNS=2)")
	}
	if !strings.Contains(out.String(), "Circuit breaker OPEN") {
		t.Fatalf("the open transition must be escalated to the human, got:\n%s", out.String())
	}

	before := prov.calls
	turn("anyone there?") // refused fast — no provider call
	if prov.calls != before {
		t.Fatalf("an open breaker must refuse the turn WITHOUT calling the provider (calls %d -> %d)", before, prov.calls)
	}
	if !strings.Contains(out.String(), "loop circuit breaker open") {
		t.Fatalf("the refusal must name the breaker, got:\n%s", out.String())
	}
}

// TestOrionAgentModelSwitchResetsBreaker: /model replaces the dependency, so
// the accumulated failure evidence is stale and the breaker closes.
func TestOrionAgentModelSwitchResetsBreaker(t *testing.T) {
	t.Setenv("ORION_LOOP_BREAKER_TURNS", "1")
	oc := orchestrator.NewWithStore(openStore(t))
	agent := NewOrionAgent(&deadLLM{}, oc, RoleTemplate{Project: "demo"})
	agent.SetModel("fake/dead", func(_, _ string) (llm.Provider, string, error) {
		return &fakeLLM{resp: []*llm.ChatResponse{endTurn("ok")}}, "fake/alive", nil
	}, nil)

	_, _ = agent.Prompt(context.Background(), "s1", "hi",
		func(acp.Update) {}, func(acp.PermissionRequest) (acp.PermissionResult, error) { return acp.PermissionResult{Outcome: "granted"}, nil })
	if !agent.breaker.Open() {
		t.Fatal("breaker must be open after the failed turn")
	}
	if _, err := agent.Control(context.Background(), "s1", "model", "alive"); err != nil {
		t.Fatalf("model switch: %v", err)
	}
	if agent.breaker.Open() {
		t.Fatal("a /model switch must reset the breaker")
	}
}
