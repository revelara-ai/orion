package sandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
)

// buildProbe compiles a static probe that reports whether it can reach the
// network, read a host path, and write to its cwd.
func buildProbe(t *testing.T) string {
	t.Helper()
	const src = `package main
import ("net";"os";"time")
func main(){
  r:=""
  c,e:=net.DialTimeout("tcp","1.1.1.1:53",1500*time.Millisecond)
  if e==nil{c.Close();r+="net=open;"}else{r+="net=denied;"}
  if p:=os.Getenv("PROBE_CTX_PATH");p!=""{ if _,e:=os.ReadFile(p);e==nil{r+="ctx=readable;"}else{r+="ctx=denied;"} }
  if e:=os.WriteFile("probe_wrote.txt",[]byte("x"),0644);e==nil{r+="work=writable;"}else{r+="work=ro;"}
  _=os.WriteFile("probe_result.txt",[]byte(r),0644)
}`
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "go.mod"), []byte("module probe\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "probe")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = srcDir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build probe: %v\n%s", err, out)
	}
	return bin
}

// TestScopedWorkdirEgressDenied: inside the sandbox, network egress is denied,
// the Context Store path is unreadable, and only the workdir is writable.
func TestScopedWorkdirEgressDenied(t *testing.T) {
	be, err := New("bwrap")
	if err != nil {
		t.Fatalf("bwrap backend required for this test: %v", err)
	}
	probe := buildProbe(t)
	workdir := t.TempDir()

	// A sentinel standing in for the Context Store, OUTSIDE the sandbox binds.
	ctxDir := t.TempDir()
	ctxFile := filepath.Join(ctxDir, "orion.db")
	if err := os.WriteFile(ctxFile, []byte("SECRET-CONTEXT-STORE"), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := be.Run(context.Background(), Spec{
		Workdir: workdir,
		Argv:    []string{probe},
		ROBinds: []string{probe},
		Env:     map[string]string{"PROBE_CTX_PATH": ctxFile},
	})
	if err != nil {
		t.Fatalf("sandbox run: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("probe exited %d; stderr=%s", res.ExitCode, res.Stderr)
	}
	out, err := os.ReadFile(filepath.Join(workdir, "probe_result.txt"))
	if err != nil {
		t.Fatalf("probe wrote no result (workdir not writable?): %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "net=denied") {
		t.Fatalf("network egress not denied: %q", got)
	}
	if !strings.Contains(got, "ctx=denied") {
		t.Fatalf("Context Store path was readable from inside the sandbox: %q", got)
	}
	if !strings.Contains(got, "work=writable") {
		t.Fatalf("workdir not writable: %q", got)
	}
}

func seedTask(t *testing.T, s *contextstore.Store) string {
	t.Helper()
	ctx := context.Background()
	var taskID string
	if err := s.WithTx(ctx, func(tx *contextstore.Tx) error {
		pid, _ := tx.Projects().Create(ctx, "demo", "build a time service", "http-service")
		sid, _ := tx.Specs().CreateDraft(ctx, pid)
		eid, _ := tx.Epics().Create(ctx, pid, sid, "epic")
		var e error
		taskID, e = tx.Tasks().Create(ctx, eid, "implement", "cmd/")
		return e
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return taskID
}

// TestGenerateArtifactInWorktree: the generator writes a REAL (compilable) Go
// service into the worktree and the artifact is persisted to the Context Store.
func TestGenerateArtifactInWorktree(t *testing.T) {
	ctx := context.Background()
	worktree := t.TempDir()

	art, err := GenerateTimeServiceFixture(worktree, GenSpec{Module: "orion-generated/svc", Route: "/time", Port: 8080, Format: "json", TimeZone: "UTC"})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if _, err := os.Stat(filepath.Join(worktree, "main.go")); err != nil {
		t.Fatalf("main.go not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(worktree, "go.mod")); err != nil {
		t.Fatalf("go.mod not written: %v", err)
	}
	// "Real" means it compiles.
	build := exec.Command("go", "build", "./...")
	build.Dir = worktree
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("generated service does not compile: %v\n%s", err, out)
	}

	// Persist the artifact.
	store, err := contextstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	taskID := seedTask(t, store)
	id, err := PersistArtifact(ctx, store, taskID, art)
	if err != nil || id == "" {
		t.Fatalf("persist artifact: id=%q err=%v", id, err)
	}
	// Confirm the artifact row exists with the right hash.
	var gotHash string
	_ = store.WithTx(ctx, func(tx *contextstore.Tx) error {
		arts, e := tx.Artifacts().ListByTask(ctx, taskID)
		if e == nil && len(arts) == 1 {
			gotHash = arts[0].ContentHash
		}
		return nil
	})
	if gotHash != art.ContentHash {
		t.Fatalf("persisted artifact hash = %q, want %q", gotHash, art.ContentHash)
	}
}

// TestGeneratedServiceConformsToContract: the generated source reflects the
// ResponseContract (route, port, format).
func TestGeneratedServiceConformsToContract(t *testing.T) {
	worktree := t.TempDir()
	if _, err := GenerateTimeServiceFixture(worktree, GenSpec{Module: "orion-generated/svc", Route: "/now", Port: 9090, Format: "json", TimeZone: "UTC"}); err != nil {
		t.Fatalf("generate: %v", err)
	}
	src, _ := os.ReadFile(filepath.Join(worktree, "main.go"))
	s := string(src)
	for _, want := range []string{`"/now"`, ":9090", "application/json"} {
		if !strings.Contains(s, want) {
			t.Fatalf("generated source missing %q", want)
		}
	}
}
