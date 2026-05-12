package detection

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/revelara-ai/orion/internal/dedup"
	"github.com/revelara-ai/orion/internal/repos"
)

// AutoFileGate is the surface LoopDriver needs from internal/backlog.
// Declared here (not imported) to avoid a hard dep on the package's
// concrete type and to keep the test surface narrow.
//
// MaybeFile returns Filed=true when the autofile gate decided to call
// the tracker adapter. An error is returned only on infrastructure
// failure; gate-rejection (e.g., trust mode shadow) returns
// Filed=false with a non-empty Reason.
type AutoFileGate interface {
	MaybeFile(ctx context.Context, runID string, in AutoFileInput) (AutoFileResult, error)
}

// AutoFileInput is the LoopDriver-facing autofile-gate payload. The
// concrete adapter to internal/backlog.AutoFileGate.MaybeFile lives
// in the wiring layer (cmd/orion-cli or future server), keeping this
// package free of the backlog dep cycle.
type AutoFileInput struct {
	BindingID      string
	TrustMode      string
	AutoFile       bool
	Pattern        string
	DedupSignature string
	FileLine       string
	Title          string
	Body           string
}

// AutoFileResult mirrors backlog.MaybeFileResult.
type AutoFileResult struct {
	Filed      bool
	Reason     string
	IssueID    *uuid.UUID
	ExternalID string
}

// NormalizedIssueLookup is the read-side surface LoopDriver needs to
// answer "has Orion already filed a tracker issue for this signature?"
// (cross-tick dedup, §8.3) and "how deep is the eligible backlog?"
// (§15.3.1 quiescence gate). Mirrors
// NormalizedIssueRepo.ExistsOrionFiledByDedup + CountEligibleByRepo.
type NormalizedIssueLookup interface {
	ExistsOrionFiledByDedup(ctx context.Context, signature string) (bool, error)
	CountEligibleByRepo(ctx context.Context, repoID uuid.UUID) (int, error)
}

// LoopDriver orchestrates one SPEC §15.2 detection tick. v1 ships
// phases 2 (scan), 4 (cross-reference), 6 (autofile), and 7 (persist).
//
// Phase 1 (clone) is the caller's responsibility: LoopInput.RepoPath
// must point at an already-checked-out tree. Phase 3 (LLM inference)
// is deferred per PRD. Phase 5 (progressive cap) lands in E3-6.
// Phase 8 (teardown) is the caller's responsibility too.
//
// Quiescence (§15.3.1) is deferred to E3-4; this driver always emits
// phase=completed when scan + persist succeed and phase=failed on
// infrastructure failure.
type LoopDriver struct {
	Scanner          *Scanner
	Runs             *repos.DetectionRunRepo
	Findings         *repos.DetectionFindingRepo
	NormalizedIssues NormalizedIssueLookup
	AutoFileGate     AutoFileGate
}

// NewLoopDriver constructs a LoopDriver. All fields must be non-nil
// at Tick time; the constructor doesn't validate (deferred to Tick so
// composition errors surface at the first call site).
func NewLoopDriver(scanner *Scanner, runs *repos.DetectionRunRepo, findings *repos.DetectionFindingRepo, ni NormalizedIssueLookup, gate AutoFileGate) *LoopDriver {
	return &LoopDriver{
		Scanner:          scanner,
		Runs:             runs,
		Findings:         findings,
		NormalizedIssues: ni,
		AutoFileGate:     gate,
	}
}

// Tick runs one detection cycle for one binding. Returns the persisted
// run's id + summary counters. Always persists exactly one
// DetectionRun row so the run is observable even with zero findings or
// scan failure.
//
// Cross-reference logic (phase 4):
//   - For each finding, compute dedup.Signature(slug, canonical-callsite)
//   - If detection_findings already has this signature (this org): mark deduped=true
//   - Else if normalized_issue.orion_filed for this signature: mark deduped=true (already-tracked)
//   - Else: new gap; phase 6 autofile decides what to do
//
// Provenance counters per §15.4:
//   - orion_filed_processed: deduped via normalized_issue.orion_filed
//   - customer_filed_processed: 0 in v1 (tracker-level dedup not yet wired)
//   - polaris_prior_processed: 0 in v1 (Polaris write surface deferred to E7)
func (d *LoopDriver) Tick(ctx context.Context, in LoopInput) (TickResult, error) {
	if d.Scanner == nil || d.Runs == nil || d.Findings == nil || d.NormalizedIssues == nil {
		return TickResult{}, fmt.Errorf("%w: missing required dependency", ErrLoopMisconfigured)
	}
	if in.RepoPath == "" || in.Service == "" || in.BindingID == "" {
		return TickResult{}, fmt.Errorf("%w: RepoPath/Service/BindingID required", ErrLoopMisconfigured)
	}
	mode := in.Mode
	if mode == "" {
		mode = LoopModeFull
	}

	bindingUUID, err := uuid.Parse(in.BindingID)
	if err != nil {
		return TickResult{}, fmt.Errorf("%w: binding_id is not a uuid: %v", ErrLoopMisconfigured, err)
	}

	scanned, _, scanErr := d.Scanner.Run(ctx, ScanOptions{
		RepoPath: in.RepoPath,
		Service:  in.Service,
	})
	if scanErr != nil {
		return d.persistFailedRun(ctx, bindingUUID, mode, scanErr)
	}

	// §15.3.1 quiescence gate: if RepoID is known, check the eligible
	// backlog depth. When eligible == 0 AND scanner returned zero
	// findings, persist a phase=quiescent run row and short-circuit —
	// no cross-reference, no autofile, no per-finding ledger.
	if in.RepoID != "" {
		repoUUID, err := uuid.Parse(in.RepoID)
		if err != nil {
			return TickResult{}, fmt.Errorf("%w: repo_id is not a uuid: %v", ErrLoopMisconfigured, err)
		}
		eligible, err := d.NormalizedIssues.CountEligibleByRepo(ctx, repoUUID)
		if err != nil {
			return TickResult{}, fmt.Errorf("detection: count eligible backlog: %w", err)
		}
		if QuiescenceCheck(eligible, len(scanned)) {
			return d.persistQuiescentRun(ctx, bindingUUID, mode)
		}
	}

	// Phase 4: cross-reference each finding against prior detection
	// findings (this run's dedup ledger) AND against orion-filed
	// tracker issues (cross-tick dedup per §8.3 level 2).
	classified := make([]classifiedFinding, 0, len(scanned))
	newCount := 0
	dedupedCount := 0
	orionFiledHits := 0
	for _, f := range scanned {
		sig := computeSignature(f)
		var deduped bool
		var orionFiledHit bool

		if sig != "" {
			already, err := d.Findings.ExistsByDedupSignature(ctx, sig)
			if err != nil {
				return TickResult{}, fmt.Errorf("detection: dedup lookup (findings): %w", err)
			}
			if already {
				deduped = true
			}
		}
		if !deduped && sig != "" {
			alreadyFiled, err := d.NormalizedIssues.ExistsOrionFiledByDedup(ctx, sig)
			if err != nil {
				return TickResult{}, fmt.Errorf("detection: dedup lookup (orion-filed): %w", err)
			}
			if alreadyFiled {
				deduped = true
				orionFiledHit = true
			}
		}

		if deduped {
			dedupedCount++
			if orionFiledHit {
				orionFiledHits++
			}
		} else {
			newCount++
		}

		classified = append(classified, classifiedFinding{
			finding:        f,
			sig:            sig,
			deduped:        deduped,
			orionFiledHit:  orionFiledHit,
		})
	}

	// Phase 7a: persist the run row with the cross-reference counters.
	run := repos.DetectionRun{
		BindingID:           bindingUUID,
		Mode:                modeToRepos(mode),
		Phase:               repos.DetectionPhaseCompleted,
		FindingsTotal:       len(scanned),
		FindingsNew:         newCount,
		FindingsDeduped:     dedupedCount,
		OrionFiledProcessed: orionFiledHits,
	}
	persisted, err := d.Runs.Create(ctx, run)
	if err != nil {
		return TickResult{}, fmt.Errorf("detection: persist run: %w", err)
	}

	// Phase 6: autofile gate for non-deduped findings (only when gate
	// is wired; tests can leave it nil to focus on persistence).
	autoFiled := 0
	if d.AutoFileGate != nil {
		for i := range classified {
			if classified[i].deduped {
				continue
			}
			res, err := d.AutoFileGate.MaybeFile(ctx, persisted.ID.String(), AutoFileInput{
				BindingID:      in.BindingID,
				TrustMode:      in.TrustMode,
				AutoFile:       in.AutoFile,
				Pattern:        classified[i].finding.Slug,
				DedupSignature: classified[i].sig,
				FileLine:       fmt.Sprintf("%s:%d", classified[i].finding.File, classified[i].finding.Line),
				Title:          classified[i].finding.Title,
				Body:           classified[i].finding.Narrative,
			})
			if err != nil {
				// Gate infrastructure failure: surface but don't abort
				// the persist; record the run as completed and let the
				// caller see autoFiled < newCount.
				continue
			}
			if res.Filed {
				autoFiled++
			}
		}
	}

	// Phase 7b: persist the per-finding ledger.
	dbFindings := make([]repos.DetectionFinding, 0, len(classified))
	for _, c := range classified {
		var sigPtr *string
		if c.sig != "" {
			s := c.sig
			sigPtr = &s
		}
		dbFindings = append(dbFindings, repos.DetectionFinding{
			RunID:          persisted.ID,
			Slug:           c.finding.Slug,
			Title:          c.finding.Title,
			Category:       c.finding.Category,
			Confidence:     c.finding.Confidence,
			Severity:       severityFor(c.finding),
			ControlCodes:   append([]string(nil), c.finding.ControlCodes...),
			FilePath:       c.finding.File,
			LineNo:         c.finding.Line,
			Fingerprint:    c.finding.Fingerprint,
			DedupSignature: sigPtr,
			Deduped:        c.deduped,
		})
	}
	if len(dbFindings) > 0 {
		if _, err := d.Findings.CreateBatch(ctx, dbFindings); err != nil {
			return TickResult{}, fmt.Errorf("detection: persist findings: %w", err)
		}
	}

	return TickResult{
		RunID:           persisted.ID.String(),
		FindingsTotal:   len(scanned),
		FindingsNew:     newCount,
		FindingsDeduped: dedupedCount,
		AutoFiled:       autoFiled,
		Phase:           string(persisted.Phase),
	}, nil
}

// persistQuiescentRun records a phase=quiescent run with zero
// findings counters. Per SPEC §15.3.1, quiescent is a SUCCESS state
// (the customer's eligible backlog is drained AND the scan found
// nothing new). The customer-facing surface frames this positively
// rather than as a no-op.
func (d *LoopDriver) persistQuiescentRun(ctx context.Context, bindingID uuid.UUID, mode LoopMode) (TickResult, error) {
	run := repos.DetectionRun{
		BindingID: bindingID,
		Mode:      modeToRepos(mode),
		Phase:     repos.DetectionPhaseQuiescent,
		Quiescent: true,
	}
	persisted, err := d.Runs.Create(ctx, run)
	if err != nil {
		return TickResult{}, fmt.Errorf("detection: persist quiescent run: %w", err)
	}
	return TickResult{
		RunID: persisted.ID.String(),
		Phase: string(persisted.Phase),
	}, nil
}

// persistFailedRun records a phase=failed run row capturing the scan
// error message. Returns the result so callers can still emit a tick
// summary (rather than swallowing the error).
func (d *LoopDriver) persistFailedRun(ctx context.Context, bindingID uuid.UUID, mode LoopMode, scanErr error) (TickResult, error) {
	errMsg := scanErr.Error()
	run := repos.DetectionRun{
		BindingID:    bindingID,
		Mode:         modeToRepos(mode),
		Phase:        repos.DetectionPhaseFailed,
		ErrorMessage: &errMsg,
	}
	persisted, err := d.Runs.Create(ctx, run)
	if err != nil {
		// Both scan AND persist failed: surface the scan failure as
		// the canonical error (it's the root cause).
		return TickResult{}, fmt.Errorf("detection: scan failed AND persist failed: scan=%w persist=%v", scanErr, err)
	}
	return TickResult{
		RunID: persisted.ID.String(),
		Phase: string(persisted.Phase),
	}, scanErr
}

type classifiedFinding struct {
	finding       Finding
	sig           string
	deduped       bool
	orionFiledHit bool
}

// computeSignature derives a stable dedup signature from a finding.
//
// v1 limitation: rvl-cli does not yet emit function/receiver context,
// so we use the file path as the canonical-callsite "function" name.
// This loses dedup.Signature's rename-invariance property — a file
// rename will look like a new gap — but is correct enough for the
// within-SHA dedup short-circuit (§15.2 phase 4) until E9 wires real
// AST context. Documented as a known limitation in the SPEC §8.3
// commentary on dedup confidence.
func computeSignature(f Finding) string {
	if f.Slug == "" || f.File == "" {
		return ""
	}
	cs := dedup.Canonicalize(f.File, f.File, "")
	return dedup.Signature(f.Slug, cs)
}

// severityFor maps an rvl-cli Finding's impact/confidence pair to the
// detection_findings.severity column. v1 prefers explicit Impact when
// set; falls back to confidence otherwise so the row is never empty.
func severityFor(f Finding) string {
	if f.Impact != "" {
		return f.Impact
	}
	if f.Confidence != "" {
		return f.Confidence
	}
	return "medium"
}

// modeToRepos maps the package-local LoopMode to the
// repos.DetectionRunMode enum. The two are kept distinct so the repos
// package isn't an import-cycle leaf for callers who don't need it.
func modeToRepos(m LoopMode) repos.DetectionRunMode {
	switch m {
	case LoopModeIncremental:
		return repos.DetectionModeIncremental
	case LoopModePostMerge:
		return repos.DetectionModePostMerge
	default:
		return repos.DetectionModeFull
	}
}

