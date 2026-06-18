package stpa

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
)

// TestQuestionnairePhasesGated: the four phases are strictly ordered (no skipping,
// no rubber-stamping), the control-structure completeness rule (every action has
// a feedback path) is enforced, and the model is only available once complete.
func TestQuestionnairePhasesGated(t *testing.T) {
	q := New()

	// Cannot skip ahead.
	if err := q.RatifyControlStructure(DefaultModel().Structure); err == nil {
		t.Fatal("must not ratify control structure before losses")
	}
	if _, err := q.Model(); err == nil {
		t.Fatal("model must not be available before completion")
	}

	// Phase 1: losses.
	if err := q.RatifyLosses(nil); err == nil {
		t.Fatal("empty losses must be rejected")
	}
	if err := q.RatifyLosses(DefaultModel().Losses); err != nil {
		t.Fatalf("ratify losses: %v", err)
	}
	if q.Phase() != PhaseControlStructure {
		t.Fatalf("phase = %s, want control-structure", q.Phase())
	}

	// Phase 2: completeness — a control action with no feedback path is rejected.
	badCS := ControlStructure{
		Controllers: []string{"X"},
		Actions:     []ControlAction{{ID: "CA1", Controller: "X", Action: "do", Feedback: FeedbackPath{}}},
	}
	if err := q.RatifyControlStructure(badCS); err == nil {
		t.Fatal("control action without a feedback path must be rejected")
	}
	if err := q.RatifyControlStructure(DefaultModel().Structure); err != nil {
		t.Fatalf("ratify control structure: %v", err)
	}

	// Phase 3: a UCA referencing an unknown control action is rejected.
	if err := q.RatifyUCAs([]UCA{{ID: "U", ControlAction: "NOPE", Type: NotProvided}}); err == nil {
		t.Fatal("UCA referencing unknown control action must be rejected")
	}
	if err := q.RatifyUCAs(DefaultModel().UCAs); err != nil {
		t.Fatalf("ratify UCAs: %v", err)
	}

	// Phase 4.
	if err := q.RatifyLossScenarios(DefaultModel().Scenarios); err != nil {
		t.Fatalf("ratify scenarios: %v", err)
	}
	if q.Phase() != PhaseComplete {
		t.Fatalf("phase = %s, want complete", q.Phase())
	}
	m, err := q.Model()
	if err != nil {
		t.Fatalf("model: %v", err)
	}
	if len(m.Losses) == 0 || len(m.Structure.Actions) == 0 || len(m.UCAs) == 0 || len(m.Scenarios) == 0 {
		t.Fatal("ratified model is incomplete")
	}
}

// TestDefaultsAreChangeable: the developer can amend the seeded defaults and the
// amended model ratifies (defaults are a starting point, not the final word).
func TestDefaultsAreChangeable(t *testing.T) {
	m := DefaultModel()
	m.Losses = append(m.Losses, Loss{ID: "L4", Description: "PII leak in logs"})
	m.Scenarios[1].Controls = append(m.Scenarios[1].Controls, "redact PII in structured logs")

	ratified, err := RatifyDefaults(m)
	if err != nil {
		t.Fatalf("ratify amended defaults: %v", err)
	}
	if len(ratified.Losses) != len(DefaultModel().Losses)+1 {
		t.Fatalf("amendment not preserved: %d losses", len(ratified.Losses))
	}
}

// TestDispositionGapsRequireExplicitApproval: a UCA is open until disposed;
// accepting a gap requires a documented rationale; open UCAs are the blocking set.
func TestDispositionGapsRequireExplicitApproval(t *testing.T) {
	m := DefaultModel()
	// Defaults are undecided → all open initially.
	if len(m.OpenUCAs()) != len(m.UCAs) {
		t.Fatalf("expected all %d UCAs open by default, got %d", len(m.UCAs), len(m.OpenUCAs()))
	}
	// Accepting a gap without a rationale is refused (gaps must be documented).
	if err := m.Decide("UCA1", DispositionAcceptedGap, "", "dev"); err == nil {
		t.Fatal("accepted gap without rationale must be refused")
	}
	// Control one, accept one with rationale.
	if err := m.Decide("UCA1", DispositionControlled, "request timeouts implemented", "dev"); err != nil {
		t.Fatalf("decide controlled: %v", err)
	}
	if err := m.Decide("UCA4", DispositionAcceptedGap, "slowloris mitigation deferred to V2.1", "dev"); err != nil {
		t.Fatalf("decide gap: %v", err)
	}
	if m.Decide("NOPE", DispositionControlled, "", "dev") == nil {
		t.Fatal("deciding an unknown UCA must error")
	}
	open := m.OpenUCAs()
	if len(open) != len(m.UCAs)-2 {
		t.Fatalf("open UCAs = %d, want %d", len(open), len(m.UCAs)-2)
	}
	if len(m.AcceptedGaps()) != 1 {
		t.Fatalf("accepted gaps = %d, want 1", len(m.AcceptedGaps()))
	}
	if !strings.Contains(m.DecisionRecord(), "accepted gaps") {
		t.Fatal("decision record should summarize accepted gaps")
	}
}

// TestRatifiedTimeServiceModel: the golden ratified model passes all four gates,
// has no open (undecided) UCAs, records exactly the three accepted gaps, and
// every control action closes a feedback loop.
func TestRatifiedTimeServiceModel(t *testing.T) {
	m := RatifiedTimeServiceModel()
	if _, err := RatifyDefaults(m); err != nil {
		t.Fatalf("ratified model fails the gated questionnaire: %v", err)
	}
	if open := m.OpenUCAs(); len(open) != 0 {
		t.Fatalf("ratified model has %d open UCAs (must be 0 to move forward)", len(open))
	}
	if gaps := m.AcceptedGaps(); len(gaps) != 3 {
		t.Fatalf("accepted gaps = %d, want 3 (UCA5-7)", len(gaps))
	}
	for _, a := range m.Structure.Actions {
		if a.Feedback.Signal == "" {
			t.Fatalf("control action %s has no feedback loop", a.ID)
		}
	}
}

// TestPersistAndRetrieve: a ratified model is persisted and retrievable (the
// trusted source for hazard proof).
func TestPersistAndRetrieve(t *testing.T) {
	ctx := context.Background()
	s, err := contextstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()
	var pid string
	_ = s.WithTx(ctx, func(tx *contextstore.Tx) error {
		pid, _ = tx.Projects().Create(ctx, "demo", "time service")
		return nil
	})

	model, err := RatifyDefaults(DefaultModel())
	if err != nil {
		t.Fatalf("ratify: %v", err)
	}
	if err := Save(ctx, s, pid, model); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, ok, err := Load(ctx, s, pid)
	if err != nil || !ok {
		t.Fatalf("load: ok=%v err=%v", ok, err)
	}
	if !reflect.DeepEqual(got, model) {
		t.Fatalf("retrieved model differs from saved")
	}
}
