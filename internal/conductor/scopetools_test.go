package conductor

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/pkg/llm"
)

// TestGoalsToolsCarryStructuredNonGoalsAndDestination (or-kjcm.6): the tool
// surface round-trips structured non-goals (with rationale) and the
// destination line; the preview shows the destination.
func TestGoalsToolsCarryStructuredNonGoalsAndDestination(t *testing.T) {
	c, run := specRegistry(t)

	out, err := run("set_destination", `{"destination":"squad co-op raids feel fair at 200ms latency"}`)
	if err != nil || !strings.Contains(out, "steers the grill") {
		t.Fatalf("set_destination: %v %q", err, out)
	}
	if gc := c.GrillContext(context.Background()); !strings.Contains(gc, "200ms") {
		t.Fatalf("destination must reach the grill context: %q", gc)
	}

	out, err = run("propose_goals", `{"goals":["fair co-op PvE"],"destination":"squad co-op raids feel fair at 200ms latency","non_goals":[{"statement":"no PvP modes","rationale":"balance work doubles per mode"}]}`)
	if err != nil {
		t.Fatalf("propose_goals: %v", err)
	}
	if !strings.Contains(out, "no PvP modes") || !strings.Contains(out, "why: balance work doubles") {
		t.Fatalf("draft must render the structured non-goal: %q", out)
	}

	preview, err := run("preview_spec", `{}`)
	if err != nil {
		t.Fatalf("preview_spec: %v", err)
	}
	if !strings.Contains(preview, "Destination: squad co-op raids") {
		t.Fatalf("preview must carry the destination line:\n%s", preview)
	}
}

// TestNativeScopeCheckerParsesVerdict: the judge's forced tool call decodes
// into a conflict (or nil), and a missing call errors (fail-open upstream).
func TestNativeScopeCheckerParsesVerdict(t *testing.T) {
	conflict := &fakeLLM{resp: []*llm.ChatResponse{{Content: []llm.ContentBlock{{
		Type:    llm.BlockToolUse,
		ToolUse: &llm.ToolUse{ID: "1", Name: "report_scope_check", Input: json.RawMessage(`{"conflict":true,"non_goal":"no PvP modes","reason":"a duel is PvP"}`)},
	}}}}}
	got, err := NativeScopeChecker(conflict)(context.Background(), "players can duel each other", []orchestrator.NonGoal{{Statement: "no PvP modes"}})
	if err != nil || got == nil || got.NonGoal != "no PvP modes" {
		t.Fatalf("conflict verdict: %v %+v", err, got)
	}

	clean := &fakeLLM{resp: []*llm.ChatResponse{{Content: []llm.ContentBlock{{
		Type:    llm.BlockToolUse,
		ToolUse: &llm.ToolUse{ID: "1", Name: "report_scope_check", Input: json.RawMessage(`{"conflict":false}`)},
	}}}}}
	if got, err := NativeScopeChecker(clean)(context.Background(), "raid bosses drop loot", nil); err != nil || got != nil {
		t.Fatalf("clean verdict: %v %+v", err, got)
	}

	silent := &fakeLLM{resp: []*llm.ChatResponse{textResp("no tool call")}}
	if _, err := NativeScopeChecker(silent)(context.Background(), "anything", nil); err == nil {
		t.Fatal("a missing report call must error so the gate fails open explicitly")
	}
}
