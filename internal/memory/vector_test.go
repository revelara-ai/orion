package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/embed"
)

// TestVectorReindexAndSearch (or-hd3.7): Reindex embeds stored items once (idempotent), and a
// brute-force vector search returns the semantically-matching item first.
func TestVectorReindexAndSearch(t *testing.T) {
	ctx := context.Background()
	s := openMem(t)
	apple, err := s.Write(ctx, Item{Tier: MTM, Kind: KindPattern, Content: "apple banana cherry"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Write(ctx, Item{Tier: MTM, Kind: KindPattern, Content: "xylophone zebra quartz"}); err != nil {
		t.Fatal(err)
	}
	s.SetEmbedder(embed.NewStub(32, "stub@32"))
	n, err := s.Reindex(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("reindex should embed 2 items, got %d", n)
	}
	if n2, err := s.Reindex(ctx); err != nil || n2 != 0 {
		t.Fatalf("second reindex should be a no-op; got %d err=%v", n2, err)
	}
	qv, err := s.emb.EmbedQueries(ctx, []string{"apple banana cherry"})
	if err != nil {
		t.Fatal(err)
	}
	hits, err := s.vidx.Search(ctx, qv[0], s.emb.ID(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].ID != apple {
		t.Fatalf("the matching item should be the top vector hit; got %v", hits)
	}
}

// TestEmbedderConfigSwap (or-hd3.7): switching the active embedder + reindexing re-embeds
// items under the new embedder; the old embedder's namespace no longer matches.
func TestEmbedderConfigSwap(t *testing.T) {
	ctx := context.Background()
	s := openMem(t)
	id, err := s.Write(ctx, Item{Tier: MTM, Kind: KindPattern, Content: "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	s.SetEmbedder(embed.NewStub(8, "modelA@8"))
	if _, err := s.Reindex(ctx); err != nil {
		t.Fatal(err)
	}
	q, err := s.emb.EmbedQueries(ctx, []string{"alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if hits, _ := s.vidx.Search(ctx, q[0], "modelA@8", 5); len(hits) != 1 || hits[0].ID != id {
		t.Fatalf("A: expected the item under modelA@8; got %v", hits)
	}

	// Swap embedder (config change) → reindex re-embeds under the new id.
	s.SetEmbedder(embed.NewStub(8, "modelB@8"))
	n, err := s.Reindex(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("a config swap should re-embed the item under the new embedder; got %d", n)
	}
	if hits, _ := s.vidx.Search(ctx, q[0], "modelB@8", 5); len(hits) != 1 || hits[0].ID != id {
		t.Fatalf("B: expected the item under modelB@8 after the swap; got %v", hits)
	}
	if hits, _ := s.vidx.Search(ctx, q[0], "modelA@8", 5); len(hits) != 0 {
		t.Fatalf("after the swap no vectors should remain under the old embedder; got %v", hits)
	}
}

// TestReindexOnDimChange (or-hd3.7): a dimension change re-embeds at the new dim, and a
// mismatched-dimension query never produces wrong-dimension cosine math.
func TestReindexOnDimChange(t *testing.T) {
	ctx := context.Background()
	s := openMem(t)
	id, err := s.Write(ctx, Item{Tier: MTM, Kind: KindPattern, Content: "alpha beta"})
	if err != nil {
		t.Fatal(err)
	}
	s.SetEmbedder(embed.NewStub(8, "m@8"))
	if _, err := s.Reindex(ctx); err != nil {
		t.Fatal(err)
	}
	// dim change 8 → 16 (new embedder id) re-embeds at the new dimension.
	s.SetEmbedder(embed.NewStub(16, "m@16"))
	n, err := s.Reindex(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("a dim change should trigger re-embedding; got %d", n)
	}
	q16, err := s.emb.EmbedQueries(ctx, []string{"alpha beta"})
	if err != nil {
		t.Fatal(err)
	}
	if hits, _ := s.vidx.Search(ctx, q16[0], "m@16", 5); len(hits) != 1 || hits[0].ID != id {
		t.Fatalf("the dim-16 search should find the re-embedded item; got %v", hits)
	}
	// A stale dim-8 query against the dim-16 index must return nothing — never wrong-dim math.
	if hits, _ := s.vidx.Search(ctx, make([]float32, 8), "m@16", 5); len(hits) != 0 {
		t.Fatalf("a mismatched-dimension query must return no hits; got %v", hits)
	}
}

// TestDecodeVecRejectsCorruptBlob (or-hd3.7 review): a non-multiple-of-4 BLOB is a corrupt
// vector and must be dropped (nil), never silently truncated; valid vectors round-trip exactly.
func TestDecodeVecRejectsCorruptBlob(t *testing.T) {
	if v := decodeVec([]byte{1, 2, 3}); v != nil {
		t.Fatalf("3-byte (corrupt) blob should decode to nil, got %v", v)
	}
	if v := decodeVec([]byte{1, 2, 3, 4, 5}); v != nil {
		t.Fatalf("5-byte (corrupt) blob should decode to nil, got %v", v)
	}
	orig := []float32{1.5, -2.25, 0, 3.0}
	got := decodeVec(encodeVec(orig))
	if len(got) != len(orig) {
		t.Fatalf("round-trip len = %d, want %d", len(got), len(orig))
	}
	for i := range orig {
		if got[i] != orig[i] {
			t.Fatalf("round-trip mismatch at %d: %v != %v", i, got[i], orig[i])
		}
	}
}

// TestHybridFusionSemanticRecall (or-hd3.8): with an embedder configured, a reordered query
// (NOT a substring → keyword misses) still recalls the item via semantic similarity. Uses the
// stub (bag-of-words), so it's deterministic without the real model.
func TestHybridFusionSemanticRecall(t *testing.T) {
	ctx := context.Background()
	s := openMem(t)
	hit, err := s.Write(ctx, Item{Tier: MTM, Kind: KindPattern, Content: "alpha beta gamma"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Write(ctx, Item{Tier: MTM, Kind: KindPattern, Content: "xray yankee zulu"}); err != nil {
		t.Fatal(err)
	}
	s.SetEmbedder(embed.NewStub(64, "stub@64"))
	if _, err := s.Reindex(ctx); err != nil {
		t.Fatal(err)
	}
	q := "gamma beta alpha" // same words, reordered → not a keyword substring of the target
	if strings.Contains("alpha beta gamma", q) {
		t.Fatal("setup: the query must NOT be a keyword substring of the target")
	}
	got, err := s.Retrieve(ctx, q, MTM)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[0].ID != hit {
		t.Fatalf("hybrid fusion should recall the reordered semantic match first; got %s", firstID(got))
	}
}

// TestVecSemanticRecallBeatsKeyword (or-hd3.8 acceptance): with the REAL model, a paraphrase
// with no shared content words recalls the semantically-matching item that keyword retrieval
// misses. Gated on the bge model (skips if absent; set ORION_EMBED_MODEL_DIR to override).
func TestVecSemanticRecallBeatsKeyword(t *testing.T) {
	dir := os.Getenv("ORION_EMBED_MODEL_DIR")
	if dir == "" {
		dir = "/tmp/embedspike/models/bge"
	}
	if _, err := os.Stat(filepath.Join(dir, "model.onnx")); err != nil {
		t.Skipf("bge model not provisioned at %s — skipping real-model semantic recall test", dir)
	}
	e, err := embed.NewGoMLX(embed.Config{Provider: "local", Model: "bge-base-en-v1.5", ModelPath: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = e.Close() }()
	ctx := context.Background()
	s := openMem(t)
	target, err := s.Write(ctx, Item{Tier: MTM, Kind: KindPattern, Content: "the feline rested on the woven rug"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Write(ctx, Item{Tier: MTM, Kind: KindPattern, Content: "the database connection pool was exhausted"}); err != nil {
		t.Fatal(err)
	}
	s.SetEmbedder(e)
	if _, err := s.Reindex(ctx); err != nil {
		t.Fatal(err)
	}
	q := "a cat napping on a carpet" // paraphrase: no shared content word with the target
	if strings.Contains("the feline rested on the woven rug", q) {
		t.Fatal("setup: the paraphrase must not be a keyword substring")
	}
	got, err := s.Retrieve(ctx, q, MTM)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[0].ID != target {
		t.Fatalf("the paraphrase should semantically recall the feline/rug item; got first=%s", firstID(got))
	}
}
