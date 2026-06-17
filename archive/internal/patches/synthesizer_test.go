package patches

import (
	"context"
	"errors"
	"testing"

	"github.com/revelara-ai/orion/internal/enrichment"
	"github.com/revelara-ai/orion/internal/llm"
	"github.com/revelara-ai/orion/internal/polaris"
)

// stubGen is a llm.Generator that returns a canned response per call.
type stubGen struct {
	responses []llm.GenerateResponse
	errs      []error
	calls     int
}

func (s *stubGen) Generate(ctx context.Context, req llm.GenerateRequest) (llm.GenerateResponse, error) {
	if s.calls >= len(s.responses) {
		return llm.GenerateResponse{}, errors.New("stubGen: out of responses")
	}
	resp := s.responses[s.calls]
	var err error
	if s.calls < len(s.errs) {
		err = s.errs[s.calls]
	}
	s.calls++
	return resp, err
}

func TestSynthesizeHappyPath(t *testing.T) {
	gen := &stubGen{
		responses: []llm.GenerateResponse{
			{
				Text:  "```diff\n--- a/client.go\n+++ b/client.go\n@@ -10,3 +10,5 @@\n func Call() {\n+\tctx, cancel := context.WithTimeout(ctx, time.Second)\n+\tdefer cancel()\n }\n```",
				Model: "test-model",
			},
		},
	}
	s := NewSynthesizer(gen, "test-model", 42)
	gaps := []Gap{
		{ID: "g1", Pattern: PatternTimeout, FilePath: "client.go", LineRange: [2]int{10, 12}, Description: "outbound call no timeout"},
	}
	ctxBlock := &enrichment.IssueContextBlock{
		Controls: []polaris.Control{{ControlCode: "RC-TIMEOUT-1", Name: "Outbound timeouts"}},
	}
	patches, errs := s.Synthesize(context.Background(), gaps, ctxBlock, SynthesizeOptions{})
	if len(patches) != 1 || len(errs) != 1 {
		t.Fatalf("len patches=%d errs=%d", len(patches), len(errs))
	}
	if errs[0] != nil {
		t.Fatalf("err: %v", errs[0])
	}
	if patches[0].GapID != "g1" {
		t.Errorf("GapID = %q", patches[0].GapID)
	}
	if patches[0].ControlID != "RC-TIMEOUT-1" {
		t.Errorf("ControlID = %q", patches[0].ControlID)
	}
	if patches[0].Pattern != PatternTimeout {
		t.Errorf("Pattern = %q", patches[0].Pattern)
	}
	if patches[0].LLMModel != "test-model" {
		t.Errorf("LLMModel = %q", patches[0].LLMModel)
	}
	if patches[0].LLMSeed != 42 {
		t.Errorf("LLMSeed = %d", patches[0].LLMSeed)
	}
	if patches[0].GeneratedAt.IsZero() {
		t.Error("GeneratedAt zero")
	}
}

func TestSynthesizeRejectsInvalidGap(t *testing.T) {
	gen := &stubGen{}
	s := NewSynthesizer(gen, "m", 0)
	gaps := []Gap{
		{ID: "g1"}, // missing pattern + path
	}
	patches, errs := s.Synthesize(context.Background(), gaps, nil, SynthesizeOptions{})
	if !errors.Is(errs[0], ErrInvalidGap) {
		t.Errorf("err = %v, want ErrInvalidGap", errs[0])
	}
	if patches[0].GapID != "" {
		t.Errorf("expected zero patch, got %v", patches[0])
	}
	if gen.calls != 0 {
		t.Errorf("LLM called for invalid gap (%d times)", gen.calls)
	}
}

func TestSynthesizeRejectsBadLLMOutput(t *testing.T) {
	gen := &stubGen{
		responses: []llm.GenerateResponse{
			{Text: "I would put context.WithTimeout but I'm too lazy to write the diff."},
		},
	}
	s := NewSynthesizer(gen, "m", 0)
	gaps := []Gap{
		{ID: "g1", Pattern: PatternTimeout, FilePath: "x.go", LineRange: [2]int{1, 1}, Description: "x"},
	}
	_, errs := s.Synthesize(context.Background(), gaps, nil, SynthesizeOptions{})
	if !errors.Is(errs[0], ErrInvalidDiff) {
		t.Errorf("err = %v, want ErrInvalidDiff", errs[0])
	}
}

func TestSynthesizePropagatesLLMError(t *testing.T) {
	gen := &stubGen{
		responses: []llm.GenerateResponse{{}},
		errs:      []error{errors.New("quota exceeded")},
	}
	s := NewSynthesizer(gen, "m", 0)
	gaps := []Gap{
		{ID: "g1", Pattern: PatternTimeout, FilePath: "x.go", LineRange: [2]int{1, 1}, Description: "x"},
	}
	_, errs := s.Synthesize(context.Background(), gaps, nil, SynthesizeOptions{})
	if !errors.Is(errs[0], ErrSynthesisFailed) {
		t.Errorf("err = %v, want ErrSynthesisFailed", errs[0])
	}
}
