package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/revelara-ai/orion/pkg/llmclient"
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

// Name identifies the provider.
func (a *Anthropic) Name() string { return "anthropic" }

// WithBaseURL overrides the API endpoint (config base_url — proxies, gateways).
// Empty means keep the default. Returns a for chaining.
func (a *Anthropic) WithBaseURL(u string) *Anthropic {
	if u != "" {
		a.baseURL = strings.TrimRight(u, "/")
	}
	return a
}

// ContextWindow reports the model's context window in tokens. Implements the
// optional llm.ContextWindow capability so the context manager governs the
// Anthropic brain precisely. It is PER-MODEL because /model can swap to a
// smaller-window model at runtime — a blanket 1M would put the policy thresholds
// above a 200K model's real ceiling and proactive clearing would never engage.
func (a *Anthropic) ContextWindow() int { return anthropicContextWindow(a.model) }

// anthropicContextWindow maps a model id to its context window in tokens via a
// WHITELIST of the known 1M-context models, defaulting everything else to 200K.
// The default direction is deliberate: legacy models (Opus 4.5/4.1/4.0, Sonnet
// 4.5, older) and unrecognized ids are 200K, and under-reporting the window only
// over-clears (safe) whereas over-reporting it would defeat proactive clearing
// and brick the session — the exact failure a naive contains("opus") caused.
// (Model→window facts per the claude-api reference; update when a new 1M model
// ships. A live Models API lookup — max_input_tokens — is the follow-up.)

// MaxOutputTokens reports the model's max OUTPUT cap (far below the context
// window). Implements the optional llm.MaxOutputTokens capability so the harness
// never requests more output than the model allows.
func (a *Anthropic) MaxOutputTokens() int { return anthropicMaxOutput(a.model) }

// anthropicMaxOutput maps a model id to its max output tokens, CONSERVATIVELY (it
// only ever under-estimates, never over — over-estimating causes an unrecoverable
// 400). The 1M-context tier supports 128K output (we request far less); the 4.x
// mid line supports ≥32K; legacy/unknown default to the universal 4096 floor so an
// unrecognized id is never bricked by an over-large max_tokens.
func anthropicMaxOutput(model string) int {
	m := strings.ToLower(model)
	for _, big := range []string{"opus-4-6", "opus-4-7", "opus-4-8", "sonnet-4-6", "sonnet-5", "fable-5", "mythos-5", "mythos-preview"} {
		if strings.Contains(m, big) {
			return 64000
		}
	}
	for _, mid := range []string{"opus-4-5", "opus-4-1", "opus-4-0", "sonnet-4-5", "haiku-4-5"} {
		if strings.Contains(m, mid) {
			return 16384
		}
	}
	return 4096
}

func anthropicContextWindow(model string) int {
	m := strings.ToLower(model)
	for _, oneM := range []string{
		"opus-4-6", "opus-4-7", "opus-4-8",
		"sonnet-4-6", "sonnet-5",
		"fable-5", "mythos-5", "mythos-preview",
	} {
		if strings.Contains(m, oneM) {
			return 1_000_000
		}
	}
	return 200_000
}

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
	body, err := json.Marshal(withContextEdits(a.toWire(req, model, maxTok)))
	if err != nil {
		return nil, fmt.Errorf("anthropic: marshal request: %w", err)
	}
	return llmclient.Do(ctx, a.rc, func(ctx context.Context) (*ChatResponse, error) {
		return a.do(ctx, body)
	})
}

// ── wire types (Anthropic Messages API shape) ────────────────────────────────

type wireRequest struct {
	Model     string            `json:"model"`
	MaxTokens int               `json:"max_tokens"`
	System    []wireSystemBlock `json:"system,omitempty"`
	Messages  []wireMessage     `json:"messages"`
	Tools     []wireTool        `json:"tools,omitempty"`
	Stream    bool              `json:"stream,omitempty"`
	// ContextManagement (or-hhq, opt-in beta): server-side context edits —
	// the Anthropic brain offloads tool-result clearing/compaction natively.
	ContextManagement *wireContextManagement `json:"context_management,omitempty"`
}

type wireContextManagement struct {
	Edits []wireContextEdit `json:"edits"`
}

type wireContextEdit struct {
	Type string `json:"type"`
}

// contextEditsEnabled: ORION_ANTHROPIC_CONTEXT_EDITS=1 opts into the
// server-side context-management beta (default off — provider-agnostic core
// behavior is unchanged; live verification tracked on or-hhq's close notes).
func contextEditsEnabled() bool { return os.Getenv("ORION_ANTHROPIC_CONTEXT_EDITS") == "1" }

const contextManagementBeta = "context-management-2025-06-27"

// withContextEdits decorates a wire request with the beta edits when enabled.
func withContextEdits(w wireRequest) wireRequest {
	if contextEditsEnabled() {
		w.ContextManagement = &wireContextManagement{Edits: []wireContextEdit{
			{Type: "clear_tool_uses_20250919"},
		}}
	}
	return w
}

// wireCacheControl marks a prompt-caching breakpoint (or-4qkg). Everything
// before the marked block is cached: reads bill at 0.1x input price with a
// 5-minute TTL — and Orion's loop iterations are seconds apart, so the
// ~25-30K-token static prefix (tools + system) stops being re-billed every
// iteration.
type wireCacheControl struct {
	Type string `json:"type"` // "ephemeral"
}

// wireSystemBlock renders system as a content-block array so the block can
// carry a cache_control breakpoint (the string form cannot).
type wireSystemBlock struct {
	Type         string            `json:"type"` // "text"
	Text         string            `json:"text"`
	CacheControl *wireCacheControl `json:"cache_control,omitempty"`
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
	// CacheControl is never set today (the context manager rewrites message
	// history, so message-prefix caching can't be relied on); present so the
	// wire shape is complete and tests can pin the absence.
	CacheControl *wireCacheControl `json:"cache_control,omitempty"`
}
type wireTool struct {
	Name         string            `json:"name"`
	Description  string            `json:"description,omitempty"`
	InputSchema  json.RawMessage   `json:"input_schema"`
	CacheControl *wireCacheControl `json:"cache_control,omitempty"`
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
	w := wireRequest{Model: model, MaxTokens: maxTok}
	if req.System != "" {
		// The system block carries a breakpoint: it caches tools+system (the
		// whole static prefix). Requires the prefix to be byte-stable across
		// iterations — it is (deterministic registry order, no timestamps).
		w.System = []wireSystemBlock{{Type: "text", Text: req.System, CacheControl: &wireCacheControl{Type: "ephemeral"}}}
	}
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
		// Never send a message with empty content — a nil slice marshals to
		// `content: null`, which the API rejects ("should be a valid array"), and a
		// single empty message poisons every subsequent request. Dropping it is safe
		// (the API permits consecutive same-role messages).
		if len(wm.Content) > 0 {
			w.Messages = append(w.Messages, wm)
		}
	}
	for _, t := range req.Tools {
		w.Tools = append(w.Tools, wireTool{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema})
	}
	if n := len(w.Tools); n > 0 {
		// Breakpoint on the LAST tool caches the whole tool array even when the
		// system prompt varies. Two breakpoints total — well under the 4 allowed.
		w.Tools[n-1].CacheControl = &wireCacheControl{Type: "ephemeral"}
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
	if contextEditsEnabled() {
		httpReq.Header.Set("anthropic-beta", contextManagementBeta)
	}

	resp, err := a.http.Do(httpReq)
	if err != nil {
		return nil, &llmclient.Retryable{Err: err} // network blip → retry
	}
	defer func() { _ = resp.Body.Close() }()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))

	switch {
	case resp.StatusCode == 429 || resp.StatusCode == 529:
		return nil, &llmclient.RetryAfter{After: parseRetryAfter(resp.Header), Err: fmt.Errorf("anthropic: status %d", resp.StatusCode)}
	case resp.StatusCode >= 500:
		return nil, &llmclient.Retryable{Err: fmt.Errorf("anthropic: status %d", resp.StatusCode)}
	case resp.StatusCode == 400 && isContextOverflow(string(rb)):
		// The prompt exceeded the context window. Surface it as the sentinel so the
		// harness shrinks-and-retries instead of bricking the session. NOT retryable
		// by llmclient — retrying the identical over-long body just fails again.
		return nil, fmt.Errorf("anthropic: %w (status 400): %s", ErrContextOverflow, truncate(string(rb), 200))
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

// CountTokens implements the optional llm.TokenCounter capability (or-hhq):
// the EXACT input-token count via POST /v1/messages/count_tokens — the
// accurate sensor CountOrEstimate prefers. Same wire shape as Chat minus
// max_tokens; errors degrade to the estimate at the caller.
func (a *Anthropic) CountTokens(ctx context.Context, req ChatRequest) (int, error) {
	model := req.Model
	if model == "" {
		model = a.model
	}
	w := a.toWire(req, model, 1)
	w.MaxTokens = 0 // count_tokens rejects max_tokens
	body, err := json.Marshal(struct {
		Model    string            `json:"model"`
		System   []wireSystemBlock `json:"system,omitempty"`
		Messages []wireMessage     `json:"messages"`
		Tools    []wireTool        `json:"tools,omitempty"`
	}{Model: w.Model, System: w.System, Messages: w.Messages, Tools: w.Tools})
	if err != nil {
		return 0, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/v1/messages/count_tokens", bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("x-api-key", a.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)
	resp, err := a.http.Do(httpReq)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("anthropic count_tokens: status %d: %s", resp.StatusCode, truncate(string(rb), 120))
	}
	var out struct {
		InputTokens int `json:"input_tokens"`
	}
	if err := json.Unmarshal(rb, &out); err != nil {
		return 0, err
	}
	return out.InputTokens, nil
}
