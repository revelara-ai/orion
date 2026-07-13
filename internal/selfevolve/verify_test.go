package selfevolve

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/memory"
)

// TestVerifyAndPromote (or-gb1.4 acceptance): a passing deterministic check
// produces a NEW TrustProof item whose provenance cites the executed check and
// the source hypothesis — while the original generation row's trust tier (and
// candidate quarantine) never mutates. A failing check leaves everything
// untouched.
func TestVerifyAndPromote(t *testing.T) {
	ctx := context.Background()
	mem, err := memory.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open memory: %v", err)
	}
	defer func() { _ = mem.Close() }()

	hypID, err := mem.Write(ctx, memory.Item{
		Tier:      memory.LTM,
		Kind:      memory.KindRule,
		Content:   "distilled rule: always bound outbound calls with a timeout",
		TrustTier: memory.TrustGeneration,
		Candidate: true,
		Heat:      0.5,
	})
	if err != nil {
		t.Fatalf("write hypothesis: %v", err)
	}

	check := Check{Name: "timeout-lint", Cmd: []string{"true"}}
	okRunner := func(context.Context, Check) error { return nil }

	newID, err := VerifyAndPromote(ctx, mem, hypID, check, okRunner)
	if err != nil {
		t.Fatalf("VerifyAndPromote: %v", err)
	}
	if newID == hypID {
		t.Fatal("verification must mint a NEW item, not touch the original")
	}
	got, ok, err := mem.Get(ctx, newID)
	if err != nil || !ok {
		t.Fatalf("verified item missing: ok=%v err=%v", ok, err)
	}
	if got.TrustTier != memory.TrustProof || got.Kind != memory.KindVerified {
		t.Fatalf("verified item must be TrustProof kind=%s, got %+v", memory.KindVerified, got)
	}
	for _, want := range []string{"timeout-lint", hypID, "always bound outbound calls"} {
		if !strings.Contains(got.Content, want) {
			t.Fatalf("provenance must cite %q, got:\n%s", want, got.Content)
		}
	}

	// First-writer-wins: the original row's classification is intact.
	orig, ok, err := mem.Get(ctx, hypID)
	if err != nil || !ok {
		t.Fatalf("hypothesis vanished: ok=%v err=%v", ok, err)
	}
	if orig.TrustTier != memory.TrustGeneration || !orig.Candidate {
		t.Fatalf("the original hypothesis row must never be re-classified, got %+v", orig)
	}

	// A failing check confirms nothing and writes nothing.
	failRunner := func(context.Context, Check) error { return errors.New("assertion failed") }
	if _, err := VerifyAndPromote(ctx, mem, hypID, Check{Name: "bad", Cmd: []string{"false"}}, failRunner); err == nil {
		t.Fatal("a failing check must not promote")
	}
	after, _, _ := mem.Get(ctx, hypID)
	if after.TrustTier != memory.TrustGeneration || !after.Candidate {
		t.Fatalf("a failed verification must leave the hypothesis untouched, got %+v", after)
	}

	// An already-proof item is not a hypothesis — refuse.
	if _, err := VerifyAndPromote(ctx, mem, newID, check, okRunner); err == nil {
		t.Fatal("re-verifying a proof-tier item must refuse")
	}
}

// TestCmdRunnerExecutesUnderScrubbedEnv: the default runner actually executes
// the command and reports non-zero exits as failures.
func TestCmdRunnerExecutesUnderScrubbedEnv(t *testing.T) {
	run := CmdRunner(10_000_000_000) // 10s
	if err := run(context.Background(), Check{Name: "ok", Cmd: []string{"true"}}); err != nil {
		t.Fatalf("true must pass: %v", err)
	}
	if err := run(context.Background(), Check{Name: "no", Cmd: []string{"false"}}); err == nil {
		t.Fatal("false must fail the check")
	}
}
