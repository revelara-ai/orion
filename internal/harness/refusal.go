package harness

import (
	"fmt"

	"github.com/revelara-ai/orion/pkg/llm"
)

// Refusal-aware stop handling (or-mvr.15): every provider adapter normalizes
// policy blocks to llm.StopRefusal, but nothing consumed it — an empty
// refusal fell into the empty-turn guard (an identical re-send to a model
// that just policy-blocked it, then a misdiagnosing "raise max_tokens"
// error), and a refusal WITH text ended the turn as a normal final answer.
// In a weeks-long unattended run, a silent refusal is a silent stall or a
// silent quality hole. Refusals now surface as a NAMED, classified error
// carrying the refusal text verbatim — routed/escalated, never retried
// identically, never misdiagnosed, never a silent pass.

// RefusalError is a policy refusal from the model or provider.
type RefusalError struct {
	Text       string // the model's refusal text ("" for an empty policy block)
	StopDetail string // provider stop classification (e.g. "refusal", a block reason)
}

func (e *RefusalError) Error() string {
	if e.Text == "" {
		return fmt.Sprintf("harness: model refused the request (%s) with no explanation — do not retry the identical prompt; rephrase or escalate", e.StopDetail)
	}
	return fmt.Sprintf("harness: model refused the request (%s): %s", e.StopDetail, e.Text)
}

// EventRefusal is the recorded refusal event kind.
const EventRefusal EventKind = "refusal"

// refusalFromResponse builds the classified error for a StopRefusal turn.
func refusalFromResponse(resp *llm.ChatResponse) *RefusalError {
	return &RefusalError{Text: resp.Text(), StopDetail: string(resp.StopReason)}
}
