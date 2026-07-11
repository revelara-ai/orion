package harness

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/tools"
	"github.com/revelara-ai/orion/pkg/llm"
)

// TestLoopMaxIterationsErrorIsActionable: hitting the cap mid-task is
// recoverable — the conversation survives in session state and a follow-up
// message resumes with a fresh budget. The error must SAY so (or-4gib run:
// gemini finished all edits, hit the cap before ratify, and the bare "max
// iterations exceeded" left the developer thinking the work was lost).
func TestLoopMaxIterationsErrorIsActionable(t *testing.T) {
	// Vary the input each call so neither the stall detector nor the
	// duplicate guard fires — this is legitimate work that outlives the cap.
	n := 0
	prov := &scriptedProvider{resp: []*llm.ChatResponse{nil}}
	prov.next = func() *llm.ChatResponse {
		n++
		return &llm.ChatResponse{StopReason: llm.StopToolUse, Content: []llm.ContentBlock{
			{Type: llm.BlockToolUse, ToolUse: &llm.ToolUse{ID: "c", Name: "step", Input: json.RawMessage(`{"n":` + strings.Repeat("1", (n%9)+1) + `}`)}},
		}}
	}
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{Name: "step", Description: "t", InputSchema: json.RawMessage(`{"type":"object"}`),
		Run: func(context.Context, json.RawMessage) (string, error) { return "ok", nil }})
	loop := Loop{Provider: prov, Tools: reg, Supervisor: Supervisor{MaxIterations: 3}}
	_, _, err := loop.Run(context.Background(), []llm.Message{llm.TextMessage(llm.RoleUser, "go")}, nil)
	if !errors.Is(err, ErrMaxIterations) {
		t.Fatalf("want ErrMaxIterations, got %v", err)
	}
	for _, want := range []string{"3", "preserved", "continue"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("cap error must be actionable (missing %q): %v", want, err)
		}
	}
}

// TestLoopPaceHint: tool-happy models (gemini burned 40 generator iterations
// re-reading files) pace themselves when the budget is VISIBLE — the back
// half of the budget appends a countdown to each tool result; the front half
// stays clean.
func TestLoopPaceHint(t *testing.T) {
	n := 0
	prov := &scriptedProvider{}
	prov.next = func() *llm.ChatResponse {
		n++
		if n > 7 {
			return &llm.ChatResponse{StopReason: llm.StopEndTurn, Content: []llm.ContentBlock{{Type: llm.BlockText, Text: "done"}}}
		}
		return &llm.ChatResponse{StopReason: llm.StopToolUse, Content: []llm.ContentBlock{
			{Type: llm.BlockToolUse, ToolUse: &llm.ToolUse{ID: "c", Name: "step", Input: json.RawMessage(`{"n":` + strings.Repeat("7", n) + `}`)}},
		}}
	}
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{Name: "step", Description: "t", InputSchema: json.RawMessage(`{"type":"object"}`),
		Run: func(context.Context, json.RawMessage) (string, error) { return "ok", nil }})
	loop := Loop{Provider: prov, Tools: reg, Supervisor: Supervisor{MaxIterations: 8}}
	convo, _, err := loop.Run(context.Background(), []llm.Message{llm.TextMessage(llm.RoleUser, "go")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var results []string
	for _, m := range convo {
		for _, b := range m.Content {
			if b.ToolResult != nil {
				results = append(results, b.ToolResult.Content)
			}
		}
	}
	if len(results) != 7 {
		t.Fatalf("expected 7 tool results, got %d", len(results))
	}
	// Front half (iterations 1-4 of 8): clean.
	for i := 0; i < 4; i++ {
		if strings.Contains(results[i], "[budget:") {
			t.Errorf("result %d must not carry a pace hint:\n%s", i, results[i])
		}
	}
	// Back half: countdown present with the remaining count.
	if !strings.Contains(results[6], "[budget: 1 of 8 tool turns remain") {
		t.Errorf("late results must carry the countdown, got:\n%s", results[6])
	}
}

// TestLoopCapHintOverride: ephemeral loops (diff generator, subagents) must
// not tell the model/developer that "progress is preserved in this session" —
// their conversations are discarded. Supervisor.CapHint overrides the resume
// advice.
func TestLoopCapHintOverride(t *testing.T) {
	n := 0
	prov := &scriptedProvider{}
	prov.next = func() *llm.ChatResponse {
		n++
		return &llm.ChatResponse{StopReason: llm.StopToolUse, Content: []llm.ContentBlock{
			{Type: llm.BlockToolUse, ToolUse: &llm.ToolUse{ID: "c", Name: "step", Input: json.RawMessage(`{"n":` + strings.Repeat("3", (n%5)+1) + `}`)}},
		}}
	}
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{Name: "step", Description: "t", InputSchema: json.RawMessage(`{"type":"object"}`),
		Run: func(context.Context, json.RawMessage) (string, error) { return "ok", nil }})
	loop := Loop{Provider: prov, Tools: reg, Supervisor: Supervisor{MaxIterations: 3, CapHint: "the attempt is discarded; the pipeline may retry"}}
	_, _, err := loop.Run(context.Background(), []llm.Message{llm.TextMessage(llm.RoleUser, "go")}, nil)
	if err == nil || !strings.Contains(err.Error(), "attempt is discarded") {
		t.Fatalf("CapHint must override the resume advice: %v", err)
	}
	if strings.Contains(err.Error(), "preserved in this session") {
		t.Fatalf("ephemeral loop must not claim session persistence: %v", err)
	}
}
