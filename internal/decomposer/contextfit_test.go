package decomposer

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
)

func fitFor(budget int, sizes map[string]int) FitEstimator {
	return func(m ProposedModule) FitEstimate {
		return FitEstimate{Tokens: sizes[m.Key], Budget: budget}
	}
}

// TestProposeFitSplitsOversizedModule (or-7et.3 acceptance): an oversized
// module is rejected with the NAMED error and routed back to the proposer
// once with an explicit split instruction; a compliant re-proposal passes and
// the plan ends up with more, smaller modules.
func TestProposeFitSplitsOversizedModule(t *testing.T) {
	es := spec.ExecutableSpec{Intent: "build a large service"}
	calls := 0
	var sawHint string
	mp := func(ctx context.Context, _ spec.ExecutableSpec, _ string, _ []completeness.Dimension) ([]ProposedModule, error) {
		calls++
		if hint, ok := SplitHintFrom(ctx); ok {
			sawHint = hint
			return []ProposedModule{
				{Key: "api", Title: "API", ProofObligation: "p", Covers: []string{"functional"}},
				{Key: "storage", Title: "Storage", ProofObligation: "p", Covers: []string{"data"}},
			}, nil
		}
		return []ProposedModule{{Key: "everything", Title: "The whole app", ProofObligation: "p", Covers: []string{"functional", "data"}}}, nil
	}
	fit := fitFor(1000, map[string]int{"everything": 5000, "api": 400, "storage": 400})

	epic, err := ProposeFit(context.Background(), es, "http-service", nil, mp, fit)
	if err != nil {
		t.Fatalf("a compliant re-proposal must pass: %v", err)
	}
	if calls != 2 {
		t.Fatalf("exactly one split retry, got %d proposer calls", calls)
	}
	if !strings.Contains(sawHint, "everything") || !strings.Contains(sawHint, "5000") {
		t.Fatalf("the split instruction must name the offender and its size: %q", sawHint)
	}
	var moduleCount int
	for _, task := range epic.Tasks {
		if task.Key != "acceptance" {
			moduleCount++
		}
	}
	if moduleCount != 2 {
		t.Fatalf("the plan must end with the split modules, got %d", moduleCount)
	}
}

// TestProposeFitEscalatesWhenStillOversized: a proposer that ignores the
// split instruction escalates with the named error — never a build.
func TestProposeFitEscalatesWhenStillOversized(t *testing.T) {
	es := spec.ExecutableSpec{Intent: "build a large service"}
	mp := func(context.Context, spec.ExecutableSpec, string, []completeness.Dimension) ([]ProposedModule, error) {
		return []ProposedModule{{Key: "everything", Title: "t", ProofObligation: "p", Covers: []string{"functional"}}}, nil
	}
	_, err := ProposeFit(context.Background(), es, "http-service", nil, mp, fitFor(1000, map[string]int{"everything": 5000}))
	if !errors.Is(err, ErrModuleOversized) {
		t.Fatalf("want ErrModuleOversized, got %v", err)
	}
	if !strings.Contains(err.Error(), "escalating") {
		t.Fatalf("the escalation must be named: %v", err)
	}
}

// TestProposeFitNilEstimatorIsNoop: without an estimator the gate never fires.
func TestProposeFitNilEstimatorIsNoop(t *testing.T) {
	es := spec.ExecutableSpec{Intent: "x"}
	mp := func(context.Context, spec.ExecutableSpec, string, []completeness.Dimension) ([]ProposedModule, error) {
		return []ProposedModule{{Key: "m", Title: "t", ProofObligation: "p", Covers: []string{"functional"}}}, nil
	}
	if _, err := ProposeFit(context.Background(), es, "http-service", nil, mp, nil); err != nil {
		t.Fatalf("nil estimator must be a no-op: %v", err)
	}
}
