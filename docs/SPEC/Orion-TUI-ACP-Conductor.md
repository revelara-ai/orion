---
title: Orion TUI, ACP & the Conductor-as-Agent
status: draft
authors: Joseph Bironas
created: 2026-06-18
derived_from:
  - https://agentclientprotocol.com/protocol/v1/overview (Agent Client Protocol, ACP)
  - Gastown mayor design (gt mayor; internal/mayor/manager.go, cleanup.go; roles/mayor.md.tmpl)
  - docs/PRD/orion-v2.md (tui, orchestrator/Conductor, a2a, cmd/orion)
---

# Orion TUI, ACP & the Conductor-as-Agent

> Two coupled decisions: (1) the **TUI is an Agent Client Protocol (ACP) client** and the **central point of orchestration**; (2) the **Conductor is itself a spawned, primed agent** — like Gastown's Mayor, the first agent in the run, not special-cased orchestration code — exposed to the TUI as an ACP *agent* and managed by a thin lifecycle manager.
>
> This fits the SRE-derived "decouple the reasoning engine from the execution engine" principle: the Conductor-agent is the **reasoning** plane; the deterministic proof harness, deployment bar, leases, dry-run, and integration gates are the **execution/safety** plane. The Conductor reasons and delegates; it never computes a proof verdict or overrides a deterministic gate.

## 0. Positioning: agentic chat, control plane, pluggable brain

**Lineage.** Orion's *interface* is an **agentic coding chat** in the lineage of **Claude Code / Pi / Hermes** — one developer ⇄ one agent that reasons, calls tools, and acts in a conversational loop. Its *mechanics* (worktree isolation, deterministic gates, spawning/driving vendor agents over ACP) are borrowed from **Gastown's control plane**. Its *differentiator* is the opinionated, **proof-gated** reliability workflow. The Gastown comparison ends at the interface: Orion is **chat-first**, not a fleet dashboard.

**Control plane, not LLM client.** Orion holds no API key and makes no inference calls. The chat "brain" is the developer's **own vendor coding-agent CLI**, spawned as a subprocess and driven over ACP:
- **Default:** Claude Code (native ACP), using the user's existing **Max/Pro login** (`CLAUDE_CONFIG_DIR` / OAuth keychain).
- **Pluggable:** any ACP-compliant agent (e.g. Gemini `--acp`) selectable via an **agent preset**.
- **Optional overrides:** a preset may set `ANTHROPIC_BASE_URL` / `ANTHROPIC_API_KEY` (e.g. route to Groq) — Orion only sets the agent's process env; it never makes the call.
- **Auth answer:** "accept an API key *or* use Max/Pro accounts" is satisfied entirely by the spawned agent's own auth + optional preset env. No Orion-side LLM SDK.

**Agent presets + spawn (the foundation).** An agent-preset registry declares, per vendor agent: launch command, injected env, process detection, and ACP mode (`native` / `--acp` flag / `subcommand`). `agent-runtime` spawns the process (Conductor or specialist), injects env + the role template, and the TUI/Conductor drive it over ACP/A2A. Maps Gastown `config/agents.go` + `agent/provider`, **minus** the MITM proxy (denylist/cert issuance) and the quota/account-rotation subsystems — both deferred control-plane features, out of scope for V2.

**Why this is the right call:** it leverages the intelligence *and* auth the developer already has, keeps Orion model-agnostic by construction (the commodity-model principle), and concentrates Orion's own code on the differentiated part — the proof-gated workflow + deterministic harness. The chat is the vehicle; the proof loop is the moat.

## 1. What changes vs. the current PRD

- The `tui` module becomes a concrete **ACP client** (bubbletea front end implementing the client half of ACP), not just "talks to the Conductor over an internal event channel."
- The `orchestrator` (Conductor) becomes a **spawned, primed ACP agent + a thin lifecycle manager**, not a monolithic "deep module." Its orchestration *personality* lives in a **role template**, not hardcoded Go. (This also resolves the round-2 "Conductor god-object" finding: reasoning moves into the primed agent; the deterministic responsibilities — `truth-align`/Converge, deployment bar, drift thresholds, leases — are separate tools the Conductor invokes but cannot influence.)
- A clean protocol split: **ACP = client↔agent (vertical, UI/control)**; **A2A = agent↔agent (horizontal, delegation)**.

## 2. ACP in one paragraph

ACP is JSON-RPC 2.0 (Methods + Notifications), agent typically a subprocess over stdio. The **client** (editor/TUI) provides the UI and environment access; the **agent** does the work. Agent-exposed: `initialize` (version/capability negotiation), `authenticate` (optional), `session/new` | `session/load`, `session/prompt`, `session/cancel`. Client-exposed: `fs/read_text_file`, `fs/write_text_file`, `terminal/*`, `session/request_permission`. The agent streams `session/update` notifications: message chunks (agent/user/thought), tool calls + updates, plans, available commands. Session lifecycle: initialize → (auth) → new/load → prompt-turn loop (prompt → streaming updates → optional cancel). Extensible via `_meta` fields and `_`-prefixed methods; all paths absolute, line numbers 1-based.

## 3. Orion's mapping

### TUI = ACP client (central orchestration surface)
The bubbletea TUI implements the ACP **client** side and is the developer's single seat:
- Sends `session/new` then `session/prompt` (the intent, then answers to the completeness gate).
- Consumes `session/update` streams → renders into panes: agent/thought chunks → **Conversation**; plans → **Plan**; tool calls + updates → **Fleet** / **Proof**; available commands → command palette.
- Serves `fs/*` and `terminal/*` **under Orion's safety controls** — every agent file write / command runs through the sandbox + reversibility (dry-run) gates; ACP file/terminal access is not a bypass.
- Handles `session/request_permission` → Orion's **approval & escalation gates**: spec ratification, destructive-op approval, HITL decisions. The human authorizes through the TUI client.
- `session/cancel` → the **interrupt / circuit-breaker / Red Button** path.

Because the TUI is a *standard* ACP client, two interop wins follow: Orion's loop can be driven from **any** ACP client (e.g., Zed), and the Conductor (or specialists) can be **any** ACP-compliant agent — consistent with the Pi-style composability principle.

### Conductor = a spawned, primed ACP agent (the "first agent")
Like the Mayor, the Conductor is not special-cased code; it is the first agent spawned, **primed by a role template** (intent intake, completeness gate, decomposition, dispatch, drift/re-anchor, escalation) and exposed to the TUI as an ACP agent. What it owns vs. doesn't:
- **Owns (reasoning/coordination):** the prompt-turn conversation with the developer, running the completeness gate, deciding decomposition, dispatching specialists, raising `request_permission` for approvals, surfacing plans/progress via `session/update`.
- **Does NOT own (delegated to deterministic tools, by invariant):** computing proof verdicts (`proof`/`truth-align`/Converge), the deployment-bar decision (`delivery`), lease/merge state (`integration`/`worktree`), dry-run/reversibility enforcement (`sandbox`). The Conductor agent *invokes* these and acts on their results; it cannot override a deterministic FAIL.

### Conductor lifecycle manager (thin)
A small manager (`cmd/orion conductor …`: `start`, `stop`, `attach`, `status`, `restart`, `acp`) — mapping `gt mayor` — that:
- Renders the role template and spawns the Conductor agent over a transport.
- Keeps a **pid / agent file** and a **cleanup veto** that blocks workspace/worktree teardown while the control process is alive (maps Gastown `cleanup.go`).
- Reports status and supports attach/observe.

### Transports
- **ACP (primary):** JSON-RPC over stdio; the managed agent-control transport. The TUI is the client; the Conductor is the agent subprocess.
- **(Optional, later) process/tmux attach:** an observe/attach transport for debugging a running Conductor, analogous to Gastown's tmux option. Not required for V2.0.

### Conductor as agent manager (the fleet)
The Conductor delegates to specialist agents (generator, instrumentor, resolver, `rvl:*` detectors, and the harness-side proof agents) — mapping Mayor→deacon/polecats. **Inter-agent delegation is A2A** (`ProofObligation` out, `EvidenceClaim` back). When the Conductor needs to *drive* a sub-agent as a client (e.g., a heterogeneous ACP coding agent), it may itself act as an ACP client to that sub-agent — but proof of the sub-agent's output remains deterministic and independent.

## 4. ACP method → Orion concept

| ACP | Direction | Orion mapping |
|---|---|---|
| `initialize` | client→agent | TUI ↔ Conductor capability negotiation (which gates/tools, tier) |
| `session/new` / `session/load` | client→agent | start a project run / resume (ties to Context Store + Recall) |
| `session/prompt` | client→agent | developer intent; answers to the completeness gate |
| `session/update` (agent/thought) | agent→client | Conversation pane; decision-log/transcript |
| `session/update` (plan) | agent→client | Plan (Epic/Tasks) pane |
| `session/update` (tool call/update) | agent→client | Fleet status + Proof panes; live progress (TUI liveness contract) |
| `session/request_permission` | agent→client | approval/escalation gates: spec ratify, destructive-op approval, HITL |
| `fs/read_text_file`, `fs/write_text_file` | agent→client | sandboxed file access (scoped to the task worktree; never the held-out corpus) |
| `terminal/*` | agent→client | sandboxed command execution (dry-run + reversibility gates apply) |
| `session/cancel` | client→agent | interrupt / circuit breaker / Red Button |
| `_meta` / `_`-prefixed | both | Orion extensions (ProofObligation refs, tier, budget, provenance) |

## 5. Trust-domain reconciliation (why an LLM Conductor is safe)

Making the Conductor an LLM agent does **not** weaken the manifesto, because:
- Proof is computed by **deterministic tools** the Conductor cannot influence (proof-mechanism hierarchy). An ACP `request_permission` grants a *human* authorization; it never substitutes for proof.
- The deterministic gates (proof, deployment bar, leases, dry-run, integration) are **caller-agnostic** — "they do not care if the caller is an agent or a human" — so a Conductor agent gets no special power over them.
- The Conductor agent runs with least-privilege, signed/pinned role definition, per-step deadline, and is interruptible (`session/cancel` → circuit breaker / Red Button).
- The generation⊥proof wall is unchanged: the Conductor coordinates generation; the proof domain (harness-side test-synthesis, empirical probes, hazard) is separate and deterministic.

## 6. Gastown Mayor → Orion Conductor

| Gastown | Orion |
|---|---|
| Mayor = first spawned, primed agent (not special code) | Conductor = first spawned, primed ACP agent |
| `roles/mayor.md.tmpl` personality | Conductor role template (intake/completeness/decompose/dispatch/escalate) |
| `gt mayor start/stop/attach/status/restart/acp` | `orion conductor start/stop/attach/status/restart/acp` |
| transports: tmux **or** ACP | transports: ACP (primary) + optional process/tmux attach |
| `mayor/manager.go` lifecycle | thin Conductor lifecycle manager |
| `cleanup.go` pid/agent file + cleanup veto | pid/agent file + worktree/teardown cleanup veto |
| delegates to deacon / polecats | delegates to specialist agents (A2A) |
| — (Gastown ACP = "Agent Control Protocol" transport) | Orion ACP = **Agent Client Protocol** (the open client↔agent standard) — TUI is a conformant client |

## 6a. Coordination & recovery (from Gastown, simplified)

Gastown's orchestration patterns, kept where they earn their place and folded into existing Orion modules (we deliberately do **not** adopt the Mayor/Deacon/Refinery/Polecat/rig hierarchy as distinct long-lived services):

- **Priming / propulsion.** The Conductor and each specialist *prime* on spawn — resolve identity, load the role template, and pull their hooked task + context (`Recall`) from the Context Store — then run autonomously. No per-step human "go". The Conductor's hook scopes it to orchestration; a specialist's hook is one task.
- **Pull-based coordination.** The Conductor dispatches but does not micromanage; workers pull task context from the Context Store (the source of truth). Coordination flows through the ledger, not push messages.
- **Dispatch lock + idempotency.** Dispatch takes a per-task lock and is idempotent (normalize agent id, match an existing target) so a task is never double-assigned — complements path leases. *(Maps Gastown `sling` lock + idempotency.)*
- **Capacity-bounded scheduling.** Bounded concurrent specialists via admission reservations (the `agent-runtime` worker pool / concurrency caps), with blocker-aware ready-task selection and a retry policy. *(Maps Gastown scheduler/capacity.)*
- **Recovery / redispatch (Deacon-lite).** A lightweight watcher re-dispatches stranded tasks (hung/timed-out agent, failed integration) after a cooldown, with **model escalation** (retry on a more capable model before giving up) and finally **escalation to the Conductor/human**. Bounded by the iteration budget + degradation guard. → folds into the Conductor + circuit breaker; `integration` is the Refinery-equivalent merge queue; `agent-runtime` is the polecat-equivalent worker lifecycle.

## 7. Phasing

| Capability | Phase |
|---|---|
| TUI as ACP client; Conductor as primed ACP agent + lifecycle manager (`start/stop/status/attach/restart/acp`); `request_permission` → approval gates; sandboxed `fs/*` + `terminal/*` | **V2.0** (amends `tui`, `orchestrator`, `cmd/orion`, `t-boot`) |
| A2A delegation to the specialist fleet | V2.0 |
| Heterogeneous ACP agents (swap Conductor/specialists; drive from external ACP clients like Zed) | V2.1+ |
| Optional process/tmux attach transport | V2.1+ |

> **Beads impact:** `t-boot` (orion binary + TUI + Conductor skeleton) and `t-a2a` should be updated so the TUI↔Conductor seam is ACP (not an ad-hoc internal channel) and the Conductor is a primed agent + lifecycle manager. The role template is a new build artifact.
