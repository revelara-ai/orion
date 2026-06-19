This is graph-backed now. Here's the end-to-end flow, traced through actual nodes and edges. One framing note up front: "the Mayor" is two things — a gt command group that manages the Mayor's session, and an agent (a Claude Code instance) that, once running, drives the system by issuing more gt commands. I'll cover both.

1. The binary entry point
cmd/gt/main.go is a thin shell — its main just delegates to cmd.Execute and exits with the status code. All real wiring lives in internal/cmd/root.go, which defines the root gt cobra command and its persistent pre-run hooks (theme init, heartbeat touch, stale-binary / off-main warnings) plus the top-level Execute entry. Every gt <subcommand> flows through here.

Layer-wise: main.go is the lone Entry Points node; the ~423 subcommand files (mayor, sling, prime, …) form the CLI Command Surface layer.

2. Bringing the Mayor up — gt mayor
internal/cmd/mayor.go implements the gt mayor suite (start, stop, attach, status, restart, acp). It imports and delegates to internal/mayor/manager.go — the core orchestrator that starts/stops/reports the Mayor agent session over one of two transports:

tmux — the Mayor runs as a Claude Code process in a tmux session, or
ACP (Agent Control Protocol) — a managed agent-control-process transport.
manager.go's edges show it pulls in internal/templates/templates.go to render the Mayor's role context, internal/workspace/find.go to locate the town root, and internal/config/agents.go for agent config. The Mayor's actual "personality" is the role template internal/templates/roles/mayor.md.tmpl — epic oversight, coordination, and delegation to deacon and polecats. Session teardown is guarded by internal/mayor/cleanup.go, which keeps an ACP pid/agent file and a cleanup veto checker that blocks workspace cleanup while the control process is alive.

So "the Mayor" is itself a spawned, primed agent — not special-cased code. It's the first agent in the town.

3. Priming — how any agent (incl. the Mayor) wakes up with work
When a session starts, internal/cmd/prime.go (gt prime) bootstraps it: resolves identity, primes role context, injects work from the hook, handles hook mode, and emits the autonomous-work banner. Its imports tell the story — beads/agent_ids.go (who am I in the ledger), state/state.go, worktree/integrity.go, workspace/find.go, lock/flock_unix.go.

This is the "propulsion principle" in code: an agent primes, finds work on its hook, and runs it — no human "go". The Mayor primes the same way; its hook/role just scopes it to orchestration rather than a single bead.

4. Delegation — the Mayor issues gt sling
This is the heart of "orchestrator of subagents." The Mayor (as an agent) delegates work by running gt sling, whose engine is executeSling in internal/cmd/sling_dispatch.go — "orchestrates bead locking, validation, and agent dispatch via SlingParams/SlingResult." The sling subsystem is large and modular:

Locking — sling_dispatch_lock acquires a per-bead dispatch lock so two slings can't double-assign.
Guards — closed/tombstone-bead guard, sling_parked_rig refusal, sling_idempotency.go (normalize agent IDs, match an existing target to avoid duplicate hooks).
Targeting — sling_target.go resolves which agent/address the work goes to.
Batch / convoy — sling_batch.go dispatches many beads as a tracked convoy; sling_convoy.go tracks membership.
Formulas — sling_formula.go + sling_helpers.go instantiate/"cook" formula workflows onto a bead.
5. Capacity & scheduling — not everything dispatches immediately
Concurrency is bounded. internal/cmd/polecat_capacity.go enforces a scheduler-configured cap on concurrent polecats via lock-protected admission reservations (with stale-reservation cleanup), and internal/cmd/capacity_dispatch.go does capacity-aware scheduled dispatch. The pure logic lives in the scheduler package:

scheduler/capacity/pipeline.go — selects which pending beads to dispatch (blocker-aware, capacity/batch planning, retry policy).
scheduler/capacity/dispatch.go — the DispatchCycle that executes a planned batch.
scheduler/capacity/state.go — persists pause/resume + last-dispatch timestamps.
Epic/convoy expansion (one parent bead → many children) is done by scheduler_epic.go and scheduler_convoy.go.

6. Spawning the worker — the Polecat
When sling actually dispatches, it materializes a polecat (the ephemeral worker subagent). Two Agent Orchestration-layer files do this:

internal/polecat/manager.go — allocates, adds, removes, repairs, and reconciles polecat worktree slots, persisting agent state to the Dolt-backed Beads ledger. (This is the worktree machinery from your earlier question — each polecat is a git worktree off the shared bare repo, with an atomically-assigned hook_bead.)
internal/polecat/session_manager.go — starts/attaches the polecat's tmux session, injects agent env, and sets it running.
The new polecat then runs gt prime (step 3), finds its hooked bead, and works autonomously. The Mayor never micromanages it — coordination is pull-based through Beads, not push.

7. Closing the loop — Refinery and Deacon
When a polecat finishes, its branch/MR flows to the Refinery: internal/refinery/engineer.go runs the merge queue (phase gates, merges/PRs, ready/blocked MR listing) and internal/refinery/manager.go controls that worker session + post-merge cleanup.
The Deacon watches for stuck work: internal/deacon/redispatch.go re-slings recovered/stranded beads with cooldowns and model-escalation rules, and escalates to the Mayor when it can't make progress — closing the orchestration loop back to where you started.
The flow in one line

gt (main) → cmd.Execute (root.go)
   ├─ gt mayor → mayor/manager.go  ── starts the Mayor agent (tmux/ACP), primed via mayor.md.tmpl
   └─ Mayor agent runs: gt sling → executeSling (sling_dispatch.go)
        → lock + guard + idempotency + target
        → scheduler/capacity (admission cap, batch planning)
        → polecat/manager.go (allocate worktree slot, write hook_bead → Beads)
        → polecat/session_manager.go (start tmux session)
            → polecat runs gt prime → reads hook → works autonomously (GUPP)
                → gt done → Refinery merge queue (engineer.go)
                → Deacon redispatch.go re-slings/escalates to Mayor on failure
Layers traversed: Entry Points → CLI Command Surface (mayor, prime, sling, scheduler) → Agent Orchestration (polecat, refinery, mayor, deacon) → Work Ledger & State (beads, Dolt).

One graph caveat worth flagging: a few import edges on these files resolve to a *_test.go node (e.g. mayor.go/prime.go → tmux/cross_socket_test.go, mayor/manager.go → acp/forward_from_agent_test.go). That's an artifact of how the scanner picked a representative file for a Go intra-package import — the real dependency is the tmux and acp packages, not those specific test files. Functionally: the Mayor and polecat sessions are driven through the tmux package, and the Mayor's alternate transport is the acp package.