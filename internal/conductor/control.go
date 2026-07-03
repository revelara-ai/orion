package conductor

import (
	"context"
	"fmt"
	"strings"

	"github.com/revelara-ai/orion/internal/llm"
)

// Control handles an out-of-turn session control op from the TUI (/compact, /model). It
// implements acp.ControlFunc.
func (a *OrionAgent) Control(ctx context.Context, sessionID, op, arg string) (string, error) {
	switch op {
	case "compact":
		return a.compact(ctx, sessionID)
	case "model":
		return a.switchModel(arg)
	default:
		return "", fmt.Errorf("unknown control op %q", op)
	}
}

// compact replaces the session's conversation history with a single model-written summary
// that preserves decisions/context — so the turn cost stops growing with the transcript.
func (a *OrionAgent) compact(ctx context.Context, sessionID string) (string, error) {
	a.mu.Lock()
	msgs := append([]llm.Message(nil), a.sessions[sessionID]...)
	prov := a.provider
	a.mu.Unlock()

	if len(msgs) == 0 {
		return "Nothing to compact — the conversation is already empty.", nil
	}
	if prov == nil {
		return "Compaction needs a model provider (offline mode has none).", nil
	}

	convo := append(msgs, llm.TextMessage(llm.RoleUser,
		"Summarize our conversation so far into a concise brief that preserves EVERY decision, code fact, file path, ratified spec detail, and open question — so we can continue with far less context. Output only the summary."))
	resp, err := prov.Chat(ctx, llm.ChatRequest{
		System:   "You compress a long conversation into a faithful brief without losing decisions, code facts, or open threads.",
		Messages: convo,
	})
	if err != nil {
		return "", fmt.Errorf("compact: %w", err)
	}
	summary := strings.TrimSpace(resp.Text())
	if summary == "" {
		return "Compaction produced no summary; leaving history unchanged.", nil
	}

	a.mu.Lock()
	a.sessions[sessionID] = []llm.Message{llm.TextMessage(llm.RoleUser, "[Summary of the earlier conversation]\n"+summary)}
	a.mu.Unlock()
	return fmt.Sprintf("Compacted %d messages into a summary — context reset to the essentials.", len(msgs)), nil
}

// switchModel shows the current model (empty arg) or rebuilds the provider for a new one.
func (a *OrionAgent) switchModel(arg string) (string, error) {
	arg = strings.TrimSpace(arg)
	a.mu.Lock()
	defer a.mu.Unlock()
	if arg == "" {
		if a.model == "" {
			return "Current model: (unknown). Pass a model id to switch, e.g. /model claude-sonnet-4-6.", nil
		}
		return "Current model: " + a.model + ". Switch with /model <id> (e.g. claude-sonnet-4-6).", nil
	}
	if a.rebuild == nil {
		return "This brain can't switch models at runtime.", nil
	}
	a.provider = a.rebuild(arg)
	a.model = arg
	// The result carries a MODEL: sentinel the TUI parses to update its brain label.
	return "MODEL:" + arg + " · switched to " + arg, nil
}
