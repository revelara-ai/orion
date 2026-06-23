package sandbox

import "testing"

// or-3ba.3: the no-LLM time-service fixture REJECTS an incomplete GenSpec instead
// of silently defaulting Module/Route/Port — a malformed/non-HTTP contract must not
// be papered over into a canonical time service.
func TestGenerateTimeServiceFixtureRejectsIncompleteGenSpec(t *testing.T) {
	full := GenSpec{Module: "orion-generated/svc", Route: "/time", Port: 8080, Format: "json", TimeZone: "UTC"}

	cases := []struct {
		name string
		gs   GenSpec
	}{
		{"empty Module", func() GenSpec { g := full; g.Module = ""; return g }()},
		{"empty Route", func() GenSpec { g := full; g.Route = ""; return g }()},
		{"zero Port", func() GenSpec { g := full; g.Port = 0; return g }()},
	}
	for _, tc := range cases {
		if _, err := GenerateTimeServiceFixture(t.TempDir(), tc.gs); err == nil {
			t.Errorf("%s: expected an error for an incomplete GenSpec, got nil", tc.name)
		}
	}

	// A complete GenSpec still succeeds (TimeZone may default).
	noTZ := full
	noTZ.TimeZone = ""
	if _, err := GenerateTimeServiceFixture(t.TempDir(), noTZ); err != nil {
		t.Fatalf("complete GenSpec (TimeZone defaulting) should succeed: %v", err)
	}
}
