package conductor

import (
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/proof/truthalign"
)

// TestEvaluateEpicOutcome: the decision matrix for partial delivery. The epic
// verdict stays HONEST (any failed task rejects the epic); "partial" is the
// separate fact that a proven, dependency-complete, wired subset exists and is
// deliverable. One failed task must never suppress delivery of proven siblings —
// and an unwired subset must never ship.
func TestEvaluateEpicOutcome(t *testing.T) {
	acc := func(id string) taskResult { return taskResult{TaskID: id, Verdict: "Accept"} }
	rej := func(id string) taskResult {
		return taskResult{TaskID: id, Verdict: "Reject", FailureAnalysis: "behavioral case c1 failed: wrong body"}
	}
	blk := func(id string) taskResult { return taskResult{TaskID: id, Blocked: true} }

	cases := []struct {
		name              string
		results           []taskResult
		integrated, wired bool
		wantAggregate     truthalign.Verdict
		wantPartial       bool
		wantBar           truthalign.Verdict
	}{
		{"all accept", []taskResult{acc("a"), acc("b")}, true, true, truthalign.Accept, false, truthalign.Accept},
		{"mixed, subset wired", []taskResult{acc("a"), rej("b")}, true, true, truthalign.Reject, true, truthalign.Accept},
		{"mixed with blocked dependent", []taskResult{acc("a"), rej("b"), blk("c")}, true, true, truthalign.Reject, true, truthalign.Accept},
		{"mixed but subset unwired", []taskResult{acc("a"), rej("b")}, true, false, truthalign.Reject, false, truthalign.Reject},
		{"mixed but assembly failed", []taskResult{acc("a"), rej("b")}, false, true, truthalign.Reject, false, truthalign.Reject},
		{"all failed", []taskResult{rej("a"), rej("b")}, true, true, truthalign.Reject, false, truthalign.Reject},
		{"single task failed", []taskResult{rej("a")}, true, true, truthalign.Reject, false, truthalign.Reject},
		{"all accept but unwired", []taskResult{acc("a")}, true, false, truthalign.Reject, false, truthalign.Reject},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := evaluateEpicOutcome(tc.results, tc.integrated, tc.wired)
			if got.aggregate != tc.wantAggregate || got.partial != tc.wantPartial || got.barVerdict != tc.wantBar {
				t.Errorf("got aggregate=%s partial=%v bar=%s, want %s/%v/%s",
					got.aggregate, got.partial, got.barVerdict, tc.wantAggregate, tc.wantPartial, tc.wantBar)
			}
		})
	}
}

// TestEscalatedRemainderNamesTasksAndReasons: the PR reviewer must see exactly
// what is NOT in the delivery and why.
func TestEscalatedRemainderNamesTasksAndReasons(t *testing.T) {
	results := []taskResult{
		{TaskID: "t-ok", Verdict: "Accept"},
		{TaskID: "t-bad", Verdict: "Reject", FailureAnalysis: "empirical probe: port never opened"},
		{TaskID: "t-blocked", Blocked: true},
	}
	r := escalatedRemainder(results)
	if !strings.Contains(r, "t-bad") || !strings.Contains(r, "port never opened") {
		t.Errorf("remainder must name the failing task and its analysis:\n%s", r)
	}
	if !strings.Contains(r, "t-blocked") {
		t.Errorf("remainder must name blocked dependents:\n%s", r)
	}
	if strings.Contains(r, "t-ok") {
		t.Errorf("delivered tasks do not belong in the remainder:\n%s", r)
	}
	if escalatedRemainder([]taskResult{{TaskID: "a", Verdict: "Accept"}}) != "" {
		t.Error("an all-accepted epic has no remainder")
	}
}
