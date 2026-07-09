package harness

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/tools"
	"github.com/revelara-ai/orion/pkg/llm"
)

func emptyTurn() *llm.ChatResponse {
	return &llm.ChatResponse{StopReason: llm.StopEndTurn}
}

// TestLoopEmptyTurnNudge: a response with no content and no tool calls is
// never a legitimate answer (qwen3.5-9b: reasoning burned the output, content
// arrived empty, the turn ended silently). One nudge must recover it.
func TestLoopEmptyTurnNudge(t *testing.T) {
	prov := &scriptedProvider{resp: []*llm.ChatResponse{
		emptyTurn(),
		{StopReason: llm.StopToolUse, Content: []llm.ContentBlock{
			{Type: llm.BlockToolUse, ToolUse: &llm.ToolUse{ID: "c1", Name: "step", Input: json.RawMessage(`{}`)}},
		}},
		textTurn("done"),
	}}
	execs := 0
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{Name: "step", Description: "t", InputSchema: json.RawMessage(`{"type":"object"}`),
		Run: func(context.Context, json.RawMessage) (string, error) { execs++; return "ok", nil }})
	loop := Loop{Provider: prov, Tools: reg, Supervisor: Supervisor{MaxIterations: 10}}
	convo, resp, err := loop.Run(context.Background(), []llm.Message{llm.TextMessage(llm.RoleUser, "go")}, nil)
	if err != nil {
		t.Fatalf("nudged empty turn must recover: %v", err)
	}
	if resp == nil || resp.Text() != "done" || execs != 1 {
		t.Fatalf("loop must continue past the empty turn: execs=%d", execs)
	}
	found := false
	for _, m := range convo {
		for _, b := range m.Content {
			if m.Role == llm.RoleUser && b.Type == llm.BlockText && strings.Contains(b.Text, "no content and no tool call") {
				found = true
			}
		}
	}
	if !found {
		t.Error("empty-turn nudge missing from conversation")
	}
}

// TestLoopEmptyTurnTwiceStopsNamed: a second empty turn stops with a NAMED
// error carrying the stop reason — a silent nothing is the one ending the
// developer can never act on.
func TestLoopEmptyTurnTwiceStopsNamed(t *testing.T) {
	prov := &scriptedProvider{resp: []*llm.ChatResponse{
		{StopReason: llm.StopMaxTokens}, // stays: empty forever, out of output budget
	}}
	loop := Loop{Provider: prov, Tools: tools.NewRegistry(), Supervisor: Supervisor{MaxIterations: 10}}
	_, _, err := loop.Run(context.Background(), []llm.Message{llm.TextMessage(llm.RoleUser, "go")}, nil)
	if err == nil || !strings.Contains(err.Error(), "no content") || !strings.Contains(err.Error(), "max_tokens") {
		t.Fatalf("repeated empty turn must stop with a named error incl. stop reason, got: %v", err)
	}
}
