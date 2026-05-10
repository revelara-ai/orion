package verify

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// Applicator applies a unified diff to a workspace repo and verifies
// the build succeeds. v1 uses `git apply` for diff application and
// `go build ./...` for build verification. Both run inside the
// workspace repo path; callers are responsible for ensuring that path
// is inside their sandbox root (SPEC §10.4).
type Applicator struct {
	// GoBinary lets tests substitute a stub binary. Empty defaults to "go".
	GoBinary string

	// GitBinary lets tests substitute a stub binary. Empty defaults to "git".
	GitBinary string
}

// Apply runs `git apply` against the workspace repo. workspaceRepo
// MUST be an absolute path; the diff is read from stdin so the file
// system is unaffected outside the workspace.
func (a Applicator) Apply(ctx context.Context, workspaceRepo, unifiedDiff string) error {
	if !filepath.IsAbs(workspaceRepo) {
		return fmt.Errorf("%w: workspaceRepo must be absolute", ErrInvalidInputs)
	}
	if strings.TrimSpace(unifiedDiff) == "" {
		return fmt.Errorf("%w: empty diff", ErrPatchApply)
	}
	bin := a.GitBinary
	if bin == "" {
		bin = "git"
	}
	cmd := exec.CommandContext(ctx, bin, "apply", "--whitespace=nowarn", "-") //#nosec G204 -- bin is a known git/test stub; args static
	cmd.Dir = workspaceRepo
	cmd.Stdin = strings.NewReader(unifiedDiff)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: git apply: %v: %s", ErrPatchApply, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Build runs `go build ./...` in the workspace repo and returns nil on
// success. Wraps non-zero exits in ErrBuildFailed.
func (a Applicator) Build(ctx context.Context, workspaceRepo string) error {
	if !filepath.IsAbs(workspaceRepo) {
		return fmt.Errorf("%w: workspaceRepo must be absolute", ErrInvalidInputs)
	}
	bin := a.GoBinary
	if bin == "" {
		bin = "go"
	}
	cmd := exec.CommandContext(ctx, bin, "build", "./...") //#nosec G204 -- bin is go binary, args static
	cmd.Dir = workspaceRepo
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: go build: %v: %s", ErrBuildFailed, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Reset undoes any in-tree changes via `git checkout -- .`. Used to
// restore the workspace between trials.
func (a Applicator) Reset(ctx context.Context, workspaceRepo string) error {
	bin := a.GitBinary
	if bin == "" {
		bin = "git"
	}
	cmd := exec.CommandContext(ctx, bin, "checkout", "--", ".") //#nosec G204 -- bin is git binary, args static
	cmd.Dir = workspaceRepo
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: git checkout: %v: %s", ErrPatchApply, err, strings.TrimSpace(string(out)))
	}
	return nil
}
