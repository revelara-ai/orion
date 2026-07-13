package conductor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/pkg/llm"
)

// artifactRepo builds a repo where a PRIOR run left a committed
// orion-change branch for the intent; diverge=true advances main afterward
// so the branch no longer fast-forwards.
func artifactRepo(t *testing.T, intent string, diverge bool) string {
	t.Helper()
	repo := gitInitGreenRepo(t)
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	branch := "orion-change-" + slugFromIntent(intent)
	run("checkout", "-b", branch)
	if err := os.WriteFile(filepath.Join(repo, "proven.go"), []byte("package t\n\nfunc Proven() int { return 1 }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-m", "proven change", "--no-verify")
	run("checkout", "main")
	if diverge {
		if err := os.WriteFile(filepath.Join(repo, "moved.go"), []byte("package t\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		run("add", "-A")
		run("commit", "-m", "base moved", "--no-verify")
	}
	return repo
}

// or-mvs acceptance: an existing proven ff-able artifact is RECOMMENDED (no
// generation runs — nil provider proves it); a diverged branch re-derives.
func TestExistingProvenArtifactRecommended(t *testing.T) {
	if testing.Short() {
		t.Skip("git worktree flow")
	}
	const intent = "add a Proven helper"
	repo := artifactRepo(t, intent, false)

	// nil provider: if ChangeAndProve got past the reuse check it would need
	// a generator — reaching one is the failure mode this test pins against.
	res, err := ChangeAndProve(context.Background(), repo, nil, nil, intent, nil, nil, nil)
	if err != nil {
		t.Fatalf("reuse path must not need a provider: %v", err)
	}
	if !res.ExistingArtifact || res.Committed {
		t.Fatalf("must RECOMMEND the artifact, never claim a fresh proof: %+v", res)
	}
	if !strings.Contains(res.Branch, "orion-change-") || !strings.Contains(res.Reason, "RECOMMENDED") || !strings.Contains(res.Reason, "force_rederive") {
		t.Fatalf("recommendation must name the branch + the override: %+v", res)
	}
}

// A diverged (non-ff) artifact must NOT be recommended — re-derivation is
// correct there, exactly as the bead scopes it.
func TestDivergedArtifactNotRecommended(t *testing.T) {
	const intent = "add a Proven helper"
	repo := artifactRepo(t, intent, true)
	if br, ok := existingProvenArtifact(context.Background(), repo, intent); ok {
		t.Fatalf("diverged branch %s must not be recommended", br)
	}
	// And an absent branch obviously not.
	if _, ok := existingProvenArtifact(context.Background(), gitInitGreenRepo(t), intent); ok {
		t.Fatal("no artifact exists — nothing to recommend")
	}
}

// The override is explicit: force_rederive regenerates fresh even though a
// clean artifact exists.
func TestForceRederiveOverridesReuse(t *testing.T) {
	if testing.Short() {
		t.Skip("git worktree + go test + LLM loop")
	}
	const intent = "add a Mul helper"
	repo := artifactRepo(t, intent, false)
	prov := &fakeLLM{resp: []*llm.ChatResponse{
		tuResp("1", "write_file", `{"path":"extra.go","content":"package t\n\nfunc Mul(a, b int) int { return a * b }\n"}`),
		endTurn("added Mul"),
	}}
	ctx := WithForceRederive(context.Background())
	res, err := ChangeAndProve(ctx, repo, nil, prov, intent, nil, nil, nil)
	if err != nil {
		t.Fatalf("forced re-derive: %v", err)
	}
	if res.ExistingArtifact {
		t.Fatalf("force_rederive must skip the reuse path: %+v", res)
	}
	if !res.Committed {
		t.Fatalf("forced re-derive must run the real flow to a fresh commit: %+v", res)
	}
}
