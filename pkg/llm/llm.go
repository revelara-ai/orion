// Package llm is Orion's native model-provider abstraction (the pivot to a native
// agent harness — SPEC §0 amendment). A single narrow interface over Anthropic
// (default), Gemini, and Ollama/OpenAI-compatible models. Types normalize on
// Anthropic's content-block shape (the most expressive), so the Anthropic adapter
// is near-identity and lossy translation is isolated in the others.
//
// Trust note: everything a provider emits is GENERATION-tier on ingress. The
// agent loop (internal/harness) is the single audit/interception point — no agent
// grades its own homework; provider output never bypasses the proof firewall.
package llm

import (
	"context"
	"encoding/json"
	"errors"
)

// Role of a conversation message.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// BlockType discriminates a content block.
type BlockType string

const (
	BlockText       BlockType = "text"
	BlockToolUse    BlockType = "tool_use"
	BlockToolResult BlockType = "tool_result"
)

// ContentBlock is one piece of a message (Anthropic-shaped).
type ContentBlock struct {
	Type       BlockType   `json:"type"`
	Text       string      `json:"text,omitempty"`
	ToolUse    *ToolUse    `json:"tool_use,omitempty"`
	ToolResult *ToolResult `json:"tool_result,omitempty"`
}

// ToolUse is the model's request to invoke a tool.
type ToolUse struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
	// Signature is a provider-opaque replay token. Gemini 3.x thinking models
	// attach a thoughtSignature to each functionCall and REQUIRE it echoed
	// when the call is replayed in history — the adapter sets it on parse and
	// sends it back on replay. Other providers neither set nor read it.
	Signature string `json:"signature,omitempty"`
}

// ToolResult is the harness's reply to a ToolUse, fed back to the model.
type ToolResult struct {
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error,omitempty"`
}

// Message is one conversation turn.
type Message struct {
	Role    Role           `json:"role"`
	Content []ContentBlock `json:"content"`
}

// Tool is a function the model may call.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// StopReason is the normalized reason a turn ended. The harness branches on this
// BEFORE reading content (a refusal is an HTTP 200 with empty content).
type StopReason string

const (
	StopEndTurn   StopReason = "end_turn"
	StopToolUse   StopReason = "tool_use" // drives the agent loop
	StopMaxTokens StopReason = "max_tokens"
	StopRefusal   StopReason = "refusal"
	StopUnknown   StopReason = ""
)

// Usage is per-response token accounting (cache fields enable cost tracking).
type Usage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

// ChatRequest is a provider-agnostic request. System is carried separately (the
// adapter places it positionally), never as a Message.
type ChatRequest struct {
	Model     string
	System    string
	Messages  []Message
	Tools     []Tool
	MaxTokens int
}

// ChatResponse is a normalized model response.
type ChatResponse struct {
	Model      string
	Content    []ContentBlock
	StopReason StopReason
	Usage      Usage
}

// ToolUses returns the tool_use blocks (the loop dispatches these).
func (r *ChatResponse) ToolUses() []ToolUse {
	var out []ToolUse
	for _, b := range r.Content {
		if b.Type == BlockToolUse && b.ToolUse != nil {
			out = append(out, *b.ToolUse)
		}
	}
	return out
}

// Text concatenates the text blocks.
func (r *ChatResponse) Text() string {
	var s string
	for _, b := range r.Content {
		if b.Type == BlockText {
			s += b.Text
		}
	}
	return s
}

// ModelInfo is a capability probe result.
type ModelInfo struct {
	ID       string
	Tools    bool
	Vision   bool
	Thinking bool
}

// Provider is the narrow interface every model backend implements.
type Provider interface {
	Name() string
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
	// ChatStream issues a streaming request: onText is called with incremental
	// text as it arrives (for a live UI), and the fully assembled response (same
	// shape as Chat — content blocks incl. tool_use, stop reason, usage) is
	// returned so the agent loop dispatches tools unchanged. onText receives only
	// text deltas; whitespace deltas matter, so callers must not trim them.
	ChatStream(ctx context.Context, req ChatRequest, onText func(string)) (*ChatResponse, error)
	Models(ctx context.Context) ([]ModelInfo, error)
	Ping(ctx context.Context) error
}

// ErrNotSupported lets the harness branch on capability degradation rather than
// hard-failing when a provider lacks a feature.
var ErrNotSupported = errors.New("llm: capability not supported by provider")

// ErrRefused marks content refused by provider policy BEFORE any candidate
// was generated (e.g. a blocked prompt). Distinct from a dependency failure:
// retrying the identical content is futile (or-mvr.15).
var ErrRefused = errors.New("llm: content refused by provider policy")

// TextMessage is a convenience constructor for a plain user/assistant turn.
func TextMessage(role Role, text string) Message {
	return Message{Role: role, Content: []ContentBlock{{Type: BlockText, Text: text}}}
}
