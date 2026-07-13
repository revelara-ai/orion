package conductor

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/revelara-ai/orion/internal/contextstore"
)

// Deterministic hazard-preservation gate (or-06lr): a brownfield change must
// PRESERVE the ratified STAMP baseline's CONTROLLED unsafe-control-actions.
// The check is a token-presence differential — no LLM, no judgment: a
// controlled UCA's code token that existed in the tree BEFORE the change and
// is GONE after it means the control vanished. Tokens already absent before
// the change are a stale baseline, not this change's fault — surfaced, never
// blocking. No ratified baseline at all → a VISIBLE advisory skip.

// hazardViolation is one vanished control.
type hazardViolation struct {
	UCAID, Hazard, Token string
}

// loadControlledUCAs reads the brownfield project's ratified baseline.
func loadControlledUCAs(ctx context.Context, store *contextstore.Store) []contextstore.RatifiedUCA {
	if store == nil {
		return nil
	}
	var out []contextstore.RatifiedUCA
	_ = store.WithTx(ctx, func(tx *contextstore.Tx) error {
		pid, err := tx.Projects().GetOrCreateReserved(ctx, contextstore.BrownfieldProjectName, "brownfield")
		if err != nil {
			return err
		}
		ucas, err := tx.RatifiedUCAs().ListForProject(ctx, pid)
		if err != nil {
			return err
		}
		for _, u := range ucas {
			if u.Disposition == "controlled" {
				out = append(out, u)
			}
		}
		return nil
	})
	return out
}

// hazardGate diffs token presence: beforeDir is the pre-change tree (the
// developer's repo at HEAD), afterDir the post-change worktree. Returns the
// vanished controls + the stale tokens (absent even before — reported, not
// blocking).
func hazardGate(ucas []contextstore.RatifiedUCA, beforeDir, afterDir string) (violations []hazardViolation, stale []string) {
	for _, u := range ucas {
		for _, tok := range u.CodeTokens {
			if strings.TrimSpace(tok) == "" {
				continue
			}
			if !tokenPresent(beforeDir, tok) {
				stale = append(stale, u.UCAID+":"+tok)
				continue
			}
			if !tokenPresent(afterDir, tok) {
				violations = append(violations, hazardViolation{UCAID: u.UCAID, Hazard: u.Hazard, Token: tok})
			}
		}
	}
	return violations, stale
}

// tokenPresent reports whether any source file under dir contains tok.
// Skips .git and harness scratch; bounded reads (source files are small).
func tokenPresent(dir, tok string) bool {
	found := false
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || found {
			return filepath.SkipAll
		}
		name := d.Name()
		if d.IsDir() {
			if name == ".git" || strings.HasPrefix(name, ".orion") || name == "node_modules" || name == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if st, serr := d.Info(); serr != nil || st.Size() > 2<<20 {
			return nil // skip unreadable/huge files
		}
		b, rerr := os.ReadFile(path) // #nosec G304 -- harness-owned trees
		if rerr == nil && strings.Contains(string(b), tok) {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}
