package stats

import (
	"errors"
	"math"
	"testing"
)

func TestPocockBoundaryReturnsKnownValues(t *testing.T) {
	// Reference: Pocock 1977 / Jennison & Turnbull 2000 table.
	// Per-look two-sided alpha for overall α=0.05.
	cases := []struct {
		maxLooks int
		approx   float64
	}{
		{1, 0.05},
		{2, 0.0294},
		{3, 0.0221},
		{4, 0.0182},
		{5, 0.0158},
	}
	for _, c := range cases {
		got, err := PocockBoundary(1, c.maxLooks, 0.05)
		if err != nil {
			t.Fatalf("PocockBoundary maxLooks=%d: %v", c.maxLooks, err)
		}
		if math.Abs(got-c.approx) > 0.001 {
			t.Errorf("maxLooks=%d: per-look α = %f, want ≈ %f", c.maxLooks, got, c.approx)
		}
	}
}

func TestPocockRejectsBadLooks(t *testing.T) {
	cases := []struct{ n, max int }{
		{0, 5},
		{6, 5},
		{-1, 5},
		{1, 0},
	}
	for _, c := range cases {
		if _, err := PocockBoundary(c.n, c.max, 0.05); !errors.Is(err, ErrInvalidLooks) {
			t.Errorf("looks=%d/%d: expected ErrInvalidLooks, got %v", c.n, c.max, err)
		}
	}
}

func TestPocockRejectsBadAlpha(t *testing.T) {
	if _, err := PocockBoundary(1, 5, 0); !errors.Is(err, ErrInvalidLevel) {
		t.Errorf("expected ErrInvalidLevel for alpha=0, got %v", err)
	}
	if _, err := PocockBoundary(1, 5, 1.5); !errors.Is(err, ErrInvalidLevel) {
		t.Errorf("expected ErrInvalidLevel for alpha=1.5, got %v", err)
	}
}

func TestPocockMonotonicInLooks(t *testing.T) {
	// More looks → smaller per-look α (more conservative).
	prev := 1.0
	for k := 1; k <= 10; k++ {
		got, err := PocockBoundary(1, k, 0.05)
		if err != nil {
			t.Fatal(err)
		}
		if got > prev {
			t.Errorf("non-monotonic at maxLooks=%d: prev=%f now=%f", k, prev, got)
		}
		prev = got
	}
}
