package conductor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/sandbox"
)

// or-dig(1): a proven-but-ESCALATED epic must NOT produce a PR handoff even
// with ORION_GIT_DELIVERY set. The lever here is the security gate: a service
// that PROVES (behavioral+empirical+hazard Accept) but carries a hardcoded
// secret escalates at the delivery bar (SecurityClean=false) — so the export
// runs (barVerdict==Accept) but the PR block's `if res.Decision == Deliver`
// guard suppresses the handoff. This exercises that guard, which was
// compile-checked only.
func TestEscalatedEpicSuppressesPRHandoff(t *testing.T) {
	if testing.Short() {
		t.Skip("full build + prove")
	}
	t.Setenv("ORION_GIT_DELIVERY", "1")
	oc, ctx := ratifiedTimeService(t)

	// Generate the proving time service, then plant a hardcoded secret so the
	// SECURITY gate escalates the delivery while proof still Accepts.
	gen := func(_ context.Context, gs sandbox.GenSpec, dir, _ string) (sandbox.GeneratedArtifact, error) {
		art, err := sandbox.GenerateTimeServiceFixture(dir, gs)
		if err != nil {
			return art, err
		}
		secret := "package main\n\n// planted for or-dig: proves fine, but the security gate escalates delivery\nconst apiKey = \"sk-live-abcdef0123456789\"\n"
		if werr := os.WriteFile(filepath.Join(dir, "secret.go"), []byte(secret), 0o600); werr != nil {
			return art, werr
		}
		return art, nil
	}

	res, err := BuildAndProve(ctx, oc.Store(), gen, nil, nil, t.TempDir())
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	// The code PROVED (behavioral+empirical+hazard) — the escalation is the
	// bar's, not proof's.
	if res.Verdict != "Accept" {
		t.Fatalf("the service must prove Accept — security is a BAR gate, not a proof mode: %+v", res)
	}
	// …but the security gate escalated the delivery.
	if res.Delivery != "escalate" {
		t.Fatalf("a hardcoded secret must escalate the delivery bar: %+v", res)
	}
	// The safety invariant: NO PR handoff for an escalated epic, even though
	// export/git-delivery ran (barVerdict==Accept).
	if res.PR.ArtifactPath != "" || res.PR.Opened || res.PR.URL != "" {
		t.Fatalf("an escalated epic must NOT produce a PR handoff, got %+v", res.PR)
	}
}

// or-dig(2): the export/commit/PR/analyzer steps use DISTINCT phase names, so
// the delivery-DECISION line survives RenderPhaseReport's last-status-per-
// phase collapse instead of being clobbered by a later "PR-ready" line.
func TestDeliveryDecisionSurvivesRender(t *testing.T) {
	// The emit ORDER build.go produces: the decision, THEN export/PR/analyzer.
	events := []PhaseEvent{
		{Phase: "Prove", Status: PhaseDone, Detail: "converged"},
		{Phase: "Deliver", Status: PhaseWarn, Detail: "escalate: security gate failed"},
		{Phase: "Export", Status: PhaseDone, Detail: "code written to /out (3 files)"},
		{Phase: "Analyzer", Status: PhaseDone, Detail: "investigation analyzer generated"},
		{Phase: "PR", Status: PhaseDone, Detail: "PR-ready: branch orion-x + pr-x.md"},
	}
	report := RenderPhaseReport(events)

	// The decision line MUST survive verbatim.
	if !strings.Contains(report, "Deliver — escalate: security gate failed") {
		t.Fatalf("the delivery decision line was clobbered:\n%s", report)
	}
	// The later steps render under their OWN names (not overwriting Deliver).
	for _, want := range []string{"Export — code written", "PR — PR-ready", "Analyzer — investigation"} {
		if !strings.Contains(report, want) {
			t.Fatalf("step %q missing from report:\n%s", want, report)
		}
	}
	// Regression guard: if these steps reused "Deliver", the escalation detail
	// would be replaced by the last one. Prove exactly one Deliver line, and
	// it's the decision.
	if n := strings.Count(report, "Deliver"); n != 1 {
		t.Fatalf("exactly one Deliver line expected (the decision), got %d:\n%s", n, report)
	}
}
