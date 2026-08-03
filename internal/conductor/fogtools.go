package conductor

import (
	"context"
	"strings"

	"github.com/revelara-ai/orion/internal/orchestrator"
)

// resolveFogPrefix expands an 8-char prefix from list_fog to the full note id
// (same affordance resolve_open_question gives question ids). A full id — or
// an unmatched prefix — passes through untouched for the orchestrator to judge.
func resolveFogPrefix(ctx context.Context, c *orchestrator.Conductor, id string) string {
	if len(id) != 8 {
		return id
	}
	fs, err := c.FogNotes(ctx)
	if err != nil {
		return id
	}
	for _, f := range fs {
		if strings.HasPrefix(f.ID, id) {
			return f.ID
		}
	}
	return id
}

// shortIDs renders ids as their 8-char display prefixes.
func shortIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if len(id) > 8 {
			id = id[:8]
		}
		out = append(out, id)
	}
	return out
}
