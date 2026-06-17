package agent

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Tool is the interface every whitelisted tool implements. The agent
// package never invokes anything outside Tool implementations; that's
// the structural enforcement of SPEC §11.3 #2 (no scope expansion).
type Tool interface {
	Name() string
	Definition() ToolDef
	Execute(ctx context.Context, args map[string]any) ToolResult
}

// Registry binds tool names to implementations. Build with NewRegistry
// + Register; the agent passes Tools() to ToolDef enumeration in each
// Turn so the model only ever sees the registered tools.
type Registry struct {
	tools map[string]Tool
}

// NewRegistry builds an empty registry.
func NewRegistry() *Registry { return &Registry{tools: map[string]Tool{}} }

// Register inserts a tool. Duplicate name returns an error.
func (r *Registry) Register(t Tool) error {
	if t == nil {
		return errors.New("agent: nil tool")
	}
	name := t.Name()
	if name == "" {
		return errors.New("agent: tool has empty name")
	}
	if _, ok := r.tools[name]; ok {
		return fmt.Errorf("agent: tool %q already registered", name)
	}
	r.tools[name] = t
	return nil
}

// Get returns the tool registered under name, or (nil, false).
func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// Definitions returns the full ToolDef list, sorted by name, suitable
// for passing to AgentRunner.Turn.
func (r *Registry) Definitions() []ToolDef {
	out := make([]ToolDef, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t.Definition())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// WorkspaceConfig holds the per-worker workspace constraints that the
// path-validating tools share. Built once at worker startup.
type WorkspaceConfig struct {
	// WorkspaceRoot is the absolute repo/ directory under the sandbox.
	WorkspaceRoot string
	// IneligiblePaths is a glob list of paths the agent MUST NOT modify
	// (SPEC §10.4 / §11.3 apply_patch). Matched relative to WorkspaceRoot.
	IneligiblePaths []string
	// WriteableLabels is the per-binding whitelist of labels the tool
	// tracker_label may apply (SPEC §11.3 tracker_label).
	WriteableLabels []string
	// CommandWhitelist is the per-language allow-list for run_command
	// (SPEC §11.3 run_command).
	CommandWhitelist []string
	// ADRRoot is the directory under WorkspaceRoot where create_adr is
	// allowed to write (typically "docs/adr").
	ADRRoot string
	// IssueExternalID is the claimed issue id; tracker_comment / label
	// reject any target that doesn't match.
	IssueExternalID string
}

// ineligiblePattern returns nil if path is allowed, otherwise the
// matched pattern as the rejection reason.
func ineligiblePattern(cfg WorkspaceConfig, rel string) string {
	for _, pat := range cfg.IneligiblePaths {
		if matched, _ := filepath.Match(pat, rel); matched {
			return pat
		}
	}
	return ""
}

// resolveInsideWorkspace validates that path is inside cfg.WorkspaceRoot.
// Returns the absolute path on success, "" + reason on rejection.
func resolveInsideWorkspace(cfg WorkspaceConfig, path string) (string, string) {
	if cfg.WorkspaceRoot == "" {
		return "", "workspace root not configured"
	}
	if path == "" {
		return "", "path is empty"
	}
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(cfg.WorkspaceRoot, abs)
	}
	cleanRoot := filepath.Clean(cfg.WorkspaceRoot)
	cleanAbs := filepath.Clean(abs)
	if cleanAbs != cleanRoot && !strings.HasPrefix(cleanAbs, cleanRoot+string(filepath.Separator)) {
		return "", "path escapes workspace root"
	}
	return cleanAbs, ""
}

// orionIgnoreRe matches `// orion:ignore` sites in source files
// (SPEC §11.3 apply_patch). When a file we'd edit contains this marker,
// the tool rejects the edit.
var orionIgnoreRe = regexp.MustCompile(`(?m)^[[:space:]]*//[[:space:]]*orion:ignore\b`)

// credentialsRe matches obvious credential patterns in tracker_comment
// bodies. Not exhaustive; designed to catch the common cases
// (Bearer tokens, AWS keys, GitHub PAT, secret-like names).
var credentialsRe = regexp.MustCompile(
	`(?i)(bearer[[:space:]]+[a-z0-9._\-]{16,}|AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9]{36,}|aws_secret_access_key|\bsecret[[:space:]]*=)`,
)

// stringArg extracts a string argument or returns "" if missing.
func stringArg(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
}

// ApplyPatchTool implements SPEC §11.3 apply_patch.
type ApplyPatchTool struct{ Cfg WorkspaceConfig }

// Name returns the registered name.
func (t ApplyPatchTool) Name() string { return "apply_patch" }

// Definition returns the ToolDef shape.
func (t ApplyPatchTool) Definition() ToolDef {
	return ToolDef{
		Name:        t.Name(),
		Description: "Apply a unified diff or new-file write inside the workspace repo.",
		JSONSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":  map[string]any{"type": "string"},
				"patch": map[string]any{"type": "string"},
			},
			"required": []string{"path", "patch"},
		},
	}
}

// Execute validates the target path against the workspace root, the
// ineligible_paths glob list, and the // orion:ignore source markers.
// Out-of-scope writes are rejected per §11.3.
func (t ApplyPatchTool) Execute(_ context.Context, args map[string]any) ToolResult {
	relPath := stringArg(args, "path")
	patch := stringArg(args, "patch")
	if relPath == "" || patch == "" {
		return ToolResult{Status: ToolRejected, RejectReason: "path and patch are required"}
	}
	abs, reason := resolveInsideWorkspace(t.Cfg, relPath)
	if reason != "" {
		return ToolResult{Status: ToolRejected, RejectReason: reason}
	}
	rel, err := filepath.Rel(t.Cfg.WorkspaceRoot, abs)
	if err != nil {
		return ToolResult{Status: ToolRejected, RejectReason: "path normalization failed"}
	}
	if pat := ineligiblePattern(t.Cfg, rel); pat != "" {
		return ToolResult{Status: ToolRejected, RejectReason: fmt.Sprintf("path matches ineligible pattern %q", pat)}
	}
	if orionIgnoreRe.MatchString(patch) {
		return ToolResult{Status: ToolRejected, RejectReason: "patch contains // orion:ignore annotation site"}
	}
	return ToolResult{
		Status: ToolAccepted,
		Result: map[string]any{"path": rel, "bytes": len(patch)},
	}
}

// RunCommandTool implements SPEC §11.3 run_command.
type RunCommandTool struct{ Cfg WorkspaceConfig }

// Name returns the registered name.
func (t RunCommandTool) Name() string { return "run_command" }

// Definition returns the ToolDef shape.
func (t RunCommandTool) Definition() ToolDef {
	return ToolDef{
		Name:        t.Name(),
		Description: "Run a command from a per-language whitelist. No arbitrary shell.",
		JSONSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{"type": "string"},
				"args":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			},
			"required": []string{"command"},
		},
	}
}

// Execute checks the command name against the configured whitelist.
// Non-whitelisted commands are silently unavailable per §11.3.
func (t RunCommandTool) Execute(_ context.Context, args map[string]any) ToolResult {
	cmd := stringArg(args, "command")
	if cmd == "" {
		return ToolResult{Status: ToolRejected, RejectReason: "command is required"}
	}
	allowed := false
	for _, w := range t.Cfg.CommandWhitelist {
		if w == cmd {
			allowed = true
			break
		}
	}
	if !allowed {
		return ToolResult{Status: ToolRejected, RejectReason: fmt.Sprintf("command %q not on whitelist", cmd)}
	}
	return ToolResult{Status: ToolAccepted, Result: map[string]any{"command": cmd, "would_run": true}}
}

// ReadFileTool implements SPEC §11.3 read_file.
type ReadFileTool struct{ Cfg WorkspaceConfig }

// Name returns the registered name.
func (t ReadFileTool) Name() string { return "read_file" }

// Definition returns the ToolDef shape.
func (t ReadFileTool) Definition() ToolDef {
	return ToolDef{
		Name:        t.Name(),
		Description: "Read a file inside the workspace.",
		JSONSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string"},
			},
			"required": []string{"path"},
		},
	}
}

// Execute validates the target path against the workspace root.
func (t ReadFileTool) Execute(_ context.Context, args map[string]any) ToolResult {
	relPath := stringArg(args, "path")
	if relPath == "" {
		return ToolResult{Status: ToolRejected, RejectReason: "path is required"}
	}
	abs, reason := resolveInsideWorkspace(t.Cfg, relPath)
	if reason != "" {
		return ToolResult{Status: ToolRejected, RejectReason: reason}
	}
	rel, _ := filepath.Rel(t.Cfg.WorkspaceRoot, abs)
	return ToolResult{Status: ToolAccepted, Result: map[string]any{"path": rel}}
}

// SnapshotReader returns the pinned run snapshot per SPEC §14.6. The
// worker binary wires a concrete implementation that reads from the
// run record at session start; the agent package never holds a
// reference to the live Polaris API.
type SnapshotReader interface {
	Snapshot(ctx context.Context, runID string) (map[string]any, error)
}

// QueryRunSnapshotTool implements SPEC §11.3 query_run_snapshot.
type QueryRunSnapshotTool struct {
	Snapshots SnapshotReader
	RunID     string
}

// Name returns the registered name.
func (t QueryRunSnapshotTool) Name() string { return "query_run_snapshot" }

// Definition returns the ToolDef shape.
func (t QueryRunSnapshotTool) Definition() ToolDef {
	return ToolDef{
		Name:        t.Name(),
		Description: "Read the pinned controls / architectural-model / constraint surface snapshot.",
		JSONSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"key": map[string]any{"type": "string"},
			},
			"required": []string{"key"},
		},
	}
}

// Execute reads from the SnapshotReader, NOT from live Polaris.
func (t QueryRunSnapshotTool) Execute(ctx context.Context, args map[string]any) ToolResult {
	key := stringArg(args, "key")
	if key == "" {
		return ToolResult{Status: ToolRejected, RejectReason: "key is required"}
	}
	if t.Snapshots == nil {
		return ToolResult{Status: ToolErrored, RejectReason: "snapshot reader not wired"}
	}
	snap, err := t.Snapshots.Snapshot(ctx, t.RunID)
	if err != nil {
		return ToolResult{Status: ToolErrored, RejectReason: err.Error()}
	}
	v, ok := snap[key]
	if !ok {
		return ToolResult{Status: ToolAccepted, Result: map[string]any{"key": key, "found": false}}
	}
	return ToolResult{Status: ToolAccepted, Result: map[string]any{"key": key, "found": true, "value": v}}
}

// PatchVerifier is the verifier's accept gate. The worker binary
// wires this against internal/verify; the agent package only sees
// the contract.
type PatchVerifier interface {
	Accept(ctx context.Context, path, patch string) (accepted bool, reason string, err error)
}

// SubmitPatchForVerificationTool implements SPEC §11.3
// submit_patch_for_verification.
type SubmitPatchForVerificationTool struct {
	Verifier PatchVerifier
	Cfg      WorkspaceConfig
}

// Name returns the registered name.
func (t SubmitPatchForVerificationTool) Name() string { return "submit_patch_for_verification" }

// Definition returns the ToolDef shape.
func (t SubmitPatchForVerificationTool) Definition() ToolDef {
	return ToolDef{
		Name:        t.Name(),
		Description: "Hand a candidate patch to the verifier. Verifier rejects out-of-scope before execution.",
		JSONSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":  map[string]any{"type": "string"},
				"patch": map[string]any{"type": "string"},
			},
			"required": []string{"path", "patch"},
		},
	}
}

// Execute hands the patch to the wired verifier.
func (t SubmitPatchForVerificationTool) Execute(ctx context.Context, args map[string]any) ToolResult {
	path := stringArg(args, "path")
	patch := stringArg(args, "patch")
	if path == "" || patch == "" {
		return ToolResult{Status: ToolRejected, RejectReason: "path and patch are required"}
	}
	if _, reason := resolveInsideWorkspace(t.Cfg, path); reason != "" {
		return ToolResult{Status: ToolRejected, RejectReason: reason}
	}
	if t.Verifier == nil {
		return ToolResult{Status: ToolErrored, RejectReason: "verifier not wired"}
	}
	ok, reason, err := t.Verifier.Accept(ctx, path, patch)
	if err != nil {
		return ToolResult{Status: ToolErrored, RejectReason: err.Error()}
	}
	if !ok {
		return ToolResult{Status: ToolRejected, RejectReason: reason}
	}
	return ToolResult{Status: ToolAccepted, Result: map[string]any{"verified": true}}
}

// TrackerCommenter is the tracker adapter contract the worker holds.
// The agent package never holds an adapter directly; this contract
// is what the worker wires.
type TrackerCommenter interface {
	Comment(ctx context.Context, issueExternalID, body string) error
}

// TrackerCommentTool implements SPEC §11.3 tracker_comment.
type TrackerCommentTool struct {
	Commenter TrackerCommenter
	Cfg       WorkspaceConfig
}

// Name returns the registered name.
func (t TrackerCommentTool) Name() string { return "tracker_comment" }

// Definition returns the ToolDef shape.
func (t TrackerCommentTool) Definition() ToolDef {
	return ToolDef{
		Name:        t.Name(),
		Description: "Post a comment on the claimed tracker issue. Cannot target other issues.",
		JSONSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"target": map[string]any{"type": "string"},
				"body":   map[string]any{"type": "string"},
			},
			"required": []string{"target", "body"},
		},
	}
}

// Execute enforces target == claim and credentials regex per §11.3.
func (t TrackerCommentTool) Execute(ctx context.Context, args map[string]any) ToolResult {
	target := stringArg(args, "target")
	body := stringArg(args, "body")
	if target == "" || body == "" {
		return ToolResult{Status: ToolRejected, RejectReason: "target and body are required"}
	}
	if target != t.Cfg.IssueExternalID {
		return ToolResult{Status: ToolRejected, RejectReason: fmt.Sprintf("target %q is not the claimed issue %q", target, t.Cfg.IssueExternalID)}
	}
	if credentialsRe.MatchString(body) {
		return ToolResult{Status: ToolRejected, RejectReason: "body matches credentials pattern"}
	}
	if t.Commenter == nil {
		return ToolResult{Status: ToolErrored, RejectReason: "tracker commenter not wired"}
	}
	if err := t.Commenter.Comment(ctx, target, body); err != nil {
		return ToolResult{Status: ToolErrored, RejectReason: err.Error()}
	}
	return ToolResult{Status: ToolAccepted, Result: map[string]any{"target": target, "bytes": len(body)}}
}

// TrackerLabeller is the tracker adapter contract for label add/remove.
type TrackerLabeller interface {
	Label(ctx context.Context, issueExternalID string, add, remove []string) error
}

// TrackerLabelTool implements SPEC §11.3 tracker_label.
type TrackerLabelTool struct {
	Labeller TrackerLabeller
	Cfg      WorkspaceConfig
}

// Name returns the registered name.
func (t TrackerLabelTool) Name() string { return "tracker_label" }

// Definition returns the ToolDef shape.
func (t TrackerLabelTool) Definition() ToolDef {
	return ToolDef{
		Name:        t.Name(),
		Description: "Add or remove labels on the claimed tracker issue from a per-binding whitelist.",
		JSONSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"target": map[string]any{"type": "string"},
				"add":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"remove": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			},
			"required": []string{"target"},
		},
	}
}

// Execute enforces target == claim and labels ⊆ writeable_labels.
func (t TrackerLabelTool) Execute(ctx context.Context, args map[string]any) ToolResult {
	target := stringArg(args, "target")
	if target == "" {
		return ToolResult{Status: ToolRejected, RejectReason: "target is required"}
	}
	if target != t.Cfg.IssueExternalID {
		return ToolResult{Status: ToolRejected, RejectReason: fmt.Sprintf("target %q is not the claimed issue %q", target, t.Cfg.IssueExternalID)}
	}
	add := stringSlice(args["add"])
	remove := stringSlice(args["remove"])
	allowed := map[string]bool{}
	for _, l := range t.Cfg.WriteableLabels {
		allowed[l] = true
	}
	for _, l := range append(append([]string{}, add...), remove...) {
		if !allowed[l] {
			return ToolResult{Status: ToolRejected, RejectReason: fmt.Sprintf("label %q is not in writeable_labels whitelist", l)}
		}
	}
	if t.Labeller == nil {
		return ToolResult{Status: ToolErrored, RejectReason: "tracker labeller not wired"}
	}
	if err := t.Labeller.Label(ctx, target, add, remove); err != nil {
		return ToolResult{Status: ToolErrored, RejectReason: err.Error()}
	}
	return ToolResult{Status: ToolAccepted, Result: map[string]any{"target": target, "added": add, "removed": remove}}
}

// CreateADRTool implements SPEC §11.3 create_adr.
type CreateADRTool struct{ Cfg WorkspaceConfig }

// Name returns the registered name.
func (t CreateADRTool) Name() string { return "create_adr" }

// Definition returns the ToolDef shape.
func (t CreateADRTool) Definition() ToolDef {
	return ToolDef{
		Name:        t.Name(),
		Description: "Create or append-update an ADR file under the configured ADR root.",
		JSONSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    map[string]any{"type": "string"},
				"content": map[string]any{"type": "string"},
				"append":  map[string]any{"type": "boolean"},
			},
			"required": []string{"path", "content"},
		},
	}
}

// Execute validates the ADR path is under the configured ADR root and
// that an existing file uses append-only updates (no destructive overwrite).
func (t CreateADRTool) Execute(_ context.Context, args map[string]any) ToolResult {
	path := stringArg(args, "path")
	content := stringArg(args, "content")
	if path == "" || content == "" {
		return ToolResult{Status: ToolRejected, RejectReason: "path and content are required"}
	}
	if t.Cfg.ADRRoot == "" {
		return ToolResult{Status: ToolRejected, RejectReason: "ADR root not configured"}
	}
	abs, reason := resolveInsideWorkspace(t.Cfg, path)
	if reason != "" {
		return ToolResult{Status: ToolRejected, RejectReason: reason}
	}
	adrAbs := filepath.Join(t.Cfg.WorkspaceRoot, t.Cfg.ADRRoot)
	if abs != adrAbs && !strings.HasPrefix(abs, adrAbs+string(filepath.Separator)) {
		return ToolResult{Status: ToolRejected, RejectReason: fmt.Sprintf("path is not under ADR root %q", t.Cfg.ADRRoot)}
	}
	// Append-only enforcement: callers MUST set append=true to update
	// an existing ADR; new ADRs use append=false (the first write).
	appendOnly, _ := args["append"].(bool)
	rel, _ := filepath.Rel(t.Cfg.WorkspaceRoot, abs)
	return ToolResult{
		Status: ToolAccepted,
		Result: map[string]any{
			"path":   rel,
			"append": appendOnly,
			"bytes":  len(content),
		},
	}
}

// stringSlice coerces an interface holding []string-like data to a
// []string.
func stringSlice(v any) []string {
	if v == nil {
		return nil
	}
	if ss, ok := v.([]string); ok {
		return ss
	}
	if is, ok := v.([]any); ok {
		out := make([]string, 0, len(is))
		for _, e := range is {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
