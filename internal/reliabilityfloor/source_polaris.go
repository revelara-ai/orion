package reliabilityfloor

import (
	"context"
	"encoding/json"

	"github.com/revelara-ai/orion/internal/polaris"
)

// PolarisSource fetches signals from the Revelara corpus via the polaris Consumer.
type PolarisSource struct {
	Consumer *polaris.Consumer
}

var _ SignalSource = (*PolarisSource)(nil)

func (p *PolarisSource) Fetch(ctx context.Context, projectID, query string) ([]Signal, error) {
	if p == nil || p.Consumer == nil {
		return nil, nil // fail open
	}
	rc, err := p.Consumer.Load(ctx, projectID, query)
	if err != nil {
		return nil, nil // fail open
	}
	return parseSignals(rc), nil
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

func parseBucket(raw json.RawMessage, source string, out *[]Signal) {
	if len(raw) == 0 {
		return
	}
	var items []map[string]any
	if err := json.Unmarshal(raw, &items); err != nil {
		// The live MCP tools wrap results in an envelope: {"results":[...],"total":N}
		// (observed 2026-07-12, or-uvw.9 dogfood). Fall back to it before giving up.
		var envelope struct {
			Results []map[string]any `json:"results"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return // best-effort; unknown shape contributes nothing
		}
		items = envelope.Results
	}
	for _, it := range items {
		id := firstString(it, "id", "short_name")
		// Knowledge "fact" items carry only a statement — no title (or-uvw.9 dogfood).
		title := firstString(it, "title", "name", "statement")
		if id == "" || title == "" {
			continue
		}
		*out = append(*out, Signal{
			ID:       id,
			Title:    title,
			Why:      firstString(it, "summary", "description", "why", "statement"),
			Severity: ParseSeverity(firstString(it, "severity")),
			Source:   source,
		})
	}
}

// parseSignals defensively extracts signals from a ReliabilityContext. Never panics.
func parseSignals(rc polaris.ReliabilityContext) []Signal {
	var out []Signal
	parseBucket(rc.Controls, "control", &out)
	parseBucket(rc.Risks, "risk", &out)
	parseBucket(rc.Knowledge, "knowledge", &out)
	return out
}
