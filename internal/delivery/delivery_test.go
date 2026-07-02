package delivery

import (
	"testing"

	"github.com/revelara-ai/orion/internal/proof/truthalign"
	"github.com/revelara-ai/orion/internal/reliabilitytier"
)

// TestDeploymentBarByTier: the bar decision depends on the tier. A throwaway tier
// ships on an Accept even without all three modes; standard/critical require the
// full converge; any Reject escalates. Bar-not-met never silently ships.
func TestDeploymentBarByTier(t *testing.T) {
	env := OperatingEnvelope{ProvenLoad: "1000 req/minute", FaultClassesControlled: []string{"slowloris", "unbounded latency"}}
	twoModes := []string{"behavioral", "empirical"}
	threeModes := []string{"behavioral", "empirical", "hazard"}

	// Throwaway: Accept on 2 modes → delivers.
	r := EvaluateBar(truthalign.Accept, twoModes, reliabilitytier.PolicyFor(reliabilitytier.Throwaway), env, true)
	if r.Decision != Deliver || !r.HumanMergeable || r.Envelope == nil {
		t.Fatalf("throwaway accept should deliver human-mergeable with envelope; got %+v", r)
	}
	if r.Envelope.Tier != "throwaway" {
		t.Fatalf("envelope tier = %q", r.Envelope.Tier)
	}

	// Standard: Accept on only 2 modes → escalate (needs all three).
	r = EvaluateBar(truthalign.Accept, twoModes, reliabilitytier.PolicyFor(reliabilitytier.Standard), env, true)
	if r.Decision != Escalate || r.Envelope != nil {
		t.Fatalf("standard accept w/o hazard should escalate; got %+v", r)
	}

	// Critical: full converge → delivers.
	r = EvaluateBar(truthalign.Accept, threeModes, reliabilitytier.PolicyFor(reliabilitytier.Critical), env, true)
	if r.Decision != Deliver || r.Envelope == nil {
		t.Fatalf("critical full converge should deliver; got %+v", r)
	}

	// Any Reject → escalate, no ship.
	r = EvaluateBar(truthalign.Reject, threeModes, reliabilitytier.PolicyFor(reliabilitytier.Standard), env, true)
	if r.Decision != Escalate || r.HumanMergeable || r.Envelope != nil {
		t.Fatalf("reject must escalate and never ship; got %+v", r)
	}

	// Security gate failure escalates even on a full Accept.
	r = EvaluateBar(truthalign.Accept, threeModes, reliabilitytier.PolicyFor(reliabilitytier.Standard), env, false)
	if r.Decision != Escalate || r.Envelope != nil {
		t.Fatalf("security-gate failure must escalate; got %+v", r)
	}
}

// TestCriticalTierRequiresCompleteEnvelope (or-v9f.13): the first real behavioral
// difference between Standard and Critical at the bar — a critical delivery
// without a complete operating envelope (proven load + controlled fault classes)
// escalates with a named reason; the same envelope passes at Standard.
func TestCriticalTierRequiresCompleteEnvelope(t *testing.T) {
	threeModes := []string{"behavioral", "empirical", "hazard"}
	empty := OperatingEnvelope{}

	r := EvaluateBar(truthalign.Accept, threeModes, reliabilitytier.PolicyFor(reliabilitytier.Critical), empty, true)
	if r.Decision != Escalate {
		t.Fatalf("critical without a complete envelope must escalate, got %+v", r)
	}
	if r.Reason == "" || r.Envelope != nil {
		t.Fatalf("the escalation must carry a named reason and no envelope, got %+v", r)
	}

	if r := EvaluateBar(truthalign.Accept, threeModes, reliabilitytier.PolicyFor(reliabilitytier.Standard), empty, true); r.Decision != Deliver {
		t.Fatalf("standard tolerates an incomplete envelope (calibrated rigor), got %+v", r)
	}

	full := OperatingEnvelope{ProvenLoad: "500 req/min", FaultClassesControlled: []string{"timeout"}}
	if r := EvaluateBar(truthalign.Accept, threeModes, reliabilitytier.PolicyFor(reliabilitytier.Critical), full, true); r.Decision != Deliver {
		t.Fatalf("critical with a complete envelope must deliver, got %+v", r)
	}
}
