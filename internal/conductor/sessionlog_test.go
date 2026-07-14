package conductor

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/pkg/llm"
)

func logAgent(t *testing.T) (*OrionAgent, string) {
	t.Helper()
	c := orchestrator.NewWithStore(openStore(t))
	a := NewOrionAgent(nil, c, RoleTemplate{Project: "demo"})
	return a, filepath.Join(c.Store().Dir(), "sessions")
}

// convo builds a small history with a tool_use (carrying an ID) and its
// tool_result (carrying the matching tool_use_id) — the exact fidelity the
// markdown transcript drops.
func convo(userText string) []llm.Message {
	return []llm.Message{
		llm.TextMessage(llm.RoleUser, userText),
		{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
			{Type: llm.BlockText, Text: "running a command"},
			{Type: llm.BlockToolUse, ToolUse: &llm.ToolUse{ID: "tu_1", Name: "bash", Input: json.RawMessage(`{"command":"ls -la"}`), Signature: "sig-abc"}},
		}},
		{Role: llm.RoleUser, Content: []llm.ContentBlock{
			{Type: llm.BlockToolResult, ToolResult: &llm.ToolResult{ToolUseID: "tu_1", Content: "file1\nfile2"}},
		}},
	}
}

// TestSessionLogRoundTrip (or-8my7 S1): a persisted session reloads as a faithful
// []llm.Message — tool_use IDs, tool_use_id linkage, and the replay signature all
// survive (the markdown transcript cannot do this).
func TestSessionLogRoundTrip(t *testing.T) {
	a, dir := logAgent(t)
	sid := "sess-1"
	msgs := convo("build me a thing")
	a.sessions[sid] = msgs
	a.persistSession(sid, msgs)

	loaded, err := loadSessionHistory(dir, a.sessionStamp(sid))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !reflect.DeepEqual(loaded, msgs) {
		t.Fatalf("reload must reproduce the history faithfully.\n got: %+v\nwant: %+v", loaded, msgs)
	}
	// Spot-check the pieces the markdown drops.
	tu := loaded[1].Content[1].ToolUse
	if tu == nil || tu.ID != "tu_1" || tu.Signature != "sig-abc" {
		t.Fatalf("tool_use ID + signature must survive: %+v", tu)
	}
	if tr := loaded[2].Content[0].ToolResult; tr == nil || tr.ToolUseID != "tu_1" {
		t.Fatalf("tool_result must keep its tool_use_id linkage: %+v", tr)
	}
}

// TestSessionLogAppendsAcrossTurns (or-8my7 S1): the log is append-only across
// turns — a second turn adds only its new messages, and the full history reloads.
func TestSessionLogAppendsAcrossTurns(t *testing.T) {
	a, dir := logAgent(t)
	sid := "sess-2"

	first := convo("turn one")
	a.persistSession(sid, first)

	second := append(append([]llm.Message{}, first...), llm.TextMessage(llm.RoleUser, "turn two"))
	a.persistSession(sid, second)

	loaded, err := loadSessionHistory(dir, a.sessionStamp(sid))
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != len(second) {
		t.Fatalf("the log must hold every turn's messages: got %d want %d", len(loaded), len(second))
	}
	if !reflect.DeepEqual(loaded, second) {
		t.Fatalf("append must preserve order + content across turns")
	}
}

// TestSessionLogRewritesOnCompaction (or-8my7 S1): when compaction REPLACES the
// history with a shorter summary, the log is rewritten to match — it must not
// keep appending stale pre-compaction messages.
func TestSessionLogRewritesOnCompaction(t *testing.T) {
	a, dir := logAgent(t)
	sid := "sess-3"

	full := convo("a long conversation")
	a.persistSession(sid, full)

	// Compaction collapses it to a single summary message.
	summary := []llm.Message{llm.TextMessage(llm.RoleUser, "[summary of the conversation so far]")}
	a.sessions[sid] = summary
	a.persistSession(sid, summary)

	loaded, err := loadSessionHistory(dir, a.sessionStamp(sid))
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].Content[0].Text != "[summary of the conversation so far]" {
		t.Fatalf("after compaction the log must reflect the compacted state, got %d msgs: %+v", len(loaded), loaded)
	}
}
