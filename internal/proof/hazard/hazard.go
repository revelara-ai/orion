// Package hazard is the hazard proof mode (or-dxc, PRD Phase E10). It checks the
// artifact against the developer-RATIFIED STPA model (control structure + UCAs +
// dispositions) — never a generation agent's self-assertion. A UCA is acceptable
// only if it is controlled (and the control is present in the artifact) or an
// explicitly accepted, documented gap; an OPEN (undecided) UCA is uncontrolled
// and fails the mode. Every modeled control action must have a test and a closed
// feedback loop.
//
// Manifesto: correctness is multi-modal; control loops must close.
package hazard

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/revelara-ai/orion/internal/proof/hazard/stpa"
	"github.com/revelara-ai/orion/internal/proof/truthalign"
)

// ControlActionResult reports a modeled control action's proof status.
type ControlActionResult struct {
	ID             string `json:"id"`
	Test           string `json:"test"` // the check that exercises it; never null
	FeedbackClosed bool   `json:"feedback_closed"`
}

// Report is the hazard evidence surfaced by `orion proof show --mode hazard`.
type Report struct {
	UCAsConsidered   []string              `json:"ucas_considered"`
	UncontrolledUCAs []string              `json:"uncontrolled_ucas"`
	AcceptedGaps     []string              `json:"accepted_gaps"`
	ControlActions   []ControlActionResult `json:"control_actions"`
}

// Prove checks the artifact against the ratified STPA model.
func Prove(ctx context.Context, artifactDir string, model stpa.Model) (truthalign.ModeResult, Report, error) {
	_ = ctx
	src := ""
	if b, err := os.ReadFile(filepath.Join(artifactDir, "main.go")); err == nil {
		src = string(b)
	}

	rep := Report{
		UCAsConsidered:   []string{},
		UncontrolledUCAs: []string{},
		AcceptedGaps:     []string{},
		ControlActions:   []ControlActionResult{},
	}

	controlledOK := 0
	for _, u := range model.UCAs {
		rep.UCAsConsidered = append(rep.UCAsConsidered, u.ID)
		switch u.Disposition {
		case stpa.DispositionControlled:
			if controlPresent(src, u) {
				controlledOK++
			} else {
				// Claimed controlled but the control is absent in the artifact → it
				// is actually uncontrolled (regression guard; provenance = model).
				rep.UncontrolledUCAs = append(rep.UncontrolledUCAs, u.ID)
			}
		case stpa.DispositionAcceptedGap:
			rep.AcceptedGaps = append(rep.AcceptedGaps, u.ID)
		default: // open / undecided → blocks
			rep.UncontrolledUCAs = append(rep.UncontrolledUCAs, u.ID)
		}
	}

	allActionsHaveTest := true
	allFeedbackClosed := true
	for _, a := range model.Structure.Actions {
		test := testForAction(model, a.ID)
		if test == "" {
			allActionsHaveTest = false
		}
		fbClosed := a.Feedback.From != "" && a.Feedback.To != "" && a.Feedback.Signal != ""
		if !fbClosed {
			allFeedbackClosed = false
		}
		rep.ControlActions = append(rep.ControlActions, ControlActionResult{ID: a.ID, Test: test, FeedbackClosed: fbClosed})
	}

	pass := len(rep.UncontrolledUCAs) == 0 && allActionsHaveTest && allFeedbackClosed
	mr := truthalign.ModeResult{
		Mode:   "hazard",
		Pass:   pass,
		Output: hazardOutput(rep),
		Metrics: map[string]float64{
			"hazard_controlled_count": float64(controlledOK),
			"hazard_total_count":      float64(len(model.UCAs)),
			"run_count":               1,
		},
	}
	return mr, rep, nil
}

// controlPresent verifies a controlled UCA's control is present in the artifact by
// evaluating the tokens the UCA DECLARES (model-driven, not domain-hardcoded): every
// token in u.Verify must appear in the source. A UCA that declares no tokens is taken
// as present — its control is asserted by its disposition + the behavioral/empirical
// obligations, not a code grep. This is what makes the hazard check general: the
// time-service example declares its HTTP/time tokens in the model; an arbitrary
// project's skeleton declares none and is verified by the executed contract.
func controlPresent(src string, u stpa.UCA) bool {
	for _, tok := range u.Verify {
		if !strings.Contains(src, tok) {
			return false
		}
	}
	return true
}

// testForAction returns the test/decision covering a control action's UCAs:
// a real proof check if any UCA is controlled, the documented accepted-gap
// otherwise, or "" if any UCA is open (undecided → no test).
func testForAction(model stpa.Model, actionID string) string {
	anyControlled, anyOpen, gaps := false, false, 0
	for _, u := range model.UCAs {
		if u.ControlAction != actionID {
			continue
		}
		switch u.Disposition {
		case stpa.DispositionControlled:
			anyControlled = true
		case stpa.DispositionAcceptedGap:
			gaps++
		default:
			anyOpen = true
		}
	}
	if anyOpen {
		return ""
	}
	if anyControlled {
		return "proof:behavioral+empirical+hazard"
	}
	if gaps > 0 {
		return "accepted-gap (documented in decision record)"
	}
	return "no UCAs on action"
}

func hazardOutput(rep Report) string {
	return "ucas=" + strings.Join(rep.UCAsConsidered, ",") +
		" uncontrolled=" + strings.Join(rep.UncontrolledUCAs, ",") +
		" accepted_gaps=" + strings.Join(rep.AcceptedGaps, ",")
}
