package harness

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/tools"
	"github.com/revelara-ai/orion/pkg/llm"
)

func textTurn(text string) *llm.ChatResponse {
	return &llm.ChatResponse{StopReason: llm.StopEndTurn, Content: []llm.ContentBlock{{Type: llm.BlockText, Text: text}}}
}

const leakedCall = "Let me explore the codebase.\n\n<tool_call> <function=read_codebase>  </tool_call>"

// TestLoopToolCallLeakNudge: a model that emits Hermes-style tool-call syntax
// as TEXT (qwen under deep context) has made a failed tool attempt, not a
// finished answer. The harness must inject a corrective message and continue
// the loop — recovering models that regress to their training template.
func TestLoopToolCallLeakNudge(t *testing.T) {
	prov := &scriptedProvider{resp: []*llm.ChatResponse{
		textTurn(leakedCall),
		{StopReason: llm.StopToolUse, Content: []llm.ContentBlock{
			{Type: llm.BlockToolUse, ToolUse: &llm.ToolUse{ID: "c1", Name: "probe", Input: json.RawMessage(`{}`)}},
		}},
		textTurn("done"),
	}}
	execs := 0
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{
		Name: "probe", Description: "t", InputSchema: json.RawMessage(`{"type":"object"}`),
		Run: func(context.Context, json.RawMessage) (string, error) { execs++; return "ok", nil },
	})
	loop := Loop{Provider: prov, Tools: reg, Supervisor: Supervisor{MaxIterations: 10}}
	convo, resp, err := loop.Run(context.Background(), []llm.Message{llm.TextMessage(llm.RoleUser, "go")}, nil)
	if err != nil {
		t.Fatalf("nudged leak must recover: %v", err)
	}
	if resp == nil || resp.Text() != "done" || execs != 1 {
		t.Fatalf("loop must continue past the leak to a real tool call: execs=%d", execs)
	}
	nudged := false
	for _, m := range convo {
		for _, b := range m.Content {
			if m.Role == llm.RoleUser && b.Type == llm.BlockText && strings.Contains(b.Text, "NOT executed") {
				if !strings.Contains(b.Text, "probe") {
					t.Error("the corrective message must list the available tools")
				}
				nudged = true
			}
		}
	}
	if !nudged {
		t.Error("corrective message missing from the conversation")
	}
}

// TestLoopToolCallLeakPersistsStopsNamed: a second leak in the same turn stops
// with a NAMED error — not a silent prose ending the developer has to LoL at.
func TestLoopToolCallLeakPersistsStopsNamed(t *testing.T) {
	prov := &scriptedProvider{resp: []*llm.ChatResponse{textTurn(leakedCall)}} // stays on last → leaks forever
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{Name: "probe", Description: "t", InputSchema: json.RawMessage(`{"type":"object"}`),
		Run: func(context.Context, json.RawMessage) (string, error) { return "ok", nil }})
	loop := Loop{Provider: prov, Tools: reg, Supervisor: Supervisor{MaxIterations: 10}}
	_, _, err := loop.Run(context.Background(), []llm.Message{llm.TextMessage(llm.RoleUser, "go")}, nil)
	if err == nil || !strings.Contains(err.Error(), "tool calls as text") {
		t.Fatalf("persistent leak must stop with a named error, got: %v", err)
	}
}

// TestLoopPlainProseIsNotALeak: normal prose endings are untouched.
func TestLoopPlainProseIsNotALeak(t *testing.T) {
	prov := &scriptedProvider{resp: []*llm.ChatResponse{textTurn("All finished — the tests pass.")}}
	loop := Loop{Provider: prov, Tools: tools.NewRegistry(), Supervisor: Supervisor{MaxIterations: 3}}
	_, resp, err := loop.Run(context.Background(), []llm.Message{llm.TextMessage(llm.RoleUser, "go")}, nil)
	if err != nil || resp == nil || resp.Text() == "" {
		t.Fatalf("prose turn must end normally: %v", err)
	}
}
