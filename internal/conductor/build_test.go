package conductor

import (
	"context"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator"
)

// answerCanonicalTimeService submits + answers the blocking functional decisions
// and ratifies — leaving an accepted, anchored spec ready to build.
func ratifiedTimeService(t *testing.T) (*orchestrator.Conductor, context.Context) {
	t.Helper()
	oc := orchestrator.NewWithStore(openStore(t))
	ctx := context.Background()
	if _, err := oc.Submit(ctx, "Build an HTTP service that returns the current time."); err != nil {
		t.Fatalf("submit: %v", err)
	}
	for _, a := range [][2]string{{"response_format", "json"}, {"timezone", "UTC"}, {"port", "8080"}, {"route", "/time"}} {
		if err := oc.RecordAnswer(ctx, a[0], a[1]); err != nil {
			t.Fatalf("answer %s: %v", a[0], err)
		}
	}
	if _, err := oc.ApproveSpec(ctx); err != nil {
		t.Fatalf("approve: %v", err)
	}
	return oc, ctx
}

// TestBuildAndProveFixture: the one-shot pipeline builds the canonical
// time-service from the ratified spec and PROVES it green end-to-end (decompose →
// fixture generate → behavioral+empirical+hazard proof → gate → bar). This is the
// "build to the spec" guarantee the user asked for.
func TestBuildAndProveFixture(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles + runs a service + proof; skipped in -short")
	}
	oc, ctx := ratifiedTimeService(t)

	res, err := BuildAndProve(ctx, oc.Store(), nil, nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if res.TaskID == "" || res.Verdict == "" || res.Delivery == "" {
		t.Fatalf("pipeline did not run to completion: %+v", res)
	}
	if res.Verdict != "Accept" || !res.Closed {
		t.Fatalf("canonical fixture must prove green and close the task: %+v", res)
	}
}
