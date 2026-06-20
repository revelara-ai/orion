package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/revelara-ai/orion/internal/llmclient"
)

// ChatStream issues a streaming Messages request. Text deltas are delivered to
// onText as they arrive; the fully assembled response (content blocks incl.
// tool_use, stop reason, usage) is returned so the agent loop dispatches tools
// exactly as with Chat. Each attempt is retried/broken per llmclient policy — but
// only BEFORE any text is emitted (see doStream), so a retry never duplicates
// output already shown to the user.
func (a *Anthropic) ChatStream(ctx context.Context, req ChatRequest, onText func(string)) (*ChatResponse, error) {
	model := req.Model
	if model == "" {
		model = a.model
	}
	maxTok := req.MaxTokens
	if maxTok <= 0 {
		maxTok = 4096
	}
	w := a.toWire(req, model, maxTok)
	w.Stream = true
	body, err := json.Marshal(w)
	if err != nil {
		return nil, fmt.Errorf("anthropic: marshal request: %w", err)
	}
	if onText == nil {
		onText = func(string) {}
	}
	return llmclient.Do(ctx, a.rc, func(ctx context.Context) (*ChatResponse, error) {
		return a.doStream(ctx, body, onText)
	})
}

// streamBlock accumulates one content block across its deltas.
type streamBlock struct {
	typ     string
	text    strings.Builder
	id      string
	name    string
	jsonBuf strings.Builder // tool_use input, streamed as input_json_delta fragments
}

// sseEvent is the union of Anthropic streaming event shapes we read.
type sseEvent struct {
	Type    string `json:"type"`
	Index   int    `json:"index"`
	Message *struct {
		Model string `json:"model"`
		Usage struct {
			InputTokens              int `json:"input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
	ContentBlock *struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"content_block"`
	Delta *struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Usage *struct {
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (a *Anthropic) doStream(ctx context.Context, body []byte, onText func(string)) (*ChatResponse, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("x-api-key", a.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)
	httpReq.Header.Set("accept", "text/event-stream")

	resp, err := a.http.Do(httpReq)
	if err != nil {
		return nil, &llmclient.Retryable{Err: err} // connect failure, nothing emitted → retry
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == 429 || resp.StatusCode == 529:
		return nil, &llmclient.RetryAfter{After: parseRetryAfter(resp.Header), Err: fmt.Errorf("anthropic: status %d", resp.StatusCode)}
	case resp.StatusCode >= 500:
		return nil, &llmclient.Retryable{Err: fmt.Errorf("anthropic: status %d", resp.StatusCode)}
	case resp.StatusCode != 200:
		rb, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		return nil, fmt.Errorf("anthropic: status %d: %s", resp.StatusCode, truncate(string(rb), 300))
	}

	out := &ChatResponse{Model: a.model}
	blocks := map[int]*streamBlock{}
	var order []int
	var emitted bool
	// Once text is on screen, a retry would duplicate it. After emit, return
	// errors with %v (not %w) so the chain doesn't match context.DeadlineExceeded /
	// *Retryable — i.e. terminal, never retried.
	terminal := func(format string, err error) error {
		if emitted {
			return fmt.Errorf(format+": %v", err)
		}
		return &llmclient.Retryable{Err: err}
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 10<<20) // a single SSE data line can be large
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue // "event:" lines and blanks are redundant with data.type
		}
		data := strings.TrimSpace(line[len("data:"):])
		if data == "" || data == "[DONE]" {
			continue
		}
		var ev sseEvent
		if json.Unmarshal([]byte(data), &ev) != nil {
			continue // tolerate unknown/garbled events
		}
		switch ev.Type {
		case "message_start":
			if ev.Message != nil {
				if ev.Message.Model != "" {
					out.Model = ev.Message.Model
				}
				out.Usage.InputTokens = ev.Message.Usage.InputTokens
				out.Usage.CacheReadInputTokens = ev.Message.Usage.CacheReadInputTokens
				out.Usage.CacheCreationInputTokens = ev.Message.Usage.CacheCreationInputTokens
			}
		case "content_block_start":
			b := &streamBlock{}
			if ev.ContentBlock != nil {
				b.typ, b.id, b.name = ev.ContentBlock.Type, ev.ContentBlock.ID, ev.ContentBlock.Name
			}
			blocks[ev.Index] = b
			order = append(order, ev.Index)
		case "content_block_delta":
			b := blocks[ev.Index]
			if b == nil || ev.Delta == nil {
				continue
			}
			switch ev.Delta.Type {
			case "text_delta":
				b.text.WriteString(ev.Delta.Text)
				if ev.Delta.Text != "" {
					onText(ev.Delta.Text)
					emitted = true
				}
			case "input_json_delta":
				b.jsonBuf.WriteString(ev.Delta.PartialJSON)
			}
		case "message_delta":
			if ev.Delta != nil && ev.Delta.StopReason != "" {
				out.StopReason = normalizeStop(ev.Delta.StopReason)
			}
			if ev.Usage != nil {
				out.Usage.OutputTokens = ev.Usage.OutputTokens
			}
		case "error":
			msg := "stream error"
			if ev.Error != nil && ev.Error.Message != "" {
				msg = ev.Error.Message
			}
			return nil, terminal("anthropic: stream", fmt.Errorf("%s", msg))
		}
	}
	if err := sc.Err(); err != nil {
		return nil, terminal("anthropic: stream read", err)
	}

	for _, idx := range order {
		b := blocks[idx]
		switch b.typ {
		case "text":
			out.Content = append(out.Content, ContentBlock{Type: BlockText, Text: b.text.String()})
		case "tool_use":
			input := b.jsonBuf.String()
			if strings.TrimSpace(input) == "" {
				input = "{}"
			}
			out.Content = append(out.Content, ContentBlock{Type: BlockToolUse, ToolUse: &ToolUse{ID: b.id, Name: b.name, Input: json.RawMessage(input)}})
		}
	}
	if out.StopReason == StopUnknown {
		out.StopReason = StopEndTurn // a clean stream that omitted stop_reason ended the turn
	}
	return out, nil
}
