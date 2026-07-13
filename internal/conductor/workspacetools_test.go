package conductor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/internal/tools"
)

func wjson(t *testing.T, m map[string]string) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestWorkspaceTools (or-5j1 slice 1): file write/read/edit round-trip, bash output+exit, the
// ANTHROPIC_API_KEY env scrub, and glob.
func TestWorkspaceTools(t *testing.T) {
	ctx := context.Background()
	c := orchestrator.NewWithStore(openStore(t))
	r := tools.NewRegistry()
	registerWorkspaceTools(r, c)

	dir := t.TempDir()
	// or-1cv: writes are anchored by default — point the anchor at this
	// test's workspace so its absolute paths stay legitimate.
	t.Setenv("ORION_WORKSPACE_ROOT", dir)
	fp := filepath.Join(dir, "sub", "f.txt")

	wt, _ := r.Get("write_file")
	if out, err := wt.Run(ctx, wjson(t, map[string]string{"path": fp, "content": "hello world\n"})); err != nil || !strings.Contains(out, "wrote") {
		t.Fatalf("write_file: %q %v", out, err)
	}

	rt, _ := r.Get("read_file")
	if out, err := rt.Run(ctx, wjson(t, map[string]string{"path": fp})); err != nil || !strings.Contains(out, "hello world") {
		t.Fatalf("read_file: %q %v", out, err)
	}

	et, _ := r.Get("edit_file")
	if _, err := et.Run(ctx, wjson(t, map[string]string{"path": fp, "old_string": "world", "new_string": "orion"})); err != nil {
		t.Fatalf("edit_file: %v", err)
	}
	if b, _ := os.ReadFile(fp); !strings.Contains(string(b), "hello orion") {
		t.Errorf("edit not applied: %s", b)
	}
	// a non-unique old_string is rejected
	if err := os.WriteFile(fp, []byte("a a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := et.Run(ctx, wjson(t, map[string]string{"path": fp, "old_string": "a", "new_string": "b"})); err == nil {
		t.Error("edit_file should reject a non-unique old_string")
	}

	bt, _ := r.Get("bash")
	out, err := bt.Run(ctx, wjson(t, map[string]string{"command": "echo hi && exit 3"}))
	if err != nil {
		t.Fatalf("bash: %v", err)
	}
	if !strings.Contains(out, "hi") || !strings.Contains(out, "exit 3") {
		t.Errorf("bash output should carry stdout + exit code: %q", out)
	}

	// Secret-shaped env vars are scrubbed from bash's environment (fail-closed heuristic).
	t.Setenv("ANTHROPIC_API_KEY", "secret-xyz")
	t.Setenv("GITHUB_TOKEN", "ghp-leak")
	t.Setenv("PATH", os.Getenv("PATH")) // a non-secret var survives
	out, _ = bt.Run(ctx, wjson(t, map[string]string{"command": "echo k=[$ANTHROPIC_API_KEY] t=[$GITHUB_TOKEN] p=[${PATH:+set}]"}))
	if strings.Contains(out, "secret-xyz") || strings.Contains(out, "ghp-leak") {
		t.Errorf("secret-shaped env vars must be scrubbed from bash, got %q", out)
	}
	if !strings.Contains(out, "p=[set]") {
		t.Errorf("non-secret env vars (PATH) must survive, got %q", out)
	}

	gt, _ := r.Get("glob")
	if out, err := gt.Run(ctx, wjson(t, map[string]string{"pattern": filepath.Join(dir, "sub", "*.txt")})); err != nil || !strings.Contains(out, "f.txt") {
		t.Fatalf("glob: %q %v", out, err)
	}
}
