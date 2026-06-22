package orchestrator

import "testing"

// or-3ba.2: the conductor chooses the project type from the intent. A CLI intent does
// not surface HTTP functional decisions (route/port/response_format); an HTTP intent
// stays http-service (the default).
func TestSubmitInfersProjectType(t *testing.T) {
	t.Run("cli intent → cli, no HTTP decisions", func(t *testing.T) {
		c, ctx := storeConductor(t)
		const intent = "Build a CLI tool that prints the current time"
		if _, err := c.Submit(ctx, intent); err != nil {
			t.Fatalf("submit: %v", err)
		}
		if pt := c.gate.ProjectType(); pt != "cli" {
			t.Fatalf("inferred project type = %q, want cli", pt)
		}
		for _, od := range c.gate.Analyze(intent, nil) {
			switch od.Key {
			case "route", "port", "response_format", "timezone":
				t.Fatalf("a CLI intent must not surface the HTTP functional decision %q", od.Key)
			}
		}
	})

	t.Run("http intent → http-service default", func(t *testing.T) {
		c, ctx := storeConductor(t)
		if _, err := c.Submit(ctx, "Build an HTTP service that returns the current time"); err != nil {
			t.Fatalf("submit: %v", err)
		}
		if pt := c.gate.ProjectType(); pt != "http-service" {
			t.Fatalf("inferred project type = %q, want http-service", pt)
		}
	})
}
