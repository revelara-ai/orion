package orchestrator

import (
	"context"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
)

// TestConductorPersistsIntentSurvivesRestart: a Conductor backed by a Context
// Store persists the submitted intent as a project + draft spec that survive a
// process restart (reopening the same data dir).
func TestConductorPersistsIntentSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	const intent = "Build an HTTP service that returns the current time."

	st, err := contextstore.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	c := NewWithStore(st)
	if _, err := c.Submit(ctx, intent); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Restart: reopen the same dir and confirm the intent + draft spec persisted.
	st2, err := contextstore.Open(dir)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer st2.Close()

	projects, err := st2.ListProjects(ctx)
	if err != nil {
		t.Fatalf("list projects: %v", err)
	}
	if len(projects) != 1 || projects[0].Intent != intent {
		t.Fatalf("projects after restart = %+v, want one with intent %q", projects, intent)
	}
	specs, err := st2.SpecsForProject(ctx, projects[0].ID)
	if err != nil || len(specs) != 1 || specs[0].Status != "drafting" {
		t.Fatalf("specs after restart = %+v err=%v, want one drafting spec", specs, err)
	}
}

// TestConductorWithoutStoreStillWorks: the in-memory Conductor (no store) keeps
// its original behavior — Submit succeeds and Status reflects the intent.
func TestConductorWithoutStoreStillWorks(t *testing.T) {
	c := New()
	if _, err := c.Submit(context.Background(), "build a thing"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if c.Status().Intent != "build a thing" {
		t.Fatalf("status intent = %q", c.Status().Intent)
	}
}
