package conductor

import "testing"

func TestPhaseStatusToActivity(t *testing.T) {
	// PhaseWarn must map to "warn" (advisory), not "fail" (hard failure).
	// The real pass/fail verdict lives in the build_report card, not the phase strip.
	cases := map[PhaseStatus]string{
		PhaseRunning: "running",
		PhaseDone:    "done",
		PhaseWarn:    "warn", // advisory — must NOT be "fail"
		PhaseFailed:  "fail", // hard error path
	}
	for in, want := range cases {
		if got := phaseStatusToActivity(in); got != want {
			t.Errorf("phaseStatusToActivity(%v) = %q, want %q", in, got, want)
		}
	}
}
