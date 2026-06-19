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
			if controlPresent(src, u.ID) {
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

// controlPresent verifies the artifact contains the control for a controlled UCA
// (V2.0 token check for the Go time-service path).
func controlPresent(src, ucaID string) bool {
	switch ucaID {
	case "UCA1": // handler must exist / serve
		return strings.Contains(src, "handleTime") && strings.Contains(src, "ListenAndServe")
	case "UCA2": // correctness: UTC
		return strings.Contains(src, "UTC")
	case "UCA3": // latency bounds: read/write timeouts
		return strings.Contains(src, "ReadTimeout") && strings.Contains(src, "WriteTimeout")
	case "UCA4": // slowloris: read-header timeout
		return strings.Contains(src, "ReadHeaderTimeout")
	default:
		return false
	}
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
