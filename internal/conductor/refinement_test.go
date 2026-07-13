package conductor

import (
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/proof"
)

// TestRefinementRegressedAxes (or-mvr.5): every degradation axis terminates
// with the regression named; improvement or stasis never does.
func TestRefinementRegressedAxes(t *testing.T) {
	base := refinementSnapshot{SecretFindings: 0, ScanFindings: 2, NewTestFailures: 1, PassingObligations: 3, hasObligations: true}
	cases := []struct {
		name string
		cur  refinementSnapshot
		want bool
		axis string
	}{
		{"secrets up", refinementSnapshot{SecretFindings: 1, ScanFindings: 2, NewTestFailures: 1, PassingObligations: 3, hasObligations: true}, true, "secrets"},
		{"failures up", refinementSnapshot{ScanFindings: 2, NewTestFailures: 2, PassingObligations: 3, hasObligations: true}, true, "test regressions"},
		{"scan up", refinementSnapshot{ScanFindings: 3, NewTestFailures: 1, PassingObligations: 3, hasObligations: true}, true, "reliability findings"},
		{"obligations down", refinementSnapshot{ScanFindings: 2, NewTestFailures: 1, PassingObligations: 2, hasObligations: true}, true, "obligations"},
		{"improvement", refinementSnapshot{ScanFindings: 1, NewTestFailures: 0, PassingObligations: 4, hasObligations: true}, false, ""},
		{"stasis", base, false, ""},
	}
	for _, tc := range cases {
		regressed, why := refinementRegressed(base, tc.cur)
		if regressed != tc.want {
			t.Fatalf("%s: regressed=%v want %v (%s)", tc.name, regressed, tc.want, why)
		}
		if tc.want && !strings.Contains(why, tc.axis) {
			t.Fatalf("%s: reason must name the axis, got %q", tc.name, why)
		}
	}
}

// TestObligationSnapshotUnmeasuredNeverCompares: a path that doesn't measure
// obligations must not fabricate a regression.
func TestObligationSnapshotUnmeasuredNeverCompares(t *testing.T) {
	prev := refinementSnapshot{PassingObligations: 5, hasObligations: true}
	cur := refinementSnapshot{} // unmeasured
	if regressed, why := refinementRegressed(prev, cur); regressed {
		t.Fatalf("unmeasured obligations must not read as a regression: %s", why)
	}
	if p, has := obligationSnapshot(proof.Report{}); has || p != 0 {
		t.Fatal("an empty report has no obligation signal")
	}
}
