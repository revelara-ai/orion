package orchestration

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/revelara-ai/orion/internal/architect"
	"github.com/revelara-ai/orion/internal/constraints"
	"github.com/revelara-ai/orion/internal/detection"
	"github.com/revelara-ai/orion/internal/enrichment"
	"github.com/revelara-ai/orion/internal/github"
	"github.com/revelara-ai/orion/internal/harness"
	"github.com/revelara-ai/orion/internal/llm"
	"github.com/revelara-ai/orion/internal/patches"
	"github.com/revelara-ai/orion/internal/polaris"
	"github.com/revelara-ai/orion/internal/verify"
)

// Pipeline holds the dependencies the v1 single-process orchestrator
// needs. Constructed once per CLI invocation; not safe for concurrent
// runs.
type Pipeline struct {
	// Polaris is the run-snapshot source for controls + enrichment.
	Polaris PolarisCatalogReader

	// Enrichment is what builds per-gap IssueContextBlocks. Typically
	// constructed from Polaris + the snapshotted catalog.
	Enrichment PolarisEnrichmentReader

	// LLM is the patch-synthesis generator.
	LLM llm.Generator

	// LLMModel records the model identifier on each CandidatePatch.
	LLMModel string

	// LLMSeed records the LLM provider seed (where available).
	LLMSeed int64

	// Scanner is the rvl-cli wrapper.
	Scanner *detection.Scanner

	// Architect is the architectural inferer.
	Architect *architect.Inferer

	// ConstraintInferer derives the constraint surface.
	ConstraintInferer *constraints.Inferer

	// VerifyConfig overrides the verifier loop config; zero falls back
	// to verify.DefaultLoopConfig().
	VerifyConfig verify.LoopConfig

	// PatchedDelta is the InProcessRunner improvement factor used in
	// the v1 reduced-fidelity verifier (deferred to live K8s runner
	// once orion-sfp's materializer ships).
	PatchedDelta float64

	// Trace is an optional per-stage callback. nil = silent.
	Trace Trace
}

// Run executes the full pipeline and returns a RunResult. On
// ErrNoImprovement the result is still returned (with empty PRPlan)
// so the caller can render a "no improvement" report.
func (p *Pipeline) Run(ctx context.Context, opts RunOptions) (*RunResult, error) {
	if err := opts.Validate(); err != nil {
		return nil, err
	}
	if p.Polaris == nil || p.Scanner == nil || p.Architect == nil || p.ConstraintInferer == nil || p.LLM == nil {
		return nil, fmt.Errorf("%w: missing pipeline dependency", ErrInvalidOptions)
	}

	res := &RunResult{
		RunID:     opts.RunID,
		StartedAt: time.Now().UTC(),
	}

	// 1. Detect.
	p.trace(StageDetect, "running rvl scanner")
	findings, stats, err := p.Scanner.Run(ctx, detection.ScanOptions{
		RepoPath: opts.RepoPath,
		Service:  opts.Service,
	})
	if err != nil {
		return nil, fmt.Errorf("detect: %w", err)
	}
	res.Findings = stats.FindingsTotal

	// 2. Architect.
	p.trace(StageArchitect, "inferring architectural model")
	model, err := p.Architect.Infer(ctx, architect.InferOptions{RepoPath: opts.RepoPath})
	if err != nil {
		return nil, fmt.Errorf("architect: %w", err)
	}
	res.Model = &model

	// 3. Snapshot Polaris controls.
	p.trace(StageConstraints, "snapshotting Polaris controls")
	catalog, err := p.Polaris.ListControls(ctx, polaris.ListControlsOptions{Limit: 200})
	if err != nil {
		return nil, fmt.Errorf("polaris snapshot: %w", err)
	}
	res.PolarisSnapshotAt = time.Now().UTC()

	// 4. ConstraintSurface.
	surface, err := p.ConstraintInferer.Infer(ctx, constraints.InferOptions{Model: &model, Catalog: catalog})
	if err != nil {
		return nil, fmt.Errorf("constraints: %w", err)
	}
	res.Constraints = surface

	// 5. Synthesize harness.
	p.trace(StageHarness, "synthesizing harness")
	h, err := harness.Synthesize(harness.SynthesizeOptions{
		RunID:        opts.RunID,
		Model:        &model,
		Constraints:  surface,
		Seed:         opts.Seed,
		Thoroughness: harness.ThoroughnessStandard,
	})
	if err != nil {
		return nil, fmt.Errorf("harness: %w", err)
	}
	res.Harness = h

	// 6. Per-finding loop: enrich, synthesize patch, verify.
	gaps := findingsToGaps(findings)
	verifier := verify.LoopConfigForThoroughness(h.Thoroughness)
	if p.VerifyConfig.MaxTrials > 0 {
		verifier = p.VerifyConfig
	}
	enrichBuilder := enrichment.NewBuilder(p.Enrichment, catalog)
	synth := patches.NewSynthesizer(p.LLM, p.LLMModel, p.LLMSeed)

	for _, gap := range gaps {
		// 6a. Enrich.
		p.trace(StageEnrich, "enriching "+gap.ID)
		ctxBlock, err := enrichBuilder.Build(ctx, enrichment.Query{
			IssueID: gap.ID,
			Pattern: string(gap.Pattern),
			Service: opts.Service,
		})
		if err != nil {
			res.NoVerdict = append(res.NoVerdict, patches.CandidatePatch{GapID: gap.ID})
			continue
		}

		// 6b. Synthesize.
		p.trace(StageSynthesize, "synthesizing patch for "+gap.ID)
		ps, errs := synth.Synthesize(ctx, []patches.Gap{gap}, ctxBlock, patches.SynthesizeOptions{})
		if len(ps) == 0 || errs[0] != nil {
			res.NoVerdict = append(res.NoVerdict, patches.CandidatePatch{GapID: gap.ID})
			continue
		}
		patch := ps[0]

		// 6c. Verify (in-process v1).
		p.trace(StageVerify, "verifying patch for "+gap.ID)
		baseline := harness.InProcessRunner{}
		patched := harness.InProcessRunner{PatchedDelta: p.PatchedDelta}
		v, err := verify.Loop(ctx, h, baseline, patched, verifier)
		if err != nil {
			res.NoVerdict = append(res.NoVerdict, patch)
			continue
		}
		v.LLMModel = patch.LLMModel
		v.LLMSeed = patch.LLMSeed
		vp := VerifiedPatch{Patch: patch, Verdict: *v}
		if v.Decision == verify.DecisionAccepted {
			res.AcceptedPatches = append(res.AcceptedPatches, vp)
		} else {
			res.RejectedPatches = append(res.RejectedPatches, vp)
		}
	}

	// 7. Compose.
	p.trace(StageCompose, "composing accepted patches")
	res.PRPlan = composePRPlan(opts, h, res.AcceptedPatches)

	res.CompletedAt = time.Now().UTC()
	if len(res.AcceptedPatches) == 0 {
		return res, ErrNoImprovement
	}
	return res, nil
}

func (p *Pipeline) trace(stage Stage, msg string) {
	if p.Trace != nil {
		p.Trace(stage, msg)
	}
}

// findingsToGaps converts detection.Finding entries into patches.Gap
// entries. v1 maps the three Epic 1 patterns by category substring;
// unknown categories are skipped.
func findingsToGaps(findings []detection.Finding) []patches.Gap {
	out := make([]patches.Gap, 0, len(findings))
	for i, f := range findings {
		pat := patternForCategory(f.Category)
		if pat == "" {
			continue
		}
		gap := patches.Gap{
			ID:          fmt.Sprintf("g%03d", i+1),
			Pattern:     pat,
			FilePath:    f.File,
			LineRange:   [2]int{f.Line, f.Line},
			Description: f.Slug,
		}
		out = append(out, gap)
	}
	return out
}

func patternForCategory(category string) patches.Pattern {
	c := strings.ToLower(category)
	switch {
	case strings.Contains(c, "timeout"):
		return patches.PatternTimeout
	case strings.Contains(c, "retry"):
		return patches.PatternRetry
	case strings.Contains(c, "idempot"):
		return patches.PatternIdempotency
	}
	return ""
}

// composePRPlan delegates to the composer; here so callers (and
// tests) can call the pipeline without the composer being a separate
// public surface.
func composePRPlan(opts RunOptions, h *harness.Harness, accepted []VerifiedPatch) *PRPlan {
	if len(accepted) == 0 {
		return nil
	}
	return Compose(opts, h, accepted)
}

// avoid unused import noise in case tests strip dead imports later
var (
	_ = github.BranchPrefix
)
