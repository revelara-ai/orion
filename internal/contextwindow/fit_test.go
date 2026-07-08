package contextwindow

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/pkg/llm"
)

// bigResult is a user message carrying one bulky tool_result body.
func bigResult(id string, n int) llm.Message {
	return llm.Message{Role: llm.RoleUser, Content: []llm.ContentBlock{{
		Type:       llm.BlockToolResult,
		ToolResult: &llm.ToolResult{ToolUseID: id, Content: strings.Repeat("x", n)},
	}}}
}

// toolUse is an assistant message requesting a tool (small; must survive Fit).
func toolUse(id, name string) llm.Message {
	return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{
		Type:    llm.BlockToolUse,
		ToolUse: &llm.ToolUse{ID: id, Name: name, Input: json.RawMessage(`{}`)},
	}}}
}

// TestFitClearsOldestToolResultsUntilUnderTarget: Fit replaces the oldest
// tool_result BODIES with a re-fetch pointer (keeping the last `keepRecent`
// verbatim) until the estimate is under target — the cheap, near-lossless lever
// (Orion's tool outputs are reconstructable from disk).
func TestFitClearsOldestToolResultsUntilUnderTarget(t *testing.T) {
	convo := []llm.Message{
		llm.TextMessage(llm.RoleUser, "build a time service"),
		toolUse("t1", "read_file"), bigResult("t1", 4000), // ~1000 tok
		toolUse("t2", "read_file"), bigResult("t2", 4000),
		toolUse("t3", "read_file"), bigResult("t3", 4000),
		toolUse("t4", "read_file"), bigResult("t4", 4000),
	}
	req := llm.ChatRequest{Messages: convo}
	before := llm.EstimateTokens(req)

	got := Fit(req, 1500, 1) // keep only the most recent tool result verbatim

	if est := llm.EstimateTokens(llm.ChatRequest{Messages: got}); est > 1500 {
		t.Fatalf("Fit left estimate at %d, want <= 1500 (was %d)", est, before)
	}
	// Newest result kept verbatim; oldest cleared to the marker.
	if got[8].Content[0].ToolResult.Content != strings.Repeat("x", 4000) {
		t.Errorf("most-recent tool_result was cleared but should be kept verbatim")
	}
	if got[2].Content[0].ToolResult.Content != ClearedMarker {
		t.Errorf("oldest tool_result body = %q, want ClearedMarker", got[2].Content[0].ToolResult.Content)
	}
	// Call records and text survive: the model keeps its plan and tool history.
	if got[1].Content[0].Type != llm.BlockToolUse || got[1].Content[0].ToolUse.Name != "read_file" {
		t.Errorf("tool_use call record was damaged: %+v", got[1])
	}
	if got[0].Content[0].Text != "build a time service" {
		t.Errorf("text turn was damaged: %+v", got[0])
	}
	// Purity: the caller's original slice is untouched.
	if convo[2].Content[0].ToolResult.Content != strings.Repeat("x", 4000) {
		t.Errorf("Fit mutated the caller's messages (broke purity)")
	}
}

// TestFitNoOpUnderTarget: a conversation already under target is returned as-is.
func TestFitNoOpUnderTarget(t *testing.T) {
	convo := []llm.Message{llm.TextMessage(llm.RoleUser, "hi"), bigResult("t1", 40)}
	got := Fit(llm.ChatRequest{Messages: convo}, 100_000, 3)
	if got[1].Content[0].ToolResult.Content != strings.Repeat("x", 40) {
		t.Fatalf("Fit cleared a result while under target")
	}
}

// TestFitStopsWhenNothingLeftToClear: a transcript that is all text (nothing
// re-fetchable) and over target must not loop forever or panic — mechanical
// clearing simply can't help (that's compaction's job, a later slice).
func TestFitStopsWhenNothingLeftToClear(t *testing.T) {
	convo := []llm.Message{llm.TextMessage(llm.RoleUser, strings.Repeat("word ", 5000))}
	got := Fit(llm.ChatRequest{Messages: convo}, 100, 0)
	if len(got) != 1 || got[0].Content[0].Text == ClearedMarker {
		t.Fatalf("Fit wrongly altered text content: %+v", got)
	}
}
