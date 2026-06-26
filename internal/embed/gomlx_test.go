package embed

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// gomlxModelDir is where the or-hd3.7 spike provisioned bge-base-en-v1.5. The parity test is
// gated on its presence so CI (and any box without the ~400MB model) skips cleanly while a
// developer with the model gets real end-to-end validation. Override with ORION_EMBED_MODEL_DIR.
func gomlxModelDir() string {
	if d := os.Getenv("ORION_EMBED_MODEL_DIR"); d != "" {
		return d
	}
	return "/tmp/embedspike/models/bge"
}

// TestGoMLXParity (or-hd3.7): with the real model present, the pure-Go GoMLX embedder yields
// 768-dim vectors whose cosine separates a semantically-close pair from an unrelated one —
// the spike's PASS criterion, now guarding the ported impl. Gated: skips if the model is absent.
func TestGoMLXParity(t *testing.T) {
	dir := gomlxModelDir()
	if _, err := os.Stat(filepath.Join(dir, "model.onnx")); err != nil {
		t.Skipf("gomlx model not provisioned at %s (set ORION_EMBED_MODEL_DIR) — skipping real-model parity test", dir)
	}
	e, err := NewGoMLX(Config{Provider: "local", Model: "bge-base-en-v1.5", ModelPath: dir})
	if err != nil {
		t.Fatalf("NewGoMLX: %v", err)
	}
	defer func() { _ = e.Close() }()
	if e.Dim() != 768 {
		t.Fatalf("dim = %d, want 768", e.Dim())
	}
	ctx := context.Background()
	docs, err := e.EmbedDocuments(ctx, []string{
		"the cat sat on the mat",
		"a feline rested on the rug",
		"quantum chromodynamics governs quarks",
	})
	if err != nil {
		t.Fatal(err)
	}
	ab := Cosine(docs[0], docs[1]) // close pair
	ac := Cosine(docs[0], docs[2]) // unrelated
	if !(ab > ac && ab-ac > 0.1) {
		t.Fatalf("semantic separation failed: cos(close)=%.3f cos(unrelated)=%.3f", ab, ac)
	}
}
