package acceptance_test

// Tests for the Epic 1 acceptance script and the expected-shape JSON
// contract. These are CONTRACT tests for the script's documented exit
// codes and the JSON schema, not end-to-end tests against a live Orion.
// They run as part of `make test` to ensure E1-A's deliverables stay
// well-formed across changes by E1-1..E1-8.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// scriptPath returns the absolute path to the epic1 smoke script.
func scriptPath(t *testing.T) string {
	t.Helper()
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(here), "epic1_smoke.sh")
}

// shapePath returns the absolute path to the expected-shape JSON.
func shapePath(t *testing.T) string {
	t.Helper()
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(here), "expected_pr_shape.json")
}

func TestSmokeScriptExists(t *testing.T) {
	p := scriptPath(t)
	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("smoke script missing at %s: %v", p, err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("smoke script %s is not executable (mode %o)", p, info.Mode().Perm())
	}
}

func TestSmokeScriptDryRunExitsZero(t *testing.T) {
	p := scriptPath(t)
	cmd := exec.Command("bash", p, "--dry-run")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dry-run expected exit 0, got error: %v; output:\n%s", err, out)
	}
}

// Default invocation against the current state should exit non-zero with
// the documented "no PR found" code, since Orion has not shipped E1-8 yet.
// We only assert the exit code is in the documented set; we do NOT call
// out to GitHub in unit tests (the script honors ORION_OFFLINE=1 to skip
// network probes and report exit 10).
func TestSmokeScriptDefaultEmitsDocumentedFailCode(t *testing.T) {
	p := scriptPath(t)
	cmd := exec.Command("bash", p)
	cmd.Env = append(os.Environ(), "ORION_OFFLINE=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit (Orion has not run yet); output:\n%s", out)
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected *exec.ExitError, got %T: %v", err, err)
	}
	switch code := exitErr.ExitCode(); code {
	case 10:
		// expected: no PR found
	default:
		t.Fatalf("expected exit 10 (no PR found), got %d; output:\n%s", code, out)
	}
}

// expectedShape mirrors the JSON schema. Adjust only via spec change and
// in lock-step with the smoke script's assertions.
type expectedShape struct {
	PR struct {
		Author  string `json:"author"`
		Commits struct {
			Min int `json:"min"`
		} `json:"commits"`
		Body struct {
			MustContain []string `json:"must_contain"`
		} `json:"body"`
		MustModifyAtLeastOnePathUnder []string `json:"must_modify_at_least_one_path_under"`
	} `json:"pr"`
	Reset struct {
		Procedure string `json:"procedure"`
	} `json:"reset"`
}

func TestExpectedShapeJSONIsWellFormed(t *testing.T) {
	p := shapePath(t)
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("expected_pr_shape.json missing at %s: %v", p, err)
	}
	var s expectedShape
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("expected_pr_shape.json does not match schema: %v", err)
	}
	if s.PR.Author == "" {
		t.Error("pr.author is empty")
	}
	if s.PR.Commits.Min < 1 {
		t.Errorf("pr.commits.min=%d; want >=1", s.PR.Commits.Min)
	}
	wantBodyContains := []string{"Operating envelope", "Confidence interval", "Reproduction bundle", "Polaris control"}
	got := map[string]bool{}
	for _, s := range s.PR.Body.MustContain {
		got[s] = true
	}
	for _, want := range wantBodyContains {
		if !got[want] {
			t.Errorf("pr.body.must_contain missing required field: %q", want)
		}
	}
	if len(s.PR.MustModifyAtLeastOnePathUnder) == 0 {
		t.Error("pr.must_modify_at_least_one_path_under is empty; need at least one path constraint")
	}
	if s.Reset.Procedure == "" {
		t.Error("reset.procedure is empty; need documented reset commands")
	}
}
