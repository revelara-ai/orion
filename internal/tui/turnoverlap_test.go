package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestCancelBlocksResubmitUntilDrained (or-or3j): cancelling a turn enters a
// DRAINING state — a new prompt cannot dispatch until the cancelled turn's
// goroutine actually returns (its turnDoneMsg lands), because until then the
// agent may still be emitting and a resubmitted turn's sink would adopt the
// stale output (the cross-turn content leak). Serialization closes the leak
// without an ACP protocol change.
func TestCancelBlocksResubmitUntilDrained(t *testing.T) {
	m := newTestConvo(t)
	m.input.SetValue("do A")
	m = feed(m, tea.KeyMsg{Type: tea.KeyEnter}) // turn A dispatched
	if !m.inFlight {
		t.Fatal("turn A should be in flight")
	}
	genA := m.turnGen

	m.cancelInFlight()
	if !m.draining || m.drainGen != genA {
		t.Fatalf("cancel must enter draining for the cancelled gen: draining=%v drainGen=%d genA=%d", m.draining, m.drainGen, genA)
	}

	// A resubmit while draining must NOT dispatch — the text stays in the box.
	m.input.SetValue("do B")
	genBefore := m.turnGen
	m = feed(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.inFlight || m.turnGen != genBefore {
		t.Fatal("a resubmit while draining must not dispatch a new turn (the leak window)")
	}
	if m.input.Value() != "do B" {
		t.Fatalf("the blocked line must stay in the input, got %q", m.input.Value())
	}

	// Negative (no over-clearing): an unrelated gen's turnDoneMsg leaves the
	// drain engaged.
	m = feed(m, turnDoneMsg{gen: 999})
	if !m.draining {
		t.Fatal("an unrelated turn's completion must not clear the drain")
	}

	// The cancelled turn's goroutine returns → drain clears → resubmit flows.
	m = feed(m, turnDoneMsg{gen: genA})
	if m.draining {
		t.Fatal("the cancelled turn's completion must clear the drain")
	}
	m = feed(m, tea.KeyMsg{Type: tea.KeyEnter})
	if !m.inFlight || m.turnGen <= genBefore {
		t.Fatalf("after draining, the resubmit must dispatch: inFlight=%v turnGen=%d", m.inFlight, m.turnGen)
	}
}

// TestDrainTimeoutUnblocksWithNote (or-or3j): a wedged agent that never acks
// the cancel must not brick the UI — the drain times out, surfaces a note,
// and accepts input again. A stale timeout (wrong gen) is a no-op.
func TestDrainTimeoutUnblocksWithNote(t *testing.T) {
	m := newTestConvo(t)
	m.input.SetValue("do A")
	m = feed(m, tea.KeyMsg{Type: tea.KeyEnter})
	m.cancelInFlight()
	gen := m.drainGen

	// Negative: a stale-gen timeout must not clear the live drain.
	m = feed(m, drainTimeoutMsg{gen: gen - 1})
	if !m.draining {
		t.Fatal("a stale drain timeout must be a no-op")
	}

	m = feed(m, drainTimeoutMsg{gen: gen})
	if m.draining {
		t.Fatal("the drain timeout must unblock input")
	}
	if !strings.Contains(transcript(m), "unacknowledged") {
		t.Fatalf("the forced unblock must be surfaced to the developer:\n%s", transcript(m))
	}
}
