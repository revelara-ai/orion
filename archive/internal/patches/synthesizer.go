package patches

import (
	"context"
	"fmt"
	"time"

	"github.com/revelara-ai/orion/internal/enrichment"
	"github.com/revelara-ai/orion/internal/llm"
)

// Synthesizer turns Gaps into LLM-generated CandidatePatches. It owns
// neither the gap detector nor the verifier; both live in sister
// packages.
type Synthesizer struct {
	gen   llm.Generator
	model string
	seed  int64
}

// NewSynthesizer returns a Synthesizer. The model + seed are recorded
// on each CandidatePatch for reproducibility (SPEC §14.6 provenance).
func NewSynthesizer(gen llm.Generator, model string, seed int64) *Synthesizer {
	return &Synthesizer{gen: gen, model: model, seed: seed}
}

// SynthesizeOptions controls one Synthesize call.
type SynthesizeOptions struct {
	// MaxTokens caps generation. 0 = SDK default.
	MaxTokens int

	// Temperature for LLM generation. Negative = SDK default.
	Temperature float32
}

// Synthesize calls the LLM once per gap and returns one
// CandidatePatch per gap. Failures (LLM error, grammar rejection,
// invalid diff) are recorded in the second return as a parallel slice
// of errors aligned by index; the patches slice contains zero-value
// CandidatePatches at those positions. Caller decides whether to
// proceed with the partial set.
func (s *Synthesizer) Synthesize(ctx context.Context, gaps []Gap, ctxBlock *enrichment.IssueContextBlock, opts SynthesizeOptions) ([]CandidatePatch, []error) {
	out := make([]CandidatePatch, len(gaps))
	errs := make([]error, len(gaps))
	primary := PrimaryControlCode(ctxBlock)
	for i, gap := range gaps {
		if err := gap.Validate(); err != nil {
			errs[i] = err
			continue
		}
		prompt := BuildPrompt(gap, ctxBlock)
		resp, err := s.gen.Generate(ctx, llm.GenerateRequest{
			User:        prompt,
			MaxTokens:   opts.MaxTokens,
			Temperature: opts.Temperature,
		})
		if err != nil {
			errs[i] = fmt.Errorf("%w: gap %s: %v", ErrSynthesisFailed, gap.ID, err)
			continue
		}
		patch, perr := Parse(resp.Text, ExtractDiffOptions{
			Pattern:            gap.Pattern,
			ExpectedTargetPath: gap.FilePath,
		})
		if perr != nil {
			errs[i] = fmt.Errorf("%w: gap %s: %v", ErrInvalidDiff, gap.ID, perr)
			continue
		}
		patch.GapID = gap.ID
		patch.ControlID = primary
		patch.LLMModel = firstNonEmpty(resp.Model, s.model)
		patch.LLMSeed = s.seed
		patch.GeneratedAt = time.Now().UTC()
		out[i] = patch
	}
	return out, errs
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
