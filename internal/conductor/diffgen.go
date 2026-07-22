package conductor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/revelara-ai/orion/internal/harness"
	"github.com/revelara-ai/orion/internal/harnessconfig"
	"github.com/revelara-ai/orion/internal/tools"
	"github.com/revelara-ai/orion/pkg/llm"
)

// readFileTool lets the diff generator read existing files (path-guarded to root) so it
// edits REAL code rather than inventing it.
func readFileTool(root string) tools.Tool {
	clean := filepath.Clean(root)
	return tools.Tool{
		Name:        "read_file",
		Description: "Read an existing file (path relative to the repo root) to understand it before editing. PREFER a line range (start_line, line_count) — full reads burn the attempt's tool-turn budget.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"start_line":{"type":"integer","description":"1-based first line of the range"},"line_count":{"type":"integer","description":"lines to return from start_line"}},"required":["path"]}`),
		Safety:      tools.Safety{ReadOnly: true},
		Run: func(_ context.Context, in json.RawMessage) (string, error) {
			var p struct {
				Path      string `json:"path"`
				StartLine int    `json:"start_line"`
				LineCount int    `json:"line_count"`
			}
			if err := json.Unmarshal(in, &p); err != nil {
				return "", err
			}
			full := filepath.Join(clean, filepath.Clean("/"+p.Path))
			if full != clean && !strings.HasPrefix(full, clean+string(os.PathSeparator)) {
				return "", fmt.Errorf("path %q escapes the repo", p.Path)
			}
			data, err := os.ReadFile(full)
			if err != nil {
				return "", err
			}
			out := sliceLines(string(data), p.StartLine, p.LineCount)
			if len(out) > 60000 {
				return out[:60000] + "\n… (truncated — use start_line/line_count to read a range)", nil
			}
			return out, nil
		},
	}
}

// DiffGenerator edits the repo at repoDir to satisfy a change intent, using read_file +
// write_file (both path-guarded to repoDir). It is the brownfield UNIT OF WORK: a
// surgical change to existing code, grounded in what's there + the codebase map. The
// caller runs it inside a WORKTREE so the developer's working tree is untouched; the
// regression gate then proves the change preserved existing behavior.
func DiffGenerator(ctx context.Context, provider llm.Provider, repoDir, intent, repoContext string, supersedes []string) error {
	reg := tools.NewRegistry()
	reg.Register(editFileTool(repoDir))  // surgical str_replace — the primary editor for existing files
	reg.Register(writeFileTool(repoDir)) // reuse the path-guarded greenfield writer (new files only)
	reg.Register(readFileTool(repoDir))
	loop := harness.Loop{
		Provider: provider,
		Tools:    reg,
		System:   diffGenRole(intent, repoContext, supersedes),
		Role:     "diffgen",
		Supervisor: harness.Supervisor{
			MaxIterations: harnessconfig.ToolTurns("DIFFGEN", 200), // or-csmy: large-change headroom; env-tunable
			// This conversation is discarded with the worktree on failure —
			// the session-resume advice would be a lie here.
			CapHint: "this generation attempt is discarded; the change flow may retry with a corrected intent",
		},
	}
	start := []llm.Message{llm.TextMessage(llm.RoleUser,
		"Make the change now. Read the files you need with read_file, then apply surgical edits with edit_file (replace a unique old_string with new_string). Use write_file only to CREATE a new file. Touch as few files as possible. "+
			"You have a LIMITED tool budget: do not re-read a file after editing it (edit_file returns the changed span — trust it), and do not re-read files you have already seen. End your turn as soon as the change is complete.")}
	if _, _, err := loop.Run(ctx, start, nil); err != nil {
		return fmt.Errorf("diff generation: %w", err)
	}
	return nil
}

func diffGenRole(intent, repoContext string, supersedes []string) string {
	var b strings.Builder
	b.WriteString("You are Orion's brownfield change generator. Make a SURGICAL change to an EXISTING Go codebase to satisfy the intent below, reusing the codebase's existing packages, APIs, and conventions.\n\n")
	b.WriteString("Rules:\n")
	b.WriteString("- Read existing files before editing them; match their style and conventions.\n")
	b.WriteString("- Keep the change minimal — touch as few files as possible; do NOT rewrite unrelated code.\n")
	b.WriteString("- PRESERVE existing behavior: do not break what works. An independent regression check will run the existing tests against your change.\n")
	b.WriteString("- Edit existing files with edit_file: replace a UNIQUE old_string with new_string (include enough surrounding context that it matches exactly once). It emits only the changed span, so a large file never truncates the edit and unrelated code is never disturbed. Use write_file ONLY to create a brand-new file.\n")
	if len(supersedes) > 0 {
		b.WriteString("- EXCEPTION — intentional behavior change: the change below DELIBERATELY changes behavior asserted by these existing tests: " + strings.Join(supersedes, ", ") + ". UPDATE those tests to assert the NEW behavior (do NOT preserve their old assertions). The regression check skips them; every OTHER test must still pass.\n")
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "# Change intent\n%s\n\n", strings.TrimSpace(intent))
	if repoContext != "" {
		b.WriteString("# Codebase map (orient yourself here)\n")
		b.WriteString(repoContext)
		b.WriteString("\n")
	}
	return b.String()
}
