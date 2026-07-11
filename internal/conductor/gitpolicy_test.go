package conductor

import (
	"strings"
	"testing"
)

// TestGitPolicy (or-4gib circumvention): the conductor's git tool is a
// fail-closed allowlist — review verbs plus the exactly-shaped landing merge.
// Commits are reachable ONLY through the proof pipeline, and the refusal must
// TEACH that (description-as-policy already failed once; the policy is code).
func TestGitPolicy(t *testing.T) {
	allowed := [][]string{
		{"status"},
		{"status", "--porcelain"},
		{"log", "--oneline", "-5"},
		{"diff", "main..orion-change-x"},
		{"show", "HEAD"},
		{"rev-parse", "--abbrev-ref", "HEAD"},
		{"ls-files", "pkg/llm"},
		{"blame", "pkg/llm/llm.go"},
		{"merge", "--ff-only", "orion-change-add-tests-3"}, // the documented landing shape
	}
	for _, args := range allowed {
		if err := gitPolicy(args); err != nil {
			t.Errorf("gitPolicy(%v) must be allowed, got: %v", args, err)
		}
	}

	refused := [][]string{
		{"commit", "-m", "x"},
		{"add", "-A"},
		{"checkout", "-b", "temp-commit-branch"}, // the observed circumvention
		{"switch", "-c", "x"},
		{"reset", "--hard", "HEAD~1"},
		{"rebase", "main"},
		{"stash", "push"},
		{"push", "origin", "main"}, // publishing needs the developer
		{"tag", "v1"},
		{"clean", "-fdx"},
		{"merge", "orion-change-x"},                   // landing without --ff-only
		{"merge", "--ff-only", "main"},                // landing a non-review branch
		{"merge", "--ff-only", "orion-change-x", "y"}, // extra args
		{"config", "user.name", "x"},
	}
	for _, args := range refused {
		err := gitPolicy(args)
		if err == nil {
			t.Errorf("gitPolicy(%v) must be refused", args)
			continue
		}
		if !strings.Contains(err.Error(), "build_change") {
			t.Errorf("refusal must teach the proof path, got: %v", err)
		}
	}
}

// TestBashGitMutationGuard: bash is the window next to the git door — the same
// mutations are refused at command position (guidance for honest models; the
// sandbox is the hard boundary).
func TestBashGitMutationGuard(t *testing.T) {
	refused := []string{
		"git commit -m 'x'",
		"git -C /tmp add -A",
		"cd pkg && git checkout -b tmp",
		"go test ./... && git push origin main",
		"git stash",
	}
	for _, cmd := range refused {
		err := bashGitMutation(cmd)
		if err == nil {
			t.Errorf("bashGitMutation(%q) must refuse", cmd)
			continue
		}
		if !strings.Contains(err.Error(), "build_change") {
			t.Errorf("refusal must teach the proof path: %v", err)
		}
	}
	allowed := []string{
		"git status",
		"git log --oneline -3",
		"go test ./pkg/llm/",
		"echo git commit is done through the pipeline", // git not at command position
		"grep -rn 'git commit' internal/",
	}
	for _, cmd := range allowed {
		if err := bashGitMutation(cmd); err != nil {
			t.Errorf("bashGitMutation(%q) must allow, got: %v", cmd, err)
		}
	}
}
