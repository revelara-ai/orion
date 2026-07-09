package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/tools"
	"github.com/revelara-ai/orion/pkg/llm"
)

func stallToolUse(name, input string) *llm.ChatResponse {
	return &llm.ChatResponse{StopReason: llm.StopToolUse, Content: []llm.ContentBlock{
		{Type: llm.BlockToolUse, ToolUse: &llm.ToolUse{ID: "c1", Name: name, Input: json.RawMessage(input)}},
	}}
}

// TestLoopStallDetector: a model repeating the IDENTICAL tool call is in a loop
// the tool result will never break (gemma-31b looped add_case to
// MaxIterations). The harness must nudge at 3 consecutive identical calls
// (skip execution, return a corrective error result) and cleanly stop the turn
// at 5 with a NAMED stall error — not grind to a generic max-iterations.
func TestLoopStallDetector(t *testing.T) {
	prov := &scriptedProvider{resp: []*llm.ChatResponse{stallToolUse("add_case", `{"x":1}`)}}
	execs := 0
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{
		Name: "add_case", Description: "t", InputSchema: json.RawMessage(`{"type":"object"}`),
		Run: func(context.Context, json.RawMessage) (string, error) {
			execs++
			return "", fmt.Errorf("ungrounded case")
		},
	})
	loop := Loop{Provider: prov, Tools: reg, Supervisor: Supervisor{MaxIterations: 20}}
	convo, _, err := loop.Run(context.Background(), []llm.Message{llm.TextMessage(llm.RoleUser, "go")}, nil)
	if err == nil || !strings.Contains(err.Error(), "stalled") {
		t.Fatalf("turn must end with a named stall error, got: %v", err)
	}
	if execs != 2 {
		t.Errorf("the 3rd+ identical call must not execute; executions = %d, want 2", execs)
	}
	nudged := false
	for _, m := range convo {
		for _, b := range m.Content {
			if b.ToolResult != nil && strings.Contains(b.ToolResult.Content, "will not change") {
				if !b.ToolResult.IsError {
					t.Error("the stall nudge must be an error result")
				}
				nudged = true
			}
		}
	}
	if !nudged {
		t.Error("corrective nudge missing from the conversation")
	}
	if prov.calls >= 20 {
		t.Errorf("stall must stop the turn well before MaxIterations, used %d", prov.calls)
	}
}

// TestLoopStallDetectorIgnoresVariedCalls: alternating inputs and different
// tools are normal agent behavior (re-running go test after an edit is
// interleaved) — no nudge, everything executes.
func TestLoopStallDetectorIgnoresVariedCalls(t *testing.T) {
	prov := &scriptedProvider{resp: []*llm.ChatResponse{
		stallToolUse("add_case", `{"x":1}`),
		stallToolUse("add_case", `{"x":2}`),
		stallToolUse("add_case", `{"x":1}`),
		stallToolUse("add_case", `{"x":2}`),
		{StopReason: llm.StopEndTurn, Content: []llm.ContentBlock{{Type: llm.BlockText, Text: "done"}}},
	}}
	execs := 0
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{
		Name: "add_case", Description: "t", InputSchema: json.RawMessage(`{"type":"object"}`),
		Run: func(context.Context, json.RawMessage) (string, error) {
			execs++
			return "ok", nil
		},
	})
	loop := Loop{Provider: prov, Tools: reg, Supervisor: Supervisor{MaxIterations: 10}}
	_, resp, err := loop.Run(context.Background(), []llm.Message{llm.TextMessage(llm.RoleUser, "go")}, nil)
	if err != nil {
		t.Fatalf("varied calls must not stall: %v", err)
	}
	if execs != 4 || resp == nil || resp.Text() != "done" {
		t.Errorf("all varied calls execute and the turn completes: execs=%d", execs)
	}
}

// TestLoopStallDetectorWhitespaceInsensitive: the same call with reformatted
// JSON is still the same call.
func TestLoopStallDetectorWhitespaceInsensitive(t *testing.T) {
	prov := &scriptedProvider{resp: []*llm.ChatResponse{
		stallToolUse("add_case", `{"x": 1}`),
		stallToolUse("add_case", `{ "x":1 }`),
		stallToolUse("add_case", `{"x":1}`),
		stallToolUse("add_case", `{"x":1}`),
		stallToolUse("add_case", `{"x":1}`),
	}}
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{
		Name: "add_case", Description: "t", InputSchema: json.RawMessage(`{"type":"object"}`),
		Run: func(context.Context, json.RawMessage) (string, error) { return "", fmt.Errorf("nope") },
	})
	loop := Loop{Provider: prov, Tools: reg, Supervisor: Supervisor{MaxIterations: 10}}
	_, _, err := loop.Run(context.Background(), []llm.Message{llm.TextMessage(llm.RoleUser, "go")}, nil)
	if err == nil || !strings.Contains(err.Error(), "stalled") {
		t.Fatalf("whitespace variants are the same call; want stall, got: %v", err)
	}
}
