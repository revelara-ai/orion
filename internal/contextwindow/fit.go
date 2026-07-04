package contextwindow

import "github.com/revelara-ai/orion/internal/llm"

// ClearedMarker replaces a bulky tool_result body that was dropped to fit the
// context window. It preserves the tool_use/tool_result PAIRING (the API requires
// each tool_use have a matching result) while shedding the payload — near-lossless
// for Orion because its tool outputs (file reads, shell) are reconstructable from
// disk. The model can re-run the tool or re-read the file if it needs the detail.
const ClearedMarker = "[tool result cleared to fit the context window — re-run the tool or re-read the file to recover]"

// Fit shrinks a conversation to fit `target` estimated input tokens by replacing
// the OLDEST tool_result bodies with ClearedMarker, keeping the `keepRecent` most
// recent results verbatim. It never touches text or tool_use blocks (the model's
// reasoning and call history survive). It is deterministic, allocates a private
// copy (never mutates the caller's messages), and stops when it reaches target or
// runs out of clearable results — dialogue growth beyond that is compaction's job.
func Fit(req llm.ChatRequest, target int, keepRecent int) []llm.Message {
	if llm.EstimateTokens(req) <= target {
		return req.Messages
	}
	msgs := cloneMessages(req.Messages)

	// Every tool_result block, in oldest→newest order.
	type ref struct{ m, b int }
	var results []ref
	for mi := range msgs {
		for bi := range msgs[mi].Content {
			if msgs[mi].Content[bi].Type == llm.BlockToolResult && msgs[mi].Content[bi].ToolResult != nil {
				results = append(results, ref{mi, bi})
			}
		}
	}

	// Protect the last keepRecent results; clear the rest oldest-first.
	clearable := results
	switch {
	case keepRecent >= len(results):
		clearable = nil
	case keepRecent > 0:
		clearable = results[:len(results)-keepRecent]
	}
	for _, r := range clearable {
		tr := msgs[r.m].Content[r.b].ToolResult
		if tr.Content == ClearedMarker || len(tr.Content) <= len(ClearedMarker) {
			continue // already cleared, or too small to be worth clearing
		}
		tr.Content = ClearedMarker
		if llm.EstimateTokens(llm.ChatRequest{System: req.System, Messages: msgs, Tools: req.Tools}) <= target {
			break
		}
	}
	return msgs
}

// cloneMessages deep-copies the message slice, its content blocks, and the
// tool_use/tool_result pointers so Fit can mutate freely without touching the
// caller's data.
func cloneMessages(in []llm.Message) []llm.Message {
	out := make([]llm.Message, len(in))
	for i, m := range in {
		c := make([]llm.ContentBlock, len(m.Content))
		for j, b := range m.Content {
			nb := b
			if b.ToolResult != nil {
				tr := *b.ToolResult
				nb.ToolResult = &tr
			}
			if b.ToolUse != nil {
				tu := *b.ToolUse
				nb.ToolUse = &tu
			}
			c[j] = nb
		}
		out[i] = llm.Message{Role: m.Role, Content: c}
	}
	return out
}
