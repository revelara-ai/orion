package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/revelara-ai/orion/pkg/llmclient"
)

// OpenAIConfig configures an OpenAI-compatible chat-completions provider
// (LM Studio, Ollama /v1, OpenAI, OpenRouter, vLLM, Groq, …). BaseURL is
// required and includes the /v1 suffix (e.g. http://localhost:1234/v1).
// APIKey is optional — local servers don't need one. ContextWindow/MaxOutput
// come from config because local endpoints don't advertise them; 0 = unknown
// (the harness and context manager fall back conservatively).
type OpenAIConfig struct {
	Name          string // registry/display name; default "openai"
	BaseURL       string // required
	APIKey        string
	Model         string
	ContextWindow int
	MaxOutput     int
}

// OpenAI is the OpenAI-compatible provider, hand-rolled over HTTP like the
// Anthropic adapter and wrapped per-request in llmclient.Do. Lossy translation
// (content blocks ↔ tool_calls/tool-role messages) is isolated here.
type OpenAI struct {
	cfg  OpenAIConfig
	http *http.Client
	rc   *llmclient.Client
}

// NewOpenAI builds the provider. The credential is held only in memory.
func NewOpenAI(cfg OpenAIConfig) *OpenAI {
	if cfg.Name == "" {
		cfg.Name = "openai"
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &OpenAI{
		cfg:  cfg,
		http: &http.Client{},
		// Same policy as the Anthropic adapter: generous per-attempt timeout,
		// retries under the breaker threshold.
		rc: llmclient.New(llmclient.Config{
			Timeout:     3 * time.Minute,
			MaxRetries:  3,
			BaseBackoff: 500 * time.Millisecond,
			MaxBackoff:  10 * time.Second,
		}),
	}
}

// Name identifies the provider.
func (o *OpenAI) Name() string { return o.cfg.Name }

// ContextWindow / MaxOutputTokens implement the optional capabilities from
// config values (0 = unknown → callers fall back).
func (o *OpenAI) ContextWindow() int { return o.cfg.ContextWindow }

// MaxOutputTokens is the configured per-response output cap.
func (o *OpenAI) MaxOutputTokens() int { return o.cfg.MaxOutput }

// ── wire types (OpenAI chat-completions shape) ───────────────────────────────

type oaRequest struct {
	Model         string        `json:"model"`
	Messages      []oaMessage   `json:"messages"`
	Tools         []oaTool      `json:"tools,omitempty"`
	MaxTokens     int           `json:"max_tokens,omitempty"`
	Stream        bool          `json:"stream,omitempty"`
	StreamOptions *oaStreamOpts `json:"stream_options,omitempty"`
}

type oaStreamOpts struct {
	IncludeUsage bool `json:"include_usage"`
}

type oaMessage struct {
	Role       string       `json:"role"`
	Content    string       `json:"content,omitempty"`
	ToolCalls  []oaToolCall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
}

type oaToolCall struct {
	ID       string     `json:"id,omitempty"`
	Type     string     `json:"type"`
	Function oaFunction `json:"function"`
}

type oaFunction struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"` // JSON object, encoded as a string
}

type oaTool struct {
	Type     string    `json:"type"`
	Function oaToolDef `json:"function"`
}

type oaToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type oaUsage struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	PromptTokensDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

type oaResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content   string       `json:"content"`
			ToolCalls []oaToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage oaUsage `json:"usage"`
}

// toWire translates the provider-agnostic request into OpenAI messages. One
// llm.Message can fan out into several wire messages: tool_result blocks each
// become a role:"tool" message (which must directly follow the assistant
// tool_calls turn), remaining user text becomes a role:"user" message.
func (o *OpenAI) toWire(req ChatRequest, model string) oaRequest {
	w := oaRequest{Model: model, MaxTokens: req.MaxTokens}
	if req.System != "" {
		w.Messages = append(w.Messages, oaMessage{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		switch m.Role {
		case RoleAssistant:
			am := oaMessage{Role: "assistant"}
			for _, b := range m.Content {
				switch b.Type {
				case BlockText:
					am.Content += b.Text
				case BlockToolUse:
					if b.ToolUse != nil {
						am.ToolCalls = append(am.ToolCalls, oaToolCall{
							ID: b.ToolUse.ID, Type: "function",
							Function: oaFunction{Name: b.ToolUse.Name, Arguments: string(b.ToolUse.Input)},
						})
					}
				}
			}
			if am.Content != "" || len(am.ToolCalls) > 0 {
				w.Messages = append(w.Messages, am)
			}
		case RoleUser:
			var text string
			for _, b := range m.Content {
				switch b.Type {
				case BlockText:
					text += b.Text
				case BlockToolResult:
					if b.ToolResult != nil {
						content := b.ToolResult.Content
						if b.ToolResult.IsError {
							// OpenAI has no is_error flag; prefix so the model sees the failure.
							content = "ERROR: " + content
						}
						w.Messages = append(w.Messages, oaMessage{Role: "tool", ToolCallID: b.ToolResult.ToolUseID, Content: content})
					}
				}
			}
			if text != "" {
				w.Messages = append(w.Messages, oaMessage{Role: "user", Content: text})
			}
		}
	}
	for _, t := range req.Tools {
		w.Tools = append(w.Tools, oaTool{Type: "function", Function: oaToolDef{Name: t.Name, Description: t.Description, Parameters: normalizeToolSchema(t.InputSchema)}})
	}
	return w
}

// normalizeToolSchema guarantees a tool's parameters are a JSON-schema object
// carrying "type" and "properties". OpenAI itself tolerates their absence, but
// strict OpenAI-compatible servers (LM Studio's zod validation) reject the
// whole request with a 400 when any tool omits properties — and Orion's no-arg
// tools default to {"type":"object"}. Unparseable schemas pass through so the
// server reports them with context.
func normalizeToolSchema(schema json.RawMessage) json.RawMessage {
	if len(schema) == 0 {
		return json.RawMessage(`{"type":"object","properties":{}}`)
	}
	var m map[string]any
	if err := json.Unmarshal(schema, &m); err != nil || m == nil {
		return schema
	}
	if _, ok := m["type"]; !ok {
		m["type"] = "object"
	}
	if _, ok := m["properties"]; !ok {
		m["properties"] = map[string]any{}
	}
	out, err := json.Marshal(m)
	if err != nil {
		return schema
	}
	return out
}

// Chat issues one chat-completions request, retried/broken per llmclient policy.
func (o *OpenAI) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	model := req.Model
	if model == "" {
		model = o.cfg.Model
	}
	body, err := json.Marshal(o.toWire(req, model))
	if err != nil {
		return nil, fmt.Errorf("%s: marshal request: %w", o.cfg.Name, err)
	}
	return llmclient.Do(ctx, o.rc, func(ctx context.Context) (*ChatResponse, error) {
		return o.do(ctx, body)
	})
}

func (o *OpenAI) do(ctx context.Context, body []byte) (*ChatResponse, error) {
	rb, err := o.post(ctx, "/chat/completions", body)
	if err != nil {
		return nil, err
	}
	var wr oaResponse
	if err := json.Unmarshal(rb, &wr); err != nil {
		return nil, fmt.Errorf("%s: decode response: %w", o.cfg.Name, err)
	}
	if len(wr.Choices) == 0 {
		return nil, fmt.Errorf("%s: response has no choices", o.cfg.Name)
	}
	ch := wr.Choices[0]
	out := &ChatResponse{
		Model:      wr.Model,
		StopReason: oaStop(ch.FinishReason, len(ch.Message.ToolCalls) > 0),
		Usage: Usage{
			InputTokens:          wr.Usage.PromptTokens,
			OutputTokens:         wr.Usage.CompletionTokens,
			CacheReadInputTokens: wr.Usage.PromptTokensDetails.CachedTokens,
		},
	}
	if ch.Message.Content != "" {
		out.Content = append(out.Content, ContentBlock{Type: BlockText, Text: ch.Message.Content})
	}
	taken := oaTakenIDs(len(ch.Message.ToolCalls))
	for _, tc := range ch.Message.ToolCalls {
		if tc.ID != "" {
			taken[tc.ID] = true
		}
	}
	for i, tc := range ch.Message.ToolCalls {
		out.Content = append(out.Content, ContentBlock{Type: BlockToolUse, ToolUse: oaToolUse(tc, i, taken)})
	}
	return out, nil
}

// oaTakenIDs allocates the per-response id set passed through oaToolUse.
func oaTakenIDs(n int) map[string]bool { return make(map[string]bool, n) }

// oaToolUse normalizes one wire tool call: some local servers omit the id
// (synthesized — the harness needs it to pair tool_results) or send empty
// arguments (defaulted to {} so json.RawMessage stays valid). taken must be
// pre-seeded with every REAL id in the response and accumulates ids as they
// are assigned: a server can mix real and missing ids in one turn, and a
// positional "call_<n>" that collides with a real id would cross-wire
// tool_results to the wrong call — on collision the synthesized id is
// prefixed with "synth_" until free.
func oaToolUse(tc oaToolCall, i int, taken map[string]bool) *ToolUse {
	id := tc.ID
	if id == "" {
		id = "call_" + strconv.Itoa(i+1)
		for taken[id] {
			id = "synth_" + id
		}
	}
	taken[id] = true
	args := strings.TrimSpace(tc.Function.Arguments)
	if args == "" {
		args = "{}"
	}
	return &ToolUse{ID: id, Name: tc.Function.Name, Input: json.RawMessage(args)}
}

// oaStop maps finish_reason → StopReason. hasToolCalls guards the servers that
// report "stop" even when tool calls are present — the loop must still dispatch.
func oaStop(reason string, hasToolCalls bool) StopReason {
	if hasToolCalls {
		return StopToolUse
	}
	switch reason {
	case "stop":
		return StopEndTurn
	case "tool_calls":
		return StopToolUse
	case "length":
		return StopMaxTokens
	case "content_filter":
		return StopRefusal
	default:
		return StopUnknown
	}
}

// post issues one POST and maps transport/status errors exactly like the
// Anthropic adapter (429/5xx retryable, 400 overflow sentinel, rest terminal).
func (o *OpenAI) post(ctx context.Context, path string, body []byte) ([]byte, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.cfg.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("content-type", "application/json")
	if o.cfg.APIKey != "" {
		httpReq.Header.Set("authorization", "Bearer "+o.cfg.APIKey)
	}
	resp, err := o.http.Do(httpReq)
	if err != nil {
		return nil, &llmclient.Retryable{Err: fmt.Errorf("%s: cannot reach %s (is it running?): %w", o.cfg.Name, o.cfg.BaseURL, err)}
	}
	defer func() { _ = resp.Body.Close() }()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	switch {
	case resp.StatusCode == 429:
		return nil, &llmclient.RetryAfter{After: parseRetryAfter(resp.Header), Err: fmt.Errorf("%s: status 429", o.cfg.Name)}
	case resp.StatusCode >= 500:
		return nil, &llmclient.Retryable{Err: fmt.Errorf("%s: status %d", o.cfg.Name, resp.StatusCode)}
	case resp.StatusCode == 400 && isContextOverflow(string(rb)):
		return nil, fmt.Errorf("%s: %w (status 400): %s", o.cfg.Name, ErrContextOverflow, truncate(string(rb), 200))
	case resp.StatusCode != 200:
		return nil, fmt.Errorf("%s: status %d: %s", o.cfg.Name, resp.StatusCode, truncate(string(rb), 300))
	}
	return rb, nil
}

// Models lists the endpoint's models. Tools is deliberately false: an OpenAI
// listing can't attest tool support — llm.Probe is the authority for gating.
// Wrapped in llmclient.Do like every provider HTTP request.
func (o *OpenAI) Models(ctx context.Context) ([]ModelInfo, error) {
	return llmclient.Do(ctx, o.rc, func(ctx context.Context) ([]ModelInfo, error) {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, o.cfg.BaseURL+"/models", nil)
		if err != nil {
			return nil, err
		}
		if o.cfg.APIKey != "" {
			httpReq.Header.Set("authorization", "Bearer "+o.cfg.APIKey)
		}
		resp, err := o.http.Do(httpReq)
		if err != nil {
			return nil, &llmclient.Retryable{Err: fmt.Errorf("%s: cannot reach %s (is it running?): %w", o.cfg.Name, o.cfg.BaseURL, err)}
		}
		defer func() { _ = resp.Body.Close() }()
		rb, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		switch {
		case resp.StatusCode == 429:
			return nil, &llmclient.RetryAfter{After: parseRetryAfter(resp.Header), Err: fmt.Errorf("%s: status 429", o.cfg.Name)}
		case resp.StatusCode >= 500:
			return nil, &llmclient.Retryable{Err: fmt.Errorf("%s: status %d", o.cfg.Name, resp.StatusCode)}
		case resp.StatusCode != 200:
			return nil, fmt.Errorf("%s: status %d: %s", o.cfg.Name, resp.StatusCode, truncate(string(rb), 300))
		}
		var wr struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rb, &wr); err != nil {
			return nil, fmt.Errorf("%s: decode models: %w", o.cfg.Name, err)
		}
		out := make([]ModelInfo, 0, len(wr.Data))
		for _, m := range wr.Data {
			out = append(out, ModelInfo{ID: m.ID})
		}
		return out, nil
	})
}

// Ping verifies the endpoint is reachable (GET /models) — for a local server
// this doubles as the "is LM Studio/Ollama actually running?" check.
func (o *OpenAI) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := o.Models(ctx)
	return err
}
