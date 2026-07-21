package delivery

import (
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/reliabilitytier"
)

// or-g2qf.1: at a tier that rejects untraced surface, scope creep blocks a
// bar-cleared delivery with the untraced list in the reason; a developer
// waiver unblocks it; tiers that don't enforce keep today's escalate-only
// (warn) behavior; an already-escalated result keeps its first reason.
func TestApplyScopeCreep(t *testing.T) {
	deliver := Result{Decision: Deliver, Reason: "bar met"}
	esc := Result{Decision: Escalate, Reason: "proof verdict is Reject, not Accept"}
	critical := reliabilitytier.PolicyFor(reliabilitytier.Critical)
	standard := reliabilitytier.PolicyFor(reliabilitytier.Standard)
	untraced := []string{"route /admin/policy", "CheckAccess"}

	got := ApplyScopeCreep(deliver, untraced, false, critical)
	if got.Decision != Escalate || !strings.Contains(got.Reason, "route /admin/policy") || !strings.Contains(got.Reason, "CheckAccess") {
		t.Fatalf("critical tier must escalate a Deliver with the untraced list in the reason: %+v", got)
	}
	if got := ApplyScopeCreep(deliver, untraced, true, critical); got.Decision != Deliver {
		t.Fatalf("a developer waiver must leave a Deliver untouched: %+v", got)
	}
	if got := ApplyScopeCreep(deliver, untraced, false, standard); got.Decision != Deliver {
		t.Fatalf("a non-enforcing tier must keep today's escalate-only behavior: %+v", got)
	}
	if got := ApplyScopeCreep(deliver, nil, false, critical); got.Decision != Deliver {
		t.Fatalf("no untraced surface must leave a Deliver untouched: %+v", got)
	}
	if got := ApplyScopeCreep(esc, untraced, false, critical); got.Reason != esc.Reason {
		t.Fatalf("an already-escalated result keeps its first reason: %+v", got)
	}
}

// or-g2qf.1: only Critical rejects untraced surface; Throwaway/Standard are
// byte-identical to before the field existed.
func TestRejectUntracedSurfaceByTier(t *testing.T) {
	if !reliabilitytier.PolicyFor(reliabilitytier.Critical).RejectUntracedSurface {
		t.Fatal("Critical must reject untraced surface")
	}
	if reliabilitytier.PolicyFor(reliabilitytier.Standard).RejectUntracedSurface {
		t.Fatal("Standard must not reject untraced surface")
	}
	if reliabilitytier.PolicyFor(reliabilitytier.Throwaway).RejectUntracedSurface {
		t.Fatal("Throwaway must not reject untraced surface")
	}
}
