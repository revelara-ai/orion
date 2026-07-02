package empirical

import (
	"context"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator/spec"
	"github.com/revelara-ai/orion/internal/proof/testsynth"
)

const execCLI = `package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

func run(args []string, stdin io.Reader, stdout, stderr io.Writer, env map[string]string) int {
	for _, a := range args {
		if v, ok := strings.CutPrefix(a, "--tz="); ok && v != "UTC" {
			fmt.Fprintf(stderr, "unknown zone %s\n", v)
			return 2
		}
	}
	fmt.Fprintln(stdout, time.Now().UTC().Format(time.RFC3339))
	return 0
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, map[string]string{}))
}
`

func ip(n int) *int { return &n }

// TestExecCasesProveAgainstRealBinary (or-v9f.3 L4): exec obligations execute
// against the SHIPPED process — built binary, real argv, real exit codes —
// through the sandbox, merged into the same obligation stream http cases use.
func TestExecCasesProveAgainstRealBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("builds + executes a binary; skipped in -short")
	}
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module orion-generated/cli\n\ngo 1.23\n")
	writeFile(t, dir, "main.go", execCLI)

	ok := spec.BehavioralCase{Kind: spec.KindExec, Exec: &spec.ExecCase{Steps: []spec.ExecStep{{
		Argv:   []string{"$BIN"},
		Expect: spec.StepExpect{Exit: ip(0), Stdout: []spec.StreamAssertion{{Kind: spec.StreamRFC3339UTC}}},
	}}}}
	bogus := spec.BehavioralCase{Kind: spec.KindExec, Exec: &spec.ExecCase{Steps: []spec.ExecStep{{
		Argv:   []string{"$BIN", "--tz=Bogus"},
		Expect: spec.StepExpect{Exit: ip(2), Stderr: []spec.StreamAssertion{{Kind: spec.StreamContains, Value: "Bogus"}}},
	}}}}
	ok.EnsureID()
	bogus.EnsureID()

	mode, pr, err := Prove(context.Background(), dir, testsynth.Contract{Cases: []spec.BehavioralCase{ok, bogus}})
	if err != nil {
		t.Fatal(err)
	}
	if !mode.Pass {
		t.Fatalf("the R1 CLI must prove empirically, got: %s", mode.Output)
	}
	for _, id := range []string{ok.ID, bogus.ID} {
		ob := pr.Cases[id]
		if !ob.Executed || !ob.Passed {
			t.Fatalf("exec obligation %s must execute+pass against the real binary, got %+v", id, ob)
		}
	}

	// A binary whose main exits 0 unconditionally fails the bogus-zone case
	// EMPIRICALLY even though run() semantics could hide it behaviorally — this
	// is the channel independence the design demands.
	dir2 := t.TempDir()
	writeFile(t, dir2, "go.mod", "module orion-generated/cli\n\ngo 1.23\n")
	writeFile(t, dir2, "main.go", strings.Replace(execCLI, "os.Exit(run(", "_ = run; os.Exit(zeroed(", 1)+`
func zeroed(args []string, stdin io.Reader, stdout, stderr io.Writer, env map[string]string) int {
	_ = run(args, stdin, stdout, stderr, env)
	return 0 // main-level drift: swallows the exit code
}
`)
	mode2, pr2, err := Prove(context.Background(), dir2, testsynth.Contract{Cases: []spec.BehavioralCase{bogus}})
	if err != nil {
		t.Fatal(err)
	}
	if mode2.Pass {
		t.Fatal("main-level exit-code drift must fail the empirical channel")
	}
	if ob := pr2.Cases[bogus.ID]; ob.Passed {
		t.Fatalf("the drifted binary must fail the bogus-zone obligation, got %+v", ob)
	}
}
