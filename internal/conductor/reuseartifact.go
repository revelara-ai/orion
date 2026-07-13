package conductor

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// Prefer landing an existing proven artifact over re-deriving (or-mvs): a
// prior run that behaved left a COMMITTED orion-change branch; when that
// branch still fast-forwards onto the developer's HEAD, landing it is
// deterministic and cheap — re-running generation+proof for the same outcome
// is waste. The recommendation is the DEFAULT with an explicit override
// (force_rederive on the change tools, ORION_REDERIVE=1 on the CLI), never a
// neutral A/B.

type forceRederiveKey struct{}

// WithForceRederive marks the context: skip artifact reuse, re-derive fresh
// (the deliberate retry-a-failed-generation case).
func WithForceRederive(ctx context.Context) context.Context {
	return context.WithValue(ctx, forceRederiveKey{}, true)
}

func forceRederive(ctx context.Context) bool {
	if v, _ := ctx.Value(forceRederiveKey{}).(bool); v {
		return true
	}
	return os.Getenv("ORION_REDERIVE") == "1"
}

// existingProvenArtifact reports a committed orion-change branch for this
// intent that still fast-forwards onto HEAD. Non-ff (the base moved into
// divergence) or absent branches yield ok=false — re-derivation is then the
// right call, exactly as the bead scopes it.
func existingProvenArtifact(ctx context.Context, repoRoot, intent string) (string, bool) {
	slug := slugFromIntent(intent)
	if slug == "" {
		return "", false
	}
	// The first collision-suffixed ids too: a retried intent lands on -2/-3.
	for _, branch := range []string{"orion-change-" + slug, "orion-change-" + slug + "-2", "orion-change-" + slug + "-3"} {
		if _, err := gitIn(ctx, repoRoot, "rev-parse", "--verify", "refs/heads/"+branch); err != nil {
			continue
		}
		// Committed work: the branch tip is ahead of HEAD…
		if out, err := gitIn(ctx, repoRoot, "rev-list", "--count", "HEAD.."+branch); err != nil || strings.TrimSpace(out) == "0" {
			continue
		}
		// …and HEAD is its ancestor, so an ff-only land applies cleanly.
		if _, err := gitIn(ctx, repoRoot, "merge-base", "--is-ancestor", "HEAD", branch); err != nil {
			continue
		}
		return branch, true
	}
	return "", false
}

// reuseRecommendation renders the default-with-override guidance.
func reuseRecommendation(branch string) string {
	return fmt.Sprintf("an existing PROVEN artifact already implements this change: branch %s fast-forwards cleanly onto HEAD. "+
		"RECOMMENDED: land it (finish_change with branch %q — deterministic, no generation/proof cost). "+
		"Re-derive only to deliberately retry generation: pass force_rederive (tools) or ORION_REDERIVE=1 (CLI).", branch, branch)
}
