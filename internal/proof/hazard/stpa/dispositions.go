package stpa

import (
	"fmt"
	"sort"
	"strings"
)

// OpenUCAs returns UCAs that are neither controlled nor an accepted gap — the
// undecided set that blocks moving forward (the developer must dispose of each).
func (m Model) OpenUCAs() []UCA {
	var open []UCA
	for _, u := range m.UCAs {
		if u.Disposition != DispositionControlled && u.Disposition != DispositionAcceptedGap {
			open = append(open, u)
		}
	}
	return open
}

// AcceptedGaps returns UCAs the developer explicitly accepted as documented gaps.
func (m Model) AcceptedGaps() []UCA {
	var gaps []UCA
	for _, u := range m.UCAs {
		if u.Disposition == DispositionAcceptedGap {
			gaps = append(gaps, u)
		}
	}
	return gaps
}

// Decide sets a UCA's disposition + rationale by id. Returns an error for an
// unknown id, or for an accepted gap with no rationale (gaps must be documented).
func (m *Model) Decide(ucaID string, d Disposition, rationale, by string) error {
	if d == DispositionAcceptedGap && strings.TrimSpace(rationale) == "" {
		return fmt.Errorf("stpa: accepted gap %s requires a documented rationale", ucaID)
	}
	for i := range m.UCAs {
		if m.UCAs[i].ID == ucaID {
			m.UCAs[i].Disposition = d
			m.UCAs[i].Rationale = rationale
			m.UCAs[i].DecidedBy = by
			return nil
		}
	}
	return fmt.Errorf("stpa: unknown UCA %q", ucaID)
}

// DecisionRecord renders the disposition decisions as Markdown — the
// development-loop documentation a later (or brownfield) developer reads to find
// and close the accepted gaps.
func (m Model) DecisionRecord() string {
	ucas := append([]UCA(nil), m.UCAs...)
	sort.Slice(ucas, func(i, j int) bool { return ucas[i].ID < ucas[j].ID })
	var b strings.Builder
	b.WriteString("# STPA Hazard Decision Record\n\n")
	b.WriteString("| UCA | Control action | Type | Hazard | Disposition | Rationale | By |\n")
	b.WriteString("|---|---|---|---|---|---|---|\n")
	for _, u := range ucas {
		b.WriteString(fmt.Sprintf("| %s | %s | %s | %s | **%s** | %s | %s |\n",
			u.ID, u.ControlAction, u.Type, u.Hazard, u.Disposition, u.Rationale, u.DecidedBy))
	}
	open := m.OpenUCAs()
	gaps := m.AcceptedGaps()
	b.WriteString(fmt.Sprintf("\n- controlled or accepted: %d/%d\n- **accepted gaps (to revisit): %d**\n- **open (blocking): %d**\n",
		len(m.UCAs)-len(open), len(m.UCAs), len(gaps), len(open)))
	return b.String()
}
