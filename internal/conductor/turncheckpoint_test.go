package conductor

import (
	"context"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/pkg/llm"
)

// TestStoreTurnCheckpointRoundTrip (or-mvr.8): the store-backed checkpoint
// survives a save/load cycle with full message fidelity, clears to absent,
// and degrades to a no-op without an active project.
func TestStoreTurnCheckpointRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, err := contextstore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if err := s.WithTx(ctx, func(tx *contextstore.Tx) error {
		pid, e := tx.Projects().Create(ctx, "demo", "build a thing", "http-service")
		if e != nil {
			return e
		}
		_, e = tx.Specs().CreateDraft(ctx, pid)
		return e
	}); err != nil {
		t.Fatal(err)
	}

	cp := storeTurnCheckpoint{store: s}
	convo := []llm.Message{
		llm.TextMessage(llm.RoleUser, "kickoff"),
		{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.BlockToolUse, ToolUse: &llm.ToolUse{ID: "t1", Name: "write_file", Input: []byte(`{"path":"main.go"}`)}}}},
	}
	if _, ok := cp.Load(ctx, "gen:wt1"); ok {
		t.Fatal("no checkpoint yet")
	}
	cp.Save(ctx, "gen:wt1", convo)
	got, ok := cp.Load(ctx, "gen:wt1")
	if !ok || len(got) != 2 {
		t.Fatalf("round trip lost the conversation: ok=%v len=%d", ok, len(got))
	}
	if got[1].Content[0].ToolUse == nil || got[1].Content[0].ToolUse.Name != "write_file" {
		t.Fatalf("tool_use fidelity lost: %+v", got[1])
	}
	cp.Clear(ctx, "gen:wt1")
	if _, ok := cp.Load(ctx, "gen:wt1"); ok {
		t.Fatal("cleared checkpoint must read as absent")
	}

	// No active project: every op degrades to a no-op, never a panic.
	empty, err := contextstore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = empty.Close() }()
	cpEmpty := storeTurnCheckpoint{store: empty}
	cpEmpty.Save(ctx, "k", convo)
	if _, ok := cpEmpty.Load(ctx, "k"); ok {
		t.Fatal("no project → no checkpoint")
	}
}
