package selfevolve

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/revelara-ai/orion/internal/memory"
	"github.com/revelara-ai/orion/internal/proof/safeenv"
)

// Stage-3 hypothesis verification (or-gb1.4): a generation-tier hypothesis
// (a distilled rule, a candidate procedure) earns proof trust ONLY by passing
// a deterministic check executed under the harness's control. First-writer-
// wins holds throughout: verification writes a NEW TrustProof item whose
// content cites the executed check and the source hypothesis id — the
// original row's trust_tier is never mutated. A failing check leaves the
// hypothesis untouched (still a quarantined candidate, now with no claim).

// Check is a deterministic verification the harness can execute: a command
// whose zero exit confirms the hypothesis.
type Check struct {
	Name string   // human-readable check name, cited in the verified item
	Cmd  []string // argv; exit 0 = hypothesis confirmed
	Dir  string   // working directory
}

// Runner executes a Check; a nil error means the check passed. Injected so
// tests (and future sandboxes) control execution.
type Runner func(ctx context.Context, c Check) error

// CmdRunner executes the check as a subprocess with the proof-grade scrubbed
// environment (safeenv — never os.Environ) and a hard timeout.
func CmdRunner(timeout time.Duration) Runner {
	return func(ctx context.Context, c Check) error {
		if len(c.Cmd) == 0 {
			return errors.New("empty check command")
		}
		cctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		cmd := exec.CommandContext(cctx, c.Cmd[0], c.Cmd[1:]...) // #nosec G204 -- skilleval fixture command from PROOF-tier evidence, never agent-authored at runtime
		cmd.Dir = c.Dir
		cmd.Env = safeenv.Build()
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("check %q failed: %w\n%s", c.Name, err, out)
		}
		return nil
	}
}

// VerifyAndPromote executes the deterministic check for a generation-tier
// hypothesis and, on pass, writes a NEW TrustProof item citing the check and
// the source hypothesis as provenance. Returns the new item's id.
// On check failure the hypothesis is left untouched and an error is returned.
func VerifyAndPromote(ctx context.Context, mem *memory.Store, hypothesisID string, c Check, run Runner) (string, error) {
	if mem == nil || run == nil {
		return "", errors.New("verify: memory store and runner are required")
	}
	hyp, ok, err := mem.Get(ctx, hypothesisID)
	if err != nil {
		return "", fmt.Errorf("verify: load hypothesis: %w", err)
	}
	if !ok {
		return "", fmt.Errorf("verify: hypothesis %s not found", hypothesisID)
	}
	if hyp.TrustTier == memory.TrustProof {
		return "", fmt.Errorf("verify: %s is already proof-tier — nothing to verify", hypothesisID)
	}
	if err := run(ctx, c); err != nil {
		// The hypothesis stays exactly as it was: unverified, quarantined.
		return "", fmt.Errorf("verify: hypothesis %s NOT confirmed: %w", hypothesisID, err)
	}
	content := fmt.Sprintf("verified: %s\nprovenance: deterministic check %q (%s) passed; source hypothesis %s",
		strings.TrimSpace(hyp.Content), c.Name, strings.Join(c.Cmd, " "), hypothesisID)
	id, err := mem.Write(ctx, memory.Item{
		Tier:      memory.LTM,
		Kind:      memory.KindVerified,
		Content:   content,
		TrustTier: memory.TrustProof, // earned: the harness executed the check itself
		Heat:      1.0,
	})
	if err != nil {
		return "", fmt.Errorf("verify: write verified item: %w", err)
	}
	return id, nil
}
