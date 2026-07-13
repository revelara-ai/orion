package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Programmatic Tool Calling (or-ykz.14, Hermes PTC): the model emits ONE
// declarative script of deterministic tool steps instead of a turn per step.
// The executor dispatches each step through the SAME registry (every tool's
// own gates still apply), buffers intermediate outputs internally, and returns
// only the FINAL output plus a compact digest — intermediates never enter the
// model's context window (Resource & Cost Governance: large DET chains stop
// burning turns and tokens).
//
// Hard safety rules, enforced BEFORE anything runs:
//   - a step naming a RequiresApproval tool is refused — a chain must never
//     launder the per-call human approval those tools demand;
//   - recursion (a chain step naming run_tool_chain) is refused;
//   - unknown tools and dangling ${refs} are refused by name;
//   - at most maxChainSteps steps.

// maxChainSteps bounds a chain plan — anything larger is a runaway, not a plan.
const maxChainSteps = 16

// ChainToolName is the registered name of the PTC tool.
const ChainToolName = "run_tool_chain"

// ChainStep is one deterministic step: run Tool with Input; if SaveAs is set,
// later steps may splice this step's output via "${name}" inside their Input's
// string values.
type ChainStep struct {
	Tool   string          `json:"tool"`
	Input  json.RawMessage `json:"input"`
	SaveAs string          `json:"save_as,omitempty"`
}

var chainRefRe = regexp.MustCompile(`\$\{([A-Za-z0-9_]+)\}`)

// RegisterChain adds the PTC tool to a registry. Call it AFTER the chained
// tools are registered (validation resolves names against the live registry).
func RegisterChain(r *Registry) {
	r.Register(Tool{
		Name: ChainToolName,
		Description: "Run a multi-step chain of DETERMINISTIC tools in one call. Steps run in order; " +
			"a step with save_as:\"name\" exposes its output to later steps as ${name} inside input string values. " +
			"Only the final step's output returns; intermediates stay out of your context (use for long mechanical sequences). " +
			"Tools that require per-call approval cannot be chained.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"steps":{"type":"array","items":{"type":"object","properties":{"tool":{"type":"string"},"input":{"type":"object"},"save_as":{"type":"string"}},"required":["tool","input"]}}},"required":["steps"]}`),
		// Destructive because chained internal tools may mutate Orion state; never
		// RequiresApproval (approval-requiring steps are refused outright instead).
		Safety: Safety{Destructive: true},
		Run: func(ctx context.Context, input json.RawMessage) (string, error) {
			var plan struct {
				Steps []ChainStep `json:"steps"`
			}
			if err := json.Unmarshal(input, &plan); err != nil {
				return "", fmt.Errorf("chain: bad plan: %w", err)
			}
			if len(plan.Steps) == 0 {
				return "", fmt.Errorf("chain: no steps")
			}
			if len(plan.Steps) > maxChainSteps {
				return "", fmt.Errorf("chain: %d steps exceeds the %d-step ceiling — split the work", len(plan.Steps), maxChainSteps)
			}
			// Validate the WHOLE plan before running anything: no partial chains
			// on plan errors.
			saved := map[string]bool{}
			for i, s := range plan.Steps {
				if s.Tool == ChainToolName {
					return "", fmt.Errorf("chain: step %d is recursive (%s cannot chain itself)", i+1, ChainToolName)
				}
				t, ok := r.Get(s.Tool)
				if !ok {
					return "", fmt.Errorf("chain: step %d names unknown tool %q", i+1, s.Tool)
				}
				if t.Safety.RequiresApproval {
					return "", fmt.Errorf("chain: step %d tool %q requires per-call approval and cannot be chained — call it directly", i+1, s.Tool)
				}
				for _, m := range chainRefRe.FindAllStringSubmatch(string(s.Input), -1) {
					if !saved[m[1]] {
						return "", fmt.Errorf("chain: step %d references ${%s} before any step saved it", i+1, m[1])
					}
				}
				if s.SaveAs != "" {
					saved[s.SaveAs] = true
				}
			}

			outputs := map[string]string{}
			var digest strings.Builder
			var finalOut string
			for i, s := range plan.Steps {
				in := spliceRefs(s.Input, outputs)
				out, isErr := r.Dispatch(ctx, s.Tool, in)
				if isErr {
					fmt.Fprintf(&digest, "%d. %s: FAILED — %s\n", i+1, s.Tool, out)
					for _, rest := range plan.Steps[i+1:] {
						fmt.Fprintf(&digest, "-. %s: not run\n", rest.Tool)
					}
					// Dispatch surfaces only the error string on failure — the digest
					// (what ran, what failed, what never ran) must ride inside it.
					return "", fmt.Errorf("chain stopped at step %d/%d (%s):\n%s", i+1, len(plan.Steps), s.Tool, digest.String())
				}
				fmt.Fprintf(&digest, "%d. %s: ok (%dB)\n", i+1, s.Tool, len(out))
				if s.SaveAs != "" {
					outputs[s.SaveAs] = out
				}
				finalOut = out
			}
			return fmt.Sprintf("chain completed (%d steps):\n%s\nfinal output:\n%s", len(plan.Steps), digest.String(), finalOut), nil
		},
	})
}

// spliceRefs replaces ${name} inside the raw JSON input with the saved output,
// JSON-escaped so the splice can never break the document structure.
func spliceRefs(input json.RawMessage, outputs map[string]string) json.RawMessage {
	if len(outputs) == 0 {
		return input
	}
	out := chainRefRe.ReplaceAllStringFunc(string(input), func(ref string) string {
		name := chainRefRe.FindStringSubmatch(ref)[1]
		v, ok := outputs[name]
		if !ok {
			return ref // pre-validated; unreachable, but never corrupt the input
		}
		b, _ := json.Marshal(v)
		return strings.Trim(string(b), `"`) // splice as escaped string CONTENT
	})
	return json.RawMessage(out)
}
