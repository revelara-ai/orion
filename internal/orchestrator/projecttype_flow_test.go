package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
)

// The dogfood intent that exposed the misclassification (413691a4, session
// 20260713T095045_s1): a large vague game with no explicit service signal.
const gameIntent = "I'd like to build a game that is like Arc Raiders, but completely PvE instead of a mix between PvE and PvP. The enemies behave like real AI driven mechs with reinforcement learning movement. I expect this to be a large project."

// TestSubmitPersistsUnclassifiedTypeAndScale (or-045a.1): a no-signal intent is
// persisted as UNCLASSIFIED (never a silent http-service) with its scale class,
// both surfaced in the Confirmation, and the intake asks zero HTTP questions.
func TestSubmitPersistsUnclassifiedTypeAndScale(t *testing.T) {
	c, ctx := storeConductor(t)
	conf, err := c.Submit(ctx, gameIntent)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if conf.ProjectType != completeness.Unclassified || conf.Scale != completeness.ScaleLarge {
		t.Fatalf("confirmation must surface type+scale: type=%q scale=%q", conf.ProjectType, conf.Scale)
	}
	for _, d := range conf.OpenDecisions {
		switch d.Key {
		case "response_format", "port", "route":
			t.Fatalf("an unclassified intent must not be asked the HTTP question %q", d.Key)
		}
	}
	proj, _, err := c.Store().CurrentProjectSpec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if proj.ProjectType != completeness.Unclassified {
		t.Fatalf("persisted type = %q, want unclassified", proj.ProjectType)
	}
	if proj.Scale != completeness.ScaleLarge {
		t.Fatalf("persisted scale = %q, want large", proj.Scale)
	}

	// Negative (no over-reach): the canonical http intent keeps its type, its
	// functional questions, and standard scale — the V2.0 path is unchanged.
	c2, ctx2 := storeConductor(t)
	conf2, err := c2.Submit(ctx2, flowIntent)
	if err != nil {
		t.Fatal(err)
	}
	if conf2.ProjectType != "http-service" || conf2.Scale != completeness.ScaleStandard {
		t.Fatalf("http intent: type=%q scale=%q", conf2.ProjectType, conf2.Scale)
	}
	keys := map[string]bool{}
	for _, d := range conf2.OpenDecisions {
		keys[d.Key] = true
	}
	if !keys["response_format"] || !keys["port"] || !keys["route"] {
		t.Fatalf("http intent must still raise its functional questions, got %v", keys)
	}
}

// TestRatifyBlockedUntilProjectTypeResolved (or-045a.1): an unclassified
// project cannot ratify — the vacuous-ratification trap (or-ep1: zero blocking
// decisions => ratifies with no human input). SetProjectType (the developer-
// ratified type the conductor proposes in conversation) unblocks it and swaps
// the gate to the resolved type's checklist.
func TestRatifyBlockedUntilProjectTypeResolved(t *testing.T) {
	c, ctx := storeConductor(t)
	if _, err := c.Submit(ctx, gameIntent); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ApproveAssumptions(ctx); err != nil {
		t.Fatal(err)
	}
	_, err := c.ApproveSpec(ctx)
	if err == nil {
		t.Fatal("ratifying an unclassified project must be blocked")
	}
	if !strings.Contains(err.Error(), "project type") {
		t.Fatalf("the block must name the missing project type, got: %v", err)
	}

	// Resolve the type (as the conductor would after the developer confirms).
	if err := c.SetProjectType(ctx, "game"); err != nil {
		t.Fatalf("set project type: %v", err)
	}
	proj, _, err := c.Store().CurrentProjectSpec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if proj.ProjectType != "game" {
		t.Fatalf("persisted type after resolution = %q, want game", proj.ProjectType)
	}
	// The gate now speaks the resolved type (an unregistered type still gets
	// only universal dimensions — no HTTP questions appear from nowhere).
	if got := c.gate.ProjectType(); got != "game" {
		t.Fatalf("gate project type = %q, want game", got)
	}

	// Invalid resolutions are refused: empty, unclassified, and junk.
	for _, bad := range []string{"", "unclassified", "Not A Slug!"} {
		if err := c.SetProjectType(ctx, bad); err == nil {
			t.Fatalf("SetProjectType(%q) must be refused", bad)
		}
	}
}

// A SetScale round-trip at the store layer (the additive column migrates).
func TestProjectScaleRoundTrip(t *testing.T) {
	st, err := contextstore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	var id string
	if err := st.WithTx(ctx, func(tx *contextstore.Tx) error {
		var e error
		id, e = tx.Projects().Create(ctx, "p", "intent", "http-service")
		if e != nil {
			return e
		}
		return tx.Projects().SetScale(ctx, id, "large")
	}); err != nil {
		t.Fatal(err)
	}
	var got contextstore.Project
	if err := st.WithTx(ctx, func(tx *contextstore.Tx) error {
		var e error
		got, e = tx.Projects().Get(ctx, id)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if got.Scale != "large" {
		t.Fatalf("scale round-trip = %q, want large", got.Scale)
	}
}
