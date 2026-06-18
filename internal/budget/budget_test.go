package budget

import "testing"

// TestRunHaltsAndEscalatesWhenCeilingConfigured: with no ceiling the accountant
// never halts (accounting only); with a ceiling it warns at ~80% and halts +
// escalates at 100%.
func TestRunHaltsAndEscalatesWhenCeilingConfigured(t *testing.T) {
	t.Run("no ceiling never halts", func(t *testing.T) {
		a := New()
		a.Record(1_000_000, 9999)
		if a.Halted() {
			t.Fatal("accountant with no ceiling must never halt")
		}
		if s := a.Snapshot(); s.HasCeiling {
			t.Fatal("snapshot should report no ceiling")
		}
		if len(a.Escalations()) != 0 {
			t.Fatalf("no ceiling should produce no escalations; got %d", len(a.Escalations()))
		}
	})

	t.Run("ceiling warns then halts and escalates", func(t *testing.T) {
		a := NewWithCeiling(Ceiling{MaxTokens: 1000})
		a.Record(800, 0) // 80% → warn
		if got := a.Snapshot().State; got != StateWarn {
			t.Fatalf("state = %q, want warn at 80%%", got)
		}
		if a.Halted() {
			t.Fatal("should not be halted at 80%")
		}
		a.Record(300, 0) // 110% → halt
		if !a.Halted() {
			t.Fatal("should be halted past the ceiling")
		}
		esc := a.Escalations()
		if len(esc) < 2 || esc[len(esc)-1].State != StateHalt {
			t.Fatalf("expected warn then halt escalations; got %+v", esc)
		}
	})

	t.Run("dollar ceiling halts independently", func(t *testing.T) {
		a := NewWithCeiling(Ceiling{MaxDollars: 10})
		a.Record(0, 11)
		if !a.Halted() {
			t.Fatal("dollar ceiling should halt")
		}
	})
}

// TestAccountingAlwaysTracks: spend accumulates and is visible in the snapshot
// regardless of ceiling.
func TestAccountingAlwaysTracks(t *testing.T) {
	a := New()
	a.Record(120, 0.5)
	a.Record(80, 0.25)
	s := a.Snapshot()
	if s.Tokens != 200 || s.Dollars != 0.75 {
		t.Fatalf("snapshot = %+v, want tokens=200 dollars=0.75", s)
	}
}
