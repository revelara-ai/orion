# Live conductor/subagent activity panel — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** While a turn is in flight, show a TUI panel of who (conductor / subagent) is doing what — actor stack, build-phase strip, recent-activity log, with a liveness heartbeat — collapsing to a one-line summary when idle.

**Architecture:** Activity rides the existing `stream func(acp.Update)` boundary (extended `acp.Update` with `Actor/Depth/Status` and a new `Kind:"activity"`). Three sources emit it — the conductor's harness loop, the nested `spawn_subagent` loop, and the build `PhaseSink`. The TUI folds those updates into the (now actor-aware) `internal/tui.ProgressBus` and renders a new pane.

**Tech Stack:** Go, Bubble Tea / Lip Gloss / Bubbles (charmbracelet), `internal/harness` native agent loop, `internal/acp` transport.

**Design spec:** `docs/superpowers/specs/2026-07-03-tui-conductor-subagent-activity-panel-design.md`

## Global Constraints

- TUI must stay on bubbletea/lipgloss/bubbles (no other TUI libs).
- `View()` is height-exact: `lipgloss.Height(m.View()) == m.height` — every layout change must preserve this (existing tests assert it).
- `emit`/stream sinks are optional: a nil `emit` MUST be a no-op so non-TUI callers (CLI, tests) that build `specTools` without a stream are unaffected.
- Mutation/lifecycle paths are out of scope; this is display-only. No change to existing `acp.Update` kinds or transcript bubbles.
- Additive only to `acp.Update` — existing kinds (`agent_message`, `tool_call`, `plan`, `spec`, `build_report`, `permission`) and their handling are untouched.

---

### Task 1: `acp.Update` carries actor identity

**Files:**
- Modify: `internal/acp/client.go` (the `Update` struct, ~L16-21)
- Test: `internal/acp/activity_test.go` (create)

**Interfaces:**
- Produces: `acp.Update{ ..., Actor string, Depth int, Status string }`; `const ActivityKind = "activity"`; `func Activity(actor, activity string, depth int, status string) Update`.
- Consumed by: Tasks 3, 4 (emit) and Task 5 (ingest).

- [ ] **Step 1: Write the failing test**

```go
// internal/acp/activity_test.go
package acp

import "testing"

func TestActivityUpdate(t *testing.T) {
	u := Activity("research", "web_search", 1, "running")
	if u.Kind != ActivityKind {
		t.Fatalf("Kind = %q, want %q", u.Kind, ActivityKind)
	}
	if u.Actor != "research" || u.Text != "web_search" || u.Depth != 1 || u.Status != "running" {
		t.Fatalf("unexpected fields: %+v", u)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/acp/ -run TestActivityUpdate -v`
Expected: FAIL — `undefined: Activity` / `ActivityKind`.

- [ ] **Step 3: Add the fields + constructor**

In `internal/acp/client.go`, extend `Update`:

```go
type Update struct {
	SessionID string          `json:"sessionId"`
	Kind      string          `json:"kind"`
	Text      string          `json:"text,omitempty"`
	Actor     string          `json:"actor,omitempty"`  // who is acting (activity kind): "Orion" or a subagent label
	Depth     int             `json:"depth,omitempty"`  // 0 = conductor, 1 = subagent (call-stack nesting)
	Status    string          `json:"status,omitempty"` // activity kind: "running" | "done" | "fail"
	Raw       json.RawMessage `json:"-"`
}

// ActivityKind marks a live "who is doing what" signal — rendered in the activity
// panel, never as a transcript bubble.
const ActivityKind = "activity"

// Activity builds an activity update. depth 0 = the conductor, 1 = a subagent.
func Activity(actor, activity string, depth int, status string) Update {
	return Update{Kind: ActivityKind, Actor: actor, Text: activity, Depth: depth, Status: status}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/acp/ -run TestActivityUpdate -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/acp/client.go internal/acp/activity_test.go
git commit -m "feat(acp): activity update — actor/depth/status carrier"
```

---

### Task 2: `ProgressBus` becomes actor-aware

**Files:**
- Modify: `internal/tui/progress.go` (`ProgressEvent` ~L13, `Emit` ~L38)
- Test: `internal/tui/progress_test.go` (create or extend)

**Interfaces:**
- Produces: `ProgressEvent{ Phase, Detail string, Actor string, Depth int, Status string, Heartbeat bool, At time.Time }`; `func (b *ProgressBus) EmitActivity(actor, activity string, depth int, status string)` (keeps the existing `Emit(phase, detail)` working).
- Consumed by: Task 5 (TUI ingest).

- [ ] **Step 1: Write the failing test**

```go
// internal/tui/progress_test.go  (add to package tui)
func TestProgressBusCarriesActor(t *testing.T) {
	b := NewProgressBus(time.Second)
	b.EmitActivity("research", "web_search", 1, "running")
	ev := b.Events()
	if len(ev) != 1 {
		t.Fatalf("want 1 event, got %d", len(ev))
	}
	if ev[0].Actor != "research" || ev[0].Depth != 1 || ev[0].Detail != "web_search" || ev[0].Status != "running" {
		t.Fatalf("actor not carried: %+v", ev[0])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/ -run TestProgressBusCarriesActor -v`
Expected: FAIL — `EmitActivity` undefined / `Actor` field missing.

- [ ] **Step 3: Extend the event + add EmitActivity**

In `internal/tui/progress.go`, add to `ProgressEvent`: `Actor string`, `Depth int`, `Status string`. Then:

```go
// EmitActivity records a who-is-doing-what event (actor + activity + call-stack
// depth + status). Resets the heartbeat window like Emit.
func (b *ProgressBus) EmitActivity(actor, activity string, depth int, status string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	t := b.now()
	b.events = append(b.events, ProgressEvent{
		Phase: activity, Detail: activity, Actor: actor, Depth: depth, Status: status, At: t,
	})
	b.last = t
}
```

- [ ] **Step 4: Run tests (new + existing heartbeat)**

Run: `go test ./internal/tui/ -run 'TestProgressBus' -v`
Expected: PASS — new test green; any existing `ProgressBus` heartbeat/`MaxSilence` tests still green.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/progress.go internal/tui/progress_test.go
git commit -m "feat(tui): ProgressBus carries actor/depth/status"
```

---

### Task 3: Thread the `emit` sink to spawn_subagent — surface nested activity (load-bearing)

**Files:**
- Modify: `internal/conductor/oriontools.go` (`specTools` ~L28, the `registerSubagentTool` call ~L35)
- Modify: `internal/conductor/subagenttool.go` (`registerSubagentTool` ~L58, params ~L77-80, nested `onEvent` ~L142-146)
- Modify: `internal/conductor/orionagent.go` (`specTools(...)` call ~L91, the `loop.Run` onEvent ~L96-112)
- Test: `internal/conductor/subagent_activity_test.go` (create)

**Interfaces:**
- Consumes: `acp.Activity(...)` (Task 1).
- Produces: `func specTools(c *orchestrator.Conductor, provider llm.Provider, cs *changeSession, emit func(acp.Update)) *tools.Registry`; `func registerSubagentTool(r *tools.Registry, c *orchestrator.Conductor, provider llm.Provider, emit func(acp.Update))`. `emit` may be nil (no-op).

- [ ] **Step 1: Write the failing test**

```go
// internal/conductor/subagent_activity_test.go
package conductor

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/revelara-ai/orion/internal/acp"
	"github.com/revelara-ai/orion/internal/tools"
)

// A spawned subagent's inner tool calls must surface as Depth:1 activity updates —
// they are no longer swallowed into the tool's local trace (the load-bearing gap).
func TestSubagentSurfacesInnerActivity(t *testing.T) {
	var got []acp.Update
	r := tools.NewRegistry()
	// Use the offline stub provider + conductor helper the other conductor tests use.
	c, prov := offlineConductorWithStubTool(t) // see helper note below
	registerSubagentTool(r, c, prov, func(u acp.Update) { got = append(got, u) })

	tool, ok := r.Get("spawn_subagent")
	if !ok {
		t.Fatal("spawn_subagent not registered")
	}
	if _, err := tool.Run(context.Background(), json.RawMessage(`{"task":"grep for X","tools":["grep"]}`)); err != nil {
		t.Fatalf("run: %v", err)
	}
	var sawDepth1 bool
	for _, u := range got {
		if u.Kind == acp.ActivityKind && u.Depth == 1 {
			sawDepth1 = true
		}
	}
	if !sawDepth1 {
		t.Fatalf("no Depth:1 subagent activity surfaced; got %d updates: %+v", len(got), got)
	}
}
```

> Helper note: reuse the existing offline/stub-provider harness the conductor tests already use to drive a nested loop deterministically (grep `internal/conductor/*_test.go` for the stub `llm.Provider` that returns a canned tool call). If none is reusable as-is, add a minimal stub provider in this test file that returns exactly one `grep` tool call then stops — the assertion only needs one inner `EventToolCall`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/conductor/ -run TestSubagentSurfacesInnerActivity -v`
Expected: FAIL — `registerSubagentTool` takes 3 args, not 4 (compile error) → then, after wiring, no Depth:1 update until Step 3b.

- [ ] **Step 3a: Add `emit` params (compile the new signatures)**

`internal/conductor/oriontools.go` — `specTools` signature + forward:

```go
func specTools(c *orchestrator.Conductor, provider llm.Provider, cs *changeSession, emit func(acp.Update)) *tools.Registry {
	// ... existing body ...
	registerSubagentTool(r, c, provider, emit) // was: registerSubagentTool(r, c, provider)
	// ... rest unchanged ...
}
```

`internal/conductor/subagenttool.go` — `registerSubagentTool` signature + optional `name` param:

```go
func registerSubagentTool(r *tools.Registry, c *orchestrator.Conductor, provider llm.Provider, emit func(acp.Update)) {
	// ... unchanged guard + Register(...) ...
	// extend the params struct:
	var p struct {
		Task  string   `json:"task"`
		Tools []string `json:"tools"`
		Name  string   `json:"name"`
	}
	// ... after resolving `granted`, derive the label:
	label := strings.TrimSpace(p.Name)
	if label == "" {
		label = subagentLabel(p.Task) // first ~2 words of the task
	}
	// ... in the loop.Run onEvent, re-emit (Step 3b) ...
}
```

Add the label helper (same file):

```go
// subagentLabel derives a short display label from a task when no name is given.
func subagentLabel(task string) string {
	fields := strings.Fields(task)
	if len(fields) > 2 {
		fields = fields[:2]
	}
	if len(fields) == 0 {
		return "subagent"
	}
	return strings.ToLower(strings.Join(fields, " "))
}
```

Also extend the InputSchema string to document the optional `name` (append a `"name"` property).

`internal/conductor/orionagent.go` — pass a nil-safe `emit` wrapping `stream`, and emit Orion's own Depth:0 tool activity:

```go
emit := func(u acp.Update) { stream(u) } // stream is always non-nil in Prompt
loop := harness.Loop{
	Provider:   prov,
	Tools:      specTools(a.conductor, prov, a.changeSessionFor(sessionID), emit),
	// ...
}
convo, _, err := loop.Run(ctx, convo, func(e harness.Event) {
	switch e.Kind {
	// ... existing agent_message / build_report cases unchanged ...
	case harness.EventToolCall:
		stream(acp.Update{Kind: "tool_call", Text: "· " + e.Tool})     // existing bubble
		emit(acp.Activity("Orion", e.Tool, 0, "running"))              // NEW: conductor activity
	case harness.EventToolResult:
		emit(acp.Activity("Orion", e.Tool, 0, "done"))                 // NEW: conductor activity
		// ... existing build_report case unchanged ...
	}
})
```

Update every OTHER caller of `specTools` (grep `specTools(` across `internal/` and `cmd/`) to pass a trailing `nil` — e.g. any offline/CLI construction.

- [ ] **Step 3b: Re-emit inner subagent events**

In `internal/conductor/subagenttool.go`, replace the nested `onEvent` (currently only appends to `trace`):

```go
convo, resp, runErr := loop.Run(cctx, []llm.Message{llm.TextMessage(llm.RoleUser, p.Task)},
	func(e harness.Event) {
		switch e.Kind {
		case harness.EventToolCall:
			trace = append(trace, e.Tool)
			if emit != nil {
				emit(acp.Activity(label, e.Tool, 1, "running"))
			}
		case harness.EventThought:
			if emit != nil && strings.TrimSpace(e.Text) != "" {
				emit(acp.Activity(label, "thinking", 1, "running"))
			}
		}
	})
if emit != nil {
	emit(acp.Activity(label, "", 1, "done")) // subagent frame resolved
}
```

Add the `acp` import to `subagenttool.go`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/conductor/ -run TestSubagentSurfacesInnerActivity -v && go build ./...`
Expected: PASS + clean build (all `specTools` callers updated).

- [ ] **Step 5: Commit**

```bash
git add internal/conductor/oriontools.go internal/conductor/subagenttool.go internal/conductor/orionagent.go internal/conductor/subagent_activity_test.go
git commit -m "feat(conductor): surface subagent + conductor activity via emit sink"
```

---

### Task 4: Stream build-pipeline phases as activity

**Files:**
- Modify: `internal/conductor/oriontools.go` (the `build_service` tool's `onPhase` closure ~L360; the `PhaseSink` is a `func(PhaseEvent)` type with a nil-safe `emit` method)
- Test: `internal/conductor/build_activity_test.go` (create)

**Interfaces:**
- Consumes: the `emit func(acp.Update)` now on `specTools` (Task 3); `PhaseEvent{Phase string, Status PhaseStatus, Detail string}` and `PhaseStatus` (`PhaseRunning|PhaseDone|PhaseWarn`) from `build.go`.
- Produces: `func phaseStatusToActivity(s PhaseStatus) string`.

- [ ] **Step 1: Write the failing test**

```go
// internal/conductor/build_activity_test.go
package conductor

import "testing"

func TestPhaseStatusToActivity(t *testing.T) {
	cases := map[PhaseStatus]string{PhaseRunning: "running", PhaseDone: "done", PhaseWarn: "fail"}
	for in, want := range cases {
		if got := phaseStatusToActivity(in); got != want {
			t.Errorf("phaseStatusToActivity(%v) = %q, want %q", in, got, want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/conductor/ -run TestPhaseStatusToActivity -v`
Expected: FAIL — `phaseStatusToActivity` undefined.

- [ ] **Step 3: Map + re-emit phases**

In `internal/conductor/oriontools.go`, add the mapper and stream phases from the `build_service` `onPhase` closure. Change:

```go
res, err := BuildAndProve(ctx, st, gen, aligner, func(e PhaseEvent) { phases = append(phases, e) }, OutputRoot())
```

to:

```go
res, err := BuildAndProve(ctx, st, gen, aligner, func(e PhaseEvent) {
	phases = append(phases, e)
	if emit != nil {
		emit(acp.Activity("Orion", e.Phase, 0, phaseStatusToActivity(e.Status)))
	}
}, OutputRoot())
```

Add:

```go
func phaseStatusToActivity(s PhaseStatus) string {
	switch s {
	case PhaseDone:
		return "done"
	case PhaseWarn:
		return "fail"
	default:
		return "running"
	}
}
```

(Do the same for the `build_change` tool's `onPhase` closure if it has one — grep `func(e PhaseEvent)` in `oriontools.go`.)

- [ ] **Step 4: Run test + build**

Run: `go test ./internal/conductor/ -run TestPhaseStatusToActivity -v && go build ./...`
Expected: PASS + clean build.

- [ ] **Step 5: Commit**

```bash
git add internal/conductor/oriontools.go internal/conductor/build_activity_test.go
git commit -m "feat(conductor): stream build-pipeline phases as activity"
```

---

### Task 5: TUI activity model + ingestion

**Files:**
- Modify: `internal/tui/conversation.go` (struct ~L119-156; `streamMsg` case ~L208-225; `turnDoneMsg` case ~L248-254; `handleEnter` where `inFlight` is set ~L441)
- Create: `internal/tui/activity.go` (the activity sub-model + reduce logic + summary)
- Test: `internal/tui/activity_test.go` (create)

**Interfaces:**
- Consumes: `acp.Update` activity fields (Task 1); `*ProgressBus` + `EmitActivity` (Task 2).
- Produces: `type activityModel struct{ stack []actorFrame; phases []phaseMark; log []string; bus *ProgressBus; summary string }`; `func (a *activityModel) apply(u acp.Update)`; `func (a *activityModel) reset()`; `func (a *activityModel) finish()` (compute summary + clear stack); `type actorFrame struct{ actor, activity string; depth int }`; `type phaseMark struct{ name, status string }`.

- [ ] **Step 1: Write the failing test**

```go
// internal/tui/activity_test.go
package tui

import (
	"testing"

	"github.com/revelara-ai/orion/internal/acp"
)

func TestActivityModelStackAndPhases(t *testing.T) {
	a := newActivityModel()
	a.apply(acp.Activity("Orion", "build_service", 0, "running"))
	a.apply(acp.Activity("Orion", "generate", 0, "done"))
	a.apply(acp.Activity("research", "web_search", 1, "running"))

	if len(a.stack) == 0 || a.stack[len(a.stack)-1].actor != "research" {
		t.Fatalf("deepest frame should be the subagent; stack=%+v", a.stack)
	}
	if !hasPhase(a.phases, "generate", "done") {
		t.Fatalf("generate phase not recorded done: %+v", a.phases)
	}

	a.apply(acp.Activity("research", "", 1, "done")) // subagent resolves
	if deepestActor(a.stack) == "research" {
		t.Fatalf("resolved subagent should be popped; stack=%+v", a.stack)
	}
}
```

(Add tiny test helpers `hasPhase`/`deepestActor` in the test file.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/ -run TestActivityModelStackAndPhases -v`
Expected: FAIL — `newActivityModel` undefined.

- [ ] **Step 3: Implement the activity model**

Create `internal/tui/activity.go`:

```go
package tui

import (
	"fmt"
	"strings"

	"github.com/revelara-ai/orion/internal/acp"
)

type actorFrame struct {
	actor    string
	activity string
	depth    int
}

type phaseMark struct {
	name   string
	status string // running | done | fail
}

type activityModel struct {
	stack   []actorFrame // call stack; index 0 = conductor, deeper = subagent
	phases  []phaseMark  // build-pipeline strip, in first-seen order
	log     []string     // ring buffer of the last logCap lines
	summary string       // one-line idle summary
	bus     *ProgressBus
}

const logCap = 4

func newActivityModel() activityModel {
	return activityModel{bus: NewProgressBus(2 * time.Second)}
}

// apply folds one activity update into the model.
func (a *activityModel) apply(u acp.Update) {
	if u.Kind != acp.ActivityKind {
		return
	}
	a.bus.EmitActivity(u.Actor, u.Text, u.Depth, u.Status)

	// Build phases (depth 0, a known phase name) drive the phase strip.
	if u.Depth == 0 && isPhaseName(u.Text) {
		a.setPhase(u.Text, u.Status)
	}

	switch u.Status {
	case "done", "fail":
		a.popTo(u.depthOrZero()) // resolve this frame and anything deeper
	default: // running
		a.pushOrReplace(actorFrame{actor: u.Actor, activity: u.Text, depth: u.Depth})
	}
	if u.Text != "" {
		a.pushLog(fmt.Sprintf("%s · %s", u.Actor, u.Text))
	}
}

func (a *activityModel) pushOrReplace(f actorFrame) {
	// Replace the frame at this depth if present; else push, truncating deeper frames.
	for len(a.stack) > f.depth {
		a.stack = a.stack[:len(a.stack)-1]
	}
	a.stack = append(a.stack, f)
}

func (a *activityModel) popTo(depth int) {
	for len(a.stack) > depth {
		a.stack = a.stack[:len(a.stack)-1]
	}
}

func (a *activityModel) setPhase(name, status string) {
	for i := range a.phases {
		if a.phases[i].name == name {
			a.phases[i].status = status
			return
		}
	}
	a.phases = append(a.phases, phaseMark{name: name, status: status})
}

func (a *activityModel) pushLog(line string) {
	a.log = append(a.log, line)
	if len(a.log) > logCap {
		a.log = a.log[len(a.log)-logCap:]
	}
}

// finish computes the one-line idle summary and clears the live stack.
func (a *activityModel) finish() {
	var done []string
	for _, p := range a.phases {
		done = append(done, p.name)
	}
	if len(done) > 0 {
		a.summary = "✓ " + strings.Join(done, "/")
	} else {
		a.summary = ""
	}
	a.stack = nil
}

func (a *activityModel) reset() {
	a.stack, a.phases, a.log, a.summary = nil, nil, nil, ""
	a.bus = NewProgressBus(2 * time.Second)
}

func isPhaseName(s string) bool {
	switch s {
	case "Decompose", "Cluster", "ReliabilityContext", "Generate", "Align", "Prove", "Deliver", "Queue":
		return true
	}
	return false
}
```

Add a small helper `func (u acp.Update) depthOrZero() int { if u.Depth < 0 { return 0 }; return u.Depth }` — or inline `u.Depth`. (Import `time` in activity.go.)

Wire into `internal/tui/conversation.go`:
- Add `activity activityModel` to the `Conversation` struct (after `msgs`, ~L128).
- Initialize it in `NewConversation`: `conv.activity = newActivityModel()`.
- In the `streamMsg` case, add an EARLY branch (before the `agent_message` accumulation) so activity never becomes a transcript bubble:

```go
case streamMsg:
	if t.u.Kind == acp.ActivityKind {
		m.activity.apply(t.u)
		m.render()
		return m, nil
	}
	// ... existing agent_message / other-kind handling unchanged ...
```

- In `turnDoneMsg`, compute the summary + clear:

```go
case turnDoneMsg:
	m.inFlight = false
	m.activity.finish()
	if t.err != nil {
		m.msgs = append(m.msgs, msg{role: "orion", text: "error: " + t.err.Error()})
	}
	m.render()
	return m, nil
```

- In `handleEnter` (where `m.inFlight = true`), call `m.activity.reset()` so each turn starts clean.

- [ ] **Step 4: Run test + build**

Run: `go test ./internal/tui/ -run TestActivityModelStackAndPhases -v && go build ./...`
Expected: PASS + clean build.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/activity.go internal/tui/activity_test.go internal/tui/conversation.go
git commit -m "feat(tui): activity model — actor stack, phase strip, log"
```

---

### Task 6: TUI liveness heartbeat

**Files:**
- Modify: `internal/tui/conversation.go` (a new `activityTickMsg` type; a tick cmd; kick it from `handleEnter`; handle it in `Update`)
- Test: `internal/tui/activity_test.go` (extend)

**Interfaces:**
- Consumes: `activityModel.bus` `Tick`/`HeartbeatDue` (Task 2/5).
- Produces: `type activityTickMsg time.Time`; `func activityTick() tea.Cmd`.

- [ ] **Step 1: Write the failing test**

```go
func TestActivityHeartbeatNudgesWhenSilent(t *testing.T) {
	a := newActivityModel()
	a.apply(acp.Activity("Orion", "prove", 0, "running"))
	before := len(a.bus.Events())
	// Simulate a heartbeat tick after the interval by forcing the bus to tick.
	a.bus.Tick(a.bus.nowForTest().Add(3 * time.Second)) // heartbeat due
	if len(a.bus.Events()) <= before {
		t.Fatalf("heartbeat did not append a liveness event")
	}
}
```

> Note: `ProgressBus` uses an internal `now func()`. If it has no test accessor, add a tiny unexported `nowForTest()` returning `b.now()` in `progress.go`, or construct the bus with a controllable clock in the test. Keep the change minimal.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/ -run TestActivityHeartbeat -v`
Expected: FAIL until the accessor/clock exists (the `bus.Tick` heartbeat path itself already exists from `progress.go`).

- [ ] **Step 3: Wire the tick command**

In `conversation.go`:

```go
type activityTickMsg time.Time

func activityTick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return activityTickMsg(t) })
}
```

- In `handleEnter`, add `activityTick()` to the `tea.Batch(...)` that already kicks `m.sp.Tick`.
- In `Update`, handle it (only while in-flight; re-arm):

```go
case activityTickMsg:
	if !m.inFlight {
		return m, nil
	}
	if m.activity.bus.Tick(time.Time(t)) {
		m.render() // a heartbeat was emitted → refresh the panel
	}
	return m, activityTick()
```

- [ ] **Step 4: Run test + build**

Run: `go test ./internal/tui/ -run TestActivityHeartbeat -v && go build ./...`
Expected: PASS + clean build.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/conversation.go internal/tui/progress.go internal/tui/activity_test.go
git commit -m "feat(tui): activity heartbeat — never appear hung"
```

---

### Task 7: Render the activity pane + idle summary + layout

**Files:**
- Modify: `internal/tui/conversation.go` (`View()` ~L810-878, specifically the status block ~L854-859 and the `parts` assembly ~L868-873; the height-budget helper ~L579)
- Create: `internal/tui/activity.go` — add `func (a activityModel) render(width int, inFlight bool) string`
- Test: `internal/tui/conversation_test.go` (extend, using `newTestConvo`/`feed`/`View`)

**Interfaces:**
- Consumes: `activityModel` (Task 5), `inFlight` (conversation).
- Produces: `func (a activityModel) render(width int, inFlight bool) string` — the bordered pane while in-flight, the one-line summary when idle (empty string if nothing to show).

- [ ] **Step 1: Write the failing test**

```go
// internal/tui/conversation_test.go
func TestActivityPaneShowsStackThenCollapses(t *testing.T) {
	m := newTestConvo(t)
	m = feed(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.inFlight = true
	m = feed(m, streamMsg{u: acp.Activity("Orion", "build_service", 0, "running")})
	m = feed(m, streamMsg{u: acp.Activity("research", "web_search", 1, "running")})

	if !strings.Contains(m.View(), "research") {
		t.Fatalf("in-flight activity pane should show the subagent actor:\n%s", m.View())
	}
	if got := lipgloss.Height(m.View()); got != 24 {
		t.Fatalf("layout not height-exact with activity pane: %d, want 24", got)
	}

	m = feed(m, turnDoneMsg{})
	if strings.Contains(m.View(), "web_search") {
		t.Fatalf("idle view must collapse the live pane:\n%s", m.View())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/ -run TestActivityPaneShowsStackThenCollapses -v`
Expected: FAIL — the pane is not rendered yet.

- [ ] **Step 3: Render + wire into layout**

Add to `internal/tui/activity.go`:

```go
// render returns the activity pane (in-flight) or the collapsed summary (idle).
func (a activityModel) render(width int, inFlight bool) string {
	if !inFlight {
		if a.summary == "" {
			return ""
		}
		return dimStyle.Render("  " + a.summary)
	}
	var b strings.Builder
	// actor stack as a tree
	for i, f := range a.stack {
		prefix := "  "
		if f.depth > 0 || i > 0 {
			prefix = "  " + strings.Repeat("  ", f.depth) + "↳ "
		}
		fmt.Fprintf(&b, "%s%s · %s\n", prefix, f.actor, f.activity)
	}
	// phase strip
	if len(a.phases) > 0 {
		var parts []string
		for _, p := range a.phases {
			parts = append(parts, p.name+" "+phaseGlyph(p.status))
		}
		fmt.Fprintf(&b, "  %s\n", strings.Join(parts, "  "))
	}
	// recent log
	for _, l := range a.log {
		fmt.Fprintf(&b, "  · %s\n", l)
	}
	body := strings.TrimRight(b.String(), "\n")
	// Reuse the existing pane border style; width-constrained like the other panes.
	return activityPane.Width(width).Render(body) // define activityPane near transPane/inputPane
}

func phaseGlyph(status string) string {
	switch status {
	case "done":
		return "✓"
	case "fail":
		return "✗"
	default:
		return "⠋"
	}
}
```

- Define `activityPane` next to the existing `transPane`/`inputPane` lipgloss styles (same border, dim title `activity`).
- In `View()`, build the pane and insert it into the layout, and account for its height so the transcript viewport reflows (the layout is height-exact):
  - Compute `act := m.activity.render(paneW, m.inFlight)`.
  - Before sizing `m.vp.Height`, subtract `lipgloss.Height(act)` (when non-empty) from the available height — mirror how the palette height is already subtracted (~L840).
  - In the `parts` assembly (~L868-873), insert `act` between `top` (transcript) and `bottom` (input) when non-empty:

```go
parts := []string{header, top}
if act != "" {
	parts = append(parts, act)
}
if palette != "" {
	parts = append(parts, palette)
}
parts = append(parts, bottom, hint)
```

  - Keep the existing in-flight status line (spinner + spend) — the pane augments it; the spinner stays the "is it alive" cue and the pane says "who/what".

- [ ] **Step 4: Run tests — new + the layout-exactness guards**

Run: `go test ./internal/tui/ -v` (runs the new test plus the existing `TestInputGrowsAndLayoutStaysExact` / empty-state / hint tests)
Expected: PASS — pane shows the actor stack in-flight, collapses idle, and `lipgloss.Height(View())==height` holds.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/activity.go internal/tui/conversation.go internal/tui/conversation_test.go
git commit -m "feat(tui): render live activity pane + idle summary"
```

---

## Self-Review

**Spec coverage:** actor identity on the wire (T1) ✓; ProgressBus actor + heartbeat (T2, T6) ✓; subagent visibility / load-bearing emit plumbing (T3) ✓; build phases live (T4) ✓; panel model incl. stack/phase/log + idle collapse (T5, T7) ✓; testing incl. the conductor "inner activity surfaces" assertion (T3) ✓. No spec requirement left unassigned.

**Placeholder scan:** none — every code step carries real code. The only deferred detail is the test stub `llm.Provider` in T3 (explicitly flagged to reuse the existing conductor-test stub) and the `ProgressBus` clock accessor in T6 (explicit minimal instruction). Both are concrete instructions, not vague TODOs.

**Type consistency:** `acp.Activity(actor, activity, depth, status)` and `acp.ActivityKind` used identically in T3/T4/T5/T7. `activityModel`/`actorFrame`/`phaseMark` names consistent across T5/T7. `EmitActivity(actor, activity, depth, status)` signature matches its T2 definition and T5 call. `emit func(acp.Update)` threads T3→T4 unchanged.

## Notes for the executor

- After each task, run `go build ./...` and `go vet ./<touched pkg>/`. The layout-exactness invariant (`lipgloss.Height(View())==height`) is the single most fragile thing — run the full `internal/tui` suite after T7.
- `emit == nil` no-op is load-bearing: grep every `specTools(` and `registerSubagentTool(` caller and pass `nil` where there is no stream.
- Do NOT alter existing `acp.Update` kinds or transcript-bubble behavior; the activity branch in `streamMsg` returns early precisely to avoid creating bubbles.
