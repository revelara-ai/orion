package decomposer

import (
	"context"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
)

// mockProposer returns a fixed module set (no LLM) so the deterministic
// scaffolding is tested in isolation.
func mockProposer(mods []ProposedModule) ModuleProposer {
	return func(context.Context, spec.ExecutableSpec, string, []completeness.Dimension) ([]ProposedModule, error) {
		return mods, nil
	}
}

func fullFloorCovers() []string {
	out := make([]string, 0, 8)
	for _, d := range DefaultFloor() {
		out = append(out, string(d))
	}
	return out
}

// TestReconcileFloorRejectsDroppedDimension (or-809 G2): the reliability floor
// is un-droppable regardless of how the proposer sliced.
func TestReconcileFloorRejectsDroppedDimension(t *testing.T) {
	full := Epic{Tasks: []Task{{Key: "m", ProofObligation: "x", Covers: fullFloorCovers()}}}
	if err := ReconcileFloor(DefaultFloor(), full); err != nil {
		t.Fatalf("a plan covering every floor dim must pass: %v", err)
	}
	// Drop DimSecurity.
	partial := fullFloorCovers()[:len(fullFloorCovers())-1]
	dropped := Epic{Tasks: []Task{{Key: "m", ProofObligation: "x", Covers: partial}}}
	err := ReconcileFloor(DefaultFloor(), dropped)
	if err == nil || !strings.Contains(err.Error(), "floor gap") {
		t.Fatalf("a dropped floor dim must be rejected, got: %v", err)
	}
}

// TestCoverageDiffSuperset (or-809 I2): the shadow-cutover safety assertion —
// proposer coverage must be a superset of the oracle's.
func TestCoverageDiffSuperset(t *testing.T) {
	oracle := Epic{Tasks: []Task{{Covers: []string{"functional", "scale", "c1"}}}}
	// Superset: proposer covers everything the oracle does, plus more.
	rich := Epic{Tasks: []Task{{Covers: []string{"functional", "scale", "c1", "security"}}}}
	if ok, missing := CoverageDiff(rich, oracle); !ok || len(missing) != 0 {
		t.Fatalf("superset must hold, got ok=%v missing=%v", ok, missing)
	}
	// Not a superset: proposer drops "scale" and case "c1".
	thin := Epic{Tasks: []Task{{Covers: []string{"functional"}}}}
	ok, missing := CoverageDiff(thin, oracle)
	if ok {
		t.Fatal("dropping oracle-covered items must fail the superset check")
	}
	if len(missing) != 2 {
		t.Fatalf("missing must name both dropped items, got %v", missing)
	}
}

// TestProposeSynthesizesAcceptanceBookend (or-809 G3): Propose always appends a
// deterministic whole-intent acceptance module depending on every proposed
// module, and it strips any proposer-supplied "acceptance" (Orion owns it).
func TestProposeSynthesizesAcceptanceBookend(t *testing.T) {
	es := spec.ExecutableSpec{Intent: "build a thing"}
	mods := []ProposedModule{
		{Key: "a", Title: "A", ProofObligation: "proves a", Covers: []string{"functional"}},
		{Key: "b", Title: "B", ProofObligation: "proves b", Covers: []string{"scale"}, DependsOn: []string{"a"}},
		{Key: "acceptance", Title: "sneaky", ProofObligation: "should be dropped"},
	}
	epic, err := Propose(context.Background(), es, "http-service", DefaultFloor(), mockProposer(mods))
	if err != nil {
		t.Fatal(err)
	}
	var acc *Task
	sneaky := 0
	for i := range epic.Tasks {
		if epic.Tasks[i].Key == "acceptance" {
			acc = &epic.Tasks[i]
		}
		if epic.Tasks[i].ProofObligation == "should be dropped" {
			sneaky++
		}
	}
	if sneaky != 0 {
		t.Fatal("a proposer-supplied acceptance module must be stripped")
	}
	if acc == nil {
		t.Fatal("Propose must synthesize an acceptance bookend")
	}
	if len(acc.DependsOn) != 2 {
		t.Fatalf("acceptance must depend on every proposed module (a,b), got %v", acc.DependsOn)
	}
	if !strings.Contains(acc.ProofObligation, "build a thing") {
		t.Errorf("acceptance obligation must carry the whole intent, got: %q", acc.ProofObligation)
	}
	// It must carry every floor dimension so ReconcileFloor is satisfied by the
	// bookend even if the proposer under-covered (defense in depth).
	if err := ReconcileFloor(DefaultFloor(), epic); err != nil {
		t.Fatalf("the acceptance bookend must satisfy the floor: %v", err)
	}
}

// TestProposeRejectsEmpty: a proposer that returns nothing is an error, not a
// silent empty plan.
func TestProposeRejectsEmpty(t *testing.T) {
	if _, err := Propose(context.Background(), spec.ExecutableSpec{}, "http-service", DefaultFloor(), mockProposer(nil)); err == nil {
		t.Fatal("an empty proposal must error")
	}
}
