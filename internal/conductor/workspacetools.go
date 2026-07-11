package conductor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/internal/tools"
)

// registerWorkspaceTools gives the Conductor first-class workspace agency (or-5j1 slice 1) — run
// shell commands and read/write/edit/search files in the developer's working directory — so it can
// do basic tasks directly, not only orchestrate the SDLC build. The Conductor is the TRUSTED
// developer-facing agent (the generation agent stays walled + sandboxed); mutating ops route through
// the red-button actuation gate (as the git tool does), so the developer can halt autonomy without a
// per-command prompt.
func registerWorkspaceTools(r *tools.Registry, c *orchestrator.Conductor) {
	r.Register(tools.Tool{
		Name:        "bash",
		Description: "Run a shell command in the developer's working directory and return its combined output + exit code. Use for build/test/inspection/scaffolding tasks (make, go test, ls, curl, …). Runs as the developer; secret-shaped env vars (API keys, tokens, passwords) are scrubbed from the environment. Mutating — halted while the red button is engaged.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string","description":"the shell command to run"}},"required":["command"]}`),
		Safety:      tools.Safety{Destructive: true, RequiresApproval: true},
		Run: func(ctx context.Context, in json.RawMessage) (string, error) {
			var p struct {
				Command string `json:"command"`
			}
			if err := json.Unmarshal(in, &p); err != nil {
				return "", err
			}
			if gerr := bashGitMutation(p.Command); gerr != nil {
				return "", gerr
			}
			if strings.TrimSpace(p.Command) == "" {
				return "", fmt.Errorf("bash: command is required")
			}
			if gerr := storeRedButton(c).Guard("bash"); gerr != nil {
				return "", gerr
			}
			cctx, cancel := context.WithTimeout(ctx, 120*time.Second)
			defer cancel()
			cmd := exec.CommandContext(cctx, "bash", "-c", p.Command)
			cmd.Env = scrubbedEnv()
			out, err := cmd.CombinedOutput()
			exit := 0
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				exit = ee.ExitCode()
			} else if err != nil {
				return "", err
			}
			return fmt.Sprintf("$ %s (exit %d)\n%s", p.Command, exit, boundOutput(string(out))), nil
		},
	})

	r.Register(tools.Tool{
		Name:        "read_file",
		Description: "Read a file from the developer's working directory and return its contents (large files are truncated). Read-only.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`),
		Safety:      tools.Safety{ReadOnly: true, ParallelSafe: true},
		Run: func(_ context.Context, in json.RawMessage) (string, error) {
			var p struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(in, &p); err != nil {
				return "", err
			}
			b, err := os.ReadFile(p.Path) // #nosec G304 G703 -- developer-facing tool; the Conductor reads the dev's own files by path
			if err != nil {
				return "", err
			}
			return boundOutput(string(b)), nil
		},
	})

	r.Register(tools.Tool{
		Name:        "write_file",
		Description: "Write (create or overwrite) a file in the developer's working directory. Mutating — halted while the red button is engaged.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`),
		Safety:      tools.Safety{Destructive: true, RequiresApproval: true},
		Run: func(_ context.Context, in json.RawMessage) (string, error) {
			var p struct {
				Path    string `json:"path"`
				Content string `json:"content"`
			}
			if err := json.Unmarshal(in, &p); err != nil {
				return "", err
			}
			if gerr := storeRedButton(c).Guard("write_file"); gerr != nil {
				return "", gerr
			}
			if dir := filepath.Dir(p.Path); dir != "" {
				if err := os.MkdirAll(dir, 0o750); err != nil {
					return "", err
				}
			}
			if err := os.WriteFile(p.Path, []byte(p.Content), 0o600); err != nil {
				return "", err
			}
			return fmt.Sprintf("wrote %s (%d bytes)", p.Path, len(p.Content)), nil
		},
	})

	r.Register(tools.Tool{
		Name:        "edit_file",
		Description: "Replace an exact, UNIQUE substring in a file in the developer's working directory (old_string must appear exactly once). Mutating — halted while the red button is engaged.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"old_string":{"type":"string"},"new_string":{"type":"string"}},"required":["path","old_string","new_string"]}`),
		Safety:      tools.Safety{Destructive: true, RequiresApproval: true},
		Run: func(_ context.Context, in json.RawMessage) (string, error) {
			var p struct {
				Path string `json:"path"`
				Old  string `json:"old_string"`
				New  string `json:"new_string"`
			}
			if err := json.Unmarshal(in, &p); err != nil {
				return "", err
			}
			if gerr := storeRedButton(c).Guard("edit_file"); gerr != nil {
				return "", gerr
			}
			b, err := os.ReadFile(p.Path) // #nosec G304 G703 -- developer-facing tool reads the dev's own files by path
			if err != nil {
				return "", err
			}
			s := string(b)
			switch strings.Count(s, p.Old) {
			case 0:
				return "", fmt.Errorf("edit_file: old_string not found in %s", p.Path)
			case 1:
				// #nosec G304 G703 -- developer-facing tool writes the dev's own file by path
				if err := os.WriteFile(p.Path, []byte(strings.Replace(s, p.Old, p.New, 1)), 0o600); err != nil {
					return "", err
				}
				return "edited " + p.Path, nil
			default:
				return "", fmt.Errorf("edit_file: old_string is not unique in %s (%d matches) — add surrounding context", p.Path, strings.Count(s, p.Old))
			}
		},
	})

	r.Register(tools.Tool{
		Name:        "grep",
		Description: "Search files recursively for a pattern (grep -rnI) under a path (default the working directory), returning file:line matches. Read-only.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string"},"path":{"type":"string","description":"optional; defaults to ."}},"required":["pattern"]}`),
		Safety:      tools.Safety{ReadOnly: true, ParallelSafe: true},
		Run: func(ctx context.Context, in json.RawMessage) (string, error) {
			var p struct {
				Pattern string `json:"pattern"`
				Path    string `json:"path"`
			}
			if err := json.Unmarshal(in, &p); err != nil {
				return "", err
			}
			if p.Pattern == "" {
				return "", fmt.Errorf("grep: pattern is required")
			}
			if p.Path == "" {
				p.Path = "."
			}
			cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			// #nosec G204 -- developer-facing search; pattern/path are the dev's own inputs, no shell
			out, _ := exec.CommandContext(cctx, "grep", "-rnI", "--exclude-dir=.git", p.Pattern, p.Path).CombinedOutput()
			if s := strings.TrimSpace(string(out)); s != "" {
				return boundOutput(s), nil
			}
			return "(no matches)", nil
		},
	})

	r.Register(tools.Tool{
		Name:        "glob",
		Description: "List files matching a shell glob pattern (e.g. internal/*/config.go). Read-only.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string"}},"required":["pattern"]}`),
		Safety:      tools.Safety{ReadOnly: true, ParallelSafe: true},
		Run: func(_ context.Context, in json.RawMessage) (string, error) {
			var p struct {
				Pattern string `json:"pattern"`
			}
			if err := json.Unmarshal(in, &p); err != nil {
				return "", err
			}
			matches, err := filepath.Glob(p.Pattern)
			if err != nil {
				return "", err
			}
			if len(matches) == 0 {
				return "(no matches)", nil
			}
			return strings.Join(matches, "\n"), nil
		},
	})
}

// scrubbedEnv is the developer's environment with SECRET-shaped variables removed, so a
// prompt-injected shell command run by the Conductor can't dump credentials from the process env
// (`env | curl …`). It FAILS CLOSED — a var whose name looks like a secret (…SECRET…, …TOKEN…,
// …API_KEY…, …PASSWORD…, …_KEY) is dropped even if that occasionally over-scrubs a build that wanted
// e.g. GITHUB_TOKEN; a missing token yields a visible auth error the developer can re-export, whereas
// a leaked one is silent + irreversible. (Not a wall — arbitrary bash can still read a file the dev
// can; this just refuses to HAND secrets to the child's env.)
func scrubbedEnv() []string {
	env := os.Environ()
	out := make([]string, 0, len(env))
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		up := strings.ToUpper(key)
		if strings.Contains(up, "SECRET") || strings.Contains(up, "TOKEN") ||
			strings.Contains(up, "API_KEY") || strings.Contains(up, "PASSWORD") ||
			strings.HasSuffix(up, "_KEY") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// boundOutput caps a tool result so a huge file/command output can't blow the context window.
func boundOutput(s string) string {
	const limit = 64 << 10
	if len(s) > limit {
		return s[:limit] + "\n…(truncated)"
	}
	return s
}
