package formal

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeModel(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "model.fizz")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCompileObligationsParsesBindings(t *testing.T) {
	p := writeModel(t, `
# obligation: NoOverlappingIntegration -> go test ./internal/integration/... -run 'TestTryAcquireLeaseRefusesOverlap'
# obligation: SingletonIntegration -> go test ./internal/integration/... -run TestOverlappingIntegrationsSerialize

always assertion NoOverlappingIntegration:
    return True

always assertion SingletonIntegration:
    return True
`)
	obs, err := CompileObligations(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(obs) != 2 {
		t.Fatalf("want 2 obligations, got %d: %+v", len(obs), obs)
	}
	if obs[0].Invariant != "NoOverlappingIntegration" || obs[0].RunPattern != "TestTryAcquireLeaseRefusesOverlap" || obs[0].Packages[0] != "./internal/integration/..." {
		t.Fatalf("first obligation parsed wrong: %+v", obs[0])
	}
}

// TestCompileObligationsRefusesUnboundInvariant: an `always assertion` with no
// declared obligation makes the design proof DECORATIVE — the compiler must
// name it and fail (the refinement chain is only as strong as its weakest
// binding).
func TestCompileObligationsRefusesUnboundInvariant(t *testing.T) {
	p := writeModel(t, `
# obligation: Covered -> go test ./x/... -run TestCovered

always assertion Covered:
    return True

always assertion Orphan:
    return True
`)
	_, err := CompileObligations(p)
	if err == nil || !strings.Contains(err.Error(), "Orphan") {
		t.Fatalf("unbound invariant must fail by name, got %v", err)
	}
}

// TestCompileObligationsRefusesDanglingBinding: an obligation naming an
// invariant the model does not assert is a stale/typo binding.
func TestCompileObligationsRefusesDanglingBinding(t *testing.T) {
	p := writeModel(t, `
# obligation: Ghost -> go test ./x/... -run TestGhost

always assertion Real:
    return True
# obligation: Real -> go test ./x/... -run TestReal
`)
	_, err := CompileObligations(p)
	if err == nil || !strings.Contains(err.Error(), "Ghost") {
		t.Fatalf("dangling binding must fail by name, got %v", err)
	}
}

// TestRefinementChainEndToEnd (or-56c.3 acceptance): the verified S1/S2 model's
// obligations resolve to real test functions and PASS against the code — the
// triad enforces what the checker verified, end to end on the dogfood case.
func TestRefinementChainEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the model checker + go test on the obligations")
	}
	fb := requireFizzBee(t)
	root := repoRoot(t)
	err := EnforceRefinement(context.Background(), root, "testdata/integration_queue_guarded.fizz", fb)
	if err != nil {
		t.Fatalf("refinement chain must hold on the dogfood model: %v", err)
	}
}

// TestEnforceRefinementRejectsUnresolvableObligation: a binding whose -run
// pattern matches no test function is a dead obligation (the or-crmw rot
// class) — enforcement must fail it, not skip it.
func TestEnforceRefinementRejectsUnresolvableObligation(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the model checker")
	}
	fb := requireFizzBee(t)
	p := writeModel(t, `
# obligation: AlwaysTrue -> go test ./internal/integration/... -run TestThisTestDoesNotExistAnywhere

action Init:
    x = 0

atomic action Step:
    require x == 0
    x = 1

atomic action Done:
    require x == 1
    pass

always assertion AlwaysTrue:
    return True
`)
	err := EnforceRefinement(context.Background(), repoRoot(t), p, fb)
	if err == nil || !strings.Contains(err.Error(), "TestThisTestDoesNotExistAnywhere") {
		t.Fatalf("dead obligation must fail by pattern, got %v", err)
	}
}

// TestEnforceRefinementRefusesFailingDesign: refinement is meaningless over a
// violated design — enforcement must stop at the checker verdict.
func TestEnforceRefinementRefusesFailingDesign(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the model checker")
	}
	err := EnforceRefinement(context.Background(), repoRoot(t), "testdata/integration_queue_unguarded.fizz", requireFizzBee(t))
	if err == nil || !strings.Contains(err.Error(), "design proof FAILED") {
		t.Fatalf("a failing design must refuse refinement, got %v", err)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}
