package completeness

import (
	"reflect"
	"testing"
)

const canonicalIntent = "Build an HTTP service that returns the current time."

// TestProjectTypeGatesFunctionalDecisions: projectType is LIVE, not a dead parameter.
// An http-service is grilled on its HTTP functional decisions PLUS the universal
// reliability dimensions; an UNREGISTERED type (a worker) is grilled ONLY on the
// universal dimensions — it is never asked route/port/timezone it has no concept of.
func TestProjectTypeGatesFunctionalDecisions(t *testing.T) {
	keysOf := func(ds []RequiredDecision) map[string]bool {
		m := map[string]bool{}
		for _, d := range ds {
			m[d.Key] = true
		}
		return m
	}
	http := keysOf(NewAnalyzer("http-service").Checklist())
	for _, k := range []string{"response_format", "route", "port", "timezone", "scale_profile", "security_model"} {
		if !http[k] {
			t.Fatalf("http-service checklist missing %q", k)
		}
	}
	if RegisteredProjectType("worker") {
		t.Fatal("worker is not a registered type")
	}
	worker := keysOf(NewAnalyzer("worker").Checklist())
	for _, h := range []string{"response_format", "route", "port", "timezone"} {
		if worker[h] {
			t.Fatalf("a non-HTTP (worker) project must NOT be grilled on the HTTP decision %q", h)
		}
	}
	for _, u := range []string{"scale_profile", "security_model", "slo_targets", "observability_signals"} {
		if !worker[u] {
			t.Fatalf("a worker project must still resolve the universal dimension %q", u)
		}
	}
}

// allAnswers returns answers resolving every required decision for a project type.
func allAnswers(a *Analyzer) map[string]string {
	m := map[string]string{}
	for _, rd := range a.Checklist() {
		m[rd.Key] = "answered"
	}
	return m
}

// without returns a copy of m with every key belonging to dim removed.
func without(a *Analyzer, m map[string]string, dim Dimension) map[string]string {
	out := map[string]string{}
	for k, v := range m {
		out[k] = v
	}
	for _, rd := range a.Checklist() {
		if rd.Dimension == dim {
			delete(out, rd.Key)
		}
	}
	return out
}

func hasDimension(ods []OpenDecision, dim Dimension) bool {
	for _, od := range ods {
		if od.Dimension == dim {
			return true
		}
	}
	return false
}

func keys(ods []OpenDecision) []string {
	out := make([]string, len(ods))
	for i, od := range ods {
		out[i] = od.Key
	}
	return out
}

// assertDimensionGates: when a dimension is unresolved it raises an OpenDecision;
// when resolved (everything answered) it raises none.
func assertDimensionGates(t *testing.T, dim Dimension) {
	t.Helper()
	a := NewAnalyzer("http-service")
	missing := without(a, allAnswers(a), dim)

	ods := a.Analyze(canonicalIntent, missing)
	if !hasDimension(ods, dim) {
		t.Fatalf("dimension %q unresolved but no OpenDecision raised; got keys %v", dim, keys(ods))
	}
	for _, od := range ods {
		if od.Dimension != dim {
			t.Fatalf("only %q should be open, but %q (%q) is open too", dim, od.Key, od.Dimension)
		}
	}
	if rem := a.Analyze(canonicalIntent, allAnswers(a)); len(rem) != 0 {
		t.Fatalf("all answered but %v still open", keys(rem))
	}
}

func TestMissingscaleDimensionRaisesOpenDecision(t *testing.T) { assertDimensionGates(t, DimScale) }
func TestMissingobservabilityDimensionRaisesOpenDecision(t *testing.T) {
	assertDimensionGates(t, DimObservability)
}
func TestMissingoncallDimensionRaisesOpenDecision(t *testing.T) { assertDimensionGates(t, DimOnCall) }
func TestMissingdataDimensionRaisesOpenDecision(t *testing.T)   { assertDimensionGates(t, DimData) }
func TestMissingsloDimensionRaisesOpenDecision(t *testing.T)    { assertDimensionGates(t, DimSLO) }
func TestMissingsecurityDimensionRaisesOpenDecision(t *testing.T) {
	assertDimensionGates(t, DimSecurity)
}
func TestMissingdepsDimensionRaisesOpenDecision(t *testing.T) {
	assertDimensionGates(t, DimDependencies)
}

// TestRequiredDecisionsChecklist: the checklist is deterministic (no live LLM),
// stable across calls, and covers all eight spec dimensions.
func TestRequiredDecisionsChecklist(t *testing.T) {
	a := NewAnalyzer("http-service")
	c1 := a.Checklist()
	c2 := a.Checklist()
	if !reflect.DeepEqual(c1, c2) {
		t.Fatal("checklist is not deterministic across calls")
	}
	if len(c1) == 0 {
		t.Fatal("empty checklist")
	}
	want := map[Dimension]bool{
		DimFunctional: false, DimScale: false, DimObservability: false, DimOnCall: false,
		DimData: false, DimSLO: false, DimSecurity: false, DimDependencies: false,
	}
	seenKey := map[string]bool{}
	for _, rd := range c1 {
		if rd.Key == "" || rd.Question == "" {
			t.Fatalf("required decision missing key/question: %+v", rd)
		}
		if seenKey[rd.Key] {
			t.Fatalf("duplicate decision key %q", rd.Key)
		}
		seenKey[rd.Key] = true
		if _, ok := want[rd.Dimension]; !ok {
			t.Fatalf("unknown dimension %q", rd.Dimension)
		}
		want[rd.Dimension] = true
	}
	for dim, covered := range want {
		if !covered {
			t.Fatalf("dimension %q not covered by the checklist", dim)
		}
	}
}

// TestAmbiguousIntentSurfacesOpenDecisions: the canonical ambiguous intent
// surfaces (at minimum) response_format, timezone, port, route — and does not
// silently guess them.
func TestAmbiguousIntentSurfacesOpenDecisions(t *testing.T) {
	a := NewAnalyzer("http-service")
	ods := a.Analyze(canonicalIntent, nil)
	got := map[string]bool{}
	for _, k := range keys(ods) {
		got[k] = true
	}
	for _, want := range []string{"response_format", "timezone", "port", "route"} {
		if !got[want] {
			t.Fatalf("ambiguous intent did not surface %q; got %v", want, keys(ods))
		}
	}
}

// TestFullyAnsweredIntentYieldsZeroOpenDecisions.
func TestFullyAnsweredIntentYieldsZeroOpenDecisions(t *testing.T) {
	a := NewAnalyzer("http-service")
	if ods := a.Analyze(canonicalIntent, allAnswers(a)); len(ods) != 0 {
		t.Fatalf("fully answered but open: %v", keys(ods))
	}
}

// TestExplicitIntentResolvesWithoutGuessing: when the intent explicitly states a
// value (e.g. "on port 9090"), that decision is resolved deterministically; an
// unstated one stays open.
func TestExplicitIntentResolvesWithoutGuessing(t *testing.T) {
	a := NewAnalyzer("http-service")
	ods := a.Analyze("Build an HTTP service on port 9090 returning JSON.", nil)
	k := map[string]bool{}
	for _, key := range keys(ods) {
		k[key] = true
	}
	if k["port"] {
		t.Fatal("port was explicitly stated (9090) but still open")
	}
	if k["response_format"] {
		t.Fatal("response_format was explicitly stated (JSON) but still open")
	}
	if !k["timezone"] {
		t.Fatal("timezone was not stated and must remain open (no silent guess)")
	}
}

// TestScaleFallbackPresetProducesConcreteThreshold: a fallback preset resolves to
// a concrete numeric threshold (PRD: scale fallback presets low/medium/high).
func TestScaleFallbackPresetProducesConcreteThreshold(t *testing.T) {
	th, ok := ResolveScalePreset("medium")
	if !ok {
		t.Fatal("medium preset not recognized")
	}
	if th.RequestsPerWindow <= 0 || th.Window == "" {
		t.Fatalf("preset did not produce a concrete threshold: %+v", th)
	}
	if _, ok := ResolveScalePreset("nonsense"); ok {
		t.Fatal("unknown preset should not resolve")
	}
}
