package contextstore

import (
	"context"
	"testing"
)

// or-6z3: re-running build_service for the same task must not append duplicate
// proof/artifact/delivery/escalation rows. Each record Create is idempotent on its
// natural content, so a second identical run returns the existing id.
func TestRecordCreatesAreIdempotentOnRerun(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	var taskID, epicID, projID string
	if err := store.WithTx(ctx, func(tx *Tx) error {
		pid, e := tx.Projects().Create(ctx, "demo", "build a time service", "http-service")
		if e != nil {
			return e
		}
		projID = pid
		sid, e := tx.Specs().CreateDraft(ctx, pid)
		if e != nil {
			return e
		}
		eid, e := tx.Epics().Create(ctx, pid, sid, "epic", "")
		if e != nil {
			return e
		}
		epicID = eid
		taskID, e = tx.Tasks().Create(ctx, eid, "task", "internal/")
		return e
	}); err != nil {
		t.Fatal(err)
	}

	// One "build run" records a proof, an artifact, a delivery and an escalation.
	run := func() (p, a, d, s string) {
		if err := store.WithTx(ctx, func(tx *Tx) error {
			var e error
			if p, e = tx.Proofs().Create(ctx, taskID, Proof{Mode: "behavioral", Verdict: "Accept", Detail: `{"k":1}`, RunCount: 3}); e != nil {
				return e
			}
			if a, e = tx.Artifacts().Create(ctx, taskID, "code", "/x/main.go", "hashAAA"); e != nil {
				return e
			}
			if d, e = tx.Deliveries().Create(ctx, epicID, `{"env":1}`, `{}`); e != nil {
				return e
			}
			s, e = tx.Escalations().Create(ctx, projID, taskID, "rejected")
			return e
		}); err != nil {
			t.Fatal(err)
		}
		return
	}

	p1, a1, d1, s1 := run()
	p2, a2, d2, s2 := run() // the idempotent re-run

	if p1 != p2 {
		t.Errorf("proof re-create returned a new id %q != %q", p1, p2)
	}
	if a1 != a2 {
		t.Errorf("artifact re-create returned a new id %q != %q", a1, a2)
	}
	if d1 != d2 {
		t.Errorf("delivery re-create returned a new id %q != %q", d1, d2)
	}
	if s1 != s2 {
		t.Errorf("escalation re-create returned a new id %q != %q", s1, s2)
	}

	// Row counts are stable at one each — no duplicate append.
	if err := store.WithTx(ctx, func(tx *Tx) error {
		if n, e := tx.Proofs().CountByTask(ctx, taskID); e != nil {
			return e
		} else if n != 1 {
			t.Errorf("proofs count = %d, want 1", n)
		}
		if n, e := tx.Escalations().CountForProject(ctx, projID); e != nil {
			return e
		} else if n != 1 {
			t.Errorf("escalations count = %d, want 1", n)
		}
		arts, e := tx.Artifacts().ListByTask(ctx, taskID)
		if e != nil {
			return e
		}
		if len(arts) != 1 {
			t.Errorf("artifacts count = %d, want 1", len(arts))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// A genuinely DIFFERENT artifact (new hash) is still recorded — dedup must not
	// over-collapse a changed build.
	if err := store.WithTx(ctx, func(tx *Tx) error {
		if _, e := tx.Artifacts().Create(ctx, taskID, "code", "/x/main.go", "hashBBB"); e != nil {
			return e
		}
		arts, e := tx.Artifacts().ListByTask(ctx, taskID)
		if e != nil {
			return e
		}
		if len(arts) != 2 {
			t.Errorf("a changed artifact should be recorded: count = %d, want 2", len(arts))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
