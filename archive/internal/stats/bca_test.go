package stats

import (
	"errors"
	"math"
	"testing"
)

func TestBCaConstantSampleCollapses(t *testing.T) {
	// All same values: bootstrap means all equal observed; CI should
	// collapse around observed.
	sample := []float64{5, 5, 5, 5, 5}
	ci, err := BCa(sample, BCaOptions{Level: 0.95, Replicates: 200, Seed: 1})
	if err != nil {
		t.Fatalf("BCa: %v", err)
	}
	if math.Abs(ci.Lower-5) > 0.01 || math.Abs(ci.Upper-5) > 0.01 {
		t.Errorf("constant-sample CI = [%f, %f], want ~[5, 5]", ci.Lower, ci.Upper)
	}
}

func TestBCaSinglePointCI(t *testing.T) {
	ci, err := BCa([]float64{42}, BCaOptions{Level: 0.95, Replicates: 100, Seed: 1})
	if err != nil {
		t.Fatalf("BCa: %v", err)
	}
	if ci.Lower != 42 || ci.Upper != 42 {
		t.Errorf("single-point CI = [%f, %f], want [42, 42]", ci.Lower, ci.Upper)
	}
}

func TestBCaWideSampleProducesWideCI(t *testing.T) {
	// Sample with high variance.
	sample := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 100}
	ci, err := BCa(sample, BCaOptions{Level: 0.95, Replicates: 500, Seed: 1})
	if err != nil {
		t.Fatalf("BCa: %v", err)
	}
	if ci.Width() < 1 {
		t.Errorf("wide-sample CI too narrow: width=%f", ci.Width())
	}
	if ci.Lower >= ci.Upper {
		t.Errorf("malformed CI: lower=%f upper=%f", ci.Lower, ci.Upper)
	}
}

func TestBCaIsDeterministicWithSeed(t *testing.T) {
	sample := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	a, _ := BCa(sample, BCaOptions{Level: 0.95, Replicates: 200, Seed: 42})
	b, _ := BCa(sample, BCaOptions{Level: 0.95, Replicates: 200, Seed: 42})
	if a.Lower != b.Lower || a.Upper != b.Upper {
		t.Errorf("non-deterministic: %v vs %v", a, b)
	}
}

func TestBCaRejectsBadInputs(t *testing.T) {
	if _, err := BCa(nil, BCaOptions{Level: 0.95}); !errors.Is(err, ErrEmptySample) {
		t.Errorf("expected ErrEmptySample, got %v", err)
	}
	if _, err := BCa([]float64{1, 2, 3}, BCaOptions{Level: 0}); !errors.Is(err, ErrInvalidLevel) {
		t.Errorf("expected ErrInvalidLevel for 0, got %v", err)
	}
	if _, err := BCa([]float64{1, 2, 3}, BCaOptions{Level: 1.5}); !errors.Is(err, ErrInvalidLevel) {
		t.Errorf("expected ErrInvalidLevel for 1.5, got %v", err)
	}
}

func TestNormalQuantileBoundaryRoundtrip(t *testing.T) {
	// Φ(Φ⁻¹(0.5)) ≈ 0.5
	for _, p := range []float64{0.1, 0.25, 0.5, 0.75, 0.9, 0.025, 0.975} {
		z := normalQuantile(p)
		back := normalCDF(z)
		if math.Abs(back-p) > 1e-4 {
			t.Errorf("Φ(Φ⁻¹(%v)) = %v, want %v", p, back, p)
		}
	}
}

func TestBCaShiftedMeanCIShifts(t *testing.T) {
	// CI should track sample mean.
	low := []float64{1, 2, 3, 4, 5}
	high := []float64{11, 12, 13, 14, 15}
	ciLow, _ := BCa(low, BCaOptions{Level: 0.95, Replicates: 300, Seed: 1})
	ciHigh, _ := BCa(high, BCaOptions{Level: 0.95, Replicates: 300, Seed: 1})
	if ciHigh.Lower < ciLow.Upper {
		t.Errorf("expected high-sample CI above low-sample CI: low=[%f,%f] high=[%f,%f]",
			ciLow.Lower, ciLow.Upper, ciHigh.Lower, ciHigh.Upper)
	}
}

func TestMean(t *testing.T) {
	if Mean(nil) != 0 {
		t.Errorf("Mean(nil) = %f, want 0", Mean(nil))
	}
	if Mean([]float64{1, 2, 3}) != 2 {
		t.Errorf("Mean([1,2,3]) = %f, want 2", Mean([]float64{1, 2, 3}))
	}
}
