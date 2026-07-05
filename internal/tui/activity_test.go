package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/revelara-ai/orion/internal/acp"
)

func TestActivityModelStackAndPhases(t *testing.T) {
	a := newActivityModel()
	a.apply(acp.Activity("Orion", "build_service", 0, "running"))
	a.apply(acp.Activity("Orion", "generate", 0, "done"))
	a.apply(acp.Activity("research", "web_search", 1, "running"))

	if len(a.stack) == 0 || a.stack[len(a.stack)-1].actor != "research" {
		t.Fatalf("deepest frame should be the subagent; stack=%+v", a.stack)
	}
	if !hasPhase(a.phases, "generate", "done") {
		t.Fatalf("generate phase not recorded done: %+v", a.phases)
	}

	a.apply(acp.Activity("research", "", 1, "done")) // subagent resolves
	if deepestActor(a.stack) == "research" {
		t.Fatalf("resolved subagent should be popped; stack=%+v", a.stack)
	}
}

func TestActivityPhaseDedupAndFailSummary(t *testing.T) {
	// --- EqualFold dedup: capitalized production-style phase ---
	a := newActivityModel()
	a.apply(acp.Activity("Orion", "Generate", 0, "running"))
	a.apply(acp.Activity("Orion", "Generate", 0, "done"))

	var generatePhases []phaseMark
	for _, p := range a.phases {
		if p.name == "Generate" {
			generatePhases = append(generatePhases, p)
		}
	}
	if len(generatePhases) != 1 {
		t.Fatalf("expected exactly 1 phaseMark named 'Generate', got %d: %+v", len(generatePhases), a.phases)
	}
	if generatePhases[0].status != "done" {
		t.Fatalf("expected Generate status 'done', got %q", generatePhases[0].status)
	}

	// --- Fail summary: finish() must lead with ✗ not ✓ ---
	a.apply(acp.Activity("Orion", "Prove", 0, "fail"))
	a.finish()

	if !strings.Contains(a.summary, "✗") {
		t.Fatalf("summary should contain ✗ for failed phase; got %q", a.summary)
	}
	if !strings.Contains(a.summary, "Prove") {
		t.Fatalf("summary should name the failed phase 'Prove'; got %q", a.summary)
	}
	if strings.HasPrefix(a.summary, "✓") {
		t.Fatalf("summary must not start with ✓ when a phase failed; got %q", a.summary)
	}
}

func TestActivityHeartbeatNudgesWhenSilent(t *testing.T) {
	a := newActivityModel()
	a.apply(acp.Activity("Orion", "prove", 0, "running"))
	before := len(a.bus.Events())
	// Simulate a heartbeat tick after the interval by forcing the bus to tick.
	a.bus.Tick(a.bus.nowForTest().Add(3 * time.Second)) // heartbeat due
	if len(a.bus.Events()) <= before {
		t.Fatalf("heartbeat did not append a liveness event")
	}
}

// TestDiagnosePhaseDoesNotTouchStack verifies that a phase update (Depth:0,
// known phase name) drives ONLY the phase strip and does not push/pop the
// actor-stack. The "build_service" tool frame must remain pinned while phases
// stream to the strip.
func TestDiagnosePhaseDoesNotTouchStack(t *testing.T) {
	a := newActivityModel()
	// Push a Depth:0 tool frame — this is what the conductor emits when calling build_service.
	a.apply(acp.Activity("Orion", "build_service", 0, "running"))
	if len(a.stack) == 0 || a.stack[0].activity != "build_service" {
		t.Fatalf("expected build_service frame on stack; stack=%+v", a.stack)
	}

	// Apply a Diagnose phase update (Depth:0, phase name).
	a.apply(acp.Activity("Orion", "Diagnose", 0, "running"))

	// Stack must still contain the build_service frame — phase must NOT pop it.
	if len(a.stack) == 0 {
		t.Fatalf("actor stack is empty after Diagnose phase; expected build_service frame to remain")
	}
	found := false
	for _, f := range a.stack {
		if f.activity == "build_service" {
			found = true
		}
	}
	if !found {
		t.Fatalf("build_service frame was removed from stack by phase update; stack=%+v", a.stack)
	}

	// Phase strip must record Diagnose.
	if !hasPhase(a.phases, "Diagnose", "running") {
		t.Fatalf("Diagnose phase not in strip; phases=%+v", a.phases)
	}
}

// TestWarnPhaseFinishSummaryNotFailure verifies that a phase with status "warn"
// does NOT produce a ✗ summary in finish(). Only status "fail" is a hard failure.
func TestWarnPhaseFinishSummaryNotFailure(t *testing.T) {
	a := newActivityModel()
	a.apply(acp.Activity("Orion", "Generate", 0, "done"))
	a.apply(acp.Activity("Orion", "Diagnose", 0, "warn"))
	a.finish()

	if strings.HasPrefix(a.summary, "✗") {
		t.Fatalf("summary must not start with ✗ for a warn phase; got %q", a.summary)
	}
	if a.summary == "" {
		t.Fatalf("summary should not be empty when there are done/warn phases; got %q", a.summary)
	}
}

func hasPhase(phases []phaseMark, name, status string) bool {
	for _, p := range phases {
		if p.name == name && p.status == status {
			return true
		}
	}
	return false
}

func deepestActor(stack []actorFrame) string {
	if len(stack) == 0 {
		return ""
	}
	return stack[len(stack)-1].actor
}
