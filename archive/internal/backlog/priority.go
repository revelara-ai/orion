package backlog

import (
	"sort"
	"strings"
	"time"

	"github.com/revelara-ai/orion/internal/repos"
)

// Compare returns -1/0/1 comparing two NormalizedIssues per SPEC
// §8.6 priority ordering:
//
//  1. Polaris risk severity=critical first (v1 proxy: priority 0)
//  2. Polaris risk score descending (v1 proxy: priority asc)
//  3. Tracker priority (smallint, 0=critical .. 4=trivial; nulls last)
//  4. created_at ascending (FIFO)
//  5. external_id lexical (tiebreak)
//
// The Polaris-risk fields (severity, score) are not yet joined into
// NormalizedIssue at the repo layer; v1 uses tracker priority as
// the proxy. Once E3 wires the risk join, the first two clauses
// can dispatch to risk-level data without changing this function's
// signature.
func Compare(a, b repos.NormalizedIssue) int {
	// 1/2/3 collapsed: tracker priority asc, nil last.
	ap := priorityValue(a.Priority)
	bp := priorityValue(b.Priority)
	if ap != bp {
		if ap < bp {
			return -1
		}
		return 1
	}

	// 4: created_at ascending (FIFO).
	if !a.CreatedAt.Equal(b.CreatedAt) {
		if a.CreatedAt.Before(b.CreatedAt) {
			return -1
		}
		return 1
	}

	// 5: tiebreak by external_id lexical.
	return strings.Compare(a.ExternalID, b.ExternalID)
}

// priorityValue returns a sortable int from a *int16 priority.
// A nil priority sorts last (treated as +infinity).
func priorityValue(p *int16) int {
	if p == nil {
		return 1 << 30
	}
	return int(*p)
}

// Sort orders the slice in place per Compare.
func Sort(issues []repos.NormalizedIssue) {
	sort.SliceStable(issues, func(i, j int) bool {
		return Compare(issues[i], issues[j]) < 0
	})
}

// epochMin is the v1 sentinel for "no created_at known"; pushes
// undated issues to the front of FIFO ties. Exported for tests.
func epochMin() time.Time { return time.Unix(0, 0).UTC() }

// Ensure unused-import for `time` is not stripped by goimports —
// `epochMin` is exported for tests but documented here.
var _ = epochMin
