// Package decomposer turns an accepted executable spec into an Epic of Tasks
// (or-f3v, PRD Trace 2 / Phase D). Each Task carries a ProofObligation (what it
// owes as proof), a declared file scope (for path leasing in integration), the
// spec requirements it covers, and its dependency edges (the DAG). A coverage
// gate asserts every spec requirement maps to at least one ProofObligation —
// closing the "decomposer narrows the proof surface" leak.
//
// Manifesto: small bounded steps; every requirement is proven. The decomposition
// is deterministic for the V2.0 Go-greenfield path (no LLM), so the plan is
// stable and the coverage gate is meaningful.
package decomposer

import (
	"fmt"
	"sort"

	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
)

// Task is one unit of the Epic.
type Task struct {
	Key             string   // stable key within the epic (deps reference this)
	Title           string   // human-readable
	ProofObligation string   // what this task must prove
	FileScope       string   // declared file scope (path leasing)
	Covers          []string // spec requirement keys this task's obligation covers
	DependsOn       []string // keys of prerequisite tasks
}

// Epic is the decomposed unit of delivery.
type Epic struct {
	Title string
	Tasks []Task
}

// Decompose produces the Epic for an accepted spec. Deterministic for the
// http-service path.
func Decompose(es spec.ExecutableSpec) Epic {
	rc := es.ResponseContract
	route, tz := rc.Route, rc.TimeZone
	port := rc.Port
	format := es.Decisions["response_format"]

	cap := capacityTarget(es)

	tasks := []Task{
		{
			Key:             "scaffold",
			Title:           "Scaffold Go module and entrypoint",
			ProofObligation: "`go build ./...` succeeds and the binary starts cleanly",
			FileScope:       "go.mod,cmd/",
			Covers:          []string{string(completeness.DimFunctional)},
		},
		{
			Key:             "handler",
			Title:           fmt.Sprintf("Implement %s handler returning %s time in %s", route, format, tz),
			ProofObligation: fmt.Sprintf("GET %s listens on port %d and returns a %s body containing the current time in %s, conforming to the ResponseContract", route, port, format, tz),
			FileScope:       "internal/server/",
			Covers:          []string{string(completeness.DimFunctional)},
			DependsOn:       []string{"scaffold"},
		},
		{
			Key:             "capacity",
			Title:           "Apply capacity & concurrency controls",
			ProofObligation: fmt.Sprintf("request handling is bounded (timeouts + concurrency limits) to sustain the stated scale of ~%s", cap),
			FileScope:       "internal/server/",
			Covers:          []string{string(completeness.DimScale)},
			DependsOn:       []string{"handler"},
		},
		{
			Key:             "observability",
			Title:           "Emit structured logs, metrics, and traces",
			ProofObligation: "the service emits structured logs and a metrics/trace surface per the observability signals",
			FileScope:       "internal/server/,internal/obs/",
			Covers:          []string{string(completeness.DimObservability)},
			DependsOn:       []string{"handler"},
		},
		{
			Key:             "operability",
			Title:           "Generate runbook, escalation, and SLO surface",
			ProofObligation: "a runbook with incident_response/escalation_path/known_failure_modes/operational_commands exists and the on-call escalation + SLO targets are recorded",
			FileScope:       "docs/runbook/",
			Covers:          []string{string(completeness.DimOnCall), string(completeness.DimSLO)},
			DependsOn:       []string{"handler"},
		},
		{
			Key:             "security",
			Title:           "Apply security controls and dependency provenance",
			ProofObligation: "authn/z matches the security model, no hardcoded secrets are present, persisted data honors the data policy, and every dependency is provenance-checked",
			FileScope:       "internal/server/,go.mod",
			Covers:          []string{string(completeness.DimSecurity), string(completeness.DimData), string(completeness.DimDependencies)},
			DependsOn:       []string{"handler"},
		},
	}

	return Epic{Title: "Deliver: " + es.Intent, Tasks: tasks}
}

// capacityTarget renders the concrete capacity threshold from the spec's scale
// dimension (a fallback preset expands to a real number).
func capacityTarget(es spec.ExecutableSpec) string {
	if th, ok := completeness.ResolveScalePreset(es.Decisions["scale_profile"]); ok {
		return fmt.Sprintf("%d req/%s", th.RequestsPerWindow, th.Window)
	}
	return es.Decisions["scale_profile"]
}

// Requirements returns the spec requirement keys that must each be covered by at
// least one ProofObligation — the spec's dimensions.
func Requirements(es spec.ExecutableSpec) []string {
	var out []string
	for _, d := range es.Dimensions {
		out = append(out, string(d.Name))
	}
	sort.Strings(out)
	return out
}

// CoverageGate asserts every spec requirement maps to >=1 ProofObligation and
// that no task has an empty obligation. Returns an error naming any gap.
func CoverageGate(es spec.ExecutableSpec, epic Epic) error {
	covered := map[string]bool{}
	for _, t := range epic.Tasks {
		if t.ProofObligation == "" {
			return fmt.Errorf("task %q has no ProofObligation", t.Key)
		}
		for _, c := range t.Covers {
			covered[c] = true
		}
	}
	var missing []string
	for _, req := range Requirements(es) {
		if !covered[req] {
			missing = append(missing, req)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("coverage gap: spec requirement(s) with no ProofObligation: %v", missing)
	}
	return nil
}
