// Package orchestration is the v1 single-process pipeline that wires
// detection → architecture inference → constraint surface → enrichment →
// patch synthesis → harness synthesis → verification → composition →
// report rendering. Conductor + Lookout + leader election arrive in
// Epic 4; this package is the "single CLI invocation" path SPEC §1.4
// promises for v1.
package orchestration

import (
	"context"
	"errors"
	"time"

	"github.com/revelara-ai/orion/internal/architect"
	"github.com/revelara-ai/orion/internal/constraints"
	"github.com/revelara-ai/orion/internal/enrichment"
	"github.com/revelara-ai/orion/internal/harness"
	"github.com/revelara-ai/orion/internal/patches"
	"github.com/revelara-ai/orion/internal/polaris"
	"github.com/revelara-ai/orion/internal/verify"
)

// Sentinel errors.
var (
	// ErrInvalidOptions: required RunOptions field missing.
	ErrInvalidOptions = errors.New("orchestration: invalid options")

	// ErrNoImprovement: pipeline ran but produced no accepted patches.
	// Surfaced to the CLI as "no_improvement" exit status (positive
	// signal per Epic 1 acceptance criteria, not failure).
	ErrNoImprovement = errors.New("orchestration: no improvement (no patches accepted)")
)

// RunOptions configures one pipeline invocation.
type RunOptions struct {
	// RunID uniquely identifies this run. Stamped on the harness
	// namespace, the branch name, and every produced artifact.
	RunID string

	// RepoPath is the absolute path to a writable working tree of the
	// target repo. The pipeline does NOT clone in v1 (clone is the
	// caller's responsibility, typically the CLI which uses
	// internal/github.Clone into a sandbox workspace). This is so the
	// pipeline is testable against a static fixture tree.
	RepoPath string

	// Service is the canonical service name within the repo (e.g.
	// "checkoutservice"). Used to scope detection and risks.
	Service string

	// Issue is the source tracker issue this run is satisfying. Used
	// to build the PR title and stamp the branch.
	Issue Issue

	// Seed is the deterministic seed for harness + bootstrap.
	Seed int64
}

// Validate checks RunOptions.
func (o RunOptions) Validate() error {
	if o.RunID == "" || o.RepoPath == "" || o.Service == "" {
		return ErrInvalidOptions
	}
	return nil
}

// Issue describes the source tracker issue driving this run.
type Issue struct {
	ExternalID string
	Title      string
	Body       string
}

// VerifiedPatch pairs a CandidatePatch with its Verdict.
type VerifiedPatch struct {
	Patch   patches.CandidatePatch
	Verdict verify.Verdict
}

// PRPlan is the composer's output. v1 doesn't actually open a PR
// inside the pipeline (the orchestrator caller does); the plan is
// what the CLI either dry-runs or hands to the GitHub App.
type PRPlan struct {
	// BranchName is the canonical orion/* branch (per SPEC §4.2).
	BranchName string

	// Title is the PR title per SPEC §16.1.
	Title string

	// Body is the rendered markdown report.
	Body string

	// Commits is the ordered list of (CommitMessage, FilePath, Diff)
	// the GitHub App will materialize.
	Commits []PlannedCommit
}

// PlannedCommit is one commit inside a PRPlan.
type PlannedCommit struct {
	// CommitMessage includes the Polaris control ID and the verification
	// axis improved per SPEC §16.1.
	CommitMessage string

	// TargetPath is the file the diff modifies.
	TargetPath string

	// UnifiedDiff is the patch body.
	UnifiedDiff string

	// ControlID is the addressed Polaris control_code.
	ControlID string

	// Patch carries the full provenance for the run record.
	Patch patches.CandidatePatch

	// Verdict is the verifier output that gated this commit.
	Verdict verify.Verdict
}

// RunResult is the full pipeline output.
type RunResult struct {
	RunID             string
	StartedAt         time.Time
	CompletedAt       time.Time
	Findings          int
	Model             *architect.ArchitecturalModel
	Constraints       *constraints.ConstraintSurface
	Harness           *harness.Harness
	AcceptedPatches   []VerifiedPatch
	RejectedPatches   []VerifiedPatch
	NoVerdict         []patches.CandidatePatch
	PRPlan            *PRPlan
	PolarisSnapshotAt time.Time
}

// Stage names are emitted in the trace log as the pipeline advances.
type Stage string

// Stages.
const (
	StageDetect      Stage = "detect"
	StageArchitect   Stage = "architect"
	StageConstraints Stage = "constraints"
	StageEnrich      Stage = "enrich"
	StageHarness     Stage = "harness"
	StageSynthesize  Stage = "synthesize"
	StageVerify      Stage = "verify"
	StageCompose     Stage = "compose"
	StageReport      Stage = "report"
)

// Trace is a per-stage progress callback. nil = silent.
type Trace func(stage Stage, msg string)

// PolarisCatalogReader is the subset of polaris.Client used to
// snapshot the controls catalog at run start. Interface lets tests
// inject a fake without spinning up an HTTP server.
type PolarisCatalogReader interface {
	ListControls(ctx context.Context, opts polaris.ListControlsOptions) (*polaris.ControlsCatalog, error)
}

// PolarisEnrichmentReader is the subset enrichment.Builder needs.
// Mirrors enrichment.PolarisReader so callers can use the same fake.
type PolarisEnrichmentReader = enrichment.PolarisReader
