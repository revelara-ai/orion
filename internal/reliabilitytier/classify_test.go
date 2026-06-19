package reliabilitytier

import "testing"

// TestTierPolicyMapping: risk dimensions map to tiers, and each tier's policy
// escalates rigor (throwaway < standard < critical).
func TestTierPolicyMapping(t *testing.T) {
	cases := []struct {
		name string
		dims RiskDimensions
		want Tier
	}{
		{"benign reversible", RiskDimensions{Reversible: true}, Throwaway},
		{"moderate concurrency", RiskDimensions{ConcurrencyExposure: 1, Reversible: true}, Standard},
		{"PII forces critical", RiskDimensions{DataSensitivity: 2, Reversible: true}, Critical},
		{"regulated forces critical", RiskDimensions{Regulated: true, Reversible: true}, Critical},
		{"cross-system blast forces critical", RiskDimensions{BlastRadius: 2, Reversible: true}, Critical},
		{"irreversible service-blast → critical", RiskDimensions{BlastRadius: 1, Reversible: false}, Critical},
	}
	for _, c := range cases {
		if got := Classify(c.dims); got != c.want {
			t.Fatalf("%s: tier = %s, want %s", c.name, got, c.want)
		}
	}

	// Policy escalates by tier.
	tw := PolicyFor(Throwaway)
	st := PolicyFor(Standard)
	cr := PolicyFor(Critical)
	if !(tw.MutationThreshold < st.MutationThreshold && st.MutationThreshold < cr.MutationThreshold) {
		t.Fatalf("mutation thresholds not escalating: %.2f %.2f %.2f", tw.MutationThreshold, st.MutationThreshold, cr.MutationThreshold)
	}
	if tw.RequireAllModes {
		t.Fatal("throwaway should not require all three modes")
	}
	if !cr.RequireAllModes {
		t.Fatal("critical must require all three modes")
	}
	if cr.AutonomyAllowed {
		t.Fatal("V2.0 must never permit autonomous delivery")
	}
}
