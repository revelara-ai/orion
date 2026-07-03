package conductor

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/revelara-ai/orion/internal/acp"
	"github.com/revelara-ai/orion/internal/llm"
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
	var sawDepth1 bool
	for _, u := range got {
		if u.Kind == acp.ActivityKind && u.Depth == 1 {
			sawDepth1 = true
		}
	}
	if !sawDepth1 {
		t.Fatalf("no Depth:1 subagent activity surfaced; got %d updates: %+v", len(got), got)
	}
}
