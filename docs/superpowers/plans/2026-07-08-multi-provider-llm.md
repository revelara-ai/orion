# Multi-Provider LLM Support Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Orion runs against any LLM provider — Anthropic (default), OpenAI-compatible endpoints (LM Studio, Ollama, OpenAI, OpenRouter, vLLM), and native Gemini — selected via `~/.orion/config.yaml`, with the provider stack publishable from `pkg/`.

**Architecture:** Move `internal/llm` + `internal/llmclient` to `pkg/` (zero internal deps, boundary-tested). Add two adapters implementing the existing `llm.Provider` interface, a `pkg/llm/config` YAML facility (named providers + `provider/model` refs), an `llm.Probe` tool-capability check, and a thin `internal/llmsetup` glue package that replaces the three hardcoded `llm.NewAnthropic` call sites.

**Tech Stack:** Go 1.26, stdlib HTTP (no vendor SDKs), `gopkg.in/yaml.v3` (already a dependency), httptest for adapter tests.

**Spec:** `docs/superpowers/specs/2026-07-08-multi-provider-llm-design.md`

## Global Constraints

- No new module dependencies; no vendor LLM SDKs — hand-rolled HTTP like the existing Anthropic adapter.
- `pkg/...` must import nothing under `internal/...` (enforced by test in Task 1).
- API keys: env-var indirection only; never written to disk, never logged.
- Every provider HTTP request goes through `llmclient.Do` (retry/backoff/breaker).
- Backward compat: no config file + `ANTHROPic_API_KEY` set → behavior identical to today; key absent → offline conductor fallback, as today.
- TDD: write the failing test first in every task. Run `gofmt -l` and `go vet ./...` before each commit.
- All new code lives in package `llm` (adapters), `config`, or `llmsetup` — follow the file layout exactly.

---

### Task 1: Move `internal/llm` and `internal/llmclient` to `pkg/`, add boundary test

**Files:**
- Move: `internal/llmclient/` → `pkg/llmclient/` (all files)
- Move: `internal/llm/` → `pkg/llm/` (all files)
- Modify: every file importing those paths (mechanical, ~40 files — see grep below)
- Create: `pkg/llm/boundary_test.go`

**Interfaces:**
- Consumes: existing `llm.Provider`, `llmclient.Do` — unchanged, only the import path moves.
- Produces: `github.com/revelara-ai/orion/pkg/llm` and `github.com/revelara-ai/orion/pkg/llmclient` import paths that every later task uses.

- [ ] **Step 1: Move the packages with git mv**

```bash
cd ~/go/src/github.com/revelara-ai/orion
git mv internal/llmclient pkg/llmclient
git mv internal/llm pkg/llm
```

- [ ] **Step 2: Rewrite imports repo-wide (llmclient first — its path is a prefix-collision hazard with llm)**

```bash
grep -rl 'revelara-ai/orion/internal/llmclient' --include='*.go' . | xargs sed -i 's|revelara-ai/orion/internal/llmclient|revelara-ai/orion/pkg/llmclient|g'
grep -rl 'revelara-ai/orion/internal/llm"' --include='*.go' . | xargs sed -i 's|revelara-ai/orion/internal/llm"|revelara-ai/orion/pkg/llm"|g'
```

The second pattern is quote-anchored so it cannot re-touch the already-rewritten llmclient imports.

- [ ] **Step 3: Verify nothing still references the old paths**

Run: `grep -rn 'internal/llm' --include='*.go' . | grep -v 'internal/llmsetup'`
Expected: no output.

- [ ] **Step 4: Build and run the full test suite**

Run: `go build ./... && go test ./...`
Expected: PASS everywhere (the move is behavior-neutral).

- [ ] **Step 5: Write the boundary test (failing is impossible here, but it locks the invariant for later tasks)**

Create `pkg/llm/boundary_test.go`:

```go
package llm_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestPkgHasNoInternalDeps enforces the publishable-module boundary: nothing
// under pkg/ may depend on internal/ (spec: extraction to its own repo must
// stay mechanical).
func TestPkgHasNoInternalDeps(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "github.com/revelara-ai/orion/pkg/...").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	for _, dep := range strings.Fields(string(out)) {
		if strings.Contains(dep, "revelara-ai/orion/internal") {
			t.Errorf("pkg/ depends on internal package %s", dep)
		}
	}
}
```

- [ ] **Step 6: Run the boundary test**

Run: `go test ./pkg/llm/ -run TestPkgHasNoInternalDeps -v`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "refactor(llm): move llm + llmclient to pkg/ — publishable module boundary"
```

---

### Task 2: OpenAI-compatible adapter — Chat, Models, Ping, capabilities

**Files:**
- Create: `pkg/llm/openai.go`
- Test: `pkg/llm/openai_test.go`

**Interfaces:**
- Consumes: `llm.Provider`, `llm.ChatRequest/ChatResponse/ContentBlock/StopReason/Usage/ModelInfo`, `llmclient.Do/Retryable/RetryAfter`, shared helpers `isContextOverflow`, `parseRetryAfter`, `truncate`, `ErrContextOverflow` (all already in package `llm`).
- Produces: `func NewOpenAI(cfg OpenAIConfig) *OpenAI` where `OpenAIConfig{Name, BaseURL, APIKey, Model string; ContextWindow, MaxOutput int}`. `*OpenAI` implements `Provider`, `ContextWindow`, `MaxOutputTokens`. Task 3 adds `ChatStream` to this struct; Task 6's `config.Build` constructs it.

- [ ] **Step 1: Write the failing tests**

Create `pkg/llm/openai_test.go`:

```go
package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// openAITestServer returns a server that captures the request body and replies
// with the canned response JSON.
func openAITestServer(t *testing.T, respJSON string, captured *oaRequest) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Write([]byte(`{"data":[{"id":"qwen3-32b"},{"id":"llama-3.3-70b"}]}`))
			return
		}
		if captured != nil {
			if err := json.NewDecoder(r.Body).Decode(captured); err != nil {
				t.Errorf("decode request: %v", err)
			}
		}
		w.Write([]byte(respJSON))
	}))
}

func TestOpenAIChatTranslation(t *testing.T) {
	var got oaRequest
	srv := openAITestServer(t, `{
		"model":"qwen3-32b",
		"choices":[{"message":{"content":"hi there","tool_calls":[
			{"id":"call_abc","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"a.go\"}"}}
		]},"finish_reason":"tool_calls"}],
		"usage":{"prompt_tokens":10,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":3}}
	}`, &got)
	defer srv.Close()

	o := NewOpenAI(OpenAIConfig{Name: "lmstudio", BaseURL: srv.URL + "/v1", Model: "qwen3-32b"})
	req := ChatRequest{
		System: "be brief",
		Messages: []Message{
			TextMessage(RoleUser, "hello"),
			{Role: RoleAssistant, Content: []ContentBlock{
				{Type: BlockText, Text: "checking"},
				{Type: BlockToolUse, ToolUse: &ToolUse{ID: "call_1", Name: "ls", Input: json.RawMessage(`{}`)}},
			}},
			{Role: RoleUser, Content: []ContentBlock{
				{Type: BlockToolResult, ToolResult: &ToolResult{ToolUseID: "call_1", Content: "a.go", IsError: false}},
			}},
		},
		Tools:     []Tool{{Name: "ls", Description: "list", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		MaxTokens: 100,
	}
	resp, err := o.Chat(context.Background(), req)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	// Request wire shape.
	if got.Messages[0].Role != "system" || got.Messages[0].Content != "be brief" {
		t.Errorf("system message wrong: %+v", got.Messages[0])
	}
	if got.Messages[1].Role != "user" || got.Messages[1].Content != "hello" {
		t.Errorf("user message wrong: %+v", got.Messages[1])
	}
	am := got.Messages[2]
	if am.Role != "assistant" || am.Content != "checking" || len(am.ToolCalls) != 1 || am.ToolCalls[0].ID != "call_1" || am.ToolCalls[0].Function.Name != "ls" {
		t.Errorf("assistant message wrong: %+v", am)
	}
	tm := got.Messages[3]
	if tm.Role != "tool" || tm.ToolCallID != "call_1" || tm.Content != "a.go" {
		t.Errorf("tool message wrong: %+v", tm)
	}
	if len(got.Tools) != 1 || got.Tools[0].Type != "function" || got.Tools[0].Function.Name != "ls" {
		t.Errorf("tools wrong: %+v", got.Tools)
	}
	if got.MaxTokens != 100 || got.Model != "qwen3-32b" {
		t.Errorf("model/max_tokens wrong: %s %d", got.Model, got.MaxTokens)
	}

	// Response mapping.
	if resp.StopReason != StopToolUse {
		t.Errorf("stop reason = %q, want tool_use", resp.StopReason)
	}
	if resp.Text() != "hi there" {
		t.Errorf("text = %q", resp.Text())
	}
	tus := resp.ToolUses()
	if len(tus) != 1 || tus[0].ID != "call_abc" || tus[0].Name != "read_file" {
		t.Errorf("tool uses wrong: %+v", tus)
	}
	if resp.Usage.InputTokens != 10 || resp.Usage.OutputTokens != 5 || resp.Usage.CacheReadInputTokens != 3 {
		t.Errorf("usage wrong: %+v", resp.Usage)
	}
}

func TestOpenAIErrorToolResultPrefixed(t *testing.T) {
	var got oaRequest
	srv := openAITestServer(t, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`, &got)
	defer srv.Close()
	o := NewOpenAI(OpenAIConfig{BaseURL: srv.URL + "/v1", Model: "m"})
	_, err := o.Chat(context.Background(), ChatRequest{Messages: []Message{
		{Role: RoleUser, Content: []ContentBlock{{Type: BlockToolResult, ToolResult: &ToolResult{ToolUseID: "c1", Content: "boom", IsError: true}}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Messages[0].Content != "ERROR: boom" {
		t.Errorf("error tool result not prefixed: %q", got.Messages[0].Content)
	}
}

func TestOpenAIStopReasonMapping(t *testing.T) {
	cases := []struct {
		finish   string
		hasCalls bool
		want     StopReason
	}{
		{"stop", false, StopEndTurn},
		{"stop", true, StopToolUse}, // some local servers report "stop" even with tool_calls
		{"tool_calls", true, StopToolUse},
		{"length", false, StopMaxTokens},
		{"content_filter", false, StopRefusal},
		{"weird", false, StopUnknown},
	}
	for _, c := range cases {
		if got := oaStop(c.finish, c.hasCalls); got != c.want {
			t.Errorf("oaStop(%q,%v) = %q, want %q", c.finish, c.hasCalls, got, c.want)
		}
	}
}

func TestOpenAISynthesizesMissingToolCallID(t *testing.T) {
	srv := openAITestServer(t, `{"choices":[{"message":{"tool_calls":[
		{"type":"function","function":{"name":"ls","arguments":""}}
	]},"finish_reason":"tool_calls"}]}`, nil)
	defer srv.Close()
	o := NewOpenAI(OpenAIConfig{BaseURL: srv.URL + "/v1", Model: "m"})
	resp, err := o.Chat(context.Background(), ChatRequest{Messages: []Message{TextMessage(RoleUser, "x")}})
	if err != nil {
		t.Fatal(err)
	}
	tus := resp.ToolUses()
	if len(tus) != 1 || tus[0].ID == "" {
		t.Fatalf("missing id not synthesized: %+v", tus)
	}
	if string(tus[0].Input) != "{}" {
		t.Errorf("empty arguments not defaulted to {}: %q", tus[0].Input)
	}
}

func TestOpenAIModelsAndPing(t *testing.T) {
	srv := openAITestServer(t, `{}`, nil)
	defer srv.Close()
	o := NewOpenAI(OpenAIConfig{Name: "lmstudio", BaseURL: srv.URL + "/v1"})
	ms, err := o.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 || ms[0].ID != "qwen3-32b" || ms[0].Tools {
		t.Errorf("models wrong (Tools must be false — probe is the authority): %+v", ms)
	}
	if err := o.Ping(context.Background()); err != nil {
		t.Errorf("ping: %v", err)
	}
	srv.Close()
	if err := o.Ping(context.Background()); err == nil {
		t.Error("ping against closed server should fail")
	}
}

func TestOpenAICapabilities(t *testing.T) {
	o := NewOpenAI(OpenAIConfig{BaseURL: "http://x/v1", ContextWindow: 32768, MaxOutput: 4096})
	if o.ContextWindow() != 32768 || o.MaxOutputTokens() != 4096 {
		t.Errorf("capabilities not plumbed: %d %d", o.ContextWindow(), o.MaxOutputTokens())
	}
	var _ Provider = o // compile-time interface check (ChatStream added in Task 3 — stub until then)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./pkg/llm/ -run TestOpenAI -v`
Expected: FAIL — `undefined: oaRequest`, `undefined: NewOpenAI`, etc.

- [ ] **Step 3: Implement the adapter**

Create `pkg/llm/openai.go`:

```go
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

func (o *OpenAI) Name() string { return o.cfg.Name }

// ContextWindow / MaxOutputTokens implement the optional capabilities from
// config values (0 = unknown → callers fall back).
func (o *OpenAI) ContextWindow() int   { return o.cfg.ContextWindow }
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
		w.Tools = append(w.Tools, oaTool{Type: "function", Function: oaToolDef{Name: t.Name, Description: t.Description, Parameters: t.InputSchema}})
	}
	return w
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
	for i, tc := range ch.Message.ToolCalls {
		out.Content = append(out.Content, ContentBlock{Type: BlockToolUse, ToolUse: oaToolUse(tc, i)})
	}
	return out, nil
}

// oaToolUse normalizes one wire tool call: some local servers omit the id
// (synthesized — the harness needs it to pair tool_results) or send empty
// arguments (defaulted to {} so json.RawMessage stays valid).
func oaToolUse(tc oaToolCall, i int) *ToolUse {
	id := tc.ID
	if id == "" {
		id = "call_" + strconv.Itoa(i+1)
	}
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
	defer resp.Body.Close()
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
func (o *OpenAI) Models(ctx context.Context) ([]ModelInfo, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, o.cfg.BaseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	if o.cfg.APIKey != "" {
		httpReq.Header.Set("authorization", "Bearer "+o.cfg.APIKey)
	}
	resp, err := o.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%s: cannot reach %s — is it running?: %w", o.cfg.Name, o.cfg.BaseURL, err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != 200 {
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
}

// Ping verifies the endpoint is reachable (GET /models) — for a local server
// this doubles as the "is LM Studio/Ollama actually running?" check.
func (o *OpenAI) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := o.Models(ctx)
	return err
}
```

Also add a temporary `ChatStream` stub so `*OpenAI` satisfies `Provider` until Task 3 replaces it:

```go
// ChatStream is implemented in openai_stream.go (Task 3); until then, degrade
// to non-streaming Chat and emit the text once.
func (o *OpenAI) ChatStream(ctx context.Context, req ChatRequest, onText func(string)) (*ChatResponse, error) {
	resp, err := o.Chat(ctx, req)
	if err == nil && onText != nil {
		onText(resp.Text())
	}
	return resp, err
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./pkg/llm/ -run TestOpenAI -v`
Expected: PASS (all 6 tests)

- [ ] **Step 5: Vet + full package test, then commit**

```bash
gofmt -l pkg/ && go vet ./pkg/... && go test ./pkg/...
git add pkg/llm/openai.go pkg/llm/openai_test.go
git commit -m "feat(llm): OpenAI-compatible provider adapter (LM Studio, Ollama, OpenAI, OpenRouter)"
```

---

### Task 3: OpenAI-compatible adapter — SSE streaming

**Files:**
- Create: `pkg/llm/openai_stream.go` (replaces the Task 2 stub — delete the stub from `openai.go`)
- Test: `pkg/llm/openai_stream_test.go`

**Interfaces:**
- Consumes: `oaRequest/oaChunk` wire types, `o.post` error mapping pattern, `errTruncatedStream` (exists in `anthropic_stream.go`, same package).
- Produces: `(*OpenAI) ChatStream(ctx, req, onText)` with the same retry-only-before-first-text contract as the Anthropic adapter.

- [ ] **Step 1: Write the failing tests**

Create `pkg/llm/openai_stream_test.go`:

```go
package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func sseServer(t *testing.T, events []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		for _, e := range events {
			w.Write([]byte("data: " + e + "\n\n"))
		}
	}))
}

func TestOpenAIChatStreamAssemblesTextAndTools(t *testing.T) {
	srv := sseServer(t, []string{
		`{"choices":[{"delta":{"content":"Hel"}}]}`,
		`{"choices":[{"delta":{"content":"lo"}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_9","function":{"name":"ls","arguments":"{\"pa"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"th\":\".\"}"}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":7,"completion_tokens":4}}`,
		`[DONE]`,
	})
	defer srv.Close()
	o := NewOpenAI(OpenAIConfig{BaseURL: srv.URL + "/v1", Model: "m"})

	var chunks []string
	resp, err := o.ChatStream(context.Background(), ChatRequest{Messages: []Message{TextMessage(RoleUser, "x")}}, func(s string) {
		chunks = append(chunks, s)
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if got := strings.Join(chunks, ""); got != "Hello" {
		t.Errorf("streamed text = %q, want Hello", got)
	}
	if resp.Text() != "Hello" {
		t.Errorf("assembled text = %q", resp.Text())
	}
	tus := resp.ToolUses()
	if len(tus) != 1 || tus[0].ID != "call_9" || tus[0].Name != "ls" || string(tus[0].Input) != `{"path":"."}` {
		t.Fatalf("assembled tool use wrong: %+v", tus)
	}
	if resp.StopReason != StopToolUse {
		t.Errorf("stop = %q, want tool_use", resp.StopReason)
	}
	if resp.Usage.InputTokens != 7 || resp.Usage.OutputTokens != 4 {
		t.Errorf("usage wrong: %+v", resp.Usage)
	}
}

func TestOpenAIChatStreamTruncated(t *testing.T) {
	// Stream ends without finish_reason or [DONE] → must error, never a silent partial.
	srv := sseServer(t, []string{`{"choices":[{"delta":{"content":"par"}}]}`})
	defer srv.Close()
	o := NewOpenAI(OpenAIConfig{BaseURL: srv.URL + "/v1", Model: "m"})
	_, err := o.ChatStream(context.Background(), ChatRequest{Messages: []Message{TextMessage(RoleUser, "x")}}, nil)
	if err == nil {
		t.Fatal("truncated stream must return an error")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./pkg/llm/ -run TestOpenAIChatStream -v`
Expected: FAIL — the Task 2 stub emits full text once as a single chunk and TestOpenAIChatStreamTruncated gets no error.

- [ ] **Step 3: Implement streaming (and delete the stub from openai.go)**

Create `pkg/llm/openai_stream.go`:

```go
package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/revelara-ai/orion/pkg/llmclient"
)

// oaChunk is one SSE delta event of a streaming chat-completions response.
type oaChunk struct {
	Model   string `json:"model"`
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *oaUsage `json:"usage"`
}

// oaStreamCall accumulates one tool call across its argument fragments.
type oaStreamCall struct {
	id, name string
	args     strings.Builder
}

// ChatStream issues a streaming request. Same contract as the Anthropic
// adapter: onText gets raw text deltas (whitespace preserved); the assembled
// response is returned for tool dispatch; retries happen only BEFORE any text
// has been emitted so a retry never duplicates visible output.
func (o *OpenAI) ChatStream(ctx context.Context, req ChatRequest, onText func(string)) (*ChatResponse, error) {
	model := req.Model
	if model == "" {
		model = o.cfg.Model
	}
	w := o.toWire(req, model)
	w.Stream = true
	w.StreamOptions = &oaStreamOpts{IncludeUsage: true}
	body, err := json.Marshal(w)
	if err != nil {
		return nil, fmt.Errorf("%s: marshal request: %w", o.cfg.Name, err)
	}
	if onText == nil {
		onText = func(string) {}
	}
	return llmclient.Do(ctx, o.rc, func(ctx context.Context) (*ChatResponse, error) {
		return o.doStream(ctx, body, onText)
	})
}

func (o *OpenAI) doStream(ctx context.Context, body []byte, onText func(string)) (*ChatResponse, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.cfg.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("accept", "text/event-stream")
	if o.cfg.APIKey != "" {
		httpReq.Header.Set("authorization", "Bearer "+o.cfg.APIKey)
	}
	resp, err := o.http.Do(httpReq)
	if err != nil {
		return nil, &llmclient.Retryable{Err: fmt.Errorf("%s: cannot reach %s (is it running?): %w", o.cfg.Name, o.cfg.BaseURL, err)}
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		// Reuse the non-streaming status mapping by reading the (JSON) error body.
		rb := make([]byte, 0, 4096)
		buf := bufio.NewReader(resp.Body)
		for {
			b, err := buf.ReadByte()
			if err != nil || len(rb) >= 4096 {
				break
			}
			rb = append(rb, b)
		}
		switch {
		case resp.StatusCode == 429:
			return nil, &llmclient.RetryAfter{After: parseRetryAfter(resp.Header), Err: fmt.Errorf("%s: status 429", o.cfg.Name)}
		case resp.StatusCode >= 500:
			return nil, &llmclient.Retryable{Err: fmt.Errorf("%s: status %d", o.cfg.Name, resp.StatusCode)}
		case resp.StatusCode == 400 && isContextOverflow(string(rb)):
			return nil, fmt.Errorf("%s: %w (status 400): %s", o.cfg.Name, ErrContextOverflow, truncate(string(rb), 200))
		default:
			return nil, fmt.Errorf("%s: status %d: %s", o.cfg.Name, resp.StatusCode, truncate(string(rb), 300))
		}
	}

	var (
		text     strings.Builder
		calls    = map[int]*oaStreamCall{}
		finish   string
		usage    Usage
		modelID  string
		emitted  bool // once true, a failure is terminal (never retry into duplicate output)
		complete bool
	)
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 8<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			complete = true
			break
		}
		var ch oaChunk
		if err := json.Unmarshal([]byte(data), &ch); err != nil {
			continue // tolerate keep-alive noise between events
		}
		if ch.Model != "" {
			modelID = ch.Model
		}
		if ch.Usage != nil {
			usage = Usage{
				InputTokens:          ch.Usage.PromptTokens,
				OutputTokens:         ch.Usage.CompletionTokens,
				CacheReadInputTokens: ch.Usage.PromptTokensDetails.CachedTokens,
			}
		}
		if len(ch.Choices) == 0 {
			continue
		}
		c := ch.Choices[0]
		if c.Delta.Content != "" {
			emitted = true
			text.WriteString(c.Delta.Content)
			onText(c.Delta.Content)
		}
		for _, tc := range c.Delta.ToolCalls {
			sc := calls[tc.Index]
			if sc == nil {
				sc = &oaStreamCall{}
				calls[tc.Index] = sc
			}
			if tc.ID != "" {
				sc.id = tc.ID
			}
			if tc.Function.Name != "" {
				sc.name = tc.Function.Name
			}
			sc.args.WriteString(tc.Function.Arguments)
		}
		if c.FinishReason != "" {
			finish = c.FinishReason
			complete = true
		}
	}
	if err := scanner.Err(); err != nil {
		if emitted {
			return nil, fmt.Errorf("%s: stream failed mid-output: %w", o.cfg.Name, err)
		}
		return nil, &llmclient.Retryable{Err: fmt.Errorf("%s: stream read: %w", o.cfg.Name, err)}
	}
	if !complete {
		if emitted {
			return nil, fmt.Errorf("%s: %w", o.cfg.Name, errTruncatedStream)
		}
		return nil, &llmclient.Retryable{Err: fmt.Errorf("%s: %w", o.cfg.Name, errTruncatedStream)}
	}

	out := &ChatResponse{Model: modelID, StopReason: oaStop(finish, len(calls) > 0), Usage: usage}
	if text.Len() > 0 {
		out.Content = append(out.Content, ContentBlock{Type: BlockText, Text: text.String()})
	}
	idxs := make([]int, 0, len(calls))
	for i := range calls {
		idxs = append(idxs, i)
	}
	sort.Ints(idxs)
	for _, i := range idxs {
		sc := calls[i]
		out.Content = append(out.Content, ContentBlock{Type: BlockToolUse, ToolUse: oaToolUse(oaToolCall{
			ID: sc.id, Type: "function", Function: oaFunction{Name: sc.name, Arguments: sc.args.String()},
		}, i)})
	}
	return out, nil
}
```

Then delete the `ChatStream` stub method from `pkg/llm/openai.go` (the one added in Task 2 Step 3).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./pkg/llm/ -run TestOpenAI -v`
Expected: PASS (streaming + all Task 2 tests)

- [ ] **Step 5: Commit**

```bash
gofmt -l pkg/ && go vet ./pkg/... && go test ./pkg/...
git add pkg/llm/openai.go pkg/llm/openai_stream.go pkg/llm/openai_stream_test.go
git commit -m "feat(llm): SSE streaming for the OpenAI-compatible adapter"
```

---

### Task 4: Gemini adapter — Chat, Models, Ping, capabilities

**Files:**
- Create: `pkg/llm/gemini.go`
- Test: `pkg/llm/gemini_test.go`
- Modify: `pkg/llm/context.go` (one new overflow marker)

**Interfaces:**
- Consumes: same package plumbing as Task 2.
- Produces: `func NewGemini(cfg GeminiConfig) *Gemini` where `GeminiConfig{Name, APIKey, Model, BaseURL string; ContextWindow, MaxOutput int}` (BaseURL defaults to `https://generativelanguage.googleapis.com/v1beta`). `*Gemini` implements `Provider`, `ContextWindow`, `MaxOutputTokens`. Synthesized tool-call ids have the form `call_<name>_<n>`.

- [ ] **Step 1: Write the failing tests**

Create `pkg/llm/gemini_test.go`:

```go
package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func geminiTestServer(t *testing.T, respJSON string, captured *gemRequest) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			w.Write([]byte(`{"models":[{"name":"models/gemini-2.5-pro"},{"name":"models/gemini-2.5-flash"}]}`))
			return
		}
		if captured != nil {
			if err := json.NewDecoder(r.Body).Decode(captured); err != nil {
				t.Errorf("decode request: %v", err)
			}
		}
		w.Write([]byte(respJSON))
	}))
}

func TestGeminiChatTranslation(t *testing.T) {
	var got gemRequest
	srv := geminiTestServer(t, `{
		"modelVersion":"gemini-2.5-pro",
		"candidates":[{"content":{"role":"model","parts":[
			{"text":"checking"},
			{"functionCall":{"name":"read_file","args":{"path":"a.go"}}}
		]},"finishReason":"STOP"}],
		"usageMetadata":{"promptTokenCount":11,"candidatesTokenCount":6,"cachedContentTokenCount":2}
	}`, &got)
	defer srv.Close()

	g := NewGemini(GeminiConfig{APIKey: "k", Model: "gemini-2.5-pro", BaseURL: srv.URL})
	req := ChatRequest{
		System: "be brief",
		Messages: []Message{
			TextMessage(RoleUser, "hello"),
			{Role: RoleAssistant, Content: []ContentBlock{
				{Type: BlockToolUse, ToolUse: &ToolUse{ID: "call_ls_1", Name: "ls", Input: json.RawMessage(`{"d":"."}`)}},
			}},
			{Role: RoleUser, Content: []ContentBlock{
				{Type: BlockToolResult, ToolResult: &ToolResult{ToolUseID: "call_ls_1", Content: "a.go"}},
			}},
		},
		Tools:     []Tool{{Name: "ls", Description: "list", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		MaxTokens: 100,
	}
	resp, err := g.Chat(context.Background(), req)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	// Request wire shape.
	if got.SystemInstruction == nil || got.SystemInstruction.Parts[0].Text != "be brief" {
		t.Errorf("systemInstruction wrong: %+v", got.SystemInstruction)
	}
	if got.Contents[0].Role != "user" || got.Contents[0].Parts[0].Text != "hello" {
		t.Errorf("user content wrong: %+v", got.Contents[0])
	}
	fc := got.Contents[1]
	if fc.Role != "model" || fc.Parts[0].FunctionCall == nil || fc.Parts[0].FunctionCall.Name != "ls" {
		t.Errorf("functionCall content wrong: %+v", fc)
	}
	fr := got.Contents[2]
	// functionResponse must carry the function NAME, recovered from the
	// tool_use with the matching synthesized id.
	if fr.Role != "user" || fr.Parts[0].FunctionResponse == nil || fr.Parts[0].FunctionResponse.Name != "ls" {
		t.Errorf("functionResponse content wrong: %+v", fr)
	}
	if fr.Parts[0].FunctionResponse.Response["result"] != "a.go" {
		t.Errorf("functionResponse payload wrong: %+v", fr.Parts[0].FunctionResponse.Response)
	}
	if len(got.Tools) != 1 || got.Tools[0].FunctionDeclarations[0].Name != "ls" {
		t.Errorf("tools wrong: %+v", got.Tools)
	}
	if got.GenerationConfig == nil || got.GenerationConfig.MaxOutputTokens != 100 {
		t.Errorf("generationConfig wrong: %+v", got.GenerationConfig)
	}

	// Response mapping: STOP + functionCall part → tool_use, synthesized id.
	if resp.StopReason != StopToolUse {
		t.Errorf("stop = %q, want tool_use", resp.StopReason)
	}
	tus := resp.ToolUses()
	if len(tus) != 1 || tus[0].Name != "read_file" || tus[0].ID != "call_read_file_1" {
		t.Fatalf("tool uses wrong: %+v", tus)
	}
	var args map[string]string
	if err := json.Unmarshal(tus[0].Input, &args); err != nil || args["path"] != "a.go" {
		t.Errorf("args wrong: %s (%v)", tus[0].Input, err)
	}
	if resp.Text() != "checking" {
		t.Errorf("text = %q", resp.Text())
	}
	if resp.Usage.InputTokens != 11 || resp.Usage.OutputTokens != 6 || resp.Usage.CacheReadInputTokens != 2 {
		t.Errorf("usage wrong: %+v", resp.Usage)
	}
}

func TestGeminiErrorToolResult(t *testing.T) {
	var got gemRequest
	srv := geminiTestServer(t, `{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`, &got)
	defer srv.Close()
	g := NewGemini(GeminiConfig{APIKey: "k", Model: "m", BaseURL: srv.URL})
	_, err := g.Chat(context.Background(), ChatRequest{Messages: []Message{
		{Role: RoleAssistant, Content: []ContentBlock{{Type: BlockToolUse, ToolUse: &ToolUse{ID: "c1", Name: "run", Input: json.RawMessage(`{}`)}}}},
		{Role: RoleUser, Content: []ContentBlock{{Type: BlockToolResult, ToolResult: &ToolResult{ToolUseID: "c1", Content: "boom", IsError: true}}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	frp := got.Contents[1].Parts[0].FunctionResponse
	if frp.Response["error"] != "boom" {
		t.Errorf("error result not mapped to error key: %+v", frp.Response)
	}
}

func TestGeminiStopMapping(t *testing.T) {
	cases := []struct {
		finish  string
		hasCall bool
		want    StopReason
	}{
		{"STOP", false, StopEndTurn},
		{"STOP", true, StopToolUse},
		{"MAX_TOKENS", false, StopMaxTokens},
		{"SAFETY", false, StopRefusal},
		{"PROHIBITED_CONTENT", false, StopRefusal},
		{"RECITATION", false, StopRefusal},
		{"OTHER", false, StopUnknown},
	}
	for _, c := range cases {
		if got := gemStop(c.finish, c.hasCall); got != c.want {
			t.Errorf("gemStop(%q,%v) = %q, want %q", c.finish, c.hasCall, got, c.want)
		}
	}
}

func TestGeminiModelsPingCapabilities(t *testing.T) {
	srv := geminiTestServer(t, `{}`, nil)
	defer srv.Close()
	g := NewGemini(GeminiConfig{APIKey: "k", Model: "gemini-2.5-pro", BaseURL: srv.URL})
	ms, err := g.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 || ms[0].ID != "gemini-2.5-pro" || !ms[0].Tools {
		t.Errorf("models wrong (name prefix must be stripped, Tools true): %+v", ms)
	}
	if err := g.Ping(context.Background()); err != nil {
		t.Errorf("ping with key: %v", err)
	}
	noKey := NewGemini(GeminiConfig{Model: "m", BaseURL: srv.URL})
	if err := noKey.Ping(context.Background()); err == nil {
		t.Error("ping without key must fail")
	}
	if g.ContextWindow() != 1_000_000 {
		t.Errorf("gemini-2.5 window = %d, want 1M", g.ContextWindow())
	}
	override := NewGemini(GeminiConfig{APIKey: "k", Model: "gemini-2.5-pro", BaseURL: srv.URL, ContextWindow: 32768, MaxOutput: 2048})
	if override.ContextWindow() != 32768 || override.MaxOutputTokens() != 2048 {
		t.Errorf("config overrides not honored: %d %d", override.ContextWindow(), override.MaxOutputTokens())
	}
	var _ Provider = g
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./pkg/llm/ -run TestGemini -v`
Expected: FAIL — `undefined: gemRequest`, `undefined: NewGemini`, etc.

- [ ] **Step 3: Implement the adapter**

Create `pkg/llm/gemini.go`:

```go
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

// DefaultGeminiBaseURL is the Generative Language API endpoint.
const DefaultGeminiBaseURL = "https://generativelanguage.googleapis.com/v1beta"

// GeminiConfig configures the native Gemini provider (its OpenAI-compat shim
// is weak on tool use, so Orion speaks the native API).
type GeminiConfig struct {
	Name          string // registry/display name; default "gemini"
	APIKey        string // required
	Model         string
	BaseURL       string // default DefaultGeminiBaseURL
	ContextWindow int    // 0 = use the known-model table
	MaxOutput     int    // 0 = conservative default
}

// Gemini is the native Gemini provider. Gemini function calls carry no id, so
// the adapter synthesizes stable ids of the form call_<name>_<n>; because the
// name is embedded in the id, translating a tool_result back to a
// functionResponse can always recover the function name, even if the same
// synthesized id recurs across turns.
type Gemini struct {
	cfg  GeminiConfig
	http *http.Client
	rc   *llmclient.Client
}

// NewGemini builds the provider. The credential is held only in memory.
func NewGemini(cfg GeminiConfig) *Gemini {
	if cfg.Name == "" {
		cfg.Name = "gemini"
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultGeminiBaseURL
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &Gemini{
		cfg:  cfg,
		http: &http.Client{},
		rc: llmclient.New(llmclient.Config{
			Timeout:     3 * time.Minute,
			MaxRetries:  3,
			BaseBackoff: 500 * time.Millisecond,
			MaxBackoff:  10 * time.Second,
		}),
	}
}

func (g *Gemini) Name() string { return g.cfg.Name }

// ContextWindow prefers the config override, else a WHITELIST of known 1M
// models with a conservative 128K default (same reasoning as the Anthropic
// table: under-reporting only over-clears, over-reporting bricks the session).
func (g *Gemini) ContextWindow() int {
	if g.cfg.ContextWindow > 0 {
		return g.cfg.ContextWindow
	}
	m := strings.ToLower(g.cfg.Model)
	for _, oneM := range []string{"gemini-3", "gemini-2.5", "gemini-2.0"} {
		if strings.Contains(m, oneM) {
			return 1_000_000
		}
	}
	return 128_000
}

// MaxOutputTokens prefers the config override, else the universal-safe floor.
func (g *Gemini) MaxOutputTokens() int {
	if g.cfg.MaxOutput > 0 {
		return g.cfg.MaxOutput
	}
	return 8192
}

// ── wire types (Gemini generateContent shape) ────────────────────────────────

type gemRequest struct {
	SystemInstruction *gemContent `json:"systemInstruction,omitempty"`
	Contents          []gemContent `json:"contents"`
	Tools             []gemTools   `json:"tools,omitempty"`
	GenerationConfig  *gemGenCfg   `json:"generationConfig,omitempty"`
}

type gemContent struct {
	Role  string    `json:"role,omitempty"` // "user" | "model"
	Parts []gemPart `json:"parts"`
}

type gemPart struct {
	Text             string               `json:"text,omitempty"`
	FunctionCall     *gemFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *gemFunctionResponse `json:"functionResponse,omitempty"`
}

type gemFunctionCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

type gemFunctionResponse struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type gemTools struct {
	FunctionDeclarations []gemFuncDecl `json:"functionDeclarations"`
}

type gemFuncDecl struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type gemGenCfg struct {
	MaxOutputTokens int `json:"maxOutputTokens,omitempty"`
}

type gemResponse struct {
	ModelVersion string `json:"modelVersion"`
	Candidates   []struct {
		Content      gemContent `json:"content"`
		FinishReason string     `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount        int `json:"promptTokenCount"`
		CandidatesTokenCount    int `json:"candidatesTokenCount"`
		CachedContentTokenCount int `json:"cachedContentTokenCount"`
	} `json:"usageMetadata"`
}

// toWire translates the provider-agnostic request. tool_results need the
// function NAME (Gemini matches responses by name, not id) — recovered by
// scanning all tool_use blocks in the conversation for the matching id.
func (g *Gemini) toWire(req ChatRequest, maxTok int) gemRequest {
	w := gemRequest{GenerationConfig: &gemGenCfg{MaxOutputTokens: maxTok}}
	if req.System != "" {
		w.SystemInstruction = &gemContent{Parts: []gemPart{{Text: req.System}}}
	}
	nameByID := map[string]string{}
	for _, m := range req.Messages {
		for _, b := range m.Content {
			if b.Type == BlockToolUse && b.ToolUse != nil {
				nameByID[b.ToolUse.ID] = b.ToolUse.Name
			}
		}
	}
	for _, m := range req.Messages {
		role := "user"
		if m.Role == RoleAssistant {
			role = "model"
		}
		gc := gemContent{Role: role}
		for _, b := range m.Content {
			switch b.Type {
			case BlockText:
				if b.Text != "" {
					gc.Parts = append(gc.Parts, gemPart{Text: b.Text})
				}
			case BlockToolUse:
				if b.ToolUse != nil {
					gc.Parts = append(gc.Parts, gemPart{FunctionCall: &gemFunctionCall{Name: b.ToolUse.Name, Args: b.ToolUse.Input}})
				}
			case BlockToolResult:
				if b.ToolResult != nil {
					payload := map[string]any{"result": b.ToolResult.Content}
					if b.ToolResult.IsError {
						payload = map[string]any{"error": b.ToolResult.Content}
					}
					gc.Parts = append(gc.Parts, gemPart{FunctionResponse: &gemFunctionResponse{
						Name:     nameByID[b.ToolResult.ToolUseID],
						Response: payload,
					}})
				}
			}
		}
		// Same empty-content rule as the Anthropic adapter: never send an
		// empty parts array.
		if len(gc.Parts) > 0 {
			w.Contents = append(w.Contents, gc)
		}
	}
	if len(req.Tools) > 0 {
		decls := make([]gemFuncDecl, 0, len(req.Tools))
		for _, t := range req.Tools {
			decls = append(decls, gemFuncDecl{Name: t.Name, Description: t.Description, Parameters: t.InputSchema})
		}
		w.Tools = []gemTools{{FunctionDeclarations: decls}}
	}
	return w
}

// Chat issues one generateContent request, retried/broken per llmclient policy.
func (g *Gemini) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	model := req.Model
	if model == "" {
		model = g.cfg.Model
	}
	maxTok := req.MaxTokens
	if maxTok <= 0 {
		maxTok = 4096
	}
	body, err := json.Marshal(g.toWire(req, maxTok))
	if err != nil {
		return nil, fmt.Errorf("%s: marshal request: %w", g.cfg.Name, err)
	}
	return llmclient.Do(ctx, g.rc, func(ctx context.Context) (*ChatResponse, error) {
		return g.do(ctx, "/models/"+model+":generateContent", body)
	})
}

func (g *Gemini) do(ctx context.Context, path string, body []byte) (*ChatResponse, error) {
	rb, err := g.post(ctx, path, body)
	if err != nil {
		return nil, err
	}
	var wr gemResponse
	if err := json.Unmarshal(rb, &wr); err != nil {
		return nil, fmt.Errorf("%s: decode response: %w", g.cfg.Name, err)
	}
	if len(wr.Candidates) == 0 {
		return nil, fmt.Errorf("%s: response has no candidates", g.cfg.Name)
	}
	return g.fromCandidate(wr), nil
}

func (g *Gemini) fromCandidate(wr gemResponse) *ChatResponse {
	cand := wr.Candidates[0]
	out := &ChatResponse{
		Model: wr.ModelVersion,
		Usage: Usage{
			InputTokens:          wr.UsageMetadata.PromptTokenCount,
			OutputTokens:         wr.UsageMetadata.CandidatesTokenCount,
			CacheReadInputTokens: wr.UsageMetadata.CachedContentTokenCount,
		},
	}
	n := 0
	for _, p := range cand.Content.Parts {
		switch {
		case p.FunctionCall != nil:
			n++
			args := p.FunctionCall.Args
			if len(args) == 0 {
				args = json.RawMessage(`{}`)
			}
			out.Content = append(out.Content, ContentBlock{Type: BlockToolUse, ToolUse: &ToolUse{
				ID:    "call_" + p.FunctionCall.Name + "_" + strconv.Itoa(n),
				Name:  p.FunctionCall.Name,
				Input: args,
			}})
		case p.Text != "":
			out.Content = append(out.Content, ContentBlock{Type: BlockText, Text: p.Text})
		}
	}
	out.StopReason = gemStop(cand.FinishReason, n > 0)
	return out
}

func gemStop(finish string, hasCall bool) StopReason {
	if hasCall {
		return StopToolUse
	}
	switch finish {
	case "STOP":
		return StopEndTurn
	case "MAX_TOKENS":
		return StopMaxTokens
	case "SAFETY", "PROHIBITED_CONTENT", "RECITATION", "BLOCKLIST":
		return StopRefusal
	default:
		return StopUnknown
	}
}

func (g *Gemini) post(ctx context.Context, path string, body []byte) ([]byte, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, g.cfg.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("x-goog-api-key", g.cfg.APIKey)
	resp, err := g.http.Do(httpReq)
	if err != nil {
		return nil, &llmclient.Retryable{Err: fmt.Errorf("%s: request: %w", g.cfg.Name, err)}
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	switch {
	case resp.StatusCode == 429:
		return nil, &llmclient.RetryAfter{After: parseRetryAfter(resp.Header), Err: fmt.Errorf("%s: status 429", g.cfg.Name)}
	case resp.StatusCode >= 500:
		return nil, &llmclient.Retryable{Err: fmt.Errorf("%s: status %d", g.cfg.Name, resp.StatusCode)}
	case resp.StatusCode == 400 && isContextOverflow(string(rb)):
		return nil, fmt.Errorf("%s: %w (status 400): %s", g.cfg.Name, ErrContextOverflow, truncate(string(rb), 200))
	case resp.StatusCode != 200:
		return nil, fmt.Errorf("%s: status %d: %s", g.cfg.Name, resp.StatusCode, truncate(string(rb), 300))
	}
	return rb, nil
}

// Models lists available models (the "models/" name prefix stripped). Gemini
// models support function calling natively → Tools true.
func (g *Gemini) Models(ctx context.Context) ([]ModelInfo, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, g.cfg.BaseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("x-goog-api-key", g.cfg.APIKey)
	resp, err := g.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%s: list models: %w", g.cfg.Name, err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s: status %d: %s", g.cfg.Name, resp.StatusCode, truncate(string(rb), 300))
	}
	var wr struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(rb, &wr); err != nil {
		return nil, fmt.Errorf("%s: decode models: %w", g.cfg.Name, err)
	}
	out := make([]ModelInfo, 0, len(wr.Models))
	for _, m := range wr.Models {
		out = append(out, ModelInfo{ID: strings.TrimPrefix(m.Name, "models/"), Tools: true, Vision: true})
	}
	return out, nil
}

// Ping verifies a credential is present (cheap, network-free — mirrors the
// Anthropic adapter; a real call happens on the first Chat).
func (g *Gemini) Ping(context.Context) error {
	if g.cfg.APIKey == "" {
		return fmt.Errorf("%s: no API key (set the configured api_key_env, e.g. GEMINI_API_KEY)", g.cfg.Name)
	}
	return nil
}
```

Also add a temporary `ChatStream` stub (removed in Task 5):

```go
// ChatStream is implemented in gemini_stream.go (Task 5); until then, degrade
// to non-streaming Chat and emit the text once.
func (g *Gemini) ChatStream(ctx context.Context, req ChatRequest, onText func(string)) (*ChatResponse, error) {
	resp, err := g.Chat(ctx, req)
	if err == nil && onText != nil {
		onText(resp.Text())
	}
	return resp, err
}
```

- [ ] **Step 4: Add the Gemini overflow marker to isContextOverflow**

In `pkg/llm/context.go`, extend the marker list (Gemini 400s say "exceeds the maximum number of tokens"):

```go
	for _, marker := range []string{
		"prompt is too long",
		"prompt too long",
		"context window",
		"context length",
		"maximum context",
		"too many tokens",
		"exceeds the maximum number of tokens",
	} {
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./pkg/llm/ -run TestGemini -v`
Expected: PASS (all 4 tests)

- [ ] **Step 6: Commit**

```bash
gofmt -l pkg/ && go vet ./pkg/... && go test ./pkg/...
git add pkg/llm/gemini.go pkg/llm/gemini_test.go pkg/llm/context.go
git commit -m "feat(llm): native Gemini provider adapter with synthesized tool-call ids"
```

---

### Task 5: Gemini adapter — SSE streaming

**Files:**
- Create: `pkg/llm/gemini_stream.go` (replaces the Task 4 stub — delete the stub from `gemini.go`)
- Test: `pkg/llm/gemini_stream_test.go`

**Interfaces:**
- Consumes: `gemResponse` wire type, `g.fromCandidate` mapping, `gemStop`, `errTruncatedStream`.
- Produces: `(*Gemini) ChatStream(ctx, req, onText)` — same contract as the other adapters.

- [ ] **Step 1: Write the failing tests**

Create `pkg/llm/gemini_stream_test.go`:

```go
package llm

import (
	"context"
	"strings"
	"testing"
)

func TestGeminiChatStreamAssembles(t *testing.T) {
	srv := sseServer(t, []string{
		`{"candidates":[{"content":{"parts":[{"text":"Hel"}]}}]}`,
		`{"candidates":[{"content":{"parts":[{"text":"lo"}]}}]}`,
		`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"ls","args":{"d":"."}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":9,"candidatesTokenCount":3}}`,
	})
	defer srv.Close()
	g := NewGemini(GeminiConfig{APIKey: "k", Model: "m", BaseURL: srv.URL})

	var chunks []string
	resp, err := g.ChatStream(context.Background(), ChatRequest{Messages: []Message{TextMessage(RoleUser, "x")}}, func(s string) {
		chunks = append(chunks, s)
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if got := strings.Join(chunks, ""); got != "Hello" {
		t.Errorf("streamed text = %q, want Hello", got)
	}
	if resp.Text() != "Hello" {
		t.Errorf("assembled text = %q", resp.Text())
	}
	tus := resp.ToolUses()
	if len(tus) != 1 || tus[0].Name != "ls" || tus[0].ID != "call_ls_1" {
		t.Fatalf("tool uses wrong: %+v", tus)
	}
	if resp.StopReason != StopToolUse {
		t.Errorf("stop = %q, want tool_use", resp.StopReason)
	}
	if resp.Usage.InputTokens != 9 || resp.Usage.OutputTokens != 3 {
		t.Errorf("usage wrong: %+v", resp.Usage)
	}
}

func TestGeminiChatStreamTruncated(t *testing.T) {
	srv := sseServer(t, []string{`{"candidates":[{"content":{"parts":[{"text":"par"}]}}]}`})
	defer srv.Close()
	g := NewGemini(GeminiConfig{APIKey: "k", Model: "m", BaseURL: srv.URL})
	_, err := g.ChatStream(context.Background(), ChatRequest{Messages: []Message{TextMessage(RoleUser, "x")}}, nil)
	if err == nil {
		t.Fatal("truncated stream must return an error")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./pkg/llm/ -run TestGeminiChatStream -v`
Expected: FAIL — stub emits one whole-text chunk; truncation returns no error.

- [ ] **Step 3: Implement streaming (and delete the stub from gemini.go)**

Create `pkg/llm/gemini_stream.go`:

```go
package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/revelara-ai/orion/pkg/llmclient"
)

// ChatStream issues a streaming generateContent request (?alt=sse). Each SSE
// event is a full gemResponse chunk: text parts are emitted as deltas,
// functionCall parts arrive whole, finishReason and usage ride the last chunk.
// Retries happen only BEFORE any text is emitted (same contract as the other
// adapters).
func (g *Gemini) ChatStream(ctx context.Context, req ChatRequest, onText func(string)) (*ChatResponse, error) {
	model := req.Model
	if model == "" {
		model = g.cfg.Model
	}
	maxTok := req.MaxTokens
	if maxTok <= 0 {
		maxTok = 4096
	}
	body, err := json.Marshal(g.toWire(req, maxTok))
	if err != nil {
		return nil, fmt.Errorf("%s: marshal request: %w", g.cfg.Name, err)
	}
	if onText == nil {
		onText = func(string) {}
	}
	path := "/models/" + model + ":streamGenerateContent?alt=sse"
	return llmclient.Do(ctx, g.rc, func(ctx context.Context) (*ChatResponse, error) {
		return g.doStream(ctx, path, body, onText)
	})
}

func (g *Gemini) doStream(ctx context.Context, path string, body []byte, onText func(string)) (*ChatResponse, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, g.cfg.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("accept", "text/event-stream")
	httpReq.Header.Set("x-goog-api-key", g.cfg.APIKey)
	resp, err := g.http.Do(httpReq)
	if err != nil {
		return nil, &llmclient.Retryable{Err: fmt.Errorf("%s: request: %w", g.cfg.Name, err)}
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		rb := make([]byte, 4096)
		n, _ := resp.Body.Read(rb)
		msg := string(rb[:n])
		switch {
		case resp.StatusCode == 429:
			return nil, &llmclient.RetryAfter{After: parseRetryAfter(resp.Header), Err: fmt.Errorf("%s: status 429", g.cfg.Name)}
		case resp.StatusCode >= 500:
			return nil, &llmclient.Retryable{Err: fmt.Errorf("%s: status %d", g.cfg.Name, resp.StatusCode)}
		case resp.StatusCode == 400 && isContextOverflow(msg):
			return nil, fmt.Errorf("%s: %w (status 400): %s", g.cfg.Name, ErrContextOverflow, truncate(msg, 200))
		default:
			return nil, fmt.Errorf("%s: status %d: %s", g.cfg.Name, resp.StatusCode, truncate(msg, 300))
		}
	}

	out := &ChatResponse{}
	var (
		text     strings.Builder
		nCalls   int
		emitted  bool
		complete bool
	)
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 8<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		var wr gemResponse
		if err := json.Unmarshal([]byte(data), &wr); err != nil {
			continue
		}
		if wr.ModelVersion != "" {
			out.Model = wr.ModelVersion
		}
		if wr.UsageMetadata.PromptTokenCount > 0 || wr.UsageMetadata.CandidatesTokenCount > 0 {
			out.Usage = Usage{
				InputTokens:          wr.UsageMetadata.PromptTokenCount,
				OutputTokens:         wr.UsageMetadata.CandidatesTokenCount,
				CacheReadInputTokens: wr.UsageMetadata.CachedContentTokenCount,
			}
		}
		if len(wr.Candidates) == 0 {
			continue
		}
		cand := wr.Candidates[0]
		for _, p := range cand.Content.Parts {
			switch {
			case p.FunctionCall != nil:
				nCalls++
				args := p.FunctionCall.Args
				if len(args) == 0 {
					args = json.RawMessage(`{}`)
				}
				out.Content = append(out.Content, ContentBlock{Type: BlockToolUse, ToolUse: &ToolUse{
					ID:    "call_" + p.FunctionCall.Name + "_" + strconv.Itoa(nCalls),
					Name:  p.FunctionCall.Name,
					Input: args,
				}})
			case p.Text != "":
				emitted = true
				text.WriteString(p.Text)
				onText(p.Text)
			}
		}
		if cand.FinishReason != "" {
			out.StopReason = gemStop(cand.FinishReason, nCalls > 0)
			complete = true
		}
	}
	if err := scanner.Err(); err != nil {
		if emitted {
			return nil, fmt.Errorf("%s: stream failed mid-output: %w", g.cfg.Name, err)
		}
		return nil, &llmclient.Retryable{Err: fmt.Errorf("%s: stream read: %w", g.cfg.Name, err)}
	}
	if !complete {
		if emitted {
			return nil, fmt.Errorf("%s: %w", g.cfg.Name, errTruncatedStream)
		}
		return nil, &llmclient.Retryable{Err: fmt.Errorf("%s: %w", g.cfg.Name, errTruncatedStream)}
	}
	// Text was accumulated across chunks — prepend it as one block, before the
	// tool_use blocks (mirrors the non-streaming block order).
	if text.Len() > 0 {
		out.Content = append([]ContentBlock{{Type: BlockText, Text: text.String()}}, out.Content...)
	}
	return out, nil
}
```

Then delete the `ChatStream` stub method from `pkg/llm/gemini.go`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./pkg/llm/ -run TestGemini -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
gofmt -l pkg/ && go vet ./pkg/... && go test ./pkg/...
git add pkg/llm/gemini.go pkg/llm/gemini_stream.go pkg/llm/gemini_stream_test.go
git commit -m "feat(llm): SSE streaming for the Gemini adapter"
```

---

### Task 6: Config facility — `pkg/llm/config`

**Files:**
- Create: `pkg/llm/config/config.go`
- Test: `pkg/llm/config/config_test.go`
- Modify: `pkg/llm/anthropic.go` (add `WithBaseURL`)

**Interfaces:**
- Consumes: `llm.NewAnthropic/NewOpenAI/NewGemini`, `llm.DefaultAnthropicModel`.
- Produces (Task 8 consumes all of these):
  - `type Provider struct { Type, BaseURL, APIKeyEnv string; ContextWindow, MaxTokens int }` (yaml tags: `type`, `base_url`, `api_key_env`, `context_window`, `max_tokens`)
  - `type Config struct { Model string; Providers map[string]Provider }`
  - `func Default() Config`
  - `func Parse(data []byte) (Config, error)` — parse + merge over Default()
  - `func LoadFile(path string) (Config, error)` — read + Parse; missing file returns the os.ReadFile error (caller branches on fs.ErrNotExist)
  - `func SplitRef(ref string) (provider, model string)` — first-slash split; no slash → `("", ref)`
  - `func Build(cfg Config, ref string) (prov llm.Provider, name, model string, err error)`
  - `func (a *llm.Anthropic) WithBaseURL(u string) *llm.Anthropic`

- [ ] **Step 1: Add `WithBaseURL` to the Anthropic adapter**

In `pkg/llm/anthropic.go`, after `NewAnthropic`:

```go
// WithBaseURL overrides the API endpoint (config base_url — proxies, gateways).
// Empty means keep the default. Returns a for chaining.
func (a *Anthropic) WithBaseURL(u string) *Anthropic {
	if u != "" {
		a.baseURL = strings.TrimRight(u, "/")
	}
	return a
}
```

- [ ] **Step 2: Write the failing tests**

Create `pkg/llm/config/config_test.go`:

```go
package config

import (
	"strings"
	"testing"

	"github.com/revelara-ai/orion/pkg/llm"
)

func TestDefaultRegistry(t *testing.T) {
	cfg := Default()
	for _, name := range []string{"anthropic", "ollama", "lmstudio", "gemini"} {
		if _, ok := cfg.Providers[name]; !ok {
			t.Errorf("default registry missing %q", name)
		}
	}
	if cfg.Providers["ollama"].BaseURL != "http://localhost:11434/v1" {
		t.Errorf("ollama base_url wrong: %q", cfg.Providers["ollama"].BaseURL)
	}
	if cfg.Providers["lmstudio"].BaseURL != "http://localhost:1234/v1" {
		t.Errorf("lmstudio base_url wrong: %q", cfg.Providers["lmstudio"].BaseURL)
	}
	if cfg.Providers["anthropic"].APIKeyEnv != "ANTHROPIC_API_KEY" {
		t.Errorf("anthropic api_key_env wrong: %q", cfg.Providers["anthropic"].APIKeyEnv)
	}
}

func TestParseMergesOverDefaults(t *testing.T) {
	cfg, err := Parse([]byte(`
model: lmstudio/qwen3-32b
providers:
  lmstudio:
    type: openai
    base_url: http://gpubox:1234/v1
    context_window: 32768
  openrouter:
    type: openai
    base_url: https://openrouter.ai/api/v1
    api_key_env: OPENROUTER_API_KEY
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model != "lmstudio/qwen3-32b" {
		t.Errorf("model = %q", cfg.Model)
	}
	if cfg.Providers["lmstudio"].BaseURL != "http://gpubox:1234/v1" {
		t.Errorf("user entry must override built-in: %q", cfg.Providers["lmstudio"].BaseURL)
	}
	if cfg.Providers["lmstudio"].ContextWindow != 32768 {
		t.Errorf("context_window not parsed: %d", cfg.Providers["lmstudio"].ContextWindow)
	}
	if _, ok := cfg.Providers["openrouter"]; !ok {
		t.Error("new user provider missing")
	}
	if _, ok := cfg.Providers["anthropic"]; !ok {
		t.Error("built-in anthropic must survive the merge")
	}
}

func TestParseMalformed(t *testing.T) {
	if _, err := Parse([]byte("model: [broken")); err == nil {
		t.Fatal("malformed YAML must error")
	}
}

func TestSplitRef(t *testing.T) {
	cases := []struct{ ref, wantProv, wantModel string }{
		{"lmstudio/qwen3-32b", "lmstudio", "qwen3-32b"},
		{"openrouter/meta-llama/llama-3.3-70b", "openrouter", "meta-llama/llama-3.3-70b"}, // first slash only
		{"claude-sonnet-5", "", "claude-sonnet-5"},
	}
	for _, c := range cases {
		p, m := SplitRef(c.ref)
		if p != c.wantProv || m != c.wantModel {
			t.Errorf("SplitRef(%q) = (%q,%q), want (%q,%q)", c.ref, p, m, c.wantProv, c.wantModel)
		}
	}
}

func TestBuild(t *testing.T) {
	cfg := Default()

	t.Run("openai local, no key needed", func(t *testing.T) {
		prov, name, model, err := Build(cfg, "lmstudio/qwen3-32b")
		if err != nil {
			t.Fatal(err)
		}
		if name != "lmstudio" || model != "qwen3-32b" || prov.Name() != "lmstudio" {
			t.Errorf("got %q %q %q", name, model, prov.Name())
		}
	})

	t.Run("unknown provider lists configured", func(t *testing.T) {
		_, _, _, err := Build(cfg, "nope/m")
		if err == nil || !strings.Contains(err.Error(), "anthropic") || !strings.Contains(err.Error(), "lmstudio") {
			t.Errorf("error must list configured providers: %v", err)
		}
	})

	t.Run("missing key env names the var", func(t *testing.T) {
		t.Setenv("ANTHROPIC_API_KEY", "")
		_, _, _, err := Build(cfg, "anthropic/claude-opus-4-8")
		if err == nil || !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") {
			t.Errorf("error must name the env var: %v", err)
		}
	})

	t.Run("anthropic with key, bare-model default provider", func(t *testing.T) {
		t.Setenv("ANTHROPIC_API_KEY", "sk-test")
		prov, name, model, err := Build(cfg, "claude-sonnet-5") // no slash → anthropic
		if err != nil {
			t.Fatal(err)
		}
		if name != "anthropic" || model != "claude-sonnet-5" || prov.Name() != "anthropic" {
			t.Errorf("got %q %q %q", name, model, prov.Name())
		}
	})

	t.Run("empty ref falls back to cfg.Model then built-in default", func(t *testing.T) {
		t.Setenv("ANTHROPIC_API_KEY", "sk-test")
		_, name, model, err := Build(cfg, "")
		if err != nil {
			t.Fatal(err)
		}
		if name != "anthropic" || model != llm.DefaultAnthropicModel {
			t.Errorf("default ref wrong: %q %q", name, model)
		}
	})

	t.Run("openai without base_url errors", func(t *testing.T) {
		bad := Default()
		bad.Providers["broken"] = Provider{Type: "openai"}
		_, _, _, err := Build(bad, "broken/m")
		if err == nil || !strings.Contains(err.Error(), "base_url") {
			t.Errorf("must demand base_url: %v", err)
		}
	})

	t.Run("gemini requires key", func(t *testing.T) {
		t.Setenv("GEMINI_API_KEY", "")
		_, _, _, err := Build(cfg, "gemini/gemini-2.5-pro")
		if err == nil || !strings.Contains(err.Error(), "GEMINI_API_KEY") {
			t.Errorf("must demand GEMINI_API_KEY: %v", err)
		}
	})

	t.Run("unknown type errors", func(t *testing.T) {
		bad := Default()
		bad.Providers["x"] = Provider{Type: "cohere"}
		_, _, _, err := Build(bad, "x/m")
		if err == nil || !strings.Contains(err.Error(), "cohere") {
			t.Errorf("must reject unknown type: %v", err)
		}
	})
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./pkg/llm/config/ -v`
Expected: FAIL — package doesn't exist yet.

- [ ] **Step 4: Implement the config package**

Create `pkg/llm/config/config.go`:

```go
// Package config is the provider-selection facility for the pkg/llm module: a
// YAML schema of named providers plus "provider/model" refs, resolved into a
// constructed llm.Provider. It is deliberately host-agnostic — no fixed file
// path, no host env-var conventions (Orion's live in internal/llmsetup). API
// keys are referenced by env-var NAME only and never stored.
package config

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/revelara-ai/orion/pkg/llm"
)

// Provider is one named entry in the providers map.
type Provider struct {
	Type          string `yaml:"type"`           // anthropic | openai | gemini
	BaseURL       string `yaml:"base_url"`       // required for type openai
	APIKeyEnv     string `yaml:"api_key_env"`    // env var NAME holding the key
	ContextWindow int    `yaml:"context_window"` // for models that don't advertise one
	MaxTokens     int    `yaml:"max_tokens"`     // default output cap
}

// Config is the parsed configuration.
type Config struct {
	Model     string              `yaml:"model"` // default "provider/model" ref
	Providers map[string]Provider `yaml:"providers"`
}

// Default is the built-in registry: always-resolvable names covering the
// default cloud provider and the standard local endpoints. User config entries
// with the same name override these.
func Default() Config {
	return Config{
		Providers: map[string]Provider{
			"anthropic": {Type: "anthropic", APIKeyEnv: "ANTHROPIC_API_KEY"},
			"ollama":    {Type: "openai", BaseURL: "http://localhost:11434/v1"},
			"lmstudio":  {Type: "openai", BaseURL: "http://localhost:1234/v1"},
			"gemini":    {Type: "gemini", APIKeyEnv: "GEMINI_API_KEY"},
		},
	}
}

// Parse parses YAML and merges it over Default(): the user's model ref wins,
// and user provider entries override same-named built-ins.
func Parse(data []byte) (Config, error) {
	var user Config
	if err := yaml.Unmarshal(data, &user); err != nil {
		return Config{}, err
	}
	cfg := Default()
	if user.Model != "" {
		cfg.Model = user.Model
	}
	for name, p := range user.Providers {
		cfg.Providers[name] = p
	}
	return cfg, nil
}

// LoadFile reads and parses a config file. A missing file surfaces the
// os.ReadFile error unchanged so callers can branch on fs.ErrNotExist and fall
// back to Default().
func LoadFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	cfg, err := Parse(data)
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// SplitRef splits a "provider/model" ref on the FIRST slash only — model ids
// may themselves contain slashes (OpenRouter's "meta-llama/llama-3.3-70b").
// A ref with no slash returns ("", ref).
func SplitRef(ref string) (provider, model string) {
	if i := strings.Index(ref, "/"); i >= 0 {
		return ref[:i], ref[i+1:]
	}
	return "", ref
}

// Build resolves ref against cfg and constructs the provider. An empty ref
// falls back to cfg.Model, then to the built-in Anthropic default. A bare
// model id (no slash) resolves against the "anthropic" provider for backward
// compatibility with ORION_MODEL=claude-….
func Build(cfg Config, ref string) (prov llm.Provider, name, model string, err error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		ref = cfg.Model
	}
	if ref == "" {
		ref = "anthropic/" + llm.DefaultAnthropicModel
	}
	name, model = SplitRef(ref)
	if name == "" {
		name = "anthropic"
	}
	p, ok := cfg.Providers[name]
	if !ok {
		return nil, "", "", fmt.Errorf("unknown provider %q (configured: %s)", name, strings.Join(providerNames(cfg), ", "))
	}
	var key string
	if p.APIKeyEnv != "" {
		key = strings.TrimSpace(os.Getenv(p.APIKeyEnv))
		if key == "" {
			return nil, "", "", fmt.Errorf("provider %q: set %s", name, p.APIKeyEnv)
		}
	}
	switch p.Type {
	case "anthropic":
		if key == "" {
			return nil, "", "", fmt.Errorf("provider %q: type anthropic requires api_key_env", name)
		}
		if model == "" {
			model = llm.DefaultAnthropicModel
		}
		return llm.NewAnthropic(key, model).WithBaseURL(p.BaseURL), name, model, nil
	case "openai":
		if p.BaseURL == "" {
			return nil, "", "", fmt.Errorf("provider %q: type openai requires base_url", name)
		}
		return llm.NewOpenAI(llm.OpenAIConfig{
			Name: name, BaseURL: p.BaseURL, APIKey: key, Model: model,
			ContextWindow: p.ContextWindow, MaxOutput: p.MaxTokens,
		}), name, model, nil
	case "gemini":
		if key == "" {
			return nil, "", "", fmt.Errorf("provider %q: type gemini requires api_key_env", name)
		}
		return llm.NewGemini(llm.GeminiConfig{
			Name: name, APIKey: key, Model: model, BaseURL: p.BaseURL,
			ContextWindow: p.ContextWindow, MaxOutput: p.MaxTokens,
		}), name, model, nil
	default:
		return nil, "", "", fmt.Errorf("provider %q: unknown type %q (want anthropic, openai, or gemini)", name, p.Type)
	}
}

func providerNames(cfg Config) []string {
	names := make([]string, 0, len(cfg.Providers))
	for n := range cfg.Providers {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./pkg/llm/config/ -v && go test ./pkg/llm/ -run TestPkgHasNoInternalDeps -v`
Expected: PASS (config tests + boundary still clean)

- [ ] **Step 6: Commit**

```bash
gofmt -l pkg/ && go vet ./pkg/... && go test ./pkg/...
git add pkg/llm/config/ pkg/llm/anthropic.go
git commit -m "feat(llm/config): YAML provider registry + provider/model ref resolution"
```

---

### Task 7: Capability probe — `llm.Probe` and `llm.AdvertisesTools`

**Files:**
- Create: `pkg/llm/probe.go`
- Test: `pkg/llm/probe_test.go`

**Interfaces:**
- Consumes: `Provider`, `ChatRequest`, `ToolUses()`, `ModelInfo`.
- Produces (Task 9 consumes):
  - `func Probe(ctx context.Context, prov Provider) (bool, error)` — one echo-tool round-trip; true only on a well-formed tool_use back.
  - `func AdvertisesTools(ctx context.Context, prov Provider, model string) bool` — true when `Models()` lists `model` with `Tools: true` (Anthropic/Gemini skip the live probe; OpenAI listings return Tools false → probe).

- [ ] **Step 1: Write the failing tests**

Create `pkg/llm/probe_test.go`:

```go
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// fakeProvider scripts Chat/Models responses for probe tests.
type fakeProvider struct {
	chat   *ChatResponse
	chatErr error
	models []ModelInfo
}

func (f *fakeProvider) Name() string { return "fake" }
func (f *fakeProvider) Chat(context.Context, ChatRequest) (*ChatResponse, error) {
	return f.chat, f.chatErr
}
func (f *fakeProvider) ChatStream(ctx context.Context, req ChatRequest, _ func(string)) (*ChatResponse, error) {
	return f.Chat(ctx, req)
}
func (f *fakeProvider) Models(context.Context) ([]ModelInfo, error) { return f.models, nil }
func (f *fakeProvider) Ping(context.Context) error                  { return nil }

func TestProbeToolCapable(t *testing.T) {
	f := &fakeProvider{chat: &ChatResponse{
		StopReason: StopToolUse,
		Content: []ContentBlock{{Type: BlockToolUse, ToolUse: &ToolUse{
			ID: "c1", Name: "echo", Input: json.RawMessage(`{"text":"ping"}`),
		}}},
	}}
	ok, err := Probe(context.Background(), f)
	if err != nil || !ok {
		t.Fatalf("Probe = (%v,%v), want (true,nil)", ok, err)
	}
}

func TestProbeProseOnlyModel(t *testing.T) {
	f := &fakeProvider{chat: &ChatResponse{
		StopReason: StopEndTurn,
		Content:    []ContentBlock{{Type: BlockText, Text: "I would call echo with ping"}},
	}}
	ok, err := Probe(context.Background(), f)
	if err != nil || ok {
		t.Fatalf("Probe = (%v,%v), want (false,nil)", ok, err)
	}
}

func TestProbeWrongToolOrBadJSON(t *testing.T) {
	f := &fakeProvider{chat: &ChatResponse{
		StopReason: StopToolUse,
		Content: []ContentBlock{{Type: BlockToolUse, ToolUse: &ToolUse{
			ID: "c1", Name: "echo", Input: json.RawMessage(`{"text":`), // malformed
		}}},
	}}
	if ok, _ := Probe(context.Background(), f); ok {
		t.Error("malformed tool input must not count as tool-capable")
	}
}

func TestProbeTransportError(t *testing.T) {
	f := &fakeProvider{chatErr: errors.New("connection refused")}
	if _, err := Probe(context.Background(), f); err == nil {
		t.Error("transport errors must surface, not read as incapable")
	}
}

func TestAdvertisesTools(t *testing.T) {
	f := &fakeProvider{models: []ModelInfo{
		{ID: "claude-opus-4-8", Tools: true},
		{ID: "qwen3-32b", Tools: false},
	}}
	if !AdvertisesTools(context.Background(), f, "claude-opus-4-8") {
		t.Error("advertised model must return true")
	}
	if AdvertisesTools(context.Background(), f, "qwen3-32b") {
		t.Error("Tools:false listing must return false")
	}
	if AdvertisesTools(context.Background(), f, "unlisted") {
		t.Error("unlisted model must return false (probe decides)")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./pkg/llm/ -run 'TestProbe|TestAdvertises' -v`
Expected: FAIL — `undefined: Probe`.

- [ ] **Step 3: Implement the probe**

Create `pkg/llm/probe.go`:

```go
package llm

import (
	"context"
	"encoding/json"
)

const probeToolName = "echo"

// Probe verifies the provider's active model can drive native tool calling
// with ONE minimal round-trip: it offers a single echo tool and instructs the
// model to call it. True only when a well-formed tool_use block comes back.
// A transport error surfaces as (false, err) — unreachable is not the same as
// incapable. Callers cache the result per session; the probe is stateless.
func Probe(ctx context.Context, prov Provider) (bool, error) {
	req := ChatRequest{
		System:   `You are a tool-use capability probe. Call the echo tool with text set to "ping". Respond ONLY by calling the tool — no prose.`,
		Messages: []Message{TextMessage(RoleUser, "Call the echo tool now.")},
		Tools: []Tool{{
			Name:        probeToolName,
			Description: "Echoes back the provided text.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`),
		}},
		// Generous cap: local reasoning models burn tokens thinking before the call.
		MaxTokens: 512,
	}
	resp, err := prov.Chat(ctx, req)
	if err != nil {
		return false, err
	}
	for _, tu := range resp.ToolUses() {
		if tu.Name == probeToolName && json.Valid(tu.Input) {
			return true, nil
		}
	}
	return false, nil
}

// AdvertisesTools reports whether prov lists model with native tool support.
// Advertised (Anthropic, Gemini) → callers skip the live probe: no extra call,
// no launch latency, no API spend. False/unlisted (OpenAI-compatible listings
// can't attest tools) → probe to find out.
func AdvertisesTools(ctx context.Context, prov Provider, model string) bool {
	ms, err := prov.Models(ctx)
	if err != nil {
		return false
	}
	for _, m := range ms {
		if m.ID == model && m.Tools {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./pkg/llm/ -run 'TestProbe|TestAdvertises' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
gofmt -l pkg/ && go vet ./pkg/... && go test ./pkg/...
git add pkg/llm/probe.go pkg/llm/probe_test.go
git commit -m "feat(llm): tool-capability probe + Models()-advertised fast path"
```

---

### Task 8: Orion glue — `internal/llmsetup`

**Files:**
- Create: `internal/llmsetup/llmsetup.go`
- Test: `internal/llmsetup/llmsetup_test.go`

**Interfaces:**
- Consumes: `config.LoadFile/Default/Build/SplitRef`, `llm.Provider`.
- Produces (Task 9 consumes):
  - `type Brain struct { Provider llm.Provider; ProviderName, Model, Ref, Reason string }` — `Provider == nil` means offline; `Reason` says why.
  - `func Select() Brain` — config + `ORION_MODEL` precedence, built provider or offline reason.
  - `func Rebuild(current Brain, arg string) (llm.Provider, string, error)` — `/model` switch; bare id stays on the current provider, `provider/model` switches; returns the new ref.
  - `func ListModels(ctx context.Context) []string` — `provider/model` refs aggregated across configured providers (best-effort).

- [ ] **Step 1: Write the failing tests**

Create `internal/llmsetup/llmsetup_test.go`:

```go
package llmsetup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setHome points HOME at a temp dir so ~/.orion/config.yaml is test-controlled.
func setHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

func writeConfig(t *testing.T, home, content string) {
	t.Helper()
	dir := filepath.Join(home, ".orion")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSelectZeroConfigWithKey(t *testing.T) {
	setHome(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")
	t.Setenv("ORION_MODEL", "")
	b := Select()
	if b.Provider == nil {
		t.Fatalf("want native brain, got offline: %s", b.Reason)
	}
	if b.ProviderName != "anthropic" || !strings.HasPrefix(b.Ref, "anthropic/") {
		t.Errorf("zero-config must select anthropic: %+v", b)
	}
}

func TestSelectZeroConfigNoKeyIsOffline(t *testing.T) {
	setHome(t)
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ORION_MODEL", "")
	b := Select()
	if b.Provider != nil {
		t.Fatal("no key + no config must be offline")
	}
	if !strings.Contains(b.Reason, "ANTHROPIC_API_KEY") {
		t.Errorf("reason must name the env var: %q", b.Reason)
	}
}

func TestSelectOrionModelEnvOverridesConfig(t *testing.T) {
	home := setHome(t)
	writeConfig(t, home, "model: lmstudio/qwen3-32b\n")
	t.Setenv("ORION_MODEL", "ollama/llama3.3")
	b := Select()
	if b.Provider == nil {
		t.Fatalf("offline: %s", b.Reason)
	}
	if b.ProviderName != "ollama" || b.Model != "llama3.3" {
		t.Errorf("ORION_MODEL must win over config model: %+v", b)
	}
}

func TestSelectConfigModel(t *testing.T) {
	home := setHome(t)
	writeConfig(t, home, "model: lmstudio/qwen3-32b\n")
	t.Setenv("ORION_MODEL", "")
	b := Select()
	if b.Provider == nil {
		t.Fatalf("offline: %s", b.Reason)
	}
	if b.Ref != "lmstudio/qwen3-32b" || b.Provider.Name() != "lmstudio" {
		t.Errorf("config model not honored: %+v", b)
	}
}

func TestSelectMalformedConfigIsOfflineWithReason(t *testing.T) {
	home := setHome(t)
	writeConfig(t, home, "model: [broken")
	b := Select()
	if b.Provider != nil {
		t.Fatal("malformed config must not silently fall back to defaults")
	}
	if !strings.Contains(b.Reason, "config") {
		t.Errorf("reason must mention the config problem: %q", b.Reason)
	}
}

func TestRebuildBareIDStaysOnProvider(t *testing.T) {
	setHome(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")
	cur := Brain{ProviderName: "anthropic", Model: "claude-opus-4-8"}
	prov, ref, err := Rebuild(cur, "claude-sonnet-5")
	if err != nil {
		t.Fatal(err)
	}
	if ref != "anthropic/claude-sonnet-5" || prov.Name() != "anthropic" {
		t.Errorf("bare id must stay on current provider: %s %s", ref, prov.Name())
	}
}

func TestRebuildRefSwitchesProvider(t *testing.T) {
	setHome(t)
	cur := Brain{ProviderName: "anthropic", Model: "claude-opus-4-8"}
	prov, ref, err := Rebuild(cur, "lmstudio/qwen3-32b")
	if err != nil {
		t.Fatal(err)
	}
	if ref != "lmstudio/qwen3-32b" || prov.Name() != "lmstudio" {
		t.Errorf("ref must switch provider: %s %s", ref, prov.Name())
	}
}

func TestRebuildUnknownProviderErrors(t *testing.T) {
	setHome(t)
	_, _, err := Rebuild(Brain{ProviderName: "anthropic"}, "nope/m")
	if err == nil {
		t.Fatal("unknown provider must error")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/llmsetup/ -v`
Expected: FAIL — package doesn't exist.

- [ ] **Step 3: Implement llmsetup**

Create `internal/llmsetup/llmsetup.go`:

```go
// Package llmsetup is the Orion-specific glue over the host-agnostic
// pkg/llm/config facility: it resolves ~/.orion/config.yaml, applies the
// ORION_MODEL env precedence (env > config model > built-in default), and
// hands the rest of Orion a ready llm.Provider. This is the ONLY place that
// policy lives — pkg/ stays publishable and Orion-free.
package llmsetup

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/revelara-ai/orion/pkg/llm"
	"github.com/revelara-ai/orion/pkg/llm/config"
)

// Brain is the resolved model selection. Provider == nil means Orion runs the
// offline deterministic conductor; Reason says why (missing key, bad config).
type Brain struct {
	Provider     llm.Provider
	ProviderName string // registry name, e.g. "anthropic", "lmstudio"
	Model        string // model id without the provider prefix
	Ref          string // "provider/model"
	Reason       string // set when Provider is nil
}

func configPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".orion", "config.yaml")
}

// loadConfig reads ~/.orion/config.yaml; a missing file is the zero-config
// path (defaults), but a MALFORMED file is an error the user must see — never
// silently fall back as if their config didn't exist.
func loadConfig() (config.Config, error) {
	p := configPath()
	if p == "" {
		return config.Default(), nil
	}
	cfg, err := config.LoadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return config.Default(), nil
	}
	if err != nil {
		return config.Config{}, err
	}
	return cfg, nil
}

// Select resolves the active brain: ORION_MODEL > config model > built-in
// default (anthropic). Build errors (missing key, unknown provider) come back
// as an offline Brain with the reason — callers fall back to the
// deterministic conductor exactly as they did pre-config.
func Select() Brain {
	cfg, err := loadConfig()
	if err != nil {
		return Brain{Reason: "config error: " + err.Error()}
	}
	ref := strings.TrimSpace(os.Getenv("ORION_MODEL"))
	prov, name, model, err := config.Build(cfg, ref)
	if err != nil {
		return Brain{Reason: err.Error()}
	}
	return Brain{Provider: prov, ProviderName: name, Model: model, Ref: name + "/" + model}
}

// Rebuild constructs a provider for a /model switch. A bare model id stays on
// the current provider; a "provider/model" ref switches providers. Returns
// the provider and its full ref.
func Rebuild(current Brain, arg string) (llm.Provider, string, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, "", err
	}
	ref := strings.TrimSpace(arg)
	if !strings.Contains(ref, "/") && current.ProviderName != "" {
		ref = current.ProviderName + "/" + ref
	}
	prov, name, model, err := config.Build(cfg, ref)
	if err != nil {
		return nil, "", err
	}
	return prov, name + "/" + model, nil
}

// ListModels aggregates Models() across all configured providers as
// "provider/model" refs, best-effort: providers that can't be built (missing
// key) or don't answer within the per-provider timeout are skipped.
func ListModels(ctx context.Context) []string {
	cfg, err := loadConfig()
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(cfg.Providers))
	for n := range cfg.Providers {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []string
	for _, n := range names {
		prov, _, _, err := config.Build(cfg, n+"/")
		if err != nil {
			continue
		}
		pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		ms, err := prov.Models(pctx)
		cancel()
		if err != nil {
			continue
		}
		for _, m := range ms {
			out = append(out, n+"/"+m.ID)
		}
	}
	return out
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/llmsetup/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
gofmt -l internal/llmsetup && go vet ./internal/llmsetup/ && go test ./internal/llmsetup/
git add internal/llmsetup/
git commit -m "feat(llmsetup): Orion glue — config path, ORION_MODEL precedence, brain selection"
```

---

### Task 9: Call-site integration — TUI, `orion change`, `orion status`, `/model`

**Files:**
- Modify: `internal/conductor/orionagent.go` (SetModel signature: rebuild returns error; add model lister)
- Modify: `internal/conductor/control.go` (switchModel: error-aware rebuild + cross-provider listing)
- Modify: `internal/tui/conversation.go` (conductorBrain via llmsetup; async probe warning)
- Modify: `cmd/orion/change.go` (llmsetup + probe gate)
- Modify: `cmd/orion/status.go` (brainLabel via llmsetup)
- Test: existing suites; update any test that calls `SetModel` or asserts the old labels

**Interfaces:**
- Consumes: `llmsetup.Select/Rebuild/ListModels`, `llm.Probe`, `llm.AdvertisesTools`.
- Produces: no new API — behavior change only. New `SetModel` signature: `SetModel(model string, rebuild func(string) (llm.Provider, error), list func(context.Context) []string)`.

- [ ] **Step 1: Write the failing test for switchModel's new behavior**

In `internal/conductor/control_test.go`, add:

```go
func TestSwitchModelRebuildError(t *testing.T) {
	a := NewOrionAgent(nil, nil, RoleTemplate{})
	a.SetModel("m1", func(string) (llm.Provider, error) {
		return nil, fmt.Errorf(`unknown provider "nope"`)
	}, nil)
	msg, err := a.switchModel("nope/m2")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "nope") || strings.Contains(msg, "MODEL:") {
		t.Errorf("failed switch must report the error and NOT emit the MODEL: sentinel: %q", msg)
	}
	if a.model != "m1" {
		t.Errorf("failed switch must not change the model: %q", a.model)
	}
}

func TestSwitchModelListsAcrossProviders(t *testing.T) {
	a := NewOrionAgent(nil, nil, RoleTemplate{})
	a.SetModel("m1", func(m string) (llm.Provider, error) { return nil, nil },
		func(context.Context) []string { return []string{"anthropic/claude-opus-4-8", "lmstudio/qwen3-32b"} })
	msg, err := a.switchModel("")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "lmstudio/qwen3-32b") {
		t.Errorf("empty-arg /model must list configured providers' models: %q", msg)
	}
}
```

(Add `"context"`, `"fmt"`, `"strings"` to the test file's imports if missing.)

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/conductor/ -run TestSwitchModel -v`
Expected: FAIL — compile error (SetModel has the old 2-arg signature).

- [ ] **Step 3: Update OrionAgent — SetModel + switchModel**

In `internal/conductor/orionagent.go`, change the struct fields and SetModel:

```go
	model    string                                   // current model id/ref (for /model)
	rebuild  func(model string) (llm.Provider, error) // rebuilds the provider for a new model (nil = no switch)
	list     func(ctx context.Context) []string       // lists "provider/model" refs across configured providers (nil = no listing)
```

```go
// SetModel records the current model and the factories that rebuild the
// provider for a new model / list available models, enabling the /model
// control op. Without rebuild, /model is show-only.
func (a *OrionAgent) SetModel(model string, rebuild func(model string) (llm.Provider, error), list func(ctx context.Context) []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.model, a.rebuild, a.list = model, rebuild, list
}
```

In `internal/conductor/control.go`, replace `switchModel`:

```go
// switchModel shows the current model + available models (empty arg) or
// rebuilds the provider for a new one. arg is a bare model id (stays on the
// current provider) or a provider/model ref (switches providers).
func (a *OrionAgent) switchModel(arg string) (string, error) {
	arg = strings.TrimSpace(arg)
	a.mu.Lock()
	defer a.mu.Unlock()
	if arg == "" {
		cur := a.model
		if cur == "" {
			cur = "(unknown)"
		}
		msg := "Current model: " + cur + ". Switch with /model <id> or /model <provider>/<id>."
		if a.list != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if models := a.list(ctx); len(models) > 0 {
				const maxShown = 30
				shown := models
				if len(shown) > maxShown {
					shown = shown[:maxShown]
				}
				msg += "\nAvailable: " + strings.Join(shown, ", ")
				if len(models) > maxShown {
					msg += fmt.Sprintf(" … (+%d more)", len(models)-maxShown)
				}
			}
		}
		return msg, nil
	}
	if a.rebuild == nil {
		return "This brain can't switch models at runtime.", nil
	}
	p, err := a.rebuild(arg)
	if err != nil {
		return "Couldn't switch to " + arg + ": " + err.Error(), nil
	}
	a.provider = p
	a.model = arg
	// The result carries a MODEL: sentinel the TUI parses to update its brain label.
	return "MODEL:" + arg + " · switched to " + arg, nil
}
```

(Ensure `context`, `fmt`, `time` are imported in each file.)

- [ ] **Step 4: Run the conductor tests, fix any other SetModel callers**

Run: `go test ./internal/conductor/ -run TestSwitchModel -v` → PASS.
Run: `grep -rn "SetModel(" --include='*.go' .` and update every caller to the 3-arg signature (tests may pass `nil` for `list`).
Run: `go build ./... ` — fix remaining compile errors the grep missed.

- [ ] **Step 5: Rewire the TUI (conversation.go)**

Replace `conductorBrain` (returns the provider too, for the probe) and add the probe command; update `Init`:

```go
// conductorBrain selects the brain via llmsetup (config file + ORION_MODEL +
// env keys): a native LLM "Orion" agent when a provider resolves, else the
// deterministic conductor (offline/CI fallback). Both satisfy acp.PromptFunc,
// so the TUI is identical for either.
func conductorBrain(oc *orchestrator.Conductor) (acpServer, string, llm.Provider, string) {
	role := conductor.RoleTemplate{Project: "orion"}
	b := llmsetup.Select()
	if b.Provider == nil {
		return conductor.NewConductorAgent(role, oc), "offline — " + b.Reason, nil, ""
	}
	agent := conductor.NewOrionAgent(b.Provider, oc, role)
	agent.SetModel(b.Ref, func(m string) (llm.Provider, error) {
		p, _, err := llmsetup.Rebuild(b, m)
		return p, err
	}, llmsetup.ListModels)
	return agent, "native · " + b.Ref, b.Provider, b.Model
}
```

In `Run` (conversation.go:1101), capture and store the extra returns:

```go
	brain, brainLabel, brainProv, brainModel := conductorBrain(oc)
	...
	conv.brainProvider = brainProv
	conv.brainModel = brainModel
```

Add the two fields to the `Conversation` struct (next to `bannerReport`):

```go
	brainProvider llm.Provider // active native provider (nil offline) — probed async at launch
	brainModel    string
```

Change `Init` and add the probe command:

```go
func (m Conversation) Init() tea.Cmd { return tea.Batch(m.input.Focus(), m.probeBrainCmd()) }

// probeBrainCmd asynchronously verifies the active brain can drive tools and
// surfaces a transcript warning when it can't (spec: probe + warn, fail only
// tool flows). Providers that advertise tool support via Models() (Anthropic,
// Gemini) are skipped — zero extra calls or launch cost on the default path.
// Runs in a bubbletea Cmd goroutine, so a slow local model never blocks launch.
func (m Conversation) probeBrainCmd() tea.Cmd {
	prov, model := m.brainProvider, m.brainModel
	if prov == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if llm.AdvertisesTools(ctx, prov, model) {
			return nil
		}
		ok, err := llm.Probe(ctx, prov)
		if ok {
			return nil
		}
		reason := "the model did not call the probe tool"
		if err != nil {
			reason = err.Error()
		}
		return CommandResultMsg{Text: "⚠ tools probe failed for " + model + " — agent flows may not work (" + reason + ")"}
	}
}
```

Add `"github.com/revelara-ai/orion/internal/llmsetup"` to imports; `os` may become unused in this file — remove it if so.

- [ ] **Step 6: Rewire `cmd/orion/change.go` (with the probe gate)**

Replace the key check + provider construction (lines 32-36 and 49):

```go
	ctx := context.Background()
	brain := llmsetup.Select()
	if brain.Provider == nil {
		fmt.Fprintln(os.Stderr, "orion change needs a model provider (it drives a model to write the change) — "+brain.Reason)
		return 1
	}
	// Tool gate (spec: fail only tool flows): orion change is entirely
	// tool-call-driven, so a model that can't demonstrate tool calling fails
	// fast HERE, before any worktree or baseline work starts.
	if !llm.AdvertisesTools(ctx, brain.Provider, brain.Model) {
		ok, perr := llm.Probe(ctx, brain.Provider)
		if perr != nil {
			fmt.Fprintf(os.Stderr, "orion change: probing %s: %v\n", brain.Ref, perr)
			return 1
		}
		if !ok {
			fmt.Fprintf(os.Stderr, "orion change: model %s did not demonstrate tool calling — orion change requires a tool-capable model\n", brain.Ref)
			return 1
		}
	}
```

…and where the provider was built (`provider := llm.NewAnthropic(...)`):

```go
	provider := brain.Provider
```

Remove the now-duplicate `ctx := context.Background()` further down, and drop unused imports.

- [ ] **Step 7: Rewire `cmd/orion/status.go`**

Replace `brainLabel`:

```go
// brainLabel mirrors the TUI's conductorBrain selection via llmsetup: native +
// model + provider when a brain resolves, else offline/deterministic.
func brainLabel() string {
	b := llmsetup.Select()
	if b.Provider == nil {
		return "offline — deterministic"
	}
	return "native · " + b.Model + " · " + b.ProviderName
}
```

Update imports (add llmsetup; the `llm` import may become unused — remove if so).

- [ ] **Step 8: Full build, test, fix label assertions**

```bash
go build ./... && go test ./...
grep -rn '"native · \|offline — set ANTHROPIC_API_KEY' --include='*_test.go' .
```

Update any test that asserts the old labels ("native · claude-…" without the provider prefix, or the old offline message) to the new forms. Rerun until green.

- [ ] **Step 9: Backward-compat smoke check (the spec's hard requirement)**

```bash
# no config file, key set → anthropic, exactly as today
env -u ORION_MODEL ANTHROPIC_API_KEY=sk-test ./orion status | grep -i brain
# expect: native · claude-opus-4-8 · anthropic

# no key, no config → offline
env -u ORION_MODEL -u ANTHROPIC_API_KEY ./orion status | grep -i brain
# expect: offline — deterministic

# zero config file, built-in registry resolves ollama
env ORION_MODEL=ollama/llama3.3 ./orion status | grep -i brain
# expect: native · llama3.3 · ollama
```

(Build the binary first with `go build -o orion ./cmd/orion` if needed.)

- [ ] **Step 10: Commit**

```bash
gofmt -l cmd internal && go vet ./... && go test ./...
git add -A
git commit -m "feat(llm): wire config-driven provider selection into TUI, orion change, and status [multi-provider]"
```

---

### Task 10: Acceptance — live end-to-end against a local model

**Files:**
- None (manual verification; optionally note results in the spec)

This is the spec's acceptance criterion: `orion change` end-to-end against LM Studio or Ollama with only a config file edit. It needs a running local server, so it's a human-in-the-loop step, not CI.

- [ ] **Step 1: Point Orion at a local model**

```bash
mkdir -p ~/.orion
cat > ~/.orion/config.yaml <<'EOF'
model: ollama/qwen3:32b
EOF
ollama list   # confirm a tool-capable model is pulled; qwen3, llama3.3, etc.
```

- [ ] **Step 2: Verify selection + probe**

```bash
./orion status          # expect: native · qwen3:32b · ollama
./orion change "add a comment to README.md explaining the project purpose"
```

Expected: either the change loop runs, or a fast, clear failure naming the model and the missing capability (if the local model can't drive tools). Both are correct behavior; a hang or a cryptic mid-loop failure is a bug.

- [ ] **Step 3: Verify the zero-config Anthropic path still works**

```bash
rm ~/.orion/config.yaml
ANTHROPIC_API_KEY=$YOUR_KEY ./orion status   # expect: native · claude-opus-4-8 · anthropic
```

- [ ] **Step 4: Record the result**

Append a short "Verified" note (date, model used, outcome) to the spec's Testing section and commit.

---

## Self-Review Notes

- **Spec coverage:** package layout (T1), OpenAI adapter (T2-3), Gemini adapter incl. synthesized ids + name-carrying functionResponse (T4-5), config schema/precedence/built-in registry/first-slash grammar (T6), probe + degradation policy (T7, wired in T9), call sites + /model cross-provider listing (T9), backward compat (T9 step 9), acceptance (T10), boundary test (T1), error-handling requirements (each adapter's `post` + config `Build` errors). Per-role model routing correctly absent (spec non-goal).
- **Type consistency:** `SetModel(model, rebuild func(string) (llm.Provider, error), list func(context.Context) []string)` used identically in T9 steps 1, 3, 5. `Build` returns `(llm.Provider, string, string, error)` in T6 and all T8 uses. Gemini ids `call_<name>_<n>` asserted in both T4 and T5 tests.
- **Known judgment calls:** `oaStop`/`gemStop` treat any response containing tool calls as `StopToolUse` regardless of finish_reason (defensive against local-server dialect drift); Gemini `Ping` is credential-presence-only (mirrors Anthropic); `ListModels` silently skips unreachable providers (listing is advisory).
