package conductor

import (
	"testing"

	"github.com/revelara-ai/orion/internal/acp"
	"github.com/revelara-ai/orion/internal/orchestrator"
)

// TestConsentToolsRequireApproval (or-7xw1): every tool that RECORDS the
// developer's consent (ratifications, assumption approval, reduced-proof
// acknowledgment) must carry RequiresApproval — the human gate is an approval
// card, never the model's own claim that "the developer confirmed". Found in
// dogfood: the conductor self-ratified a spec seconds after telling the
// developer no spec was needed.
func TestConsentToolsRequireApproval(t *testing.T) {
	c := orchestrator.New()
	r := specTools(c, nil, &changeSession{}, func(acp.Update) {})
	consent := []string{
		"ratify_spec", "approve_assumptions", "ratify_goals", "ratify_losses",
		"ratify_stamp_baseline", "ratify_split", "acknowledge_reduced_proof",
		"ratify_cases",
	}
	for _, name := range consent {
		tool, ok := r.Get(name)
		if !ok {
			t.Errorf("consent tool %q not registered", name)
			continue
		}
		if !tool.Safety.RequiresApproval {
			t.Errorf("%q must carry RequiresApproval — consent is recorded through the human gate, not model say-so", name)
		}
	}
}
