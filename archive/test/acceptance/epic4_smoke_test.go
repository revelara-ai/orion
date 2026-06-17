package acceptance_test

// Contract tests for the Epic 4 acceptance smoke script and the
// expected-restart-drill-shape JSON. These mirror epic1_smoke_test.go:
// they assert that the bookend artifacts (orion-e4a) stay well-formed
// across changes by E4-1..E4-9, without running an end-to-end live drill.
//
// The live restart drill (kill-and-recover against a real minikube
// Conductor + Lookout + worker) is exercised in --live-minikube mode and
// in epic4_restart_drill_test.go, both of which are intentionally red
// until the slices that ship the Conductor land.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func epic4ScriptPath(t *testing.T) string {
	t.Helper()
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(here), "epic4_smoke.sh")
}

func epic4ShapePath(t *testing.T) string {
	t.Helper()
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(here), "expected_restart_drill_shape.json")
}

func epic4FixturePath(t *testing.T) string {
	t.Helper()
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(here), "fixtures", "epic4-conductor")
}

func TestEpic4SmokeScriptExists(t *testing.T) {
	p := epic4ScriptPath(t)
	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("smoke script missing at %s: %v", p, err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("smoke script %s is not executable (mode %o)", p, info.Mode().Perm())
	}
}

// Dry-run mode is the contract that holds today: builds, validates the
// fixture and shape, and exits 0. Live mode is intentionally red until
// E4-1..E4-9 land and is not exercised here.
func TestEpic4SmokeDryRunExitsZero(t *testing.T) {
	p := epic4ScriptPath(t)
	cmd := exec.Command("bash", p, "--dry-run")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dry-run expected exit 0, got error: %v; output:\n%s", err, out)
	}
}

// Live-minikube mode must refuse to run with a documented pre-condition
// exit (14) until the Conductor + Lookout + worker slices ship. This
// pins the failing target the bookend defines.
func TestEpic4SmokeLiveMinikubeRefusesUntilSlicesLand(t *testing.T) {
	p := epic4ScriptPath(t)
	cmd := exec.Command("bash", p, "--live-minikube")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit (live mode not yet shippable); output:\n%s", out)
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected *exec.ExitError, got %T: %v", err, err)
	}
	if code := exitErr.ExitCode(); code != 14 {
		t.Fatalf("expected exit 14 (pre-condition: slices not yet built), got %d; output:\n%s", code, out)
	}
}

// expectedRestartDrillShape mirrors the JSON schema for the Epic 4
// restart-recovery contract. Adjust only via spec change and in
// lock-step with the smoke script's assertions and the restart-drill
// Go test.
type expectedRestartDrillShape struct {
	Fixture struct {
		Path   string   `json:"path"`
		Module string   `json:"module"`
		Files  []string `json:"files"`
	} `json:"fixture"`
	Backlog struct {
		Path   string `json:"path"`
		Issues int    `json:"issues"`
	} `json:"backlog"`
	Invariants struct {
		ConductorReplicas        int      `json:"conductor_replicas"`
		MustNotDoubleSpawn       bool     `json:"must_not_double_spawn"`
		MustRespectFencingToken  bool     `json:"must_respect_fencing_token"`
		LookoutResumesAfterKill  bool     `json:"lookout_resumes_after_kill"`
		NamespaceTornDownOnExit  bool     `json:"namespace_torn_down_on_exit"`
		RunStatesObserved        []string `json:"run_states_observed"`
		IssueClaimStatesObserved []string `json:"issue_claim_states_observed"`
	} `json:"invariants"`
	ExitCodes map[string]string `json:"exit_codes"`
}

func TestEpic4ExpectedShapeIsWellFormed(t *testing.T) {
	p := epic4ShapePath(t)
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("expected_restart_drill_shape.json missing at %s: %v", p, err)
	}
	var s expectedRestartDrillShape
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("expected_restart_drill_shape.json does not match schema: %v", err)
	}
	if s.Fixture.Module == "" {
		t.Error("fixture.module is empty")
	}
	if len(s.Fixture.Files) < 3 {
		t.Errorf("fixture.files len=%d; want >=3 (one per pattern)", len(s.Fixture.Files))
	}
	if s.Backlog.Issues != 2 {
		t.Errorf("backlog.issues=%d; bookend pins 2", s.Backlog.Issues)
	}
	if s.Invariants.ConductorReplicas < 2 {
		t.Errorf("invariants.conductor_replicas=%d; restart drill needs >=2", s.Invariants.ConductorReplicas)
	}
	if !s.Invariants.MustNotDoubleSpawn {
		t.Error("invariants.must_not_double_spawn must be true (workspace key idempotency)")
	}
	if !s.Invariants.MustRespectFencingToken {
		t.Error("invariants.must_respect_fencing_token must be true (split-brain guard)")
	}
	wantRunStates := []string{"created", "claimed", "in_progress", "completed"}
	gotStates := map[string]bool{}
	for _, st := range s.Invariants.RunStatesObserved {
		gotStates[st] = true
	}
	for _, want := range wantRunStates {
		if !gotStates[want] {
			t.Errorf("invariants.run_states_observed missing %q", want)
		}
	}
	for _, want := range []string{"0", "10", "11", "12", "13", "14"} {
		if _, ok := s.ExitCodes[want]; !ok {
			t.Errorf("exit_codes missing documented code %q", want)
		}
	}
}

// Fixture well-formedness: 1 Go service with 3 gaps + backlog.json
// describing 2 issues. The integration-acceptance test (orion-e4f)
// drives this fixture end-to-end; orion-e4a only pins its shape.
func TestEpic4FixtureIsWellFormed(t *testing.T) {
	dir := epic4FixturePath(t)
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		t.Fatalf("fixture dir missing at %s: %v", dir, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		t.Errorf("fixture missing go.mod: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "backlog.json")); err != nil {
		t.Errorf("fixture missing backlog.json: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir fixture: %v", err)
	}
	goCount := 0
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".go" {
			goCount++
		}
	}
	if goCount < 3 {
		t.Errorf("fixture has %d top-level .go files; want >=3 (one per pattern)", goCount)
	}

	// backlog.json must describe exactly 2 issues per the bookend contract.
	data, err := os.ReadFile(filepath.Join(dir, "backlog.json"))
	if err != nil {
		t.Fatalf("read backlog.json: %v", err)
	}
	var backlog struct {
		Issues []struct {
			ID      string `json:"id"`
			Title   string `json:"title"`
			Service string `json:"service"`
		} `json:"issues"`
	}
	if err := json.Unmarshal(data, &backlog); err != nil {
		t.Fatalf("backlog.json malformed: %v", err)
	}
	if len(backlog.Issues) != 2 {
		t.Errorf("backlog.json has %d issues; bookend pins 2", len(backlog.Issues))
	}
	for i, issue := range backlog.Issues {
		if issue.ID == "" || issue.Title == "" || issue.Service == "" {
			t.Errorf("backlog.issues[%d] has empty id/title/service: %+v", i, issue)
		}
	}
}

// The restart-drill Go test file must exist and compile (build tag
// keeps it out of the default `go test` cycle until invoked explicitly).
// If the file is absent, orion-e4a's deliverable is incomplete.
func TestEpic4RestartDrillFileExists(t *testing.T) {
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	drill := filepath.Join(filepath.Dir(here), "epic4_restart_drill_test.go")
	if _, err := os.Stat(drill); err != nil {
		t.Fatalf("epic4_restart_drill_test.go missing at %s: %v", drill, err)
	}
}
