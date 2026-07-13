package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/tools"
	"github.com/revelara-ai/orion/pkg/llm"
)

// mapCheckpoint is an in-memory TurnCheckpoint.
type mapCheckpoint struct{ m map[string][]llm.Message }

func (c *mapCheckpoint) Load(_ context.Context, k string) ([]llm.Message, bool) {
	v, ok := c.m[k]
	return v, ok
}
func (c *mapCheckpoint) Save(_ context.Context, k string, convo []llm.Message) { c.m[k] = convo }
func (c *mapCheckpoint) Clear(_ context.Context, k string)                     { delete(c.m, k) }

// outageProvider runs a script until failAt, then errors every call until
// restored — a provider-wide outage mid-turn.
type outageProvider struct {
	scriptedProvider
	calls2 int
	failAt int // fail when calls2 == failAt (1-based); 0 = never
}

func (p *outageProvider) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	p.calls2++
	if p.failAt > 0 && p.calls2 >= p.failAt {
		return nil, errors.New("529 overloaded (provider outage)")
	}
	return p.scriptedProvider.Chat(ctx, req)
}
func (p *outageProvider) ChatStream(ctx context.Context, req llm.ChatRequest, onText func(string)) (*llm.ChatResponse, error) {
	return p.Chat(ctx, req)
}

// TestProviderOutageCheckpointResume (or-mvr.8 acceptance, turn half): a
// provider outage mid-turn persists the half-generated conversation; after
// the provider recovers, the next Run under the same key RESUMES from the
// checkpoint (its executed tool results intact) instead of starting over,
// and a completed turn clears the checkpoint.
func TestProviderOutageCheckpointResume(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{
		Name: "probe", Description: "p", InputSchema: json.RawMessage(`{"type":"object"}`),
		Safety: tools.Safety{ReadOnly: true},
		Run:    func(context.Context, json.RawMessage) (string, error) { return "probe-result-42", nil },
	})
	cp := &mapCheckpoint{m: map[string][]llm.Message{}}
	kickoff := []llm.Message{llm.TextMessage(llm.RoleUser, "do the work")}

	// Turn 1: one successful tool call, then the provider dies.
	p1 := &outageProvider{failAt: 2}
	p1.resp = []*llm.ChatResponse{toolUseResp("id1", "probe", `{}`)}
	l1 := &Loop{Provider: p1, Tools: reg, System: "t",
		Supervisor: Supervisor{MaxIterations: 8}, Checkpoint: cp, CheckpointKey: "gen:wt1"}
	_, _, err := l1.Run(context.Background(), kickoff, nil)
	if !errors.Is(err, ErrProvider) {
		t.Fatalf("the outage must surface as ErrProvider, got %v", err)
	}
	saved, ok := cp.m["gen:wt1"]
	if !ok || len(saved) < 3 {
		t.Fatalf("the half-generated turn must be checkpointed (kickoff+assistant+tool result), got %d messages ok=%v", len(saved), ok)
	}

	// Provider restored. Turn 2 resumes FROM THE CHECKPOINT, not the kickoff.
	p2 := &outageProvider{}
	p2.resp = []*llm.ChatResponse{endResp("all done")}
	l2 := &Loop{Provider: p2, Tools: reg, System: "t",
		Supervisor: Supervisor{MaxIterations: 8}, Checkpoint: cp, CheckpointKey: "gen:wt1"}
	_, final, err := l2.Run(context.Background(), kickoff, nil)
	if err != nil || final == nil {
		t.Fatalf("restored run must complete: %v", err)
	}
	got := p2.lastReq.Messages
	if len(got) < 3 {
		t.Fatalf("the restored turn must carry the checkpointed conversation, got %d messages", len(got))
	}
	var sawResult bool
	for _, m := range got {
		for _, b := range m.Content {
			if b.ToolResult != nil && strings.Contains(b.ToolResult.Content, "probe-result-42") {
				sawResult = true
			}
		}
	}
	if !sawResult {
		t.Fatal("the pre-outage tool result must survive into the resumed turn")
	}
	if _, ok := cp.m["gen:wt1"]; ok {
		t.Fatal("a completed turn must clear its checkpoint")
	}

	// A key with no checkpoint runs the kickoff untouched.
	p3 := &outageProvider{}
	p3.resp = []*llm.ChatResponse{endResp("fresh")}
	l3 := &Loop{Provider: p3, Tools: reg, System: "t",
		Supervisor: Supervisor{MaxIterations: 8}, Checkpoint: cp, CheckpointKey: "gen:other"}
	if _, _, err := l3.Run(context.Background(), kickoff, nil); err != nil {
		t.Fatal(err)
	}
	if n := len(p3.lastReq.Messages); n != 1 {
		t.Fatalf("no checkpoint → the kickoff alone, got %d messages", n)
	}
}

// TestNonProviderStopsNeverCheckpoint (or-mvr.8): a stall/cap stop is the
// turn's own verdict — it must NOT save a checkpoint (replaying it would
// re-run the same doomed turn).
func TestNonProviderStopsNeverCheckpoint(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{
		Name: "probe", Description: "p", InputSchema: json.RawMessage(`{"type":"object"}`),
		Safety: tools.Safety{ReadOnly: true},
		Run:    func(context.Context, json.RawMessage) (string, error) { return "ok", nil },
	})
	cp := &mapCheckpoint{m: map[string][]llm.Message{}}
	n := 0
	p := &scriptedProvider{next: func() *llm.ChatResponse {
		n++
		return toolUseResp(fmt.Sprintf("id%d", n), "probe", fmt.Sprintf(`{"n":%d}`, n))
	}}
	l := &Loop{Provider: p, Tools: reg, System: "t",
		Supervisor: Supervisor{MaxIterations: 2}, Checkpoint: cp, CheckpointKey: "gen:cap"}
	_, _, err := l.Run(context.Background(), []llm.Message{llm.TextMessage(llm.RoleUser, "go")}, nil)
	if !errors.Is(err, ErrMaxIterations) {
		t.Fatalf("expected the cap, got %v", err)
	}
	if len(cp.m) != 0 {
		t.Fatal("a non-provider stop must not checkpoint")
	}
}
