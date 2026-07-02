package contextstore

import (
	"context"
	"testing"
)

// TestProofMemoRoundTrip (or-v9f.6): a persisted proof is retrievable by its
// (spec, artifact) key and upserts idempotently; unknown keys miss cleanly.
func TestProofMemoRoundTrip(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	if _, ok, err := store.ProofMemoGet(ctx, "spec-1", "art-1"); err != nil || ok {
		t.Fatalf("empty memo must miss cleanly, got ok=%v err=%v", ok, err)
	}
	if err := store.ProofMemoPut(ctx, "spec-1", "art-1", `{"verdict":"Accept"}`); err != nil {
		t.Fatal(err)
	}
	js, ok, err := store.ProofMemoGet(ctx, "spec-1", "art-1")
	if err != nil || !ok || js != `{"verdict":"Accept"}` {
		t.Fatalf("memo round-trip failed: js=%q ok=%v err=%v", js, ok, err)
	}
	// Different spec anchor over the same artifact is a distinct key (verdict is a
	// function of the contract too).
	if _, ok, _ := store.ProofMemoGet(ctx, "spec-2", "art-1"); ok {
		t.Fatal("a different spec anchor must not reuse another spec's memo")
	}
	// Upsert refreshes rather than duplicating.
	if err := store.ProofMemoPut(ctx, "spec-1", "art-1", `{"verdict":"Reject"}`); err != nil {
		t.Fatal(err)
	}
	if js, _, _ := store.ProofMemoGet(ctx, "spec-1", "art-1"); js != `{"verdict":"Reject"}` {
		t.Fatalf("upsert must refresh the report, got %q", js)
	}
}
