package conductor

import (
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
)

// or-g2qf.1: the scope-creep escalation carries a surface fingerprint in its
// REASON (so a new surface set files a new, actionable inbox row instead of
// deduping into a stale resolved one) and the canonical untraced list in its
// DETAIL (the waiver-coverage source of truth).
func TestScopeCreepEscalationShape(t *testing.T) {
	reason1, detail1 := scopeCreepEscalation("DRIFT ...; untraced: route /admin, CheckAccess", []string{"route /admin", "CheckAccess"})
	reason2, _ := scopeCreepEscalation("DRIFT ...", []string{"route /admin"})

	if !strings.HasPrefix(reason1, scopeCreepReason) || !strings.HasPrefix(reason2, scopeCreepReason) {
		t.Fatalf("reasons must share the waiver-lookup prefix: %q / %q", reason1, reason2)
	}
	if reason1 == reason2 {
		t.Fatalf("different surface sets must yield different reasons (dedup would go stale): %q", reason1)
	}
	if r, _ := scopeCreepEscalation("other drift text", []string{"CheckAccess", "route /admin"}); r != reason1 {
		t.Fatalf("the fingerprint must be order-independent and detail-independent: %q vs %q", r, reason1)
	}
	for _, entry := range []string{"route /admin", "CheckAccess"} {
		if !strings.Contains(detail1, "\n"+entry) {
			t.Fatalf("detail must carry each untraced entry as a canonical line, got %q", detail1)
		}
	}
}

// or-g2qf.1: waiver coverage — every current untraced entry must appear as a
// canonical line in some RESOLVED scope-creep escalation's detail; new surface
// re-blocks; substring collisions don't count as coverage.
func TestScopeCreepCovered(t *testing.T) {
	_, detail := scopeCreepEscalation("dr", []string{"route /admin", "CheckAccess"})
	resolved := []contextstore.Escalation{{Detail: detail, Resolved: true}}

	if !scopeCreepCovered(resolved, []string{"route /admin", "CheckAccess"}) {
		t.Fatal("a resolved escalation covering the exact surface must waive it")
	}
	if !scopeCreepCovered(resolved, []string{"CheckAccess"}) {
		t.Fatal("a subset of a waived surface is still waived")
	}
	if scopeCreepCovered(resolved, []string{"route /admin", "CheckAccess", "NewPolicy"}) {
		t.Fatal("new untraced surface must re-block (not covered by the old waiver)")
	}
	if scopeCreepCovered(resolved, []string{"route /adm"}) {
		t.Fatal("a substring of a waived entry is not coverage")
	}
	if scopeCreepCovered(nil, []string{"route /admin"}) {
		t.Fatal("no resolved escalations means nothing is waived")
	}
	if !scopeCreepCovered(nil, nil) {
		t.Fatal("an empty untraced set is vacuously covered")
	}
}
