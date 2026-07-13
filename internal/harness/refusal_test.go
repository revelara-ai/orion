package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/tools"
	"github.com/revelara-ai/orion/pkg/llm"
)

func refusalResp(text string) *llm.ChatResponse {
	r := &llm.ChatResponse{StopReason: llm.StopRefusal, Usage: llm.Usage{InputTokens: 3, OutputTokens: 3}}
	if text != "" {
		r.Content = []llm.ContentBlock{{Type: llm.BlockText, Text: text}}
	}
	return r
}

// TestRefusalStopsNamedAndUnretried (or-mvr.15 i+ii): a policy refusal — with
// or without text — surfaces as a RefusalError carrying the refusal text, is
// NEVER re-sent identically (the old empty-turn path), and never ends the
// turn as a normal final answer.
func TestRefusalStopsNamedAndUnretried(t *testing.T) {
	for _, tc := range []struct {
		name, text string
	}{
		{"empty policy block", ""},
		{"refusal with text", "I can't help with generating that content."},
	} {
		p := &scriptedProvider{resp: []*llm.ChatResponse{refusalResp(tc.text)}}
		var events []EventKind
		l := &Loop{Provider: p, Tools: tools.NewRegistry(), System: "t", Supervisor: Supervisor{MaxIterations: 5}}
		_, resp, err := l.Run(context.Background(), []llm.Message{llm.TextMessage(llm.RoleUser, "go")},
			func(e Event) { events = append(events, e.Kind) })

		var re *RefusalError
		if !errors.As(err, &re) {
			t.Fatalf("%s: want RefusalError, got %v (resp=%v)", tc.name, err, resp)
		}
		if re.Text != tc.text {
			t.Fatalf("%s: the refusal text must ride the error verbatim, got %q", tc.name, re.Text)
		}
		if strings.Contains(err.Error(), "max_tokens") {
			t.Fatalf("%s: a refusal must not be misdiagnosed as an output-budget problem: %v", tc.name, err)
		}
		if p.calls != 1 {
			t.Fatalf("%s: a refusal must never be re-sent identically, provider called %d times", tc.name, p.calls)
		}
		var sawRefusal bool
		for _, k := range events {
			if k == EventRefusal {
				sawRefusal = true
			}
		}
		if !sawRefusal {
			t.Fatalf("%s: the refusal must be a recorded event, got %v", tc.name, events)
		}
	}
}

// errProviderChat wraps scriptedProvider to return a fixed error.
type errChatProvider struct {
	scriptedProvider
	err error
}

func (p *errChatProvider) Chat(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
	p.calls++
	return nil, p.err
}
func (p *errChatProvider) ChatStream(ctx context.Context, req llm.ChatRequest, _ func(string)) (*llm.ChatResponse, error) {
	return p.Chat(ctx, req)
}

// TestBlockedPromptClassifiedAsRefusalNotOutage (or-mvr.15 iii, harness half):
// an llm.ErrRefused (blocked prompt) is a refusal — not ErrProvider (no
// breaker count) and NOT checkpointed (resuming would replay the exact
// blocked prompt).
func TestBlockedPromptClassifiedAsRefusalNotOutage(t *testing.T) {
	cp := &mapCheckpoint{m: map[string][]llm.Message{}}
	p := &errChatProvider{err: fmt.Errorf("gemini: prompt blocked (SAFETY): %w", llm.ErrRefused)}
	l := &Loop{Provider: p, Tools: tools.NewRegistry(), System: "t",
		Supervisor: Supervisor{MaxIterations: 5}, Checkpoint: cp, CheckpointKey: "gen:x"}
	_, _, err := l.Run(context.Background(), []llm.Message{llm.TextMessage(llm.RoleUser, "go")}, nil)

	var re *RefusalError
	if !errors.As(err, &re) {
		t.Fatalf("want RefusalError, got %v", err)
	}
	if errors.Is(err, ErrProvider) {
		t.Fatal("a policy block is not a dead dependency — it must not feed the breaker")
	}
	if !strings.Contains(re.StopDetail, "SAFETY") {
		t.Fatalf("the block reason must survive: %q", re.StopDetail)
	}
	if len(cp.m) != 0 {
		t.Fatal("a blocked prompt must not be checkpointed for replay")
	}
	_ = json.Valid // keep imports honest
}
