package contextstore

import (
	"context"
	"testing"
)

// or-g2qf.1: the scope-creep waiver lookup — resolved escalations matching a
// reason prefix, project-scoped. Open rows and other projects' rows are
// excluded; distinct fingerprint-suffixed reasons all match the prefix.
func TestEscalationResolvedByReason(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	var projA, projB string
	var waivedID string
	if err := store.WithTx(ctx, func(tx *Tx) error {
		var e error
		if projA, e = tx.Projects().Create(ctx, "a", "intent a", "http-service"); e != nil {
			return e
		}
		if projB, e = tx.Projects().Create(ctx, "b", "intent b", "http-service"); e != nil {
			return e
		}
		if waivedID, e = tx.Escalations().CreateDetailed(ctx, projA, "", "scope creep (built not in spec) [abc123]", "UNTRACED:\nroute /admin"); e != nil {
			return e
		}
		if e = tx.Escalations().Resolve(ctx, waivedID, "waived: intentional admin surface"); e != nil {
			return e
		}
		// Still-open row with a different fingerprint: not a waiver.
		if _, e = tx.Escalations().CreateDetailed(ctx, projA, "", "scope creep (built not in spec) [def456]", "UNTRACED:\nCheckAccess"); e != nil {
			return e
		}
		// Another project's resolved row: out of scope.
		otherID, e := tx.Escalations().CreateDetailed(ctx, projB, "", "scope creep (built not in spec) [abc123]", "UNTRACED:\nroute /admin")
		if e != nil {
			return e
		}
		return tx.Escalations().Resolve(ctx, otherID, "waived")
	}); err != nil {
		t.Fatal(err)
	}

	var got []Escalation
	if err := store.WithTx(ctx, func(tx *Tx) error {
		var e error
		got, e = tx.Escalations().ResolvedByReason(ctx, projA, "scope creep (built not in spec)")
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != waivedID {
		t.Fatalf("want exactly the resolved scope-creep row for project A, got %+v", got)
	}
	if got[0].Resolution == "" || !got[0].Resolved {
		t.Fatalf("the row must carry its resolution: %+v", got[0])
	}
}
