package conductor

import (
	"context"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/pkg/llm"
	"github.com/revelara-ai/orion/internal/orchestrator"
)

// /compact replaces a session's history with a single model-written summary; /model shows
// the current model and switches it (rebuilding the provider).
func TestControlCompactAndModel(t *testing.T) {
	prov := &fakeLLM{resp: []*llm.ChatResponse{endTurn("SUMMARY: built a time service on :8080 (UTC, json)")}}
	a := NewOrionAgent(prov, orchestrator.New(), RoleTemplate{})
	a.sessions["s1"] = []llm.Message{
		llm.TextMessage(llm.RoleUser, "build a time service"),
		llm.TextMessage(llm.RoleAssistant, "what port?"),
		llm.TextMessage(llm.RoleUser, "8080"),
	}

	res, err := a.Control(context.Background(), "s1", "compact", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res, "Compacted") {
		t.Errorf("compact result: %q", res)
	}
	if got := a.sessions["s1"]; len(got) != 1 || !strings.Contains(got[0].Content[0].Text, "SUMMARY") {
		t.Errorf("compact should collapse to one summary message, got %+v", got)
	}

	// /model with no arg shows the current model.
	a.SetModel("claude-opus-4-8", func(string) llm.Provider {
		return &fakeLLM{resp: []*llm.ChatResponse{endTurn("x")}}
	})
	if r, _ := a.Control(context.Background(), "s1", "model", ""); !strings.Contains(r, "claude-opus-4-8") {
		t.Errorf("/model (show) = %q", r)
	}
	// /model <id> switches and signals the new model to the TUI via the MODEL: sentinel.
	r, _ := a.Control(context.Background(), "s1", "model", "claude-sonnet-4-6")
	if !strings.HasPrefix(r, "MODEL:claude-sonnet-4-6") {
		t.Errorf("/model switch = %q, want MODEL: sentinel", r)
	}
	if a.model != "claude-sonnet-4-6" {
		t.Errorf("current model not updated, got %q", a.model)
	}
}

// compact is a graceful no-op with no history or no provider.
func TestControlCompactGraceful(t *testing.T) {
	a := NewOrionAgent(nil, orchestrator.New(), RoleTemplate{})
	if r, _ := a.Control(context.Background(), "empty", "compact", ""); !strings.Contains(r, "Nothing to compact") {
		t.Errorf("empty compact = %q", r)
	}
	a.sessions["s1"] = []llm.Message{llm.TextMessage(llm.RoleUser, "hi")}
	if r, _ := a.Control(context.Background(), "s1", "compact", ""); !strings.Contains(r, "offline") {
		t.Errorf("nil-provider compact should report offline, got %q", r)
	}
}
