package reliabilityfloor

import (
	"context"
	"testing"
)

func TestParseSeverity(t *testing.T) {
	cases := map[string]Severity{
		"critical": SevCritical, "CRITICAL": SevCritical,
		"high": SevHigh, "medium": SevMedium, "low": SevLow,
		"": SevLow, "garbage": SevLow,
	}
	for in, want := range cases {
		if got := ParseSeverity(in); got != want {
			t.Errorf("ParseSeverity(%q)=%v want %v", in, got, want)
		}
	}
}

func TestFakeSourceReturnsConfigured(t *testing.T) {
	want := []Signal{{ID: "RC-1", Title: "t", Severity: SevHigh}}
	src := &FakeSource{Signals: want}
	got, err := src.Fetch(context.Background(), "proj", "q")
	if err != nil || len(got) != 1 || got[0].ID != "RC-1" {
		t.Fatalf("Fetch=%v,%v want RC-1", got, err)
	}
}
