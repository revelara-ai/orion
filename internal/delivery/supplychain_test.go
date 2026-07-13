package delivery

import (
	"strings"
	"testing"
)

// or-ykz.16: OSV findings block a bar-cleared delivery; a skip or an already-
// escalated result is untouched (first reason wins; skips surface elsewhere).
func TestApplySupplyChain(t *testing.T) {
	deliver := Result{Decision: Deliver, Reason: "bar met"}
	esc := Result{Decision: Escalate, Reason: "proof verdict is Reject, not Accept"}

	got := ApplySupplyChain(deliver, "known-vulnerable dependencies: x@v1 (CVE-2026-0001)", 1)
	if got.Decision != Escalate || !strings.Contains(got.Reason, "CVE-2026-0001") {
		t.Fatalf("findings must escalate a Deliver with the ids in the reason: %+v", got)
	}
	if got := ApplySupplyChain(deliver, "2 dependencies clean (OSV)", 0); got.Decision != Deliver {
		t.Fatalf("no findings must leave a Deliver untouched: %+v", got)
	}
	if got := ApplySupplyChain(esc, "known-vulnerable dependencies: x", 1); got.Reason != esc.Reason {
		t.Fatalf("an already-escalated result keeps its first reason: %+v", got)
	}
}
