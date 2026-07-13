package conductor

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/revelara-ai/orion/internal/acp"
	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/pkg/llm"
)

// raceLLM is a concurrency-safe provider: every turn ends immediately. The
// shared fakeLLM mutates unguarded fields and would itself race.
type raceLLM struct {
	mu    sync.Mutex
	calls int
}

func (r *raceLLM) Name() string                                    { return "race" }
func (r *raceLLM) Models(context.Context) ([]llm.ModelInfo, error) { return nil, nil }
func (r *raceLLM) Ping(context.Context) error                      { return nil }
func (r *raceLLM) Chat(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
	r.mu.Lock()
	r.calls++
	n := r.calls
	r.mu.Unlock()
	return endTurn(fmt.Sprintf("ok %d", n)), nil
}
func (r *raceLLM) ChatStream(ctx context.Context, req llm.ChatRequest, _ func(string)) (*llm.ChatResponse, error) {
	return r.Chat(ctx, req)
}

// or-08l acceptance: concurrent same-session turns must not lose updates —
// without the per-session turn lock, Prompt's load-modify-store of the
// history drops turns and races the transcript write (run under -race).
func TestConcurrentSameSessionTurnsDoNotLoseUpdates(t *testing.T) {
	oc := orchestrator.NewWithStore(openStore(t))
	agent := NewOrionAgent(&raceLLM{}, oc, RoleTemplate{Project: "demo"})

	const turns = 8
	var wg sync.WaitGroup
	for i := 0; i < turns; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := agent.Prompt(context.Background(), "s1", fmt.Sprintf("turn %d", i),
				func(acp.Update) {},
				func(acp.PermissionRequest) (acp.PermissionResult, error) { return acp.PermissionResult{}, nil })
			if err != nil {
				t.Errorf("prompt %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	agent.mu.Lock()
	msgs := append([]llm.Message(nil), agent.sessions["s1"]...)
	agent.mu.Unlock()
	got := 0
	for _, m := range msgs {
		if m.Role == llm.RoleUser {
			for _, b := range m.Content {
				if b.Type == llm.BlockText && strings.HasPrefix(b.Text, "turn ") {
					got++
				}
			}
		}
	}
	if got != turns {
		t.Fatalf("lost update: %d of %d user turns survive in the session history", got, turns)
	}

	// Persist is async (off the response path) but must land: poll briefly.
	dir := filepath.Join(oc.Store().Dir(), "sessions")
	deadline := time.Now().Add(3 * time.Second)
	for {
		if ents, err := os.ReadDir(dir); err == nil && len(ents) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("transcript never persisted after concurrent turns")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// or-08l acceptance: a repeated transcript-write failure is logged ONCE — not
// silently swallowed, not once per turn.
func TestPersistFailureLoggedOnce(t *testing.T) {
	oc := orchestrator.NewWithStore(openStore(t))
	agent := NewOrionAgent(&raceLLM{}, oc, RoleTemplate{Project: "demo"})

	// Occupy the sessions path with a FILE so MkdirAll fails deterministically.
	if err := os.WriteFile(filepath.Join(oc.Store().Dir(), "sessions"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)

	msgs := []llm.Message{llm.TextMessage(llm.RoleUser, "hi")}
	agent.persistSession("s1", msgs)
	agent.persistSession("s1", msgs)

	if n := strings.Count(buf.String(), "transcript persist failed"); n != 1 {
		t.Fatalf("persist failure must be logged exactly once, got %d:\n%s", n, buf.String())
	}
}
