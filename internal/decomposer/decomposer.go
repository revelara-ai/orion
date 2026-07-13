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
	"strings"

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
	// Provides/Requires (or-7et.5): the inter-module interface manifest.
	// Requires is checked pre-merge against deps' EXTRACTED surfaces;
	// Provides here is the proposer's declaration, never the proof truth.
	Provides []string
	Requires []string
}

// Epic is the decomposed unit of delivery.
type Epic struct {
	Title string
	Tasks []Task
}

// Decompose produces the Epic for an accepted spec. The FUNCTIONAL task is selected
// by projectType (a non-HTTP type does not get an HTTP "GET route on port" obligation);
// the scaffold + universal reliability tasks are shared across types, and CoverageGate
// stays generic (or-3ba.1). Deterministic for the V2.0 Go-greenfield path. Empty
// projectType defaults to http-service.
func Decompose(es spec.ExecutableSpec, projectType string) Epic {
	target := capacityTarget(es)

	tasks := []Task{
		scaffoldTask(es, projectType),
		functionalTask(projectType, es),
		{
			Key:             "capacity",
			Title:           "Apply capacity & concurrency controls",
			ProofObligation: fmt.Sprintf("request handling is bounded (timeouts + concurrency limits) to sustain the stated scale of ~%s", target),
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

// scaffoldTask is the project-skeleton task. The Go-specific scaffold (module +
// `go build`) applies only when the ratified direction IS Go (or the default);
// a ratified non-Go direction gets a language-honest skeleton obligation
// instead of a blind Go+HTTP one (or-045a.5 — the harness must never silently
// steer the plan back to its own comfort zone). Full non-Go proof execution is
// owned by or-4rxw; here the DECISION visibly shapes the plan.
func scaffoldTask(es spec.ExecutableSpec, projectType string) Task {
	lang := strings.ToLower(strings.TrimSpace(es.Decisions["direction.language"]))
	// or-hn15.4: an UNREGISTERED type with no chosen language must NOT inherit
	// the Go scaffold — a game whose direction.language was never answered is
	// unchosen, not Go. Emit a language-neutral skeleton so the plan doesn't
	// presume go.mod / `go build`. A registered type (http-service) keeps the Go
	// default, and any explicit language is honored below.
	if lang == "" && !completeness.RegisteredProjectType(projectType) {
		return Task{
			Key:             "scaffold",
			Title:           "Scaffold the project skeleton (language undecided — ratify direction.language)",
			ProofObligation: "the project skeleton builds cleanly from a fresh checkout once the language/toolchain is chosen (reduced proof until direction.language is ratified, or-4rxw)",
			FileScope:       ".",
			Covers:          []string{string(completeness.DimFunctional)},
		}
	}
	if lang == "" || lang == "go" {
		return Task{
			Key:             "scaffold",
			Title:           "Scaffold Go module and entrypoint",
			ProofObligation: "`go build ./...` succeeds and the binary starts cleanly",
			FileScope:       "go.mod,cmd/",
			Covers:          []string{string(completeness.DimFunctional)},
		}
	}
	return Task{
		Key:             "scaffold",
		Title:           fmt.Sprintf("Scaffold the %s project skeleton (direction.language=%s)", lang, lang),
		ProofObligation: fmt.Sprintf("the %s project builds cleanly from a fresh checkout — reduced proof until the harness gains this toolchain (or-4rxw)", lang),
		FileScope:       ".",
		Covers:          []string{string(completeness.DimFunctional)},
	}
}

// functionalTask is the per-type FUNCTIONAL task — the one that bakes in what the
// software does and how it is invoked. It keeps the key "handler" so the shared
// universal tasks' dependency edges hold across types. Only http-service gets an
// HTTP "GET <route> on port" obligation; any other type gets a domain-neutral
// "satisfies its declared behavioral contract" obligation (or-3ba.1). Add a case
// (cli, worker, library) to give that type its own functional obligation.
func functionalTask(projectType string, es spec.ExecutableSpec) Task {
	switch projectType {
	case "http-service", "":
		rc := es.ResponseContract
		format := es.Decisions["response_format"]
		return Task{
			Key:             "handler",
			Title:           fmt.Sprintf("Implement the %s handler (%s response)", rc.Route, format),
			ProofObligation: fmt.Sprintf("GET %s listens on port %d and returns a %s body satisfying the ResponseContract's declared cases", rc.Route, rc.Port, format),
			FileScope:       "internal/server/",
			Covers:          append([]string{string(completeness.DimFunctional)}, es.ResponseContract.RequiredCaseIDs()...),
			DependsOn:       []string{"scaffold"},
		}
	default:
		return Task{
			Key:             "handler",
			Title:           "Implement the declared behavior",
			ProofObligation: "the program satisfies its declared behavioral contract — the expected output for every declared case",
			FileScope:       "internal/",
			Covers:          append([]string{string(completeness.DimFunctional)}, es.ResponseContract.RequiredCaseIDs()...),
			DependsOn:       []string{"scaffold"},
		}
	}
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
	// Case-ID granularity (or-jh4): every declared behavioral case must be owned by a
	// task's ProofObligation, not just the functional DIMENSION — so a decomposition
	// that drops a specific case is caught at plan time (belt-and-suspenders for the
	// proof-time ObligationGate). A spec with no cases is vacuously covered here; the
	// requirement that a behavioral spec MUST declare cases is enforced elsewhere.
	var missingCases []string
	for _, cid := range es.ResponseContract.RequiredCaseIDs() {
		if !covered[cid] {
			missingCases = append(missingCases, cid)
		}
	}
	if len(missingCases) > 0 {
		return fmt.Errorf("coverage gap: behavioral case(s) with no ProofObligation: %v", missingCases)
	}
	return nil
}
