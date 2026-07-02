package conductor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/llm"
	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/internal/tools"
)

// TestSpawnSubagentRunsNestedLoop (or-5j1 slice 3): spawn_subagent runs a bounded nested harness
// loop over a scoped toolset, the subagent calls a granted tool, and its final answer + a tool
// trace come back to the Conductor.
func TestSpawnSubagentRunsNestedLoop(t *testing.T) {
	ctx := context.Background()
	c := orchestrator.NewWithStore(openStore(t))
	r := tools.NewRegistry()
	registerWorkspaceTools(r, c)
	registerWebTools(r)

	dir := t.TempDir()
	fp := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(fp, []byte("the answer is 42"), 0o600); err != nil {
		t.Fatal(err)
	}

	// The fake model drives the SUBAGENT's loop: read the file, then answer.
	prov := &fakeLLM{resp: []*llm.ChatResponse{
		tuResp("1", "read_file", `{"path":"`+fp+`"}`),
		endTurn("The file says the answer is 42."),
	}}
	registerSubagentTool(r, c, prov)

	st, ok := r.Get("spawn_subagent")
	if !ok {
		t.Fatal("spawn_subagent should be registered when a provider is present")
	}
	out, err := st.Run(ctx, json.RawMessage(`{"task":"read note.txt and report its contents","tools":["read_file"]}`))
	if err != nil {
		t.Fatalf("spawn_subagent: %v", err)
	}
	if !strings.Contains(out, "answer is 42") {
		t.Errorf("should return the subagent's final answer, got %q", out)
	}
	if !strings.Contains(out, "used: read_file") {
		t.Errorf("should trace the subagent's tool use, got %q", out)
	}
	if !strings.Contains(out, "subagent [read_file]") {
		t.Errorf("should list the granted toolset, got %q", out)
	}
}

// TestSpawnSubagentFailsClosedOnForbiddenTools (or-5j1 slice 3): requesting ONLY forbidden tools
// (git, spawn_subagent itself → recursion, ratify_spec → pipeline) is refused — nothing forbidden
// ever reaches a subagent.
func TestSpawnSubagentFailsClosedOnForbiddenTools(t *testing.T) {
	ctx := context.Background()
	c := orchestrator.NewWithStore(openStore(t))
	r := tools.NewRegistry()
	registerWorkspaceTools(r, c)
	prov := &fakeLLM{resp: []*llm.ChatResponse{endTurn("unused")}}
	registerSubagentTool(r, c, prov)

	st, _ := r.Get("spawn_subagent")
	if _, err := st.Run(ctx, json.RawMessage(`{"task":"escalate","tools":["git","spawn_subagent","ratify_spec"]}`)); err == nil {
		t.Fatal("requesting only forbidden tools must be refused")
	} else if !strings.Contains(err.Error(), "no delegatable tools") {
		t.Errorf("error should explain the refusal, got %v", err)
	}
}

// TestSpawnSubagentDefaultsReadOnlyAndRefusesForbidden (or-5j1 slice 3): with no tools named, the
// subagent gets the read-only research default (no mutators); an explicit allowed+forbidden mix
// grants the allowed and refuses the forbidden.
func TestSpawnSubagentDefaultsReadOnlyAndRefusesForbidden(t *testing.T) {
	ctx := context.Background()
	c := orchestrator.NewWithStore(openStore(t))
	r := tools.NewRegistry()
	registerWorkspaceTools(r, c)
	registerWebTools(r)
	prov := &fakeLLM{resp: []*llm.ChatResponse{endTurn("done — nothing to do")}}
	registerSubagentTool(r, c, prov)
	st, _ := r.Get("spawn_subagent")

	out, err := st.Run(ctx, json.RawMessage(`{"task":"just answer"}`))
	if err != nil {
		t.Fatalf("default spawn: %v", err)
	}
	for _, want := range []string{"read_file", "grep", "glob", "web_fetch", "web_search"} {
		if !strings.Contains(out, want) {
			t.Errorf("default toolset should include %s, got %q", want, out)
		}
	}
	for _, mutator := range []string{"bash", "write_file", "edit_file"} {
		if strings.Contains(out, mutator) {
			t.Errorf("default toolset must be read-only, leaked %s: %q", mutator, out)
		}
	}

	out2, err := st.Run(ctx, json.RawMessage(`{"task":"x","tools":["read_file","git"]}`))
	if err != nil {
		t.Fatalf("mixed spawn: %v", err)
	}
	if !strings.Contains(out2, "subagent [read_file]") {
		t.Errorf("should grant the allowed tool, got %q", out2)
	}
	if !strings.Contains(out2, "refused: git") {
		t.Errorf("should refuse the forbidden tool, got %q", out2)
	}
}

// TestSpawnSubagentAbsentWithoutProvider (or-5j1 slice 3): the deterministic/offline conductor
// (no model) exposes no spawn_subagent — there is no nested loop to run.
func TestSpawnSubagentAbsentWithoutProvider(t *testing.T) {
	c := orchestrator.NewWithStore(openStore(t))
	r := tools.NewRegistry()
	registerSubagentTool(r, c, nil)
	if _, ok := r.Get("spawn_subagent"); ok {
		t.Error("no provider → spawn_subagent must not be registered")
	}
}

// TestSpawnSubagentPrefixToolMustBeReadOnly (or-5j1 slice 3, hardening): a revelara_* tool is
// delegatable ONLY if it is actually read-only — the prefix is a convenience, not a trust grant.
// A read-only revelara_* tool is granted; a non-read-only one is refused even though it matches
// the prefix.
func TestSpawnSubagentPrefixToolMustBeReadOnly(t *testing.T) {
	ctx := context.Background()
	c := orchestrator.NewWithStore(openStore(t))
	r := tools.NewRegistry()
	noop := func(context.Context, json.RawMessage) (string, error) { return "ok", nil }
	r.Register(tools.Tool{Name: "revelara_search_ok", Safety: tools.Safety{ReadOnly: true}, Run: noop})
	r.Register(tools.Tool{Name: "revelara_mutate_evil", Safety: tools.Safety{Destructive: true}, Run: noop})
	prov := &fakeLLM{resp: []*llm.ChatResponse{endTurn("ok")}}
	registerSubagentTool(r, c, prov)

	st, _ := r.Get("spawn_subagent")
	out, err := st.Run(ctx, json.RawMessage(`{"task":"x","tools":["revelara_search_ok","revelara_mutate_evil"]}`))
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if !strings.Contains(out, "subagent [revelara_search_ok]") {
		t.Errorf("read-only revelara_* tool should be granted, got %q", out)
	}
	if !strings.Contains(out, "revelara_mutate_evil(not-read-only)") {
		t.Errorf("non-read-only revelara_* tool must be refused, got %q", out)
	}
}

// TestSpawnSubagentMarksIncompleteOnEarlyStop (or-5j1 slice 3, hardening): when the nested loop
// stops early (here: max iterations, because the scripted model never ends the turn) but produced
// partial assistant text, the result is surfaced with an unmistakable INCOMPLETE marker — never
// silently as a clean success.
func TestSpawnSubagentMarksIncompleteOnEarlyStop(t *testing.T) {
	ctx := context.Background()
	c := orchestrator.NewWithStore(openStore(t))
	r := tools.NewRegistry()
	registerWorkspaceTools(r, c)

	dir := t.TempDir()
	fp := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(fp, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Every turn emits partial text AND a tool call, never end_turn → the loop exhausts
	// MaxIterations and returns ErrMaxIterations with partial assistant text captured.
	partial := &llm.ChatResponse{StopReason: llm.StopToolUse, Content: []llm.ContentBlock{
		{Type: llm.BlockText, Text: "still working..."},
		{Type: llm.BlockToolUse, ToolUse: &llm.ToolUse{ID: "1", Name: "read_file", Input: json.RawMessage(`{"path":"` + fp + `"}`)}},
	}}
	prov := &fakeLLM{resp: []*llm.ChatResponse{partial}}
	registerSubagentTool(r, c, prov)

	st, _ := r.Get("spawn_subagent")
	out, err := st.Run(ctx, json.RawMessage(`{"task":"loop forever","tools":["read_file"]}`))
	if err != nil {
		t.Fatalf("partial run should return a result, not a hard error: %v", err)
	}
	if !strings.Contains(out, "INCOMPLETE") {
		t.Errorf("early stop must be marked INCOMPLETE, got %q", out)
	}
	if !strings.Contains(out, "stopped early") {
		t.Errorf("early stop must state why, got %q", out)
	}
	if !strings.Contains(out, "still working") {
		t.Errorf("partial answer should still be surfaced, got %q", out)
	}
}
