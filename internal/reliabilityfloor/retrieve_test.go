package reliabilityfloor

import (
	"context"
	"errors"
	"testing"
)

func TestRetrieveFailsOpen(t *testing.T) {
	got := Retrieve(context.Background(), &FakeSource{Err: errors.New("no creds")}, "p", "add http client", 3)
	if got != nil {
		t.Fatalf("want nil on source error, got %v", got)
	}
}

func TestRetrieveDedupesSortsCapsAttaches(t *testing.T) {
	src := &FakeSource{Signals: []Signal{
		{ID: "RC-1", Title: "Outbound HTTP without timeout", Severity: SevHigh},
		{ID: "RC-1", Title: "dup", Severity: SevHigh},
		{ID: "R-2", Title: "on-call rotation", Severity: SevCritical},
		{ID: "R-3", Title: "low thing", Severity: SevLow},
	}}
	got := Retrieve(context.Background(), src, "p", "intent", 2)
	if len(got) != 2 {
		t.Fatalf("len=%d want 2 (capped)", len(got))
	}
	if got[0].ID != "R-2" {
		t.Fatalf("first=%s want R-2 (highest severity)", got[0].ID)
	}
	// checks attached: the timeout signal is mechanizable
	var found bool
	for _, s := range got {
		if s.ID == "RC-1" && s.Check.Kind == CheckGolangciLint {
			found = true
		}
	}
	if !found {
		t.Fatal("RC-1 should have golangci-lint check attached")
	}
}
