package delivery

import (
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
	"github.com/revelara-ai/orion/internal/proof/hazard/stpa"
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
