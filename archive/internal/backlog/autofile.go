// Package backlog also implements SPEC §8.7 — the auto-file gate
// that decides whether the scan loop may file a new tracker issue
// for a freshly-detected control gap. The gate is intentionally
// decoupled from the scan loop trigger (which lives in E3 detection)
// so this slice can ship tested and ready to wire.
package backlog

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/revelara-ai/orion/internal/repos"
	"github.com/revelara-ai/orion/internal/trackers"
)

// TrustMode mirrors SPEC §6.4 / §8.7 trust modes. v1 surfaces all
// four since the gate's branching depends on the mode; the
// downstream E5 ConnectedRepo slice wires this through from the
// stored trust_mode column.
type TrustMode string

// Trust modes.
const (
	TrustModeShadow  TrustMode = "shadow"
	TrustModeDraft   TrustMode = "draft"
	TrustModeStaging TrustMode = "staging"
	TrustModeFull    TrustMode = "full"
)

// AutoFileCaps holds the per-run + per-24h limits from §8.7. Default
// values match the spec's defaults; callers may override per-org or
// per-binding.
type AutoFileCaps struct {
	MaxPerRun  int
	MaxPer24h  int
	WindowSize time.Duration // for tests; defaults to 24h
}

// DefaultCapsFor returns the §8.7 defaults for the given trust mode.
// shadow + draft return zero caps (filing is forbidden); staging is
// reduced; full is normal.
func DefaultCapsFor(mode TrustMode) AutoFileCaps {
	switch mode {
	case TrustModeFull:
		return AutoFileCaps{MaxPerRun: 25, MaxPer24h: 100, WindowSize: 24 * time.Hour}
	case TrustModeStaging:
		return AutoFileCaps{MaxPerRun: 10, MaxPer24h: 30, WindowSize: 24 * time.Hour}
	}
	return AutoFileCaps{MaxPerRun: 0, MaxPer24h: 0, WindowSize: 24 * time.Hour}
}

// Finding is the per-gap input to MaybeFile. Constructed by the scan
// loop (E3); the autofile gate consumes only the fields it needs.
type Finding struct {
	// Pattern is the rvl-cli pattern name (e.g. "missing_timeout").
	Pattern string

	// PolarisRiskID links to the Polaris-tracked risk, when known.
	PolarisRiskID *uuid.UUID

	// DedupSignature is dedup.Signature(pattern, callsite) computed
	// upstream. MaybeFile checks the §8.3 level-2 dedup against
	// already-orion-filed issues before calling adapter.Create.
	DedupSignature string

	// FileLine is the affected source location (e.g. "internal/svc/foo.go:42").
	FileLine string

	// InventoryContext is the §8.7 yield blurb (e.g. "gap 3 of 18 detected").
	InventoryContext string

	// Title and Body are the draft fields adapter.Create consumes.
	// The caller (scan loop) constructs these to include the §8.7
	// required content (pattern, file:line, dedup signature,
	// inventory context, Polaris risk URL).
	Title string
	Body  string
}

// MaybeFileResult is the gate's return value. Filed=true means
// adapter.Create was invoked and the issue exists upstream; the
// downstream caller persists the NormalizedIssue row separately.
type MaybeFileResult struct {
	Filed      bool
	Reason     string // human-readable: why the gate decided to file or skip
	IssueID    *uuid.UUID
	ExternalID string
}

// AutoFileGate is the §8.7 decision surface.
type AutoFileGate struct {
	Adapter trackers.TrackerAdapter
	Counts  *repos.AutoFileCountsRepo

	// CheckDedup, when non-nil, queries for an already-orion-filed
	// issue with this signature. Production wires
	// NormalizedIssueRepo.ExistsOrionFiledByDedup.
	CheckDedup func(ctx context.Context, signature string) (bool, error)

	// PatternTrustAbove, when non-nil, gates filing on per-pattern
	// trust score per §8.7 rule 3. v1 default (nil) treats every
	// pattern as above the threshold; E9 wires the real score.
	PatternTrustAbove func(pattern string) bool

	// Now is the clock source; tests override for window math.
	Now func() time.Time
}

// MaybeFile evaluates §8.7 gates and, when all pass, calls
// adapter.Create. Returns Filed=false with a human-readable Reason
// for every skip path.
func (g *AutoFileGate) MaybeFile(ctx context.Context, runID string, binding repos.TrackerBinding, trustMode TrustMode, autoFile bool, caps AutoFileCaps, finding Finding) (MaybeFileResult, error) {
	if g == nil {
		return MaybeFileResult{}, errors.New("backlog: autofile gate not wired")
	}
	if !autoFile {
		return MaybeFileResult{Reason: "binding.auto_file=false"}, nil
	}
	// Trust-mode gate: shadow + draft never file.
	if trustMode == TrustModeShadow || trustMode == TrustModeDraft {
		return MaybeFileResult{Reason: fmt.Sprintf("trust mode %q does not auto-file", trustMode)}, nil
	}

	// Caps gate: per-run + per-24h.
	if caps.MaxPerRun <= 0 || caps.MaxPer24h <= 0 {
		return MaybeFileResult{Reason: "caps are zero for trust mode"}, nil
	}
	if g.Counts != nil {
		runCount, err := g.Counts.CountByRun(ctx, runID)
		if err != nil {
			return MaybeFileResult{}, err
		}
		if runCount >= caps.MaxPerRun {
			return MaybeFileResult{Reason: fmt.Sprintf("per-run cap reached (%d)", runCount)}, nil
		}
		window := caps.WindowSize
		if window <= 0 {
			window = 24 * time.Hour
		}
		windowCount, err := g.Counts.CountInWindow(ctx, window)
		if err != nil {
			return MaybeFileResult{}, err
		}
		if windowCount >= caps.MaxPer24h {
			return MaybeFileResult{Reason: fmt.Sprintf("per-24h cap reached (%d)", windowCount)}, nil
		}
	}

	// Pattern trust gate (§8.7 rule 3).
	if g.PatternTrustAbove != nil && !g.PatternTrustAbove(finding.Pattern) {
		return MaybeFileResult{Reason: fmt.Sprintf("pattern %q below trust threshold", finding.Pattern)}, nil
	}

	// §8.3 level-2 semantic dedup: if an Orion-filed issue with this
	// signature already exists, comment on it (caller's job) rather
	// than file a new one.
	if g.CheckDedup != nil && finding.DedupSignature != "" {
		exists, err := g.CheckDedup(ctx, finding.DedupSignature)
		if err != nil {
			return MaybeFileResult{}, err
		}
		if exists {
			return MaybeFileResult{Reason: "dedup hit on existing orion-filed issue"}, nil
		}
	}

	// All gates pass; call the adapter.
	draft := trackers.IssueDraft{
		Title: finding.Title,
		Body:  finding.Body,
		Labels: append([]string{"orion-filed"},
			extractAutoFileLabels(binding)...),
	}
	created, err := g.Adapter.Create(ctx, asAdapterBinding(binding), draft)
	if err != nil {
		return MaybeFileResult{}, fmt.Errorf("backlog: autofile create: %w", err)
	}

	// Record the filed count.
	if g.Counts != nil {
		if err := g.Counts.Record(ctx, repos.AutoFileEntry{
			RunID:   runID,
			Pattern: finding.Pattern,
		}); err != nil {
			return MaybeFileResult{Filed: true, ExternalID: created.ExternalID, Reason: fmt.Sprintf("filed but cap record failed: %v", err)}, nil
		}
	}

	return MaybeFileResult{
		Filed:      true,
		Reason:     "filed",
		ExternalID: created.ExternalID,
	}, nil
}

// extractAutoFileLabels reads the binding's optional auto_file_labels
// list from its config map. Missing returns nil.
func extractAutoFileLabels(binding repos.TrackerBinding) []string {
	v, ok := binding.Config["auto_file_labels"]
	if !ok {
		return nil
	}
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, x := range t {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// asAdapterBinding converts the repo-layer TrackerBinding to the
// adapter-layer one. v1: credentials must already be resolved by
// the caller; the gate does not perform credential resolution.
func asAdapterBinding(b repos.TrackerBinding) trackers.TrackerBinding {
	return trackers.TrackerBinding{
		ID:     b.ID,
		OrgID:  b.OrgID,
		RepoID: b.RepoID,
		Kind:   trackers.TrackerKind(b.Kind),
		Config: b.Config,
	}
}
