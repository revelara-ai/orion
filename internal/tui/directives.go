package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Input directives (or-ykz.6, Pi-parity ergonomics): a submitted line may
// carry inline shortcuts the TUI expands before the prompt is dispatched.
//
//	@path        — inline the file's contents (fenced) into the message
//	!cmd         — run cmd, append its output to the message (run + SEND)
//	!!cmd        — run cmd, show its output locally, do NOT send (a local action)
//
// The expansion is a PURE transform over (line → ExpandResult) so it is
// unit-testable without a live terminal. Command execution is bounded and
// runs in the user's own shell (the TUI is the developer's own machine).

// ExpandResult is the outcome of expanding one submitted line.
type ExpandResult struct {
	Text  string // the message to send (empty when Send is false)
	Send  bool   // false for a local-only action (!!cmd)
	Local string // output to show locally regardless of Send
}

// directiveTimeout bounds an inline command.
const directiveTimeout = 30 * time.Second

// ExpandDirectives resolves @file / !cmd / !!cmd in a submitted line. A line
// with no directive returns unchanged (Send=true). runCmd is injected so
// tests drive execution without a real shell; nil uses the default runner.
func ExpandDirectives(line string, runCmd func(ctx context.Context, cmd string) (string, error)) ExpandResult {
	if runCmd == nil {
		runCmd = defaultRunCmd
	}
	trimmed := strings.TrimSpace(line)

	// !!cmd (run, don't send) — checked before !cmd (prefix overlap).
	if cmd, ok := strings.CutPrefix(trimmed, "!!"); ok {
		out := runInline(runCmd, cmd)
		return ExpandResult{Send: false, Local: out}
	}
	// !cmd (run + send): the command output becomes context in the message.
	if cmd, ok := strings.CutPrefix(trimmed, "!"); ok {
		out := runInline(runCmd, cmd)
		return ExpandResult{
			Text:  fmt.Sprintf("$ %s\n```\n%s\n```", strings.TrimSpace(cmd), out),
			Send:  true,
			Local: out,
		}
	}

	// @file inlining: every @token that resolves to a readable file is
	// replaced by a fenced block; an unreadable @token is left verbatim (it
	// may be a literal @mention, never silently dropped).
	if strings.Contains(line, "@") {
		if expanded, any := expandFileRefs(line); any {
			return ExpandResult{Text: expanded, Send: true}
		}
	}
	return ExpandResult{Text: line, Send: true}
}

// expandFileRefs replaces @path tokens whose target reads as a file. Returns
// (expanded, anyReplaced).
func expandFileRefs(line string) (string, bool) {
	fields := strings.Fields(line)
	any := false
	for i, f := range fields {
		ref, ok := strings.CutPrefix(f, "@")
		if !ok || ref == "" {
			continue
		}
		b, err := os.ReadFile(ref) // #nosec G304 -- the developer's own @-referenced path
		if err != nil {
			continue // not a readable file → leave the token as-is
		}
		fields[i] = fmt.Sprintf("\n--- %s ---\n```\n%s\n```\n", filepath.Base(ref), strings.TrimRight(string(b), "\n"))
		any = true
	}
	if !any {
		return line, false
	}
	return strings.Join(fields, " "), true
}

func runInline(runCmd func(ctx context.Context, cmd string) (string, error), cmd string) string {
	if strings.TrimSpace(cmd) == "" {
		return "(empty command)"
	}
	ctx, cancel := context.WithTimeout(context.Background(), directiveTimeout)
	defer cancel()
	out, err := runCmd(ctx, cmd)
	out = strings.TrimRight(out, "\n")
	if err != nil {
		if out != "" {
			return out + "\n(exit: " + err.Error() + ")"
		}
		return "(command failed: " + err.Error() + ")"
	}
	if out == "" {
		return "(no output)"
	}
	return out
}

func defaultRunCmd(ctx context.Context, cmd string) (string, error) {
	c := exec.CommandContext(ctx, "sh", "-c", cmd) // #nosec G204 -- the developer's own inline shell command in their TUI
	out, err := c.CombinedOutput()
	return string(out), err
}
