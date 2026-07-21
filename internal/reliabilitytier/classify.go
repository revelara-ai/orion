package reliabilitytier

import (
	"os"
	"strconv"
)

// RiskDimensions are the risk inputs that determine a project's reliability tier
// (PRD reliability-tier: data sensitivity, concurrency exposure, blast radius,
// reversibility, regulated domain).
type RiskDimensions struct {
	DataSensitivity     int  // 0 none · 1 internal · 2 PII/secret
	ConcurrencyExposure int  // 0 none · 1 moderate · 2 high
	BlastRadius         int  // 0 local · 1 service · 2 cross-system
	Reversible          bool // can a bad change be rolled back cheaply?
	Regulated           bool // regulated domain (PCI/HIPAA/etc.)
}

// Classify maps risk dimensions to a tier. Regulated, PII, or large blast radius
// force Critical; a fully-benign, reversible, low-everything project is
// Throwaway; everything else is Standard.
func Classify(d RiskDimensions) Tier {
	if d.Regulated || d.DataSensitivity >= 2 || d.BlastRadius >= 2 {
		return Critical
	}
	if !d.Reversible && d.BlastRadius >= 1 {
		return Critical
	}
	score := d.DataSensitivity + d.ConcurrencyExposure + d.BlastRadius
	if score == 0 && d.Reversible {
		return Throwaway
	}
	return Standard
}

// Policy is the rigor a tier demands, consumed by proof (gate strictness) and
// delivery (autonomy gate — V2.0 is never autonomous).
type Policy struct {
	Tier              Tier
	MutationThreshold float64
	RequireAllModes   bool // require behavioral+empirical+hazard converge
	RequireEnvelope   bool // critical only: a delivery must carry a complete operating envelope (or-v9f.13)
	// RequireLiveReliabilityContext (or-xe7.6): critical only — a Critical-tier
	// delivery decided WITHOUT live revelara.ai reliability context cannot
	// attest the org's known controls/risks were considered, so it escalates.
	RequireLiveReliabilityContext bool
	// RejectUntracedSurface (or-g2qf.1): critical only — untraced artifact
	// surface (routes/exports with no spec lineage, drift dimension 3) blocks
	// delivery instead of only escalating; a resolved scope-creep escalation
	// covering the surface is the developer waiver that unblocks.
	RejectUntracedSurface bool
	AutonomyAllowed       bool // V2.3 only; always false in V2.0
}

// PolicyFor returns the policy for a tier.
func PolicyFor(t Tier) Policy {
	return Policy{
		Tier:                          t,
		MutationThreshold:             MutationThreshold(t),
		RequireAllModes:               t != Throwaway, // throwaway tools may ship on fewer modes
		RequireEnvelope:               t == Critical,  // critical ships only with proven load + controlled fault classes documented
		RequireLiveReliabilityContext: t == Critical,  // critical ships only on ATTESTED reliability context (or-xe7.6)
		RejectUntracedSurface:         t == Critical,  // critical: scope creep blocks delivery, not just an inbox row (or-g2qf.1)
		AutonomyAllowed:               false,          // V2.0/V2.1: human-mergeable only
	}
}

// AutonomyBar is the consecutive-clean-delivery count that EARNS autonomy
// (or-v9f.30; ORION_AUTONOMY_BAR overrides, floor 1).
func AutonomyBar() int {
	if v := os.Getenv("ORION_AUTONOMY_BAR"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 {
			return n
		}
	}
	return 5
}

// PolicyForRecord is the earned-autonomy ladder (or-v9f.30, the minimal
// or-lrr slice): the tier policy, with AutonomyAllowed earned by track
// record. Throwaway/Standard earn it after `consecutive` clean deliveries
// clear the bar; CRITICAL is permanently human-mergeable in this slice — no
// record buys autonomy there. Bare PolicyFor keeps returning false
// (back-compat: every existing caller stays human-mergeable).
func PolicyForRecord(t Tier, consecutive int) Policy {
	p := PolicyFor(t)
	if t != Critical && consecutive >= AutonomyBar() {
		p.AutonomyAllowed = true
	}
	return p
}
