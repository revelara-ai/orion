package behavioral

import (
	"strings"
	"testing"
)

// The status-500 mutant targets the DECLARED entry symbol's handler signature,
// not a hardwired handleTime — so the mutation gate generalizes with the contract.
func TestMutantsForTargetsDeclaredEntrySymbol(t *testing.T) {
	ms := mutantsFor("handleRequest")
	var status500 *mutant
	for i := range ms {
		if ms[i].name == "status-500" {
			status500 = &ms[i]
		}
	}
	if status500 == nil {
		t.Fatal("no status-500 mutant produced")
	}
	if !strings.Contains(status500.old, "func handleRequest(w http.ResponseWriter, r *http.Request) {") {
		t.Fatalf("status-500 mutant does not target the declared symbol: old=%q", status500.old)
	}
	if strings.Contains(status500.old, "handleTime") {
		t.Fatalf("status-500 mutant still references handleTime: old=%q", status500.old)
	}
	if !strings.Contains(status500.new, "w.WriteHeader(500)") {
		t.Fatalf("status-500 mutant does not inject the 500: new=%q", status500.new)
	}
}

// Default symbol keeps the historical handleTime target (backward compatible).
func TestMutantsForDefaultSymbol(t *testing.T) {
	ms := mutantsFor("handleTime")
	found := false
	for _, m := range ms {
		if m.name == "status-500" && strings.Contains(m.old, "func handleTime(") {
			found = true
		}
	}
	if !found {
		t.Fatal("default handleTime status-500 mutant missing")
	}
}
