package actuation

import (
	"path/filepath"
	"testing"
)

// TestRedButtonPausesAndBlocksNewActions: engaging the red button blocks every
// mutating action at the gate; releasing it restores normal operation.
func TestRedButtonPausesAndBlocksNewActions(t *testing.T) {
	rb := RedButton{Path: filepath.Join(t.TempDir(), "red_button")}

	// Clear: actions allowed.
	if err := rb.Guard("sandbox.write"); err != nil {
		t.Fatalf("guard should allow before engage: %v", err)
	}

	// Engaged: all mutating actions blocked (new dispatch + in-flight gates).
	if err := rb.Engage(); err != nil {
		t.Fatalf("engage: %v", err)
	}
	if !rb.Engaged() {
		t.Fatal("red button should read engaged")
	}
	for _, tool := range []string{"sandbox.write", "worktree.create", "integration.merge", "polaris.write"} {
		if rb.Guard(tool) == nil {
			t.Fatalf("engaged red button must block %q", tool)
		}
	}

	// Released: actions allowed again.
	if err := rb.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	if rb.Guard("sandbox.write") != nil {
		t.Fatal("released red button should allow actions")
	}
}

// TestRedButtonRevokesAutonomy: while engaged, autonomy is revoked — an
// otherwise-deliverable bar decision may not auto-deliver.
func TestRedButtonRevokesAutonomy(t *testing.T) {
	rb := RedButton{Path: filepath.Join(t.TempDir(), "red_button")}

	if rb.AutonomyRevoked() {
		t.Fatal("autonomy must not be revoked by default")
	}
	if !AutonomousDeliverPermitted(rb, "deliver") {
		t.Fatal("a deliver decision should auto-deliver when the red button is clear")
	}

	if err := rb.Engage(); err != nil {
		t.Fatalf("engage: %v", err)
	}
	if !rb.AutonomyRevoked() {
		t.Fatal("engaged red button must revoke autonomy")
	}
	if AutonomousDeliverPermitted(rb, "deliver") {
		t.Fatal("engaged red button must block autonomous deliver (human required)")
	}
}

// TestReleaseIdempotent.
func TestReleaseIdempotent(t *testing.T) {
	rb := RedButton{Path: filepath.Join(t.TempDir(), "red_button")}
	if err := rb.Release(); err != nil {
		t.Fatalf("release on a clear button should be a no-op: %v", err)
	}
}
