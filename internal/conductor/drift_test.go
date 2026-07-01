package conductor

import (
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

	if out, drift := driftReport(es, covered, nil); drift || !strings.Contains(out, "aligned") {
		t.Errorf("full coverage + no orphans should read aligned: %q", out)
	}

	// coverage drift — c2 has no passing obligation (spec not built)
	partial := proof.Report{ObligationResults: map[string]proof.ObligationResult{"c1": {Executed: true, Passed: true}}}
	if out, drift := driftReport(es, partial, nil); !drift || !strings.Contains(out, "unbuilt: c2") {
		t.Errorf("an uncovered obligation must be DRIFT: %q", out)
	}

	// wireup drift — an orphan package (built, not wired)
	if out, drift := driftReport(es, covered, []string{"internal/orphan"}); !drift || !strings.Contains(out, "orphan") {
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
	out, drift := driftReport(dup, rep, nil)
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
	if out, drift := driftReport(dup, full, nil); drift || !strings.Contains(out, "coverage 2/2") {
		t.Errorf("fully covered must read aligned 2/2 (deduped): %q", out)
	}
}
