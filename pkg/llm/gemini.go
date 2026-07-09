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
	SystemInstruction *gemContent  `json:"systemInstruction,omitempty"`
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
			decls = append(decls, gemFuncDecl{Name: t.Name, Description: t.Description, Parameters: gemToolSchema(t.InputSchema)})
		}
		w.Tools = []gemTools{{FunctionDeclarations: decls}}
	}
	return w
}

// gemToolSchema adapts a tool's JSON schema to Gemini's dialect: Gemini rejects
// OBJECT schemas whose properties are absent or empty (the shape Orion's no-arg
// tools carry), and the correct declaration for a parameterless function omits
// parameters entirely. Schemas with real properties pass through unchanged;
// unparseable ones pass through so the server reports them with context.
func gemToolSchema(schema json.RawMessage) json.RawMessage {
	if len(schema) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(schema, &m); err != nil || m == nil {
		return schema
	}
	if props, ok := m["properties"].(map[string]any); !ok || len(props) == 0 {
		return nil
	}
	return schema
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
// models support function calling natively → Tools true. Amendment 1: wrapped
// in llmclient.Do per the provider HTTP constraint.
func (g *Gemini) Models(ctx context.Context) ([]ModelInfo, error) {
	return llmclient.Do(ctx, g.rc, func(ctx context.Context) ([]ModelInfo, error) {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, g.cfg.BaseURL+"/models", nil)
		if err != nil {
			return nil, err
		}
		httpReq.Header.Set("x-goog-api-key", g.cfg.APIKey)
		resp, err := g.http.Do(httpReq)
		if err != nil {
			return nil, &llmclient.Retryable{Err: fmt.Errorf("%s: list models: %w", g.cfg.Name, err)}
		}
		defer resp.Body.Close()
		rb, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		switch {
		case resp.StatusCode == 429:
			return nil, &llmclient.RetryAfter{After: parseRetryAfter(resp.Header), Err: fmt.Errorf("%s: status 429", g.cfg.Name)}
		case resp.StatusCode >= 500:
			return nil, &llmclient.Retryable{Err: fmt.Errorf("%s: status %d", g.cfg.Name, resp.StatusCode)}
		case resp.StatusCode != 200:
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
	})
}

// Ping verifies a credential is present (cheap, network-free — mirrors the
// Anthropic adapter; a real call happens on the first Chat).
func (g *Gemini) Ping(context.Context) error {
	if g.cfg.APIKey == "" {
		return fmt.Errorf("%s: no API key (set the configured api_key_env, e.g. GEMINI_API_KEY)", g.cfg.Name)
	}
	return nil
}

// ChatStream is implemented in gemini_stream.go.
