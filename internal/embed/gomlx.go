package embed

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/gomlx/compute/dtypes"
	"github.com/gomlx/compute/gobackend"
	"github.com/gomlx/compute/shapes"
	"github.com/gomlx/gomlx/core/graph"
	"github.com/gomlx/gomlx/core/tensors"
	"github.com/gomlx/gomlx/ml/model"
	"github.com/gomlx/go-huggingface/tokenizers/api"
	"github.com/gomlx/go-huggingface/tokenizers/hftokenizer"
	"github.com/gomlx/onnx-gomlx/onnx"
	"github.com/gomlx/onnx-gomlx/onnx/parser"
)

const (
	// bge-base-en-v1.5 retrieval prefixes: queries get the instruction; documents are bare.
	bgeQueryPrefix = "Represent this sentence for searching relevant passages: "
	bgeDocPrefix   = ""
	bgeOutputName  = "last_hidden_state"
)

// GoMLX is the self-contained, in-process, CGO-free local embedder: an ONNX
// sentence-transformer (default bge-base-en-v1.5, 768-dim) run on GoMLX's pure-Go backend
// with a pure-Go WordPiece tokenizer. Validated by the or-hd3.7 spike (CGO_ENABLED=0;
// cosine 0.80 close vs 0.42 unrelated). The GoMLX Exec is single-graph, so calls are
// serialized under mu.
type GoMLX struct {
	mu      sync.Mutex
	tok     *hftokenizer.Tokenizer
	omodel  onnx.Model
	exec    *model.Exec
	dim     int
	id      string
	qPrefix string
	dPrefix string
}

// NewGoMLX loads model.onnx + tokenizer.json from cfg.ModelPath and builds the pure-Go
// executor. The model is large (~400MB for bge-base) and must be provisioned at that path;
// a missing asset returns actionable guidance rather than a cryptic failure. (Auto-download
// on first use is a tracked follow-up — keeping this slice's footprint bounded.)
func NewGoMLX(cfg Config) (*GoMLX, error) {
	modelName := cfg.Model
	if modelName == "" {
		modelName = "bge-base-en-v1.5"
	}
	if cfg.ModelPath == "" {
		return nil, fmt.Errorf("embed: local model path not configured (set Config.ModelPath to a dir with model.onnx + tokenizer.json for %s)", modelName)
	}
	modelFile := filepath.Join(cfg.ModelPath, "model.onnx")
	tokFile := filepath.Join(cfg.ModelPath, "tokenizer.json")
	for _, f := range []string{modelFile, tokFile} {
		if _, err := os.Stat(f); err != nil {
			return nil, fmt.Errorf("embed: model asset missing: %s (provision the %s ONNX export under %s)", f, modelName, cfg.ModelPath)
		}
	}

	backend, err := gobackend.New("")
	if err != nil {
		return nil, fmt.Errorf("embed: gobackend: %w", err)
	}
	tok, err := hftokenizer.NewFromFile(&api.Config{}, tokFile)
	if err != nil {
		return nil, fmt.Errorf("embed: tokenizer: %w", err)
	}
	if err := tok.With(api.EncodeOptions{AddSpecialTokens: true}); err != nil {
		return nil, fmt.Errorf("embed: tokenizer opts: %w", err)
	}
	om, err := parser.ParseFile(modelFile)
	if err != nil {
		return nil, fmt.Errorf("embed: parse onnx: %w", err)
	}
	store := model.NewStore()
	if err := om.VariablesToScope(store.RootScope()); err != nil {
		return nil, fmt.Errorf("embed: load weights: %w", err)
	}
	exec := model.MustNewExec(backend, store,
		func(scope *model.Scope, ids, mask, types *graph.Node) *graph.Node {
			g := ids.Graph()
			return om.CallGraph(scope, g, map[string]*graph.Node{
				"input_ids":      ids,
				"attention_mask": mask,
				"token_type_ids": types,
			}, bgeOutputName)[0]
		})

	e := &GoMLX{tok: tok, omodel: om, exec: exec, qPrefix: bgeQueryPrefix, dPrefix: bgeDocPrefix}
	// Probe the dimension (also warms/JITs the graph).
	v, err := e.embedOne("dimension probe")
	if err != nil {
		return nil, fmt.Errorf("embed: probe: %w", err)
	}
	e.dim = len(v)
	e.id = fmt.Sprintf("gomlx/%s@%d", modelName, e.dim)
	return e, nil
}

// Close releases the GoMLX executor.
func (e *GoMLX) Close() error {
	e.exec.Finalize()
	return nil
}

func (e *GoMLX) Dim() int   { return e.dim }
func (e *GoMLX) ID() string { return e.id }

func (e *GoMLX) EmbedDocuments(_ context.Context, texts []string) ([][]float32, error) {
	return e.embedBatch(texts, e.dPrefix)
}

func (e *GoMLX) EmbedQueries(_ context.Context, texts []string) ([][]float32, error) {
	return e.embedBatch(texts, e.qPrefix)
}

func (e *GoMLX) embedBatch(texts []string, prefix string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v, err := e.embedOne(prefix + t)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

// embedOne tokenizes (with special tokens), runs the model, masked-mean-pools the token
// embeddings, and L2-normalizes — the validated spike pipeline.
func (e *GoMLX) embedOne(text string) ([]float32, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	ids := e.tok.Encode(text)
	if len(ids) == 0 {
		ids = []int{0}
	}
	seqLen := len(ids)
	ids64 := make([]int64, seqLen)
	mask64 := make([]int64, seqLen)
	types64 := make([]int64, seqLen) // single sentence → all token-type 0
	for i, v := range ids {
		ids64[i] = int64(v)
		mask64[i] = 1
	}
	idT := tensors.FromShape(shapes.Make(dtypes.Int64, 1, seqLen))
	maskT := tensors.FromShape(shapes.Make(dtypes.Int64, 1, seqLen))
	typeT := tensors.FromShape(shapes.Make(dtypes.Int64, 1, seqLen))
	if err := tensors.MutableFlatData(idT, func(f []int64) { copy(f, ids64) }); err != nil {
		return nil, fmt.Errorf("embed: fill ids: %w", err)
	}
	if err := tensors.MutableFlatData(maskT, func(f []int64) { copy(f, mask64) }); err != nil {
		return nil, fmt.Errorf("embed: fill mask: %w", err)
	}
	if err := tensors.MutableFlatData(typeT, func(f []int64) { copy(f, types64) }); err != nil {
		return nil, fmt.Errorf("embed: fill types: %w", err)
	}

	out := e.exec.MustCall(idT, maskT, typeT)[0]
	dims := out.Shape().Dimensions // [1, seqLen, hidden]
	hidden := dims[len(dims)-1]
	pooled := make([]float32, hidden)
	if err := tensors.ConstFlatData(out, func(flat []float32) {
		var denom float32
		for t := 0; t < seqLen; t++ {
			m := float32(mask64[t])
			denom += m
			base := t * hidden
			for h := 0; h < hidden; h++ {
				pooled[h] += m * flat[base+h]
			}
		}
		if denom == 0 {
			denom = 1
		}
		for h := range pooled {
			pooled[h] /= denom
		}
	}); err != nil {
		_ = out.FinalizeAll()
		return nil, fmt.Errorf("embed: read output: %w", err)
	}
	_ = out.FinalizeAll() // best-effort cleanup of the output tensor
	l2normalize(pooled)
	return pooled, nil
}
