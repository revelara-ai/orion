package conductor

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/proof"
)

// maxFailureModesInContext caps the KNOWN FAILURE MODES section — the newest
// classes matter most; an unbounded list would erode the window it protects.
const maxFailureModesInContext = 10

// failureCategory derives the deduped failure-mode category from the proof
// report: the first dissenting mode ("behavioral", "empirical", "hazard",
// "alignment:*"), else the bare verdict class.
func failureCategory(report proof.Report) string {
	if d := report.Outcome.Dissenting; len(d) > 0 {
		if i := strings.IndexByte(d[0], ':'); i > 0 {
			return d[0][:i]
		}
		return d[0]
	}
	return "proof-" + strings.ToLower(string(report.Outcome.Verdict))
}

// failureSymptom distills analyzeFailure's output into the symptom class: the
// first substantive line after the verdict header.
func failureSymptom(analysis string) string {
	for _, line := range strings.Split(analysis, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Proof verdict:") {
			continue
		}
		return line
	}
	return "unspecified"
}

// knownFailureModesSection renders the CONSULT half of or-gb1.3: the active
// project's recorded failure modes (deduped classes, newest first, capped) as
// a generation-context section. Harness-derived rows — TrustProof posture.
// Best-effort: any miss yields "" and the build proceeds.
func knownFailureModesSection(ctx context.Context, store *contextstore.Store, stateMu *sync.Mutex) string {
	if store == nil {
		return ""
	}
	var modes []contextstore.FailureMode
	withLock(stateMu, func() {
		_ = store.WithTx(ctx, func(tx *contextstore.Tx) error {
			proj, e := tx.Projects().Active(ctx)
			if e != nil {
				return e
			}
			ms, e := tx.FailureModes().ListForProject(ctx, proj.ID)
			modes = ms
			return e
		})
	})
	if len(modes) == 0 {
		return ""
	}
	if len(modes) > maxFailureModesInContext {
		modes = modes[:maxFailureModesInContext]
	}
	var b strings.Builder
	b.WriteString("# KNOWN FAILURE MODES (recorded by the proof harness — do NOT repeat these)\n")
	for _, m := range modes {
		fmt.Fprintf(&b, "- [%s] %s — %s\n", m.Category, m.ComponentType, m.SymptomClass)
	}
	return b.String()
}
