package orchestrator

import "testing"

// or-jh7 repro: an intent that explicitly states its functional decisions must be
// assemblable. Previously Analyze dropped those decisions from OpenDecisions but
// recorded no value, so ApproveSpec failed with a contradictory "unresolved" error
// even though check_completeness reported nothing open.
func TestIntentStatedDecisionsAssembleNotUnresolved(t *testing.T) {
	c, ctx := storeConductor(t)
	const intent = "Build a JSON HTTP service on port 8080 at /time in UTC."
	if _, err := c.Submit(ctx, intent); err != nil {
		t.Fatalf("submit: %v", err)
	}

	// No RecordAnswer — the intent states the functional decisions itself.
	es, err := c.ApproveSpec(ctx)
	if err != nil {
		t.Fatalf("approve should succeed for an intent that states its decisions, got: %v", err)
	}
	rc := es.ResponseContract
	if rc.Port != 8080 {
		t.Errorf("ResponseContract.Port = %d, want 8080", rc.Port)
	}
	if rc.Route != "/time" {
		t.Errorf("ResponseContract.Route = %q, want /time", rc.Route)
	}
	if rc.Format() != "json" {
		t.Errorf("ResponseContract.Format = %q, want json", rc.Format())
	}
	if rc.TimeZone != "UTC" {
		t.Errorf("ResponseContract.TimeZone = %q, want UTC", rc.TimeZone)
	}
}

// An explicit answer still wins over the intent-stated value (intent is only a
// fallback for what the developer did not separately decide).
func TestExplicitAnswerOverridesIntentValue(t *testing.T) {
	c, ctx := storeConductor(t)
	if _, err := c.Submit(ctx, "Build a JSON HTTP service on port 8080 at /time in UTC."); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := c.RecordAnswer(ctx, "port", "9090"); err != nil {
		t.Fatalf("answer port: %v", err)
	}
	es, err := c.ApproveSpec(ctx)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if es.ResponseContract.Port != 9090 {
		t.Fatalf("explicit answer should win: Port = %d, want 9090", es.ResponseContract.Port)
	}
}
