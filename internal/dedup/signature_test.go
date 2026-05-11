package dedup

import "testing"

// Same call from two different file paths must produce the same
// signature — this is the file-rename invariance that lets dedup
// survive refactors.
func TestSignature_FileRenameInvariance(t *testing.T) {
	a := Canonicalize("internal/svc/a.go", "DoThing", "")
	b := Canonicalize("internal/svc/sub/foo.go", "DoThing", "")
	if Signature("missing_timeout", a) != Signature("missing_timeout", b) {
		t.Errorf("file rename changed the signature: %s vs %s",
			Signature("missing_timeout", a), Signature("missing_timeout", b))
	}
}

// A method on a receiver must produce a different signature from a
// free function with the same name (otherwise we'd dedup unrelated
// callsites).
func TestSignature_ReceiverDistinguishes(t *testing.T) {
	free := Canonicalize("svc.go", "Run", "")
	method := Canonicalize("svc.go", "Run", "Server")
	if Signature("p", free) == Signature("p", method) {
		t.Error("receiver should change signature")
	}
}

// Different patterns on the same callsite produce different
// signatures (the pattern is part of the key so a callsite suppressed
// for one pattern is not suppressed for another).
func TestSignature_PatternDistinguishes(t *testing.T) {
	cs := Canonicalize("svc.go", "Run", "")
	if Signature("missing_timeout", cs) == Signature("rate_limit_inference", cs) {
		t.Error("pattern should change signature")
	}
}

// Same callsite + same pattern are reproducible across runs (sha256
// is deterministic; this test is the contract).
func TestSignature_Stable(t *testing.T) {
	cs := Canonicalize("svc.go", "Run", "")
	first := Signature("missing_timeout", cs)
	second := Signature("missing_timeout", cs)
	if first != second {
		t.Errorf("not deterministic: %s != %s", first, second)
	}
	if len(first) != 64 {
		t.Errorf("expected hex SHA256 (64 chars), got %d", len(first))
	}
}

// Empty pattern OR empty function name produces empty string —
// callers treat this as "not determinable" and skip stamping.
func TestSignature_EmptyInputs(t *testing.T) {
	if Signature("", Canonicalize("svc.go", "Run", "")) != "" {
		t.Error("empty pattern should yield empty signature")
	}
	if Signature("p", Canonicalize("svc.go", "", "")) != "" {
		t.Error("empty function should yield empty signature")
	}
}
