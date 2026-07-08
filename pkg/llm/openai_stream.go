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
