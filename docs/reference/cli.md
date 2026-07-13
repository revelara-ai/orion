# CLI reference

`orion` with no arguments launches the interactive TUI (the Conversation with
the Conductor). Everything below is the non-interactive surface. An unknown
command exits 2 with a `not implemented` marker.

`orion --version` · `orion help`

## Spec flow

| Command | What it does |
|---|---|
| `orion init` | Initialize the data dir + Context Store (also implicit on first use). |
| `orion submit [--non-interactive]` | Submit a build intent; `--non-interactive` reads stdin and emits JSON with the open decisions. |
| `orion answer <key> <value>` | Answer a blocking spec decision (e.g. `response_format json`). |
| `orion spec show\|approve` | Preview the spec + assumptions; ratify it (hash-anchored — the contract everything downstream re-grounds to). |
| `orion plan show [--json]` | The decomposed Epic/Task plan for the accepted spec. |
| `orion plan cutover` | The shadow→live ModuleProposer cutover evidence (measured-window criterion). |

## Build, prove, deliver

| Command | What it does |
|---|---|
| `orion run` | Execute the full pipeline: decompose → generate (sandboxed) → 3-mode proof → deployment bar → deliver/escalate. |
| `orion proof show [--task <id>] [--mode <m>] [--json]` | Proof reports (behavioral / empirical / hazard / converged). |
| `orion deliver show [--json]` | The delivery record: operating envelope, runbook, escalation reason if not delivered. |
| `orion baseline` | Capture/inspect the brownfield baseline (toolchain detection + green-before). |
| `orion change [--cases <file.json>] [--] "<intent>"` | Brownfield change-proof loop: worktree → generate → regression gate → new-behavior proof → review-branch commit + PR artifact. |
| `orion map` | The brownfield repo map (packages, entrypoints, test surface). |
| `orion deps verify` | Dependency provenance checks (exists / age / popularity — anti-slopsquat). |

## Operations

| Command | What it does |
|---|---|
| `orion conductor start\|stop\|status\|restart\|attach\|acp` | The daemonized conductor lifecycle; `acp` serves the agent over stdio. |
| `orion status` | One-screen install + pipeline + subsystem status. |
| `orion doctor [--json]` | Health checks; `fail` flips the exit code, `warn` is advisory (e.g. revelara.ai not logged in). |
| `orion queue` | The intent queue (single-active-project invariant). |
| `orion escalations list\|resolve <id>` | The unified human inbox: proof failures, alignment reviews, realignment events. |
| `orion redbutton engage\|release\|status` | Cross-process kill switch: blocks mutating actions and revokes autonomy. |
| `orion tracker project` | One-way projection of tasks into an external tracker (beads backend). |

## Agents, skills, memory

| Command | What it does |
|---|---|
| `orion agents` | Registered vendor-agent presets (claude / gemini / codex over ACP). |
| `orion skills` | The skill registry (trust-tiered; proof-tier skills are immutable at runtime). |
| `orion evolve` | Manually promote validated memory candidates into generation-tier skills (default-off self-evolution). |
| `orion design show` / `orion design ratify <hash>` | Review and ratify the drafted design-proof formal model (or-56c.2); only the human signature gives it proof authority. |
| `orion login\|logout` | revelara.ai OAuth (WorkOS); the token lives in `credentials/`, never in the Context Store. |
