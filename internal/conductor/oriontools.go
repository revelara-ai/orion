package conductor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/revelara-ai/orion/internal/llm"
	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/internal/tools"
)

// specTools exposes the deterministic spec pipeline as TOOLS the native Orion
// agent calls (the gates-as-tools inversion). The model reasons + grills; these
// tools are the only way it touches the store, and the completeness/compile/
// accept gates stay the deterministic truth source — the agent proposes, the
// gates verify (no agent grades its own homework).
func specTools(c *orchestrator.Conductor, provider llm.Provider) *tools.Registry {
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
		Description: "Record the developer's answer to a spec decision (key from check_completeness + the value). For response_format, use a canonical value — \"json\" or \"plain text\" (the only formats the build+proof pipeline supports). If a tool returns an \"unrecognized/ambiguous format\" error, re-ask and record one of those.",
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
		Description: "Accept + anchor the spec. Call ONLY after the developer has reviewed it and confirmed it is correct. Returns the ratified spec DOCUMENT (Markdown) to show the developer.",
		Safety:      tools.Safety{Destructive: true},
		Run: func(ctx context.Context, _ json.RawMessage) (string, error) {
			es, err := c.ApproveSpec(ctx)
			if err != nil {
				return "", err
			}
			doc := SpecDocument(es)
			// Persist the document as the durable artifact of the grill.
			if st := c.Store(); st != nil {
				dir := filepath.Join(st.Dir(), "specs")
				if err := os.MkdirAll(dir, 0o755); err == nil {
					_ = os.WriteFile(filepath.Join(dir, "spec-"+shortHash(es.Hash)+".md"), []byte(doc), 0o644)
				}
			}
			return "Ratified (anchor " + shortHash(es.Hash) + "). Spec document:\n\n" + doc, nil
		},
	})

	r.Register(tools.Tool{
		Name:        "build_service",
		Description: "Build the service to the ratified spec and PROVE it in one shot (decompose → generate → behavioral+empirical+hazard proof → reliability tier → deployment bar). Call after ratify_spec. Returns the proof verdict and delivery decision.",
		Safety:      tools.Safety{Destructive: true},
		Run: func(ctx context.Context, _ json.RawMessage) (string, error) {
			st := c.Store()
			if st == nil {
				return "", fmt.Errorf("build requires a persistent store")
			}
			var steps []string
			// With a model provider, generate ARBITRARY code to the spec (general)
			// and audit alignment to intent; without one (offline/CI) fall back to
			// the deterministic fixture and skip alignment.
			var gen Generator
			var aligner Aligner
			if provider != nil {
				gen = NativeGenerator(provider)
				aligner = NativeAligner(provider)
			}
			res, err := BuildAndProve(ctx, st, gen, aligner, func(s string) { steps = append(steps, s) })
			if err != nil {
				return "", err
			}
			out := strings.Join(steps, "\n")
			out += fmt.Sprintf("\n\nBuild pipeline finished — task %s: proof verdict=%s, task closed=%v, reliability tier=%s, delivery=%s.",
				res.TaskID, res.Verdict, res.Closed, res.Tier, res.Delivery)
			if res.Reason != "" {
				out += "\nEscalation: " + res.Reason
			}
			if res.Alignment.Ran && !res.Alignment.Aligned {
				out += fmt.Sprintf("\nAlignment audit (advisory, %s): %s", res.Alignment.Severity, res.Alignment.Concern)
			}
			return out, nil
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
