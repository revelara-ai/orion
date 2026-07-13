package conductor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/revelara-ai/orion/internal/proof"
	"github.com/revelara-ai/orion/internal/reliabilityscan"
)

// Net-negative-refinement detector (or-mvr.5, inc-qdi C10): "keep prompting
// until it works" is not a control strategy — research shows critical
// vulnerabilities RISING across refinement passes. Each retry is compared to
// the prior attempt on deterministic security/quality signals; a pass that
// made the artifact WORSE terminates the loop (escalate with the regression
// named) instead of prompting for more.

// refinementSnapshot is one attempt's comparable signal set. Zero values mean
// "not measured on this path" — only signals present on BOTH sides compare.
type refinementSnapshot struct {
	SecretFindings     int // hardcoded secrets introduced (security)
	ScanFindings       int // reliability-scan findings on the artifact
	NewTestFailures    int // change flow: regressions named by the evidence diff
	PassingObligations int // greenfield: obligations proven passing
	hasObligations     bool
}

// refinementRegressed reports whether cur is a NET-NEGATIVE refinement of
// prev, with an auditable reason naming every regressed axis. Conservative
// and deterministic: only strict degradation terminates —
//   - any security regression (secrets up),
//   - more named test regressions than the attempt it "fixed",
//   - more reliability findings,
//   - fewer passing obligations.
func refinementRegressed(prev, cur refinementSnapshot) (bool, string) {
	var axes []string
	if cur.SecretFindings > prev.SecretFindings {
		axes = append(axes, fmt.Sprintf("hardcoded secrets %d→%d", prev.SecretFindings, cur.SecretFindings))
	}
	if cur.NewTestFailures > prev.NewTestFailures {
		axes = append(axes, fmt.Sprintf("test regressions %d→%d", prev.NewTestFailures, cur.NewTestFailures))
	}
	if cur.ScanFindings > prev.ScanFindings {
		axes = append(axes, fmt.Sprintf("reliability findings %d→%d", prev.ScanFindings, cur.ScanFindings))
	}
	if prev.hasObligations && cur.hasObligations && cur.PassingObligations < prev.PassingObligations {
		axes = append(axes, fmt.Sprintf("passing obligations %d→%d", prev.PassingObligations, cur.PassingObligations))
	}
	if len(axes) == 0 {
		return false, ""
	}
	return true, "refinement degraded the artifact vs the prior attempt: " + strings.Join(axes, "; ")
}

// obligationSnapshot derives the greenfield quality signal from a proof report.
func obligationSnapshot(report proof.Report) (passing int, has bool) {
	if len(report.ObligationResults) == 0 {
		return 0, false
	}
	for _, o := range report.ObligationResults {
		if o.Passed {
			passing++
		}
	}
	return passing, true
}

// artifactScanFindings counts reliability-scan findings over the artifact's
// entrypoint — the per-attempt security/quality scan signal.
func artifactScanFindings(buildDir string) int {
	b, err := os.ReadFile(filepath.Join(buildDir, "main.go"))
	if err != nil {
		return 0
	}
	return len(reliabilityscan.ScanSource(string(b)))
}
