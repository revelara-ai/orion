package conductor

import (
	"context"
	"fmt"
	"strings"

	"github.com/revelara-ai/orion/internal/proof/formal"
	"github.com/revelara-ai/orion/pkg/llm"
)

// NativeModelSynthesizer adapts the conductor's brain to the design-proof
// synthesis slot (or-56c.2): it drafts a candidate FizzBee model from the
// ratified STPA UCAs + control structure. The draft carries NO proof
// authority — formal.SynthesizeDesignModel validates it (total obligation
// binding, ≥1 invariant) and a human ratifies its exact hash before the
// checker will touch it.
func NativeModelSynthesizer(provider llm.Provider) formal.Synthesizer {
	return func(ctx context.Context, in formal.SynthesisInput) (string, error) {
		resp, err := provider.Chat(ctx, llm.ChatRequest{
			System:    formalDraftSystem,
			Messages:  []llm.Message{llm.TextMessage(llm.RoleUser, renderSynthesisTask(in))},
			MaxTokens: 4000,
		})
		if err != nil {
			return "", err
		}
		return resp.Text(), nil
	}
}

const formalDraftSystem = `You draft FizzBee formal models from STPA hazard analyses. Output ONLY the model source, no prose. Rules:
- Model the ratified control structure's coordination (controllers, control actions, state).
- Every unsafe control action (UCA) becomes an invariant: an ` + "`always assertion <Name>:`" + ` block that fails in any state where the UCA occurs.
- Every invariant MUST carry a binding comment line: ` + "`# obligation: <Name> -> go test <packages> -run <TestPattern>`" + ` naming the behavioral test that enforces it in code.
- Include a terminal-quiescence action so completed runs are not flagged as deadlocks.`

func renderSynthesisTask(in formal.SynthesisInput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Intent: %s\n\nControl structure:\n", in.Intent)
	for _, c := range in.Structure.Controllers {
		fmt.Fprintf(&b, "- controller %s\n", c)
	}
	for _, a := range in.Structure.Actions {
		fmt.Fprintf(&b, "- action %s: %s -> %s\n", a.ID, a.Controller, a.Action)
	}
	b.WriteString("\nUnsafe control actions (each becomes an invariant):\n")
	for _, u := range in.UCAs {
		fmt.Fprintf(&b, "- %s [%s on %s]: %s\n", u.ID, u.Type, u.ControlAction, u.Hazard)
	}
	if len(in.DesignTexts) > 0 {
		b.WriteString("\nDesign requirements:\n")
		for _, t := range in.DesignTexts {
			fmt.Fprintf(&b, "- %s\n", t)
		}
	}
	b.WriteString("\nDraft the FizzBee model now.")
	return b.String()
}
