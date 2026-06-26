package embed

import (
	"context"
	"testing"
)

// TestStubDeterministicAndDim: the dependency-free stub embedder is deterministic, honors its
// configured dimension, and separates distinct texts — enough to drive storage/reindex tests
// without the real model.
func TestStubDeterministicAndDim(t *testing.T) {
	s := NewStub(64, "stub@64")
	if s.Dim() != 64 || s.ID() != "stub@64" {
		t.Fatalf("dim/id: got dim=%d id=%q", s.Dim(), s.ID())
	}
	ctx := context.Background()
	a1, err := s.EmbedDocuments(ctx, []string{"hello world"})
	if err != nil {
		t.Fatal(err)
	}
	a2, err := s.EmbedQueries(ctx, []string{"hello world"})
	if err != nil {
		t.Fatal(err)
	}
	if len(a1) != 1 || len(a1[0]) != 64 {
		t.Fatalf("want one 64-dim vector, got %d x %d", len(a1), len(a1[0]))
	}
	if Cosine(a1[0], a2[0]) < 0.999 {
		t.Fatal("the same text must embed identically (determinism)")
	}
	b, err := s.EmbedDocuments(ctx, []string{"completely unrelated subject matter entirely"})
	if err != nil {
		t.Fatal(err)
	}
	if Cosine(a1[0], b[0]) > 0.99 {
		t.Fatal("clearly different texts should not be near-identical")
	}
}

// TestNewProviderValidation: New routes by provider and fails clearly on a bad config.
func TestNewProviderValidation(t *testing.T) {
	if _, err := New(Config{Provider: "bogus"}); err == nil {
		t.Fatal("unknown provider should error")
	}
	// local (default) without a provisioned model path must error with guidance, not panic.
	if _, err := New(Config{Provider: "local"}); err == nil {
		t.Fatal("local provider without a model path should error")
	}
}

// TestCosine: basic sanity for the shared similarity helper.
func TestCosine(t *testing.T) {
	a := []float32{1, 0, 0}
	b := []float32{1, 0, 0}
	c := []float32{0, 1, 0}
	if Cosine(a, b) < 0.999 {
		t.Fatal("identical vectors should be ~1")
	}
	if Cosine(a, c) != 0 {
		t.Fatal("orthogonal vectors should be 0")
	}
	if Cosine(a, []float32{1, 0}) != 0 {
		t.Fatal("mismatched dimensions should be 0")
	}
}
