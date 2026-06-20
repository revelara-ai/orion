package conductor

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/internal/tools"
)

// specTools exposes the deterministic spec pipeline as TOOLS the native Orion
// agent calls (the gates-as-tools inversion). The model reasons + grills; these
// tools are the only way it touches the store, and the completeness/compile/
// accept gates stay the deterministic truth source — the agent proposes, the
// gates verify (no agent grades its own homework).
func specTools(c *orchestrator.Conductor) *tools.Registry {
	r := tools.NewRegistry()

	r.Register(tools.Tool{
		Name:        "submit_intent",
		Description: "Submit the developer's build intent (call once, first). Returns the open spec decisions to resolve.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"intent":{"type":"string","description":"the developer's stated goal"}},"required":["intent"]}`),
		Safety:      tools.Safety{Destructive: true},
		Run: func(ctx context.Context, in json.RawMessage) (string, error) {
			var p struct {
				Intent string `json:"intent"`
			}
			_ = json.Unmarshal(in, &p)
			conf, err := c.Submit(ctx, p.Intent)
			if err != nil {
				return "", err
			}
			return asJSON(map[string]any{"message": conf.Message, "open_decisions": conf.OpenDecisions}), nil
		},
	})

	r.Register(tools.Tool{
		Name:        "check_completeness",
		Description: "List the spec decisions still open. Those with no fallback are BLOCKING — they must be answered before ratifying.",
		Safety:      tools.Safety{ReadOnly: true},
		Run: func(ctx context.Context, _ json.RawMessage) (string, error) {
			sv, err := c.SpecView(ctx)
			if err != nil {
				return "", err
			}
			return asJSON(map[string]any{"status": sv.Status, "open_decisions": sv.OpenDecisions}), nil
		},
	})

	r.Register(tools.Tool{
		Name:        "record_answer",
		Description: "Record the developer's answer to a spec decision (key from check_completeness + the value).",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"key":{"type":"string"},"value":{"type":"string"}},"required":["key","value"]}`),
		Safety:      tools.Safety{Destructive: true},
		Run: func(ctx context.Context, in json.RawMessage) (string, error) {
			var p struct{ Key, Value string }
			if err := json.Unmarshal(in, &p); err != nil {
				return "", err
			}
			if !c.DecisionKeys()[p.Key] {
				return "", fmt.Errorf("%q is not a spec decision key", p.Key)
			}
			if err := c.RecordAnswer(ctx, p.Key, p.Value); err != nil {
				return "", err
			}
			return "recorded " + p.Key + "=" + p.Value, nil
		},
	})

	r.Register(tools.Tool{
		Name:        "preview_spec",
		Description: "Assemble the spec WITHOUT accepting it (fallbacks resolved) and return it to review with the developer.",
		Safety:      tools.Safety{ReadOnly: true},
		Run: func(ctx context.Context, _ json.RawMessage) (string, error) {
			es, err := c.PreviewSpec(ctx)
			if err != nil {
				return "", err
			}
			return asJSON(es), nil
		},
	})

	r.Register(tools.Tool{
		Name:        "ratify_spec",
		Description: "Accept + anchor the spec. Call ONLY after the developer has reviewed it and confirmed it is correct.",
		Safety:      tools.Safety{Destructive: true},
		Run: func(ctx context.Context, _ json.RawMessage) (string, error) {
			es, err := c.ApproveSpec(ctx)
			if err != nil {
				return "", err
			}
			return "ratified spec (hash " + shortHash(es.Hash) + ")", nil
		},
	})

	return r
}

func asJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}
