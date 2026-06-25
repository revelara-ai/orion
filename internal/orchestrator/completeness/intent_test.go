package completeness

import "testing"

// An intent that explicitly states functional decisions yields their values, so
// the flow can record them instead of dropping a "resolved" decision with no value
// (or-jh7).
func TestIntentValuesExtractsStatedDecisions(t *testing.T) {
	a := NewAnalyzer("http-service")
	got := a.IntentValues("Build a JSON HTTP service on port 8080 at /time in UTC")
	want := map[string]string{
		"response_format": "json",
		"port":            "8080",
		"route":           "/time",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("IntentValues[%q] = %q, want %q (full=%v)", k, got[k], v, got)
		}
	}
}

// A bare idea states nothing explicit → no intent-resolved values (the gate must
// never guess; those decisions stay open to be asked).
func TestIntentValuesEmptyForBareIdea(t *testing.T) {
	a := NewAnalyzer("http-service")
	got := a.IntentValues("Build a service that returns the current time")
	if len(got) != 0 {
		t.Fatalf("a bare idea should yield no intent-resolved values, got %v", got)
	}
}

// A non-path slash ("client/server", "and/or") must NOT be mistaken for a route —
// the gate resolves a route only from a real path.
func TestIntentValuesDoesNotMistakeSlashForRoute(t *testing.T) {
	a := NewAnalyzer("http-service")
	for _, intent := range []string{"Build a client/server app", "Support read and/or write"} {
		if v, ok := a.IntentValues(intent)["route"]; ok {
			t.Errorf("intent %q falsely resolved route=%q", intent, v)
		}
	}
}
