package conductor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/tools"
)

// TestAnchorWorkspacePath (or-1cv): relative and inside-absolute paths pass;
// ../ escapes and outside-absolute paths are refused with the remedy named;
// ORION_WORKSPACE_WRITES=unrestricted restores the trusted legacy behavior.
func TestAnchorWorkspacePath(t *testing.T) {
	root := t.TempDir()
	t.Setenv("ORION_WORKSPACE_ROOT", root)
	t.Setenv("ORION_WORKSPACE_WRITES", "")

	if got, err := anchorWorkspacePath("pkg/main.go"); err != nil || got != filepath.Join(root, "pkg/main.go") {
		t.Fatalf("relative path must anchor inside the root: %q %v", got, err)
	}
	inside := filepath.Join(root, "sub", "f.go")
	if got, err := anchorWorkspacePath(inside); err != nil || got != inside {
		t.Fatalf("an absolute path INSIDE the root must pass: %q %v", got, err)
	}
	if _, err := anchorWorkspacePath("../escape.txt"); err == nil || !strings.Contains(err.Error(), "outside the workspace root") {
		t.Fatalf("a ../ escape must be refused with the reason: %v", err)
	}
	if _, err := anchorWorkspacePath("/etc/passwd"); err == nil {
		t.Fatal("an outside absolute path must be refused")
	}
	if _, err := anchorWorkspacePath("a/../../b.txt"); err == nil {
		t.Fatal("a nested traversal must be refused after cleaning")
	}
	if _, err := anchorWorkspacePath(""); err == nil {
		t.Fatal("an empty path must be refused")
	}

	// Explicit trusted opt-out: the developer asked for an outside write.
	t.Setenv("ORION_WORKSPACE_WRITES", "unrestricted")
	if got, err := anchorWorkspacePath("/tmp/explicit.txt"); err != nil || got != "/tmp/explicit.txt" {
		t.Fatalf("unrestricted mode must pass outside paths through: %q %v", got, err)
	}
}

// TestWorkspaceWriteToolsRefuseTraversal (or-1cv): the REGISTERED tools
// enforce the anchor — a traversal write never lands on disk and edit_file
// shares the guard.
func TestWorkspaceWriteToolsRefuseTraversal(t *testing.T) {
	root := t.TempDir()
	t.Setenv("ORION_WORKSPACE_ROOT", root)
	t.Setenv("ORION_WORKSPACE_WRITES", "")
	outside := filepath.Join(filepath.Dir(root), "escaped-"+filepath.Base(root)+".txt")

	reg := tools.NewRegistry()
	registerWorkspaceTools(reg, nil)
	wf, _ := reg.Get("write_file")
	in, _ := json.Marshal(map[string]string{"path": "../" + filepath.Base(outside), "content": "pwned"})
	if _, err := wf.Run(context.Background(), in); err == nil || !strings.Contains(err.Error(), "outside the workspace root") {
		t.Fatalf("write_file must refuse traversal: %v", err)
	}
	if _, err := os.Stat(outside); err == nil {
		t.Fatal("the refused write must not land on disk")
	}
	// In-root write works, relative to the anchor.
	in2, _ := json.Marshal(map[string]string{"path": "ok.txt", "content": "fine"})
	if _, err := wf.Run(context.Background(), in2); err != nil {
		t.Fatalf("an in-root relative write must pass: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "ok.txt")); err != nil {
		t.Fatal("the in-root write must land under the anchor root")
	}

	ef, _ := reg.Get("edit_file")
	in3, _ := json.Marshal(map[string]string{"path": "/etc/hostname", "old_string": "a", "new_string": "b"})
	if _, err := ef.Run(context.Background(), in3); err == nil || !strings.Contains(err.Error(), "outside the workspace root") {
		t.Fatalf("edit_file must share the anchor guard: %v", err)
	}
}
