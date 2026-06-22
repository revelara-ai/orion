package conductor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/revelara-ai/orion/internal/brownfield"
	"github.com/revelara-ai/orion/internal/llm"
	"github.com/revelara-ai/orion/internal/proof/hazard/stpa"
)

// AnalyzeSTAMPBaseline proposes a STAMP control-structure model for the EXISTING
// system, grounded in its functional model + structure: the LOSSES it must avoid, the
// CONTROLLERS + CONTROL ACTIONS (with feedback) that constitute its control structure,
// and the UNSAFE CONTROL ACTIONS (each with the hazard, the losses it leads to, and the
// code tokens that prove the control is present). This is the brownfield BASELINE — the
// "what must not break" half of the two-model pairing.
//
// It is LLM-PROPOSED; every UCA's disposition is left OPEN, because STPA is human-owned:
// the developer ratifies (controlled / accepted-gap) before it becomes the project's
// baseline. The Verify tokens feed the (now model-driven) hazard proof, so a change is
// later held to PRESERVE the baseline's controlled hazards.
func AnalyzeSTAMPBaseline(ctx context.Context, provider llm.Provider, m brownfield.RepoMap, fm FunctionalModel) (stpa.Model, error) {
	tool := llm.Tool{
		Name:        "report_stamp_baseline",
		Description: "Report a STAMP control-structure baseline for the existing system: losses, controllers, control actions (with feedback), and unsafe control actions (UCAs).",
		InputSchema: json.RawMessage(`{"type":"object","properties":{
			"losses":{"type":"array","items":{"type":"object","properties":{"id":{"type":"string"},"description":{"type":"string"}},"required":["id","description"]}},
			"controllers":{"type":"array","items":{"type":"string"}},
			"control_actions":{"type":"array","items":{"type":"object","properties":{"id":{"type":"string"},"controller":{"type":"string"},"action":{"type":"string"},"feedback_from":{"type":"string"},"feedback_to":{"type":"string"},"feedback_signal":{"type":"string"}},"required":["id","controller","action"]}},
			"ucas":{"type":"array","items":{"type":"object","properties":{"id":{"type":"string"},"control_action":{"type":"string"},"type":{"type":"string","enum":["not_provided","provided_incorrectly","wrong_timing","wrong_duration"]},"hazard":{"type":"string"},"loss_refs":{"type":"array","items":{"type":"string"}},"verify":{"type":"array","items":{"type":"string"}}},"required":["id","control_action","type","hazard"]}}
		},"required":["losses","controllers","control_actions","ucas"]}`),
	}
	resp, err := provider.Chat(ctx, llm.ChatRequest{
		System:   stampBaselinePrompt,
		Tools:    []llm.Tool{tool},
		Messages: []llm.Message{llm.TextMessage(llm.RoleUser, m.Digest()+"\n\n"+fm.Digest()+"\n\nCall report_stamp_baseline for THIS system's control structure.")},
	})
	if err != nil {
		return stpa.Model{}, err
	}
	for _, tu := range resp.ToolUses() {
		if tu.Name == "report_stamp_baseline" {
			return decodeBaseline(tu.Input)
		}
	}
	return stpa.Model{}, nil
}

func decodeBaseline(in json.RawMessage) (stpa.Model, error) {
	type caWire struct {
		ID             string `json:"id"`
		Controller     string `json:"controller"`
		Action         string `json:"action"`
		FeedbackFrom   string `json:"feedback_from"`
		FeedbackTo     string `json:"feedback_to"`
		FeedbackSignal string `json:"feedback_signal"`
	}
	var raw struct {
		Losses         []struct{ ID, Description string } `json:"losses"`
		Controllers    []string                           `json:"controllers"`
		ControlActions []caWire                           `json:"control_actions"`
		UCAs           []struct {
			ID            string   `json:"id"`
			ControlAction string   `json:"control_action"`
			Type          string   `json:"type"`
			Hazard        string   `json:"hazard"`
			LossRefs      []string `json:"loss_refs"`
			Verify        []string `json:"verify"`
		} `json:"ucas"`
	}
	if err := json.Unmarshal(in, &raw); err != nil {
		return stpa.Model{}, fmt.Errorf("stamp baseline: decode: %w", err)
	}

	var model stpa.Model
	for _, l := range raw.Losses {
		model.Losses = append(model.Losses, stpa.Loss{ID: l.ID, Description: l.Description})
	}
	model.Structure.Controllers = raw.Controllers
	for _, ca := range raw.ControlActions {
		model.Structure.Actions = append(model.Structure.Actions, stpa.ControlAction{
			ID: ca.ID, Controller: ca.Controller, Action: ca.Action,
			Feedback: stpa.FeedbackPath{From: ca.FeedbackFrom, To: ca.FeedbackTo, Signal: ca.FeedbackSignal},
		})
	}
	for _, u := range raw.UCAs {
		model.UCAs = append(model.UCAs, stpa.UCA{
			ID: u.ID, ControlAction: u.ControlAction, Type: normalizeUCAType(u.Type),
			Hazard: u.Hazard, LossRefs: u.LossRefs, Verify: u.Verify,
			Disposition: stpa.DispositionOpen, // human ratifies before it is the baseline
		})
	}
	return model, nil
}

func normalizeUCAType(s string) stpa.UCAType {
	switch stpa.UCAType(strings.TrimSpace(s)) {
	case stpa.ProvidedIncorrectly:
		return stpa.ProvidedIncorrectly
	case stpa.WrongTiming:
		return stpa.WrongTiming
	case stpa.WrongDuration:
		return stpa.WrongDuration
	default:
		return stpa.NotProvided
	}
}

// RenderBaseline renders a proposed STAMP model for developer review/ratification.
func RenderBaseline(m stpa.Model) string {
	var b strings.Builder
	b.WriteString("# STAMP baseline (proposed — ratify before it anchors)\n")
	if len(m.Losses) > 0 {
		b.WriteString("\n## Losses (what the system must avoid)\n")
		for _, l := range m.Losses {
			fmt.Fprintf(&b, "- %s: %s\n", l.ID, l.Description)
		}
	}
	if len(m.Structure.Controllers) > 0 {
		fmt.Fprintf(&b, "\n## Control structure\ncontrollers: %s\n", strings.Join(m.Structure.Controllers, ", "))
		for _, a := range m.Structure.Actions {
			fmt.Fprintf(&b, "- %s: %s — %s", a.ID, a.Controller, a.Action)
			if a.Feedback.Signal != "" {
				fmt.Fprintf(&b, "  (feedback: %s→%s: %s)", a.Feedback.From, a.Feedback.To, a.Feedback.Signal)
			}
			b.WriteString("\n")
		}
	}
	if len(m.UCAs) > 0 {
		b.WriteString("\n## Unsafe control actions (review + ratify each: controlled / accepted-gap)\n")
		for _, u := range m.UCAs {
			fmt.Fprintf(&b, "- %s [%s on %s]: %s", u.ID, u.Type, u.ControlAction, u.Hazard)
			if len(u.Verify) > 0 {
				fmt.Fprintf(&b, "  (control present iff code has: %s)", strings.Join(u.Verify, ", "))
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

const stampBaselinePrompt = `You are an STPA (System-Theoretic Process Analysis) analyst. You receive a codebase's deterministic map and its functional domains. Propose a STAMP control-structure BASELINE for the system AS IT EXISTS today.

Model the system as control loops:
- LOSSES: the unacceptable outcomes the system exists to avoid (e.g. data loss, wrong results, unavailability, resource exhaustion).
- CONTROLLERS + CONTROL ACTIONS: the components that command/constrain behavior, and the actions they issue, each with its FEEDBACK path (what tells the controller it worked). Ground controllers in the real domains/packages.
- UCAs (unsafe control actions): for each control action, how it becomes unsafe — type (not_provided | provided_incorrectly | wrong_timing | wrong_duration), the hazard, the losses it leads to, and VERIFY: concrete source tokens whose presence proves the control IS implemented (e.g. a guard symbol, a timeout call, a validation function) — these let a proof check the control later.

This is a PROPOSAL the developer ratifies; do not assign dispositions. Be faithful to THIS system, not a generic template. Call report_stamp_baseline.`
