package conductor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/actuation"
)

// landFixtureRepo builds a repo with main + a committed orion-change branch
// one commit ahead; returns (root, branch). moveBase additionally advances
// main after the branch was cut, making the branch NOT a fast-forward.
func landFixtureRepo(t *testing.T, moveBase bool) (string, string) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Skipf("git unavailable: %v: %s", err, out)
		}
		return string(out)
	}
	run("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-m", "base")

	branch := "orion-change-fix"
	run("checkout", "-b", branch)
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("change\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-m", "proven change")
	run("checkout", "main")

	if moveBase {
		if err := os.WriteFile(filepath.Join(dir, "c.txt"), []byte("moved\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		run("add", "-A")
		run("commit", "-m", "base moved")
	}
	return dir, branch
}

// The consolidated workflow, happy path: ff-land onto main + branch reclaimed,
// one call, no prompts. (No .beads here, so the close step is skipped.)
func TestLandProvenChangeFastForwardsAndReclaims(t *testing.T) {
	root, branch := landFixtureRepo(t, false)
	summary, err := LandProvenChange(context.Background(), root, nil, actuation.RedButton{}, branch, "")
	if err != nil {
		t.Fatalf("land: %v", err)
	}
	if !strings.Contains(summary, "landed "+branch) {
		t.Fatalf("summary must state the landing: %s", summary)
	}
	if _, err := os.Stat(filepath.Join(root, "b.txt")); err != nil {
		t.Fatal("change did not land on main")
	}
	out, _ := exec.Command("git", "-C", root, "branch", "--list", branch).CombinedOutput()
	if strings.TrimSpace(string(out)) != "" {
		t.Fatalf("review branch must be reclaimed after landing, still have: %s", out)
	}
}

// The ff-only stance is load-bearing: a moved base means a STALE proof — the
// land must refuse and teach re-proving, never hand-merge.
func TestLandProvenChangeRefusesStaleBase(t *testing.T) {
	root, branch := landFixtureRepo(t, true)
	_, err := LandProvenChange(context.Background(), root, nil, actuation.RedButton{}, branch, "")
	if err == nil {
		t.Fatal("a non-fast-forward land must refuse")
	}
	if !strings.Contains(err.Error(), "STALE") || !strings.Contains(err.Error(), "Re-run the change flow") {
		t.Fatalf("refusal must teach the stale-proof recovery: %v", err)
	}
	if _, serr := os.Stat(filepath.Join(root, "b.txt")); !os.IsNotExist(serr) {
		t.Fatal("nothing may land on a stale base")
	}
}

// Red button: engaged → the land refuses before touching git.
func TestLandProvenChangeHonorsRedButton(t *testing.T) {
	root, branch := landFixtureRepo(t, false)
	rbPath := filepath.Join(t.TempDir(), "red_button")
	rb := actuation.RedButton{Path: rbPath}
	if err := rb.Engage(); err != nil {
		t.Fatal(err)
	}
	if _, err := LandProvenChange(context.Background(), root, nil, rb, branch, ""); err == nil {
		t.Fatal("an engaged red button must refuse the land")
	}
	if _, serr := os.Stat(filepath.Join(root, "b.txt")); !os.IsNotExist(serr) {
		t.Fatal("red button engaged but the change landed anyway")
	}
}

// citedIssue's exactly-one rule: zero or ambiguous citations never close
// anything; the single cited id must also be readable before it counts.
func TestCitedIssueExactlyOneRule(t *testing.T) {
	// This repo IS a beads workspace, so bd show runs for real: use ids that
	// cannot exist for the negative case and a shim for the positive one.
	ctx := context.Background()
	root, _ := beadsWorkspace(ctx)
	if root == "" {
		t.Skip("not a beads workspace")
	}
	if got := citedIssue(ctx, root, "no ids at all here"); got != "" {
		t.Fatalf("zero citations must yield none, got %q", got)
	}
	if got := citedIssue(ctx, root, "fixes zz-abc123 and also zz-def456"); got != "" {
		t.Fatalf("two citations are ambiguous, got %q", got)
	}
	if got := citedIssue(ctx, root, "fixes zz-abc123 only"); got != "" {
		t.Fatalf("an unreadable id must not count, got %q", got)
	}

	// Positive: a fake bd that answers every show.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bd"), []byte("#!/bin/sh\necho '[{\"id\":\"zz-abc123\"}]'\n"), 0o755); err != nil { // #nosec G306
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if got := citedIssue(ctx, root, "fixes zz-abc123 only"); got != "zz-abc123" {
		t.Fatalf("one verified citation must count, got %q", got)
	}
	// Ambiguity holds even when every cited id IS readable: two verifiable
	// citations must still close nothing (guessing closes the wrong issue).
	if got := citedIssue(ctx, root, "fixes zz-abc123 and also zz-def456"); got != "" {
		t.Fatalf("two READABLE citations are still ambiguous, got %q", got)
	}
}
