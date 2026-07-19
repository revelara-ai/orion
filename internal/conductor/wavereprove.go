package conductor

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/revelara-ai/orion/internal/decomposer"
	"github.com/revelara-ai/orion/internal/proof/safeenv"
)

// scopedWaveReprove (or-7et.4b) restricts a wave's re-proof to the packages
// its merged diff touches — the change-flow blast-radius posture — with the
// soundness escalations: an UNDECLARED (empty) cluster FileScope in the wave,
// or a diff touching go.mod/go.sum, forces the FULL re-proof for that wave.
// The scoped pass is the regression posture (build the whole tree, test the
// affected packages); the mandatory FULL bookend still proves the delivered
// head whole.
func scopedWaveReprove(full func(ctx context.Context, dir string) (bool, error), language string) WaveReprove {
	return func(ctx context.Context, dir string, wave []decomposer.TaskCluster, preRev string) (bool, error) {
		if full == nil {
			return true, nil
		}
		// or-4y7.9: the scoped shortcut below is a GO blast-radius optimization
		// (`go build ./...` + package-scoped `go test`). Any other ratified
		// language takes the FULL language-aware reprove (proof.ProveAll
		// dispatches by contract language) — never a Go build over non-Go trees.
		if language != "" && language != "go" {
			return full(ctx, dir)
		}
		// Soundness rule 1: an undeclared FileScope means the cluster may have
		// touched anything — scope cannot be trusted; escalate to full.
		for _, cl := range wave {
			if len(clusterLeaseScope(cl)) == 0 {
				return full(ctx, dir)
			}
		}
		diff, err := gitIn(ctx, dir, "diff", "--name-only", preRev+"..HEAD")
		if err != nil {
			return full(ctx, dir) // can't establish the blast radius — full
		}
		dirs := map[string]bool{}
		for _, f := range strings.Fields(diff) {
			// Soundness rule 2: dependency-graph changes invalidate every scope.
			base := filepath.Base(f)
			if base == "go.mod" || base == "go.sum" {
				return full(ctx, dir)
			}
			d := filepath.Dir(f)
			if d == "." {
				d = ""
			}
			dirs[d] = true
		}
		if len(dirs) == 0 {
			return true, nil // an empty wave diff proves nothing new
		}
		// Scoped regression: whole-tree build + tests over the touched packages.
		if out, rerr := goRun(ctx, dir, "build", "./..."); rerr != nil {
			return false, fmt.Errorf("wave build failed:\n%s", out)
		}
		patterns := make([]string, 0, len(dirs))
		for d := range dirs {
			if d == "" {
				patterns = append(patterns, ".")
				continue
			}
			patterns = append(patterns, "./"+d+"/...")
		}
		sort.Strings(patterns)
		if out, rerr := goRun(ctx, dir, append([]string{"test"}, patterns...)...); rerr != nil {
			return false, fmt.Errorf("wave-scoped tests failed:\n%s", out)
		}
		return true, nil
	}
}

// goRun executes `go <args...>` at dir under the proof-exec scrubbed env.
func goRun(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "go", args...) // #nosec G204 -- fixed toolchain binary; args are harness-built patterns
	cmd.Dir = dir
	cmd.Env = safeenv.Build()
	out, err := cmd.CombinedOutput()
	return string(out), err
}
