# S2 · Permission UX — Design

**Date:** 2026-07-02
**Status:** Approved (autonomous — full-autonomy grant); UX via /ui-ux-pro-max
**Scope:** Per-tool approval prompt for the conductor's mutating tools, layered over the
existing ACP permission gate + red button. Preserves the conductor/proof architecture.

## 1. Overview

When the conductor wants to run a **mutating** tool (`bash`, `write_file`, `edit_file` —
`Safety{Destructive:true}`), it pauses and asks the developer to approve. The prompt is a
transcript card showing the pending call (command, or file + unified-diff preview), with
three keyboard choices: **Allow once**, **Allow always (this tool, this session)**,
**Deny**. A denied tool returns a message to the model so it adapts. Read-only tools
(`read_file`, `grep`, `glob`, web, `revelara_*`) never prompt.

## 2. UX (applied ui-ux-pro-max ux guidelines)

- **confirmation-dialogs** (High): confirm before a destructive/irreversible action.
- **destructive-emphasis**: `deny` in danger rose, visually separated; `allow once` is the
  primary; color is never the only signal (each choice has a letter + word).
- **keyboard-nav / escape-routes**: fully keyboard-driven; `Esc` = deny (safe default).
- **truncation-strategy** (Medium): long diffs truncate with an ellipsis + expand.
- **success-feedback**: the approved tool's result renders inline (truncated + expand);
  a denial renders a clear "denied" line.

### Card layout (ASCII mockup)

```
  ✦ Orion
  ╭─ ⚠ permission · edit_file ─────────────────────────╮
  │ src/verdict.go                                     │
  │ @@ -10,3 +10,4 @@ func (v Verdict) OK() bool {      │
  │  context line stays                                │
  │ -old removed line                                  │   (red)
  │ +new added line                                    │   (green)
  │ … +18 more lines · e expand                        │   (dim)
  │                                                    │
  │ y allow once   a allow always   n deny             │
  ╰────────────────────────────────────────────────────╯
```

For `bash`, the body is the command (`$ go test ./...`); no diff.

### Keybindings (single-key, while a tool-permission card is pending)
- `y` / `Enter` → **Allow once** (run it).
- `a` → **Allow always** for this tool this session (run it; future calls skip the prompt).
- `n` / `Esc` → **Deny** (don't run; tell the model).
- `e` → expand / collapse the diff preview (progressive disclosure).

### Color roles (existing palette)
- Card border + title: `cWarning` amber (a gate needing attention).
- Path / command: `cText`. Diff: S1 `colorizeDiff` (green add / red del / lavender hunk).
- Choices: `y` indigo (primary), `a` lavender, `n` rose/danger; truncation hint dim.

### Composition with streaming
The request BLOCKS the turn exactly like the existing spec-ratification gate: the
conductor's tool dispatch pauses (in its own goroutine, off the UI loop) until the human
answers, then the turn resumes. Reuses the `programGate` → `permMsg` → decision channel.

## 3. Architecture

### 3.1 Approval hook (harness)
`harness.Loop` gains an optional hook consulted before dispatching a **destructive** tool:

```go
type Decision int
const ( DecisionAllow Decision = iota; DecisionDeny )

// Approve (may be nil) is consulted before dispatching a Destructive tool.
Approve func(ctx context.Context, name string, input json.RawMessage, safety tools.Safety) Decision
```

`dispatch` looks up the tool's `Safety`; if `Destructive` and `Approve != nil`, it calls
`Approve`. On `DecisionDeny` the tool is **not run** and the result fed back to the model is
`IsError:true`, content "The user denied permission to run <tool>; do not retry — adapt or
ask." Read-only tools skip the hook entirely. Subagents get **no** `Approve` hook (headless)
— their mutating tools keep self-gating on the red button, never prompting.

### 3.2 Wiring (OrionAgent)
`OrionAgent.Prompt` already receives the ACP `ask acp.AskFunc`. It sets `Loop.Approve` to an
approver that:
1. If the tool name is in the session **allow-always** set → `DecisionAllow` (no prompt).
2. Else calls `ask(PermissionRequest{Kind:"tool", …})`; maps the outcome:
   `allow_once`→Allow, `allow_always`→Allow + add to the set, `deny`→Deny.

The allow-always set is per-`OrionAgent` (session-scoped), guarded by the existing mutex.

### 3.3 ACP types
- `PermissionRequest` gains tool fields: `Tool string`, `Command string` (bash), `Path
  string` + `Diff string` (write/edit), used to render the card. `Kind:"tool"`
  distinguishes it from `spec_ratify`.
- `PermissionResult.Outcome` gains `"allow_once"` / `"allow_always"` (existing
  `"granted"`/`"denied"` stay for ratification).

### 3.4 TUI
- A `permMsg` with `Kind:"tool"` renders the tool-permission card (§2) instead of the ratify
  card. The card body uses `colorizeDiff` (from S1) for the diff, truncated to `permDiffLines`
  (default 12) unless expanded.
- Key handling while a tool-perm is pending: `y`/Enter→allow_once, `a`→allow_always,
  `n`/Esc→deny, `e`→toggle expand. These route the decision back through the `pendingPerm`
  channel (extended to carry the outcome string).
- The result of an approved tool renders as an existing tool_result-style block, truncated
  with an expand hint for long output.

## 4. Testing (TDD)
- **harness**: a Destructive tool with `Approve`→Deny is NOT dispatched and yields the denial
  message (IsError); →Allow dispatches normally; a ReadOnly tool never calls Approve.
- **approver**: allow-always set short-circuits the prompt; `allow_always` adds to the set;
  `deny` maps to DecisionDeny.
- **TUI**: a `Kind:"tool"` permMsg renders the command / diff + the three choices; `y`/`a`/`n`
  route the right outcome; `a` records allow-always; `e` toggles truncation; layout invariant
  holds.

## 5. Non-goals
- Persisting allow-always across sessions (session-scoped only).
- A separate general "agent mode" (rejected in the S-decomposition).
- Per-argument policies (allow `bash` but only some commands) — tool-name granularity only.
