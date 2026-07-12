package acceptance

import (
	"os/exec"
	"sort"
	"strings"
	"testing"
)

const modulePath = "github.com/revelara-ai/orion"

// deferredOrphans are internal packages intentionally not yet reachable from the
// orion binary. Each entry MUST carry a reason + tracking id. This allowlist is a
// RATCHET: it shrinks as packages are wired, a NEW unreachable package (not
// listed) fails the gate, and an entry that becomes reachable also fails (forcing
// its removal). It is the executable form of Orion's own rule — built ≠ wired.
//
// Process note (or-rsm): six packages were built + unit-proven + mutation-tested
// yet unreachable from cmd/orion. Per-task Done-when proved components, never the
// wired system. This gate closes that gap.
// Previously empty: or-b73 wired memory + contextengine into Conductor context
// assembly; or-y0z exposed tracker via `orion tracker project`.
var deferredOrphans = map[string]string{
	"internal/reliabilityfloor": "or-uvw.8 wires the log-only floor into ChangeAndProve; library slices or-uvw.1–.5 land first by design (docs/PRD/orion-reliability-floor.md slice plan)",
}

// goListInternal returns the set of internal/ packages emitted by `go list <args>`.
func goListInternal(t *testing.T, args ...string) map[string]bool {
	t.Helper()
	out, err := exec.Command("go", args...).Output()
	if err != nil {
		t.Fatalf("go %s: %v", strings.Join(args, " "), err)
	}
	set := map[string]bool{}
	for _, line := range strings.Fields(string(out)) {
		if rel, ok := strings.CutPrefix(line, modulePath+"/"); ok && strings.HasPrefix(rel, "internal/") {
			set[rel] = true
		}
	}
	return set
}

// TestNoOrphanPackages is the wireup gate (or-rsm): every internal package must be
// reachable from cmd/orion, or explicitly deferred with a reason. "Built but not
// wired" is a failing test.
func TestNoOrphanPackages(t *testing.T) {
	reachable := goListInternal(t, "list", "-deps", modulePath+"/cmd/orion")
	all := goListInternal(t, "list", modulePath+"/internal/...")
	if len(all) == 0 {
		t.Fatal("go list returned no internal packages")
	}

	var orphans, staleAllow []string
	for p := range all {
		if reachable[p] || deferredOrphans[p] != "" {
			continue
		}
		orphans = append(orphans, p)
	}
	// A deferred entry that is now reachable must be removed — keeps the ratchet honest.
	for p := range deferredOrphans {
		if reachable[p] {
			staleAllow = append(staleAllow, p)
		}
	}
	sort.Strings(orphans)
	sort.Strings(staleAllow)

	if len(orphans) > 0 {
		t.Fatalf("orphan package(s) built but unreachable from cmd/orion and NOT on the deferred allowlist: %v\n"+
			"either wire them into the binary or add them to deferredOrphans with a reason + tracking id", orphans)
	}
	if len(staleAllow) > 0 {
		t.Fatalf("deferred-allowlist entries are now reachable — remove them from deferredOrphans to tighten the ratchet: %v", staleAllow)
	}
}
