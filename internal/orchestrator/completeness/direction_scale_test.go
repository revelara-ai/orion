package completeness

import "testing"

func hasKey(checklist []RequiredDecision, key string) bool {
	for _, rd := range checklist {
		if rd.Key == key {
			return true
		}
	}
	return false
}

// TestDirectionRidesLargeAndUnregistered (or-hn15.4 DONE-WHEN a+e): the
// direction family rides the checklist for EVERY large intake AND every
// unregistered-type intake (a game, where stack/language must be elicited) —
// but a standard http-service stays byte-identical (no direction), preserving
// legacy anchors.
func TestDirectionRidesLargeAndUnregistered(t *testing.T) {
	cases := []struct {
		ptype string
		scale string
		want  bool
	}{
		{"http-service", ScaleLarge, true},     // large: always
		{"http-service", ScaleStandard, false}, // the ONLY registered type: standard stays byte-identical, NO direction
		{"game", ScaleStandard, true},          // unregistered standard: elicit direction
		{"game", ScaleLarge, true},             // unregistered large: direction
		{"cli", ScaleStandard, true},           // unregistered standard: elicit direction (no functional template to presume the stack)
	}
	for _, c := range cases {
		a := NewAnalyzerScaled(c.ptype, c.scale)
		got := hasKey(a.Checklist(), "direction.language")
		if got != c.want {
			t.Errorf("NewAnalyzerScaled(%q,%q): direction.language present=%v, want %v", c.ptype, c.scale, got, c.want)
		}
		if a.Scale() != c.scale {
			t.Errorf("NewAnalyzerScaled(%q,%q): Scale()=%q, want %q", c.ptype, c.scale, a.Scale(), c.scale)
		}
	}
	// The unscaled V2 constructor stays direction-free and standard-scaled.
	base := NewAnalyzer("http-service")
	if hasKey(base.Checklist(), "direction.language") {
		t.Fatal("NewAnalyzer must stay byte-compatible with V2 (no direction)")
	}
	if base.Scale() != ScaleStandard {
		t.Fatalf("NewAnalyzer scale = %q, want standard", base.Scale())
	}
}
