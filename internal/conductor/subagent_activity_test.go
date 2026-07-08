package conductor

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/revelara-ai/orion/internal/acp"
	"github.com/revelara-ai/orion/pkg/llm"
	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/internal/tools"
)

// TestSubagentSurfacesInnerActivity: a spawned subagent's inner tool calls must
// surface as Depth:1 activity updates — they are no longer swallowed into the
// tool's local trace (the load-bearing gap).
func TestSubagentSurfacesInnerActivity(t *testing.T) {
	ctx := context.Background()
	var got []acp.Update
	r := tools.NewRegistry()
	registerWorkspaceTools(r, orchestrator.NewWithStore(openStore(t)))

	// Script the subagent's nested loop: one grep tool call then end.
	grepInput, _ := json.Marshal(map[string]string{"pattern": "X", "path": "."})
	prov := &fakeLLM{resp: []*llm.ChatResponse{
		tuResp("1", "grep", string(grepInput)),
		endTurn("found nothing"),
	}}

	c := orchestrator.NewWithStore(openStore(t))
	registerSubagentTool(r, c, prov, func(u acp.Update) { got = append(got, u) })

	tool, ok := r.Get("spawn_subagent")
	if !ok {
		t.Fatal("spawn_subagent not registered")
	}
	if _, err := tool.Run(ctx, json.RawMessage(`{"task":"grep for X","tools":["grep"]}`)); err != nil {
		t.Fatalf("run: %v", err)
	}
	var running *acp.Update
	for i := range got {
		if got[i].Kind == acp.ActivityKind && got[i].Depth == 1 && got[i].Status == "running" {
			running = &got[i]
			break
		}
	}
	if running == nil {
		t.Fatalf("no Depth:1 running subagent activity surfaced; got %d updates: %+v", len(got), got)
	}
	// The update must carry the inner tool name and a non-empty subagent label —
	// a structurally-correct-but-content-wrong emit would otherwise pass.
	if running.Text != "grep" {
		t.Fatalf("Depth:1 activity should name the inner tool 'grep', got %q", running.Text)
	}
	if running.Actor == "" {
		t.Fatalf("Depth:1 activity must carry a subagent label (Actor), got empty")
	}
}
