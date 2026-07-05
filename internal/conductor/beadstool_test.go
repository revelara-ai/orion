package conductor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator"
)

// stubBd installs a fake `bd` at the front of PATH so the tool's exec behavior is
// deterministic (no real beads DB, no Dolt lock).
func stubBd(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bd"), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// initBeadsRepo makes a throwaway git repo that carries a .beads/ workspace marker,
// the signal that the developer's project is tracked with beads.
func initBeadsRepo(t *testing.T) string {
	t.Helper()
	dir := initDogfoodRepo(t)
	if err := os.Mkdir(filepath.Join(dir, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestBeadsToolRegisteredWhenWorkspacePresent: in a repo with a .beads/ workspace the
// conductor exposes a general bd tool (Destructive — it can mutate the issue DB) with
// an args schema, mirroring the git tool.
func TestBeadsToolRegisteredWhenWorkspacePresent(t *testing.T) {
	repo := initBeadsRepo(t)
	t.Chdir(repo)
	r := specTools(orchestrator.NewWithStore(openStore(t)), nil, &changeSession{}, nil)
	tool, ok := r.Get("bd")
	if !ok {
		t.Fatal("bd tool is not registered in a beads workspace")
	}
	if !tool.Safety.Destructive {
		t.Error("bd should be Destructive (it can mutate the issue DB)")
	}
	if !strings.Contains(string(tool.InputSchema), "args") {
		t.Error("bd schema should declare args")
	}
}

// TestBeadsToolAbsentWithoutBeadsWorkspace: no .beads/ in the repo → the tool is not
// exposed at all (registration is separate from exposure; the agent never sees a tool
// it cannot use).
func TestBeadsToolAbsentWithoutBeadsWorkspace(t *testing.T) {
	repo := initDogfoodRepo(t)
	t.Chdir(repo)
	r := specTools(orchestrator.NewWithStore(openStore(t)), nil, &changeSession{}, nil)
	if _, ok := r.Get("bd"); ok {
		t.Fatal("bd tool must not be registered without a .beads/ workspace")
	}
}

// TestBeadsToolRunsBd: the tool runs bd in the developer's repo and reports exit 0 + output.
func TestBeadsToolRunsBd(t *testing.T) {
	repo := initBeadsRepo(t)
	t.Chdir(repo)
	stubBd(t, `echo "ready: or-123 open"`)
	tool, _ := specTools(orchestrator.NewWithStore(openStore(t)), nil, &changeSession{}, nil).Get("bd")
	out, err := tool.Run(context.Background(), json.RawMessage(`{"args":["ready"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "exit 0") || !strings.Contains(out, "or-123") {
		t.Errorf("bd ready should report exit 0 + output, got: %s", out)
	}
}

// TestBeadsToolReportsFailureAsExitNotError: a failed bd op comes back as readable
// output with a non-zero exit — NOT a Go error — so the brain can read it and react.
func TestBeadsToolReportsFailureAsExitNotError(t *testing.T) {
	repo := initBeadsRepo(t)
	t.Chdir(repo)
	stubBd(t, `echo "no such issue: or-nope"; exit 3`)
	tool, _ := specTools(orchestrator.NewWithStore(openStore(t)), nil, &changeSession{}, nil).Get("bd")
	out, err := tool.Run(context.Background(), json.RawMessage(`{"args":["show","or-nope"]}`))
	if err != nil {
		t.Fatalf("a failed bd op must be reported as output, not a Go error: %v", err)
	}
	if strings.Contains(out, "exit 0") || !strings.Contains(out, "no such issue") {
		t.Errorf("a failed bd op must report its non-zero exit + output, got: %s", out)
	}
}

// TestBeadsToolRetriesOnLockContention: the beads DB is single-writer — a write that
// loses the lock to another bd process has not been applied and must be retried, not
// reported as a failure on the first bounce.
func TestBeadsToolRetriesOnLockContention(t *testing.T) {
	repo := initBeadsRepo(t)
	t.Chdir(repo)
	counter := filepath.Join(t.TempDir(), "count")
	stubBd(t, fmt.Sprintf(`n=$(cat %[1]s 2>/dev/null || echo 0)
n=$((n+1))
echo $n > %[1]s
if [ $n -lt 3 ]; then echo "beads: database is locked by another process"; exit 1; fi
echo "issue updated"`, counter))
	out, exit := bdRun(context.Background(), repo, "update", "or-123", "--status", "in_progress")
	if exit != 0 {
		t.Fatalf("bdRun should retry through lock contention to success, got exit %d: %s", exit, out)
	}
	data, _ := os.ReadFile(counter)
	if strings.TrimSpace(string(data)) != "3" {
		t.Errorf("expected 3 attempts (2 contended + 1 success), got %q", string(data))
	}
}

// TestBeadsToolRefusesInteractiveEdit: bd edit opens $EDITOR and would wedge the
// session — the guard is deterministic, not prompt discipline.
func TestBeadsToolRefusesInteractiveEdit(t *testing.T) {
	repo := initBeadsRepo(t)
	t.Chdir(repo)
	tool, _ := specTools(orchestrator.NewWithStore(openStore(t)), nil, &changeSession{}, nil).Get("bd")
	if _, err := tool.Run(context.Background(), json.RawMessage(`{"args":["edit","or-123"]}`)); err == nil {
		t.Fatal("bd edit must be refused (interactive editor blocks the session)")
	}
}

// TestBeadsGuidanceOnlyWhenWorkspacePresent: the system prompt advertises the beads
// capability exactly when the tool surface carries it.
func TestBeadsGuidanceOnlyWhenWorkspacePresent(t *testing.T) {
	t.Chdir(initBeadsRepo(t))
	if g := maybeBeadsGuidance(); !strings.Contains(g, "bd") || !strings.Contains(g, "beads") {
		t.Errorf("beads workspace should yield bd guidance, got: %q", g)
	}
	t.Chdir(initDogfoodRepo(t))
	if g := maybeBeadsGuidance(); g != "" {
		t.Errorf("no beads workspace must yield empty guidance, got: %q", g)
	}
}
