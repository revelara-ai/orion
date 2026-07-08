package delivery

import (
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
	"github.com/revelara-ai/orion/internal/proof/hazard/stpa"
	"github.com/revelara-ai/orion/internal/proof/truthalign"
	"github.com/revelara-ai/orion/internal/reliabilitytier"
)

// TestRunbookCompleteness: the generated runbook carries every required section,
// and the known-failure-modes section reflects the ratified STPA UCAs.
func TestRunbookCompleteness(t *testing.T) {
	answers := map[string]string{
		"response_format": "json", "timezone": "UTC", "port": "8080", "route": "/time",
		"scale_profile": "medium", "observability_signals": "logs", "oncall_escalation": "team-sre",
		"data_storage": "none", "slo_targets": "tier-default", "security_model": "untrusted", "dependencies": "none",
	}
	es, err := spec.Compile("Build an HTTP service that returns the current time.", answers,
		map[string]string{"scale_profile": "fallback_preset"}, completeness.NewAnalyzer("http-service").Checklist(), nil)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	rb := GenerateRunbook(es, stpa.RatifiedTimeServiceModel(), OperatingEnvelope{ProvenLoad: "1000 req/minute", Tier: "standard"})

	if !rb.Complete() {
		t.Fatalf("runbook missing required sections; have %v", keys(rb.Sections))
	}
	for _, s := range RequiredRunbookSections {
		if strings.TrimSpace(rb.Sections[s]) == "" {
			t.Fatalf("section %q empty", s)
		}
	}
	// Known failure modes reflect the ratified UCAs.
	if !strings.Contains(rb.Sections["known_failure_modes"], "UCA1") {
		t.Fatalf("known_failure_modes should list ratified UCAs:\n%s", rb.Sections["known_failure_modes"])
	}
	// Operational commands include the health check on the spec's route.
	if !strings.Contains(rb.Sections["operational_commands"], "/time") {
		t.Fatalf("operational_commands missing the route health check")
	}
}

func keys(m map[string]string) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

// TestVerifyRunbookMarksUnevidencedClaims (or-v9f.12): a runbook claim the
// artifact cannot honor is marked UNVERIFIED and reported — never repeated as
// fact to the 3 a.m. operator.
func TestVerifyRunbookMarksUnevidencedClaims(t *testing.T) {
	rb := Runbook{Sections: map[string]string{
		"operational_commands": "- Logs: structured logs on stderr (slog)\n- Stop: SIGTERM (graceful shutdown)",
	}}

	bare := "package main\nfunc main() {}\n"
	verified, missing := VerifyRunbook(rb, bare)
	if len(missing) != 2 {
		t.Fatalf("bare artifact honors neither claim, got missing=%v", missing)
	}
	if !strings.Contains(verified.Sections["operational_commands"], "UNVERIFIED") {
		t.Errorf("unevidenced claims must be marked:\n%s", verified.Sections["operational_commands"])
	}

	instrumented := "package main\nimport (\"log/slog\"\n\"os/signal\")\nfunc main() { signal.Notify(nil) ; slog.Info(\"up\") }\n"
	verified, missing = VerifyRunbook(rb, instrumented)
	if len(missing) != 0 {
		t.Fatalf("instrumented artifact honors both claims, got missing=%v", missing)
	}
	if strings.Contains(verified.Sections["operational_commands"], "UNVERIFIED") {
		t.Errorf("verified claims must pass through untouched:\n%s", verified.Sections["operational_commands"])
	}
}

// TestCriticalTierRefusesUnverifiedOperability: the highest tier does not ship
// instructions the artifact cannot honor.
func TestCriticalTierRefusesUnverifiedOperability(t *testing.T) {
	env := OperatingEnvelope{ProvenLoad: "100 req/min", FaultClassesControlled: []string{"timeout"}}
	modes := []string{"behavioral", "empirical", "hazard"}
	r := EvaluateBar(truthalign.Accept, modes, reliabilitytier.PolicyFor(reliabilitytier.Critical), env, true, []string{"structured-logs"})
	if r.Decision != Escalate || !strings.Contains(r.Reason, "operability") {
		t.Fatalf("critical + unverified operability must escalate with a named reason, got %+v", r)
	}
	if r := EvaluateBar(truthalign.Accept, modes, reliabilitytier.PolicyFor(reliabilitytier.Standard), env, true, []string{"structured-logs"}); r.Decision != Deliver {
		t.Fatalf("standard delivers with UNVERIFIED markers visible, got %+v", r)
	}
}
