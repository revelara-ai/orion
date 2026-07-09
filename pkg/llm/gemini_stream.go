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
	"strconv"
	"strings"

	"github.com/revelara-ai/orion/pkg/llmclient"
)

// ChatStream issues a streaming generateContent request (?alt=sse). Each SSE
// event is a full gemResponse chunk: text parts are emitted as deltas,
// functionCall parts arrive whole, finishReason and usage ride the last chunk.
// Retries happen only BEFORE any text is emitted (same contract as the other
// adapters) — see doStream's terminal() closure.
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
		rb, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		msg := string(rb)
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
		emitted  bool // once true, a failure is terminal (never retry into duplicate output)
		complete bool
	)
	// Once text is on screen, a retry would duplicate it. After emit, return
	// errors with %v (not %w) so the chain doesn't match context.DeadlineExceeded /
	// *Retryable — i.e. terminal, never retried. Mirrors openai_stream.go /
	// anthropic_stream.go's terminal().
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
		var wr gemResponse
		if err := json.Unmarshal([]byte(data), &wr); err != nil {
			continue // tolerate keep-alive noise between events
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
		if errors.Is(err, bufio.ErrTooLong) {
			// Retrying re-streams the same oversized line — terminal, never retry,
			// regardless of emit state (the resend fails identically).
			return nil, fmt.Errorf("%s: stream line exceeds buffer: %v", g.cfg.Name, err)
		}
		return nil, terminal(g.cfg.Name+": stream read", err)
	}
	if !complete {
		return nil, terminal(g.cfg.Name+": stream", errTruncatedStream)
	}
	// Text was accumulated across chunks — prepend it as one block, before the
	// tool_use blocks. This places the aggregated text block first regardless of
	// where it fell among the original parts, which can differ from the
	// non-streaming part order (fromCandidate preserves that order, so a
	// functionCall preceding text there stays first). Tool dispatch is
	// unaffected — callers key off block type, not position.
	if text.Len() > 0 {
		out.Content = append([]ContentBlock{{Type: BlockText, Text: text.String()}}, out.Content...)
	}
	return out, nil
}
