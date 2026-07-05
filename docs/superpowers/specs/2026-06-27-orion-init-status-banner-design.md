# Orion init status banner + conductor workflow-awareness — design

**Date:** 2026-06-27
**Status:** Approved (design); decomposed into a beads epic.
**Author:** brainstorming session (josebiro)

## Motivation

Hermes Agent prints a rich, branded status panel the moment it initializes: a
gold wordmark, a caduceus, and a titled panel that reports identity (model, cwd,
session) on the left and a capability/health inventory (tools, MCP servers,
skills, update state) on the right. It tells you, at a glance, *what the agent is
and whether it's ready*.

Orion's TUI today shows only a one-line header (`✦ Orion · <brain>`) plus a
budget status line, and a separate `orion doctor` health command. We want Orion
to have the same "informative status at init" — branded to Orion's existing
indigo/lavender Revelara palette — but reporting what is true for Orion rather
than copying Hermes' tool/skill model.

Orion is a **conductor** that drives a generate→prove→deliver pipeline, not a
chat agent with a flat tool catalog. So the honest analog of Hermes' right column
is **live pipeline-gate and subsystem readiness**, not a tool list.

Two adjacent requests are folded into this work:

1. Remove the hand-holding input placeholder (`your intent…`) — leave it blank.
2. Give the conductor awareness of Orion's opinionated workflow so it can explain
   it when a developer asks.

## Goals

- A branded, two-column status banner shown at Orion startup, in Orion's palette.
- The same banner available non-interactively via the CLI.
- A **single source of truth** for readiness, shared between `orion doctor` and
  the banner (no drift between two implementations of "is bwrap present").
- Blank the initial input placeholder.
- Conductor can explain the Orion workflow on request.

## Non-goals

- No network calls on the TUI launch path (must stay instant). Live Polaris
  reachability belongs to the CLI `orion status`, where blocking is acceptable.
- No alt-screen surgery — the banner reuses the existing empty-state slot.
- No update-check/self-update mechanism (Hermes has one; Orion does not need it
  here). The title shows version + git branch only.
- No skin/theming engine. One palette: the existing Revelara colors.

## The visual

The gold caduceus becomes the **Orion constellation**; the gold wordmark becomes
an **indigo→lavender ORION** using the palette already defined at
`internal/tui/conversation.go:63-90`. The right column is live **pipeline gates +
subsystem readiness**.

```
 ██████  ██████  ██  ██████  ███   ██
██    ██ ██   ██ ██ ██    ██ ████  ██        ← indigo→lavender gradient wordmark
██    ██ ██████  ██ ██    ██ ██ ██ ██          (hidden when term < ~90 cols)
██    ██ ██   ██ ██ ██    ██ ██  ████
 ██████  ██   ██ ██  ██████  ██   ███

╭─ Orion v0.0.0-dev+a1e93c1 · main ──────────────────────────────────────╮
│        ✦                     Pipeline                                   │
│      ·   ·                     intent → spec      ✓                     │
│    ✦       ✦                   completeness gate  ✓                     │
│      ·   ·                     proof harness      ✓ behavioral·empirical│
│   ✦  ✦  ✦                                           ·hazard             │
│      ·   ·                     lsp gate (gopls)    ✓                     │
│    ✦       ✦                                                            │
│        ·                     Subsystems                                 │
│                                sandbox (bwrap)     ✓                     │
│  native · claude-opus-4-8      context store       ✓                     │
│  1M context · Anthropic        memory              ✓                     │
│  ~/go/src/.../orion            tracker (beads)     ✓                     │
│  Session: 20260627_1003        polaris             ⚠ not logged in       │
│  budget: 0 tok · $0.00         agent preset        ✓ fixture            │
│                                                                        │
│                          8/9 ready · 1 warning · /help for commands     │
╰────────────────────────────────────────────────────────────────────────╯
Welcome to Orion. Describe an intent, or ask how the build works.
```

- **Left column (identity):** constellation art; brain line
  (`native · <model> · <context> · Anthropic`, or amber `offline — deterministic`
  when `ANTHROPIC_API_KEY` is unset); cwd; session id; budget snapshot.
- **Right column (readiness):** two groups — **Pipeline** (the generate→prove→
  deliver gates) and **Subsystems** (the doctor probes). Each row is `✓`/`⚠`/`✗`
  color-coded with the success/warning/danger glyphs already defined at
  `internal/tui/conversation.go:88-90`. This mirrors how Hermes colors connected
  vs. disabled tools.
- **Title:** `Orion <version> · <git-branch>`, version from `resolveVersion()`
  (`cmd/orion/main.go:140`).
- **Summary line:** `N/M ready · K warnings · /help for commands` — the Orion
  analog of Hermes' `29 tools · 190 skills · /help for commands`.
- **Welcome line (below panel):** replaces today's verbose empty-state text;
  points at both actions ("describe an intent, or ask how the build works").

## Architecture

The structural principle: **one readiness source, two render surfaces.**

### `internal/health` (new) — pure readiness data

Today `cmd/orion/doctor.go:78` (`doctorChecks`) computes readiness inline. The
banner needs the same signals. Rather than duplicate, extract a package:

- `type Status string` — `ok` / `warn` / `fail` (mirrors doctor's `checkStatus`).
- `type Check struct { Name string; Status Status; Detail string }`.
- `type Report struct { Pipeline []Check; Subsystems []Check }` — grouped for the
  banner's two right-column sections.
- `func Probe(opts Options) Report` — runs all probes. `Options` injects
  `lookPath`, env getters, and the data dir so the probes stay testable (the
  pattern doctor already uses at `doctor.go:78`).
- Probes are **cheap, local, non-panicking**. Each probe that errors yields a
  `fail`/`warn` row carrying the error string; a probe never panics the caller
  (the Go analog of Hermes wrapping every banner section in try/except).
- **No network.** The Polaris probe reports cached-credential presence only, from
  the token store on disk — never a live `Me()` call on this path.

`cmd/orion/doctor.go` is refactored to call `health.Probe` and render the result
in its existing `[%-4s] %-16s %s` line format. Doctor's observable behavior
(check names, statuses, exit code) is preserved; this is verified by its existing
tests plus a golden comparison.

#### Probe inventory

| Group | Check | Source / detection |
|---|---|---|
| Pipeline | `intent → spec` | conductor/spec flow reachable (always ok unless data dir broken) |
| Pipeline | `completeness gate` | analyzer constructs (orchestrator) |
| Pipeline | `proof harness` | `internal/proof` modes present (behavioral/empirical/hazard) |
| Pipeline | `lsp gate (gopls)` | `lookPath("gopls")` — warn/skip if absent (`internal/lspcheck`) |
| Subsystem | `sandbox (bwrap)` | `lookPath("bwrap")` — warn → safeenv fallback (`doctor.go:119`) |
| Subsystem | `context store` | `contextstore.Open(dir)` openable (`doctor.go:100`) |
| Subsystem | `memory` | `memory.Open(memDir)` openable (`doctor.go:109`) |
| Subsystem | `tracker (beads)` | tracker backend resolvable (`cmd/orion/tracker.go`) |
| Subsystem | `polaris` | cached credential presence (`cmd/orion/polaris.go` token store) |
| Subsystem | `agent preset` | `doctorAgentCheck` — fixture/native/unknown (`doctor.go:133`) |

The exact pipeline-gate detection (how to cheaply assert the proof harness and
completeness analyzer are wired) is refined per-subissue against the actual
`internal/proof` and orchestrator packages.

### `internal/tui/banner.go` (new) — pure renderer

- `func Render(rep health.Report, id Identity, width int) string` where
  `Identity` carries version, branch, model/brain label, context-window string,
  cwd, session id, and budget snapshot.
- Uses the existing lipgloss palette and glyph styles from `conversation.go`
  (do not redefine colors; reference the package vars).
- **Width-responsive:** below ~90 cols, drop the wordmark and/or collapse to a
  single column (Hermes hides its wordmark below 95 cols). Never overflow.
- **Pure:** no I/O, deterministic given inputs → golden/snapshot testable.

One renderer, two call sites:

1. **TUI** embeds the string as the empty-state body.
2. **`orion status`** prints it to stdout (lipgloss renders ANSI to a string and
   degrades acceptably for non-TTY).

## TUI integration

Render the banner as the **empty-state body** inside the existing transcript
viewport. The block at `internal/tui/conversation.go:392-399` already swaps
`body` to `emptyState` when `len(m.msgs)==0`. The banner replaces that text; the
compact `✦ Orion · <brain>` header at `conversation.go:410` stays. The banner is
shown until the first message is sent, then the conversation replaces it — the
Hermes feel, with no alt-screen changes.

The renderer needs the terminal width, already tracked as `m.width`.

### Blank placeholder

- `conversation.go:136` `ti.Placeholder = "your intent…"` → `""`.
- `conversation.go:194` reset `"new intent…"` → `""`.
- **Keep** the action-hint placeholders `"y to ratify · e to edit"`
  (`conversation.go:202`) and `"your reply…"` (`conversation.go:276`) — these
  convey required input format, not hand-holding.

## CLI integration

Repurpose the existing `orion status` (`cmd/orion/main.go:74` → `cmdStatus` in
`cmd/orion/polaris.go`, currently Polaris-only) to print the **full banner**. The
current Polaris connection line becomes the `polaris` subsystem row. `orion
status` is the natural home for "show me everything's state," and the existing
output is a strict subset.

Because the CLI path can block, `orion status` performs the **live** Polaris
reachability probe (the `Me()` call the current command already does) and folds
its result into the `polaris` row — while the TUI launch banner stays
network-free (cached-credential presence only). This is the one place the two
surfaces differ, and it is deliberate.

## Conductor workflow-awareness

Add a compact **"The Orion workflow"** section to the conductor's priming
(`internal/conductor/role.go:30` `Render()` and/or `orionagent.go:94`
`systemPrompt()`):

- The opinionated loop: intent → adversarial grill → ratified spec →
  `build_service` → 3-mode proof harness must converge (behavioral, empirical,
  hazard) → delivery into the developer's repo.
- The philosophy: *you propose; the deterministic gates verify; a human
  authorization is never a substitute for proof.* (This restates the invariants
  already in `role.go:44-47`, framed as something to explain.)
- An explicit instruction: *if the developer asks how Orion works, what the
  workflow is, or what the gates are, explain it concisely.*

Kept short to avoid token bloat. Verified by a test asserting the workflow
section is present in the rendered prompt.

## Resilience & performance

- TUI launch banner: all-local probes, no network, no goroutine blocking.
- Every probe is defensive — a failure becomes a `✗ <reason>` row; the banner
  never panics and always renders.
- Renderer is pure and width-bounded; degrades on narrow terminals rather than
  overflowing.

## Testing

- `internal/health.Probe` — table tests with injected `lookPath`/env (doctor's
  existing pattern).
- `cmd/orion/doctor.go` — existing tests preserved; assert no behavior change
  after the refactor.
- `internal/tui/banner.go` — golden/snapshot tests at a few widths × offline vs.
  native × a failing-probe row.
- `cmd/orion` status — assert the banner renders and the Polaris row reflects
  login state.
- `internal/conductor` — assert the workflow section is present in the prompt.

## Epic breakdown

**Epic:** Orion branded init status banner + conductor workflow-awareness

1. **`internal/health`** — extract shared readiness probes into a grouped
   `Report`; `doctor.go` consumes it, behavior preserved. *(foundation)*
2. **Banner renderer** (`internal/tui/banner.go`) — wordmark + constellation +
   two-column panel, width-responsive, golden-tested. *(depends on 1)*
3. **TUI integration** — banner as empty-state body; blank the initial input
   placeholder. *(depends on 2)*
4. **`orion status` → full banner** — fold in the live Polaris probe.
   *(depends on 2)*
5. **Conductor workflow-awareness** — workflow section + explain-on-ask
   instruction in the role/system prompt. *(independent)*

## Decisions resolved

- **Surface:** TUI opening view + `orion status` (one renderer, two call sites).
- **Right-column content:** live pipeline gates + subsystem readiness.
- **`orion status`:** repurposed (not a new `orion info`); Polaris folds in.
- **Placeholders:** blank the two intent placeholders; keep the action-hint ones.
- **Art motif:** Orion constellation (the hunter), not a caduceus copy.
