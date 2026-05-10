package verify

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/revelara-ai/orion/internal/harness"
	"github.com/revelara-ai/orion/internal/patches"
	"github.com/revelara-ai/orion/internal/sandbox"
)

// RunnerFactory constructs the (baseline, patched) runners the
// verifier exercises against a Harness for one candidate. The
// verifier doesn't know whether to use an in-process or K8s runner;
// the caller injects a factory that returns the appropriate pair.
type RunnerFactory func(ctx context.Context, h *harness.Harness, candidate patches.CandidatePatch) (baseline, patched harness.Runner, err error)

// Verify applies the patch, exercises the trial loop, and returns
// the verdict.
func Verify(
	ctx context.Context,
	workspace *sandbox.Workspace,
	h *harness.Harness,
	candidate patches.CandidatePatch,
	factory RunnerFactory,
	cfg LoopConfig,
) (*Verdict, error) {
	if workspace == nil {
		return nil, fmt.Errorf("%w: workspace required", ErrInvalidInputs)
	}
	if h == nil {
		return nil, fmt.Errorf("%w: harness required", ErrInvalidInputs)
	}
	if factory == nil {
		return nil, fmt.Errorf("%w: factory required", ErrInvalidInputs)
	}
	repoDir := filepath.Join(workspace.Path, "repo")
	if !filepath.IsAbs(repoDir) {
		return nil, fmt.Errorf("%w: workspace repo path must be absolute", ErrInvalidInputs)
	}

	app := Applicator{}
	if err := app.Apply(ctx, repoDir, candidate.UnifiedDiff); err != nil {
		return nil, err
	}
	if err := app.Build(ctx, repoDir); err != nil {
		return nil, err
	}

	baseline, patched, err := factory(ctx, h, candidate)
	if err != nil {
		return nil, fmt.Errorf("runner factory: %w", err)
	}
	verdict, err := Loop(ctx, h, baseline, patched, cfg)
	if err != nil {
		return nil, err
	}
	verdict.LLMModel = candidate.LLMModel
	verdict.LLMSeed = candidate.LLMSeed
	return verdict, nil
}
