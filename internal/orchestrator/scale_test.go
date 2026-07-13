package orchestrator

import (
	"strings"
	"testing"
)

// TestSubmitVerbatimHonorsScale (or-hn15.2 DONE-WHEN b+d): scale is classified
// from the developer's VERBATIM words, not the agent's paraphrase — so a
// paraphrase that drops "I expect this to be a large project" still classifies
// large. Verbatim only ever UPGRADES the signal; it never downgrades one the
// intent already carries.
func TestSubmitVerbatimHonorsScale(t *testing.T) {
	// Paraphrase lost the scale signal; the verbatim turn kept it.
	c, ctx := storeConductor(t)
	conf, err := c.SubmitVerbatim(ctx,
		"Build a PvE game like Arc Raiders with RL-driven mechs.",
		"I'd like to build a game like Arc Raiders, pure PvE. I expect this to be a large project.")
	if err != nil {
		t.Fatal(err)
	}
	if conf.Scale != "large" {
		t.Fatalf("a verbatim large-project signal must classify large despite the paraphrase, got %q", conf.Scale)
	}
	proj, _, err := c.Store().CurrentProjectSpec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if proj.Scale != "large" {
		t.Fatalf("the large scale must persist on the project row, got %q", proj.Scale)
	}

	// Negative: no large signal anywhere → standard (verbatim doesn't invent it).
	c2, ctx2 := storeConductor(t)
	conf2, err := c2.SubmitVerbatim(ctx2, "Build a small time service.", "just a tiny thing this afternoon")
	if err != nil {
		t.Fatal(err)
	}
	if conf2.Scale != "standard" {
		t.Fatalf("no large signal must stay standard, got %q", conf2.Scale)
	}

	// Verbatim never DOWNGRADES: the intent itself carries the signal.
	c3, ctx3 := storeConductor(t)
	conf3, err := c3.SubmitVerbatim(ctx3, "Build a large platform, a multi-team effort.", "small")
	if err != nil {
		t.Fatal(err)
	}
	if conf3.Scale != "large" {
		t.Fatalf("a large signal in the intent must stand even if verbatim is terse, got %q", conf3.Scale)
	}

	// Plain Submit (no verbatim) is unchanged.
	c4, ctx4 := storeConductor(t)
	conf4, err := c4.Submit(ctx4, "Build an ambitious project spanning many teams.")
	if err != nil {
		t.Fatal(err)
	}
	if conf4.Scale != "large" {
		t.Fatalf("Submit must still classify a large intent large, got %q", conf4.Scale)
	}
}

// TestSetScaleRecovers (or-hn15.2 DONE-WHEN c): set_scale is the recovery path
// for a misclassified scale — it re-persists the scale, rebuilds the gate, and
// records an audited gold label; junk and no-project are refused.
func TestSetScaleRecovers(t *testing.T) {
	c, ctx := storeConductor(t)
	if _, err := c.Submit(ctx, "Build a plain time service."); err != nil {
		t.Fatal(err)
	}
	proj, _, err := c.Store().CurrentProjectSpec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if proj.Scale != "standard" {
		t.Fatalf("precondition: expected standard, got %q", proj.Scale)
	}

	if err := c.SetScale(ctx, "large"); err != nil {
		t.Fatalf("set_scale must accept a valid scale: %v", err)
	}
	proj, _, err = c.Store().CurrentProjectSpec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if proj.Scale != "large" {
		t.Fatalf("set_scale must re-persist the scale, got %q", proj.Scale)
	}
	// Audited: a scale gold label lands on the project.
	labels, err := c.Store().ListGoldLabels(ctx, proj.ID)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, l := range labels {
		if l.RatificationKind == "scale" {
			found = true
		}
	}
	if !found {
		t.Fatalf("set_scale must record an audited scale gold label, got %+v", labels)
	}

	// Negative: an unknown scale is refused (closed vocabulary).
	if err := c.SetScale(ctx, "gigantic"); err == nil {
		t.Fatal("set_scale must refuse an unknown scale")
	}
	// Negative: no active project is refused.
	c2, ctx2 := storeConductor(t)
	if err := c2.SetScale(ctx2, "large"); err == nil {
		t.Fatal("set_scale must refuse when no project is active")
	}
}

// TestSetScaleRaisesDirectionRail (or-hn15.2): upgrading to large through the
// recovery path rebuilds the gate so the large-only rail (goals-first / the
// direction family, or-045a) actually engages — a misclassification is fully
// recoverable, not cosmetic.
func TestSetScaleRaisesDirectionRail(t *testing.T) {
	c, ctx := storeConductor(t)
	if _, err := c.Submit(ctx, "Build a plain time service."); err != nil {
		t.Fatal(err)
	}
	if err := c.SetScale(ctx, "large"); err != nil {
		t.Fatal(err)
	}
	sv, err := c.SpecView(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var hasDirection bool
	for _, d := range sv.OpenDecisions {
		if strings.HasPrefix(d.Key, "direction.") {
			hasDirection = true
		}
	}
	if !hasDirection {
		t.Fatalf("after upgrading to large the direction rail must appear in the checklist, got %+v", sv.OpenDecisions)
	}
}
