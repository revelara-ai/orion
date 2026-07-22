package contextwindow

import (
	"context"
	"testing"

	"github.com/revelara-ai/orion/pkg/llm"
)

// TestThresholdsScaleWithWindow: the defensive layers engage at fractions of the
// model window, so the SAME policy governs a 1M Anthropic brain and an 8K local
// brain — just at different absolute token counts.
func TestThresholdsScaleWithWindow(t *testing.T) {
	for _, tc := range []struct {
		window                            int
		wantClear, wantCompact, wantGuard int
	}{
		// or-txyn: ClearAt is min(fraction·window, absolute cost target) — a
		// 1M window caps at 100K instead of re-billing a 400K working set.
		{1_000_000, 100_000, 700_000, 850_000},
		{8_192, 3_276, 5_734, 6_963},
	} {
		p := For(tc.window)
		if p.ClearAt != tc.wantClear || p.CompactAt != tc.wantCompact || p.GuardAt != tc.wantGuard {
			t.Errorf("For(%d) = {clear:%d compact:%d guard:%d}, want {clear:%d compact:%d guard:%d}",
				tc.window, p.ClearAt, p.CompactAt, p.GuardAt, tc.wantClear, tc.wantCompact, tc.wantGuard)
		}
	}
}

// TestThresholdsOrdered: clear < compact < guard < window, always — a cheaper
// lever must engage before a costlier one, and the guard leaves headroom below
// the hard ceiling.
func TestThresholdsOrdered(t *testing.T) {
	for _, w := range []int{8_192, 128_000, 1_000_000} {
		p := For(w)
		if !(p.ClearAt < p.CompactAt && p.CompactAt < p.GuardAt && p.GuardAt < p.Window) {
			t.Errorf("For(%d) thresholds not strictly ordered: %+v", w, p)
		}
	}
}

// windowedProvider reports a small window (a cheap local model).
type windowedProvider struct{ llm.Provider }

func (windowedProvider) ContextWindow() int { return 8_192 }

// TestWindowOfPrefersProviderReport: when a provider advertises its window, use
// it; otherwise fall back to the conservative default so unknown providers are
// still governed.
func TestWindowOfPrefersProviderReport(t *testing.T) {
	if got := WindowOf(windowedProvider{}, DefaultWindow); got != 8_192 {
		t.Fatalf("WindowOf(windowed) = %d, want 8192", got)
	}
	if got := WindowOf(noWindowProvider{}, 55_000); got != 55_000 {
		t.Fatalf("WindowOf(unknown) = %d, want fallback 55000", got)
	}
}

// noWindowProvider satisfies Provider without ContextWindow (local/offline brain).
type noWindowProvider struct{}

func (noWindowProvider) Name() string { return "no-window" }
func (noWindowProvider) Chat(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{}, nil
}
func (noWindowProvider) ChatStream(context.Context, llm.ChatRequest, func(string)) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{}, nil
}
func (noWindowProvider) Models(context.Context) ([]llm.ModelInfo, error) { return nil, nil }
func (noWindowProvider) Ping(context.Context) error                      { return nil }

// or-txyn: ClearAt is capped by an ABSOLUTE cost target, not just a window
// fraction — a 1M-window model otherwise re-bills a ~400K steady-state
// context every loop iteration (observed: 13M cumulative tokens on a small
// change). Overflow thresholds (CompactAt/GuardAt) stay window-relative.
func TestClearAtAbsoluteCostTarget(t *testing.T) {
	if got := For(1_000_000).ClearAt; got != 100_000 {
		t.Fatalf("1M window must cap ClearAt at the 100K default cost target, got %d", got)
	}
	if got := For(128_000).ClearAt; got != int(128_000*ClearFraction) {
		t.Fatalf("a small window keeps the fractional ClearAt, got %d", got)
	}
	t.Setenv("ORION_CONTEXT_TARGET", "50000")
	if got := For(1_000_000).ClearAt; got != 50_000 {
		t.Fatalf("ORION_CONTEXT_TARGET must override the cap, got %d", got)
	}
	t.Setenv("ORION_CONTEXT_TARGET", "junk")
	if got := For(1_000_000).ClearAt; got != 100_000 {
		t.Fatalf("junk env degrades to the default cap, got %d", got)
	}
	if got := For(1_000_000); got.CompactAt != 700_000 || got.GuardAt != 850_000 {
		t.Fatalf("overflow thresholds stay window-relative, got %+v", got)
	}
}
