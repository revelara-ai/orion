package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
		rb, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
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
	// Once text is on screen, a retry would duplicate it. After emit, return
	// errors with %v (not %w) so the chain doesn't match context.DeadlineExceeded /
	// *Retryable — i.e. terminal, never retried. Mirrors anthropic_stream.go's terminal().
	terminal := func(format string, err error) error {
		wrapped := fmt.Errorf("%s: %v", format, err) // %v severs the chain (no DeadlineExceeded match)
		if emitted {
			return wrapped // already showed output → non-retryable, never duplicate
		}
		return &llmclient.Retryable{Err: wrapped}
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 10<<20) // a single SSE data line can be large
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
		// A server that fails mid-stream (context overflow, OOM) emits an SSE
		// error event and closes; swallowing it leaves only a generic
		// "truncated" — surface the server's own explanation instead. An
		// overflow maps to the ErrContextOverflow sentinel so the harness
		// shrinks-and-retries rather than re-sending the same over-long prompt.
		if msg := sseErrorMessage(data); msg != "" {
			if isContextOverflow(msg) {
				return nil, fmt.Errorf("%s: %w (mid-stream): %s", o.cfg.Name, ErrContextOverflow, truncate(msg, 200))
			}
			return nil, terminal(o.cfg.Name+": server error mid-stream", errors.New(truncate(msg, 300)))
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
		if errors.Is(err, bufio.ErrTooLong) {
			// Retrying re-streams the same oversized line — terminal, never retry.
			return nil, fmt.Errorf("%s: stream line exceeds buffer: %v", o.cfg.Name, err)
		}
		return nil, terminal(o.cfg.Name+": stream read", err)
	}
	if !complete {
		return nil, terminal(o.cfg.Name+": stream", errTruncatedStream)
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

// sseErrorMessage extracts the message from an SSE error event — either
// {"error":{"message":"..."}} (OpenAI shape) or {"error":"..."} (bare string,
// some local servers). Returns "" for anything that isn't an error event.
func sseErrorMessage(data string) string {
	var probe struct {
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal([]byte(data), &probe); err != nil || len(probe.Error) == 0 || string(probe.Error) == "null" {
		return ""
	}
	var obj struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(probe.Error, &obj); err == nil && obj.Message != "" {
		return obj.Message
	}
	var str string
	if err := json.Unmarshal(probe.Error, &str); err == nil && str != "" {
		return str
	}
	return string(probe.Error) // unknown shape: raw JSON beats silence
}
