package orchestrator

import (
	"context"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
)

// or-3ba.5 part 1: the inferred projectType is persisted on the project and a
// conductor reloaded in a LATER session reconstructs the right gate from it —
// instead of reverting to the http-service default. (Submit in session 1;
// Approve/Build in session 2.)
func TestProjectTypePersistsAcrossReloadAndReconstructsGate(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	// Session 1: submit a CLI intent.
	st1, err := contextstore.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	c1 := NewWithStore(st1)
	if _, err := c1.Submit(ctx, "build a CLI tool that prints the current date"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	proj, _, err := st1.CurrentProjectSpec(ctx)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	if proj.ProjectType != "cli" {
		t.Fatalf("persisted ProjectType = %q, want cli", proj.ProjectType)
	}
	_ = st1.Close()

	// Session 2: reopen the store + a FRESH conductor (whose gate defaults to
	// http-service). A load path must reconstruct the CLI gate from the persisted
	// type, NOT revert to http-service.
	st2, err := contextstore.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	c2 := NewWithStore(st2)
	if _, err := c2.SpecView(ctx); err != nil { // a load path → reconstructs the gate
		t.Fatalf("specview: %v", err)
	}
	keys := c2.DecisionKeys()
	for _, httpKey := range []string{"route", "response_format", "port", "timezone"} {
		if keys[httpKey] {
			t.Fatalf("reloaded conductor reverted to the http-service gate (raised %q); gate not reconstructed from the persisted cli type: %v", httpKey, keys)
		}
	}
}
