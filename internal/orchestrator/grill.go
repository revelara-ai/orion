package orchestrator

import (
	"context"
	"os"
	"strings"

	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
)

// V3 Step 5 (or-794, the last migration-ladder step): the GRILL drives
// elicitation open-endedly — relentless listening at INTENT altitude — and
// the fixed completeness checklist is DEMOTED to a reliability floor. The
// floor never goes away: every floor dimension must still resolve before the
// spec compiles (spec.Compile's checklist loop is untouched), and unresolved
// floor dimensions still surface as questions — they just no longer lead.
//
// Staged + reversible like every V3 swap: ORION_ELICITATION=grill activates;
// unset, the checklist drives byte-identically to V2. The grill is an LLM
// (assumed adversarial): a grill error or panic FAILS OPEN to the checklist
// driver, and nothing the grill produces can remove a floor question.

// GrillAgent proposes the next open-ended elicitation questions for an
// intent, given what is already resolved. Empty means the grill has nothing
// left to ask (the floor may still have questions).
type GrillAgent func(ctx context.Context, intent string, resolved map[string]string, floor []completeness.OpenDecision) ([]completeness.OpenDecision, error)

// SetGrillAgent injects the open-ended elicitation driver (or-794). Safe to
// leave nil — the checklist drives, exactly as before.
func (c *Conductor) SetGrillAgent(g GrillAgent) { c.grill = g }

func grillDrives() bool { return os.Getenv("ORION_ELICITATION") == "grill" }

// openDecisions is THE elicitation driver seam: it returns the questions the
// developer sees. Checklist mode (default): the deterministic floor alone.
// Grill mode: the grill's open-ended questions lead, followed by every floor
// dimension still unresolved — demoted, never dropped.
func (c *Conductor) openDecisions(ctx context.Context, intent string, answers map[string]string) (out []completeness.OpenDecision) {
	floor := c.gate.Analyze(intent, answers)
	if !grillDrives() || c.grill == nil {
		return floor
	}
	defer func() {
		if r := recover(); r != nil {
			c.log.WarnContext(ctx, "grill elicitation: recovered from panic — checklist drives", "panic", r)
			out = floor
		}
	}()
	qs, err := c.grill(ctx, intent, answers, floor)
	if err != nil {
		c.log.WarnContext(ctx, "grill elicitation failed — checklist drives", "err", err)
		return floor
	}
	seen := map[string]bool{}
	for _, q := range qs {
		key := strings.TrimSpace(q.Key)
		if key == "" || seen[key] {
			continue // an unkeyed or duplicate grill question is unanswerable — drop it
		}
		if strings.TrimSpace(answers[key]) != "" {
			continue // already answered in a prior round
		}
		if q.Dimension == "" {
			q.Dimension = completeness.DimFunctional
		}
		seen[key] = true
		out = append(out, q)
	}
	// The demoted floor: every unresolved checklist dimension still surfaces —
	// after the grill's questions, deduped against them.
	for _, f := range floor {
		if !seen[f.Key] {
			seen[f.Key] = true
			out = append(out, f)
		}
	}
	return out
}
