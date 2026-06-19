package actuation

import (
	"fmt"
	"os"
)

// RedButton is the global emergency stop (or-utm, PRD SRE-Derived Refinements;
// SPEC §5). It is file-backed so it is CROSS-PROCESS: the Conductor daemon, every
// worker, and the run loop all consult the same flag, so engaging it halts
// in-flight and new mutating actions everywhere. Enforcement lives at the
// deterministic actuation gate (Guard), decoupled from the agents — a paused
// state cannot be reasoned around.
type RedButton struct {
	Path string
}

// Engage trips the red button: pause all mutating actions, block new dispatch,
// and revoke autonomy.
func (rb RedButton) Engage() error {
	if rb.Path == "" {
		return fmt.Errorf("red button: no path")
	}
	return os.WriteFile(rb.Path, []byte("engaged\n"), 0o600)
}

// Release clears the red button.
func (rb RedButton) Release() error {
	err := os.Remove(rb.Path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// Engaged reports whether the red button is currently tripped.
func (rb RedButton) Engaged() bool {
	if rb.Path == "" {
		return false
	}
	_, err := os.Stat(rb.Path)
	return err == nil
}

// Guard is called by the deterministic actuation gate before any state-mutating
// action. When the red button is engaged it returns an error, so the action is
// blocked regardless of whether the caller is an agent or a human.
func (rb RedButton) Guard(tool string) error {
	if rb.Engaged() {
		return fmt.Errorf("actuation halted: red button engaged (%q blocked)", tool)
	}
	return nil
}

// AutonomyRevoked reports whether autonomous (auto-deliver) actions are revoked.
// While the red button is engaged, nothing ships without a human.
func (rb RedButton) AutonomyRevoked() bool { return rb.Engaged() }

// AutonomousDeliverPermitted reports whether an auto-deliver may proceed: only on
// a "deliver" bar decision AND with the red button clear.
func AutonomousDeliverPermitted(rb RedButton, barDecision string) bool {
	return barDecision == "deliver" && !rb.AutonomyRevoked()
}
