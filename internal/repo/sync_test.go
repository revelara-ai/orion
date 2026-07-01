package repo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// gitT runs git in dir, failing the test on error.
func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// bareRemote makes a bare repo with one commit on main, returning its path — a stand-in origin.
func bareRemote(t *testing.T) string {
	t.Helper()
	work := t.TempDir()
	gitT(t, work, "init", "-q", "-b", "main")
	gitT(t, work, "config", "user.email", "t@t")
	gitT(t, work, "config", "user.name", "T")
	if err := os.WriteFile(filepath.Join(work, "f.txt"), []byte("v1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitT(t, work, "add", "-A")
	gitT(t, work, "commit", "-q", "-m", "v1")
	bare := filepath.Join(t.TempDir(), "remote.git")
	gitT(t, filepath.Dir(bare), "clone", "-q", "--bare", work, bare)
	return bare
}

// clone makes a working clone of remote at a fresh path, returning it.
func clone(t *testing.T, remote string) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "repo")
	gitT(t, filepath.Dir(dst), "clone", "-q", remote, dst)
	gitT(t, dst, "config", "user.email", "t@t")
	gitT(t, dst, "config", "user.name", "T")
	return dst
}

func TestSyncMainNoRepo(t *testing.T) {
	st, err := SyncMain(context.Background(), filepath.Join(t.TempDir(), "nope"))
	if err != nil || st != SyncNoRepo {
		t.Fatalf("missing repo → (%s, %v), want no-repo", st, err)
	}
}

func TestSyncMainNoRemote(t *testing.T) {
	// A greenfield managed repo has no origin remote.
	r, err := initGreenfield(context.Background(), filepath.Join(t.TempDir(), "repo"))
	if err != nil {
		t.Fatal(err)
	}
	if st, err := SyncMain(context.Background(), r.Path); err != nil || st != SyncNoRemote {
		t.Fatalf("no-remote repo → (%s, %v), want no-remote", st, err)
	}
}

func TestSyncMainInSync(t *testing.T) {
	local := clone(t, bareRemote(t))
	if st, err := SyncMain(context.Background(), local); err != nil || st != SyncInSync {
		t.Fatalf("matching clone → (%s, %v), want in-sync", st, err)
	}
}

func TestSyncMainFastForwardsWhenBehind(t *testing.T) {
	remote := bareRemote(t)
	local := clone(t, remote)
	// Advance the remote (a merged PR): a second clone commits + pushes.
	other := clone(t, remote)
	if err := os.WriteFile(filepath.Join(other, "f.txt"), []byte("v2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitT(t, other, "commit", "-qam", "v2")
	gitT(t, other, "push", "-q", "origin", "main")

	// local is now strictly behind → SyncMain fast-forwards it.
	st, err := SyncMain(context.Background(), local)
	if err != nil || st != SyncResynced {
		t.Fatalf("behind → (%s, %v), want resynced", st, err)
	}
	if got, _ := os.ReadFile(filepath.Join(local, "f.txt")); string(got) != "v2\n" {
		t.Errorf("local should be fast-forwarded to remote v2, got %q", got)
	}
}

// TestSyncMainSurfacesDirtyTreeInsteadOfDiscarding (or-tcs.8): if the managed base is behind origin
// but has UNCOMMITTED work on a tracked file, the fast-forward must SURFACE an error rather than
// silently discard it — the reason SyncMain uses `merge --ff-only`, not `reset --hard`.
func TestSyncMainSurfacesDirtyTreeInsteadOfDiscarding(t *testing.T) {
	remote := bareRemote(t)
	local := clone(t, remote)
	// Remote advances f.txt → local is strictly behind.
	other := clone(t, remote)
	if err := os.WriteFile(filepath.Join(other, "f.txt"), []byte("v2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitT(t, other, "commit", "-qam", "v2")
	gitT(t, other, "push", "-q", "origin", "main")
	// Local has an UNCOMMITTED edit to the same tracked file the fast-forward would touch.
	if err := os.WriteFile(filepath.Join(local, "f.txt"), []byte("local edit\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	st, err := SyncMain(context.Background(), local)
	if err == nil {
		t.Fatalf("a dirty, behind base must SURFACE an error, not silently discard — got status %s", st)
	}
	if got, _ := os.ReadFile(filepath.Join(local, "f.txt")); string(got) != "local edit\n" {
		t.Errorf("uncommitted work must be preserved, got %q", got)
	}
}

func TestSyncMainReportsDivergence(t *testing.T) {
	remote := bareRemote(t)
	local := clone(t, remote)
	// Remote advances (merged PR).
	other := clone(t, remote)
	if err := os.WriteFile(filepath.Join(other, "f.txt"), []byte("remote\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitT(t, other, "commit", "-qam", "remote change")
	gitT(t, other, "push", "-q", "origin", "main")
	// Local ALSO advances with a different commit → the two diverge.
	if err := os.WriteFile(filepath.Join(local, "g.txt"), []byte("local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitT(t, local, "add", "-A")
	gitT(t, local, "commit", "-qam", "local change")

	st, err := SyncMain(context.Background(), local)
	if err != nil {
		t.Fatalf("diverged should not error: %v", err)
	}
	if st != SyncDiverged {
		t.Fatalf("divergent local+remote → %s, want diverged", st)
	}
	// The tree is NOT touched on divergence — local's commit survives.
	if _, err := os.Stat(filepath.Join(local, "g.txt")); err != nil {
		t.Errorf("divergence must not discard local work: %v", err)
	}
}
