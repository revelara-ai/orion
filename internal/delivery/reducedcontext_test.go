package delivery

import (
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/proof/truthalign"
	"github.com/revelara-ai/orion/internal/reliabilitytier"
)

// or-xe7.6 piece 2: a Critical-tier delivery on REDUCED reliability context
// escalates; a Standard-tier delivery does not; live context ships.
func TestReducedContextBarPolicy(t *testing.T) {
	fullEnv := OperatingEnvelope{ProvenLoad: "100 rps", FaultClassesControlled: []string{"timeout"}}

	// Critical + reduced context → escalate, naming the cause.
	critReduced := fullEnv
	critReduced.ReducedReliabilityContext = true
	res := EvaluateBar(truthalign.Accept, []string{"behavioral", "empirical", "hazard"},
		reliabilitytier.PolicyFor(reliabilitytier.Critical), critReduced, true, nil)
	if res.Decision != Escalate || !strings.Contains(res.Reason, "LIVE reliability context") {
		t.Fatalf("critical + reduced must escalate: %+v", res)
	}

	// Critical + LIVE context (not reduced) → delivers.
	critLive := fullEnv // ReducedReliabilityContext=false
	res = EvaluateBar(truthalign.Accept, []string{"behavioral", "empirical", "hazard"},
		reliabilitytier.PolicyFor(reliabilitytier.Critical), critLive, true, nil)
	if res.Decision != Deliver {
		t.Fatalf("critical + live context must deliver: %+v", res)
	}

	// Standard + reduced context → the reduced-context clause does NOT gate
	// (only Critical requires live context).
	stdReduced := OperatingEnvelope{ReducedReliabilityContext: true}
	res = EvaluateBar(truthalign.Accept, []string{"behavioral", "empirical", "hazard"},
		reliabilitytier.PolicyFor(reliabilitytier.Standard), stdReduced, true, nil)
	if res.Decision != Deliver {
		t.Fatalf("standard + reduced context must still deliver: %+v", res)
	}
}
