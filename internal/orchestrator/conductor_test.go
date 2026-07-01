package orchestrator

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// TestConductorSubmitBlocksOnDivergedManagedRepo (or-tcs.8): a new intent is BLOCKED when the
// managed repo's base has diverged from origin (the developer must reconcile first). This is the
// step-11 gate that closes the loop before step 12.
func TestConductorSubmitBlocksOnDivergedManagedRepo(t *testing.T) {
	store, err := contextstore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// A bare remote on main, cloned into the managed repo path <store.Dir()>/repo.
	src := t.TempDir()
	git(t, src, "init", "-q", "-b", "main")
	git(t, src, "config", "user.email", "t@t")
	git(t, src, "config", "user.name", "T")
	if err := os.WriteFile(filepath.Join(src, "f.txt"), []byte("v1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, src, "add", "-A")
	git(t, src, "commit", "-q", "-m", "v1")
	bare := filepath.Join(t.TempDir(), "remote.git")
	git(t, filepath.Dir(bare), "clone", "-q", "--bare", src, bare)

	repoDir := filepath.Join(store.Dir(), "repo")
	git(t, filepath.Dir(repoDir), "clone", "-q", bare, repoDir)
	git(t, repoDir, "config", "user.email", "t@t")
	git(t, repoDir, "config", "user.name", "T")

	// Remote advances (a merged PR) AND the local base advances differently → diverged.
	git(t, src, "commit", "-q", "--allow-empty", "-m", "remote change")
	git(t, src, "push", "-q", bare, "main")
	if err := os.WriteFile(filepath.Join(repoDir, "g.txt"), []byte("local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, repoDir, "add", "-A")
	git(t, repoDir, "commit", "-q", "-m", "local change")

	_, err = NewWithStore(store).Submit(context.Background(), "Build an HTTP service")
	if err == nil || !strings.Contains(err.Error(), "diverged") {
		t.Fatalf("Submit must block on a diverged managed repo, got err=%v", err)
	}
}

// TestConductorSubmit is the or-0d2 done-gate: the Conductor accepts an intent
// and returns a confirmation that echoes it. This is the thinnest slice of the
// Conductor.Submit contract the rest of the loop thickens.
func TestConductorSubmit(t *testing.T) {
	c := New()
	const intent = "Build an HTTP service that returns the current time."

	conf, err := c.Submit(context.Background(), intent)
	if err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}
	if !conf.Accepted {
		t.Fatalf("expected intent to be accepted, got Accepted=false (%q)", conf.Message)
	}
	if !strings.Contains(conf.Message, intent) {
		t.Fatalf("confirmation should echo the intent; got %q", conf.Message)
	}
	if conf.Intent != intent {
		t.Fatalf("confirmation Intent = %q, want %q", conf.Intent, intent)
	}
}

func TestConductorSubmitRejectsEmptyIntent(t *testing.T) {
	c := New()
	if _, err := c.Submit(context.Background(), "   "); err == nil {
		t.Fatal("expected error for empty/whitespace intent, got nil")
	}
}

func TestConductorSubmitHonorsContextCancellation(t *testing.T) {
	c := New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Submit(ctx, "anything"); err == nil {
		t.Fatal("expected error when context is already cancelled, got nil")
	}
}

// TestConductorStatusReflectsSubmittedIntent ensures Status() observes state set
// by Submit — the skeleton of the situational-awareness surface.
func TestConductorStatusReflectsSubmittedIntent(t *testing.T) {
	c := New()
	if got := c.Status().Intent; got != "" {
		t.Fatalf("fresh Conductor Status().Intent = %q, want empty", got)
	}
	const intent = "Build a thing"
	if _, err := c.Submit(context.Background(), intent); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if got := c.Status().Intent; got != intent {
		t.Fatalf("Status().Intent = %q, want %q", got, intent)
	}
}
