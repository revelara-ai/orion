package behavioral

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator/spec"
	"github.com/revelara-ai/orion/internal/proof/testsynth"
)

func pyUnitCase(id, pkg, call, want, wantErrRE string) spec.BehavioralCase {
	st := spec.UnitStep{Call: call, Want: want, WantErrRE: wantErrRE}
	return spec.BehavioralCase{ID: id, Kind: spec.KindUnit, Unit: &spec.UnitCase{Pkg: pkg, Steps: []spec.UnitStep{st}}}
}

func requirePySandbox(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap not on host")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not on host")
	}
}

// writeCalcLib stages a tiny python library artifact the corpus imports.
func writeCalcLib(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "calclib"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := "def add(a, b):\n    return a + b\n\n\ndef div(a, b):\n    return a / b\n"
	if err := os.WriteFile(filepath.Join(dir, "calclib", "__init__.py"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestPyProverRegistered (or-4y7.9): python resolves to the py prover.
func TestPyProverRegistered(t *testing.T) {
	if p := proverFor("python"); p == nil || p.Language() != "python" {
		t.Fatal("the python behavioral prover must be registered")
	}
}

// TestPyCorpusShape (or-4y7.9): the authored corpus imports the case's module,
// carries the obligation markers, and asserts the declared want.
func TestPyCorpusShape(t *testing.T) {
	files, err := proverFor("python").SynthesizeCorpus(testsynth.Contract{
		Language: "python",
		Cases:    []spec.BehavioralCase{pyUnitCase("case1", "calclib", "add(2, 3)", "5", "")},
	}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	src := files["orion_behavioral_test.py"]
	for _, want := range []string{
		`importlib.import_module("calclib")`,
		"ORION_OBLIGATION_RUN:case1", "ORION_OBLIGATION_PASS:case1",
		"self.assertEqual(got, want)", "unittest",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("corpus missing %q:\n%s", want, src)
		}
	}
}

// TestPyCorpusRefusals (or-4y7.9): no unit cases refuses loudly (the tracer
// proves through unit obligations), and a non-identifier pkg is rejected —
// it is interpolated into generated source.
func TestPyCorpusRefusals(t *testing.T) {
	if _, err := proverFor("python").SynthesizeCorpus(testsynth.Contract{Language: "python"}, t.TempDir()); err == nil || !strings.Contains(err.Error(), "unit cases") {
		t.Fatalf("a contract without unit cases must refuse, got %v", err)
	}
	bad := pyUnitCase("x", "os; import shutil #", "add(1,2)", "3", "")
	if _, err := proverFor("python").SynthesizeCorpus(testsynth.Contract{Language: "python", Cases: []spec.BehavioralCase{bad}}, t.TempDir()); err == nil || !strings.Contains(err.Error(), "importable module") {
		t.Fatalf("a non-identifier pkg must be rejected, got %v", err)
	}
}

// TestPyProveEndToEndGreen (or-4y7.9): the full behavioral mode over a real
// python library inside the sandbox — both obligation kinds (value + raise)
// execute and pass, and the mode PASSES with the explicit REDUCED label
// (python declares no mutation engine — a capability fact, not Inconclusive;
// the caveat must be visible in the output, never silent).
func TestPyProveEndToEndGreen(t *testing.T) {
	requirePySandbox(t)
	art := t.TempDir()
	writeCalcLib(t, art)
	mr, err := ProveWithThreshold(context.Background(), art, testsynth.Contract{
		Language: "python",
		Cases: []spec.BehavioralCase{
			pyUnitCase("sum", "calclib", "add(2, 3)", "5", ""),
			pyUnitCase("raises", "calclib", "div(1, 0)", "", "division"),
		},
	}, nil, 0.6)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"sum", "raises"} {
		ob := mr.Obligations[id]
		if !ob.Executed || !ob.Passed {
			t.Errorf("obligation %s must execute and pass, got %+v\n%s", id, ob, mr.Output)
		}
	}
	if !mr.Pass || mr.Inconclusive {
		t.Fatalf("a green python corpus is a REDUCED pass (declared no-engine), got %+v\n%s", mr, mr.Output)
	}
	if !strings.Contains(mr.Output, "REDUCED") || !strings.Contains(mr.Output, "NOT SUPPORTED") {
		t.Fatalf("the reduced-proof caveat must be explicit in the output:\n%s", mr.Output)
	}
	if _, ok := mr.Metrics["mutation_score"]; ok {
		t.Fatal("an unmeasured mutation score must not be reported as a number")
	}
	if v, ok := mr.Metrics["mutation_supported"]; !ok || v != 0 {
		t.Fatalf("the metrics must carry mutation_supported=0, got %v", mr.Metrics)
	}
}

// TestPyProveEndToEndCatchesWrongBehavior (or-4y7.9): a want the artifact does
// not satisfy fails the run — the RUN marker prints, the PASS marker does not.
func TestPyProveEndToEndCatchesWrongBehavior(t *testing.T) {
	requirePySandbox(t)
	art := t.TempDir()
	writeCalcLib(t, art)
	mr, err := ProveWithThreshold(context.Background(), art, testsynth.Contract{
		Language: "python",
		Cases:    []spec.BehavioralCase{pyUnitCase("wrong", "calclib", "add(2, 3)", "6", "")},
	}, nil, 0.6)
	if err != nil {
		t.Fatal(err)
	}
	if mr.Pass || mr.Inconclusive {
		t.Fatalf("a violated obligation is a hard fail (not pass, not inconclusive): %+v", mr)
	}
	ob := mr.Obligations["wrong"]
	if !ob.Executed || ob.Passed {
		t.Errorf("the violated obligation must be executed-but-failed, got %+v\n%s", ob, mr.Output)
	}
}
