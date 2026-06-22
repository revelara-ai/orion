package proof

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/proof/hazard/stpa"
	"github.com/revelara-ai/orion/internal/proof/testsynth"
	"github.com/revelara-ai/orion/internal/proof/truthalign"
	"github.com/revelara-ai/orion/internal/sandbox"
)

const jsonContract = "json"

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestKnownFaultyArtifactIsRejected: a conforming artifact converges Accept; a
// planted-defective artifact converges Reject (the canary).
func TestKnownFaultyArtifactIsRejected(t *testing.T) {
	ctx := context.Background()
	contract := testsynth.Contract{Route: "/time", Format: jsonContract, TimeZone: "UTC"}

	// Conforming artifact (Orion's real generator).
	good := t.TempDir()
	if _, err := sandbox.GenerateFixtureService(good, sandbox.GenSpec{Route: "/time", Port: 8080, Format: "json", TimeZone: "UTC"}); err != nil {
		t.Fatalf("generate good: %v", err)
	}
	goodOutcome, err := ProveBehavioral(ctx, good, contract)
	if err != nil {
		t.Fatalf("prove good: %v", err)
	}
	if goodOutcome.Verdict != truthalign.Accept {
		t.Fatalf("conforming artifact verdict = %s, want Accept\n%s", goodOutcome.Verdict, modeOutput(goodOutcome))
	}

	// Planted-defective artifact: compiles, but violates the ResponseContract.
	faulty := t.TempDir()
	writeFile(t, filepath.Join(faulty, "go.mod"), "module faulty\n\ngo 1.25\n")
	writeFile(t, filepath.Join(faulty, "main.go"), `package main
import "net/http"
func handleTime(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("nope")) }
func main() { http.HandleFunc("/time", handleTime); _ = http.ListenAndServe(":8080", nil) }
`)
	faultyOutcome, err := ProveBehavioral(ctx, faulty, contract)
	if err != nil {
		t.Fatalf("prove faulty: %v", err)
	}
	if faultyOutcome.Verdict != truthalign.Reject {
		t.Fatalf("defective artifact verdict = %s, want Reject", faultyOutcome.Verdict)
	}
}

func modeOutput(o truthalign.Outcome) string {
	if len(o.Modes) > 0 {
		return o.Modes[0].Output
	}
	return ""
}

// buildIsolationProbe compiles a static probe reporting whether it can read the
// corpus, spec, and store paths handed via env.
func buildIsolationProbe(t *testing.T) string {
	t.Helper()
	const src = `package main
import ("os")
func main(){
  r:=""
  for _,k:=range []string{"PROBE_CORPUS","PROBE_SPEC","PROBE_STORE"}{
    p:=os.Getenv(k)
    if p==""{continue}
    if _,e:=os.ReadFile(p);e==nil{r+=k+"=readable;"}else{r+=k+"=denied;"}
  }
  _=os.WriteFile("iso_result.txt",[]byte(r),0644)
}`
	srcDir := t.TempDir()
	writeFile(t, filepath.Join(srcDir, "main.go"), src)
	writeFile(t, filepath.Join(srcDir, "go.mod"), "module isoprobe\n\ngo 1.25\n")
	bin := filepath.Join(t.TempDir(), "isoprobe")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = srcDir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build probe: %v\n%s", err, out)
	}
	return bin
}

// TestHarnessIsolation: a generation-domain sandbox cannot read the held-out
// corpus, the spec, or the Context Store (Trust invariant 1/3).
func TestHarnessIsolation(t *testing.T) {
	be, err := sandbox.New("bwrap")
	if err != nil {
		t.Fatalf("bwrap backend required: %v", err)
	}
	probe := buildIsolationProbe(t)
	workdir := t.TempDir()

	// Held-out artifacts, all OUTSIDE the generator's sandbox binds.
	corpusDir := t.TempDir()
	corpusFile := filepath.Join(corpusDir, "orion_behavioral_test.go")
	writeFile(t, corpusFile, "// secret corpus")
	specFile := filepath.Join(t.TempDir(), "spec.json")
	writeFile(t, specFile, `{"secret":"spec"}`)
	storeFile := filepath.Join(t.TempDir(), "orion.db")
	writeFile(t, storeFile, "SECRET-STORE")

	res, err := be.Run(context.Background(), sandbox.Spec{
		Workdir: workdir,
		Argv:    []string{probe},
		ROBinds: []string{probe},
		Env: map[string]string{
			"PROBE_CORPUS": corpusFile,
			"PROBE_SPEC":   specFile,
			"PROBE_STORE":  storeFile,
		},
	})
	if err != nil {
		t.Fatalf("sandbox run: %v", err)
	}
	out, err := os.ReadFile(filepath.Join(workdir, "iso_result.txt"))
	if err != nil {
		t.Fatalf("probe wrote no result: %v (stderr=%s)", err, res.Stderr)
	}
	got := string(out)
	for _, want := range []string{"PROBE_CORPUS=denied", "PROBE_SPEC=denied", "PROBE_STORE=denied"} {
		if !strings.Contains(got, want) {
			t.Fatalf("generation domain could reach a proof artifact: %q", got)
		}
	}
}

// TestProveAllFastFailsOnNonCompilingCode: the diagnostics tier short-circuits the
// expensive proof — a non-compiling artifact returns a Reject carrying ONLY the
// diagnostics mode (behavioral/empirical/hazard never run), with the compiler error in
// the output for the refinement loop to feed back.
func TestProveAllFastFailsOnNonCompilingCode(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go vet")
	}
	ctx := context.Background()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module broken\n\ngo 1.25\n")
	writeFile(t, filepath.Join(dir, "main.go"), "package main\n\nfunc main() { _ = nope() }\n") // undefined: nope

	rep, err := ProveAll(ctx, dir, testsynth.Contract{Route: "/time", Format: jsonContract}, stpa.SkeletonModel())
	if err != nil {
		t.Fatalf("proveall: %v", err)
	}
	if rep.Outcome.Verdict == truthalign.Accept {
		t.Fatal("non-compiling code must not Accept")
	}
	if len(rep.Modes) != 1 || rep.Modes[0].Result.Mode != "diagnostics" {
		t.Fatalf("expected ONLY the diagnostics mode (expensive modes skipped), got %d: %+v", len(rep.Modes), rep.Modes)
	}
	if !strings.Contains(rep.Modes[0].Result.Output, "nope") {
		t.Fatalf("diagnostics output should carry the compiler error: %q", rep.Modes[0].Result.Output)
	}
}
