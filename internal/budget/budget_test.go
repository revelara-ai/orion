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

// TestFromEnv (or-v9f.18): ceilings are configurable from the environment; unset
// or invalid values keep the accounting-only posture that never halts.
func TestFromEnv(t *testing.T) {
	t.Setenv("ORION_BUDGET_MAX_TOKENS", "")
	t.Setenv("ORION_BUDGET_MAX_DOLLARS", "")
	t.Setenv("ORION_BUDGET_MAX_WALL_MINUTES", "")
	if a := FromEnv(); a.Snapshot().HasCeiling {
		t.Fatal("no env → accounting-only, no ceiling")
	}

	t.Setenv("ORION_BUDGET_MAX_TOKENS", "1000")
	a := FromEnv()
	if !a.Snapshot().HasCeiling {
		t.Fatal("token ceiling from env must arm the accountant")
	}
	a.Record(1000, 0)
	if !a.Halted() {
		t.Fatal("recording up to the env ceiling must halt")
	}

	t.Setenv("ORION_BUDGET_MAX_TOKENS", "not-a-number")
	if a := FromEnv(); a.Snapshot().HasCeiling {
		t.Fatal("an unparseable value is ignored, never a partial ceiling")
	}
}

// TestSeedEvaluatesCeilingAgainstCumulativeSpend (or-v9f.28): seeding from
// the persisted ledger makes ceilings evaluate PROJECT spend — a restart
// cannot reset the basis. Idempotent.
func TestSeedEvaluatesCeilingAgainstCumulativeSpend(t *testing.T) {
	a := NewWithCeiling(Ceiling{MaxDollars: 1})
	a.Seed(1000, 2.0) // prior runs already crossed the ceiling
	if !a.Halted() {
		t.Fatal("seeded cumulative spend beyond the ceiling must halt")
	}
	a.Seed(1000, 2.0) // second seed is a no-op
	if s := a.Snapshot(); s.Dollars != 2.0 {
		t.Fatalf("seeding must be idempotent, got $%.2f", s.Dollars)
	}
}
