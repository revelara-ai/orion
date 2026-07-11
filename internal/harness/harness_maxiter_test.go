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
