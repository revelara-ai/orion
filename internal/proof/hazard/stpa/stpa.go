// Package stpa is Orion's directed STPA questionnaire (or-j6o, PRD C7 / hazard
// mode). Adapted from the stpa-design-review skill: four GATED phases — losses →
// control structure (every control action must have a feedback path) → UCAs (the
// four STPA questions) → loss scenarios. Polaris supplies schema + reasonable
// defaults; the developer RATIFIES each gate (no rubber-stamping); the ratified
// model is the trusted source for hazard proof — never a generation agent's
// self-assertion.
//
// Manifesto: STAMP-driven hazard modeling; reliability calibrated to the project.
package stpa

import "fmt"

// Phase is a questionnaire gate.
type Phase int

const (
	PhaseLosses Phase = iota
	PhaseControlStructure
	PhaseUCAs
	PhaseLossScenarios
	PhaseComplete
)

func (p Phase) String() string {
	switch p {
	case PhaseLosses:
		return "losses"
	case PhaseControlStructure:
		return "control-structure"
	case PhaseUCAs:
		return "ucas"
	case PhaseLossScenarios:
		return "loss-scenarios"
	default:
		return "complete"
	}
}

// Loss is a numbered unacceptable outcome.
type Loss struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}

// FeedbackPath is a control loop's feedback edge. Every control action must have one.
type FeedbackPath struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Signal string `json:"signal"`
}

// ControlAction is a controller→process command with its feedback path.
type ControlAction struct {
	ID         string       `json:"id"`
	Controller string       `json:"controller"`
	Action     string       `json:"action"`
	Feedback   FeedbackPath `json:"feedback"`
}

// ControlStructure is the modeled controllers + control actions.
type ControlStructure struct {
	Controllers []string        `json:"controllers"`
	Actions     []ControlAction `json:"actions"`
}

// UCAType is one of the four STPA questions.
type UCAType string

const (
	NotProvided         UCAType = "not_provided"
	ProvidedIncorrectly UCAType = "provided_incorrectly"
	WrongTiming         UCAType = "wrong_timing"
	WrongDuration       UCAType = "wrong_duration"
)

// Disposition is the developer's decision on a UCA: it must be either controlled
// (a control exists/will exist) or an explicitly accepted, documented gap. An
// "open" UCA is undecided and BLOCKS moving forward. This encodes the developer's
// rule: don't block on fully addressing every UCA, but require explicit,
// documented approval of which are accepted as gaps (so a later/brownfield
// developer can find and close them).
type Disposition string

const (
	DispositionOpen        Disposition = "open" // undecided → blocks
	DispositionControlled  Disposition = "controlled"
	DispositionAcceptedGap Disposition = "accepted_gap"
)

// UCA is an unsafe control action with its developer disposition.
type UCA struct {
	ID            string      `json:"id"`
	ControlAction string      `json:"control_action"` // ControlAction.ID
	Type          UCAType     `json:"type"`
	Hazard        string      `json:"hazard"`
	LossRefs      []string    `json:"loss_refs"`
	Disposition   Disposition `json:"disposition"`
	Rationale     string      `json:"rationale,omitempty"` // why controlled, or why the gap is accepted
	DecidedBy     string      `json:"decided_by,omitempty"`
}

// LossScenario traces trigger → sustaining condition → loss, with mitigating controls.
type LossScenario struct {
	ID                  string   `json:"id"`
	Trigger             string   `json:"trigger"`
	SustainingCondition string   `json:"sustaining_condition"`
	Loss                string   `json:"loss"` // Loss.ID
	Controls            []string `json:"controls"`
}

// Model is the full developed STPA artifact set.
type Model struct {
	Losses    []Loss           `json:"losses"`
	Structure ControlStructure `json:"control_structure"`
	UCAs      []UCA            `json:"ucas"`
	Scenarios []LossScenario   `json:"loss_scenarios"`
}

// Questionnaire drives the gated, developer-ratified modeling. It refuses to
// advance a phase out of order and enforces the completeness rules.
type Questionnaire struct {
	phase Phase
	model Model
}

// New starts a questionnaire at the losses phase.
func New() *Questionnaire { return &Questionnaire{phase: PhaseLosses} }

// Phase returns the current gate.
func (q *Questionnaire) Phase() Phase { return q.phase }

// RatifyLosses ratifies phase 1. At least one loss is required.
func (q *Questionnaire) RatifyLosses(losses []Loss) error {
	if q.phase != PhaseLosses {
		return fmt.Errorf("stpa: cannot ratify losses in phase %s", q.phase)
	}
	if len(losses) == 0 {
		return fmt.Errorf("stpa: at least one loss is required")
	}
	q.model.Losses = losses
	q.phase = PhaseControlStructure
	return nil
}

// RatifyControlStructure ratifies phase 2. Completeness rule: EVERY control action
// must have a feedback path (this is what makes control loops testable in proof).
func (q *Questionnaire) RatifyControlStructure(cs ControlStructure) error {
	if q.phase != PhaseControlStructure {
		return fmt.Errorf("stpa: cannot ratify control structure in phase %s", q.phase)
	}
	if len(cs.Actions) == 0 {
		return fmt.Errorf("stpa: at least one control action is required")
	}
	for _, a := range cs.Actions {
		if a.Feedback.From == "" || a.Feedback.To == "" || a.Feedback.Signal == "" {
			return fmt.Errorf("stpa: control action %q has no feedback path (every action must close a loop)", a.ID)
		}
	}
	q.model.Structure = cs
	q.phase = PhaseUCAs
	return nil
}

// RatifyUCAs ratifies phase 3. Each UCA must reference a modeled control action.
func (q *Questionnaire) RatifyUCAs(ucas []UCA) error {
	if q.phase != PhaseUCAs {
		return fmt.Errorf("stpa: cannot ratify UCAs in phase %s", q.phase)
	}
	if len(ucas) == 0 {
		return fmt.Errorf("stpa: at least one UCA is required")
	}
	valid := map[string]bool{}
	for _, a := range q.model.Structure.Actions {
		valid[a.ID] = true
	}
	for _, u := range ucas {
		if !valid[u.ControlAction] {
			return fmt.Errorf("stpa: UCA %q references unknown control action %q", u.ID, u.ControlAction)
		}
	}
	q.model.UCAs = ucas
	q.phase = PhaseLossScenarios
	return nil
}

// RatifyLossScenarios ratifies phase 4 and completes the questionnaire.
func (q *Questionnaire) RatifyLossScenarios(scenarios []LossScenario) error {
	if q.phase != PhaseLossScenarios {
		return fmt.Errorf("stpa: cannot ratify loss scenarios in phase %s", q.phase)
	}
	if len(scenarios) == 0 {
		return fmt.Errorf("stpa: at least one loss scenario is required")
	}
	q.model.Scenarios = scenarios
	q.phase = PhaseComplete
	return nil
}

// Model returns the ratified model, only once all gates are complete.
func (q *Questionnaire) Model() (Model, error) {
	if q.phase != PhaseComplete {
		return Model{}, fmt.Errorf("stpa: questionnaire incomplete (at phase %s)", q.phase)
	}
	return q.model, nil
}
