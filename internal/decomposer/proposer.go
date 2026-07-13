package decomposer

import (
	"context"
	"fmt"
	"sort"

	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
)

// ProposedModule is one semantic vertical-slice module a ModuleProposer emits
// (or-809). It maps 1:1 onto a decomposer.Task so the existing DAG runs
// unchanged; the proposer chooses the slicing, the deterministic gates
// (ReconcileFloor + CoverageGate + proof-time EnforceObligations) keep it honest.
type ProposedModule struct {
	Key             string   `json:"key"`
	Title           string   `json:"title"`
	ProofObligation string   `json:"proof_obligation"`
	FileScope       string   `json:"file_scope"`
	Covers          []string `json:"covers"` // dimension keys AND behavioral case IDs this module owns
	DependsOn       []string `json:"depends_on"`
}

// ModuleProposer proposes the module set for an accepted spec. It is assumed
// ADVERSARIAL (an LLM): its output is never trusted directly — Propose runs it,
// then the deterministic backstops gate the result. floor is the reliability
// dimension set the proposer MUST cover (it may re-slice, never drop).
type ModuleProposer func(ctx context.Context, es spec.ExecutableSpec, projectType string, floor []completeness.Dimension) ([]ProposedModule, error)

// DefaultFloor is the reliability-floor dimension set the V2 template covers —
// the proposer-independent required set ReconcileFloor enforces (or-809 G2).
func DefaultFloor() []completeness.Dimension {
	return []completeness.Dimension{
		completeness.DimFunctional, completeness.DimScale, completeness.DimObservability,
		completeness.DimOnCall, completeness.DimData, completeness.DimSLO,
		completeness.DimSecurity, completeness.DimDependencies,
	}
}

// ReconcileFloor rejects an epic whose modules do not, between them, cover every
// floor dimension — regardless of how the proposer sliced, and independent of
// the proposer's own labels being otherwise plausible (or-809 G2). This is the
// strongest trust-wall layer: the reliability floor is structurally un-droppable.
func ReconcileFloor(floor []completeness.Dimension, epic Epic) error {
	covered := map[string]bool{}
	for _, t := range epic.Tasks {
		for _, c := range t.Covers {
			covered[c] = true
		}
	}
	var missing []string
	for _, d := range floor {
		if !covered[string(d)] {
			missing = append(missing, string(d))
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("reliability floor gap: no module covers dimension(s): %v", missing)
	}
	return nil
}

// CoverageDiff reports whether proposer's coverage is a SUPERSET of oracle's —
// the deterministic shadow-cutover safety assertion (or-809 I2): the new plan
// must cover everything the proven template covered. missing lists what the
// oracle covers that the proposer does not (empty ⇒ superset holds).
func CoverageDiff(proposer, oracle Epic) (superset bool, missing []string) {
	pc := coverSet(proposer)
	for item := range coverSet(oracle) {
		if !pc[item] {
			missing = append(missing, item)
		}
	}
	sort.Strings(missing)
	return len(missing) == 0, missing
}

func coverSet(e Epic) map[string]bool {
	s := map[string]bool{}
	for _, t := range e.Tasks {
		for _, c := range t.Covers {
			s[c] = true
		}
	}
	return s
}

// Propose runs the (adversarial) ModuleProposer, converts its modules to the
// Epic the DAG consumes, and SYNTHESIZES a deterministic bookend "acceptance"
// module (or-809 G3): it depends on every proposed module and faces the
// whole-intent contract (all floor dims + every required case id), so a narrow
// or maliciously-sliced module set still meets the full intent at a leaf-
// dependent node. The bookend is synthesized by Orion, never taken from the LLM.
func Propose(ctx context.Context, es spec.ExecutableSpec, projectType string, floor []completeness.Dimension, mp ModuleProposer) (Epic, error) {
	mods, err := mp(ctx, es, projectType, floor)
	if err != nil {
		return Epic{}, fmt.Errorf("module proposer: %w", err)
	}
	if len(mods) == 0 {
		return Epic{}, fmt.Errorf("module proposer returned no modules")
	}
	return bookendEpic(es, floor, mods), nil
}

// bookendEpic converts proposed modules to the Epic the DAG consumes and
// appends the deterministic whole-intent acceptance bookend.
func bookendEpic(es spec.ExecutableSpec, floor []completeness.Dimension, mods []ProposedModule) Epic {
	tasks := make([]Task, 0, len(mods)+1)
	keys := make([]string, 0, len(mods))
	for _, m := range mods {
		if m.Key == "acceptance" {
			continue // the bookend is Orion's to synthesize; never the proposer's
		}
		tasks = append(tasks, Task{
			Key: m.Key, Title: m.Title, ProofObligation: m.ProofObligation,
			FileScope: m.FileScope, Covers: m.Covers, DependsOn: m.DependsOn,
		})
		keys = append(keys, m.Key)
	}
	tasks = append(tasks, acceptanceModule(es, floor, keys))
	return Epic{Title: es.Intent, Tasks: tasks}
}

// acceptanceModule is the deterministic whole-intent bookend.
func acceptanceModule(es spec.ExecutableSpec, floor []completeness.Dimension, deps []string) Task {
	covers := make([]string, 0, len(floor))
	for _, d := range floor {
		covers = append(covers, string(d))
	}
	covers = append(covers, es.ResponseContract.RequiredCaseIDs()...)
	sort.Strings(deps)
	return Task{
		Key:             "acceptance",
		Title:           "Whole-intent acceptance",
		ProofObligation: "the assembled system satisfies the whole-intent contract: " + es.Intent,
		Covers:          covers,
		DependsOn:       deps,
	}
}
