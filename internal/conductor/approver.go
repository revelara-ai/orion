package conductor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/revelara-ai/orion/internal/acp"
	"github.com/revelara-ai/orion/internal/harness"
	"github.com/revelara-ai/orion/internal/tools"
)

// approver returns the harness Approve hook for a session: before a mutating tool runs it
// consults the session allow-always set, and otherwise prompts the developer through the
// ACP gate (ask), mapping the outcome. "allow_always" records the tool so future calls
// skip the prompt. A nil ask (headless/subagent — no interactive gate) yields a nil hook,
// so those tools keep their own red-button gating and never prompt.
func (a *OrionAgent) approver(sessionID string, reg *tools.Registry, ask acp.AskFunc) func(context.Context, string, json.RawMessage, tools.Safety, string) harness.Decision {
	if ask == nil {
		return nil
	}
	return func(_ context.Context, name string, input json.RawMessage, _ tools.Safety, rationale string) harness.Decision {
		if a.toolAllowed(sessionID, name) {
			return harness.DecisionAllow
		}
		// or-8noc: a tool-provided Preview (which can carry session state —
		// e.g. the oracle a ratification locks) beats the generic input dump.
		preview := toolPreview(name, input)
		if reg != nil {
			if t, ok := reg.Get(name); ok && t.Preview != nil {
				if p := t.Preview(input); p != "" {
					preview = p
				}
			}
		}
		res, err := ask(acp.PermissionRequest{
			Kind:      "tool",
			Tool:      name,
			Title:     "Run " + name + "?",
			Preview:   preview,
			Rationale: approvalRationale(rationale),
		})
		if err != nil {
			return harness.DecisionDeny // a gate error is a safe default: don't run it
		}
		switch res.Outcome {
		case "allow_always":
			a.allowTool(sessionID, name)
			return harness.DecisionAllow
		case "allow_once", "granted":
			return harness.DecisionAllow
		default:
			return harness.DecisionDeny
		}
	}
}

// approvalRationale is the assistant's stated reason for a tool call, trimmed for
// the approval card — or an honest placeholder when the model gave none, so a
// prompt is never a context-free "Run bash?" (or-10m0).
func approvalRationale(rationale string) string {
	if r := strings.TrimSpace(rationale); r != "" {
		return r
	}
	return "(the model gave no explanation for this call)"
}

// toolAllowed reports whether the developer allow-always'd this tool for the session.
func (a *OrionAgent) toolAllowed(sessionID, name string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.allowed[sessionID][name]
}

// allowTool records an allow-always grant for the session.
func (a *OrionAgent) allowTool(sessionID, name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.allowed[sessionID] == nil {
		a.allowed[sessionID] = map[string]bool{}
	}
	a.allowed[sessionID][name] = true
}

// toolPreview renders a readable, colorizable preview of a pending mutating tool call for
// the approval card: the bash command; or a file path plus a -/+ unified-diff-style
// preview for edit_file/write_file (the TUI colorizes +/- lines green/red).
func toolPreview(name string, input json.RawMessage) string {
	switch name {
	case "bash":
		var p struct {
			Command string `json:"command"`
		}
		_ = json.Unmarshal(input, &p)
		return "$ " + p.Command
	case "edit_file":
		var p struct {
			Path string `json:"path"`
			Old  string `json:"old_string"`
			New  string `json:"new_string"`
		}
		_ = json.Unmarshal(input, &p)
		return p.Path + "\n" + prefixLines(p.Old, "-") + prefixLines(p.New, "+")
	case "write_file":
		var p struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		_ = json.Unmarshal(input, &p)
		return fmt.Sprintf("write %s (%d bytes)\n%s", p.Path, len(p.Content), prefixLines(p.Content, "+"))
	default:
		return string(input)
	}
}

// prefixLines prefixes every non-final line of s with mark, producing +/- diff rows.
func prefixLines(s, mark string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		b.WriteString(mark)
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}
