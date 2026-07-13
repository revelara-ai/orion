package reliabilityscan

import (
	"testing"

	"github.com/revelara-ai/orion/internal/reliabilitytier"
)

// or-xe7.6 piece 1: a severe org risk raises the dimensions (never lowers);
// benign/empty context is a no-op.
func TestEnrichDimensions(t *testing.T) {
	base := reliabilitytier.RiskDimensions{Reversible: true} // all zero
	// No risks / empty → untouched (still Throwaway-eligible).
	if got := EnrichDimensions(base, ""); got != base {
		t.Fatalf("empty risks must not change dimensions: %+v", got)
	}
	if got := EnrichDimensions(base, `[{"severity":"low","title":"minor"}]`); got.BlastRadius != 0 {
		t.Fatalf("a low-severity risk must not raise blast radius: %+v", got)
	}

	// A critical org risk raises blast radius to service level.
	got := EnrichDimensions(base, `{"risks":[{"severity":"critical","title":"cascading outage"}]}`)
	if got.BlastRadius < 1 {
		t.Fatalf("a critical org risk must raise blast radius: %+v", got)
	}
	// The tier reflects it: base classifies Throwaway; enriched is not.
	if reliabilitytier.Classify(base) != reliabilitytier.Throwaway {
		t.Fatal("fixture base must be Throwaway")
	}
	if reliabilitytier.Classify(got) == reliabilitytier.Throwaway {
		t.Fatalf("enriched dimensions must lift the tier off Throwaway: %+v", got)
	}

	// A PII-named severe risk raises data sensitivity → Critical.
	pii := EnrichDimensions(base, `{"risks":[{"severity":"high","title":"PII exposure in logs"}]}`)
	if pii.DataSensitivity < 2 {
		t.Fatalf("a PII severe risk must raise data sensitivity: %+v", pii)
	}
	if reliabilitytier.Classify(pii) != reliabilitytier.Critical {
		t.Fatalf("PII severe risk must force Critical: %+v", pii)
	}

	// NEVER lowers: a high risk against already-high dimensions stays high.
	high := reliabilitytier.RiskDimensions{DataSensitivity: 2, BlastRadius: 2, Reversible: false}
	if got := EnrichDimensions(high, `{"risks":[{"severity":"critical"}]}`); got.BlastRadius != 2 || got.DataSensitivity != 2 {
		t.Fatalf("enrich must never lower an existing dimension: %+v", got)
	}
}
