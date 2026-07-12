package reliabilityfloor

import (
	"context"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/revelara-ai/orion/internal/proof/safeenv"
)

type LintResult struct {
	Ran     bool
	ExitOK  bool
	Output  string
	Skipped string
}

// GoDirs returns the deduped, sorted dirs of changed .go files.
func GoDirs(changedFiles []string) []string {
	set := map[string]bool{}
	for _, f := range changedFiles {
		if strings.HasSuffix(f, ".go") {
			set[filepath.ToSlash(filepath.Dir(f))] = true
		}
	}
	dirs := make([]string, 0, len(set))
	for d := range set {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	return dirs
}

// RunLint runs golangci-lint under safeenv (host module cache) in dir. Log-only:
// it never errors and never blocks; a missing binary or nil args is a Skip.
func RunLint(ctx context.Context, dir string, args []string) LintResult {
	if len(args) == 0 {
		return LintResult{Skipped: "no mechanizable signals"}
	}
	if _, err := exec.LookPath("golangci-lint"); err != nil {
		return LintResult{Skipped: "golangci-lint not installed"}
	}
	cmd := exec.CommandContext(ctx, "golangci-lint", args...)
	cmd.Dir = dir
	cmd.Env = safeenv.Build() // host module cache; NEVER os.Environ()
	out, err := cmd.CombinedOutput()
	return LintResult{Ran: true, ExitOK: err == nil, Output: string(out)}
}
