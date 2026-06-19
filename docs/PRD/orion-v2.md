---
title: Orion V2 — The Reliability Layer of the Agentic SDLC
status: draft
authors: Joseph Bironas
created: 2026-06-17
last_updated: 2026-06-17
supersedes:
  - docs/archive/PRD/orion-v1.md
  - docs/archive/SPEC/Orion-SPEC.md (+ drafts 1-3)
derived_from:
  - docs/MANIFESTO.md
  - docs/research/Harness-reliability-research-2026-06-16.md
references:
  - docs/PRD/A2A-Protocol-Spec.md
  - docs/PRD/Lookout-Agent-Spec.md
  - docs/PRD/Orchestrator-Logic-Spec.md
  - docs/PRD/Task-Decomposer-Spec.md
  - docs/PRD/Verification-Engine-Spec.md
  - docs/TDS/Orchestrator-Decision-Matrix.md
---

# Orion V2 PRD: The Reliability Layer of the Agentic SDLC

> This PRD is the product-level realization of [the Orion Manifesto](../MANIFESTO.md). Every mechanism here traces to a manifesto goal and a documented failure mode. Where the manifesto states a belief, this PRD states what gets built, how data flows, and how we prove it works.

## Problem Statement

A developer wants to build reliable software with agents. Today the only options are:

- **Single-agent assistants** (Copilot, Cursor): fast at producing code, but they game their own verifiers, drift from intent over long runs, emit uninstrumented code, and leave the developer holding an artifact nobody can operate at 3 a.m.
- **Multi-agent harnesses** (Gastown, Hermes, OpenClaw, Pi): good at coordinating fleets of agents, but they inherit and *amplify* the reliability failure modes — error compounding across steps, context/memory decay, non-reversible side effects, "green build is lying" verification — because the orchestrator trusts its own subagents' checkmarks.

In both cases the loop optimizes for a **local signal** (a passing test, an agent's confidence) while the developer's **true intent** drifts, decays, or goes unmeasured. The cost is not paid at write time; it is paid at comprehension time, during an incident, when there is no author to page and no proof that the code does what was meant.

There is no harness whose first-class obsession is **reliability** — one that makes intent complete before building, proves correctness by independent means before calling the loop done, produces operable software, calibrates rigor to the project's real risk, and gets smarter from every failure it sees. That is the gap Orion V2 fills.

## Solution

**Orion V2 is an agentic coding chat — in the lineage of Claude Code, Pi, and Hermes — driven by an opinionated, proof-gated workflow** (Go; cobra + bubbletea). The developer converses with the **Conductor** in a chat-first TUI; the Conductor takes the intent (a bare idea, a design doc, or an existing backlog), makes it unambiguous, decomposes it, and drives the build to **proven, operable software** — treating output as done only when its correctness is independently proven, never merely asserted.

**Orion is a control plane for agents, not an LLM client.** It holds no API key and makes no inference calls. It **spawns and drives the developer's own coding agent** — Claude Code by default, or any ACP-compliant agent — over the **Agent Client Protocol (ACP)**, using *that agent's* existing authentication (e.g. a Claude Max/Pro login; optional per-preset `ANTHROPIC_BASE_URL`/key override). The chat "brain" is therefore pluggable; Orion's contribution is the layer around it. Behind the chat, Orion solves the hard problems the manifesto names — efficient agent coordination, context/memory management, durable task tracking, and above all the deterministic **multi-modal proof harness** that gates completion.

Lineage, explicitly: from **Pi / Hermes / Claude Code**, the chat-first agentic UX and composable skills; from **Gastown's control plane**, worktree isolation, deterministic gates, and spawning/driving vendor agents over ACP; **Orion's own contribution** is the opinionated, proof-gated reliability workflow that turns a generic coding agent into one that ships *proven* software.

The shape of the loop:

```
 ┌────────────────────────── TUI (developer ⇄ Conductor) ──────────────────────────┐
 │                                                                                 │
 │   intent ──▶ [Intent Completeness Gate] ──▶ executable spec                     │
 │                   │ (elicit from human when ambiguous)                          │
 │                   ▼                                                             │
 │            [Task Decomposer] ──▶ task DAG ──▶ Project Context Store             │
 │                   │                                                             │
 │                   ▼                                                             │
 │            [Conductor dispatch] ──A2A──▶ specialist agents (sandboxed)          │
 │                   │                       (generate · test · instrument)        │
 │                   ▼                                                             │
 │            [Proof Harness]  behavioral + empirical + hazard  (independent)      │
 │                   │            no agent grades its own homework                 │
 │                   ▼                                                             │
 │            [Deployment Bar] ── proof passes? ──▶ deliver (autonomy by tier)     │
 │                   │                          └─▶ fallback: human-mergeable      │
 │                   ▼                                                             │
 │            [Polaris loop] consume controls/KB/risks · contribute failure modes  │
 │                                                                                 │
 └─────────────────────────────────────────────────────────────────────────────────┘
```

**Runtime:** everything in the loop runs locally on the developer's machine — Conductor, specialist agents, sandbox, proof harness, and the Project Context Store. The only cloud dependency is **Polaris**, which supplies reliability knowledge (controls catalog, knowledge base, risk register) and receives the failure modes Orion encounters.

**The completion criterion is proof, not assertion.** A task is "done" only when independent, non-agentic verification converges across three modes — behavioral (tests, mutation-scored), empirical (Lookout-style shell probes of the running artifact), and hazard (STPA-derived: the unsafe control actions are controlled). Proof is the right to ship: when the bar is met at the project's reliability tier, Orion delivers autonomously; when it cannot be met, Orion falls back to a proven, human-mergeable change.

**Why this is the durable bet (commodity models).** Orion's reliability comes from the loop, not the model — the same way microservice architecture delivered reliable systems on cheap, unreliable commodity hardware (because hardening the hardware itself was prohibitively expensive). Orion treats the generation model as a fallible, swappable component and puts the guarantees in independent proof, bounded steps, and embedded reliability knowledge. V2 is therefore **model-agnostic by construction** — frontier models today because they are the best components available, commodity models as they mature — and Orion's value *increases* as generation commoditizes. No requirement in this PRD may depend on a specific model's behavior; anything that does is a design defect.

**This PRD defines the full V2 product and phases delivery.** The first buildable slice — **V2.0** — proves the entire loop on one path: *a developer states an idea in the TUI and receives a proven, instrumented, runnable Go service.* Polyglot (Go/TS/Python) and brownfield intake are full-V2 scope, phased after the tracer bullet.

### Phasing

| Phase | Theme | Scope |
|---|---|---|
| **V2.0** | Tracer bullet | Go **greenfield**: TUI → Conductor → completeness gate → decompose → 1–2 Go specialist agents → multi-modal proof → proven runnable service + runbook. Context Store with beads-backed default. Polaris consume-only. Delivery = local proven artifact (human-mergeable; no auto-deploy yet). |
| **V2.1** | Brownfield + tracker projection | Existing Go repo intake; task tracker projection (beads ⇄ GitHub Issues); human-mergeable PR delivery; drift detection + re-anchoring hardened. |
| **V2.2** | Polyglot | TypeScript and Python generators + proof harnesses; per-language hazard libraries. |
| **V2.3** | Earned autonomy + learning write-back | Reliability-tier-gated autonomous delivery for low-risk classes; Polaris failure-mode write-back loop. |

## User Stories

### Developer — intent and the TUI

1. As a developer, I want to describe what I want to build in plain language in a terminal UI, so that I can start without writing a formal spec myself.
2. As a developer, I want Orion to tell me *exactly which decisions are underspecified* and ask me about them, so that I resolve ambiguity up front instead of discovering it in a rebuild.
3. As a developer, I want to see the executable spec Orion derived from our conversation and approve it, so that I know what contract the build is held to.
4. As a developer, I want to converse with one orchestrator (the Conductor) rather than manage individual agents, so that coordination complexity stays hidden behind a simple interface.
5. As a developer, I want to see, at a glance in the TUI, what the fleet is doing right now — which agents are active, on which tasks, with what status — so that I have situational awareness without micromanaging.
6. As a developer, I want to interrupt the Conductor at any time to change direction, so that I stay in control of the work.
7. As a developer, I want the TUI to surface when Orion is blocked on a decision only I can make, so that I am pulled in exactly when needed and not before.
8. As a developer, I want a readable transcript of decisions the Conductor and agents made, so that I can audit the trajectory after the fact.

### Developer — intake modes

9. As a developer with only an idea, I want Orion to scaffold a greenfield project from intent, so that I get a reliable starting point instead of a blank repo.
10. As a developer with an existing codebase, I want Orion to ingest it and build context, so that new work respects what's already there. *(V2.1)*
11. As a developer with a backlog, I want Orion to pull work items from my tracker and drive them, so that I don't have to re-describe each task. *(V2.1)*
12. As a developer, I want to choose the starting point — idea, design doc, or backlog — so that Orion adapts to where I actually am.

### Developer — reliability and proof

13. As a developer, I want Orion to refuse to call a task "done" until correctness is independently proven, so that "the agent says it works" is never the verdict.
14. As a developer, I want test quality measured by whether tests actually catch faults (mutation score), not by coverage percentage, so that a green build is not a lie.
15. As a developer, I want Orion to verify the *running* artifact (ports open, files present, real requests succeed), so that proof reflects reality, not the agent's report of reality.
16. As a developer, I want Orion to identify the unsafe things a change could cause and prove they're controlled, so that hazards are caught before they ship.
17. As a developer, I want every component delivered with structured logs, traces, metrics, and a runbook, so that I can operate it at 3 a.m. without paging the author who doesn't exist.
18. As a developer, I want Orion to verify that every dependency it adds actually exists and resolves to the real artifact, so that I'm not exposed to slopsquatting.
19. As a developer, I want Orion to set the rigor to my project's real risk via a reliability tier, so that a throwaway tool isn't over-engineered and a payments path isn't under-protected.
20. As a developer, I want Orion to detect when a refinement pass made things worse and stop, so that "keep prompting until it works" doesn't silently degrade the artifact.

### Developer — delivery and autonomy

21. As a developer, I want Orion to deliver a proven, runnable artifact when the proof bar is met, so that I get working software, not a draft.
22. As a developer, I want Orion to fall back to a clearly-marked human-mergeable change when the proof bar can't be met, so that I make the judgment call exactly when proof is insufficient. *(V2.1)*
23. As a developer on a low-risk project, I want Orion to deliver autonomously once proof passes at my tier, so that I'm not the bottleneck on changes that are provably safe. *(V2.3)*
24. As a developer, I want any destructive action (DB writes, infra changes, external calls) sandboxed and gated on a defined rollback path, so that a wrong agent action can't execute faster than I can intervene.

### Operator / SRE

25. As an SRE, I want the software Orion produces to carry the reliability primitives I'd add by hand (timeouts, retries with backoff, idempotency, bounded resources), so that reliability is built in, not bolted on.
26. As an SRE, I want stated scaling and concurrency assumptions in the deliverable, so that "works on localhost" failures are surfaced before load, not after.
27. As an SRE, I want a runbook generated for each component, so that the documented-failure-class coverage doesn't lag feature velocity.

### Platform / learning loop

28. As a developer, I want Orion to reason with Polaris's controls catalog and risk register on every task, so that reliability context is applied to feature work too — not reserved for "reliability work."
29. As a platform owner, I want Orion to contribute the failure modes it encounters back to Polaris, so that every run makes the platform — and every other developer — smarter. *(V2.3)*
30. As a developer, I want Orion to never silently repeat a failure mode that Polaris already knows about, so that learned knowledge actually guards future work.

### Coordination internals (made visible)

31. As a developer, I want any agent to be able to rebuild full context for a task from a durable store, so that a crashed or restarted agent resumes without losing the thread.
32. As a developer, I want the original intent re-anchored periodically during long runs, so that the cumulative path doesn't drift away from what I asked for.
33. As a developer, I want Orion to bound how much context each agent step carries, so that error compounding and context decay are contained by design.
34. As a developer, I want Orion to treat injected or stale instructions as a threat, so that memory poisoning can't quietly corrupt intent across the run.
35. As a developer, I want per-step confidence tracked and a circuit breaker that escalates to me when confidence degrades, so that the loop stops compounding errors instead of grinding on.

### Developer — reliability touchpoint (Orion subsumes rvl-cli)

36. As a developer, I want Orion to be my single reliability touchpoint, so that I don't run a separate CLI — Orion does everything rvl-cli does and drives the build loop too.
37. As a developer, I want to authenticate to Revelara/Polaris from Orion (`login`/`logout`/`status`), so that one tool holds my platform connection.
38. As a developer, I want to run a reliability scan from Orion that detects risks and saves them to my register, so that scanning is part of the same loop that fixes them (the rvl multi-agent scan, run by Orion's specialist fleet).
39. As a developer, I want Orion to read and write risks natively to Polaris (list/show/resolve, and open new risks it finds), so that the risk register stays current without a second tool.
40. As a developer, I want Orion to submit evidence to Polaris when a control is implemented and verified by proof, so that "proven" in Orion becomes "evidenced" in Polaris automatically.
41. As a developer, I want Orion to query the 61-control catalog and search the org knowledge base (facts, patterns, procedures) during the loop, so that reliability context is applied as work happens.
42. As a developer, I want Orion to contribute knowledge and failure modes back to Polaris (with my confirmation), so that what one run learns, the org keeps.

## Data Flow Traces

> Runtime is local; the **Project Context Store** is the durable source of truth (V2.0 default backing: an embedded store — see Implementation Decisions). "Module" references are target package boundaries, not committed file paths.

### Trace 1: Intent → executable spec (the completeness gate)

1. Developer types an intent in the TUI input pane → `tui` (input view).
2. TUI forwards the raw intent to the Conductor as an `Intent` message → `orchestrator` (Conductor).
3. Conductor runs **completeness analysis**: classifies the intent against a required-decisions checklist for the project type, producing a list of `OpenDecision`s → `orchestrator/completeness`.
4. If `OpenDecision`s remain, Conductor emits clarifying questions back to the TUI; developer answers; answers are recorded as `Decision` rows → `context-store` (`decisions`).
5. Loop until zero open decisions, then Conductor compiles the conversation + decisions into an **executable spec** (structured, testable; the immutable contract) → `orchestrator/spec`, persisted to `context-store` (`spec` for the project/work-item).
6. TUI renders the spec for developer approval; approval flips spec status to `accepted` → `context-store` (`spec.status`).

*Missing-implementation flags:* the required-decisions checklist per project type, and the spec schema, are V2.0 build tasks.

### Trace 2: Spec → task DAG → dispatch

1. Accepted spec → **Task Decomposer** (reuses [Task-Decomposer-Spec](../archive/PRD/Task-Decomposer-Spec.md)) → `decomposer`.
2. Decomposer queries the Context Store for relevant prior context (existing code map, decision log, Polaris context) → `context-store` recall API.
3. Decomposer emits an **Epic** whose `Task` nodes form a dependency DAG, each carrying its own `ProofObligation` (what proof this task owes) → persisted as `context-store` (`epics`, `tasks`, `task_deps`).
4. TUI renders the DAG so the developer can review the plan before execution → `tui` (plan view).
5. Conductor selects ready tasks (no unmet dependencies) and dispatches each to a specialist agent over the A2A protocol → `orchestrator/dispatch`, `a2a`.

### Trace 3: Specialist task → A2A → evidence

1. Conductor sends an `A2A` payload (`Intent` + constraints + `ProofObligation`, read-only to the agent) to a specialist → `a2a` (re-derives [A2A-Protocol-Spec](../archive/PRD/A2A-Protocol-Spec.md)). The agent returns an untrusted `EvidenceClaim`.
2. Specialist agent runs inside a sandbox with a bounded context budget → `agent-runtime`, `sandbox`, `context-engine`.
3. Specialist produces artifacts (code diff, new files, test files) and a `Response Envelope` with claimed `assertion_status` and evidence → `a2a`.
4. Envelope returns to the Conductor; raw claim recorded but **not trusted** → `context-store` (`task_attempts`).

### Trace 4: Multi-modal proof → verdict → closure

1. Conductor routes the completed task's `ProofObligation` to the **Proof Harness** → `proof` (re-derives [Verification-Engine-Spec](../archive/PRD/Verification-Engine-Spec.md)). The harness reads the spec directly from the anchor-verified Context Store, not from the agent's bundle.
2. **Behavioral**: independent test run + mutation analysis on the artifact; test suite is owned by the harness, hidden from the generating agent → `proof/behavioral`.
3. **Empirical**: a transient **Lookout** agent probes the running artifact — ports, files, hashes, real I/O (reuses [Lookout-Agent-Spec](../archive/PRD/Lookout-Agent-Spec.md)) → `proof/empirical`.
4. **Hazard**: STPA-derived check that the unsafe control actions the change enables are controlled → `proof/hazard`.
5. The **Truth Alignment** engine applies the Discrepancy Decision Matrix to `(claim, converged evidence)` → `Accept` / `Reject` / `Inconclusive` (reuses [Orchestrator-Decision-Matrix](../archive/TDS/Orchestrator-Decision-Matrix.md)) → `orchestrator/truth-align`.
6. Verdict persisted to `context-store` (`proofs`). A `Task` **cannot transition to `done`** unless its proof verdict is `Accept` — closure is verification-gated.
7. On `Reject`/`Inconclusive`: task returns to the loop within its iteration budget; a degradation check compares this pass to the previous → `proof/degradation`. A net-negative pass terminates the loop rather than retrying.

### Trace 5: Proven change → deployment bar → delivery

1. When all tasks in a DAG are `done` (proof-passed), Conductor evaluates the **deployment bar** against the project's reliability tier → `delivery`.
2. **Bar met + tier permits autonomy** (V2.3): Orion delivers autonomously (commit/merge/deploy per tier policy) → `delivery/autonomous`.
3. **Bar met, autonomy not permitted** (V2.0/V2.1 default): Orion produces a proven, human-mergeable artifact/PR and marks it ready → `delivery/human-merge`.
4. **Bar not met**: Orion routes the open decision to the developer via the TUI with the specific failed proof named → `tui`, `context-store` (`escalations`).
5. Delivery outcome + the operating envelope (what was proven, under what workload/faults) recorded → `context-store` (`deliveries`).

### Trace 6: Failure mode observed → Polaris learning loop

1. On every task, the Decomposer and specialists are seeded with Polaris context (controls catalog, KB, risk register) pulled at session start → `polaris-connector` (consume) → `context-store` (`polaris_context`).
2. When the Proof Harness or Truth Alignment surfaces a failure mode (e.g., a hazard not previously catalogued, a recurring reward-hack pattern), it is recorded locally as a `FailureMode` observation → `context-store` (`failure_modes`).
3. **(V2.3)** Observed failure modes are contributed back to Polaris via a signed write-back → `polaris-connector` (contribute).
4. Polaris incorporates them; the next session's pulled context includes them, closing the learning loop.

### Trace 7: Context recall (any agent rebuilds context)

1. An agent (new, resumed, or restarted) needs context for task T → calls the Context Store recall API with `task_id` → `context-store`.
2. Store assembles a **context bundle**: the executable spec, the relevant decision-log slice, ancestor task outputs, prior proof verdicts, and applicable Polaris context → `context-store/recall`.
3. The **Context Engine** budgets the bundle to the agent's window, prioritizing intent-anchoring content, and stamps it with a re-anchor checkpoint → `context-engine`.
4. Agent proceeds with reconstructed, bounded, intent-anchored context — no dependence on in-memory session state.

## The Opinionated Reliability Loop (canonical execution map)

> This is the spine of the product: Orion's opinionated develop-and-deploy loop, mapped step by step. Each step is classified by **execution kind** so that implementation is unambiguous and `/prd-to-issues` can cut the right work item: a deterministic step becomes library/tool code; an LLM step becomes a prompt/agent; a hybrid step becomes both (an LLM proposal behind a deterministic gate). This map is normative — the data-flow traces above show *what data moves*; this shows *what runs, and whether a machine or a model does it.*

**Execution-kind legend** · **DET** = deterministic (library/shell/git/file-IO/MCP tool/API — no model judgment) · **LLM** = model interpretation/generation/judgment · **HYB** = LLM proposes, deterministic code verifies/gates.
**Trust legend** · **C** Conductor (control) · **G** Generation (untrusted) · **P** Proof (trusted) · **S** Context Store · **X** external (Polaris / git / package registry).
**A note on tooling:** every DET step is a candidate for an MCP tool or a plain Go library call; the right column names the concrete mechanism. LLM steps always run against the model as a swappable component (commodity-model principle).

### Phase A — Intent → Executable Spec

| # | Step | Kind | Mechanism | Trust | Writes |
|---|---|---|---|---|---|
| A1 | Capture intent from TUI | DET | TUI input event | C | raw intent |
| A2 | **Branch:** classify intake (idea · design doc · existing spec · backlog item) | HYB | rules + LLM fallback | C | intake_mode |
| A3 | Read existing spec / design doc / repo signals (if present) | DET | file IO, git read, MCP fs | C | source material |
| A4 | Completeness analysis vs required-decisions checklist | HYB | rules checklist (DET) + LLM enrichment (cassette-replayable) | C | OpenDecisions |
| A5 | Grill loop: ask clarifying questions, capture answers | LLM (ask) + DET (capture) | LLM question-gen; TUI capture | C↔human | decisions |
| A6 | Compile executable spec + `ResponseContract` from approved decisions; human ratifies/amends | HYB | LLM compile → human approval (DET) | C→P | spec (accepted), ResponseContract |
| A7 | Hash + anchor spec | DET | hash, store write | S | spec hash/anchor |

### The Executable Spec: required dimensions (the first line of defense)

The spec compiled in A6 is not just functional behavior. A **reliable, executable spec** must reach explicit alignment on the dimensions below *before* implementation — because each one is the seed of a control loop downstream. Leaving a dimension unspecified is how an uncontrolled control action reaches production: no stated scale → no concurrency/capacity controls; no stated observability → a system that can't be operated; no stated escalation → an alert with nobody on the other end. The completeness gate (A4) treats missing dimensions as `OpenDecisions` and grills for them (A5); the **reliability tier** calibrates which are mandatory and how precise they must be. These decisions flow directly into Phase C (which Polaris controls/knowledge are relevant), Phase D (what tasks and `ProofObligation`s exist), and Phase E (the STPA control structure the hazard mode models).

| Dimension | Captures | Precise form (with fallback) | Flows to |
|---|---|---|---|
| **Functional intent** | what it does | behavioral requirements → `ResponseContract` | proof (behavioral/empirical) |
| **Scale / load profile** | expected traffic + shape | X requests over Y window **+ request weight** (payload size, fan-out, CPU cost); fallback presets **low / medium / high** | capacity & concurrency controls; perf proof; tier |
| **Observability** | what's monitored & how | required signals (traces / metrics / logs), collection method (e.g. OTel), and export targets/integrations (e.g. Grafana, Datadog); fallback = tier-default signal set | instrumentation deliverables; control-loop observability (E10) |
| **On-call & escalation** | who to contact when it breaks | support method, escalation path/tiers, alert+notification tooling (e.g. PagerDuty/Slack); fallback = "single owner, log-only alert" | runbook; alert wiring; the 3 a.m. test |
| **Data & storage** | what persists, where | stores used, durability/consistency/retention, PII/sensitivity; fallback = "no persistence" | storage controls; reversibility gates; secret handling; tier |
| **Availability / SLO** | reliability targets | uptime/latency objectives + error budget; fallback = tier-default SLO | resilience controls; deployment-bar strictness |
| **Security & compliance** | trust + regulation | authn/z model, data classification, regulated domain; fallback = "untrusted input, no regulated data" | tier; security gates; hazard UCAs |
| **Dependencies & integrations** | external services it calls | downstream services/APIs and their failure modes; fallback = "none" | provenance; timeouts/retries/circuit breakers; hazard control structure |

> This list is intended to be complete enough to gate V2.0 but **extensible** — new dimensions register as new checklist entries and new control-structure seeds. The principle is fixed: *a dimension that affects a control loop must be decided in the spec, not discovered in production.*

### Phase B — Repository & Worktree (git operations)

| # | Step | Kind | Mechanism | Trust | Writes |
|---|---|---|---|---|---|
| B1 | **Branch:** greenfield → scaffold new repo · brownfield → locate + clone/open existing | DET | git, scaffolder, MCP git | C | repo handle |
| B2 | Checkout main / base branch | DET | git | C | base ref |
| B3 | Create isolated worktree (`worktrees-<repo>/<issue-id>`, branch `<issue-id>`, off `main`, shared object store — see [Orion-Worktree-Git](../SPEC/Orion-Worktree-Git.md)) | DET | `worktree` module (git plumbing) | C/S | worktree path + branch (recorded txn) |
| B4 | Initialize sandbox over the worktree (the worktree is the agent's only writable workdir) | DET | sandbox backend (gVisor/container/microVM) | C/S | sandbox handle |
| B5 | Brownfield only: build architectural/code map of existing repo | HYB | static analysis (DET) + LLM summarization | C (read-only) | code map |

### Phase C — Context & Reliability Loading

| # | Step | Kind | Mechanism | Trust | Writes |
|---|---|---|---|---|---|
| C1 | Connect/auth to Polaris (if not cached) | DET | polaris-connector API | X | session |
| C2 | Pull/refresh controls catalog · risk register · KB (relevance-filtered, cached, TTL) | DET | polaris-connector API + local cache | X→S | polaris_context |
| C3 | Reliability scan of target — dispatch `rvl:*` detector fleet, correlate, save risks | LLM (fleet) + DET (correlate/persist) | reliability-scan + agent-runtime; risk write | G→X/S | risks/findings |
| C4 | Search KB / facts / patterns / procedures relevant to spec | HYB | embedding/keyword search (DET) + LLM relevance | X→S | retrieved context |
| C5 | Determine reliability tier from risk dimensions | HYB | LLM classify → human confirm | C | tier |
| C6 | Retrieve relevant LTM patterns / procedures / refinements (self-evolution recall) | DET (retrieve) + HYB (relevance) | memory + context-engine | S | retrieved memory |
| C7 | **Develop/refine STPA artifacts** via the directed **STPA questionnaire skill** (4 gated phases: losses → control structure → UCAs → loss scenarios; adapted from the existing `stpa-design-review` skill) — read Polaris defaults → drive the developer Q&A, confirming each control-structure edge → persist ratified artifacts back to Polaris | HYB (questionnaire) + human (ratify each gate) + DET (persist) | Conductor + polaris-connector | C↔human / X | losses, control structure, UCAs, loss scenarios (ratified) |

### Phase D — Decomposition → Epics & Tasks → Tracker

> **Terminology (use agile/PM language).** An accepted spec becomes an **Epic** — the unit of delivery. The Epic decomposes into **Tasks**, with dependency edges between them; that dependency graph *is* the DAG (we keep "DAG" only as a parenthetical annotation where the graph structure matters). This is deliberate: the tracker projection (beads / GitHub Issues / Jira) already speaks Epic/Task, so Orion's source-of-truth uses the same nouns it projects.

| # | Step | Kind | Mechanism | Trust | Writes |
|---|---|---|---|---|---|
| D1 | Decompose spec → an **Epic** of **Tasks** with dependency edges (the DAG); each Task gets a `ProofObligation` | LLM | decomposer | C | epic, tasks, task_deps, proof_obligations |
| D2 | Coverage gate: every spec requirement maps to ≥1 `ProofObligation` across the Epic's Tasks | DET | set check | C | gate result |
| D3 | Render the Epic/Task plan for human approval | DET (render) + human | TUI / web design pane | C↔human | plan_approved_at |
| D4 | Write Epic + Tasks to the issue tracker (projection) | DET | beads/GitHub/Jira adapter | S→X | tracker projection |
| D5 | Persist Epic + Task graph to Context Store | DET | store write (txn) | S | epic + task graph |

### Phase E — Per-Issue Coding / Verification Loop  *(for each ready task)*

| # | Step | Kind | Mechanism | Trust | Writes |
|---|---|---|---|---|---|
| E1 | Mark issue `in_progress` (store + tracker) | DET | store txn + tracker write | S/X | task.status |
| E2 | Recall: assemble bounded, budgeted context bundle | DET (windowed query) + HYB (relevance/budget) | context-store + context-engine | S | ContextBundle |
| E3 | Dispatch specialist generator (A2A, bounded, sandboxed) | LLM | agent-runtime + a2a + sandbox | G | artifacts, EvidenceClaim |
| E4 | Dependency provenance check on new deps (existence + provenance) | DET | registry API + heuristics | P/X | provenance verdict (gate) |
| E5 | Secret scan of generated artifact | DET | scanner | P | secret findings (gate) |
| E6 | Persist artifacts (+ untrusted EvidenceClaim) | DET | store txn | S | artifacts, task_attempts |
| E7 | Mark issue `being_validated` | DET | store + tracker | S/X | task.status |
| E8 | **Behavioral proof:** harness-side test synthesis from spec → run → mutation score | HYB (synth in **P**) + DET (run/mutation) | proof/test-synthesis + proof/behavioral | P | proof(behavioral) |
| E9 | **Empirical proof:** run real entry point + probe (per-type adapter) vs ResponseContract + control-loop tests | DET | proof/empirical (Lookout) | P | proof(empirical) |
| E10 | **Hazard proof:** check the change against the **developer-ratified, Polaris-persisted** STPA control structure/UCAs → controls present? → test control actions/loops (artifact *development* is the developer-involved modeling step, upstream) | HYB + DET (checks) | proof/hazard | P | proof(hazard) |
| E11 | `Converge(behavioral, empirical, hazard)` → Verdict (+ dissenting modes) | DET | truth-align | P | verdict |
| E12 | Degradation check vs previous attempt (per-dimension) | DET | metric compare | P | degradation result |
| E13 | **Branch on verdict:** Accept → proven · Reject/Inconclusive → re-loop (iteration budget) · degraded → terminate | DET | conductor state machine | C | task.status |
| E14 | Drift check / re-anchor (cheap-first; escalate on threshold) | HYB | context-engine (embedding DET + LLM re-anchor) | C | drift score |
| E15 | Per-step confidence + circuit breaker (harness-derived signals) | DET | conductor | C | breaker state |
| E16 | Mark issue `done` — store-enforced: requires `proof_id` with verdict=Accept | DET | store constraint + tracker | S/X | task.status=done |

### Phase E2 — Change Coordination & Integration

> The hard part of a multi-agent loop: many independent agents producing changes that must merge into one coherent codebase without clobbering each other or main. This layer is deliberately conservative — **no change is silently overwritten, and every integration is re-proven on the merged tree.** It mirrors and generalizes the existing `/queue` (serialized merge worker) and `/resolve` (rebase-conflict resolver) patterns.

**Coordination policies (normative):**

- **Avoid conflicts before they happen (partition + leases).** The decomposer partitions Tasks to minimize file/path overlap; each Task declares its expected **file scope**. The Conductor grants **path leases** over that scope — a Task whose scope overlaps an active lease *waits* rather than editing concurrently. This makes "two agents editing the same code at once" the rare exception, not the norm.
- **Isolation:** each Task works in its own worktree (Phase B3); agents never share a working tree.
- **Serialized integration (singleton lock):** proven Tasks enter one integration queue, processed one at a time onto the Epic's integration head — no parallel merges.
- **Re-prove after integration is mandatory.** A Task proven *in isolation* is necessary but not sufficient: the integration head may have moved, so proof is re-run on the **merged** tree before the change counts. This is the defense against "main changed while the agent was working."
- **Conflict resolution, never silent resolution.** A rebase conflict dispatches a resolver agent that merges preserving *both* intents, then re-proves; if it can't, it escalates to the human. Conflicts are never auto-discarded.
- **Winner policy:** leases prevent same-path collisions; if one still occurs, the queue serializes — the first Task integrates, the second rebases onto the result and re-proves. Neither side is silently dropped.
- **Rollback on red:** if post-merge proof fails, the integration is hard-reset and the Task returns to the coding loop; the integration head is never left broken.

| # | Step | Kind | Mechanism | Trust | Writes |
|---|---|---|---|---|---|
| E2.1 | Acquire path lease for the Task's declared file scope (queue if it overlaps an active lease) | DET | lease manager | C | lease |
| E2.2 | Enqueue the proven Task into the serialized integration queue (singleton lock) | DET | integration queue | C/S | queue entry |
| E2.3 | Rebase the Task worktree onto current integration head | DET | git rebase | C | rebased tree |
| E2.4 | **Branch:** rebase conflict? → dispatch resolver agent (merge preserving both intents); unresolved → escalate | HYB (resolver) + DET (git) | resolver agent + git | G→C | resolution / escalation |
| E2.5 | Pre-merge gates on the rebased tree (build, lint, fast checks) | DET | shell / CI | P | gate result |
| E2.6 | Merge into the integration head | DET | git merge | C | merged tree |
| E2.7 | **Re-prove on the integrated tree** (behavioral + empirical + hazard) — isolated proof is not enough | HYB/DET | proof harness | P | post-merge verdict |
| E2.8 | **Branch:** green → advance head, release lease, mark `integrated` · red → hard-reset rollback, return Task to loop | DET | git + store txn | C | head / rollback |

> **Open design choice flagged:** lease granularity (file vs. package vs. symbol range) trades parallelism against collision rate. V2.0 default is **file-scope leases**; finer granularity is a later optimization. The integration queue is per-Epic in V2.0 (one Epic in flight); cross-Epic integration ordering is a V2.1+ concern.

### Phase F — Delivery & Deployment Bar

| # | Step | Kind | Mechanism | Trust | Writes |
|---|---|---|---|---|---|
| F1 | On DAG complete: evaluate deployment bar vs tier | DET | delivery | C | bar decision |
| F2 | Generate instrumentation + runbook + operating envelope | HYB (LLM author) + DET (validate completeness) | generation + proof | G→P | runbook, envelope |
| F3 | **Branch:** bar met + autonomy permitted (V2.3) → deliver autonomously (commit/merge/deploy) · else → human-mergeable PR/artifact | DET | git/CI, delivery | C/X | delivery |
| F4 | Reversibility gate on any destructive delivery op (interception, not declaration) | DET | sandbox syscall/egress interception → human approve | C/S | approval record |
| F5 | Record delivery + operating envelope | DET | store txn | S | deliveries |
| F6 | Submit evidence to Polaris for proven controls *(V2.1; automatic — evidence is proof-of-work)* | DET | polaris-connector (evidence) | X | evidence |

### Phase G — Learning Write-back

| # | Step | Kind | Mechanism | Trust | Writes |
|---|---|---|---|---|---|
| G1 | Record observed failure modes locally (canonical_key dedup) | DET | store write | S | failure_modes |
| G2 | Resolve/update risks in Polaris (proven-fixed) | DET | polaris-connector (risk) | X | risk status |
| G3 | Contribute knowledge / failure modes back (V2.3; minimized + redacted + human-confirm) | HYB (LLM redact) + DET (submit) + human gate | polaris-connector (knowledge) | X | contributions |
| G4 | Promote validated patterns/procedures/skill refinements to LTM (self-evolution); provenance-tagged, reversible | HYB (LLM distill) + DET (write) | memory | S | LTM updates |

**Cross-cutting (run throughout, not a phase):** budget accounting (DET), TUI liveness/progress events (DET), structured self-instrumentation logs/traces (DET), signal-handler cleanup on abort (DET), context-store transactional writes (DET). These are specified in the Harness Reliability and Resource & Cost Governance sections.

> **Implication for issue-cutting:** the DET steps are the "MCP tool / library code" paths the request calls out (git, worktree, shell verification, registry checks, tracker writes, store txns, scanners); the LLM/HYB steps are the model-interpretation paths (completeness, grilling, decomposition, generation, test/hazard synthesis, drift, redaction). V2.0 builds Phases A–F on the Go-greenfield-service path; brownfield (B5), tracker projection (D4 GitHub), polyglot proof adapters (E8–E10), and write-back (G2–G3) phase in per the roadmap.

## UI Navigation

> Orion V2's **primary** surface is a **TUI** — the developer↔Conductor conversation and control live there. An **optional companion web surface** complements it for the two things a terminal does poorly: (1) presenting visual design options/mockups for approval, and (2) richer fleet/proof observability dashboards. The web surface is read-mostly-plus-approve; it never replaces the TUI as the control plane, and the loop is fully operable from the TUI alone. (Polaris, a separate product, retains its own web UI.)

| View / Pane | How reached | Empty State | Gating |
|---|---|---|---|
| **Conversation** (default) | Launch `orion` | "Describe what you want to build, or point me at a repo or backlog." | always |
| **Spec review** | Auto-shown when the completeness gate produces an accepted-pending spec; `:spec` | "No spec yet — still clarifying intent." | always |
| **Plan (Epic / Tasks)** | `:plan` or auto on decomposition | "No plan yet — approve a spec to generate the Epic." | always |
| **Fleet status** | `:fleet` | "No agents active." | always |
| **Proof / Evidence** | `:proof <task>` or selecting a task | "No proofs yet — tasks are still in progress." | always |
| **Transcript / decision log** | `:log` | "No decisions recorded yet." | always |
| **Escalations** | Auto-surfaced (modal) when blocked on a human decision; `:asks` | "Nothing needs you right now." | always |
| **Delivery** | `:deliver` or auto on bar evaluation | "Nothing ready to deliver." | always |
| **Reliability tier** | `:tier` | shows current tier + dimensions | always |

Interaction model: the developer mostly lives in **Conversation**; the Conductor proactively raises **Escalations** (the "pull the human in" moment) and surfaces **Spec review** / **Plan** for approval gates. **Fleet status** and **Proof** are observability panes — visible on demand, never required for the happy path.

### Companion web surface (optional, phased)

A local web app Orion can serve when richer-than-terminal interaction helps. Scope is deliberately narrow:

| Web view | Purpose | Approve here? |
|---|---|---|
| **Design review** | Render visual design options/mockups for a spec or UI decision and collect feedback (can leverage design tooling, e.g. Stitch/Figma, to generate candidates) | yes — design-decision approval can happen in the web view or the TUI |
| **Fleet & proof dashboard** | Live view of the agent fleet, the Epic/Task graph, integration queue, and proof results | no — observability only |

Principles: the TUI remains the authoritative control plane and conversational surface; any approval made in the web surface is recorded as a `decision`/approval in the Context Store exactly as a TUI approval would be (one source of truth); the web surface is **optional** (the loop runs headless/TUI-only without it). Phasing: fleet/proof dashboard is the simpler first cut; visual design-review is the higher-value piece for design-heavy work — both land post-V2.0 unless a tracer-bullet need pulls them earlier.

## Scope: Orion as the Reliability Touchpoint (subsumes rvl-cli)

Orion is intended to **supplant rvl-cli as the developer's primary touchpoint** and absorb its capabilities, so reliability scanning, the risk register, knowledge, and evidence live inside the same loop that builds and proves software — not in a separate tool the developer has to remember to run.

**rvl-cli capabilities Orion absorbs** (current rvl surface, for parity):

| rvl-cli capability | Orion home | Notes |
|---|---|---|
| `rvl login/logout/status`, `config` | `polaris-connector` + `:status`/CLI | One platform connection lives in Orion. |
| `rvl init` (project + agent plugin) | `cmd/orion init` | Orion *is* the agent harness, so "install the plugin" collapses into Orion itself. |
| `/rvl:scan` (multi-agent risk scan) | `reliability-scan` (uses the `rvl:*` fleet) | Scanning becomes a phase of the loop, feeding the same register the loop remediates. |
| `rvl risk` (list/show/resolve) + open new | `polaris-connector` (risks) | Read **and** write; the loop opens risks it finds and resolves risks it proves fixed. |
| `rvl control` (61-control catalog) | `polaris-connector` (controls) | Consumed as reliability context on every task. |
| `rvl knowledge` (search KB) | `polaris-connector` (knowledge) + `context-engine` | Facts/patterns/procedures injected during the loop; contributable back. |
| `rvl evidence` (submit/manage) | `polaris-connector` (evidence) | A proof-passed control auto-produces evidence (see data flow below). |
| `/rvl:fix`, `/rvl:ask`, `/rvl:review` | the loop + Conductor conversation | Remediation/Q&A/review become native Conductor interactions, not separate commands. |

**Native Polaris read/write — data flows:**

- **Evidence (write):** when a task's proof verdict is `Accept` for a control-bearing change, `delivery`/`polaris-connector` submits structured evidence to Polaris (control id + the proof envelope) → Polaris evidence store. *"Proven in Orion" becomes "evidenced in Polaris" automatically.*
- **Risk (read+write):** the `reliability-scan` and proof harness open risks for findings they cannot fix in-loop; the loop resolves risks whose controls it proves implemented; `:risks` reads the register. Risks carry the Orion run id for traceability.
- **Knowledge (read+write):** the loop reads facts/patterns/procedures relevant to the spec/tier; with developer confirmation it contributes newly-validated patterns and observed failure modes back (the learning loop; minimized + redacted per Security Requirements).
- **Controls (read):** the 61-control catalog is reliability context for decomposition and proof rigor.

**Deployment split (interactive vs. headless):** Orion is exposed as **(a) the interactive TUI** (primary) and **(b) non-interactive `orion` CLI subcommands** for CI/agent/headless use — the same surface the acceptance criteria exercise. rvl-cli's headless niche is served by Orion's CLI mode, so rvl-cli can be deprecated once parity is reached.

**Phasing of the touchpoint:**

| Capability | Phase |
|---|---|
| Auth, `status`, controls/KB/risk **read**, reliability scan | V2.0 |
| Risk **write** (open/resolve), evidence **submit** | V2.1 |
| Knowledge **contribute**, failure-mode write-back | V2.3 |
| rvl-cli deprecation (after full parity verified) | V2.3+ |

> **Parity prerequisite:** the rvl-cli command/flag/output inventory and the Polaris API contracts are captured as a first design task alongside the Triad reconciliation table. The Polaris API is **OpenAPI-spec'd**, so the contract is consumed directly (no manual reverse-engineering) — see [`docs/SPEC/Orion-Polaris-API-Contract.md`](../SPEC/Orion-Polaris-API-Contract.md). Orion's CLI surface must be a strict superset of rvl-cli so no workflow regresses.

## Memory & Context-Erosion Defense (a PRD-level primitive)

Context erosion is the slow death of long agentic runs: as the window fills, earlier decisions and constraints get compressed, diluted, or silently dropped, and the agent starts reasoning on a thinned-out picture of its own task. It is distinct from **drift** (the trajectory diverges while facts stay correct) and **poisoning** (injected instructions corrupt memory) — and Orion must defend all three. Countering erosion is treated here as a **first-class primitive**, not a side effect of prompt assembly, because a harness that loses the thread cannot produce reliable software.

Orion's answer is a **MemoryOS-style tiered memory** (the `memory` module), inspired by hierarchical memory operating systems for LLM agents:

- **STM — short-term:** the current Task's working set (active spec slice, current decisions, in-flight artifacts). Small, hot, fully in-context.
- **MTM — mid-term:** the run/session memory, organized as **pages** with a **heat** score (recency + frequency + relevance). When a page is displaced it is **summarized, not dropped**; cold pages are evicted only after summarization.
- **LTM — long-term:** durable, cross-run memory — the anchored original intent, validated patterns, procedures, failure modes, and prompt/skill refinements, scoped per project and per developer. This is the substrate for **self-evolution**.

**Mechanisms:**

- **Heat-based promotion / eviction:** hot MTM pages promote to LTM; cold ones summarize-then-evict. Nothing relevant is hard-dropped.
- **Anti-erosion pins:** the executable spec and critical decisions are pinned and **re-injected every step** — they can never be evicted or summarized away. This is the structural cure for "the agent forgot the constraint."
- **Retrieval-augmented assembly:** `context-engine` builds each step's `PromptContext` by retrieving from the right tiers, intent-anchored and budgeted (ties to bounded/windowed recall in Resource & Cost Governance).
- **Provenance-tagged memory:** every memory item carries its trust tier (per Security Requirements) so a learned pattern or summarized page cannot smuggle instructions — erosion defense and poisoning defense share the same substrate.

**Self-evolution (why this is strategic).** LTM is not just recall — it is how the harness *improves itself*. Validated patterns, procedures, and refined skills/prompts accumulate in LTM and are retrieved on future runs, so Orion gets better at a project (and a developer's style) over time without code changes. This is the local analogue of the Polaris learning loop: **Polaris is the cross-organization shared memory; LTM is Orion's local, private memory.** Guardrails are non-negotiable: self-evolution must respect the trust domains — a learned artifact may never weaken proof, proof-side evolution is gated and re-validated, and every evolved artifact is provenance-tagged and reversible (a bad "learning" can be rolled back). Self-modification of skills/prompts is the Pi-style `/reload` analogue (see Composition Model), bounded by these guardrails.

**Relationship to the Context Store.** They are different layers and must not be conflated: the **Context Store** is the durable, authoritative record of *project facts* (spec, Epics/Tasks, proofs, deliveries — the SQLite source of truth); **memory** is the *cognitive layer* that decides what enters context and what the harness carries forward and learns. Memory holds summaries, heat, and retrieval indices over the facts; the Context Store holds the facts themselves. STM is in-memory; MTM/LTM persist (alongside the Context Store).

**Loop touchpoints:** LTM patterns/procedures are retrieved in Phase C (context loading); the memory tiers drive Recall in Phase E2; validated learnings are promoted to LTM in Phase G (and shared to Polaris on confirmation). These are reflected as steps C6 and G4 in the loop map.

## Implementation Decisions

### Modules (target boundaries; deep modules favored)

1. **`tui`** — terminal UI (**bubbletea**) and the **central orchestration surface**, implemented as an **Agent Client Protocol (ACP) client**. Renders the Conductor's `session/update` stream (agent/thought chunks → Conversation; plans → Plan; tool calls → Fleet/Proof), sends `session/prompt` (intent + answers), handles `session/request_permission` as Orion's approval/escalation gates, and serves sandboxed `fs/*` + `terminal/*`. Because it's a standard ACP client, the loop can also be driven by other ACP clients (e.g., Zed). Full design: [Orion-TUI-ACP-Conductor](../SPEC/Orion-TUI-ACP-Conductor.md). *(Comparator UX: Gastown/Hermes/Pi.)*
2. **`orchestrator` (the Conductor)** — the developer's **own coding agent, spawned and primed** as the Conductor (Claude Code by default; any ACP agent) + a thin lifecycle manager — *not* Orion-authored inference. Its orchestration personality is a **role template** (intent intake, completeness gate, decomposition, dispatch, drift/re-anchor, escalation) injected at spawn; the TUI drives it over ACP. **It reasons and delegates; it does not compute proof verdicts or override deterministic gates** — `truth-align`/Converge, the deployment bar (`delivery`), leases/merge (`integration`/`worktree`), and dry-run/reversibility (`sandbox`) are separate deterministic tools it invokes but cannot influence (the reasoning⊥execution split; resolves the round-2 god-object finding). The `internal/conductor` deterministic state machine remains the gating spine. Lifecycle manager keeps a pid/agent file + cleanup veto while alive. See [Orion-TUI-ACP-Conductor](../SPEC/Orion-TUI-ACP-Conductor.md).
3. **`decomposer`** — deep module. `Decompose(spec, context) → Epic{Tasks, deps}` — produces an **Epic** whose **Tasks** form a dependency DAG; each Task carries a `ProofObligation` and a **declared file scope** (used for path leasing in integration). Partitions Tasks to minimize file-scope overlap. Re-derives the [Task-Decomposer-Spec](../archive/PRD/Task-Decomposer-Spec.md) concept.
4. **`context-store`** — deep module; **the durable source of truth**. `Put/Get/Recall` over spec, decisions, tasks/DAG, attempts, proofs, deliveries, failure modes, Polaris context. Exposes a `Recall(task_id) → ContextBundle` API. Backed by an embedded store in V2.0 (see below). A **tracker projection adapter** syncs the task subset out to beads or GitHub Issues (V2.1); the tracker is a *view*, never the source of truth.
5. **`context-engine`** — deep module that sits atop `memory`. Context-window budgeting, intent-anchoring, drift detection + re-anchoring, memory-poisoning defense; assembles the per-step `PromptContext` from the memory tiers. Interface: `BudgetBundle(bundle, window) → PromptContext`, `DetectDrift(active, spec) → DriftScore`.
6. **`memory` (MemoryOS-style)** — the cognitive-memory primitive that counters **context erosion** and carries learning across runs (see "Memory & Context-Erosion Defense"). Tiered: **STM** (current-task working set), **MTM** (run/session pages with heat = recency+frequency+relevance, summarized when displaced), **LTM** (durable patterns, procedures, refinements, anchored intent — per project & developer; the substrate for self-evolution). Heat-based promotion (MTM→LTM) and summarize-then-evict (never hard-drop); pins spec + critical decisions so they are never evicted. Interface: `Write(item, tier)`, `Promote/Evict`, `Retrieve(query, tiers) → []MemoryItem`. Feeds `context-engine`; ground-truth facts still live in `context-store`.
7. **`a2a`** — agent-to-agent message protocol + bus for **horizontal delegation** (Conductor→specialists; `ProofObligation` out, untrusted `EvidenceClaim` back). Distinct from **ACP**, which is the **vertical client↔agent** protocol (TUI↔Conductor; see module 1 + [Orion-TUI-ACP-Conductor](../SPEC/Orion-TUI-ACP-Conductor.md)). Dispatch is lock-guarded + idempotent (no double-assign). Re-derives [A2A-Protocol-Spec](../archive/PRD/A2A-Protocol-Spec.md).
8. **`agent-runtime` (agent provider + spawn)** — spawns and manages the developer's **vendor coding-agent CLIs** (Claude Code default; Gemini, Codex, …) as both the Conductor and the specialist fleet — Orion is a control plane, not an LLM client. Backed by an **agent-preset registry**: per-agent launch command, injected env (`CLAUDE_CONFIG_DIR`, optional `ANTHROPIC_BASE_URL`/key), process detection, and ACP mode (`native`/`--acp`/subcommand). Orion injects only env + the role template; the agent authenticates itself (Max/Pro login). Agents are driven over **ACP** (client↔agent) / **A2A** (agent↔agent), each bounded, sandboxed, deadline'd, least-privilege. Interface: `Registry.Spawn(role, task) → AgentHandle` (Cancel/deadline). **Note:** the behavioral test author is a *separate* spawned agent in the *proof* domain (`proof/test-synthesis`), never the generator — per Trust-Domain invariant 1.
9. **`sandbox`** — isolated execution for generation and proof; reversibility gates on destructive ops; first-class mid-execution abort. Pluggable backend (gVisor default / container / microVM). Mounts the task's `worktree` as the agent's only writable workdir.
10. **`worktree`** — deterministic git-plumbing primitive (the single chokepoint for `git worktree …`). One worktree per task at `worktrees-<repo>/<issue-id>` (branch `<issue-id>`, off `main`, shared object store). Interface: `Create/CreateResume/List/Remove(safety-gated)/Prune/Reconcile/Status`. Owns deletion-safety gates and filesystem-as-source-of-truth reconciliation. Full design: [Orion-Worktree-Git](../SPEC/Orion-Worktree-Git.md).
11. **`proof`** — deep module; the **Proof Harness**. Sub-engines: `behavioral` (test-synthesis + run + mutation score), `empirical` (Lookout per-type adapters), `hazard` (STPA control-structure modeling). `Prove(artifact, obligation) → Verdict` requiring convergence. Re-derives [Verification-Engine-Spec](../archive/PRD/Verification-Engine-Spec.md) + [Lookout-Agent-Spec](../archive/PRD/Lookout-Agent-Spec.md).
12. **`dependency-provenance`** — deep module. `Verify(pkgRef) → {exists, resolves, provenance}` before any dependency enters the build.
13. **`reliability-tier`** — config + policy. Maps a project's risk dimensions (data sensitivity, concurrency exposure, blast radius, reversibility, regulated domain) to the controls and proof rigor required, and to whether autonomous delivery is permitted.
14. **`delivery`** — deployment-bar evaluation; autonomous delivery (V2.3) vs. human-mergeable fallback; operating-envelope reporting.
15. **`integration`** — the change-coordination layer (Phase E2). Owns: path-lease manager, the serialized integration queue (singleton lock), rebase-onto-head, pre-merge gates, post-merge re-proof, rollback-on-red, and dispatch of the resolver agent on conflicts; drives the `worktree` module for merge/cleanup. Interface: `AcquireLease(scope)`, `Enqueue(task)`, `Integrate(task) → {integrated | conflict | rolled_back}`. Generalizes the `/queue` + `/resolve` patterns.
16. **`polaris-connector`** — the full bidirectional Polaris client that subsumes rvl-cli (see "Scope: Orion as the Reliability Touchpoint"). Capabilities: auth (`login/logout/status`), consume (controls catalog, KB/facts/patterns/procedures, risk register), and write (open/resolve risks, submit evidence, contribute knowledge, contribute failure modes). All reads land in the local `context-store` cache (offline-tolerant); all writes are signed server-to-server and, for outbound content, pass the redaction + confirm gate. Phased per the scope section.
17. **`reliability-scan`** — runs the rvl multi-agent reliability scan using Orion's specialist fleet (the `rvl:*` detector roles), correlates findings, and writes risks to the register. Reuses `agent-runtime`; outputs are findings/risks, not deliverable artifacts.
18. **`cmd/orion`** — the single binary (**cobra** CLI + **bubbletea** TUI). **`orion` with no args launches the agentic chat TUI** (the default experience — it spawns + primes the Conductor agent and opens the conversation). Non-interactive CLI subcommands cover the rvl-cli-parity surface + the loop-control surface used by the acceptance criteria, plus `orion conductor {start|stop|attach|status|restart|acp}` lifecycle subcommands (maps `gt mayor`).

### The Project Context Store — design

The Context Store is the answer to "externalize task/project context into a persistent layer any agent can call to build/rebuild/update context." Decisions:

- **Source of truth vs. tracker are separated.** The store holds the full structured context (spec, decision log, task DAG with verification contracts, attempts, proofs, deliveries, failure modes, Polaris context). A **tracker** (beads or GitHub Issues) is a *projection* of the task subset, kept in sync by an adapter. This avoids coupling the source-of-truth to beads' setup fragility or to GitHub Issues' weak structured-recall.
- **V2.0 backing store:** embedded and local (e.g., SQLite or an embedded KV) — no external service required for the core loop, consistent with local-first runtime. The exact engine is a V2.0 build decision; the *interface* (`Put/Get/Recall`) is the stable contract.
- **Recall is first-class.** `Recall(task_id) → ContextBundle` is the API every agent uses to (re)build context; it is the mechanism that makes agents resumable and the run robust to crashes (Story 31).
- **Tracker default:** V2.0 ships beads-backed (already integrated in the existing pipeline) behind the projection adapter; GitHub Issues projection lands in V2.1. Either can be swapped without touching the source of truth.

### Schema (Context Store entities — logical)

`projects`, `specs` (status: drafting/accepted/revised; `version`/`parent_spec_id`), `spec_dimensions` (**typed**, one row per dimension per spec version: `{spec_id, dimension[functional|scale|observability|oncall|data|slo|security|dependencies], value_structured JSONB, value_kind[precise|fallback_preset|unresolved], tier_required bool, resolved_at}` — not prose annotation), `decisions`, `epics` (an accepted spec's unit of delivery), `tasks` (belong to an epic; status: ready/in_progress/being_validated/proven/integrated/done; carry a declared **file scope**; `proof_id` FK), `task_deps` (the dependency DAG), `task_attempts` (idempotency key; consumed-upstream-artifact refs), `proof_obligations`, `proofs` (mode: behavioral/empirical/hazard; verdict + quantitative metrics), `deliveries` (with operating envelope), `escalations`, `failure_modes` (with `canonical_key`), `polaris_context`, `artifacts`, `polaris_write_attempts` (`{idempotency_key, op, status, last_http_status, retry_count}`); integration entities: `worktrees` (`task_id, path, branch, start_point, status`), `leases` (path scope + holder + TTL/heartbeat), `integration_queue`, `integration_lock` (`holder_id, acquired_at, heartbeat_at, pre_merge_head_sha`), `merge_checkpoint` (`task_id, pre_merge_head_sha`), `merge_attempts` (conflict/rollback records). All carry `project_id`; the Epic+Task subset projects to the configured tracker. **The `memory` tiers (`memory_items`, `memory_evolution_log`) live in a separate embedded store** (see Memory & Self-Evolution Hardening), not the Context Store.

### Contracts (selective)

- **ACP** (TUI ⇄ Conductor, client↔agent): JSON-RPC 2.0; agent serves `initialize`/`session/new|load`/`session/prompt`/`session/cancel` + streams `session/update`; client serves `fs/*`/`terminal/*`/`session/request_permission`. Orion extensions via `_meta` (ProofObligation refs, tier, budget). See [Orion-TUI-ACP-Conductor](../SPEC/Orion-TUI-ACP-Conductor.md).
- **A2A payload** (Conductor ⇄ specialist, agent↔agent): `Intent`, `constraints`, `ProofObligation` (read-only to the agent), `Response Envelope{EvidenceClaim}` (untrusted). Per [A2A-Protocol-Spec](../archive/PRD/A2A-Protocol-Spec.md).
- **Proof verdict**: `Converge(behavioral, empirical, hazard) → {mode_results, verdict: Accept|Reject|Inconclusive, dissenting_modes, envelope}` (per-mode provenance retained; not a bare bool).
- **Polaris (server-to-server, signed) — all writes carry an `orion_idempotency_key` (= `run_id+task_id+op`); 4xx → log full payload + escalate, never silent-drop:**
  - `GET controls/kb/risks` (consume, V2.0).
  - `POST /evidence` `{control_id, orion_run_id, task_id, verdict_summary, proof_metrics, active_skill/package versions}` — **automatic on proof-pass for all tiers.** Polaris evidence is *proof-of-work*, not a correctness attestation, so auto-submission is the intended behavior; carries proof provenance for traceability/revocability. *(V2.1.)*
  - `POST /risks` / `PATCH /risks/{id}` — open is additive (auto); resolve/close is automatic but carries proof-provenance, is idempotent, and is rate-limited + deduped via `canonical_key`. *(V2.1.)*
  - `POST /knowledge` / `POST /failure-modes` `MinimizedPattern{class, control_category, hazard_type, symptom_class, occurrences, run_ids}` — **deterministic** secret/path/code redaction (not LLM-judge; LLM may only abstract already-redacted content); confirm gate shows a diff of exactly what leaves the machine; default off. *(V2.3.)*
  - *Exact field sets are taken from the **Polaris OpenAPI spec** (the contract is published; see `docs/SPEC/Orion-Polaris-API-Contract.md`).*

### Feature Flag / Config Wiring

Local config (V2 has no remote flag service for the core loop; flags are config keys read at startup and per-run):

1. **`tracker.backend`** — `beads` (default V2.0) | `github` (V2.1).
   - *Definition:* Orion config file. *Check:* `context-store` projection adapter. *Behavior:* selects which tracker the task subset projects to. (GitHub path is a V2.1 implementation task.)
2. **`autonomy.enabled`** — `false` (default V2.0/V2.1) | `true` (V2.3, per tier).
   - *Definition:* config + `reliability-tier` policy. *Check:* `delivery`. *Behavior:* on → autonomous delivery when proof passes at the tier; off → human-mergeable fallback always. (Autonomous path is a V2.3 task.)
3. **`polaris.learning_writeback`** — `false` (default) | `true` (V2.3).
   - *Definition:* config. *Check:* `polaris-connector`. *Behavior:* on → observed failure modes are contributed back; off → consume-only. (Write-back is a V2.3 task.)
4. **`reliability.tier`** — `throwaway` | `standard` | `critical` (+ explicit risk-dimension overrides).
   - *Definition:* per-project config. *Check:* `reliability-tier`, consumed by `proof` (rigor) and `delivery` (autonomy gate). *Behavior:* sets which controls are required and how strict the proof bar is.

## Composition Model: Primitives, Skills & Workflows (Pi-style)

Orion adopts [Pi](https://pi.dev/)'s philosophy: **primitives over features.** The core stays minimal and composable; capabilities are added as on-demand skills, extensions, and installable packages — "features other agents bake in, you build (or swap) yourself." Orion differs from Pi in one deliberate way: where Pi is unopinionated, **Orion ships an opinionated reliability workflow by default** (the loop in this PRD). The stance is *opinionated defaults, composable internals* — the reliability loop is the batteries-included default, but every layer underneath it is a swappable primitive, skill, or workflow.

Three unit types — this is also the taxonomy `/prd-to-issues` uses to break the PRD down. It maps directly onto the loop map's **Kind** column (`DET → primitive`, `LLM → skill`, `HYB → skill behind a primitive gate`, orchestration/branches → `workflow`):

| Unit type | What it is | Maps to | Examples | How it's built/tested |
|---|---|---|---|---|
| **Functional primitive** | Deterministic coded capability — a Go package and/or MCP tool. The stable, rarely-changing core. | loop-map **DET** steps | git/worktree ops, sandbox backends, `integration` (queue+leases), `context-store`, `memory` tiers, proof runners (mutation, Lookout exec), `dependency-provenance`, secret-scan, tracker adapters, `budget` accountant | Go pkg + unit tests; deep module, isolated. |
| **Skill** | Model-facing capability package — instructions + tool grants, loaded **on-demand** (progressive disclosure). The main extension surface; swappable per model (commodity-model principle). | loop-map **LLM/HYB** steps | completeness-grilling, decomposition, code generation (per language), `proof/test-synthesis`, hazard/STPA modeling, drift re-anchoring, conflict resolution, redaction, runbook authoring, the `rvl:*` detectors | prompt/agent definition + an **eval**; lives in a trust-domain registry (generation vs proof, kept separate). |
| **Workflow** | Opinionated composition wiring primitives + skills into a phase of the loop. Encodes Orion's reliability stance; overridable with safe defaults. | loop-map **phases & branches** | spec-elicitation (Phase A), per-Task coding/verification loop (Phase E), integration (Phase E2), delivery (Phase F), scan, learning write-back (Phase G) | composition code + an **integration test**. |

**Developer-facing skills (a sub-class).** Most skills are agent-facing, but a few are **directed questionnaires that interview the developer** — the intent-completeness gate (Phase A) and the STPA modeling skill (Phase C7) are the canonical examples. Prior art already exists: the `stpa-design-review` skill (4 gated phases, edge-by-edge confirmation) is the STPA questionnaire Orion adapts, and the same gated-elicitation pattern underlies the completeness gate. These are first-class skills in the registry, just with a human respondent instead of an artifact input.

**Extensions & packages (the ecosystem + self-evolution surface).** Following Pi: primitives(tools), skills, and workflows are distributable as **packages** installable from a registry (npm/git/OCI), so the community and the developer extend Orion — a new language generator, a new empirical-proof adapter, a new detector, a new tracker backend — **without forking core.** This is also where self-evolution becomes concrete: skills and prompts the harness refines (stored in LTM, see Memory) can be reloaded in place (the Pi `/reload` analogue), bounded by the trust-domain and proof guardrails. A package may ship any mix of the three unit types; the reliability tier and trust-domain rules constrain what an installed package is allowed to touch (e.g., a third-party package may add a generation skill but may not weaken the proof harness).

**Implication for issue breakdown:** each loop-map step becomes an issue tagged by unit type — primitive issues (Go pkg + tests), skill issues (definition + eval), workflow issues (composition + integration test) — plus packaging issues for the extension/registry mechanism. This makes the `/prd-to-issues` cut mechanical and keeps the trust-domain wall intact (generation skills and proof skills are never the same issue).

## Trust Domains and the Independence Invariant

> Added by adversarial review. All six reviewers flagged this as the load-bearing gap: the manifesto's "no agent grades its own homework" is asserted in prose but must be a **structural invariant**, not a comment. This section is normative and overrides any looser language elsewhere.

Orion has exactly two trust domains. Data crosses the boundary in one direction only.

- **Generation domain (untrusted):** every specialist agent that produces or modifies artifacts — code generators, the test-*author*, instrumentors. Everything it emits is an *artifact* or an *EvidenceClaim*, never a proof input.
- **Proof domain (trusted):** the Proof Harness (`proof/behavioral`, `proof/empirical`, `proof/hazard`), the held-out test corpus, the executable spec, and the Context Store. The verdict is computed **only** from evidence the proof domain collects itself.

**Invariants (each must be enforced by topology/types/process, and each has an acceptance test):**

1. **The behavioral test corpus is authored and held by the proof domain, from the executable spec — never by a generating fleet agent.** A dedicated harness-side `proof/test-synthesis` component authors tests from the Context Store spec. The generating agent never reads the corpus path. (Removes the `test-author` role from `agent-runtime`.) *Counters the manifesto's visible/held-out gap.*
2. **Agent-supplied `EvidenceClaim` is recorded as an untrusted claim and is never an input to the `Verdict`.** The Verdict is recomputed from harness-collected evidence (harness-run tests, Lookout probes, hazard checks).
3. **The Context Store and the held-out corpus live OUTSIDE the agent sandbox and are unreachable from inside it.** An agent that can read the spec's hidden tests or rewrite the spec defeats the whole design.
4. **The Lookout (empirical) probe runs in an environment isolated from the generating agent** — separate process namespace, read-only artifact mount — and receives only the probe contract (ports, paths, expected responses), never how the artifact was built.
5. **Adjudication is separated from dispatch.** The verdict adjudicator (`truth-align`) is not a dependency the dispatcher can influence; the Conductor *invokes* proof and acts on the result, it does not produce it.
6. **The confidence/drift/degradation signals that trip safety controls are harness-derived, never agent-self-reported** (mutation-score delta, empirical pass-rate, hazard-control delta, attempt-to-verdict ratio) — a gamed agent reporting high confidence must not be able to hold the circuit breaker open.
7. **Memory is trust-partitioned; the proof domain reads the spec directly.** Every memory item carries a trust domain; `memory.Retrieve` for a proof-domain consumer (`proof/test-synthesis`, `proof/hazard`, `truth-align`) returns only proof-domain or human-decision items — a generation-domain "learning" can never enter a proof prompt. The Proof Harness reads the executable spec / `ProofObligation` **directly from the anchor-verified Context Store**, never from a `context-engine`/`memory`-assembled bundle. (Closes the memory→proof puncture introduced by the new memory layer.)
8. **Self-evolution may never touch the proof domain.** Proof-domain skills/prompts are immutable at runtime (signed, pinned, supply-chain-loaded). Only generation-domain skills are self-evolvable, and only through the re-sign + re-validate lifecycle (see Memory & Self-Evolution Hardening). Self-evolution defaults **off**.
9. **The proof domain is not third-party-extensible.** Installed packages may register generation-domain skills/primitives and observability surfaces only; registering anything in the proof domain (test-synthesis, empirical, hazard, truth-align, held-out corpus) or re-wiring Phase E proof routing is rejected at load unless signed by the Orion core key.

**ADR to write:** "Generation and Proof are separate trust domains; data flows generation→proof as artifacts only, never as proof inputs."

## Harness Reliability Requirements

> Added by adversarial review (reliability + performance). Orion is itself an agentic workflow and inherits the manifesto's failure modes; these close them at the harness layer. Each is a contract the implementation is held to, not an intention.

- **Every external call (LLM provider, Polaris) MUST have:** a per-call timeout (`context.WithTimeout`), exponential backoff with full jitter honoring `Retry-After`, and a circuit breaker. A hung LLM call must never block the TUI indefinitely.
- **Every specialist step MUST have a wall-clock deadline** (tier-derived) and a `Cancel(ctx)` path on `AgentHandle`. The Conductor enforces it; a hung agent is timed out, cancelled, and rescheduled.
- **The Context Store write path MUST be crash-safe and transactional.** The sequence `(task_attempt, artifact reference, side-effect record, proof link)` commits as one atomic unit; the embedded store runs in write-ahead-journal mode. `task_attempt` rows carry an **idempotency key** so a restarted agent detects and skips an already-committed side effect. The resumability claim (Story 31 / acceptance) is only valid if the kill is tested *mid-write*, not just mid-compute.
- **Orion MUST install signal handlers** (SIGINT/SIGTERM) that cancel all in-flight agent contexts and reap sandbox subprocesses before exit. Lookout-started subprocesses run under an Orion-owned process group so orphans are killed on crash. `Conductor.Interrupt()` triggers the same rollback path as the reversibility gate; abort is transactional w.r.t. any in-flight gated op.
- **Polaris is optional at runtime.** The loop reads reliability context only from the local `polaris_context` cache; `polaris-connector` is a one-way importer with a freshness timestamp + TTL. If Polaris is unreachable, the loop proceeds on cached/empty context and **visibly flags reduced reliability context** on the delivery envelope (never silently lowers the bar). Add `polaris.offline_mode`.
- **Mutation/proof verdicts MUST account for flakiness.** Behavioral verdict carries a `run_count`/variance; high-variance results map to `Inconclusive`, not a coin-flip `Accept`/`Reject`.
- **Cross-task contamination:** when a downstream task's proof implicates an upstream artifact, the Conductor flags the upstream proof for re-examination before retrying. `task_attempt` records which upstream artifacts it consumed.
- **Orion instruments itself** (the dogfood/3 a.m. test applied recursively): structured log per Conductor state transition, a trace span per A2A dispatch/response, a metric per proof verdict. A completed run produces a machine-readable audit trail.

## Security Requirements

> Added by adversarial review (security). The verification design assumes adversarial agents; that assumption MUST extend to the execution and memory substrate where untrusted code actually runs.

- **Sandbox isolation is a pluggable backend with a stable interface.** Three implementations selectable via `sandbox.isolation`: **gVisor/runsc (default)**, container (Docker/Podman), and microVM (Firecracker) — so Orion serves the broadest user base (laptop to cloud) without changing the proof path. gVisor is the default because its userspace syscall interception *also* powers reversibility-by-interception (below) on a typical WSL2/Linux laptop without nested-virt. Every backend MUST meet the same contract: scoped workdir only (no host FS mount beyond it), default-deny network egress with an allowlist limited to the resolved dependency registries, no ambient cloud/SSH credentials, dropped capabilities. (Reinforces Trust-Domain invariant 3.)
- **Reversibility gates enforce at the boundary by interception, not by agent declaration.** Destructive ops (external egress, persistent-storage writes outside the workdir) are detected from *observed* syscalls/network calls and paused for approval regardless of whether the agent declared them. The adversarial-agent assumption forbids trusting a self-reported side-effect list. Approval prompts show the *intercepted concrete operation*, not the agent's description of it.
- **Context Store carries provenance + trust tier on every row** (`human-decision | agent-generated | polaris | ingested-repo | ingested-doc`). At bundle assembly, untrusted-origin content is rendered as **inert, delimited reference data — never as instructions**. Only the human-approved spec and human decisions are instruction-trust. Spec + decisions are hashed at approval; `Recall` verifies the anchor is unmutated. Embedded store is encrypted at rest with `0600` perms.
- **Secret handling is a proof gate.** Secret-scanning of generated artifacts blocks the deployment bar (hardcoded credentials → fail). A redaction filter on the Context Store write path rejects/tokenizes secrets before persistence (matters acutely for brownfield/doc ingestion).
- **Both Polaris directions are untrusted boundaries.** Consume path: validate response against a strict schema, reject instruction-like free-form fields, render as inert data, verify server identity (mTLS/signed). Write-back (V2.3): a minimized `FailureMode` schema carrying only abstracted pattern signatures (class, control category, hazard type) — **no raw code, no file paths, no spec text** — behind a local redaction + per-session developer-confirm gate (honors the draft-then-confirm guardrail; default off).
- **Dependency provenance verifies more than existence.** Existence ≠ safe: a pre-registered slopsquat *exists*. `provenance` checks first-publish age, popularity/namespace ownership, and lockfile checksum pinning. Acceptance includes a typosquat that exists-but-is-anomalous and asserts rejection.
- **Agent definitions are a signed, version-pinned supply chain.** `Registry.Spawn` loads role definitions + tool-grant manifests from a trusted, integrity-checked source the running loop cannot write (never the Context Store). Least-privilege tool grants per role (a test-author gets no network/FS-write). Startup asserts the loaded set matches a pinned manifest hash.
- **TUI render path sanitizes control/escape sequences** from any agent- or ingestion-sourced text (terminal-injection defense).
- **Polaris credential storage (round-2).** The platform token is stored in the OS keychain/credential manager where available, else encrypted-at-rest `0600` — **never in the Context Store** (agents/recall touch it) and **never reachable from inside the sandbox** (extends invariant 3 to the credential). Prefer short-lived tokens; write-credential scope is capability- and tier-bounded (a `throwaway` project holds no evidence-submit credential); `logout` erases the cached credential.
- **Spec-capture redaction (round-2).** The deterministic secret/credential redactor (already on the Context-Store write path) MUST also run on the **grill-loop spec/decision capture path** — the observability and on-call dimensions invite pasting PagerDuty/Datadog keys. Capture integrations **by reference, not value** ("alerts via PagerDuty, key in env `PAGERDUTY_KEY`"); secret-shaped values are tokenized *before* pinning, so the every-step re-injection of pinned decisions carries a token, never a live secret.

## Resource & Cost Governance

> Added by adversarial review (performance). Orion's scaling axis is "many agents and expensive proofs on one machine against a token budget" — these gates keep it from bankrupting the API quota, melting the laptop, or stalling for hours.

- **Budget accounting (warn-by-default; ceilings opt-in).** A `budget` accountant ALWAYS tracks and surfaces cumulative tokens + dollar-cost + wall-clock live in the TUI (all agents, retries, proof passes, drift checks) — visibility is never optional. **Hard ceilings are opt-in and unset by default** (`budget.run_token_ceiling`, `budget.run_dollar_ceiling`, `budget.task_iteration_max`). When a ceiling *is* configured, the loop escalates at ~80% *before* exhaustion and treats "out of budget" as a first-class terminal state with a human-mergeable fallback. With no ceiling set (the default), the loop warns but never halts on spend. *(Decision: the developer owns the spend/safety trade-off; Orion makes spend impossible to miss but does not impose a cap out of the box.)*
- **Bounded concurrency.** `agent-runtime` owns a worker pool / dispatch semaphore: `concurrency.max_agents`, `concurrency.max_sandboxes`, `concurrency.max_inflight_llm`. The Conductor dispatches into a ready-queue drained at the configured parallelism; saturated → ready tasks wait (backpressure). Fleet status shows queued vs. active. Shared rate limiter ties into LLM-retry backoff.
- **Mutation testing is scoped and cached.** Mutate the changed diff only (not the whole tree), with `proof.mutation.timeout_per_mutant`, `proof.mutation.max_mutants` (sampling above a threshold), and tier-calibrated thresholds (`throwaway` samples/skips; `critical` runs full diff). Results cached by artifact hash so retries don't re-mutate unchanged code. Time budget surfaced in the operating envelope.
- **Warm sandbox pool.** Pre-pulled base images, `sandbox.warm_pool_size`, sandbox reuse across a task's retries, artifact-layer caching by hash. `sandbox.startup_timeout`; startup latency shown in Fleet status.
- **Bounded, windowed context recall.** Push the budget into the query: recall fetches the K most-relevant decisions, immediate-ancestor outputs (summarize deeper ancestry), and the latest proof verdict per ancestor — via a batched ancestor fetch (no N+1). "Relevant decision-log slice" has a defined cap. Indices: `tasks(project_id,status)`, `task_deps`, `proofs(task_id)`. Acceptance benchmark: recall latency + bundle size stay ~flat as the DAG grows to N=1000.
- **Cheap-first drift detection.** A low-cost embedding/heuristic similarity runs per step; the expensive LLM re-anchor fires only on threshold breach or fixed cadence (`drift.check_every_n_steps`). Spec embedding cached once per run; drift/re-anchor tokens count against the run budget.
- **TUI liveness contract.** Every long op (especially proof) emits incremental progress events (per-mode start/finish, mutation %, Lookout boot/probe phases) with a heartbeat on the internal event channel; no pane goes >N seconds without an update.
- **Polaris context cached + relevance-filtered.** Local cache with `polaris.cache_ttl`, incremental sync (ETag/`updated_since`), and per-task injection filtered to the task's risk dimensions / tier — not the whole catalog in every prompt.
- **Embedded store concurrency.** If SQLite (the chosen engine — see Core Data Model Hardening): WAL mode + busy-timeout + a single serialized writer goroutine (agents enqueue writes); reads stay concurrent. The concurrency contract is part of the store interface.

## Core Data Model Hardening

> Added by adversarial review (data flow + architecture). The Context Store is the source of truth; these resolve under-specified entities and the proof decision logic before component specs are cut.

- **Backing engine is SQLite (WAL), decided now — not deferred.** A plain KV cannot serve the DAG/recursive queries or enforce the `done`-gate as a constraint; the "interface stable, engine deferred" claim was false. Replace the `Put/Get/Recall` facade with per-aggregate repositories (`SpecStore`, `TaskGraph`, `ProofStore`, …) sharing a transaction boundary; `Recall` is a read-model over them.
- **`ContextBundle` is a concrete, schematized type** (named fields, each tagged with its source entity and budget contribution), since it is load-bearing for resumability and is the spec source for proof. No prose-only definition.
- **Rename the two `VerificationContract` meanings:** `ProofObligation` (what a task owes; harness-owned; read-only to the agent; travels Conductor→harness) vs. `EvidenceClaim` (untrusted agent self-report; travels agent→Conductor). The A2A payload carries the `ProofObligation`; the harness computes the `Verdict` independently.
- **`Verdict` keeps per-mode provenance.** Define `Converge(behavioral, empirical, hazard) → {Accept | Reject | Inconclusive, dissenting_modes}` as an explicit, testable function (not a bare `converged: bool`), with the `Inconclusive` mapping spelled out. Reconcile with the Decision Matrix (per-mode decision, then aggregate). `proofs` rows carry quantitative metrics: `{mutation_score, empirical_pass_rate, hazard_controlled_count, hazard_total_count, run_count}` so degradation is computable.
- **Spec versioning replaces naïve "immutability."** Add `version` + `parent_spec_id` + a `revised` status. Re-anchoring stays read-only (compare-only); a developer interrupt (Story 6) mints a new spec version requiring re-approval and marks in-flight tasks `blocked` pending re-evaluation. The *accepted version* is immutable; the spec lineage evolves.
- **Tracker projection is one-way (store→tracker) in V2.1**, with a `tracker_sync` sub-schema (`entity_id, tracker_external_id, last_synced_at, conflict_policy=store-wins-with-alert`). Out-of-band human edits to the tracker are overwritten with a visible warning; bidirectional reconciliation is a named future phase. Entities carry `updated_at`/`version` for staleness.
- **`failure_modes` gets a `canonical_key`** (deterministic hash over `{category, component_type, symptom_class}`) + a `Matches(other)` contract, so Story 30 ("never silently repeat a known failure") is implementable against both local rows and Polaris-seeded context.
- **Add an `artifacts` entity** (`{task_id, artifact_type, storage_path, content_hash, created_at}`) with a producer step in Trace 3 — "ancestor task outputs" in the context bundle currently has no persistent producer.
- **`tasks` gains `proof_id` FK → `proofs`;** the transition to `proven` is a store-enforced constraint requiring a non-null `proof_id` with `verdict=Accept`. The `done`-gate is a DB constraint, not just orchestrator code.
- **Operating envelope is schematized and displayed:** `{proven_load, fault_classes_controlled, assumptions, tier}`, rendered in the `:proof`/`:deliver` panes and included in the delivery artifact/runbook (Stories 26–27).
- **Hazard mode models the STPA control structure, not just a UCA list — and the model is developed with the developer, not auto-consumed.** Polaris supplies the STPA **primitives, persistence, and reasonable-default loss scenarios** (`control-structure/*`, `ucas`, `loss-scenarios`, `defense-layers`); it does **not** hand back a finished model. Developing the system's actual control structure, UCAs, and refined loss scenarios is **developer-involved modeling** (HYB) driven by a **directed STPA questionnaire skill** — adapted from the existing `stpa-design-review` skill, which already implements the four gated phases (1: define losses · 2: model the control structure, confirming each controller→action→feedback edge individually · 3: identify unsafe control actions via the four STPA questions — not-provided / provided-incorrectly / wrong-timing / wrong-duration · 4: trace loss scenarios). Orion reads Polaris's defaults as a starting point, runs the questionnaire, and uses the context the developer gives (tied to the Phase A spec dimensions: security, data, dependencies, scale). The **developer ratifies** at each gate (much like ratifying the executable spec), and Orion persists the developed artifacts back to Polaris (shared, versioned). The questionnaire's "every control action must have a feedback path" completeness check is exactly what makes control actions and feedback loops testable in proof. The ratified, Polaris-persisted UCAs/control structure are the trusted source — never a generation-agent's self-assertion (this is what "UCA provenance from the ProofObligation / Polaris risk register, not the generator" means). The modeled **control actions and feedback loops then become first-class observability surfaces and test targets**: each control action gets a probe/assertion (issued when it should be, suppressed when it shouldn't, observable in telemetry), and each control loop gets a validation that its feedback actually closes. So hazard proof yields both "are UCAs controlled?" *and* executable control-loop tests the empirical mode runs. See [Orion-Polaris-API-Contract §4](../SPEC/Orion-Polaris-API-Contract.md).
- **Decomposition coverage check:** after decomposition, assert every behavioral requirement in the spec maps to ≥1 `ProofObligation` clause in the DAG; gate plan approval on it (closes the "Decomposer narrows the proof surface" leak). Define minimal fields for `decisions` and `escalations` (currently fieldless).
- **Completeness gate is deterministic-first:** a rules-based required-decisions checklist per project type (unit-testable without an LLM) + an optional LLM enrichment pass replayed from recorded fixtures (cassettes) — so the golden test is meaningful, not flaky.

## Memory & Self-Evolution Hardening

> Round-2 review: memory and self-evolution were added without the crash-safety, schema, and trust contracts Round-1 gave the Context Store. These extend those contracts. Default posture: **`self_evolution.enabled = false`** until V2.3 field-validation.

- **Single assembler.** `context-engine` is the **sole** assembler of the `ContextBundle`/`PromptContext`: it calls `context-store.LoadFacts(task_id)` (facts) + `memory.Retrieve(query, tiers)` (cognition), applies pins + budget, emits the bundle. `context-store` no longer returns a `ContextBundle` (drop it from that API). Per Trust invariant 7, **proof reads spec/`ProofObligation` directly from the Context Store**, never from this bundle.
- **Memory store = Context Store's contracts, separate instance.** MTM/LTM persist in a separate embedded store (facts vs. cognition) that inherits every Context-Store contract: WAL/transactional writes, encryption at rest + `0600`, and a `provenance`/trust-tier on every item. STM is in-memory only (rebuilt from the store on restart). **Engine: SQLite with a vector extension (e.g., `sqlite-vec`)** for the ANN retrieval that C4/C6 need; `pglite` is an acceptable alternative if SQLite+vec proves unsuitable. Separate DB file from the Context Store. C4/C6 retrieval is a bounded top-K vector search (this is the mechanism behind the LTM flat-latency benchmark).
- **Memory schema:** `memory_items(id, tier[STM|MTM|LTM], project_id, developer_id, kind[page|summary|pattern|procedure|skill_refinement], content/summary, content_hash, source_ref{entity,id,source_version}, heat{recency,frequency,relevance}, pinned bool, trust_tier, version, prior_version_id, active bool, created_at, last_accessed_at)`; `memory_evolution_log(id, memory_item_id, promotion_id, run_id, action[write|summarize|promote|evict|rollback], prior_hash, new_hash, created_at)`.
- **Crash-safe eviction (two-phase).** Summarize-then-evict is 2PC: (1) write+flush summary to the store, (2) only then drop the raw page; idempotent (upsert by `content_hash`). A summarization failure leaves the raw page intact — "nothing relevant is hard-dropped" becomes enforceable. *(Test: `TestMTMEvictionCrashSafeNoLoss`.)*
- **Pins are categorical, not heat.** `pinned=true` items (spec + critical/`security_relevant` decisions) are excluded from the evict candidate set regardless of heat score. *(Test: `TestPinnedSpecItemNeverEvicted`.)*
- **Divergence reconciliation.** Items link `source_ref.source_version`; `Retrieve` skips/refreshes items stale vs. the live Context-Store row (live facts win); a startup + periodic sweep quarantines items whose source fact was rolled back. *(Test: `TestMemoryStoreContextStoreDivergenceDetected`.)*
- **Summarization cost control.** Runs **async/off the step critical path** (background worker on the shared dispatch semaphore), cheap-first (extractive default; LLM only above an importance threshold), heat hysteresis (anti-thrash), per-run `memory.summarize_max_calls`; tokens charged to the budget accountant.
- **LTM growth control.** Flat-latency retrieval benchmark as LTM grows to 10k items (symmetric with recall's N=1000 benchmark); compaction/GC: dedup via the existing `canonical_key`/`Matches`, decay-evict never-retrieved items, supersede old skill-refinement versions; per-step retrieval is top-K with a budget contribution.
- **Security constraints survive summarization.** Items flagged `security_relevant` (constraints, controls, hazards, PII tags) are pin-eligible and retained as structured non-lossy records, never free-text-summarized away; a summarization pass that drops a `security_relevant` item is rejected. *(Test: `TestSummarizationPreservesSecurityConstraints`.)*

**Self-evolution lifecycle (generation domain only; default off):**
- Proof-domain skills are **never** self-evolved (Trust invariant 8). A generation skill refinement enters LTM as a **candidate** (`active=false`).
- Promotion to `active=true` requires: re-sign + re-hash + tool-grant re-validation (grants ⊆ role ceiling), pass its **SkillEval regression gate** (score ≥ baseline·(1−tolerance)), and a quarantine/`pending_review` shadow period for the first N uses. `Registry.Spawn` loads only `active=true` rows.
- **Kill-switch:** `self_evolution.enabled=false` disables C6 evolved-skill retrieval + G4 promotion without disabling base memory.
- **Canary:** record proof pass-rate + mutation score for the N runs before/after each promotion; "performance degraded since promotion P" is an escalation.
- **Rollback:** each promotion has a `promotion_id` + inverse `rollback_op`; `orion rollback evolution <promotion_id>` un-applies it and flags downstream deliveries that used it.
- **Cross-project leakage:** LTM is project-scoped by default; developer-scoped (cross-project) items are opt-in per item and must pass the deterministic secret/identifier redaction (Context-Store write-path filter) before crossing a boundary — reuse the minimized-signature approach (no raw code/paths/spec text).
- *(Tests: `TestSelfEvolutionRegressionGate`, `TestEvolvedSkillCannotCrossProofTrustDomain`, `TestLTMEvolutionRollback`, `TestLTMPromotionCrashSafeNoCorruption`, `TestDeveloperScopedLTMRedactsProjectLiterals`.)*

## Extension & Package Security

> Round-2 review: the Composition Model's installable packages (npm/git/OCI) are a code-execution supply chain needing the same rigor as agent definitions.

- **Signed capability manifest.** Every package ships a signed manifest declaring unit types, the **trust domain** of each skill, requested tool grants, and which loop phases/workflows it touches. Install verifies signature + provenance by **reusing `dependency-provenance`** (first-publish age, namespace ownership, checksum pinning) — Orion's own extension registry gets the same slopsquatting defense as build deps.
- **Proof domain is not third-party-extensible** (Trust invariant 9): a manifest declaring a proof-domain unit, or a workflow that re-wires Phase E proof routing, is rejected at load unless signed by the Orion core key.
- **Grant lattice.** Effective grants = intersection(package-requested, role ceiling, tier policy). Network/FS-write for generation skills default-deny + per-project approval. *(Test: `TestInstalledSkillGrantsCappedToRoleCeiling`.)*
- **Extension lockfile.** Checked-in lockfile (name, version, content hash, signature); startup asserts the installed set matches it (extends the agent-definition pinned-hash assertion to packages); updates require re-confirmation. *(Test: `TestThirdPartyPackageCannotRegisterProofDomainSkill`.)*
- **Third-party in-process primitives** run under their least-privilege manifest grant and the same reversibility interception as agents; out-of-scope egress/writes pause for approval. Per-install confirmation shows requested capabilities (draft-then-confirm).
- **SkillEval contract** (blocks self-evolution's regression gate): a `SkillEval` asserts deterministically — (1) cassette happy-path → deterministic predicate; (2) adversarial input → adversarial predicate (e.g., underspecified intent → `open_decisions>0`); (3) latency SLO. The harness **rejects LLM-as-judge predicates** (else the regression gate is gameable). *(Tests: `TestSkillEval_HappyPath`, `TestSkillEval_AdversarialInput`, `TestEvalHarnessRejectsNonDeterministicPredicate`.)*
- **Taxonomy scope clarification.** Primitive/Skill/Workflow classifies **loop-map steps and extension units**, not infrastructure — `orchestrator`, `tui`, `cmd/orion`, `reliability-tier` are engine/shell modules outside the scheme; cross-cutting concerns (budget, liveness, self-instrumentation, signal cleanup, transactional writes) are cut as primitive + invariant-test issues independent of the step table.

## Phase E2 Reliability & Recovery

> Round-2 review: the integration layer is under-specified where distributed state is hardest. **`integration` (not the Conductor) owns all post-proof git-tree mutation and the integration state machine**; the Conductor enqueues and reacts. Re-label E2.3/E2.6/E2.8 owner = `integration`; E2.7 re-proof runs in the **proof** domain.

- **Pre-merge checkpoint.** Before E2.6, atomically persist `merge_checkpoint{task_id, pre_merge_head_sha, acquired_at}`; rollback (E2.8 red) and crash recovery reset to this SHA. *(Tests: `TestPostMergeProofRedCausesRollback`, `TestMergeAttemptWriteIsAtomicWithRollback`.)*
- **Lock heartbeat + stale recovery.** The singleton integration lock carries `holder_id`, `acquired_at`, `heartbeat_at`, `pre_merge_head_sha`; the holder heartbeats during E2.3–E2.7. On startup a stale lock (heartbeat expired) triggers automatic rollback to `pre_merge_head_sha`, lock release, task → coding loop — no permanent deadlock. *(Test: `TestIntegrationLockStaleLockRecoveryOnRestart`.)*
- **Worktree baseline reset on rollback.** Rollback resets the worktree to the pre-lease baseline (not the merged-bad state) before releasing the lease. *(Test: `TestWorktreeBaselineRestoredAfterRollback`.)*
- **Separate re-proof ceiling.** Integration re-proof attempts are tracked separately from coding-loop iterations (`integration.max_reproof_attempts`); N consecutive `Inconclusive` re-proofs escalate with the flaky mode named rather than cycling forever; empirical re-probe uses a fresh ephemeral port each attempt.
- **Lease deadlock prevention.** Multi-path leases acquire in canonical (sorted) order; leases carry TTL + heartbeat (crashed holder's lease expires); over-broad declared scope releases its untouched portion early (verified against touched paths); acquisition timeout returns the task to the ready queue. *(Tests: `TestLeaseBlocksOverlappingScope`, `TestLeaseManagerNoDeadlockUnderContestedScopes`, `TestSerializedQueuePreventsParallelMerge`.)*
- **Resolved merges are untrusted artifacts.** The conflict resolver is a generation-domain agent; its output is re-proven, and the merge's `ProofObligation` is the **union** of both original tasks' obligations (no dropping one side to make tests pass). *(Test: `TestResolvedMergeProofCoversAllOriginalObligations`.)*
- **Re-proof scoped + batched (performance).** Post-merge re-proof is scoped to the merge delta + its reverse-dependency closure (unchanged artifacts reuse the isolation result via the artifact-hash mutation cache); integration may batch K proven tasks onto head and re-prove once, falling back to one-at-a-time on conflict. Per-Epic integration cost appears in the operating envelope.
- **Prefer fix-forward over binary rollback for multi-change integration (the "Intervening-PR Problem").** Rolling back to last-known-good can unwind concurrent fixes/security patches. For an isolated change, hard rollback (above) stands; when intervening changes exist, prefer **feature-flag disable + targeted fix-forward** over reverting the integration head. *(Google SRE corroboration; V2.1.)*

## SRE-Derived Refinements (Google SRE corroboration)

> Added 2026-06-18 from a synthesis of two Google SRE publications on agentic AI for reliable operations (full mapping: [docs/research/Google-SRE-AI-practices-2026-06-18.md](../research/Google-SRE-AI-practices-2026-06-18.md)). Google independently arrives at Orion's load-bearing principles (independent harnesses, deterministic scoring, oversight-on-intent, caller-agnostic safety boundaries). These refinements adopt the concrete practices that go beyond what the PRD already specified; each amends the named section.

- **Mandatory dry-run on every mutating tool** *(amends Security Requirements + `worktree`/`integration`/`sandbox`; V2.0–V2.1).* Any tool/MCP that mutates state MUST support `dry_run=true`, returning the predicted effect + blast radius without mutating. The reversibility gate uses dry-run to predict before it intercepts; the deterministic actuation path is caller-agnostic ("doesn't care if the caller is an agent or a human"). Requires a **tool-effects catalog** (each mutating tool's effect + blast radius) — a tracked gap.
- **Red Button — global pause/kill of autonomy** *(amends Harness Reliability Requirements; V2.0).* Beyond the per-step circuit breaker and signal cleanup, expose an emergency control to **instantly pause all in-flight agentic actions, block new actions, and globally revoke autonomous (auto-deliver) permissions**. The reasoning side (Conductor/agents) is decoupled from the actuation side (`delivery`/`worktree`/`integration`) so the kill is enforced at the deterministic gate, not requested of the agents.
- **Hybrid eval: LLM-judge for reasoning, deterministic for the verdict** *(amends the SkillEval contract in Extension & Package Security).* An LLM rater MAY grade qualitative trajectory/reasoning; the **final pass/fail is always deterministic** against golden expectations. LLM-as-judge may never set the gate verdict.
- **Bronze / Silver / Gold golden data + True-vs-Observed precision** *(amends SkillEval + Memory & Self-Evolution Hardening).* Evals run against tiered data — Gold (human-verified), Silver (calibrated against Gold to a quality threshold), Bronze (heuristic). Stratified sampling keeps Silver calibrated; skill-evolution promotion requires a statistically-significant margin on Gold, not raw accuracy on Bronze.
- **Frictionless golden-data capture from the human's workflow** *(amends Memory & Self-Evolution Hardening; self-evolution lifecycle).* Every developer ratification — accepting/amending/rejecting a spec decision, an STPA artifact, or a proposed fix — is captured as a **Gold label** for that skill at zero extra effort, feeding self-evolution + memory. (Default-off self-evolution still gates promotion.)
- **Autonomy Levels L0–L4 (earned ladder)** *(amends "deployment bar and earned autonomy" + V2.3 phasing).* Replace the binary autonomy switch with a maturity ladder per function: **L0 Manual · L1 Assisted · L2 Partial (human approval) · L3 High (bounded auto, human notified) · L4 Full (multi-step)**. A project/tier advances only by demonstrating a statistically-significant success rate against Gold data. V2.0–V2.1 operate at L0–L2 (human-merge); V2.3 introduces L3 for low-risk classes.
- **Dynamic, per-action risk evaluation** *(amends `reliability-tier` + `delivery`; V2.3).* The reliability tier is *static*; add a **real-time risk check at the actuation/delivery gate** considering error budget, concurrent in-flight changes, active incidents, and time-of-day. Elevated risk **auto-downgrades autonomy** (e.g., L3→L2) and routes to a human — autonomy is contextual, not just tiered.

## Testing Decisions

### Test philosophy

Test **external behavior**, not implementation. Every deep module has observable outputs that can be asserted without poking internals: the completeness gate emits a deterministic `OpenDecision` set for a given intent; the decomposer emits a DAG; the proof harness emits a `Verdict`; the context store's `Recall` emits a `ContextBundle`. Orion holds itself to its own bar: validation is independent of generation, and we measure our tests by **mutation score**, not coverage. Prior art: the existing `internal/architect` golden-file tests and `internal/verify` verdict unit tests in the archived V1 tree are reusable patterns.

### Modules with priority coverage

1. **`orchestrator/completeness`** — golden tests: given intent X for project type Y, the `OpenDecision` set matches expectation; a fully-specified intent yields zero open decisions.
2. **`decomposer`** — given an accepted spec, the Epic's Tasks cover the spec and every Task carries a `ProofObligation`.
3. **`proof`** — convergence logic: a verdict is `Accept` only when all three modes pass; any single failing/missing mode yields `Reject`/`Inconclusive`. Mutation-score gate behaves (high coverage + low mutation score does *not* pass behavioral).
4. **`context-store`** — `Recall(task_id)` reconstructs a bundle sufficient to resume a task after simulated agent restart (no in-memory dependence).
5. **`context-engine`** — drift detection flags a trajectory that diverges from the spec; budgeting preserves intent-anchoring content under a tight window.
6. **`dependency-provenance`** — a hallucinated package name is rejected; a real one resolves.
7. **`delivery`** — deployment-bar decision matches the tier policy; bar-not-met routes to escalation, never silently ships.

### Integration Acceptance Test

**Orion V2.0 acceptance test (FIRST issue created, LAST issue closed):**

1. Launch the Orion TUI in an empty directory.
2. In Conversation, state an intent with one deliberate ambiguity, e.g.: *"Build an HTTP service that returns the current time."* (ambiguous: format? timezone? port? endpoint path?).
3. **Verify:** the completeness gate surfaces the specific open decisions (at minimum: response format, timezone, port/route) and asks about them — it does **not** silently guess.
4. Answer the questions; **verify** an executable spec is produced and shown for approval, and approval is recorded.
5. **Verify:** a task DAG is generated and rendered in the Plan view; each task carries a verification contract.
6. **Verify:** specialist agents generate the Go service, its tests, and instrumentation, coordinated by the Conductor; Fleet status reflects activity.
7. **Verify (behavioral):** the harness runs independent tests and reports a **mutation score**, not just coverage; a deliberately tautological assertion injected into the suite is flagged as not fault-catching.
8. **Verify (empirical):** a Lookout probe starts the built service and confirms it listens on the chosen port and returns the spec-conformant response to a real request.
9. **Verify (hazard):** the proof report names the unsafe control actions considered (e.g., unbounded request handling) and shows they're controlled (e.g., timeouts/limits present).
10. **Verify:** the task DAG cannot reach `done` until all three proof modes converge; an artifact with passing tests but a failing empirical probe is **not** marked done.
11. **Verify:** the deliverable includes structured logs, a metric/trace surface, and a generated runbook (the 3 a.m. test).
12. **Verify:** every dependency added by the build is provenance-checked; a planted hallucinated dependency name is rejected.
13. **Verify:** the final delivery is a proven, runnable Go service with a published operating envelope; the Context Store records spec, decisions, DAG, proofs, and delivery.
14. **Verify (resumability):** kill an in-flight specialist agent; on retry, the replacement reconstructs context via `Recall` and completes the task without re-asking the developer.

This pins end-to-end correctness of the V2.0 loop — intent → completeness → decomposition → coordinated generation → multi-modal proof → operable delivery — on a known greenfield path, and gates the V2.0 release.

### Shell-Verifiable Acceptance Criteria (V2.0)

> Added by adversarial review. The narrative test above is choreography; these are the runnable predicates a CI/`/build` gate checks. The `orion …` subcommands referenced here *define the CLI surface V2.0 must expose* (non-interactive control of the TUI loop). Every box is a command that exits 0 (or a named Go test that passes). The test harness MUST isolate state: `export ORION_DATA_DIR=$(mktemp -d) && orion init --dir $ORION_DATA_DIR`.

```markdown
# Intent completeness gate (no silent guessing)
- [ ] echo "Build an HTTP service that returns the current time." | orion submit --non-interactive \
      | jq -e '.open_decisions|map(.key)|contains(["response_format","timezone","port","route"])'
- [ ] (after answering) orion spec show --json | jq -e '.status=="accepted" and (.open_decisions|length==0)'

# Decomposition + coverage
- [ ] orion plan show --json | jq -e '.tasks|length>0 and ([.tasks[]|select(.proof_obligation==null)]|length==0)'
- [ ] go test ./decomposer/... -run TestEverySpecRequirementHasProofObligation   # coverage check

# Trust-domain independence (the credibility hinge)
- [ ] go test ./proof/... -run TestHarnessIsolation            # generating agent cannot read corpus/spec/store
- [ ] go test ./proof/... -run TestKnownFaultyArtifactIsRejected   # canary: planted defect ⇒ Reject
- [ ] go test ./proof/behavioral/... -run TestMutationGateRejectsTautology  # tautological test ⇒ not fault-catching

# Multi-modal proof converges; done-gate is real
- [ ] go test ./internal/conductor/... -run TestStateMachineRequiresAllThreeModes
- [ ] orion task show <id> --json | jq -e '.status!="done"'   # while empirical probe = Reject
- [ ] orion proof show --task <id> --mode empirical --json | jq -e '.port_open and .response_contract_satisfied'
- [ ] orion proof show --task <id> --mode hazard --json | jq -e '(.ucas_considered|length>0) and (.uncontrolled_ucas|length==0)'
- [ ] orion proof show --task <id> --mode hazard --json | jq -e '(.control_actions|length>0) and ([.control_actions[]|select(.test==null)]|length==0)'  # every control action has a test
- [ ] go test ./proof/hazard/... -run TestControlLoopFeedbackValidated   # modeled feedback loops actually close

# Operability (3 a.m. test)
- [ ] orion deliver show --runbook --json \
      | jq -e '.sections|keys|contains(["incident_response","escalation_path","known_failure_modes","operational_commands"])'
- [ ] orion deliver show --json | jq -e '.operating_envelope!=null'

# Security gates
- [ ] orion deps verify github.com/nonexistent-org/nonexistent-pkg-xyzzy-42 ; test $? -ne 0   # hallucinated dep rejected
- [ ] go test ./dependency-provenance/... -run TestPreRegisteredTyposquatRejected             # exists-but-anomalous
- [ ] go test ./proof/... -run TestHardcodedSecretBlocksDeliveryBar
- [ ] go test ./context-engine/... -run TestInjectedInstructionRenderedInert                  # memory-poisoning defense

# Harness reliability
- [ ] go test ./context-store/... -run TestRecallRebuildsContextAfterAgentKill    # kill mid-WRITE, resumes, +1 attempt, 0 new decisions
- [ ] go test ./...           -run TestLLMCallHasTimeoutAndCircuitBreaker
- [ ] go test ./...           -run TestSpendIsSurfacedLiveInTUI                  # accounting always on
- [ ] go test ./...           -run TestRunHaltsAndEscalatesWhenCeilingConfigured  # only when an opt-in ceiling is set
- [ ] go test ./polaris-connector/... -run TestLoopProceedsWhenPolarisUnreachable  # flags reduced context, does not block

# Determinism of the completeness gate
- [ ] go test ./orchestrator/completeness/... -run TestRequiredDecisionsChecklist  # rules-based, no live LLM
```

Each `orion` subcommand above is itself a V2.0 implementation task (the non-interactive CLI surface). A criterion that cannot be expressed as a command or named test signals an incomplete design, not acceptable prose.

### Shell-Verifiable Acceptance Criteria (Round-2 additions)

> Additive to the V2.0 block above; cover the memory, self-evolution, spec-dimension, integration, and package surfaces added since Round 1.

```markdown
# Memory & context-erosion
- [ ] go test ./memory/... -run TestAntiErosionPinsNeverEvictedUnderPressure
- [ ] go test ./memory/... -run TestSummarizeBeforeEvict           # no hard-drop; summary committed before raw removed
- [ ] go test ./memory/... -run TestMTMEvictionCrashSafeNoLoss     # kill mid-summarize → raw page intact
- [ ] go test ./memory/... -run TestHeatPromotionMTMToLTM
- [ ] go test ./context-engine/... -run TestConstraintHonored50StepsLater
- [ ] go test ./memory/... -run TestPinnedSpecItemNeverEvicted
- [ ] go test ./memory/... -run TestSummarizationPreservesSecurityConstraints
- [ ] go test ./memory/... -run TestMemoryStoreContextStoreDivergenceDetected
- [ ] go test ./memory/... -run TestLTMPromotionCrashSafeNoCorruption

# Self-evolution (default off; trust-wall; rollback)
- [ ] go test ./memory/... -run TestEvolvedSkillCannotCrossProofTrustDomain   # Promote(proof role) → ErrProofDomainViolation
- [ ] go test ./memory/... -run TestGenerationLTMNeverReachesProofPrompt
- [ ] go test ./memory/... -run TestSelfEvolutionRegressionGate               # evolved < baseline·(1-tol) → ErrRegressionDetected
- [ ] go test ./memory/... -run TestLTMEvolutionRollback
- [ ] go test ./memory/... -run TestDeveloperScopedLTMRedactsProjectLiterals

# Executable spec dimensions
- [ ] for d in scale observability oncall data slo security deps; do go test ./orchestrator/completeness/... -run TestMissing${d}DimensionRaisesOpenDecision; done
- [ ] go test ./decomposer/... -run TestStatedScaleDimensionProducesCapacityProofObligation
- [ ] go test ./orchestrator/completeness/... -run TestScaleFallbackPresetProducesConcreteThreshold

# Phase E2 integration (run with -race)
- [ ] go test -race ./integration/... -run TestLeaseBlocksOverlappingScope
- [ ] go test ./integration/... -run TestRebaseOnMovedHeadTriggersReproof
- [ ] go test ./integration/... -run TestRebaseConflictDispatchesResolverOrEscalates
- [ ] go test ./integration/... -run TestPostMergeProofRedCausesRollback
- [ ] go test ./integration/... -run TestIntegrationLockStaleLockRecoveryOnRestart
- [ ] go test ./integration/... -run TestWorktreeBaselineRestoredAfterRollback
- [ ] go test ./integration/... -run TestLeaseManagerNoDeadlockUnderContestedScopes
- [ ] go test ./integration/... -run TestResolvedMergeProofCoversAllOriginalObligations

# Packages / skills
- [ ] go test ./skill-registry/... -run TestThirdPartyPackageCannotRegisterProofDomainSkill
- [ ] go test ./skill-registry/... -run TestInstalledSkillGrantsCappedToRoleCeiling
- [ ] go test ./skill-eval/... -run TestEvalHarnessRejectsNonDeterministicPredicate

# Polaris write integrity
- [ ] go test ./polaris-connector/... -run TestPolarisEvidenceWriteIsIdempotentOnRetry
- [ ] go test ./polaris-connector/... -run TestKnowledgeContributionContainsNoRawCodeOrPaths
- [ ] go test ./polaris-connector/... -run TestPolarisTokenNotInContextStoreAndUnreachableFromSandbox
```

## Out of Scope

- **Web/GUI as the control plane.** V2 is TUI-first; the TUI is the authoritative control/conversation surface. An *optional companion web surface* (design review + fleet/proof dashboards) is in scope but read-mostly-plus-approve, phased post-V2.0, and never required to run the loop.
- **Autonomous delivery / auto-deploy.** Deferred to V2.3 and gated by reliability tier; V2.0/V2.1 deliver human-mergeable, proof-passed artifacts only.
- **Polaris failure-mode write-back.** Consume-only until V2.3.
- **Brownfield intake and tracker projection (GitHub Issues).** V2.1.
- **TypeScript and Python generators + proof harnesses.** V2.2 (V2.0 is Go-only).
- **Multi-developer / shared-session collaboration.** Single developer, single machine in V2.
- **Production access / runtime telemetry ingestion.** Orion does not call production or scrape APM in V2; proof is generated in the local sandbox.
- **Hosted SaaS execution of the core loop.** Local-first; only Polaris is cloud.
- **Cross-tenant model training on user code.** Out of scope, permanently.

## Further Notes

- **Credibility hinge (from the manifesto):** Orion is itself an agentic workflow and inherits every failure mode it defends against. The design constraint is recursive — Orion must not trust its own subagents' green checkmarks. This is why the Proof Harness is independent of and hidden from generating agents, and why task closure is verification-gated. Any future change that lets a generating agent influence its own proof reintroduces the core defect and must be rejected in review.
- **Triad reconciliation is a BLOCKING prerequisite, not a "refresh" (raised by architecture review).** The referenced component specs (A2A, Lookout, Orchestrator Logic, Task Decomposer, Verification Engine, Decision Matrix) are written for a **Rust / HTTP-microservice / beads-as-source-of-truth / 2-tier-verification** design that contradicts this PRD's **Go / local-first in-process / Context-Store-as-truth / 3-mode-proof** design. Therefore "reuses [spec]" anywhere above means **"re-derives the concept from"**, not "depends on" — those specs are conceptual ancestors that must be *reconciled and rewritten* before any module that cites them is built. Produce a reconciliation table (Rust→Go, HTTP→in-process channel, beads-truth→Context-Store-truth, 2-tier→3-mode, single-tuple Decision Matrix→per-mode `Converge`) as the first V2.0 design task.
- **Why local-first:** the comparators that define this UX (Gastown, Hermes, OpenClaw, Pi) are local harnesses, and the manifesto's "operable by the developer" thesis is best served when the developer owns the loop on their machine. Polaris remains the cloud knowledge layer — the place reliability wisdom accumulates across everyone.
- **Dogfood path:** prove V2.0 on small greenfield Go services, then turn Orion on Revelara's own repos (V2.1 brownfield) before any external user.

## Adversarial Review Summary

Six parallel expert reviews (architecture, security, testability, data flow, performance, reliability) on 2026-06-17.

Findings by reviewer:
- **Architecture:** 12 findings (3 blocker, 8 important, 1 nit)
- **Security:** 12 findings (3 blocker, 6 important, 3 nit)
- **Testability:** 14 findings (6 blocker, 7 important, 1 nit)
- **Data flow:** 14 findings (5 blocker, 6 important, 3 nit)
- **Performance:** 11 findings (3 blocker, 7 important, 1 nit)
- **Reliability:** 13 findings (4 blocker, 8 important, 1 nit)

The single highest-signal finding, raised independently by **all six** reviewers: the generation⊥proof independence boundary — the manifesto's "no agent grades its own homework" — was asserted in prose but not structurally enforced (a `test-author` agent sat on the generating side, agent-supplied evidence fed the verdict, no module owned the held-out corpus). All reviewers also affirmed the architecture and the manifesto→mechanism mapping are coherent; the blockers are "specify the mechanism," not "the design is wrong."

**Patched inline (new normative sections):** Trust Domains and the Independence Invariant · Harness Reliability Requirements · Security Requirements · Resource & Cost Governance · Core Data Model Hardening. Plus: shell-verifiable Acceptance Criteria added; "reuses [Triad spec]" corrected to "re-derives" and the Triad reconciliation made a blocking prerequisite.

**Acceptance Criteria:** converted from a 14-step narrative test to ~25 shell/Go-test predicates.

**Net effect:** the blockers that were *mechanism-missing* are now stated as contracts. The blockers that were *judgment calls* are recorded in Resolved Design Decisions below (all five resolved). This PRD is now at the altitude where the next step is the component-spec / Triad-reconciliation pass — not another `/write-a-prd`.

## Resolved Design Decisions (post-review)

The five judgment calls surfaced by the adversarial review, resolved 2026-06-17:

1. **Budget posture — warn-by-default, ceilings opt-in.** Spend (tokens/$/wall-clock) is always tracked and surfaced live in the TUI, but no hard ceiling ships by default; ceilings are opt-in config. With a ceiling set, the loop escalates at ~80% and treats out-of-budget as a terminal-with-fallback state. The developer owns the spend/safety trade-off. *(See Resource & Cost Governance.)*
2. **Sandbox — gVisor/runsc default, pluggable across all three.** Isolation is a swappable backend (`sandbox.isolation`): gVisor/runsc (default, and it powers reversibility-by-interception on WSL2), container (Docker/Podman), microVM (Firecracker) — to reach the broadest user base. All backends meet one isolation contract. *(See Security Requirements.)*
3. **Triad — reconciliation table first, then decide per-spec.** The concept-mapping table is the first V2.0 design task; each Triad spec is then refreshed to V2 or archived. *(See Further Notes / Triad reconciliation.)*
4. **Empirical proof — generic "run the real entry point, observe real effects" + per-type adapters.** service = port + request; CLI = exec + exit/stdout/fs; library = harness-authored driver program; batch = fixture run + output diff. V2.0 ships the service adapter only; the others are defined for V2.2. *(See Core Data Model Hardening.)*
5. **Response contract — auto-derived + ratified + amendable, plus STPA control-loop modeling.** The completeness gate compiles the human-approved decisions into a machine-checkable `ResponseContract`, surfaced in spec review for ratification and amendment (derived from human-approved decisions, so it stays in the trusted proof domain — never authored by the generating agent). **Additionally**, hazard mode models the STPA control structure (controllers, control actions, feedback loops) so those become observability surfaces and executable test targets the empirical mode validates — extending proof from "is the response conformant?" to "are the control actions and feedback loops correct and observable?" *(See Core Data Model Hardening — hazard mode.)*

Round-1 questions resolved. Round-2 added new decisions/questions below.

## Adversarial Review Summary — Round 2

Six parallel expert reviews on the material added since Round 1 (Memory & Context-Erosion, Self-Evolution, Composition Model / packages, Phase E2 integration, rvl-cli subsumption, Executable Spec dimensions, web UI), 2026-06-17.

Findings by reviewer (approx): Architecture 11 (2 blocker) · Security 12 (3 critical) · Testability 14 (~9 blocker, mostly missing tests for new claims) · Data flow 11 (4 blocker, schema gaps) · Performance 7 (3 high) · Reliability 13 (4 critical).

All six affirmed the architecture is coherent; the findings are that **the three new subsystems need the same rigor Round 1 gave the Context Store.** The dominant cross-cutting blocker (4 reviewers, independently): **self-evolution can cross the generation⊥proof trust wall and shipped with no kill-switch/rollback** — i.e., the new memory/learning layer reopened the exact "agent grades its own homework" defect the design exists to prevent.

**Patched inline (new normative content):** Trust-Domain invariants 7–9 (memory trust-partition + proof-reads-spec-directly; self-evolution proof-immutable + default-off; proof domain not third-party-extensible) · Memory & Self-Evolution Hardening (crash-safe 2PC eviction, categorical pins, memory schema, divergence reconciliation, LTM compaction/flat-latency, async cheap-first summarization, full self-evolution lifecycle with kill-switch/canary/rollback) · Extension & Package Security (signed capability manifest, grant lattice, lockfile, SkillEval contract, taxonomy clarification) · Phase E2 Reliability & Recovery (lock heartbeat + stale-recovery, pre-merge checkpoint, separate re-proof ceiling, worktree baseline reset, lease ordering/TTL, scoped+batched re-proof) · typed `spec_dimensions` schema + memory/integration/Polaris entities · Polaris write contracts (idempotency keys, deterministic redaction, evidence/risk/knowledge stubs, token storage) · ~30 new shell-verifiable acceptance criteria · `VerificationContract` naming drift swept.

**Net:** the mechanism-missing blockers are now contracts; the remaining judgment calls are below.

## Resolved Design Decisions (Round 2)

All four Round-2 questions resolved 2026-06-17:

1. **Self-evolution:** `self_evolution.enabled = false` by default, deferred to **V2.3**, gated behind the full lifecycle (re-sign → SkillEval regression gate → quarantine → rollback). ✓
2. **Polaris API:** the platform is **OpenAPI-spec'd** — Orion consumes that contract directly rather than reverse-engineering it. The evidence/risk/knowledge/control field sets come from the spec. See [`docs/SPEC/Orion-Polaris-API-Contract.md`](../SPEC/Orion-Polaris-API-Contract.md). ✓
3. **Memory store engine:** **SQLite with a vector extension (`sqlite-vec`)**, a separate DB file from the Context Store, inheriting the same WAL/encryption/`0600`/provenance contracts; `pglite` is the fallback. The vector index serves C4/C6 ANN retrieval. ✓
4. **Auto-evidence:** **automatic submission at all tiers** — Polaris evidence is *proof-of-work*, not a correctness attestation, so no confirm gate is needed. (Risk resolve/close stays automatic but idempotent + provenance-tagged + deduped.) ✓

No open questions remain. Proceeding with the two blocking first design tasks: **Triad reconciliation** ([`docs/SPEC/Orion-Triad-Reconciliation.md`](../SPEC/Orion-Triad-Reconciliation.md)) and the **Polaris API contract** ([`docs/SPEC/Orion-Polaris-API-Contract.md`](../SPEC/Orion-Polaris-API-Contract.md)), then component specs / `/prd-to-issues`.
