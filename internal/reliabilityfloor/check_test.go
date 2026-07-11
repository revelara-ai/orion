package reliabilityfloor

import "testing"

func TestAttachChecksMapsTimeout(t *testing.T) {
	out := AttachChecks([]Signal{{ID: "RC-1", Title: "Outbound HTTP call without timeout"}})
	if out[0].Check.Kind != CheckGolangciLint {
		t.Fatalf("kind=%v want golangci-lint", out[0].Check.Kind)
	}
	if !contains(out[0].Check.Linters, "noctx") {
		t.Fatalf("linters=%v want noctx", out[0].Check.Linters)
	}
}

func TestAttachChecksUnmatchedIsAdvisory(t *testing.T) {
	out := AttachChecks([]Signal{{ID: "R-9", Title: "Establish an on-call rotation"}})
	if out[0].Check.Kind != CheckNone {
		t.Fatalf("kind=%v want none", out[0].Check.Kind)
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
