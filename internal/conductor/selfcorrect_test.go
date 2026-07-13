package conductor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/pkg/llm"
)

// seqGen is an attempt-sequenced generator stub: conversation N writes
// attempts[N]'s files (a fresh DiffGenerator loop per ChangeAndProve attempt
// starts with exactly one user message). It records each attempt's System
// prompt so tests can assert the failure digest reached the NEXT attempt.
type seqGen struct {
	attempts []map[string]string
	systems  []string
	turn     int
}

func (s *seqGen) Name() string                                    { return "seq" }
func (s *seqGen) Models(context.Context) ([]llm.ModelInfo, error) { return nil, nil }
func (s *seqGen) Ping(context.Context) error                      { return nil }
func (s *seqGen) Chat(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	return s.respond(req), nil
}
func (s *seqGen) ChatStream(_ context.Context, req llm.ChatRequest, _ func(string)) (*llm.ChatResponse, error) {
	return s.respond(req), nil
}
func (s *seqGen) respond(req llm.ChatRequest) *llm.ChatResponse {
	if len(req.Messages) == 1 { // a fresh generator conversation = a new attempt
		s.turn++
		s.systems = append(s.systems, req.System)
	}
	if len(req.Messages) > 1 {
		return &llm.ChatResponse{StopReason: llm.StopEndTurn,
			Content: []llm.ContentBlock{{Type: llm.BlockText, Text: "change complete"}}}
	}
	files := s.attempts[min(s.turn, len(s.attempts))-1]
	var blocks []llm.ContentBlock
	i := 0
	for path, content := range files {
		in, _ := json.Marshal(map[string]string{"path": path, "content": content})
		blocks = append(blocks, llm.ContentBlock{Type: llm.BlockToolUse, ToolUse: &llm.ToolUse{
			ID: fmt.Sprintf("w%d", i), Name: "write_file", Input: in}})
		i++
	}
	return &llm.ChatResponse{StopReason: llm.StopToolUse, Content: blocks}
}

const goodNote = "package dogfood\n\n// Note documents the module.\nfunc Note() string { return \"noted\" }\n"

// breakingNote fails the regression gate: it redefines an existing symbol the
// fixture repo already declares, so the package no longer compiles.
const breakingNote = "package dogfood\n\nfunc Add(a, b int) int { return a - b } // clobbers the existing Add\n"

// TestChangeSelfCorrectsFromFailureDigest (or-sk7u): attempt 1 breaks the
// build; the loop feeds the failure digest back and attempt 2 lands a good
// change — committed, with the digest visibly present in attempt 2's context
// and the attempts narrated in phase events.
func TestChangeSelfCorrectsFromFailureDigest(t *testing.T) {
	if testing.Short() {
		t.Skip("runs regression gates (go test) per attempt")
	}
	repo := initDogfoodRepo(t)
	store := openStore(t)
	gen := &seqGen{attempts: []map[string]string{
		{"broken.go": breakingNote},
		{"note.go": goodNote},
	}}

	var phases []string
	sink := PhaseSink(func(e PhaseEvent) { phases = append(phases, e.Phase+": "+e.Detail) })
	res, err := ChangeAndProve(context.Background(), repo, store, gen, "add a Note helper", nil, nil, sink)
	if err != nil {
		t.Fatalf("ChangeAndProve: %v", err)
	}
	if !res.Committed || res.Delivery != "deliver" {
		t.Fatalf("the self-corrected change must deliver, got committed=%v delivery=%q reason=%q", res.Committed, res.Delivery, res.Reason)
	}
	if len(gen.systems) != 2 {
		t.Fatalf("expected exactly 2 generator attempts, got %d", len(gen.systems))
	}
	// The judge never changes; the GENERATOR gets the evidence: attempt 2's
	// context must carry the failure digest from attempt 1.
	if !strings.Contains(gen.systems[1], "PREVIOUS ATTEMPT FAILED") {
		t.Fatalf("attempt 2 must receive the failure digest, system was:\n%.400s", gen.systems[1])
	}
	if strings.Contains(gen.systems[0], "PREVIOUS ATTEMPT FAILED") {
		t.Fatal("attempt 1 must not carry a phantom digest")
	}
	joined := strings.Join(phases, "\n")
	if !strings.Contains(joined, "attempt 2/") {
		t.Fatalf("phase events must narrate the retry, got:\n%s", joined)
	}
}

// TestChangeEscalatesWithAllDigestsAfterBudget (or-sk7u): every attempt fails —
// the loop stops at the configured budget and escalates with each attempt's
// digest so the human sees the whole trajectory.
func TestChangeEscalatesWithAllDigestsAfterBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("runs regression gates (go test) per attempt")
	}
	t.Setenv("ORION_CHANGE_ATTEMPTS", "2")
	repo := initDogfoodRepo(t)
	store := openStore(t)
	gen := &seqGen{attempts: []map[string]string{
		{"broken.go": breakingNote},
		{"broken.go": breakingNote},
	}}

	res, err := ChangeAndProve(context.Background(), repo, store, gen, "add a Note helper", nil, nil, nil)
	if err != nil {
		t.Fatalf("ChangeAndProve: %v", err)
	}
	if res.Committed || res.Delivery != "escalate" {
		t.Fatalf("exhausted attempts must escalate, got committed=%v delivery=%q", res.Committed, res.Delivery)
	}
	if len(gen.systems) != 2 {
		t.Fatalf("budget=2 must mean exactly 2 attempts, got %d", len(gen.systems))
	}
	if !strings.Contains(res.Reason, "attempt 1") || !strings.Contains(res.Reason, "attempt 2") {
		t.Fatalf("the escalation must carry every attempt's digest, got reason:\n%s", res.Reason)
	}
}
