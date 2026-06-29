package conductor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator"
)

// verdictWithBlocker is the MODIFIED behavior: failures now classify as "blocker" (was "critical").
const verdictWithBlocker = `package prov

type Verdict struct {
	Failures int
	Warnings int
}

func (v Verdict) Severity() string {
	if v.Failures > 0 {
		return "blocker"
	}
	if v.Warnings > 0 {
		return "warn"
	}
	return "ok"
}
`

// initSeverityRepo: Verdict.Severity() already returns "critical" for failures, with a test that
// ASSERTS that (TestSeverityCritical) plus an unrelated green test (TestVerdictZero).
func initSeverityRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("config", "user.name", "T")
	git("config", "user.email", "t@example.com")
	w := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w("go.mod", "module prov\n\ngo 1.23\n")
	w("verdict.go", verdictWithSeverity) // Severity() returns critical|warn|ok
	w("verdict_test.go", "package prov\n\nimport \"testing\"\n\n"+
		"func TestVerdictZero(t *testing.T) {\n\tif (Verdict{}).Severity() != \"ok\" {\n\t\tt.Fatal(\"zero should be ok\")\n\t}\n}\n\n"+
		"func TestSeverityCritical(t *testing.T) {\n\tif (Verdict{Failures: 1}).Severity() != \"critical\" {\n\t\tt.Fatal(\"failure should be critical\")\n\t}\n}\n")
	git("add", "-A")
	git("-c", "commit.gpgsign=false", "commit", "-q", "-m", "init")
	return dir
}

// TestChangeFlowDogfoodBehaviorChange (Slice C / dogfood #3): a change that INTENTIONALLY modifies
// existing behavior (failures: critical → blocker), with the superseded test DECLARED, lands —
// the regression gate skips the superseded test, the new behavior is proven, COMMITTED. The
// generator changes only the source (not the old test), so the supersede-by-skip is what saves it.
func TestChangeFlowDogfoodBehaviorChange(t *testing.T) {
	repo := initSeverityRepo(t)
	t.Chdir(repo)
	stub := &changeStub{
		cases:    []caseInput{{Modality: "synth_test", Pkg: ".", Call: "Verdict{Failures: 1}.Severity()", Want: `"blocker"`}},
		genFiles: map[string]string{"verdict.go": verdictWithBlocker},
	}
	cs := &changeSession{}
	r := specTools(orchestrator.NewWithStore(openStore(t)), stub, cs)
	mustDispatch(t, r, "submit_change_intent", `{"intent":"Severity() should classify failures as blocker, not critical"}`)
	mustDispatch(t, r, "propose_cases", `{}`)
	mustDispatch(t, r, "supersede_test", `{"test":"TestSeverityCritical"}`)
	mustDispatch(t, r, "ratify_cases", `{}`)
	out := mustDispatch(t, r, "build_change", `{}`)

	if !strings.Contains(out, "regression: do-no-harm held=true") {
		t.Errorf("regression should hold (the superseded test is skipped):\n%s", out)
	}
	if !strings.Contains(out, "verification: pass=true") {
		t.Errorf("the new behavior should be proven:\n%s", out)
	}
	if !strings.Contains(out, "COMMITTED") {
		t.Fatalf("a DECLARED behavior change must commit:\n%s", out)
	}
}

// TestChangeFlowBlocksUndeclaredBehaviorChange (Slice C safety): the SAME behavior change WITHOUT
// declaring the superseded test is BLOCKED — TestSeverityCritical genuinely regresses (asserts the
// old behavior) and nothing marked it intentional, so the gate rejects it. A real regression is
// still caught; supersession must be declared.
func TestChangeFlowBlocksUndeclaredBehaviorChange(t *testing.T) {
	repo := initSeverityRepo(t)
	t.Chdir(repo)
	stub := &changeStub{
		cases:    []caseInput{{Modality: "synth_test", Pkg: ".", Call: "Verdict{Failures: 1}.Severity()", Want: `"blocker"`}},
		genFiles: map[string]string{"verdict.go": verdictWithBlocker},
	}
	cs := &changeSession{}
	r := specTools(orchestrator.NewWithStore(openStore(t)), stub, cs)
	mustDispatch(t, r, "submit_change_intent", `{"intent":"Severity() returns blocker for failures"}`)
	mustDispatch(t, r, "propose_cases", `{}`)
	// no supersede_test → the change is undeclared
	mustDispatch(t, r, "ratify_cases", `{}`)
	out := mustDispatch(t, r, "build_change", `{}`)

	if strings.Contains(out, "COMMITTED") {
		t.Fatalf("an UNDECLARED behavior change must be blocked by the regression gate:\n%s", out)
	}
	if !strings.Contains(out, "held=false") {
		t.Errorf("regression should NOT hold (TestSeverityCritical regressed, not skipped):\n%s", out)
	}
}
