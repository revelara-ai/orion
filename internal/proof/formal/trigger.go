package formal

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/revelara-ai/orion/internal/reliabilitytier"
)

// TriggerInput is what the fire-vs-skip predicate reads (or-56c.4): the
// project's reliability tier plus the DESIGN's observable shape — the ratified
// texts (intent, requirements, obligations) and the ratified STPA control
// structure's size. All deterministic inputs; no LLM opinion.
type TriggerInput struct {
	Tier           reliabilitytier.Tier
	DesignTexts    []string // intent + requirement texts + proof obligations
	Controllers    int      // ratified STPA control structure
	ControlActions int
}

// TriggerDecision is an auditable fire/skip verdict.
type TriggerDecision struct {
	Fire   bool
	Reason string
}

// shapeRe is the concurrency/ordering/shared-state/protocol vocabulary that
// makes a design MODEL-CHECKABLE-WORTHY. Deterministic and auditable: the
// decision names what matched. Deliberately conservative — a keyword the list
// misses costs one skipped check, which the critical-tier rule and the
// control-structure rule backstop.
var shapeRe = regexp.MustCompile(`(?i)\b(concurren\w*|parallel\w*|race\s+condition|races?\b|mutex|lock(?:ing|s)?\b|semaphore|queue[sd]?\b|worker(?:s|\s+pool)?|ordering|out[- ]of[- ]order|protocol|state\s+machine|shared\s+state|websocket|stream(?:ing|s)?\b|session\s+state|distributed|consensus|leader\s+election|replica\w*|transaction\w*|saga|idempoten\w*|at[- ](?:least|most)[- ]once|exactly[- ]once)\b`)

// controlStructureFireAt: a ratified control structure with this many
// controllers (or more) is coordination by construction.
const controlStructureFireAt = 2

// ShouldCheck decides whether the design-proof gate fires (or-56c.4), honoring
// the manifesto calibration tenet: model-checking a stateless CRUD endpoint is
// waste; a concurrent state machine is not.
//
//	critical tier        → fire (always)
//	throwaway tier       → skip (always — never gold-plate a throwaway)
//	standard + shape     → fire (concurrency/ordering/shared-state/protocol
//	                       vocabulary in the ratified texts, or a multi-
//	                       controller STPA structure)
//	standard, stateless  → skip
func ShouldCheck(in TriggerInput) TriggerDecision {
	switch in.Tier {
	case reliabilitytier.Critical:
		return TriggerDecision{Fire: true, Reason: "critical reliability tier — the design proof always fires"}
	case reliabilitytier.Throwaway:
		return TriggerDecision{Fire: false, Reason: "throwaway tier — calibration skips the design proof regardless of shape"}
	}
	for _, text := range in.DesignTexts {
		if m := shapeRe.FindString(text); m != "" {
			return TriggerDecision{Fire: true, Reason: fmt.Sprintf("design shape %q in %q", strings.ToLower(m), truncate(text, 80))}
		}
	}
	if in.Controllers >= controlStructureFireAt {
		return TriggerDecision{Fire: true, Reason: fmt.Sprintf("control structure complexity: %d controllers / %d control actions — coordination by construction", in.Controllers, in.ControlActions)}
	}
	return TriggerDecision{Fire: false, Reason: "stateless shape at standard tier — the design proof is calibrated off"}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
