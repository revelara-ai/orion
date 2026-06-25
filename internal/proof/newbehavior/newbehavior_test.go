package newbehavior

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// fixtureModule writes a tiny stdlib-only Go module with package calc, returning the
// module root. ProveNewBehavior runs `go test` against it; no git is needed.
func fixtureModule(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testmod\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "calc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "calc", "calc.go"),
		[]byte("package calc\n\nfunc Add(a, b int) int { return a + b }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestProveNewBehavior_SynthTestPasses(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	dir := fixtureModule(t)
	mr, err := ProveNewBehavior(context.Background(), dir, []Case{
		{Modality: "synth_test", Synth: &SynthTest{Pkg: "calc", Call: "Add(2, 3)", Want: "5"}},
	})
	if err != nil {
		t.Fatalf("ProveNewBehavior: %v", err)
	}
	if !mr.Pass {
		t.Fatalf("expected Pass for a correct case; got %+v\noutput:\n%s", mr, mr.Output)
	}
	if len(mr.Obligations) != 1 {
		t.Fatalf("expected 1 obligation, got %d: %+v", len(mr.Obligations), mr.Obligations)
	}
	for id, st := range mr.Obligations {
		if !st.Executed || !st.Passed {
			t.Fatalf("obligation %s: executed=%v passed=%v, want both true", id, st.Executed, st.Passed)
		}
	}
}

func TestProveNewBehavior_WrongWantRejected(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	dir := fixtureModule(t)
	mr, err := ProveNewBehavior(context.Background(), dir, []Case{
		{Modality: "synth_test", Synth: &SynthTest{Pkg: "calc", Call: "Add(2, 3)", Want: "6"}},
	})
	if err != nil {
		t.Fatalf("ProveNewBehavior: %v", err)
	}
	if mr.Pass {
		t.Fatalf("expected NOT Pass for a wrong oracle; got %+v", mr)
	}
	// The case ran (executed) but failed — distinguishable from a coverage hole.
	for id, st := range mr.Obligations {
		if !st.Executed || st.Passed {
			t.Fatalf("obligation %s: executed=%v passed=%v, want executed=true passed=false", id, st.Executed, st.Passed)
		}
	}
}

func TestProveNewBehavior_LeavesNoArtifact(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	dir := fixtureModule(t)
	if _, err := ProveNewBehavior(context.Background(), dir, []Case{
		{Modality: "synth_test", Synth: &SynthTest{Pkg: "calc", Call: "Add(2, 3)", Want: "5"}},
	}); err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "calc", "orion_newbehavior*_test.go"))
	if len(matches) != 0 {
		t.Fatalf("synth test artifact leaked into the package: %v", matches)
	}
}
