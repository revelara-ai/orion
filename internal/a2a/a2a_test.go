package a2a

import (
	"reflect"
	"testing"
)

// TestPayloadRoundTrip: a Request (Intent + read-only ProofObligation) survives
// marshal/unmarshal unchanged, and so does an EvidenceClaim.
func TestPayloadRoundTrip(t *testing.T) {
	req := Request{
		CorrelationID: "corr-1",
		Role:          "go-generator",
		Intent:        Intent{Summary: "build a time service", Constraints: map[string]string{"port": "8080"}},
		Obligation:    ProofObligation{TaskID: "task-1", Clauses: []string{"returns current time", "listens on port"}},
	}
	b, err := MarshalRequest(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := UnmarshalRequest(b)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(req, got) {
		t.Fatalf("round-trip mismatch:\n have %+v\n want %+v", got, req)
	}

	claim := EvidenceClaim{
		CorrelationID:   "corr-1",
		TaskID:          "task-1",
		Role:            "go-generator",
		AssertionStatus: "implemented",
		ArtifactRefs:    []ArtifactRef{{Type: "code", StoragePath: "/p/main.go", ContentHash: "h"}},
	}
	cb, err := MarshalClaim(claim)
	if err != nil {
		t.Fatalf("marshal claim: %v", err)
	}
	gotClaim, err := UnmarshalClaim(cb)
	if err != nil {
		t.Fatalf("unmarshal claim: %v", err)
	}
	if !reflect.DeepEqual(claim, gotClaim) {
		t.Fatalf("claim round-trip mismatch:\n have %+v\n want %+v", gotClaim, claim)
	}
}

// TestEvidenceClaimIsNeverTrusted: the type system marks an EvidenceClaim as
// untrusted — it can never be read as a verdict.
func TestEvidenceClaimIsNeverTrusted(t *testing.T) {
	if (EvidenceClaim{AssertionStatus: "implemented"}).Trusted() {
		t.Fatal("EvidenceClaim.Trusted() must always be false")
	}
}

type echoHandler struct{ status string }

func (h echoHandler) Handle(_ Request) (EvidenceClaim, error) {
	// A handler that tries to forge a different correlation/task id.
	return EvidenceClaim{CorrelationID: "FORGED", TaskID: "FORGED", AssertionStatus: h.status}, nil
}

// TestBusStampsCorrelationFromRequest: the bus stamps correlation/task id from
// the request, so a handler cannot forge them.
func TestBusStampsCorrelationFromRequest(t *testing.T) {
	bus := NewBus()
	bus.Register("go-generator", echoHandler{status: "implemented"})
	req := Request{CorrelationID: "corr-9", Role: "go-generator", Obligation: ProofObligation{TaskID: "task-9"}}
	claim, err := bus.Send(req)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if claim.CorrelationID != "corr-9" || claim.TaskID != "task-9" || claim.Role != "go-generator" {
		t.Fatalf("bus did not stamp ids from request: %+v", claim)
	}
	if claim.AssertionStatus != "implemented" {
		t.Fatalf("claim status = %q, want implemented", claim.AssertionStatus)
	}
}

func TestBusUnknownRoleErrors(t *testing.T) {
	bus := NewBus()
	if _, err := bus.Send(Request{Role: "nope"}); err == nil {
		t.Fatal("expected error for unknown role")
	}
}
