package completeness

import "testing"

func TestInferProjectType(t *testing.T) {
	cases := []struct{ intent, want string }{
		{"Build an HTTP service that returns the current time", "http-service"},
		{"Build a JSON API on port 8080", "http-service"},
		{"Build a REST endpoint for user lookup", "http-service"},
		{"Build a CLI tool that prints the time", "cli"},
		{"Write a command-line utility", "cli"},
		{"Build a reusable Go library for date parsing", "library"},
		{"Create an SDK for our service", "library"}, // library signal outranks the http 'service' word
		{"Build a background worker that processes jobs", "worker"},
		{"Run a cron job nightly", "worker"},
		// or-045a.1 (dogfood 413691a4): NO explicit signal → UNCLASSIFIED, never a
		// silent http-service. The old default sent a GAME intent down the
		// http-ops checklist (response_format/port/route).
		{"Build a thing", Unclassified},
		{"I'd like to build a game that is like Arc Raiders, but completely PvE instead of a mix between PvE and PvP. I think the most interesting thing about the enemies is that they behave like real AI driven mechs, with real reinforcement learning walking and movement characteristics. I expect this to be a large project.", Unclassified},
	}
	for _, c := range cases {
		if got := InferProjectType(c.intent); got != c.want {
			t.Errorf("InferProjectType(%q) = %q, want %q", c.intent, got, c.want)
		}
	}
}

// An unclassified project is grilled ONLY on the universal reliability
// dimensions — never on HTTP specifics it may not have (or-045a.1).
func TestUnclassifiedGetsOnlyUniversalDecisions(t *testing.T) {
	a := NewAnalyzer(Unclassified)
	for _, d := range a.Checklist() {
		switch d.Key {
		case "response_format", "port", "route":
			t.Errorf("unclassified must not raise the HTTP question %q", d.Key)
		}
	}
	if len(a.Checklist()) == 0 {
		t.Fatal("unclassified still gets the universal reliability dimensions")
	}
}

// ClassifyScale is deterministic and explicit-signal-only: a stated
// large-project signal yields "large"; everything else stays "standard"
// (the gate never guesses scale either).
func TestClassifyScale(t *testing.T) {
	cases := []struct{ intent, want string }{
		{"I'd like to build a game like Arc Raiders. I expect this to be a large project.", ScaleLarge},
		{"This is a big project spanning several services", ScaleLarge},
		{"Build the entire platform for our startup", ScaleLarge},
		{"a multi-month effort across teams", ScaleLarge},
		// Negative: no signal → standard, even for vague intents.
		{"Build an HTTP service that returns the current time", ScaleStandard},
		{"Build a thing", ScaleStandard},
	}
	for _, c := range cases {
		if got := ClassifyScale(c.intent); got != c.want {
			t.Errorf("ClassifyScale(%q) = %q, want %q", c.intent, got, c.want)
		}
	}
}
