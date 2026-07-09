package conductor

import (
	"context"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/brownfield"
	"github.com/revelara-ai/orion/internal/contextstore"
)

func redChangeResult() ChangeResult {
	return ChangeResult{
		Branch: "orion-change-x",
		Regression: brownfield.RegressionResult{
			Held:   false,
			Reason: "the change regressed the existing tests (green→red) within scope",
			Before: brownfield.TestResult{Detected: true, Passed: true, Output: "ok  \texample.com/t\t0.1s"},
			After: brownfield.TestResult{Detected: true, Passed: false, Output: `# example.com/t [example.com/t.test]
t/config_test.go:90:14: undefined: filepath
FAIL	example.com/t [build failed]`},
		},
	}
}

// TestRenderChangeResultCarriesFailureDigest: a NOT-committed result must hand
// the model the evidence it needs to self-correct — the failing run's digest in
// the tool result, not just "held=false" (or-67av: gemma literally asked for
// the do-no-harm transcript and the loop had no way to give it).
func TestRenderChangeResultCarriesFailureDigest(t *testing.T) {
	out := renderChangeResult("add tests", redChangeResult())
	if !strings.Contains(out, "undefined: filepath") {
		t.Errorf("tool result must carry the failing run's digest:\n%s", out)
	}
	if !strings.Contains(out, "do-no-harm transcript") {
		t.Errorf("digest block must be labeled:\n%s", out)
	}
}

func TestRenderChangeResultOmitsDigestWhenHeld(t *testing.T) {
	res := redChangeResult()
	res.Regression.Held = true
	res.Regression.After.Passed = true
	res.Committed = true
	out := renderChangeResult("add tests", res)
	if strings.Contains(out, "do-no-harm transcript") {
		t.Errorf("a green result must not carry a failure digest:\n%s", out)
	}
}

// TestChangeResultFailureDigestPicksFailingRun: red baseline digests Before;
// green→red digests After.
func TestChangeResultFailureDigestPicksFailingRun(t *testing.T) {
	res := redChangeResult()
	if d := res.FailureDigest(); !strings.Contains(d, "undefined: filepath") {
		t.Errorf("green→red must digest the After run: %q", d)
	}
	res.Regression.Before = brownfield.TestResult{Detected: true, Passed: false, Output: "--- FAIL: TestPre (0.0s)\nFAIL\texample.com/t"}
	if d := res.FailureDigest(); !strings.Contains(d, "TestPre") {
		t.Errorf("red baseline must digest the Before run: %q", d)
	}
	green := ChangeResult{Regression: brownfield.RegressionResult{Held: true}}
	if green.FailureDigest() != "" {
		t.Error("held result has no failure digest")
	}
}

// TestEscalationCarriesFailureDigest: a green→red change's inbox escalation
// must persist the failing run's digest — `orion escalations show` becomes the
// transcript access the loop previously lacked.
func TestEscalationCarriesFailureDigest(t *testing.T) {
	if testing.Short() {
		t.Skip("git worktree + go test loop")
	}
	repo := gitInitGreenRepo(t)
	store := openStore(t)
	stub := &stubGen{files: map[string]string{"lib_test.go": "package t\nimport \"testing\"\nfunc TestBroken(t *testing.T){ undefinedCall() }\n"}}
	res, err := ChangeAndProve(context.Background(), repo, store, stub, "break the tests", nil, nil, nil)
	if err != nil {
		t.Fatalf("change: %v", err)
	}
	if res.Committed || res.EscalationID == "" {
		t.Fatalf("green→red must escalate with an inbox record: %+v", res)
	}
	ctx := context.Background()
	if err := store.WithTx(ctx, func(tx *contextstore.Tx) error {
		open, e := tx.Escalations().ListOpen(ctx)
		if e != nil {
			return e
		}
		for _, esc := range open {
			if esc.ID != res.EscalationID {
				continue
			}
			if !strings.Contains(esc.Detail, "do-no-harm transcript") || !strings.Contains(esc.Detail, "undefined") {
				t.Errorf("escalation detail must carry the failure digest, got:\n%s", esc.Detail)
			}
			return nil
		}
		t.Fatal("escalation not found in open inbox")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestRenderChangeResultCarriesRetryAffordance: a NOT-committed result must
// tell the model the expected next move IN the tool result — small models act
// on proximal instructions, not a system prompt 100K tokens back. Without
// this, capable-enough models diagnose the digest correctly and then give up
// or punt to the human (observed with gemma-4-e4b on or-4gib).
func TestRenderChangeResultCarriesRetryAffordance(t *testing.T) {
	out := renderChangeResult("add tests", redChangeResult())
	if !strings.Contains(out, "call build_change again") {
		t.Errorf("failed result must carry the retry affordance:\n%s", out)
	}
	if !strings.Contains(out, "fresh worktree") {
		t.Errorf("affordance must state retry is safe (fresh worktree):\n%s", out)
	}
}

func TestRenderChangeResultOmitsRetryAffordanceWhenCommitted(t *testing.T) {
	res := redChangeResult()
	res.Regression.Held = true
	res.Regression.After.Passed = true
	res.Committed = true
	if out := renderChangeResult("add tests", res); strings.Contains(out, "call build_change again") {
		t.Errorf("a committed result must not suggest retrying:\n%s", out)
	}
}
