package stats

import (
	"errors"
	"math"
	"testing"
)

func TestBonferroniSplitsAlphaEvenly(t *testing.T) {
	// Overall α=0.05 across 4 axes → per-axis α=0.0125 → confidence 0.9875.
	got, err := BonferroniAdjust(0.95, 4)
	if err != nil {
		t.Fatalf("BonferroniAdjust: %v", err)
	}
	if math.Abs(got-0.9875) > 1e-6 {
		t.Errorf("BonferroniAdjust(0.95, 4) = %f, want 0.9875", got)
	}
}

func TestBonferroniSingleAxisIsIdentity(t *testing.T) {
	got, err := BonferroniAdjust(0.95, 1)
	if err != nil {
		t.Fatalf("BonferroniAdjust: %v", err)
	}
	if got != 0.95 {
		t.Errorf("BonferroniAdjust(0.95, 1) = %f, want 0.95", got)
	}
}

func TestBonferroniRejectsBadInputs(t *testing.T) {
	if _, err := BonferroniAdjust(0, 4); !errors.Is(err, ErrInvalidLevel) {
		t.Errorf("expected ErrInvalidLevel, got %v", err)
	}
	if _, err := BonferroniAdjust(0.95, 0); !errors.Is(err, ErrInvalidLooks) {
		t.Errorf("expected ErrInvalidLooks for 0 axes, got %v", err)
	}
	if _, err := BonferroniAdjust(0.95, -1); !errors.Is(err, ErrInvalidLooks) {
		t.Errorf("expected ErrInvalidLooks for -1 axes, got %v", err)
	}
}
