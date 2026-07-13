package conductor

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/pkg/llm"
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
	a.SetModel("claude-opus-4-8", func(_, m string) (llm.Provider, string, error) {
		return &fakeLLM{resp: []*llm.ChatResponse{endTurn("x")}}, m, nil
	}, nil)
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

func TestSwitchModelRebuildError(t *testing.T) {
	a := NewOrionAgent(nil, nil, RoleTemplate{})
	a.SetModel("m1", func(_, arg string) (llm.Provider, string, error) {
		return nil, "", fmt.Errorf(`unknown provider "nope"`)
	}, nil)
	msg, err := a.switchModel("nope/m2")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "nope") || strings.Contains(msg, "MODEL:") {
		t.Errorf("failed switch must report the error and NOT emit the MODEL: sentinel: %q", msg)
	}
	if a.model != "m1" {
		t.Errorf("failed switch must not change the model: %q", a.model)
	}
}

func TestSwitchModelListsAcrossProviders(t *testing.T) {
	a := NewOrionAgent(nil, nil, RoleTemplate{})
	a.SetModel("m1", func(_, arg string) (llm.Provider, string, error) { return nil, arg, nil },
		func(context.Context) []string { return []string{"anthropic/claude-opus-4-8", "lmstudio/qwen3-32b"} })
	msg, err := a.switchModel("")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "lmstudio/qwen3-32b") {
		t.Errorf("empty-arg /model must list configured providers' models: %q", msg)
	}
}

// TestSwitchModelBareIDResolvesAgainstCurrentProvider is the regression test for the
// merge-blocking bug: after a provider switch (/model gemini/x), a later bare /model y
// must resolve against the NEW current provider, not the launch-time one. The fake
// rebuild records the currentRef it was called with so we can assert on it directly.
func TestSwitchModelBareIDResolvesAgainstCurrentProvider(t *testing.T) {
	a := NewOrionAgent(nil, nil, RoleTemplate{})
	var gotCurrentRef []string
	rebuild := func(currentRef, arg string) (llm.Provider, string, error) {
		gotCurrentRef = append(gotCurrentRef, currentRef)
		ref := arg
		if !strings.Contains(ref, "/") {
			provider, _, _ := strings.Cut(currentRef, "/")
			ref = provider + "/" + arg
		}
		return nil, ref, nil
	}
	a.SetModel("prov1/m1", rebuild, nil)

	// Switch providers explicitly.
	msg, err := a.switchModel("prov2/x")
	if err != nil {
		t.Fatal(err)
	}
	if a.model != "prov2/x" {
		t.Errorf("a.model after provider switch = %q, want %q", a.model, "prov2/x")
	}
	if !strings.HasPrefix(msg, "MODEL:prov2/x") {
		t.Errorf("MODEL: sentinel after provider switch = %q, want full ref prov2/x", msg)
	}

	// Bare-id switch MUST resolve against the CURRENT provider (prov2), not the
	// launch-time provider (prov1) — this is the bug the fix closes.
	msg, err = a.switchModel("y")
	if err != nil {
		t.Fatal(err)
	}
	if len(gotCurrentRef) != 2 || gotCurrentRef[1] != "prov2/x" {
		t.Fatalf("bare switch must call rebuild with currentRef %q, got calls %v", "prov2/x", gotCurrentRef)
	}
	if a.model != "prov2/y" {
		t.Errorf("a.model after bare switch = %q, want %q", a.model, "prov2/y")
	}
	if !strings.HasPrefix(msg, "MODEL:prov2/y") {
		t.Errorf("MODEL: sentinel for bare-id switch = %q, want full normalized ref prov2/y", msg)
	}
}
