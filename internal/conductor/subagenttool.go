package conductor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/revelara-ai/orion/internal/acp"
	"github.com/revelara-ai/orion/internal/harness"
	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/internal/tools"
	"github.com/revelara-ai/orion/pkg/llm"
)

// delegatableTools is the fail-closed ALLOWLIST of the Conductor's own tools a spawned subagent may
// hold. A subagent is a bounded nested worker for a self-contained slice (research, code inspection,
// a scoped edit) — it MUST NOT reach the deterministic spec/build/change pipeline, git, beads, or
// spawn_subagent itself (which would let a subagent spawn its own subagents — a recursion bomb).
// Anything NOT in this set (nor a revelara.ai research tool) is dropped from a subagent's toolset,
// so pipeline/git tools — and any tool added later — stay forbidden by default. The mutating members
// (bash/write_file/edit_file) still self-gate on the red button inside their own Run, so an engaged
// halt stops a subagent's writes exactly as it stops the Conductor's.
var delegatableTools = map[string]bool{
	"read_file": true, "grep": true, "glob": true, // read-only workspace
	"web_fetch": true, "web_search": true, // read-only web
	"bash": true, "write_file": true, "edit_file": true, // mutating workspace (self-gated, opt-in)
}

// isDelegatable reports whether a tool may be handed to a subagent: the static allowlist above, plus
// every revelara.ai MCP research tool (revelara_*), which are registered read-only.
func isDelegatable(name string) bool {
	return delegatableTools[name] || strings.HasPrefix(name, "revelara_")
}

// subagentDefaultTools is the safe READ-ONLY default toolset when the caller names none: workspace
// + web reads, plus whatever revelara.ai research tools are currently registered.
func subagentDefaultTools(r *tools.Registry) []string {
	out := []string{"read_file", "grep", "glob", "web_fetch", "web_search"}
	for _, n := range r.Names() {
		if strings.HasPrefix(n, "revelara_") {
			out = append(out, n)
		}
	}
	return out
}

// registerSubagentTool gives the Conductor the ability to fire up + coordinate a bounded nested
// agent (or-5j1 slice 3): spawn_subagent(task, tools?) runs a fresh harness loop over a SCOPED,
// fail-closed subset of the Conductor's own tools and returns the subagent's final answer. This is
// the "coordinate a swarm when it helps" half of the general-agent vision — the Conductor does basic
// work directly (slices 1–2: workspace + web) and delegates a self-contained slice to a subagent
// when that keeps its main thread clean. The subagent shares the Conductor's token budget, is
// depth-1 (it cannot spawn its own subagents), is iteration- and wall-clock-bounded, runs a
// worker (not grill) system prompt, and inherits the red-button gate on any mutating tool. It is
// built on harness.Loop, NOT agentruntime — agentruntime is the walled, sandboxed GENERATION domain
// (external vendor CLIs under bwrap); a Conductor subagent is trusted in-process orchestration.
func registerSubagentTool(r *tools.Registry, c *orchestrator.Conductor, provider llm.Provider, emit func(acp.Update)) {
	if provider == nil || c == nil {
		return // no model or no conductor → no nested loop (the offline/deterministic conductor has none)
	}
	r.Register(tools.Tool{
		Name: "spawn_subagent",
		Description: "Delegate a self-contained sub-task to a bounded nested agent and get its final result. " +
			"Use when a slice of work is best handled in its own focused loop — e.g. research a question with " +
			"web_search/web_fetch, or map part of the codebase with read_file/grep/glob — so it doesn't clutter " +
			"your main thread. 'task' is the full instruction for the subagent (it has no other context). 'tools' " +
			"optionally names which of your tools it may use; omit for the read-only research default (read_file, " +
			"grep, glob, web_fetch, web_search, plus revelara.ai research). Grantable if requested: bash, write_file, " +
			"edit_file (still halted by the red button). The subagent runs headless, returns its answer, and cannot " +
			"spawn further subagents or touch the spec/build/git pipeline.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"task":{"type":"string","description":"the full, self-contained instruction for the subagent"},"tools":{"type":"array","items":{"type":"string"},"description":"optional subset of tool names to grant; omitted = read-only research default"},"name":{"type":"string","description":"optional display label for this subagent in the activity panel; defaults to first 2 words of task"}},"required":["task"]}`),
		// Meta-tool: not itself red-button gated (a read-only research subagent must run mid-halt,
		// like any other read) — any mutating CHILD tool self-gates on the red button.
		Safety: tools.Safety{},
		Run: func(ctx context.Context, in json.RawMessage) (string, error) {
			var p struct {
				Task  string   `json:"task"`
				Tools []string `json:"tools"`
				Name  string   `json:"name"`
			}
			if err := json.Unmarshal(in, &p); err != nil {
				return "", err
			}
			if strings.TrimSpace(p.Task) == "" {
				return "", fmt.Errorf("spawn_subagent: task is required")
			}

			// Resolve the requested toolset against the fail-closed allowlist. Forbidden names
			// (spawn_subagent, pipeline/git/beads tools) and unknown names are dropped and reported,
			// so the caller sees exactly what the subagent got — but nothing forbidden ever runs.
			want := p.Tools
			if len(want) == 0 {
				want = subagentDefaultTools(r)
			}
			sub := tools.NewRegistry()
			var granted, refused []string
			seen := map[string]bool{}
			for _, name := range want {
				if seen[name] {
					continue
				}
				seen[name] = true
				if !isDelegatable(name) {
					refused = append(refused, name)
					continue
				}
				t, ok := r.Get(name)
				if !ok {
					refused = append(refused, name+"(unavailable)")
					continue
				}
				// The revelara_* prefix is a convenience, not a trust grant: a prefix-matched tool
				// is delegatable only if it is ACTUALLY read-only, so a future non-read-only
				// revelara_* tool can't auto-delegate. The static allowlist members
				// (bash/write_file/edit_file) are the only intentionally-grantable mutators.
				if !delegatableTools[name] && !t.Safety.ReadOnly {
					refused = append(refused, name+"(not-read-only)")
					continue
				}
				sub.Register(t)
				granted = append(granted, name)
			}
			if len(granted) == 0 {
				return "", fmt.Errorf("spawn_subagent: no delegatable tools resolved from %v "+
					"(grantable: read_file, grep, glob, web_fetch, web_search, bash, write_file, edit_file, revelara_*)", want)
			}

			// Bounded nested loop: shares the Conductor's budget (nested tokens count against the
			// same ceiling), capped iterations, and a wall-clock timeout so a runaway subagent
			// can't hang the turn. Depth-1 is structural: spawn_subagent is not delegatable, so it
			// is never in `sub`.
			label := strings.TrimSpace(p.Name)
			if label == "" {
				label = subagentLabel(p.Task)
			}

			cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
			defer cancel()
			loop := harness.Loop{
				Provider:   provider,
				Tools:      sub,
				System:     subagentSystemPrompt(granted),
				Supervisor: harness.Supervisor{MaxIterations: 12, Budget: c.Budget(), CapHint: "the subagent's conversation is discarded; its partial answer (if any) is returned to the caller"},
				Role:       "subagent",
			}
			var trace []string
			convo, resp, runErr := loop.Run(cctx, []llm.Message{llm.TextMessage(llm.RoleUser, p.Task)},
				func(e harness.Event) {
					switch e.Kind {
					case harness.EventToolCall:
						trace = append(trace, e.Tool)
						if emit != nil {
							emit(acp.Activity(label, e.Tool, 1, "running"))
						}
					case harness.EventThought:
						if emit != nil && strings.TrimSpace(e.Text) != "" {
							emit(acp.Activity(label, "thinking", 1, "running"))
						}
					}
				})
			if emit != nil {
				emit(acp.Activity(label, "", 1, "done"))
			}

			answer := ""
			if resp != nil {
				answer = strings.TrimSpace(resp.Text())
			}
			if answer == "" {
				answer = strings.TrimSpace(lastAssistantText(convo))
			}
			// A hard failure with nothing to show is a tool error the Conductor should see as one.
			if runErr != nil && answer == "" {
				return "", fmt.Errorf("spawn_subagent: %w", runErr)
			}

			var b strings.Builder
			resultLabel := "subagent"
			if runErr != nil {
				// Unmistakable in the first token: the answer below is partial, not a clean result.
				resultLabel = "subagent INCOMPLETE"
			}
			fmt.Fprintf(&b, "%s [%s]", resultLabel, strings.Join(granted, ", "))
			if len(refused) > 0 {
				fmt.Fprintf(&b, " (refused: %s)", strings.Join(refused, ", "))
			}
			if len(trace) > 0 {
				fmt.Fprintf(&b, " · used: %s", strings.Join(trace, " → "))
			}
			b.WriteByte('\n')
			if runErr != nil {
				// Why it stopped (max-iterations / budget ceiling / timeout); the result is partial.
				fmt.Fprintf(&b, "⚠ stopped early (%v) — the result below may be incomplete.\n", runErr)
			}
			if answer == "" {
				answer = "(subagent produced no text answer)"
			}
			b.WriteString(answer)
			return boundOutput(b.String()), nil
		},
	})
}

// subagentLabel derives a short display label from a task when no name is given.
func subagentLabel(task string) string {
	fields := strings.Fields(task)
	if len(fields) > 2 {
		fields = fields[:2]
	}
	if len(fields) == 0 {
		return "subagent"
	}
	return strings.ToLower(strings.Join(fields, " "))
}

// subagentSystemPrompt primes a spawned worker: focused, headless, and explicitly walled off from
// the Conductor's orchestration authority. It is deliberately NOT the adversarial-grill RoleTemplate
// — a subagent executes one task, it does not run the spec conversation.
func subagentSystemPrompt(granted []string) string {
	return fmt.Sprintf(`You are a subagent spawned by Orion's Conductor to complete ONE specific, self-contained task.

Available tools: %s.

- Use the tools to accomplish the task, then STOP and return a concise, complete answer.
- You are a worker, not a planner. Do the task directly. There is no human to ask — make reasonable assumptions and state them in your answer rather than asking questions.
- You CANNOT spawn further subagents, author or ratify a spec, run the build/change/proof pipeline, or use git. Those are the Conductor's job. Stay within your task and your granted tools.
- Your final message IS the result handed back to the Conductor, which has no view into your steps — make it self-contained: the finding, where you found it, and any caveats.`,
		strings.Join(granted, ", "))
}

// lastAssistantText returns the text of the last assistant message that carries any — the fallback
// when the terminal response is nil (e.g. the loop stopped on max-iterations, not end_turn).
func lastAssistantText(convo []llm.Message) string {
	for i := len(convo) - 1; i >= 0; i-- {
		if convo[i].Role != llm.RoleAssistant {
			continue
		}
		var s strings.Builder
		for _, blk := range convo[i].Content {
			if blk.Type == llm.BlockText {
				s.WriteString(blk.Text)
			}
		}
		if t := strings.TrimSpace(s.String()); t != "" {
			return t
		}
	}
	return ""
}
