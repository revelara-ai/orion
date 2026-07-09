package harness

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/tools"
	"github.com/revelara-ai/orion/pkg/llm"
)

// TestLoopAnnouncedActionNudge: a turn ending "Let me submit the intent:" with
// no tool call is an abandoned action, not an answer (qwen3.5-9b planned
// perfectly then stopped at exactly this point). One continuation nudge must
// recover it.
func TestLoopAnnouncedActionNudge(t *testing.T) {
	prov := &scriptedProvider{resp: []*llm.ChatResponse{
		textTurn("The flow is clear. Let me start by submitting the intent:"),
		{StopReason: llm.StopToolUse, Content: []llm.ContentBlock{
			{Type: llm.BlockToolUse, ToolUse: &llm.ToolUse{ID: "c1", Name: "submit", Input: json.RawMessage(`{}`)}},
		}},
		textTurn("done"),
	}}
	execs := 0
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{Name: "submit", Description: "t", InputSchema: json.RawMessage(`{"type":"object"}`),
		Run: func(context.Context, json.RawMessage) (string, error) { execs++; return "ok", nil }})
	loop := Loop{Provider: prov, Tools: reg, Supervisor: Supervisor{MaxIterations: 10}}
	convo, resp, err := loop.Run(context.Background(), []llm.Message{llm.TextMessage(llm.RoleUser, "go")}, nil)
	if err != nil {
		t.Fatalf("nudged announcement must recover: %v", err)
	}
	if resp == nil || resp.Text() != "done" || execs != 1 {
		t.Fatalf("loop must continue to the real call: execs=%d", execs)
	}
	found := false
	for _, m := range convo {
		for _, b := range m.Content {
			if m.Role == llm.RoleUser && b.Type == llm.BlockText && strings.Contains(b.Text, "announced an action") {
				found = true
			}
		}
	}
	if !found {
		t.Error("continuation nudge missing from conversation")
	}
}

// TestLoopAnnouncedActionAcceptsSecondProseEnding: unlike template leakage, a
// prose ending is legitimate — if the model still ends in prose after one
// nudge, the turn completes normally (never trap a model that finished).
func TestLoopAnnouncedActionAcceptsSecondProseEnding(t *testing.T) {
	prov := &scriptedProvider{resp: []*llm.ChatResponse{
		textTurn("Let me check the file:"),
	}} // stays on last: announces forever
	loop := Loop{Provider: prov, Tools: tools.NewRegistry(), Supervisor: Supervisor{MaxIterations: 10}}
	_, resp, err := loop.Run(context.Background(), []llm.Message{llm.TextMessage(llm.RoleUser, "go")}, nil)
	if err != nil {
		t.Fatalf("persistent announcer must end normally, got: %v", err)
	}
	if resp == nil || !strings.Contains(resp.Text(), "Let me check") {
		t.Fatal("second prose ending must be accepted as the answer")
	}
	if prov.calls != 2 {
		t.Errorf("exactly one nudge round expected, provider calls = %d", prov.calls)
	}
}

// TestLoopNormalProseEndingUntouched: ordinary answers don't trigger the nudge.
func TestLoopNormalProseEndingUntouched(t *testing.T) {
	prov := &scriptedProvider{resp: []*llm.ChatResponse{textTurn("All tests pass. The change is committed.")}}
	loop := Loop{Provider: prov, Tools: tools.NewRegistry(), Supervisor: Supervisor{MaxIterations: 3}}
	_, _, err := loop.Run(context.Background(), []llm.Message{llm.TextMessage(llm.RoleUser, "go")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if prov.calls != 1 {
		t.Errorf("normal ending must not be nudged, provider calls = %d", prov.calls)
	}
}
