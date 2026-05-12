package detection

import (
	"testing"
	"time"
)

func mkRun(daysAgo int, orion, customer int) RunSummary {
	return RunSummary{
		StartedAt:              time.Now().AddDate(0, 0, -daysAgo),
		OrionFiledProcessed:    orion,
		CustomerFiledProcessed: customer,
	}
}

func TestLoopGuard_FiresOnExcessiveRatio(t *testing.T) {
	// 3 runs in last 31d, each with orion=10, customer=2 (ratio 5x).
	// Window >= 30d. Should fire.
	runs := []RunSummary{
		mkRun(1, 10, 2),
		mkRun(15, 10, 2),
		mkRun(31, 10, 2),
	}
	got := LoopGuardCheck(runs, time.Now())
	if !got.Warning {
		t.Errorf("expected warning: %+v", got)
	}
}

func TestLoopGuard_SuppressesAtThreshold(t *testing.T) {
	// 10/4 = 2.5x, below the 3x threshold. No warning.
	runs := []RunSummary{
		mkRun(1, 10, 4),
		mkRun(15, 10, 4),
		mkRun(31, 10, 4),
	}
	got := LoopGuardCheck(runs, time.Now())
	if got.Warning {
		t.Errorf("expected no warning at 2.5x: %+v", got)
	}
}

func TestLoopGuard_SuppressesFirst3Runs(t *testing.T) {
	// Only 2 prior runs → suppressed regardless of ratio.
	runs := []RunSummary{
		mkRun(1, 100, 0),
		mkRun(15, 100, 0),
	}
	got := LoopGuardCheck(runs, time.Now())
	if got.Warning {
		t.Errorf("expected suppression for <3 runs: %+v", got)
	}
}

func TestLoopGuard_SuppressesShortWindow(t *testing.T) {
	// 3 runs but all within 20d → window too short, no warning.
	runs := []RunSummary{
		mkRun(1, 10, 0),
		mkRun(10, 10, 0),
		mkRun(20, 10, 0),
	}
	got := LoopGuardCheck(runs, time.Now())
	if got.Warning {
		t.Errorf("expected suppression for 20d window: %+v", got)
	}
}

func TestLoopGuard_OneRunBelowThresholdSuppresses(t *testing.T) {
	// Two runs at 5x, one at 1x → suppressed (not consecutive).
	runs := []RunSummary{
		mkRun(1, 10, 2),
		mkRun(15, 10, 10), // 1x
		mkRun(31, 10, 2),
	}
	got := LoopGuardCheck(runs, time.Now())
	if got.Warning {
		t.Errorf("expected suppression with mixed ratios: %+v", got)
	}
}

func TestLoopGuard_ZeroActivitySuppresses(t *testing.T) {
	// One run with zero on both sides → undefined ratio, suppressed.
	runs := []RunSummary{
		mkRun(1, 0, 0),
		mkRun(15, 10, 2),
		mkRun(31, 10, 2),
	}
	got := LoopGuardCheck(runs, time.Now())
	if got.Warning {
		t.Errorf("expected suppression on zero activity: %+v", got)
	}
}

func TestLoopGuard_ZeroCustomerWithOrionFiledQualifies(t *testing.T) {
	// 3 runs with customer=0 but orion>0 → ratio is "infinity";
	// treat as qualifying for warning iff window+suppression pass.
	runs := []RunSummary{
		mkRun(1, 10, 0),
		mkRun(15, 10, 0),
		mkRun(31, 10, 0),
	}
	got := LoopGuardCheck(runs, time.Now())
	if !got.Warning {
		t.Errorf("expected warning when orion-only filings span 30d: %+v", got)
	}
}

func TestLoopGuard_OnlyFirstThreeConsidered(t *testing.T) {
	// 5 runs given; only the 3 newest count. The 4th and 5th are
	// noise per the spec ("3 consecutive runs"). All 3 newest pass
	// → fire.
	runs := []RunSummary{
		mkRun(1, 10, 2),
		mkRun(15, 10, 2),
		mkRun(31, 10, 2),
		mkRun(60, 0, 0),   // noise
		mkRun(120, 0, 0),  // noise
	}
	got := LoopGuardCheck(runs, time.Now())
	if !got.Warning {
		t.Errorf("expected warning with only top-3 counted: %+v", got)
	}
}
