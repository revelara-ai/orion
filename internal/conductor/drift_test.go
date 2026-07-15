package conductor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator/spec"
	"github.com/revelara-ai/orion/internal/proof"
)

// TestDriftReport (or-tcs.10): the spec↔build re-evaluation flags coverage drift (a required
// obligation not proven) and wireup drift (an orphan package), and reads aligned when neither.
func TestDriftReport(t *testing.T) {
	es := spec.ExecutableSpec{
		ResponseContract: spec.ResponseContract{Cases: []spec.BehavioralCase{{ID: "c1"}, {ID: "c2"}}},
	}
	covered := proof.Report{ObligationResults: map[string]proof.ObligationResult{
		"c1": {Executed: true, Passed: true},
		"c2": {Executed: true, Passed: true},
	}}

	if out, drift := driftReport(es, covered, WireupWired, nil, nil); drift || !strings.Contains(out, "aligned") {
		t.Errorf("full coverage + no orphans should read aligned: %q", out)
	}

	// coverage drift — c2 has no passing obligation (spec not built)
	partial := proof.Report{ObligationResults: map[string]proof.ObligationResult{"c1": {Executed: true, Passed: true}}}
	if out, drift := driftReport(es, partial, WireupWired, nil, nil); !drift || !strings.Contains(out, "unbuilt: c2") {
		t.Errorf("an uncovered obligation must be DRIFT: %q", out)
	}

	// wireup drift — an orphan package (built, not wired)
	if out, drift := driftReport(es, covered, WireupOrphaned, []string{"internal/orphan"}, nil); !drift || !strings.Contains(out, "orphan") {
		t.Errorf("an orphan package must be DRIFT: %q", out)
	}
}

// TestDriftReportDedupsCollidingCaseIDs: RequiredCaseIDs can repeat a content-addressed id (two
// cases collapsing to the same request/expect). The coverage fraction must count each DISTINCT
// obligation once — not inflate the denominator or double the unbuilt list.
func TestDriftReportDedupsCollidingCaseIDs(t *testing.T) {
	dup := spec.ExecutableSpec{
		ResponseContract: spec.ResponseContract{Cases: []spec.BehavioralCase{{ID: "a"}, {ID: "a"}, {ID: "b"}}},
	}
	// "a" covered, "b" not → 1 of 2 DISTINCT obligations proven, "b" the sole unbuilt.
	rep := proof.Report{ObligationResults: map[string]proof.ObligationResult{"a": {Executed: true, Passed: true}}}
	out, drift := driftReport(dup, rep, WireupWired, nil, nil)
	if !drift || !strings.Contains(out, "coverage 1/2") {
		t.Errorf("distinct denominator must be 2, not 3: %q", out)
	}
	if !strings.Contains(out, "unbuilt: b") || strings.Contains(out, "unbuilt: a") || strings.Contains(out, "a, a") {
		t.Errorf("unbuilt list must be the distinct uncovered set {b}, no doubled ids: %q", out)
	}
	// both distinct obligations covered → aligned, denominator still 2 (not the 3-long raw slice).
	full := proof.Report{ObligationResults: map[string]proof.ObligationResult{
		"a": {Executed: true, Passed: true}, "b": {Executed: true, Passed: true},
	}}
	if out, drift := driftReport(dup, full, WireupWired, nil, nil); drift || !strings.Contains(out, "coverage 2/2") {
		t.Errorf("fully covered must read aligned 2/2 (deduped): %q", out)
	}
}

// TestUntracedSurfaceScopeCreep (or-hik): an artifact route no case requests
// and an exported func no case calls are flagged as scope creep; the entry
// symbol, main, spec-cased routes, and unit-called exports are traced;
// support types never flag.
func TestUntracedSurfaceScopeCreep(t *testing.T) {
	dir := t.TempDir()
	src := `package main

import "net/http"

func handleTime(w http.ResponseWriter, r *http.Request) {}

func Add(a, b int) int { return a + b }

func SneakyBackdoor() string { return "not in any spec" }

type helperState struct{ n int }

type PublicHelper struct{}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/time", handleTime)
	mux.HandleFunc("/debug/secret", handleTime)
	_ = http.ListenAndServe(":8080", mux)
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	es := spec.ExecutableSpec{}
	es.ResponseContract.Cases = []spec.BehavioralCase{
		{ID: "c1", Request: spec.RequestShape{Method: "GET", Path: "/time"}, Expect: spec.ExpectShape{Status: 200}},
		{ID: "c2", Kind: spec.KindUnit, Unit: &spec.UnitCase{Steps: []spec.UnitStep{{Call: "Add(1,2)", Want: "3"}}}},
	}

	untraced := untracedSurface(es, "handleTime", dir)
	joined := strings.Join(untraced, ";")
	for _, want := range []string{"route /debug/secret", "func SneakyBackdoor"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("scope creep must flag %q, got %v", want, untraced)
		}
	}
	for _, never := range []string{"route /time", "func Add", "func handleTime", "func main", "type "} {
		if strings.Contains(joined, never) {
			t.Fatalf("%q is traced/exempt and must not flag, got %v", never, untraced)
		}
	}

	// The report's third clause carries the finding and flips DRIFT — with
	// coverage FULLY GREEN, so untraced alone drives the verdict.
	var rep proof.Report
	rep.ObligationResults = map[string]proof.ObligationResult{
		"c1": {Executed: true, Passed: true},
		"c2": {Executed: true, Passed: true},
	}
	out, drift := driftReport(es, rep, WireupWired, nil, untraced)
	if !drift || !strings.Contains(out, "untraced: ") || !strings.Contains(out, "scope creep") {
		t.Fatalf("the untraced clause must escalate drift: %s", out)
	}
	clean, cleanDrift := driftReport(spec.ExecutableSpec{}, rep, WireupWired, nil, nil)
	if !strings.Contains(clean, "traceability clean") {
		t.Fatalf("no untraced surface → traceability clean: %s (drift=%v)", clean, cleanDrift)
	}
}
