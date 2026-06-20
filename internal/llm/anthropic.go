package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/revelara-ai/orion/internal/llmclient"
)

// DefaultAnthropicModel is the default model id (overridable at setup).
const DefaultAnthropicModel = "claude-opus-4-8"

const anthropicVersion = "2023-06-01"

// Anthropic is the Anthropic Messages API provider, hand-rolled over HTTP (no
// vendor SDK) and wrapped per-request in llmclient.Do for retry/backoff/breaker.
// The credential is held only in memory and never logged.
type Anthropic struct {
	apiKey  string
	model   string
	baseURL string
	http    *http.Client
	rc      *llmclient.Client
}

// NewAnthropic builds the provider. apiKey comes from the environment
// (ANTHROPIC_API_KEY); Orion never persists it.
func NewAnthropic(apiKey, model string) *Anthropic {
	if model == "" {
		model = DefaultAnthropicModel
	}
	return &Anthropic{
		apiKey:  apiKey,
		model:   model,
		baseURL: "https://api.anthropic.com",
		// No client-level Timeout: the per-attempt deadline is governed by llmclient
		// (propagated via NewRequestWithContext), so the two never fight.
		http: &http.Client{},
		// A zero Config disabled retries (MaxRetries 0) and capped each attempt at
		// 30s — far too short for a model turn, so a slow completion surfaced as
		// "context deadline exceeded" with no retry. Configure real policy: a
		// generous per-attempt timeout + exponential backoff on transient failures
		// (timeouts, 5xx, 429/529 Retry-After), with the breaker as a backstop.
		// MaxRetries 3 (4 attempts) stays under the breaker's 5-failure threshold, so
		// a single fully-failed turn doesn't trip the circuit for the next one.
		rc: llmclient.New(llmclient.Config{
			Timeout:     3 * time.Minute,
			MaxRetries:  3,
			BaseBackoff: 500 * time.Millisecond,
			MaxBackoff:  10 * time.Second,
		}),
	}
}

func (a *Anthropic) Name() string { return "anthropic" }

// Models returns the configured model as tool-capable (Opus/Sonnet 4.x).
func (a *Anthropic) Models(context.Context) ([]ModelInfo, error) {
	return []ModelInfo{{ID: a.model, Tools: true, Vision: true, Thinking: true}}, nil
}

// Ping verifies a credential is present (a cheap, network-free liveness check;
// a real call happens on the first Chat).
func (a *Anthropic) Ping(context.Context) error {
	if a.apiKey == "" {
		return fmt.Errorf("anthropic: no API key (set ANTHROPIC_API_KEY)")
	}
	return nil
}

// Chat issues one Messages API request, retried/broken per llmclient policy.
func (a *Anthropic) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	model := req.Model
	if model == "" {
		model = a.model
	}
	maxTok := req.MaxTokens
	if maxTok <= 0 {
		maxTok = 4096
	}
	body, err := json.Marshal(a.toWire(req, model, maxTok))
	if err != nil {
		return nil, fmt.Errorf("anthropic: marshal request: %w", err)
	}
	return llmclient.Do(ctx, a.rc, func(ctx context.Context) (*ChatResponse, error) {
		return a.do(ctx, body)
	})
}

// ── wire types (Anthropic Messages API shape) ────────────────────────────────

type wireRequest struct {
	Model     string        `json:"model"`
	MaxTokens int           `json:"max_tokens"`
	System    string        `json:"system,omitempty"`
	Messages  []wireMessage `json:"messages"`
	Tools     []wireTool    `json:"tools,omitempty"`
	Stream    bool          `json:"stream,omitempty"`
}
type wireMessage struct {
	Role    string      `json:"role"`
	Content []wireBlock `json:"content"`
}
type wireBlock struct {
	Type string `json:"type"`
	// text
	Text string `json:"text,omitempty"`
	// tool_use
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	// tool_result
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   any    `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
}
type wireTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}
type wireResponse struct {
	Model      string `json:"model"`
	StopReason string `json:"stop_reason"`
	Content    []struct {
		Type  string          `json:"type"`
		Text  string          `json:"text"`
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	} `json:"content"`
	Usage struct {
		InputTokens              int `json:"input_tokens"`
		OutputTokens             int `json:"output_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	} `json:"usage"`
}

func (a *Anthropic) toWire(req ChatRequest, model string, maxTok int) wireRequest {
	w := wireRequest{Model: model, MaxTokens: maxTok, System: req.System}
	for _, m := range req.Messages {
		wm := wireMessage{Role: string(m.Role)}
		for _, b := range m.Content {
			switch b.Type {
			case BlockText:
				wm.Content = append(wm.Content, wireBlock{Type: "text", Text: b.Text})
			case BlockToolUse:
				if b.ToolUse != nil {
					wm.Content = append(wm.Content, wireBlock{Type: "tool_use", ID: b.ToolUse.ID, Name: b.ToolUse.Name, Input: b.ToolUse.Input})
				}
			case BlockToolResult:
				if b.ToolResult != nil {
					wm.Content = append(wm.Content, wireBlock{Type: "tool_result", ToolUseID: b.ToolResult.ToolUseID, Content: b.ToolResult.Content, IsError: b.ToolResult.IsError})
				}
			}
		}
		w.Messages = append(w.Messages, wm)
	}
	for _, t := range req.Tools {
		w.Tools = append(w.Tools, wireTool{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema})
	}
	return w
}

func (a *Anthropic) do(ctx context.Context, body []byte) (*ChatResponse, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("x-api-key", a.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)

	resp, err := a.http.Do(httpReq)
	if err != nil {
		return nil, &llmclient.Retryable{Err: err} // network blip → retry
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))

	switch {
	case resp.StatusCode == 429 || resp.StatusCode == 529:
		return nil, &llmclient.RetryAfter{After: parseRetryAfter(resp.Header), Err: fmt.Errorf("anthropic: status %d", resp.StatusCode)}
	case resp.StatusCode >= 500:
		return nil, &llmclient.Retryable{Err: fmt.Errorf("anthropic: status %d", resp.StatusCode)}
	case resp.StatusCode != 200:
		return nil, fmt.Errorf("anthropic: status %d: %s", resp.StatusCode, truncate(string(rb), 300))
	}

	var wr wireResponse
	if err := json.Unmarshal(rb, &wr); err != nil {
		return nil, fmt.Errorf("anthropic: decode response: %w", err)
	}
	out := &ChatResponse{
		Model:      wr.Model,
		StopReason: normalizeStop(wr.StopReason),
		Usage: Usage{
			InputTokens: wr.Usage.InputTokens, OutputTokens: wr.Usage.OutputTokens,
			CacheReadInputTokens: wr.Usage.CacheReadInputTokens, CacheCreationInputTokens: wr.Usage.CacheCreationInputTokens,
		},
	}
	for _, c := range wr.Content {
		switch c.Type {
		case "text":
			out.Content = append(out.Content, ContentBlock{Type: BlockText, Text: c.Text})
		case "tool_use":
			out.Content = append(out.Content, ContentBlock{Type: BlockToolUse, ToolUse: &ToolUse{ID: c.ID, Name: c.Name, Input: c.Input}})
		}
	}
	return out, nil
}

func normalizeStop(s string) StopReason {
	switch s {
	case "end_turn":
		return StopEndTurn
	case "tool_use":
		return StopToolUse
	case "max_tokens":
		return StopMaxTokens
	case "refusal":
		return StopRefusal
	default:
		return StopUnknown
	}
}

func parseRetryAfter(h http.Header) time.Duration {
	if v := h.Get("retry-after"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil {
			return time.Duration(secs) * time.Second
		}
	}
	return 2 * time.Second
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
