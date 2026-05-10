// Package report renders the verification report as markdown for the
// PR body per SPEC §16.1 and §12.8.
package report

import (
	"github.com/revelara-ai/orion/internal/harness"
	"github.com/revelara-ai/orion/internal/verify"
)

// Issue describes the source tracker issue.
type Issue struct {
	Title      string
	ExternalID string
}

// AcceptedPatch is the report-side projection of one accepted patch +
// its verdict. Mirrors orchestration.VerifiedPatch but in the report
// package's vocabulary so report has no upward dependency.
type AcceptedPatch struct {
	GapID      string
	ControlID  string
	Pattern    string
	TargetPath string
	LLMModel   string
	LLMSeed    int64
	Verdict    verify.Verdict
}

// Data is the input to Render.
type Data struct {
	RunID                string
	Issue                Issue
	Harness              *harness.Harness
	AcceptedPatches      []AcceptedPatch
	BundleURLPlaceholder string
}
