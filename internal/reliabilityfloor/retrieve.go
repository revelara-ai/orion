package reliabilityfloor

import (
	"context"
	"sort"
)

// Retrieve fetches signals for the change intent, deduped by ID, highest-severity
// first, capped to maxN, with checks attached. Fails open: any source error yields nil.
func Retrieve(ctx context.Context, src SignalSource, projectID, intent string, maxN int) []Signal {
	if src == nil {
		return nil
	}
	raw, err := src.Fetch(ctx, projectID, intent)
	if err != nil || len(raw) == 0 {
		return nil
	}
	seen := map[string]bool{}
	deduped := raw[:0:0]
	for _, s := range raw {
		if s.ID == "" || seen[s.ID] {
			continue
		}
		seen[s.ID] = true
		deduped = append(deduped, s)
	}
	sort.SliceStable(deduped, func(i, j int) bool { return deduped[i].Severity > deduped[j].Severity })
	if maxN > 0 && len(deduped) > maxN {
		deduped = deduped[:maxN]
	}
	return AttachChecks(deduped)
}
