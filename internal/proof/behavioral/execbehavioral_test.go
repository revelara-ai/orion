package behavioral

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator/spec"
	"github.com/revelara-ai/orion/internal/proof/testsynth"
)

// cliArtifact is a handwritten R1-shaped CLI honoring the run()/thin-main
// generation contract: prints RFC3339 UTC time; --tz=Bogus exits 2 naming the zone.
const cliArtifact = `package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

func run(args []string, stdin io.Reader, stdout, stderr io.Writer, env map[string]string) int {
	zone := "UTC"
	for _, a := range args {
		if v, ok := strings.CutPrefix(a, "--tz="); ok {
			zone = v
		}
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		fmt.Fprintf(stderr, "unknown zone %s\n", zone)
		return 2
	}
	fmt.Fprintln(stdout, time.Now().In(loc).Format(time.RFC3339))
	return 0
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, map[string]string{}))
}
`

func intp(n int) *int { return &n }

func r1Cases() []spec.BehavioralCase {
	ok := spec.BehavioralCase{Kind: spec.KindExec, Exec: &spec.ExecCase{Steps: []spec.ExecStep{{
		Argv:   []string{"$BIN"},
		Expect: spec.StepExpect{Exit: intp(0), Stdout: []spec.StreamAssertion{{Kind: spec.StreamRFC3339UTC}}},
	}}}}
	bogus := spec.BehavioralCase{Kind: spec.KindExec, Exec: &spec.ExecCase{Steps: []spec.ExecStep{{
		Argv:   []string{"$BIN", "--tz=Bogus"},
		Expect: spec.StepExpect{Exit: intp(2), Stderr: []spec.StreamAssertion{{Kind: spec.StreamContains, Value: "Bogus"}}},
	}}}}
	ok.EnsureID()
	bogus.EnsureID()
	return []spec.BehavioralCase{ok, bogus}
}

// TestExecCasesProveCLIBehaviorally (or-v9f.3): the R1 CLI passes its exec
// obligations in-process through the synthesized corpus + embedded oracle; a
// wrong artifact fails with the oracle's detail; obligations flow through the
// existing marker parser untouched.
func TestExecCasesProveCLIBehaviorally(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles + runs a corpus; skipped in -short")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module orion-generated/cli\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(cliArtifact), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := r1Cases()
	contract := testsynth.Contract{Cases: cases, EntrySymbol: "run"}

	mr, err := Prove(context.Background(), dir, contract, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, cs := range cases {
		ob, ok := mr.Obligations[cs.ID]
		if !ok || !ob.Executed || !ob.Passed {
			t.Fatalf("exec obligation %s must execute and pass, got %+v (output:\n%s)", cs.ID, ob, mr.Output)
		}
	}
	if !mr.Pass && !mr.Inconclusive {
		t.Fatalf("R1 CLI must not fail behaviorally: %s", mr.Output)
	}

	// A wrong artifact (never errors on bad zones) fails the bogus-zone obligation.
	broken := strings.Replace(cliArtifact, `return 2`, `return 0`, 1)
	broken = strings.Replace(broken, "fmt.Fprintf(stderr,", "_ = zone; fmt.Fprintf(io.Discard,", 1)
	dir2 := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir2, "go.mod"), []byte("module orion-generated/cli\n\ngo 1.23\n"), 0o644)
	if err := os.WriteFile(filepath.Join(dir2, "main.go"), []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	mr2, err := Prove(context.Background(), dir2, contract, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ob := mr2.Obligations[cases[1].ID]; ob.Passed {
		t.Fatalf("the broken CLI must fail the bogus-zone obligation, got %+v", ob)
	}
}
