package contextstore

import (
	"context"
	"errors"
	"testing"
)

// TestEscalationInboxLifecycle: escalations are an actionable queue, not
// write-only rows — created with a structured payload, listable while open,
// resolvable with a note, and excluded from the inbox once resolved.
func TestEscalationInboxLifecycle(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	var projID, escID string
	if err := store.WithTx(ctx, func(tx *Tx) error {
		var e error
		projID, e = tx.Projects().Create(ctx, "p", "build the thing", "http-service")
		if e != nil {
			return e
		}
		escID, e = tx.Escalations().CreateDetailed(ctx, projID, "", "mutation score 0.4 below tier bar", "behavioral proof: 2 of 5 mutants survived in handler.go")
		return e
	}); err != nil {
		t.Fatal(err)
	}

	// The inbox lists it, payload intact.
	var open []Escalation
	if err := store.WithTx(ctx, func(tx *Tx) error {
		var e error
		open, e = tx.Escalations().ListOpen(ctx)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 || open[0].ID != escID || open[0].Detail == "" {
		t.Fatalf("inbox should list the open escalation with its detail, got %+v", open)
	}

	// Dedup on (project, task, reason) returns the same row and keeps the payload.
	if err := store.WithTx(ctx, func(tx *Tx) error {
		id2, e := tx.Escalations().CreateDetailed(ctx, projID, "", "mutation score 0.4 below tier bar", "different detail on re-run")
		if e != nil {
			return e
		}
		if id2 != escID {
			t.Errorf("re-escalating the same (project,task,reason) must dedup: %s vs %s", id2, escID)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Resolving records the note + timestamp and empties the inbox.
	if err := store.WithTx(ctx, func(tx *Tx) error {
		return tx.Escalations().Resolve(ctx, escID, "raised the mutation corpus; re-running build")
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.WithTx(ctx, func(tx *Tx) error {
		got, e := tx.Escalations().Get(ctx, escID)
		if e != nil {
			return e
		}
		if !got.Resolved || got.Resolution == "" || got.ResolvedAt == "" {
			t.Errorf("resolved escalation must carry note + timestamp: %+v", got)
		}
		remaining, e := tx.Escalations().ListOpen(ctx)
		if e != nil {
			return e
		}
		if len(remaining) != 0 {
			t.Errorf("resolved escalations must leave the inbox, got %+v", remaining)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Resolving an unknown id is a loud error, not a silent no-op.
	if err := store.WithTx(ctx, func(tx *Tx) error {
		return tx.Escalations().Resolve(ctx, "nope", "x")
	}); !errors.Is(err, ErrNotFound) {
		t.Errorf("resolving an unknown escalation must return ErrNotFound, got %v", err)
	}
}

// TestHasOpenForTask: the bar-time escalation pass must not double-file a task
// that already escalated at exhaustion time.
func TestHasOpenForTask(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	var projID, taskID, escID string
	if err := store.WithTx(ctx, func(tx *Tx) error {
		var e error
		projID, e = tx.Projects().Create(ctx, "p", "intent", "http-service")
		if e != nil {
			return e
		}
		specID, e := tx.Specs().CreateDraft(ctx, projID)
		if e != nil {
			return e
		}
		epicID, e := tx.Epics().Create(ctx, projID, specID, "epic", "")
		if e != nil {
			return e
		}
		taskID, e = tx.Tasks().Create(ctx, epicID, "task", "")
		if e != nil {
			return e
		}
		if has, e := tx.Escalations().HasOpenForTask(ctx, projID, taskID); e != nil || has {
			t.Errorf("no escalation yet: want false, got %v/%v", has, e)
		}
		escID, e = tx.Escalations().CreateDetailed(ctx, projID, taskID, "task failed proof after 3 attempt(s)", "analysis")
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.WithTx(ctx, func(tx *Tx) error {
		if has, e := tx.Escalations().HasOpenForTask(ctx, projID, taskID); e != nil || !has {
			t.Errorf("open escalation exists: want true, got %v/%v", has, e)
		}
		if e := tx.Escalations().Resolve(ctx, escID, "fixed"); e != nil {
			return e
		}
		if has, e := tx.Escalations().HasOpenForTask(ctx, projID, taskID); e != nil || has {
			t.Errorf("resolved escalation: want false, got %v/%v", has, e)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
