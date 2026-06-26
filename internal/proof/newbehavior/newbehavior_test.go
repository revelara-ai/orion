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

func TestProveNewBehavior_CommandModality(t *testing.T) {
	if testing.Short() {
		t.Skip("runs subprocesses")
	}
	ctx := context.Background()
	dir := t.TempDir()

	cmdCase := func(cmd *Command) []Case { return []Case{{Modality: "command", Command: cmd}} }

	// exit 0 + stdout substring → pass
	if mr, _ := ProveNewBehavior(ctx, dir, cmdCase(&Command{Assert: []string{"echo", "answer=42"}, ExpectStdout: "answer=42"})); !mr.Pass {
		t.Fatalf("echo command should pass: %+v", mr)
	}
	// wrong stdout → reject (executed, not passed)
	if mr, _ := ProveNewBehavior(ctx, dir, cmdCase(&Command{Assert: []string{"echo", "answer=42"}, ExpectStdout: "nope"})); mr.Pass {
		t.Fatalf("wrong stdout must not pass: %+v", mr)
	}
	// regexp match
	if mr, _ := ProveNewBehavior(ctx, dir, cmdCase(&Command{Assert: []string{"echo", "count=7"}, ExpectStdout: `/count=\d+/`})); !mr.Pass {
		t.Fatalf("regex should match: %+v", mr)
	}
	// nonzero exit vs ExpectExit=0 → reject
	if mr, _ := ProveNewBehavior(ctx, dir, cmdCase(&Command{Assert: []string{"sh", "-c", "exit 3"}, ExpectExit: 0})); mr.Pass {
		t.Fatalf("nonzero exit must not pass when ExpectExit=0: %+v", mr)
	}
}

// TestProveNewBehavior_CommandBuildAndRun proves the build-the-binary-and-run shape (the
// motivating example; build+loopback-curl is the same with a curl in the Assert).
func TestProveNewBehavior_CommandBuildAndRun(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go build + subprocess")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module svcmod\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"),
		[]byte("package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"answer=42\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mr, err := ProveNewBehavior(context.Background(), dir, []Case{
		{Modality: "command", Command: &Command{
			Setup:        [][]string{{"go", "build", "-o", "svc", "."}},
			Assert:       []string{"./svc"},
			ExpectExit:   0,
			ExpectStdout: "answer=42",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !mr.Pass {
		t.Fatalf("build+run command should prove the new behavior: %+v\n%s", mr, mr.Output)
	}
}
