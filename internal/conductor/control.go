package conductor

import (
	"context"
	"fmt"
	"github.com/revelara-ai/orion/pkg/llm"
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
	// Tree-structured sessions (or-ykz.5): branch/clone/navigate without ever
	// touching the source branch.
	case "fork":
		return a.fork(sessionID, arg)
	case "clone":
		return a.cloneSession(sessionID)
	case "tree":
		return a.treeView(sessionID)
	case "switch":
		return a.switchSession(sessionID, arg)
	// Resume a prior session from its on-disk log (or-8my7): survives a process
	// restart, unlike the in-memory fork/clone/switch family above.
	case "sessions":
		return a.sessionsView()
	case "resume":
		return a.resumeSession(sessionID, arg)
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
	// or-2l7: the changeSession survives compaction out-of-band, but the
	// model's in-context memory of it does not — re-inject the in-flight
	// change digest so the flow resumes instead of restarting.
	a.mu.Lock()
	cs := a.changes[sessionID]
	a.mu.Unlock()
	if cs != nil {
		if d := cs.pendingDigest(); d != "" {
			a.mu.Lock()
			a.sessions[sessionID] = append(a.sessions[sessionID], llm.TextMessage(llm.RoleUser, d))
			a.mu.Unlock()
			return fmt.Sprintf("Compacted %d messages into a summary — context reset to the essentials.\n%s", count, d), nil
		}
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
	if a.conductor != nil {
		a.conductor.SetProducerProvenance(ref, "") // or-gb1.8: /model swaps re-stamp provenance
	}
	// A model switch replaces the dependency the breaker accumulated evidence
	// against — stale evidence must not refuse turns on the NEW provider.
	a.breaker.Reset()
	// or-1aw3(1): re-probe tool capability on the NEW model — a switch to a
	// tool-incapable model otherwise fails mysteriously on the next turn.
	// Advertised-capable models (Anthropic/Gemini listings) skip the live
	// probe; a transport error stays silent (unreachable ≠ incapable).
	warn := ""
	pctx, pcancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer pcancel()
	bare := ref
	if i := strings.IndexByte(bare, '/'); i >= 0 {
		bare = bare[i+1:]
	}
	if p != nil && !llm.AdvertisesTools(pctx, p, bare) {
		if capable, perr := llm.Probe(pctx, p); perr == nil && !capable {
			warn = "\nWARNING: " + ref + " did not demonstrate native tool calling — the conductor's tools may not work on this model; consider switching back."
		}
	}
	// The result carries a MODEL: sentinel the TUI parses to update its brain label.
	return "MODEL:" + ref + " · switched to " + ref + warn, nil
}
