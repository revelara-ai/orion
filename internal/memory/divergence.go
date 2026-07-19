package memory

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// Divergence names one memory item whose stored decision contradicts the
// Context Store's current value (or-ha0z, North-Star predicate): the context
// store is the spec of record; a pinned/decision memory item that disagrees
// with it is stale recall that could steer generation against the ratified
// spec. Detection is the safety net for spec REVISIONS — pins written under an
// earlier spec version don't update themselves.
type Divergence struct {
	ItemID  string // the diverging memory item
	Key     string // the decision key
	Stored  string // what memory remembers
	Current string // what the context store says now
}

// decisionLineRE matches the canonical pinned-decision line format the context
// engine renders ("decision <key> = <value>").
var decisionLineRE = regexp.MustCompile(`(?m)^\s*decision\s+([A-Za-z0-9_.-]+)\s*=\s*(.+?)\s*$`)

// DetectDivergence scans this store's (project-scoped) pinned and
// decision-kind items for canonical decision lines and reports every one whose
// key exists in current with a DIFFERENT value. Items carrying no canonical
// decision lines contribute nothing; keys absent from current (a removed
// decision) are not divergence — only a direct contradiction is.
func (s *Store) DetectDivergence(ctx context.Context, current map[string]string) ([]Divergence, error) {
	if len(current) == 0 {
		return nil, nil
	}
	prScope, prArgs := s.scopeWhere()
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, content FROM memory_items WHERE (pinned=1 OR kind=?)`+prScope, // #nosec G202 -- scopeWhere is a code-built fragment; values bound
		append([]any{KindDecision}, prArgs...)...)
	if err != nil {
		return nil, fmt.Errorf("memory divergence scan: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Divergence
	for rows.Next() {
		var id, content string
		if err := rows.Scan(&id, &content); err != nil {
			return nil, err
		}
		for _, m := range decisionLineRE.FindAllStringSubmatch(content, -1) {
			key, stored := m[1], strings.TrimSpace(m[2])
			if cur, ok := current[key]; ok && cur != stored {
				out = append(out, Divergence{ItemID: id, Key: key, Stored: stored, Current: cur})
			}
		}
	}
	return out, rows.Err()
}
