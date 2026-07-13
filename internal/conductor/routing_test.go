package conductor

import (
	"context"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/acp"
	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/pkg/llm"
)

// toolResultContaining drives one scripted agent turn and returns the first
// tool result containing marker ("" if none).
func toolResultContaining(t *testing.T, prov *fakeLLM, marker string) string {
	t.Helper()
	oc := orchestrator.NewWithStore(openStore(t))
	agent := NewOrionAgent(prov, oc, RoleTemplate{Project: "demo"})
	if _, err := agent.Prompt(context.Background(), "s1", "go",
		func(acp.Update) {},
		func(acp.PermissionRequest) (acp.PermissionResult, error) { return acp.PermissionResult{}, nil }); err != nil {
		t.Fatal(err)
	}
	for _, m := range prov.lastReq.Messages {
		for _, b := range m.Content {
			if b.Type == llm.BlockToolResult && b.ToolResult != nil && strings.Contains(b.ToolResult.Content, marker) {
				return b.ToolResult.Content
			}
		}
	}
	return ""
}

// or-3p5.10 acceptance: a change intent in an EXISTING repo auto-routes to
// the change tools — no role hint; force_greenfield is the explicit override.
func TestSubmitIntentAutoRoutesBrownfieldToChangeFlow(t *testing.T) {
	brownfieldFixtureRepo(t) // existing Go code + chdir (from grounding_test)

	prov := &fakeLLM{resp: []*llm.ChatResponse{
		tuResp("1", "submit_intent", `{"intent":"rename the widget helper"}`),
		endTurn("ok"),
	}}
	got := toolResultContaining(t, prov, "ROUTED")
	if !strings.Contains(got, "submit_change_intent") || !strings.Contains(got, "brownfield") {
		t.Fatalf("brownfield submit_intent must route to the change flow: %q", got)
	}

	// The override proceeds into the real build intake (open decisions).
	prov2 := &fakeLLM{resp: []*llm.ChatResponse{
		tuResp("1", "submit_intent", `{"intent":"build a brand new standalone metrics service","force_greenfield":true}`),
		endTurn("ok"),
	}}
	got = toolResultContaining(t, prov2, "open_decisions")
	if got == "" {
		t.Fatal("force_greenfield must proceed into the build intake")
	}
}

// The mirror: a change intent with NO existing code routes to the build flow.
func TestSubmitChangeIntentAutoRoutesGreenfieldToBuildFlow(t *testing.T) {
	t.Chdir(t.TempDir()) // empty dir: greenfield

	prov := &fakeLLM{resp: []*llm.ChatResponse{
		tuResp("1", "submit_change_intent", `{"intent":"add a Mul helper"}`),
		endTurn("ok"),
	}}
	got := toolResultContaining(t, prov, "ROUTED")
	if !strings.Contains(got, "submit_intent") || !strings.Contains(got, "greenfield") {
		t.Fatalf("greenfield submit_change_intent must route to the build flow: %q", got)
	}
}
