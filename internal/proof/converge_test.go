package proof

import (
	"testing"

	"github.com/revelara-ai/orion/internal/proof/truthalign"
)

// TestConvergeRequiresAllThreeModes: a full verdict requires behavioral AND
// empirical AND hazard. Missing any → Inconclusive (never a 2-of-3 Accept); all
// three passing → Accept; any failing → Reject.
func TestConvergeRequiresAllThreeModes(t *testing.T) {
	b := truthalign.ModeResult{Mode: "behavioral", Pass: true}
	e := truthalign.ModeResult{Mode: "empirical", Pass: true}
	h := truthalign.ModeResult{Mode: "hazard", Pass: true}

	if got := truthalign.ConvergeFull(b, e).Verdict; got != truthalign.Inconclusive {
		t.Fatalf("2 modes (missing hazard) = %s, want Inconclusive", got)
	}
	if got := truthalign.ConvergeFull(b, e, h).Verdict; got != truthalign.Accept {
		t.Fatalf("all three passing = %s, want Accept", got)
	}
	hFail := truthalign.ModeResult{Mode: "hazard", Pass: false}
	if got := truthalign.ConvergeFull(b, e, hFail).Verdict; got != truthalign.Reject {
		t.Fatalf("hazard failing = %s, want Reject", got)
	}
}
