package completeness

import (
	"strings"
	"testing"
)

// TestDirectionDecisionsGatedByScale (or-045a.5): a LARGE-scale intake raises
// the direction family (stack/language/engine/wire-protocol/repo-layout) as
// first-class decisions; a standard-scale intake does not (no new noise on the
// V2 path), and the unscaled NewAnalyzer stays byte-compatible.
func TestDirectionDecisionsGatedByScale(t *testing.T) {
	keysOf := func(a *Analyzer) map[string]bool {
		m := map[string]bool{}
		for _, d := range a.Checklist() {
			m[d.Key] = true
		}
		return m
	}
	large := keysOf(NewAnalyzerScaled(Unclassified, ScaleLarge))
	for _, k := range []string{"direction.stack", "direction.language", "direction.engine", "direction.wire_protocol", "direction.repo_layout"} {
		if !large[k] {
			t.Errorf("large-scale checklist must raise %s", k)
		}
	}
	std := keysOf(NewAnalyzerScaled("http-service", ScaleStandard))
	compat := keysOf(NewAnalyzer("http-service"))
	for _, k := range []string{"direction.stack", "direction.language"} {
		if std[k] {
			t.Errorf("standard-scale checklist must NOT raise %s (V2 noise)", k)
		}
		if compat[k] {
			t.Errorf("NewAnalyzer (unscaled) must stay byte-compatible, raised %s", k)
		}
	}
	// Direction questions carry fallbacks (a standard flow proceeds on approved
	// defaults; the assumption gate audits them).
	for _, d := range NewAnalyzerScaled(Unclassified, ScaleLarge).Checklist() {
		if strings.HasPrefix(d.Key, "direction.") && d.Fallback == "" {
			t.Errorf("%s needs a fallback so the assumption gate can audit it", d.Key)
		}
	}
}

// TestDirectionGaps (or-045a.5): the deterministic capability manifest — an
// out-of-capability direction answer yields a gap naming the value and what
// IS provable; in-capability and unconstrained answers yield none.
func TestDirectionGaps(t *testing.T) {
	gaps := DirectionGaps(map[string]string{"direction.wire_protocol": "grpc"})
	if len(gaps) != 1 || gaps[0].Key != "direction.wire_protocol" || gaps[0].Value != "grpc" {
		t.Fatalf("grpc must gap: %+v", gaps)
	}
	if len(gaps[0].Provable) == 0 {
		t.Fatal("a gap must name what IS provable (the honest alternative)")
	}
	// Case-insensitive.
	if g := DirectionGaps(map[string]string{"direction.language": "Rust"}); len(g) != 1 {
		t.Fatalf("Rust must gap (case-insensitive): %+v", g)
	}
	// Negative: the provable stack yields NO gaps, and unconstrained keys
	// (stack/repo_layout free text) never gap.
	none := DirectionGaps(map[string]string{
		"direction.language":      "go",
		"direction.wire_protocol": "http-json",
		"direction.engine":        "none",
		"direction.stack":         "unreal engine client + go inference service",
		"direction.repo_layout":   "new standalone repo",
		"response_format":         "json", // non-direction keys are ignored
	})
	if len(none) != 0 {
		t.Fatalf("provable directions must not gap: %+v", none)
	}
}
