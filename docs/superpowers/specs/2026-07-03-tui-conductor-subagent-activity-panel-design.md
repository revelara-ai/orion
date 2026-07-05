# Design: Live conductor/subagent activity panel (TUI)

**Date:** 2026-07-03
**Status:** Approved design — ready for implementation plan
**Area:** `internal/tui`, `internal/acp`, `internal/conductor`, `internal/harness`

## Problem

During a turn there are long pauses (a `build_service` prove can take ~90s; a
`spawn_subagent` runs a full nested loop) where the TUI shows only a spinner and the
literal word **"working"**. The developer can't tell *who* is doing *what* — conductor
vs. a subagent — and the pane looks hung. The feature is a live TUI affordance that
shows the current actor(s) and activity while the spinner is active.

## Goals

- While in-flight, show **who is doing what** across the conductor and any subagents:
  the actor stack (Orion → tool → subagent), a phase strip for the build pipeline
  (generate/align/prove/deliver), and a short scrolling log of recent activity.
- Never appear hung: a liveness heartbeat keeps the panel moving even during a silent
  operation.
- When the turn finishes, collapse the panel to a **one-line summary** of the outcome.

## Non-goals

- Parallel/concurrent subagents. Subagents nest **synchronously** (the parent tool
  blocks on the child loop), so the display is a call *stack* with one active leaf.
  Concurrency is YAGNI; the data model leaves room (Depth) but the renderer assumes a
  stack.
- A persistent always-on pane, or full historical scrollback. Idle state is a single
  summary line.
- Changing what the transcript shows (agent messages, tool bubbles, build report card
  all stay as-is).

## Current state (grounding)

- **One actor, literal label.** `Conversation.View()` renders the status row while
  `inFlight` as `spinner + "working" + spendLine` (`internal/tui/conversation.go`, the
  Bottom-pane block ~L854–859). Nothing downstream carries an actor.
- **The event chain drops identity.** `harness.Event{Kind,Text,Tool,Error}`
  (`internal/harness/harness.go` ~L40) → `OrionAgent.Prompt`'s `loop.Run` onEvent
  closure translates to `acp.Update{SessionID,Kind,Text,Raw}`
  (`internal/acp/client.go` ~L16) via the per-turn `stream func(acp.Update)`
  (`internal/conductor/orionagent.go` ~L80–112). Neither struct has an actor field.
- **Subagents are invisible (the load-bearing gap).** `spawn_subagent`'s nested
  `harness.Loop` onEvent appends inner tool names to a local `trace []string` and
  returns one text blob (`internal/conductor/subagenttool.go` ~L134–166). The tool has
  **no handle to the TUI**: `specTools(c, provider, cs)` (`oriontools.go:28`) and
  `registerSubagentTool(r, c, provider)` (`subagenttool.go:58`) take no sink, and
  `OrionAgent.Prompt` calls `specTools(...)` without passing `stream`
  (`orionagent.go` ~L91).
- **Build phases are buffered, not live.** The Generate/Align/Prove/Deliver `PhaseSink`
  events (`internal/conductor/build.go`) are collected and returned with the build
  report rather than streamed.
- **An orphaned liveness bus already exists.** `internal/tui/progress.go` defines
  `ProgressBus` (`Emit(phase, detail)`, `HeartbeatDue`/`Tick` heartbeat, `Events()`,
  `MaxSilence`, `RenderProgress`) with a `ProgressEvent{Phase,Detail,Heartbeat,At}`.
  It is unwired. It lacks an `Actor` field and a consumer.

## Approach (chosen: A)

Activity rides the existing `stream func(acp.Update)` boundary; the TUI folds updates
into its own (extended) `ProgressBus` for heartbeat + rendering. This respects layering
(lower packages already emit `acp.Update`; only `tui` owns `ProgressBus`) and finally
wires the orphaned bus and its heartbeat.

Rejected alternatives: **B** — a new activity Kind without `ProgressBus` (no heartbeat,
leaves the bus dead); **C** — prefix subagent tool-calls in the transcript (no panel;
rejected by the panel choice).

## Data flow

```
harness.Loop onEvent (Orion's tools/thoughts)  ┐
subagent nested onEvent (tagged name + Depth+1) ┼─▶ emit(acp.Update{Kind:"activity", Actor, Text, Depth, Status})
build PhaseSink: generate/align/prove/deliver   ┘                     │
                                                                      ▼
                                     TUI onUpdate sink ─▶ ProgressBus(+Actor) ─▶ Activity panel (View)
                                                            heartbeat Tick (tea.Tick)     stack + phases + log
```

## Components & changes

1. **`internal/acp` — carry identity (additive).**
   Add to `Update`: `Actor string`, `Depth int`, `Status string` (`"running" | "done" |
   "fail"`). Introduce `Kind:"activity"`. Existing kinds and consumers are untouched
   (the pipeline fans unknown kinds through unchanged).

2. **`internal/conductor/orionagent.go` — emit Orion's activity + thread the sink.**
   - In the `loop.Run` onEvent closure, alongside the existing `tool_call`/`agent_message`
     emits, emit `acp.Update{Kind:"activity", Actor:"Orion", Depth:0, Text:e.Tool,
     Status:"running"}` on `EventToolCall` and `"done"` on `EventToolResult`.
   - Pass the per-turn `stream` down: `specTools(a.conductor, prov, cs, emit)` where
     `emit` wraps `stream` (or is `stream` itself).

3. **`internal/conductor/oriontools.go` — thread `emit` through `specTools`.**
   Add an `emit func(acp.Update)` parameter; forward it to `registerSubagentTool` and to
   the build tool so the phase sink can stream. (No behavior change when `emit` is nil —
   guard for the non-TUI callers/tests.)

4. **`internal/conductor/subagenttool.go` — surface subagent activity (the crux).**
   - `registerSubagentTool` gains `emit func(acp.Update)`.
   - **Identity:** add an optional `name` param to `spawn_subagent`; fallback to a short
     label derived from the task. Used as `Actor`.
   - **Re-emit:** the nested `onEvent` emits each inner `EventToolCall`/`EventThought`
     as `acp.Update{Kind:"activity", Actor:name, Depth:1, Text:e.Tool, Status:"running"}`
     (in addition to, or instead of, the local `trace`). Emit a `Status:"done"` when the
     subagent returns.

5. **`internal/conductor/build.go` — stream build phases.**
   The Generate/Align/Prove/Deliver `PhaseSink` emits `acp.Update{Kind:"activity",
   Actor:"Orion", Depth:0, Text:<phase>, Status:<done|running|fail>}` as each phase
   fires, via the threaded `emit`. Phases feed the panel's phase strip.

6. **`internal/tui/progress.go` — actor-aware liveness bus.**
   Add `Actor string` (and `Depth int`, `Status string`) to `ProgressEvent`; extend
   `Emit` accordingly. Keep the heartbeat (`Tick`/`HeartbeatDue`) and `MaxSilence`.

7. **`internal/tui/conversation.go` — model, ingestion, render, layout.**
   - **Model:** an `activity` sub-model holding the current **actor stack**
     (`[]actorFrame{actor, activity, depth}`), the phase set/status, and a ring buffer
     of the last ~4 log lines; plus an `idleSummary string`. Hold a `*ProgressBus`.
   - **Ingestion:** in the `streamMsg` case, branch on `Kind=="activity"`: update the
     stack (push on `running` at a new depth, pop/replace on `done`), phase strip, and
     log — **without** appending a transcript `msg` (no bubble, no coalescing impact).
     On `turnDoneMsg`, compute `idleSummary` from the final phase/deliver state and clear
     the stack.
   - **Heartbeat:** a `tea.Tick` cmd (active only while `inFlight`) calls `bus.Tick`; a
     heartbeat event nudges the log so a silent prove never looks hung.
   - **Render:** a new bordered **activity pane** rendered between the transcript and
     input while `inFlight` (actor stack as a tree, phase strip, recent log, spend line);
     collapse to the 1-line `idleSummary` when idle. Update the `View()` height math so
     the transcript viewport reflows (the layout is height-exact today).

## Rendering (target)

Working:
```
╭─ activity ─────────────────────────╮
│ ⠋ Orion · proving                  │
│   generate ✓  align ✓  prove ⠋     │
│   ↳ subagent(research) · web_search│
│   · 12.4k tok · $0.08 · 23s        │
╰────────────────────────────────────╯
```
Idle:
```
  ✓ delivered · 3 files · generate/align/prove
```

## Concurrency model

Synchronous nesting → a call stack. `Depth` distinguishes conductor (0) from subagent
(1). The renderer shows the stack; if two frames ever share a depth (future
concurrency), the renderer shows the most recent — no crash, just under-display. Not
built for now.

## Testing

- **`ProgressBus`** (`progress_test.go`): actor/depth carried through `Emit`; heartbeat
  still bounds `MaxSilence`; a `done` status resolves a running frame.
- **TUI** (`conversation_test.go` style, `feed`/`View`): feeding synthetic
  `acp.Update{Kind:"activity"}` frames renders the actor stack (Orion + a Depth:1
  subagent) in the pane; `turnDoneMsg` collapses it to the one-line summary; layout stays
  height-exact (the `lipgloss.Height(View())==H` invariant the existing tests assert).
- **Conductor** (closing the load-bearing gap with a real assertion): a `spawn_subagent`
  run drives its threaded `emit`, and the test asserts at least one
  `acp.Update{Kind:"activity", Depth:1, Actor:<subagent>}` was emitted — i.e. inner
  subagent activity is no longer swallowed.
- **Non-TUI callers:** `emit == nil` is a no-op (guarded), so CLI/tests that build
  `specTools` without a stream are unaffected.

## Defaults (adjustable)

- Heartbeat interval: ~2s (matches "never silent" without churn).
- Recent-log ring buffer: last 4 lines.
- Subagent label: `name` param if given, else first ~2 words of the task.
- Actor glyph: `↳` for nested frames; phase marks `✓ / ⠋ / ✗`.

## Open questions

None blocking. The `name` param vs. derived-label choice and the exact heartbeat
interval are cheap to tune during implementation.
