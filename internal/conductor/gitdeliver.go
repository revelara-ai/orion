package conductor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
	"github.com/revelara-ai/orion/internal/worktree"
)

// GitDeliverEnabled reports whether git delivery is opted in (ORION_GIT_DELIVERY
// truthy). It is OPT-IN so a build never commits into a repo unless the developer
// asked — tests + the harness's own repo are never auto-committed.
func GitDeliverEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("ORION_GIT_DELIVERY"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// GitDelivery is the result of committing proven code into the developer's repo.
type GitDelivery struct {
	Branch string // the Orion branch the proven code was committed to
	Commit string // short hash
	Path   string // the worktree path where the proven code is checked out
}

// gitIn runs git in dir. Git is trusted plumbing on the developer's own repo; the
// COMMIT is run with --no-verify so a target repo's hooks never execute during
// delivery, and with an explicit Orion identity so it works in a fresh/unconfigured repo.
func gitIn(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...) // #nosec G204 -- fixed binary; callers pass vetted git verbs
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// GitRoot returns the git top-level containing dir, or "" if dir is not in a git repo.
func GitRoot(ctx context.Context, dir string) string {
	root, err := gitIn(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return ""
	}
	return root
}

// GitDeliver commits the proven, exported code onto an Orion branch in a WORKTREE of
// repoRoot — NOT the developer's active working tree, so their in-progress work is
// undisturbed. The branch (orion-<slug>) persists for review/PR; the returned worktree
// path is where the proven code is checked out for inspection. Re-delivery resumes the
// same branch and adds a commit (no-op if the code is unchanged). The branch starts
// from the current HEAD, so no assumption about the base branch name.
func GitDeliver(ctx context.Context, repoRoot string, store *contextstore.Store, buildDir string, es spec.ExecutableSpec) (GitDelivery, error) {
	slug := serviceSlug(es)
	issueID := "orion-" + slug
	mgr := worktree.New(repoRoot, store)

	wt, err := mgr.Create(ctx, issueID, "HEAD")
	if err != nil {
		// Branch/worktree exists from a prior delivery → resume it.
		if wt, err = mgr.CreateResume(ctx, issueID, issueID); err != nil {
			return GitDelivery{}, fmt.Errorf("worktree for delivery: %w", err)
		}
	}

	if _, err := ExportProvenCode(buildDir, filepath.Join(wt.Path, slug), es); err != nil {
		return GitDelivery{}, fmt.Errorf("export into worktree: %w", err)
	}
	if _, err := gitIn(ctx, wt.Path, "add", "-A"); err != nil {
		return GitDelivery{}, err
	}
	// Nothing staged (idempotent re-delivery of identical code) → return the head.
	if st, _ := gitIn(ctx, wt.Path, "status", "--porcelain"); st == "" {
		hash, _ := gitIn(ctx, wt.Path, "rev-parse", "--short", "HEAD")
		return GitDelivery{Branch: wt.Branch, Commit: hash, Path: wt.Path}, nil
	}
	if _, err := gitIn(ctx, wt.Path,
		"-c", "user.name=Orion", "-c", "user.email=orion@revelara.ai", "-c", "commit.gpgsign=false",
		"commit", "--no-verify", "-m", gitProvenanceMessage(es)); err != nil {
		return GitDelivery{}, err
	}
	hash, _ := gitIn(ctx, wt.Path, "rev-parse", "--short", "HEAD")
	return GitDelivery{Branch: wt.Branch, Commit: hash, Path: wt.Path}, nil
}

func gitProvenanceMessage(es spec.ExecutableSpec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "orion: %s (proven)\n\n", strings.TrimSpace(es.Intent))
	fmt.Fprintf(&b, "Spec anchor: %s\n", shortHash(es.Hash))
	fmt.Fprintf(&b, "Route: %s · Port: %d · Format: %s\n", es.ResponseContract.Route, es.ResponseContract.Port, es.ResponseContract.Format())
	b.WriteString("Verdict: Accept (behavioral + empirical + hazard)\n\n")
	b.WriteString("Generated and independently proven by Orion.\n")
	return b.String()
}
