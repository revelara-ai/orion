package conductor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Workspace write anchoring (or-1cv): write_file/edit_file historically
// accepted arbitrary paths — a deliberate property of the TRUSTED
// developer-facing Conductor. But spawn_subagent can now delegate these
// tools to an agent driven by possibly-injected task text, so writes are
// ANCHORED by default: paths (relative or absolute) must resolve inside the
// workspace root (the Conductor's working directory), and ../ escapes or
// outside-absolute paths are refused with a corrective error. The trusted
// unrestricted behavior stays available explicitly:
//
//	ORION_WORKSPACE_WRITES=unrestricted  → legacy anywhere-writes
//	ORION_WORKSPACE_ROOT=<dir>           → widen/relocate the anchor
//
// Reads (read_file) stay unrestricted — inspecting the developer's machine
// is the Conductor's job; MUTATING it outside the workspace is not.
func anchorWorkspacePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("path is required")
	}
	if os.Getenv("ORION_WORKSPACE_WRITES") == "unrestricted" {
		return path, nil
	}
	root := os.Getenv("ORION_WORKSPACE_ROOT")
	if root == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("workspace root unresolvable: %w", err)
		}
		root = wd
	}
	root = filepath.Clean(root)
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, abs)
	}
	abs = filepath.Clean(abs)
	if abs != root && !strings.HasPrefix(abs, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("path %q resolves outside the workspace root %s — writes are anchored (or-1cv); use a path inside the workspace, or set ORION_WORKSPACE_WRITES=unrestricted if the developer explicitly asked for an outside write", path, root)
	}
	return abs, nil
}
