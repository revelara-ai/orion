package conductor

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/revelara-ai/orion/internal/contextstore"
)

// scopeCreepReason is the shared prefix of every dimension-3 escalation's
// reason — the waiver lookup key (EscalationRepo.ResolvedByReason).
const scopeCreepReason = "scope creep (built not in spec)"

// scopeCreepDetailMarker separates the human-readable drift line from the
// canonical untraced list in an escalation's detail. Coverage (the waiver
// check) reads only the lines after the marker — never the prose.
const scopeCreepDetailMarker = "UNTRACED:"

// scopeCreepEscalation renders the dimension-3 escalation row (or-g2qf.1).
// The reason carries an order-independent fingerprint of the untraced set so
// a CHANGED surface files a fresh, actionable inbox row instead of deduping
// into a stale resolved one (CreateDetailed dedups on reason regardless of
// resolved state). The detail carries the drift line plus each untraced entry
// as a canonical line — the waiver-coverage source of truth.
func scopeCreepEscalation(driftLine string, untraced []string) (reason, detail string) {
	sorted := append([]string(nil), untraced...)
	sort.Strings(sorted)
	sum := sha256.Sum256([]byte(strings.Join(sorted, "\n")))
	reason = fmt.Sprintf("%s [%s]", scopeCreepReason, hex.EncodeToString(sum[:])[:8])
	detail = driftLine + "\n\n" + scopeCreepDetailMarker + "\n" + strings.Join(sorted, "\n")
	return reason, detail
}

// scopeCreepCovered reports whether every current untraced entry appears as a
// canonical line in some RESOLVED scope-creep escalation's detail — the
// developer waiver. Any entry outside the union (new surface) re-blocks.
func scopeCreepCovered(resolved []contextstore.Escalation, untraced []string) bool {
	waived := map[string]bool{}
	for _, e := range resolved {
		if !e.Resolved {
			continue
		}
		_, list, found := strings.Cut(e.Detail, scopeCreepDetailMarker+"\n")
		if !found {
			continue
		}
		for _, line := range strings.Split(list, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				waived[line] = true
			}
		}
	}
	for _, entry := range untraced {
		if !waived[entry] {
			return false
		}
	}
	return true
}
