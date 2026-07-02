package conductor

import (
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
)

// TestDriftMonitorDetectsMidRunAnchorBreak: the spec anchor is re-verified at
// every dispatch — a post-ratification mutation of the stored decisions breaks
// the recompiled hash and PAUSES the loop, instead of building the rest of the
// backlog against a spec that is no longer the one the developer ratified.
func TestDriftMonitorDetectsMidRunAnchorBreak(t *testing.T) {
	oc, ctx := ratifiedTimeService(t)
	m := newDriftMonitor(oc)

	if err := m.Check(ctx); err != nil {
		t.Fatalf("a freshly ratified spec must pass the drift gate: %v", err)
	}

	// Mid-run tamper: a new answer for an anchored decision changes what the
	// spec recompiles to; the stored hash no longer matches.
	if err := oc.Store().WithTx(ctx, func(tx *contextstore.Tx) error {
		proj, e := tx.Projects().Active(ctx)
		if e != nil {
			return e
		}
		sp, e := tx.Specs().LatestForProject(ctx, proj.ID)
		if e != nil {
			return e
		}
		_, e = tx.Decisions().Create(ctx, proj.ID, sp.ID, "port", "9999", "precise", false)
		return e
	}); err != nil {
		t.Fatal(err)
	}

	err := m.Check(ctx)
	if err == nil {
		t.Fatal("a broken anchor must refuse further dispatch")
	}
	if !strings.Contains(err.Error(), "anchor") {
		t.Errorf("the refusal must name the anchor, got: %v", err)
	}
}

// TestDriftMonitorAlignmentThreshold: advisory concerns accumulate; the PAUSE at
// the threshold is deterministic. The judge only ever supplies the signal.
func TestDriftMonitorAlignmentThreshold(t *testing.T) {
	oc, ctx := ratifiedTimeService(t)
	m := newDriftMonitor(oc)

	concern := AlignmentRecord{Ran: true, Aligned: false, Concern: "serves local time, intent was UTC"}
	aligned := AlignmentRecord{Ran: true, Aligned: true}
	skipped := AlignmentRecord{Ran: false}

	m.RecordAlignment(concern)
	m.RecordAlignment(aligned) // aligned tasks never count
	m.RecordAlignment(skipped) // skipped audits never count
	m.RecordAlignment(concern)
	if err := m.Check(ctx); err != nil {
		t.Fatalf("below threshold must keep dispatching: %v", err)
	}

	m.RecordAlignment(concern)
	err := m.Check(ctx)
	if err == nil {
		t.Fatal("the third concern breaches the threshold and pauses dispatch")
	}
	if !strings.Contains(err.Error(), "alignment") {
		t.Errorf("the refusal must name alignment degradation, got: %v", err)
	}
}
