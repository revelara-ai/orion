package conductor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/memory"
	"github.com/revelara-ai/orion/internal/orchestrator"
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
	// Count GENERATION attempts only: the always-on post-commit alignment audit
	// (or-3p5.4) is one more single-message Chat that seqGen records, but under
	// the align role's system prompt — it is not a generator retry.
	var genAttempts int
	for _, sys := range gen.systems {
		if sys != alignSystemPrompt {
			genAttempts++
		}
	}
	if genAttempts != 2 {
		t.Fatalf("expected exactly 2 generator attempts, got %d (systems=%d)", genAttempts, len(gen.systems))
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

// TestNetNegativeRefinementTerminates (or-mvr.5): attempt 2 breaks MORE than
// attempt 1 — the detector terminates self-correction (no attempt 3 despite
// budget) and names the regression in the escalation.
func TestNetNegativeRefinementTerminates(t *testing.T) {
	if testing.Short() {
		t.Skip("runs regression gates per attempt")
	}
	t.Setenv("ORION_CHANGE_ATTEMPTS", "3")
	repo := t.TempDir()
	git := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("config", "user.name", "T")
	git("config", "user.email", "t@e.c")
	dogWrite(t, filepath.Join(repo, "go.mod"), "module dogfood2\n\ngo 1.23\n")
	dogWrite(t, filepath.Join(repo, "calc.go"), "package dogfood2\n\nfunc Add(a, b int) int { return a + b }\n\nfunc Mul(a, b int) int { return a * b }\n")
	dogWrite(t, filepath.Join(repo, "calc_test.go"), "package dogfood2\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 {\n\t\tt.Fatal(\"add\")\n\t}\n}\n\nfunc TestMul(t *testing.T) {\n\tif Mul(2, 3) != 6 {\n\t\tt.Fatal(\"mul\")\n\t}\n}\n")
	git("add", "-A")
	git("-c", "commit.gpgsign=false", "commit", "-q", "-m", "init")

	gen := &seqGen{attempts: []map[string]string{
		{"calc.go": "package dogfood2\n\nfunc Add(a, b int) int { return a - b }\n\nfunc Mul(a, b int) int { return a * b }\n"}, // breaks TestAdd
		{"calc.go": "package dogfood2\n\nfunc Add(a, b int) int { return a - b }\n\nfunc Mul(a, b int) int { return a + b }\n"}, // breaks BOTH
		{"note.go": "package dogfood2\n\nfunc Note() string { return \"never reached\" }\n"},                                    // must NOT run
	}}
	res, err := ChangeAndProve(context.Background(), repo, openStore(t), gen, "tweak the calculator", nil, nil, nil)
	if err != nil {
		t.Fatalf("ChangeAndProve: %v", err)
	}
	if res.Committed {
		t.Fatal("a degrading refinement must not commit")
	}
	if len(gen.systems) != 2 {
		t.Fatalf("the detector must terminate after the degrading attempt 2 (no attempt 3), got %d attempts", len(gen.systems))
	}
	if !strings.Contains(res.Reason, "degraded the artifact") || !strings.Contains(res.Reason, "test regressions 1→2") {
		t.Fatalf("the escalation must name the regression, got:\n%s", res.Reason)
	}
}

// TestChangeConsultsAndWritesMemory (or-3p5.13 acceptance 1+2): a seeded
// memory item rides the diff-generator prompt, and an ACCEPTED change writes
// outcome + decision items back.
func TestChangeConsultsAndWritesMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("runs regression gates")
	}
	repo := initDogfoodRepo(t)
	store := openStore(t)
	memDir := filepath.Join(store.Dir(), "memory")
	_ = os.MkdirAll(memDir, 0o700)
	seed, err := memory.Open(memDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seed.Write(context.Background(), memory.Item{
		Tier: memory.MTM, Kind: memory.KindFailure, TrustTier: memory.TrustProof, Heat: 1.0,
		Content: "causal analysis (change: add a Note helper): the Note helper previously clobbered Add",
	}); err != nil {
		t.Fatal(err)
	}
	_ = seed.Close()

	gen := &seqGen{attempts: []map[string]string{{"note.go": goodNote}}}
	res, err := ChangeAndProve(context.Background(), repo, store, gen, "add a Note helper", nil, nil, nil)
	if err != nil || !res.Committed {
		t.Fatalf("change: %v committed=%v %s", err, res.Committed, res.Reason)
	}
	// (1) Consult: the recalled item rode the generator prompt.
	if len(gen.systems) == 0 || !strings.Contains(gen.systems[0], "RECALLED MEMORY") || !strings.Contains(gen.systems[0], "previously clobbered Add") {
		t.Fatalf("the seeded failure must ride the generator prompt:\n%.600s", gen.systems[0])
	}
	// (2) Write on Accept: outcome + decision items exist.
	mem2, err := memory.Open(memDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mem2.Close() }()
	items, err := mem2.Retrieve(context.Background(), "proven change note helper decided constraints", memory.MTM)
	if err != nil {
		t.Fatal(err)
	}
	var sawOutcome, sawDecision bool
	for _, it := range items {
		if it.Kind == memory.KindPattern && strings.Contains(it.Content, `Proven change "add a Note helper"`) {
			sawOutcome = true
		}
		if it.Kind == memory.KindDecision && strings.Contains(it.Content, "func Note") {
			sawDecision = true
		}
	}
	if !sawOutcome || !sawDecision {
		t.Fatalf("an accepted change must write outcome+decision items (outcome=%v decision=%v)", sawOutcome, sawDecision)
	}
}

// TestChangeFailureWritesThenSecondRunConsults (or-3p5.13 acceptance 3): a
// failed change writes its causal analysis; a second run of the same intent
// carries that analysis in its generator prompt — consult, not re-derive.
func TestChangeFailureWritesThenSecondRunConsults(t *testing.T) {
	if testing.Short() {
		t.Skip("runs regression gates twice")
	}
	t.Setenv("ORION_CHANGE_ATTEMPTS", "1")
	repo := initDogfoodRepo(t)
	store := openStore(t)

	gen1 := &seqGen{attempts: []map[string]string{{"broken.go": breakingNote}}}
	res1, err := ChangeAndProve(context.Background(), repo, store, gen1, "add a Note helper", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res1.Committed {
		t.Fatal("the breaking change must not commit")
	}
	// or-kt5: a NOT-committed change reclaims its worktree AND branch (the
	// evidence is persisted; the checkout is not the record).
	if out, _ := exec.Command("git", "-C", repo, "branch", "--list", "orion-change-*").Output(); strings.TrimSpace(string(out)) != "" {
		t.Fatalf("a failed change must reclaim its branch, still present:\n%s", out)
	}

	gen2 := &seqGen{attempts: []map[string]string{{"note.go": goodNote}}}
	if _, err := ChangeAndProve(context.Background(), repo, store, gen2, "add a Note helper", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(gen2.systems) == 0 || !strings.Contains(gen2.systems[0], "RECALLED MEMORY") || !strings.Contains(gen2.systems[0], "regressed the existing tests") {
		t.Fatalf("the second run must consult the first run's failure analysis:\n%.600s", gen2.systems[0])
	}
}

// TestSessionMemoryBrief (or-3p5.13 acceptance 4): a non-empty store yields a
// bounded brief; empty or missing memory starts clean.
func TestSessionMemoryBrief(t *testing.T) {
	store := openStore(t)
	a := &OrionAgent{conductor: orchestrator.NewWithStore(store)}
	if got := a.sessionMemoryBrief(context.Background()); got != "" {
		t.Fatalf("an empty store must yield no brief, got %q", got)
	}
	_ = os.MkdirAll(filepath.Join(store.Dir(), "memory"), 0o700)
	seed, err := memory.Open(filepath.Join(store.Dir(), "memory"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seed.Write(context.Background(), memory.Item{Tier: memory.MTM, Kind: memory.KindPattern, TrustTier: memory.TrustProof, Heat: 1.0, Content: "Proven task T1: the time service serves /time"}); err != nil {
		t.Fatal(err)
	}
	_ = seed.Close()
	got := a.sessionMemoryBrief(context.Background())
	if !strings.Contains(got, "SESSION MEMORY BRIEF") || !strings.Contains(got, "the time service serves /time") {
		t.Fatalf("a seeded store must yield the brief, got %q", got)
	}
}
