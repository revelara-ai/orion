package conductor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/revelara-ai/orion/internal/tools"
)

// beadsWorkspace reports whether the developer's resolved repo is tracked with
// beads (a .beads/ workspace at the git root), returning the root when it is.
func beadsWorkspace(ctx context.Context) (string, bool) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", false
	}
	root := GitRoot(ctx, cwd)
	if root == "" {
		return "", false
	}
	fi, err := os.Stat(filepath.Join(root, ".beads"))
	if err != nil || !fi.IsDir() {
		return "", false
	}
	return root, true
}

// bdLockContention matches beads' embedded-Dolt single-writer errors: another bd
// process (or a live build loop) holds the DB lock, so a failed write has NOT been
// applied and is safe to retry.
var bdLockContention = regexp.MustCompile(`(?i)(locked|lock is held|another (bd|writer|process)|acquire.*lock)`)

// bdRun runs `bd <args...>` in dir and returns the combined output and exit code,
// WITHOUT turning a non-zero exit into a Go error — the bd tool reports a failed op
// back to the brain as readable output, not a tool error (mirrors gitRun). Lock
// contention is retried with a short backoff before the failure is reported.
func bdRun(ctx context.Context, dir string, args ...string) (string, int) {
	const attempts = 3
	var out string
	var exit int
	for i := 1; ; i++ {
		out, exit = runOnce(ctx, dir, args)
		if exit == 0 || i == attempts || !bdLockContention.MatchString(out) {
			return out, exit
		}
		select {
		case <-ctx.Done():
			return out, exit
		case <-time.After(time.Duration(i) * 200 * time.Millisecond):
		}
	}
}

func runOnce(ctx context.Context, dir string, args []string) (string, int) {
	cmd := exec.CommandContext(ctx, "bd", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return string(out), ee.ExitCode()
	}
	return string(out) + err.Error(), -1
}

// registerBeadsTool exposes a general bd tool when (and only when) the developer's
// repo is a beads workspace — registration is separate from exposure, so an agent in
// an untracked repo never sees a tool it cannot use.
func registerBeadsTool(r *tools.Registry) {
	if _, ok := beadsWorkspace(context.Background()); !ok {
		return
	}
	r.Register(tools.Tool{
		Name: "bd",
		Description: "Run a beads (bd) issue-tracker operation in the developer's repo and return its output + exit code. The project tracks its work in beads — use it to GROUND specs and changes in the real backlog. Read freely: ready, show, list, search, blocked, stats, comments, dep tree. WRITE (create, update, close, dep add, comment, label) only on the developer's say-so — writes mutate their shared issue DB. NEVER run `bd edit` (it opens $EDITOR and blocks forever; update fields with `bd update --title/--description/--notes` instead). `bd dolt push`/`pull` reach the shared remote — only when the developer explicitly asks. The DB is single-writer: a write that loses the lock to another bd process is retried automatically; if it still fails, report the error rather than assuming it applied.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"args":{"type":"array","items":{"type":"string"},"description":"bd arguments after 'bd', e.g. [\"ready\"] or [\"show\",\"or-123\"] or [\"close\",\"or-123\",\"--reason\",\"done\"]"}},"required":["args"]}`),
		Safety:      tools.Safety{Destructive: true},
		Run: func(ctx context.Context, in json.RawMessage) (string, error) {
			var p struct {
				Args []string `json:"args"`
			}
			if err := json.Unmarshal(in, &p); err != nil {
				return "", err
			}
			if len(p.Args) == 0 {
				return "", fmt.Errorf("bd: args is required (the bd arguments to run)")
			}
			if p.Args[0] == "edit" {
				return "", fmt.Errorf("bd edit opens an interactive $EDITOR and would block the session — use bd update with --title/--description/--notes instead")
			}
			root, ok := beadsWorkspace(ctx)
			if !ok {
				return "", fmt.Errorf("no beads workspace (.beads/) in the current repo")
			}
			if _, err := exec.LookPath("bd"); err != nil {
				return "", fmt.Errorf("bd not found on PATH; beads tracking unavailable")
			}
			out, exit := bdRun(ctx, root, p.Args...)
			var b strings.Builder
			fmt.Fprintf(&b, "bd %s (exit %d)\n", strings.Join(p.Args, " "), exit)
			if s := strings.TrimSpace(out); s != "" {
				b.WriteString(s)
			} else {
				b.WriteString("(no output)")
			}
			return b.String(), nil
		},
	})
}

// maybeBeadsGuidance returns the system-prompt section teaching the agent to use the
// project's beads tracker — empty when the repo isn't a beads workspace, so the
// prompt never advertises a capability the tool surface doesn't carry.
func maybeBeadsGuidance() string {
	if _, ok := beadsWorkspace(context.Background()); !ok {
		return ""
	}
	return `

## Project issue tracking (beads — you CAN read and update the backlog)
This project tracks its work in beads (.beads/ present); the bd tool is available.
- Ground your grilling in the tracked backlog: bd ["ready"], ["show","<id>"], ["search","<kw>"] — cite real issue ids when the intent overlaps tracked work.
- Reads are free. WRITES (create/update/close/dep add/comment) only when the developer asks — the issue DB is shared.
- When a build/change completes for a tracked issue, offer to update it (status, a comment with the outcome) rather than doing it silently.
- Never run bd edit (interactive editor; it blocks). Never bd dolt push/pull unless explicitly asked (shared remote).`
}
