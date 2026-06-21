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
	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
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
		Name:        "add_requirement",
		Description: "Record a behavioral requirement the developer stated, as STRUCTURED CASES (request → expected response). Use this for ANY conditional or multi-case behavior — query parameters, error responses, status codes, alternate inputs — that record_answer cannot hold (it is only for a single scalar value). Each case becomes a proof obligation, so the build is held to it.",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"text":{"type":"string","description":"the behavior in one sentence"},
				"decision_key":{"type":"string","description":"the related decision key if any (e.g. timezone)"},
				"cases":{"type":"array","minItems":1,"items":{
					"type":"object",
					"properties":{
						"request":{"type":"object","properties":{"method":{"type":"string"},"path":{"type":"string"},"query":{"type":"object","additionalProperties":{"type":"string"}},"body":{"type":"string"}},"required":["method","path"]},
						"expect":{"type":"object","properties":{
							"status":{"type":"integer"},
							"content_type":{"type":"string","enum":["application/json","text/plain"]},
							"assertions":{"type":"array","items":{"type":"object","properties":{
								"kind":{"type":"string","enum":["json_key_present","json_key_rfc3339","json_key_in_tz","json_error_present","body_rfc3339"]},
								"key":{"type":"string"},"value":{"type":"string","description":"e.g. an IANA timezone for json_key_in_tz"}},"required":["kind"]}}
						},"required":["status","content_type"]}
					},"required":["request","expect"]}}
			},"required":["text","cases"]}`),
		Safety: tools.Safety{Destructive: true},
		Run: func(ctx context.Context, in json.RawMessage) (string, error) {
			var p struct {
				Text        string                `json:"text"`
				DecisionKey string                `json:"decision_key"`
				Cases       []spec.BehavioralCase `json:"cases"`
			}
			if err := json.Unmarshal(in, &p); err != nil {
				return "", err
			}
			req := spec.Requirement{Source: completeness.DimFunctional, DecisionKey: p.DecisionKey, Text: p.Text, Cases: p.Cases}
			if err := c.AddRequirement(ctx, req); err != nil {
				return "", err
			}
			return fmt.Sprintf("recorded requirement %q (%d case(s)) — it will be proven", p.Text, len(p.Cases)), nil
		},
	})

	r.Register(tools.Tool{
		Name:        "list_requirements",
		Description: "List the structured behavioral requirements recorded so far, to review with the developer before ratifying.",
		Safety:      tools.Safety{ReadOnly: true},
		Run: func(ctx context.Context, _ json.RawMessage) (string, error) {
			reqs, err := c.Requirements(ctx)
			if err != nil {
				return "", err
			}
			return asJSON(reqs), nil
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
			var phases []PhaseEvent
			// With a model provider, generate ARBITRARY code to the spec (general)
			// and audit alignment to intent; without one (offline/CI) fall back to
			// the deterministic fixture and skip alignment.
			var gen Generator
			var aligner Aligner
			if provider != nil {
				gen = NativeGenerator(provider)
				aligner = NativeAligner(provider)
			}
			res, err := BuildAndProve(ctx, st, gen, aligner, func(e PhaseEvent) { phases = append(phases, e) }, OutputRoot())
			if err != nil {
				return "", err
			}
			out := "Build pipeline:\n" + RenderPhaseReport(phases)
			out += fmt.Sprintf("\n\nVerdict %s · attempts %d · task closed=%v · tier %s · delivery %s.", res.Verdict, res.Attempts, res.Closed, res.Tier, res.Delivery)
			if res.OutputDir != "" {
				out += "\nCode written to: " + res.OutputDir + " (proven; visible in your working repo)"
			}
			if res.Reason != "" {
				out += "\nEscalation: " + res.Reason
			}
			if res.Alignment.Ran && !res.Alignment.Aligned {
				out += fmt.Sprintf("\nAlignment (advisory, %s): %s", res.Alignment.Severity, res.Alignment.Concern)
			}
			// On a non-Accept verdict, surface the causal analysis so the developer sees
			// WHY it rejected (and what the refinement loop already tried to fix).
			if res.FailureAnalysis != "" {
				out += fmt.Sprintf("\n\nCausal analysis (after %d refinement attempt(s)):\n%s", res.Attempts, res.FailureAnalysis)
			}
			return out, nil
		},
	})

	r.Register(tools.Tool{
		Name:        "show_code",
		Description: "Report WHERE the proven code for the current spec lives in the developer's working repo and return its contents. Use this whenever the developer asks where the code is, to see it, or to answer questions about what was produced. Read-only.",
		Safety:      tools.Safety{ReadOnly: true},
		Run: func(ctx context.Context, _ json.RawMessage) (string, error) {
			es, err := c.RecallSpec(ctx)
			if err != nil {
				return "There is no accepted, proven spec yet — ratify a spec and build it (build_service); on Accept the code is written into your working repo.", nil
			}
			dir, files, lerr := locateProvenCode(es)
			if lerr != nil || len(files) == 0 {
				return fmt.Sprintf("No proven code on disk yet. When the ratified spec builds and proves Accept, the code is written to %s.", dir), nil
			}
			var b strings.Builder
			fmt.Fprintf(&b, "Proven code location: %s\n(%d files: %s)\n", dir, len(files), strings.Join(files, ", "))
			const perFileCap, totalCap = 6000, 24000
			for _, f := range files {
				if b.Len() > totalCap {
					b.WriteString("\n… (remaining files omitted; open the directory above to see them all)\n")
					break
				}
				data, rerr := os.ReadFile(filepath.Join(dir, f))
				if rerr != nil {
					continue
				}
				body := string(data)
				if len(body) > perFileCap {
					body = body[:perFileCap] + "\n… (truncated)"
				}
				fmt.Fprintf(&b, "\n===== %s =====\n%s\n", f, body)
			}
			return b.String(), nil
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
