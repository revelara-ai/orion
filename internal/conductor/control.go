package conductor

import (
	"context"
	"fmt"
	"strings"
	"time"
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

// compact replaces the session's conversation history with a self-safe,
// model-written summary that preserves decisions/context — so the turn cost stops
// growing with the transcript. The heavy lifting (chunked/folded summarization
// that never itself exceeds the window, plus the transcript-to-disk record) lives
// in compactSession; this wrapper just formats the developer-facing result.
func (a *OrionAgent) compact(ctx context.Context, sessionID string) (string, error) {
	a.mu.Lock()
	n := len(a.sessions[sessionID])
	prov := a.provider
	a.mu.Unlock()

	if n == 0 {
		return "Nothing to compact — the conversation is already empty.", nil
	}
	if prov == nil {
		return "Compaction needs a model provider (offline mode has none).", nil
	}

	count, _, err := a.compactSession(ctx, sessionID)
	if err != nil {
		return "", fmt.Errorf("compact: %w", err)
	}
	if count == 0 {
		return "Compaction produced no summary; leaving history unchanged.", nil
	}
	return fmt.Sprintf("Compacted %d messages into a summary — context reset to the essentials.", count), nil
}

// switchModel shows the current model + available models (empty arg) or
// rebuilds the provider for a new one. arg is a bare model id (stays on the
// current provider) or a provider/model ref (switches providers).
func (a *OrionAgent) switchModel(arg string) (string, error) {
	arg = strings.TrimSpace(arg)
	a.mu.Lock()
	defer a.mu.Unlock()
	if arg == "" {
		cur := a.model
		if cur == "" {
			cur = "(unknown)"
		}
		msg := "Current model: " + cur + ". Switch with /model <id> or /model <provider>/<id>."
		if a.list != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if models := a.list(ctx); len(models) > 0 {
				const maxShown = 30
				shown := models
				if len(shown) > maxShown {
					shown = shown[:maxShown]
				}
				msg += "\nAvailable: " + strings.Join(shown, ", ")
				if len(models) > maxShown {
					msg += fmt.Sprintf(" … (+%d more)", len(models)-maxShown)
				}
			}
		}
		return msg, nil
	}
	if a.rebuild == nil {
		return "This brain can't switch models at runtime.", nil
	}
	p, ref, err := a.rebuild(a.model, arg)
	if err != nil {
		return "Couldn't switch to " + arg + ": " + err.Error(), nil
	}
	a.provider, a.model = p, ref
	// A model switch replaces the dependency the breaker accumulated evidence
	// against — stale evidence must not refuse turns on the NEW provider.
	a.breaker.Reset()
	// The result carries a MODEL: sentinel the TUI parses to update its brain label.
	return "MODEL:" + ref + " · switched to " + ref, nil
}
