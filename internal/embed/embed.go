// Package embed is Orion's pluggable text embedder for semantic memory recall (or-hd3.7).
//
// It is SELF-CONTAINED by design: the only provider is a fully in-process, CGO-free local
// model (GoMLX running an ONNX sentence-transformer — bge-base-en-v1.5, 768-dim), validated
// to build with CGO_ENABLED=0 and ship without any native shared library, daemon, or cloud
// API. (A cloud provider was deliberately rejected to keep Orion offline-capable.)
//
// The interface is ROLE-AWARE: retrieval models embed a search QUERY differently from a
// stored DOCUMENT (bge prepends a query instruction), so the prefix/instruction logic lives
// inside each impl and call sites simply say which they mean.
package embed

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"strings"
)

// Embedder turns text into dense unit vectors for semantic recall.
type Embedder interface {
	// EmbedDocuments embeds text being STORED (memory items).
	EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error)
	// EmbedQueries embeds a recall LOOKUP (may use a different prefix/instruction).
	EmbedQueries(ctx context.Context, texts []string) ([][]float32, error)
	// Dim is the vector dimension.
	Dim() int
	// ID is the model identity (e.g. "gomlx/bge-base-en-v1.5@768"); it is stored alongside
	// every vector so a model/dim change is detectable and cross-model similarity is refused.
	ID() string
}

// Config selects + configures the active embedder.
type Config struct {
	Provider  string // "local" (default; in-process GoMLX/ONNX). Cloud is intentionally unsupported.
	Model     string // model name, e.g. "bge-base-en-v1.5"
	ModelPath string // directory holding model.onnx + tokenizer.json (pre-seeded / provisioned)
}

// New builds the configured embedder.
func New(cfg Config) (Embedder, error) {
	switch cfg.Provider {
	case "", "local":
		return NewGoMLX(cfg)
	default:
		return nil, fmt.Errorf("embed: unknown provider %q (only \"local\" is supported — Orion is self-contained)", cfg.Provider)
	}
}

// Stub is a deterministic, dependency-free embedder: it hashes word features into a fixed-dim
// unit vector. It is NOT semantic — its purpose is to exercise the config/storage/reindex/
// retrieval machinery in tests without provisioning the multi-hundred-MB real model. Same
// text → same vector; different text → (almost always) different vector.
type Stub struct {
	dim int
	id  string
}

// NewStub returns a deterministic stub embedder of the given dimension + identity.
func NewStub(dim int, id string) *Stub { return &Stub{dim: dim, id: id} }

func (s *Stub) Dim() int   { return s.dim }
func (s *Stub) ID() string { return s.id }

func (s *Stub) EmbedDocuments(_ context.Context, texts []string) ([][]float32, error) {
	return s.embedAll(texts), nil
}

func (s *Stub) EmbedQueries(_ context.Context, texts []string) ([][]float32, error) {
	return s.embedAll(texts), nil
}

func (s *Stub) embedAll(texts []string) [][]float32 {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = s.embedOne(t)
	}
	return out
}

func (s *Stub) embedOne(text string) []float32 {
	v := make([]float32, s.dim)
	for _, w := range strings.Fields(strings.ToLower(text)) {
		h := fnv.New32a()
		_, _ = h.Write([]byte(w))
		v[int(h.Sum32()%uint32(s.dim))]++ // #nosec G115 -- dim is a small positive config constant; the modulo bounds the index
	}
	l2normalize(v)
	return v
}

// l2normalize scales v to unit length in place (shared by all impls).
func l2normalize(v []float32) {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	n := float32(math.Sqrt(sum))
	if n == 0 {
		return
	}
	for i := range v {
		v[i] /= n
	}
}

// Cosine is the similarity between two unit (or non-unit) vectors. Returns 0 if either is
// zero-length or the dimensions differ.
func Cosine(a, b []float32) float32 {
	if len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return float32(dot / (math.Sqrt(na) * math.Sqrt(nb)))
}
