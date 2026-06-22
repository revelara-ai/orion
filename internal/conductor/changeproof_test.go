package conductor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/llm"
)

// gitInitGreenRepo makes a temp git repo (branch main) with a GREEN Go module committed,
// and an uncommitted WIP file in the working tree (to prove the change loop never
// touches it).
func gitInitGreenRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git unavailable: %v: %s", err, out)
		}
	}
	w := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-b", "main")
	w("go.mod", "module example.com/t\n\ngo 1.25\n")
	w("lib.go", "package t\n\nfunc Add(a, b int) int { return a + b }\n")
	w("lib_test.go", "package t\nimport \"testing\"\nfunc TestAdd(t *testing.T){ if Add(2,3)!=5 { t.Fatal(\"math\") } }\n")
	run("add", "-A")
	run("commit", "-m", "init", "--no-verify")
	w("WIP.txt", "in progress\n") // uncommitted; must survive the change loop
	return dir
}

// TestDiffGeneratorEditsRepo: the generator reads + writes within the repo (path-guarded).
func TestDiffGeneratorEditsRepo(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "existing.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prov := &fakeLLM{resp: []*llm.ChatResponse{
		tuResp("1", "read_file", `{"path":"existing.go"}`),
		tuResp("2", "write_file", `{"path":"new.go","content":"package x\n\nfunc New() int { return 1 }\n"}`),
		endTurn("done"),
	}}
	if err := DiffGenerator(context.Background(), prov, dir, "add a New func", "# codebase map"); err != nil {
		t.Fatalf("diffgen: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "new.go"))
	if err != nil || !strings.Contains(string(data), "func New()") {
		t.Fatalf("change not applied: %v / %s", err, data)
	}
}

// TestChangeAndProveCommitsRegressionSafeChange: a change that keeps the suite green is
// committed on the worktree branch; the developer's working tree (main + WIP) is untouched.
func TestChangeAndProveCommitsRegressionSafeChange(t *testing.T) {
	if testing.Short() {
		t.Skip("git worktree + go test + LLM loop")
	}
	repo := gitInitGreenRepo(t)
	prov := &fakeLLM{resp: []*llm.ChatResponse{
		tuResp("1", "write_file", `{"path":"extra.go","content":"package t\n\nfunc Mul(a, b int) int { return a * b }\n"}`),
		endTurn("added Mul"),
	}}

	res, err := ChangeAndProve(context.Background(), repo, nil, prov, "add a Mul helper")
	if err != nil {
		t.Fatalf("change: %v", err)
	}
	if !res.Regression.Held || !res.Committed {
		t.Fatalf("a regression-safe change should hold + commit: %+v", res)
	}
	git := func(args ...string) string {
		out, _ := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
		return strings.TrimSpace(string(out))
	}
	if files := git("ls-tree", "-r", "--name-only", res.Branch); !strings.Contains(files, "extra.go") {
		t.Fatalf("the change is not on the branch:\n%s", files)
	}
	// developer's working tree untouched
	if br := git("rev-parse", "--abbrev-ref", "HEAD"); br != "main" {
		t.Fatalf("change loop moved the developer off main → %q", br)
	}
	if st := git("status", "--porcelain"); !strings.Contains(st, "WIP.txt") {
		t.Fatalf("change loop disturbed the developer's working tree:\n%q", st)
	}
}

// TestChangeAndProveRejectsRegression: a change that breaks an existing test is NOT
// committed — the regression gate catches it.
func TestChangeAndProveRejectsRegression(t *testing.T) {
	if testing.Short() {
		t.Skip("git worktree + go test + LLM loop")
	}
	repo := gitInitGreenRepo(t)
	prov := &fakeLLM{resp: []*llm.ChatResponse{
		// break Add → TestAdd fails
		tuResp("1", "write_file", `{"path":"lib.go","content":"package t\n\nfunc Add(a, b int) int { return a - b }\n"}`),
		endTurn("changed Add"),
	}}

	res, err := ChangeAndProve(context.Background(), repo, nil, prov, "tweak Add")
	if err != nil {
		t.Fatalf("change: %v", err)
	}
	if res.Regression.Held || res.Committed {
		t.Fatalf("a regression must NOT be committed: %+v", res)
	}
	if res.Reason == "" {
		t.Fatal("a rejected change should explain why")
	}
}
