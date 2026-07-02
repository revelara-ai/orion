package conductor

import (
	"context"
	"fmt"
	"sync"

	"github.com/revelara-ai/orion/internal/orchestrator"
)

// driftConcernThreshold is how many completed tasks may carry an advisory
// alignment concern before the run pauses (Manifesto: "when alignment degrades
// past a threshold, the loop pauses and re-grounds"). The judge is advisory —
// it supplies the signal; the pause itself is this deterministic count.
const driftConcernThreshold = 3

// driftMonitor re-evaluates drift at EVERY cluster dispatch, not once at
// end-of-run (or-v9f.9): the spec anchor is re-verified against the store
// (a mid-run spec mutation or tamper stops the loop immediately), and
// accumulated alignment concerns past the threshold pause new dispatch. A
// refusal blocks further clusters through the preDispatch gate — in-flight
// work finishes, the bar escalates, the inbox and webhook carry the reason.
type driftMonitor struct {
	c         *orchestrator.Conductor
	threshold int

	mu       sync.Mutex
	concerns int
}

func newDriftMonitor(c *orchestrator.Conductor) *driftMonitor {
	return &driftMonitor{c: c, threshold: driftConcernThreshold}
}

// RecordAlignment feeds a settled task's advisory alignment outcome into the
// degradation count. Only a RUN audit that flagged misalignment counts.
func (m *driftMonitor) RecordAlignment(a AlignmentRecord) {
	if !a.Ran || a.Aligned {
		return
	}
	m.mu.Lock()
	m.concerns++
	m.mu.Unlock()
}

// Check is the per-dispatch drift gate. It returns an error — refusing further
// dispatch — when the spec anchor no longer verifies or the alignment
// degradation threshold is breached.
func (m *driftMonitor) Check(ctx context.Context) error {
	if _, err := m.c.RecallSpec(ctx); err != nil {
		return fmt.Errorf("drift gate: spec anchor no longer verifies mid-run — pausing dispatch (re-ratify before continuing): %w", err)
	}
	m.mu.Lock()
	n := m.concerns
	m.mu.Unlock()
	if n >= m.threshold {
		return fmt.Errorf("drift gate: %d task(s) completed with alignment concerns (threshold %d) — pausing dispatch to re-ground before divergence compounds", n, m.threshold)
	}
	return nil
}
