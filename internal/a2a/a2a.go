// Package a2a is Orion's in-process agent-to-agent protocol and bus. It carries
// a Request (Intent + constraints + a read-only ProofObligation) to a
// generation-domain specialist and returns an untrusted EvidenceClaim.
//
// Trust-domain invariant (manifesto: no agent grades its own homework): the
// ProofObligation travels Conductor→agent read-only; the EvidenceClaim travels
// agent→Conductor and is ALWAYS untrusted — it is recorded as a claim and is
// NEVER an input to a Verdict (which the proof domain computes independently from
// its own evidence). The payload never carries proof inputs (held-out tests,
// hidden corpus).
package a2a

import (
	"encoding/json"
	"fmt"
)

// ProofObligation states what a task owes as proof. It is harness-owned and
// read-only to the agent — the agent may read it to know what it must satisfy,
// but cannot author or mutate it.
type ProofObligation struct {
	TaskID  string   `json:"task_id"`
	Clauses []string `json:"clauses"`
}

// Intent is the work description handed to a specialist, plus constraints.
type Intent struct {
	Summary     string            `json:"summary"`
	Constraints map[string]string `json:"constraints,omitempty"`
}

// Request is the immutable payload Conductor→agent. CorrelationID ties the
// request to its claim for tracing.
type Request struct {
	CorrelationID string          `json:"correlation_id"`
	Role          string          `json:"role"`
	Intent        Intent          `json:"intent"`
	Obligation    ProofObligation `json:"obligation"`
}

// EvidenceClaim is the agent's untrusted self-report. AssertionStatus is what the
// agent claims; Trusted is hardwired false to make the trust boundary explicit
// in the type system — nothing in the proof path may read this as a verdict.
type EvidenceClaim struct {
	CorrelationID   string        `json:"correlation_id"`
	TaskID          string        `json:"task_id"`
	Role            string        `json:"role"`
	AssertionStatus string        `json:"assertion_status"`
	ArtifactRefs    []ArtifactRef `json:"artifact_refs,omitempty"`
}

// ArtifactRef points at an artifact the agent produced (path + content hash).
type ArtifactRef struct {
	Type        string `json:"type"`
	StoragePath string `json:"storage_path"`
	ContentHash string `json:"content_hash"`
}

// Trusted always reports false: an EvidenceClaim is never trusted as a verdict.
func (EvidenceClaim) Trusted() bool { return false }

// MarshalRequest / UnmarshalRequest round-trip the immutable payload.
func MarshalRequest(r Request) ([]byte, error) { return json.Marshal(r) }
func UnmarshalRequest(b []byte) (Request, error) {
	var r Request
	err := json.Unmarshal(b, &r)
	return r, err
}
func MarshalClaim(c EvidenceClaim) ([]byte, error) { return json.Marshal(c) }
func UnmarshalClaim(b []byte) (EvidenceClaim, error) {
	var c EvidenceClaim
	err := json.Unmarshal(b, &c)
	return c, err
}

// Handler is a generation-domain specialist that answers a Request with a claim.
type Handler interface {
	Handle(req Request) (EvidenceClaim, error)
}

// HandlerFunc adapts a function to a Handler.
type HandlerFunc func(Request) (EvidenceClaim, error)

// Handle calls f.
func (f HandlerFunc) Handle(req Request) (EvidenceClaim, error) { return f(req) }

// Bus routes a Request to the handler registered for its role, in-process.
type Bus struct {
	handlers map[string]Handler
}

// NewBus returns an empty in-process bus.
func NewBus() *Bus { return &Bus{handlers: map[string]Handler{}} }

// Register binds a handler to a role.
func (b *Bus) Register(role string, h Handler) { b.handlers[role] = h }

// Send routes the request to the role's handler and returns its claim. The
// returned claim's CorrelationID/TaskID are stamped from the request so a
// handler cannot forge a different correlation.
func (b *Bus) Send(req Request) (EvidenceClaim, error) {
	h, ok := b.handlers[req.Role]
	if !ok {
		return EvidenceClaim{}, fmt.Errorf("a2a: no handler for role %q", req.Role)
	}
	claim, err := h.Handle(req)
	if err != nil {
		return EvidenceClaim{}, err
	}
	claim.CorrelationID = req.CorrelationID
	claim.TaskID = req.Obligation.TaskID
	claim.Role = req.Role
	return claim, nil
}
