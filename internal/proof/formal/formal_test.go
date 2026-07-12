package formal

import (
	"context"
	"os"
	"strings"
	"testing"
)

func readDirNames(dir string) ([]string, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(ents))
	for _, e := range ents {
		names = append(names, e.Name())
	}
	return names, nil
}

func requireFizzBee(t *testing.T) *FizzBee {
	t.Helper()
	if ResolveFizzBeeDir() == "" {
		t.Skip("fizzbee not installed (~/.orion/tools/fizzbee or ORION_FIZZBEE_DIR)")
	}
	return &FizzBee{}
}

// TestGuardedIntegrationQueueHoldsS1S2 (or-56c.1 acceptance, half 1): the
// hand-written model of Orion's integration queue WITH the lease guard proves
// both named invariants (S1 NoOverlappingIntegration, S2 SingletonIntegration)
// across the state space.
func TestGuardedIntegrationQueueHoldsS1S2(t *testing.T) {
	if testing.Short() {
		t.Skip("model checking execs the fizzbee toolchain")
	}
	r, err := requireFizzBee(t).Check(context.Background(), "testdata/integration_queue_guarded.fizz")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if r.Skipped != "" {
		t.Skip(r.Skipped)
	}
	if !r.Passed {
		t.Fatalf("guarded model must hold S1+S2, got invariant=%q\n%s", r.Invariant, r.Output)
	}
}

// TestUnguardedIntegrationQueueYieldsCounterexample (or-56c.1 acceptance, half
// 2): deleting the lease guard (the or-1lz latent bug under a parallelized
// integrateEpic) must produce the overlapping-integration counterexample —
// the checker names S1 and the trace shows both tasks mid-integration.
func TestUnguardedIntegrationQueueYieldsCounterexample(t *testing.T) {
	if testing.Short() {
		t.Skip("model checking execs the fizzbee toolchain")
	}
	r, err := requireFizzBee(t).Check(context.Background(), "testdata/integration_queue_unguarded.fizz")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if r.Skipped != "" {
		t.Skip(r.Skipped)
	}
	if r.Passed {
		t.Fatal("the unguarded model must FAIL — S1 is unenforceable without the lease guard")
	}
	if r.Invariant != "NoOverlappingIntegration" {
		t.Fatalf("the checker must name S1, got %q\n%s", r.Invariant, r.Output)
	}
	if !strings.Contains(r.Output, "integrating") {
		t.Fatalf("the counterexample trace must be in the output:\n%s", r.Output)
	}
}

// TestCheckerRunArtifactsStayOutOfTheTree: the checker writes graphs/html next
// to the model — the runner must confine them to its scratch workdir.
func TestCheckerRunArtifactsStayOutOfTheTree(t *testing.T) {
	if testing.Short() {
		t.Skip("model checking execs the fizzbee toolchain")
	}
	fb := requireFizzBee(t)
	if _, err := fb.Check(context.Background(), "testdata/integration_queue_guarded.fizz"); err != nil {
		t.Fatalf("check: %v", err)
	}
	if _, err := readDirNames("testdata"); err != nil {
		t.Fatal(err)
	}
	names, _ := readDirNames("testdata")
	for _, n := range names {
		if n == "out" {
			t.Fatal("checker run artifacts leaked into testdata/ — the model must be checked in a scratch workdir")
		}
	}
}

func TestSkipsCleanlyWhenToolchainAbsent(t *testing.T) {
	fb := &FizzBee{Dir: ""}
	t.Setenv("ORION_FIZZBEE_DIR", "")
	t.Setenv("HOME", t.TempDir()) // no ~/.orion/tools/fizzbee here
	r, err := fb.Check(context.Background(), "testdata/integration_queue_guarded.fizz")
	if err != nil {
		t.Fatalf("absent toolchain must skip, not error: %v", err)
	}
	if r.Skipped == "" {
		t.Fatal("absent toolchain must report Skipped")
	}
}

func TestApalacheEscapeHatchSkipsWhenAbsent(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	r, err := Apalache{}.Check(context.Background(), "testdata/whatever.tla")
	if err != nil || r.Skipped == "" {
		t.Fatalf("apalache absent must skip cleanly, got r=%+v err=%v", r, err)
	}
}
