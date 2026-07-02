package delivery

import (
	"fmt"
	"sort"
	"strings"

	"github.com/revelara-ai/orion/internal/orchestrator/spec"
	"github.com/revelara-ai/orion/internal/proof/hazard/stpa"
)

// RequiredRunbookSections are the sections a delivered component's runbook must
// carry (the 3 a.m. test).
var RequiredRunbookSections = []string{"incident_response", "escalation_path", "known_failure_modes", "operational_commands"}

// Runbook is the generated operability document, validated as a completion
// artifact (or-d82, PRD Phase F2 / Stories 17,25-27).
type Runbook struct {
	Sections map[string]string `json:"sections"`
}

// GenerateRunbook produces the runbook from the executable spec, the ratified
// STPA model (known failure modes), and the operating envelope.
func GenerateRunbook(es spec.ExecutableSpec, model stpa.Model, env OperatingEnvelope) Runbook {
	route := es.ResponseContract.Route
	port := es.ResponseContract.Port

	oncall := es.Decisions["oncall_escalation"]
	if oncall == "" {
		oncall = "single owner, log-only alert"
	}

	var failures strings.Builder
	ucas := append([]stpa.UCA(nil), model.UCAs...)
	sort.Slice(ucas, func(i, j int) bool { return ucas[i].ID < ucas[j].ID })
	for _, u := range ucas {
		failures.WriteString(fmt.Sprintf("- %s (%s) [%s]: %s\n", u.ID, u.Type, u.Disposition, u.Hazard))
	}

	ops := fmt.Sprintf(`- Build:  go build ./...
- Run:    PORT=%d ./svc   (defaults to :%d)
- Health: curl -fsS http://localhost:%d%s
- Logs:   structured logs on stderr (slog)
- Stop:   SIGTERM (graceful shutdown)`, port, port, port, route)

	incident := fmt.Sprintf(`1. Confirm the process is running and listening on :%d.
2. curl http://localhost:%d%s — expect a 200 with the contract-conformant body.
3. Inspect structured logs (stderr) for errors.
4. Restart the binary; verify the health check.
5. If unresolved, follow the escalation path.`, port, port, route)

	escalation := fmt.Sprintf("Primary: %s. If unresolved within the SLO window, escalate per team policy. Alerting: %s.", oncall, oncall)

	return Runbook{Sections: map[string]string{
		"incident_response":    incident,
		"escalation_path":      escalation,
		"known_failure_modes":  failures.String(),
		"operational_commands": ops,
		"scaling_assumptions":  "proven load: " + env.ProvenLoad + "; tier: " + env.Tier,
		"observability":        "structured logs (slog) on stderr; metrics/SLO alerting tracked as accepted gaps (see decision record)",
	}}
}

// Complete reports whether the runbook has all required sections, non-empty.
func (r Runbook) Complete() bool {
	for _, s := range RequiredRunbookSections {
		if strings.TrimSpace(r.Sections[s]) == "" {
			return false
		}
	}
	return true
}

// operabilityClaims are the runbook statements that assert something about the
// ARTIFACT — each carries the deterministic evidence predicate that must hold in
// the artifact source for the claim to stand (or-v9f.12: the 3 a.m. reader must
// never follow instructions the artifact cannot honor).
var operabilityClaims = []struct {
	name     string
	claimRE  string   // substring that identifies the claim in a section
	evidence []string // any of these substrings in the artifact source verifies it
}{
	{"structured-logs", "slog", []string{"log/slog", "slog."}},
	{"graceful-shutdown", "SIGTERM", []string{"os/signal", "signal.Notify"}},
}

// VerifyRunbook checks every operability claim against the artifact source.
// Unevidenced claims are rewritten with an UNVERIFIED marker (visible to the
// reviewer and the 3 a.m. operator) and returned in missing; verified claims
// pass through untouched.
func VerifyRunbook(rb Runbook, artifactSrc string) (Runbook, []string) {
	verified := Runbook{Sections: make(map[string]string, len(rb.Sections))}
	var missing []string
	flagged := map[string]bool{}
	for name, body := range rb.Sections {
		out := body
		for _, c := range operabilityClaims {
			if !strings.Contains(out, c.claimRE) {
				continue
			}
			ok := false
			for _, e := range c.evidence {
				if strings.Contains(artifactSrc, e) {
					ok = true
					break
				}
			}
			if !ok {
				out = strings.ReplaceAll(out, c.claimRE, c.claimRE+" [UNVERIFIED: not present in the artifact]")
				if !flagged[c.name] {
					flagged[c.name] = true
					missing = append(missing, c.name)
				}
			}
		}
		verified.Sections[name] = out
	}
	sort.Strings(missing)
	return verified, missing
}
