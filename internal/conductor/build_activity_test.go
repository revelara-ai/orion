package conductor

import "testing"

func TestPhaseStatusToActivity(t *testing.T) {
	cases := map[PhaseStatus]string{PhaseRunning: "running", PhaseDone: "done", PhaseWarn: "fail"}
	for in, want := range cases {
		if got := phaseStatusToActivity(in); got != want {
			t.Errorf("phaseStatusToActivity(%v) = %q, want %q", in, got, want)
		}
	}
}
