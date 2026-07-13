package conductor

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/acp"
	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/internal/tools"
	"github.com/revelara-ai/orion/pkg/llm"
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

// TestThinkingPreviewShowsContentTail (or-3nv): a subagent's thinking activity
// carries a bounded, single-line preview of the thought — not a content-free
// "thinking".
func TestThinkingPreviewShowsContentTail(t *testing.T) {
	// A short thought is shown in full behind the label.
	if got := thinkingPreview("evaluate the reuse branch"); got != "thinking: evaluate the reuse branch" {
		t.Fatalf("short thought: got %q", got)
	}
	// Newlines/whitespace collapse to a single line.
	if got := thinkingPreview("line one\n\n  line two"); strings.Contains(got, "\n") || !strings.Contains(got, "line one line two") {
		t.Fatalf("preview must be single-line: %q", got)
	}
	// A long thought keeps only the freshest tail, ellipsis-prefixed and bounded.
	long := strings.Repeat("stale ", 40) + "FRESHEST_TAIL"
	got := thinkingPreview(long)
	if !strings.Contains(got, "FRESHEST_TAIL") {
		t.Fatalf("preview must keep the freshest tail: %q", got)
	}
	if r := []rune(got); len(r) > len("thinking: …")+80 {
		t.Fatalf("preview must be bounded, got %d runes: %q", len(r), got)
	}
	// Negative: an empty/whitespace thought falls back to the bare label — never
	// an empty "thinking: " with no content.
	if got := thinkingPreview("   \n  "); got != "thinking" {
		t.Fatalf("empty thought must fall back to bare 'thinking', got %q", got)
	}
}

// TestSubagentThoughtActivityCarriesContent (or-3nv): the subagent's streamed
// thought surfaces as a Depth:1 activity whose text includes the thought
// content, not a generic "thinking".
func TestSubagentThoughtActivityCarriesContent(t *testing.T) {
	ctx := context.Background()
	var got []acp.Update
	r := tools.NewRegistry()
	registerWorkspaceTools(r, orchestrator.NewWithStore(openStore(t)))

	// The subagent streams a distinctive thought, then ends the turn.
	prov := &fakeLLM{resp: []*llm.ChatResponse{
		endTurn("inspecting the reuseartifact path for divergence"),
	}}
	c := orchestrator.NewWithStore(openStore(t))
	registerSubagentTool(r, c, prov, func(u acp.Update) { got = append(got, u) })

	tool, ok := r.Get("spawn_subagent")
	if !ok {
		t.Fatal("spawn_subagent not registered")
	}
	if _, err := tool.Run(ctx, json.RawMessage(`{"task":"inspect","tools":["grep"]}`)); err != nil {
		t.Fatalf("run: %v", err)
	}
	var thoughtText string
	for _, u := range got {
		if u.Kind == acp.ActivityKind && u.Depth == 1 && strings.HasPrefix(u.Text, "thinking") {
			thoughtText = u.Text
		}
	}
	if thoughtText == "" {
		t.Fatalf("no Depth:1 thinking activity surfaced; got %+v", got)
	}
	if !strings.Contains(thoughtText, "reuseartifact") {
		t.Fatalf("thinking activity must carry the thought content, got %q", thoughtText)
	}
}
