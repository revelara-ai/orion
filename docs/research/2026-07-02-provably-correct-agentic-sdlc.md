# The Provably Correct Agentic SDLC

**Status:** North-star synthesis (Stage 1 of 3). Draft for review.
**Date:** 2026-07-02
**Scope:** Orion at HEAD `194c478`. Grounds its reliability claims in the Revelara incident
corpus, reconciles them against Orion's manifesto / PRDs / code, and introduces a new proof
mode — design-time formal verification — as a per-project proof gate.
**Provenance:** Built from (a) the Revelara production incident corpus (MCP `revelara-prod`,
~4,237 incidents), (b) `docs/MANIFESTO.md` + `docs/PRD/{orion-v2,orion-v3,orion-brownfield,orion-generalization}.md`,
(c) the code-level reliability inventory (`internal/{llmclient,agentruntime,proof,sandbox,budget,actuation,reliabilitytier,integration,worktree,conductor}`),
and (d) the harness-reliability research already in `docs/research/`.

> **Naming:** *Polaris* is used here as the internal codename for the Revelara reliability
> backend Orion consumes. Internal doc only — never in public/customer copy.

---

## 1. Thesis

Orion's founding claim is **"the proof is the right to ship."** Today that proof covers the
*product*: does the code do what was meant? It is established by **triad convergence** —
behavioral (harness-authored, mutation-scored tests), empirical (Lookout shell probes of the
running artifact), and hazard (STPA-derived checks) — computed by a trusted proof domain that
no generating agent can influence.

A **provably correct agentic SDLC** has to prove a *second, independent* thing: the **design**.
Is what was meant even coherent and safe under concurrency, ordering, and failure — *before*
a line of it is written? A test suite proves an implementation matches a design. It cannot prove
the design has no race, no deadlock, no reachable unsafe state. That is a different question, it
is answerable, and the tool for answering it is a **model checker**.

So the complete picture is two proofs, not one:

| | Question | Established by | When |
|---|---|---|---|
| **Product proof** | Does the code do what was meant? | Triad convergence (behavioral + empirical + hazard) | Artifact-time (Phase E) |
| **Design proof** | Is what was meant safe/live under concurrency & ordering? | **Formal model checking** (this document's addition) | **Design-time (after spec+STPA, before generation)** |

This document argues the thesis in three grounded moves: **(§2–3)** why harness and SDLC
reliability is a hard, empirical problem, using real outages and Orion's actual loop; **(§4)**
where Orion stands against that bar today, reconciling the incident-derived checklist with
Orion's own gap audit; and **(§5–6)** the design proof — the formal-methods gate — including a
worked model of a *real, currently-latent* Orion defect, folded into a single unified proof
architecture.

The through-line the manifesto already states, sharpened: every agentic failure arises because
**the loop optimizes a local signal while the true goal drifts, decays, or goes unmeasured.**
Product proof pins the local signal to the true goal at the artifact. Design proof pins it at
the design. Between them there is a third class — **the loop's own control plane** (state
transitions, leases, queues) — whose bugs are *systemic*, and which neither proof reaches. The
formal-methods gate is what reaches it, and (§6) Orion's own orchestrator is the first thing it
should be pointed at.

---

## 2. Why this matters — the empirical case

The reliability bar is not theoretical. A harness like Orion is, structurally, a resilience
wrapper around a **stochastic, rate-limited, frequently-degraded remote dependency** (the model
API), driving a loop that executes tools. Its reliability is dominated not by the model but by
how the loop behaves when the model misbehaves. The Revelara incident corpus makes the failure
modes concrete — these are real, recent, public postmortems (cited by `short_name`; each carries
a `source_url` for verification).

Two facts set the stakes:

- **The model API is a regularly-degraded dependency, not an occasional one.** June 2026 alone:
  multiple model-wide elevated-error events (`inc-9yf`, `inc-ii6`, `inc-vw0`, `inc-112`,
  `inc-6la`), Copilot losing "many models" for ~85 min at 5–11% error rate (`inc-ubc`), and Azure
  OpenAI degrading across Europe + Australia (`inc-qdi`). Every Anthropic incident lists *Claude
  Code* and *Claude Cowork* as affected, because harnesses sit directly on the API with no bulkhead.
- **The best-documented case in the corpus is harness-shaped.** `inc-qdi` (Azure OpenAI PIR,
  quality 8/10) is a **retry-amplification** outage: a change made capacity-exhaustion errors look
  retriable, multiple layers each retried without backoff, and **one failed request became up to
  48 retries**, OOM-crashing a shared load balancer across regions. Read it as Orion's cautionary
  tale.

### The harness reliability checklist

Distilled from the corpus. Each control names a failure mode, the incident evidence, and the
design obligation. §4 scores Orion against each.

| # | Control | Failure mode it prevents | Incident evidence |
|---|---|---|---|
| **C1** | **Bounded retry / anti-amplification** — one retry budget across the *whole* call stack (SDK × agent-loop × user × subagent fan-out), backoff + jitter, precise retriable-vs-terminal classification | Retry storm: multiplicative retries overwhelm the provider (or self) | `inc-qdi` (1 req → 48 retries → OOM) |
| **C2** | **Concurrency cap** on in-flight model/agent calls | Self-inflicted DoS / loopback death spiral | `inc-3ik` (Bluesky "missing concurrency limits") |
| **C3** | **Model fallback ladder** — degrade Opus→Sonnet→Haiku or cross-provider on error/exhaustion | Hard-fail mid-task when the provider is down | `inc-9yf` cluster, `inc-ubc` |
| **C4** | **Circuit breaker at the loop level** — trip the agent loop, not just the HTTP client, after N bad turns | A wedged/hung loop burning budget on a dead dependency | `inc-qdi` |
| **C5** | **Overload protection covering internal/background traffic** — subagents, routines, speculative/shadow work share the interactive limiter; shed background first | Your own background traffic bypasses rate limits and causes the outage | `inc-qdi` (internal first-party traffic bypassed the overload controls external traffic was subject to) |
| **C6** | **Staged rollout + kill switch** for prompt/model/config/skill changes — canary, ring-deploy, feature-flag, fast rollback | A bad "content" update auto-deploys everywhere at once | `inc-u12` (CrowdStrike Channel File 291: malformed rapid-response content, no canary, global) |
| **C7** | **Validate untrusted input at every boundary** — model output *and* tool/command output (2nd-order injection, malformed JSON, oversized/binary) | One bad payload crashes the loop / injects | `inc-u12` (parser crash on malformed content); OWASP ASI01 |
| **C8** | **Control-plane dependency isolation** — auth/config/telemetry failures must not wedge the loop | A hidden control-plane dependency takes everything down | `inc-3ik` (Railway: GCP suspension exposed a hidden control-plane dep → 8h SEV-1) |
| **C9** | **Structured per-step tracing / loop observability** — a queryable trace per tool call and model round-trip, with retry/token/latency counts | Can't tell the agent is drifting; can't distinguish symptom from cause | distilled knowledge on observability gaps; `inc-qdi` (4h chasing a wrong mitigation) |
| **C10** | **Guard against the "misleading first fix"** — don't let auto-remediation mask the real signal; detect net-negative refinement | Auto-fix confirms a coincidence; iterative degradation | `inc-qdi` (disabled a feature, traffic coincidentally dropped, recurred worse); iterative-security-degradation research |
| **C11** | **Checkpoint / resume across a provider outage** — pause-and-resume, not lost work | An outage mid-task discards everything | frequency of `inc-9yf`-class events |
| **C12** | **Token / cost budget enforcement** — accounting + ceilings that actually halt | Unbounded spend from a runaway loop | (governance; ubiquitous) |

Six of these are control-*systems* problems — retry feedback, concurrency limits, overload
propagation, blast-radius isolation. Not "the AI was wrong." That is the tell that the reliability
of an agentic SDLC is an *engineering* problem with an *engineering* answer, which is the rest of
this document.

---

## 3. The Orion loop as it stands — the SDLC spine

The provably-correct SDLC is not a greenfield proposal; it is Orion's existing loop with one gate
added. This is that loop as implemented at HEAD, the spine everything else hangs on.

### 3.1 The developer loop

```
intent
  → completeness gate (deterministic)         [HITL: answer open decisions]
  → executable spec (hashed, anchored)        [HITL: orion spec approve]
  → STPA hazard model (directed questionnaire) [HITL: ratify]
  →〈FORMAL-METHODS GATE〉                      ← §5, the addition (tier-gated)
  → decompose → Epic/Tasks + DAG (coverage gate)
  → worktree per task (isolated, sandboxed)
  → generate (specialist agent, sandboxed)     [autonomous, generation⊥proof wall]
  → triad proof: behavioral + empirical + hazard → Converge
  → done-gate (store-enforced: proven ⇔ verdict=Accept)
  → deployment bar → deliver | escalate        [HITL: merge decision]
  → learn (Polaris write-back; opt-in evolve)
```

### 3.2 CLI surface (headless loop-control + interactive TUI)

The TUI (`orion` with no args) is the primary seat; the same loop is drivable headless:

| Stage | Command | Purpose |
|---|---|---|
| Intake | `orion submit --non-interactive` | Persist intent + draft spec, run completeness gate, emit open decisions |
| Elicit | `orion answer --key --value` | Record a completeness decision |
| Spec | `orion spec approve \| show` | Ratify the executable spec (returns hash) |
| Plan | `orion plan show` | Decompose the spec → render Epic/Task plan |
| Build | `orion run` | Execute build→prove→deliver for the lead task; close only if Accept |
| Proof | `orion proof show --task --mode` | Inspect a task's proof verdict per mode |
| Deliver | `orion deliver show [--runbook]` | Latest delivery + operating envelope (or escalation) |
| Brownfield | `orion change "<intent>"` | Change-proof loop in an existing repo → commit on a review branch |
| Brownfield | `orion baseline [dir]` / `orion map [dir]` | Capture regression baseline / print the codebase map |
| Queue | `orion queue list\|next\|abandon\|activate` | FIFO intent queue |
| Human loop | `orion escalations list\|show\|resolve` | The escalation inbox |
| Safety | `orion redbutton engage\|release\|status` | Emergency stop for all mutating actuation |
| Lifecycle | `orion conductor start\|stop\|status\|attach\|acp` | Conductor control process (`stop --red-button` trips the stop) |
| Deps | `orion deps verify <module>` | Provenance check — a hallucinated package exits non-zero pre-build |
| Learn | `orion evolve` | Opt-in (default OFF) promotion of proof-passed memory → skills |
| Health | `orion doctor [--fix]` | Component health — the 3 a.m. test applied to Orion itself |

### 3.3 Where the human is

| Stage | Mode | Human decision |
|---|---|---|
| Intake / elicitation | **HITL** | Types intent; answers the completeness gate |
| Spec ratification | **HITL approval** | `orion spec approve` — spec frozen only on sign-off |
| STPA ratification | **HITL** | Ratifies the hazard model |
| Decompose → generate → triad → done-gate | **Autonomous + deterministic** | The Conductor *cannot* override a proof FAIL |
| Deployment bar | Autonomous → **escalate on fail** | Unmet bar routes to the escalation inbox |
| Integration / merge | **HITL** | Operator authorizes merge; conflicts escalate |
| Learn / evolve | **Opt-in HITL** | `orion evolve` runs only when invoked |
| Any stage | **Red Button** | `redbutton engage` / `conductor stop --red-button` / ACP `session/cancel` |

**Load-bearing invariant** (`Orion-TUI-ACP-Conductor.md §5`): the deterministic gates — proof,
deployment bar, leases, dry-run, integration — are **caller-agnostic**. An LLM Conductor gets no
special power over them; a human `request_permission` grants authorization but never substitutes
for proof. This is the manifesto's answer to the recursive trap: **the orchestrator is the one
non-agentic component.**

### 3.4 Supporting mechanics

- **Git worktrees** (`internal/worktree`): one worktree ⟷ one task ⟷ one branch at
  `worktrees-<repo>/<issue-id>`, the agent's only writable FS. Reconciled against the filesystem
  on startup; deletion is safety-gated (refuses mid-integration even under `--force`).
- **Tracker/beads** (`internal/tracker`): the Context Store (SQLite/WAL) is the source of truth;
  the tracker is a **one-way projection** (`orion tracker project` never mutates task state).
- **Escalations** (`internal/contextstore` + `orion escalations`): the unified queue of decisions
  routed to a human; delivery failure surfaces an escalation ID with the exact resolve command.
- **Red button** (`internal/actuation.RedButton`): file-backed, cross-process emergency stop;
  halts new dispatch/export/git/bd writes while in-flight proofs finish.

---

## 4. Reconciliation — the checklist against Orion

Two independent analyses — the incident-corpus checklist (§2) and Orion's own
`Backlog-harness-gap-audit-2026-07-01.md` — **agree** on where the open work is. That agreement
is the point: the empirical outside view and the internal architectural view converge.

Legend: **✅ ADDRESSED** · **◑ PARTIAL** · **✗ GAP**. Staleness note: three audit items were
closed *after* the 2026-07-01 audit by the `or-v9f.*` series (Red Button `Guard()` wiring,
budget ceilings, AlignmentGate blocking); reflected below.

| # | Control | Status | Where / why |
|---|---|---|---|
| C1 | Bounded retry / anti-amplification | **◑** | Per-call bound: `llmclient.Do` caps `MaxRetries` + jittered backoff + breaker. **No stack-wide budget** summing coordinator + agent turns + proof re-loops into one ceiling. The `inc-qdi` multiplicative case is only partly defended. |
| C2 | Concurrency cap | **✅** | `agentruntime.Dispatcher.sem` bounded semaphore + backpressure (`WithMaxConcurrency`); DAG scheduler bounds cluster parallelism (`dag.go`, `inflight >= maxConc`). |
| C3 | Model fallback ladder | **✗** | `internal/llm` has a multi-provider interface but **no automatic degrade chain** on rate-limit/exhaustion. Tracked as gap **#A12** (failover + auth-profile rotation). |
| C4 | Loop-level circuit breaker | **◑** | HTTP breaker is solid (`llmclient`) but wraps coordinator calls only. Loop resilience is per-step deadline + confidence escalation — **no failure-accumulating breaker** that opens after N bad turns. |
| C5 | Overload protection incl. internal traffic | **◑** | Dispatcher semaphore caps all dispatched subagent work; budget records generation-path spend (`or-v9f.18`). But **shadow/speculative** work (ModuleProposer shadow runs) and coordinator inference aren't counted against the same in-flight cap — the exact `inc-qdi` internal-traffic hole. |
| C6 | Staged rollout + kill switch | **◑** | Kill switch **✅** (Red Button, wired `or-v9f.14`). Staged rollout **◑**: shadow→cutover exists for ModuleProposer + reversible LTM promotion + env feature-flags, but **prompts/roles/checklists are hardcoded in Go** with no versioned-config canary; no model-swap canary. |
| C7 | Validate untrusted output | **◑** | Model *artifacts*: sandboxed (bwrap, default-deny egress) + independent proof + secretscan + promptguard. **Tool/command *output* fed back to the loop (2nd-order injection): not guarded** — gap **#B1** (OWASP ASI01). |
| C8 | Control-plane isolation | **◑** | Polaris reachability injected; provider keys scrubbed from sandbox (`safeenv`); tracker/gopls/sandbox degrade to Warn not Fail. Weakness: coordinator inference now sits on the critical path (ADR-0001) with no shown bypass; Context Store is a single in-process mutex. **No test that a telemetry/config outage cannot wedge the loop.** |
| C9 | Per-step tracing / observability | **◑** | Self-instrumentation exists (log per state transition, phase events, trace per dispatch, budget snapshots). **No first-class trace/drift surface** — Day1 Gap 3; no longitudinal loop-quality eval — Bayer R1. |
| C10 | Guard against misleading first fix | **◑** | Structural defense is strong: generation⊥proof wall + `EnforceObligations` (un-run obligation → Inconclusive, not false green) + blocking AlignmentGate. **Iterative degradation is not specifically instrumented** — no net-negative-refinement detector. |
| C11 | Checkpoint/resume across outage | **◑** | Crash/restart resume exists (`worktree.CreateResume`, integration crash-recovery, idempotent re-run skips unchanged clusters, WAL). But mid-task death restarts the task from scratch; **no provider-outage checkpoint** (resume a half-generated turn after a 529). |
| C12 | Budget enforcement | **✅** | `internal/budget`: always-on token/$/wall accounting, env ceilings, warn at 80% / halt at 100%, generation-path recording + dispatch-gate (`or-v9f.18`). |

**Reading of the matrix.** Orion is strong exactly where it has invested in the *structural*
defenses (C2, C12 done; C7/C10 well-defended at the artifact via the trust wall). The open items
cluster into two groups:

1. **Runtime-resilience gaps** (C1, C3, C4, C5, C8, C9, C11) — the classic harness controls the
   corpus screams about. These are the natural content of the **Stage-2 checklist doc + a
   resilience PRD**. They are known, bounded, and mostly "wire up a pattern."
2. **A class neither the checklist nor the triad covers: systemic control-plane correctness.**
   A wrong state transition, a lease race, a queue deadlock. The triad proves *products*; the
   checklist hardens *runtime*; but the orchestrator's own logic — "the one non-agentic
   component, its bugs are systemic" — is verified by neither. §5 is the answer.

---

## 5. The new proof mode — design-time formal verification

### 5.1 What it is and where it sits

A **formal-methods module**: a proof gate Orion applies to **the product it was asked to build**,
tailored per-project. When the ChangeSpec involves a concurrent protocol, a state machine, or an
ordering/safety invariant, Orion synthesizes a formal model (FizzBee by default, TLA+/Apalache
as the escape hatch) of *that specific design*, model-checks it, and the result becomes a verdict.

It runs **design-time** — after the executable spec and STPA, **before generation** — for four
reasons: (1) model checkers verify *models*, not code, so this is the honest placement; (2) it is
the cheapest place to kill a concurrency bug — before 20 files exist; (3) it composes with the
STPA hazard work Orion already does; (4) it satisfies the manifesto tenet *"intent must be
complete before code is written."*

Conceptually it is a **fourth proof mode** — *formal* — alongside behavioral / empirical /
hazard. Mechanically it does not re-check the artifact at convergence; instead its verified
invariants **compile into behavioral proof obligations** (model-based test generation), so the
artifact-time triad proves the code *implements the verified design*. The refinement chain:

```
STPA UCA  →  formal invariant  →  model-check (safety+liveness)  →  behavioral obligation  →  generated test  →  code
 (hazard)      (design proof)         (FizzBee/TLA+)                  (coverage gate)          (behavioral)   (empirical)
```

### 5.2 Trust story

Identical posture to STPA, which Orion already trusts: an LLM **drafts** the model, the developer
**ratifies** it (like the STPA questionnaire), and the **model checker is deterministic**. The
untrusted generation fleet never authors its own formal proof — the model is a proof-domain
artifact. A wrong model yields a wrong-but-*inspectable* spec that the human ratifies and the
checker exercises; it cannot manufacture a false green, because a green model-check is only ever
`Accept`-eligible for the *design*, and the design is human-ratified.

### 5.3 Tier-gating

Per the manifesto tenet *"reliability is calibrated to the project, not maximized blindly,"* the
gate is **tier-gated** off `internal/reliabilitytier` and the design's shape:

- **Fires** when tier = `critical`, or the design exhibits concurrency / ordering / shared-state /
  protocol / distributed-consensus structure (detectable from the spec + STPA control structure).
- **Skips** a stateless CRUD endpoint at `throwaway`/`standard` tier — model-checking it is waste.

### 5.4 Why FizzBee (and when not)

FizzBee is a model checker whose language is **Starlark, a Python subset** — specs are
Python-readable and REPL-testable, which matches a Python-first team. It checks **safety**
(`always assertion`, TLA+ `[]`), **liveness** (`always eventually` / `eventually always`), and
**invariants**, with atomicity made explicit (`atomic` / `serial` actions, `oneof`, `parallel`,
`any`) and built-in probabilistic/performance analysis via Markov reachability.

Design around its current limits: **deadlock detection and full liveness/fairness are still
early, and functions don't take parameters yet.** So the module keeps **TLA+/Apalache as the
escape hatch** for hard liveness/deadlock cases, with FizzBee as the default for safety-invariant
checking on ordinary designs.

### 5.5 Worked example — a *real, currently-latent* Orion defect

The best demonstration is a genuine open invariant in Orion's own control plane: **file-scope
mutual exclusion during integration.** This doubles as the dogfood case (§6) — the model checked
here is Orion's *own* integration queue.

**The real code.** Two lease mechanisms exist:

- **System A (LIVE)** — `internal/conductor/dag.go` gates *dispatch/build* concurrency on
  overlapping file-scope leases (`dag.go:237-246`), trusting the **LLM-declared** `FileScope`.
- **System B (DEAD)** — `internal/integration/integration.go` defines `AcquireLease` /
  `ReleaseLease` (`integration.go:60-84`) exactly for *integration-time* mutual exclusion — and
  **they are never called in production** (only in tests). The integration path uses only
  `queue sync.Mutex` + git.

**The named invariant** (spec `Orion-Worktree-Git.md §9`, PRD Phase E2.1/E2.8):

- **S1** — no two tasks with overlapping declared file scope integrate concurrently.
- **S2** — at most one integration onto the head at a time.

**The gap.** S2 holds today, but **incidentally** — because `integrateEpic`
(`dagintegrate.go:65`) happens to loop *sequentially* and `Integrate` holds `queue.Mutex`. S1 is
**not enforced by the mechanism the spec names** — leases are dead. The moment someone
parallelizes `integrateEpic` (the obvious next step — the build phase is already
`maxConc`-parallel), there is **no lease guard on the integration path**; overlapping merges can
race, with only after-the-fact git rebase-conflict detection as a backstop. The triad cannot see
this — each cluster proved green *in isolation*.

**The model.** Orion would generate this from the ChangeSpec's concurrency + the STPA UCA "two
agents edit the same file concurrently." FizzBee-flavored (schematic — surface syntax finalized
against the toolchain):

```python
# Orion integration queue — file-scope mutual exclusion (models Phase E2).
# Two proven clusters A, B whose DECLARED file scopes overlap (both touch internal/foo).
SCOPE = {"A": ["foo"], "B": ["foo"]}

atomic action Init:
    status = {"A": "ready", "B": "ready"}   # ready -> integrating -> integrated
    integrating = []                         # tasks currently mid-integration (on the head)

# Begin integrating a ready task.
# LEASE GUARD = what Integrator.AcquireLease would enforce — but is DEAD in Orion today.
# Delete the guarded block to reproduce the latent bug under a parallelized integrateEpic.
atomic action StartIntegrate:
    any t in ["A", "B"]:
        if status[t] == "ready":
            conflict = False
            for u in integrating:                         # --- lease guard begin ---
                if [p for p in SCOPE[t] if p in SCOPE[u]]:   # declared-scope overlap
                    conflict = True
            if not conflict:                              # --- lease guard end ---
                status[t] = "integrating"
                integrating.append(t)

# Finish: ff-merge + re-prove + advance head, then release lease.
atomic action FinishIntegrate:
    any t in ["A", "B"]:
        if status[t] == "integrating":
            status[t] = "integrated"
            integrating.remove(t)

# S1 — no two overlapping-scope tasks integrate concurrently (the lease invariant).
always assertion NoOverlappingIntegration:
    for t in integrating:
        for u in integrating:
            if t != u and [p for p in SCOPE[t] if p in SCOPE[u]]:
                return False
    return True

# S2 — at most one integration onto the head at a time (the queue.Mutex).
always assertion SingletonIntegration:
    return len(integrating) <= 1
```

**What the checker does.** *With* the lease guard, both invariants hold across the state space.
*Without* it (System B dead + `integrateEpic` parallelized), FizzBee explores the interleaving
`StartIntegrate(A) ∥ StartIntegrate(B)` and returns a counterexample trace: both tasks in
`integrating`, overlapping scope → `NoOverlappingIntegration` = False (and `SingletonIntegration`
= False, len 2). Milliseconds, before any parallelized integration code is written.

**What Orion does with the result.** The failing check **blocks/flags the design** at design-time.
The verified guard becomes a **behavioral obligation** — a generated test asserting
`Integrator.AcquireLease` refuses overlapping scope and that `integrateEpic` acquires before
merging — which the artifact-time triad then enforces on the code. The STPA UCA that seeded the
invariant is closed by construction, not by hope.

This is the whole thesis in one example: a **systemic control-plane bug**, invisible to
per-task product proof, caught by **design proof**, and cashed out as a **behavioral obligation**
that keeps it caught.

> **Tracked as `or-1lz`** (P1). The fix — wire the integration-time lease guard
> (`Integrator.AcquireLease`) into `dagintegrate`, and/or assert the sequential-integration
> constraint so a future parallelization cannot silently violate S1 — is shippable independent
> of this document.

---

## 6. The complete picture — unified proof architecture

Assembled, the provably-correct agentic SDLC is a small number of independent, composable proofs
and calibrations, each closing a specific failure class:

```
                         ┌───────────────────────────────────────────────┐
   intent  ─▶ completeness gate ─▶ executable spec (anchored) ─▶ STPA     │
                         │                                                │
                         ▼                                                │
              ┌─────────────────────┐                                     │
              │ DESIGN PROOF (§5)    │  formal model-check (FizzBee/TLA+)  │  ← closes systemic
              │ tier-gated           │  safety + liveness on the design    │    control-plane +
              └──────────┬──────────┘  → behavioral obligations           │    design-race bugs
                         ▼                                                │
                decompose ─▶ worktrees ─▶ generate (sandboxed, ⊥ proof)   │
                         ▼                                                │
              ┌─────────────────────┐                                     │
              │ PRODUCT PROOF        │  behavioral + empirical + hazard    │  ← closes
              │ (triad, Converge)    │  → done-gate (Accept ⇔ proven)      │    product-correctness
              └──────────┬──────────┘                                     │    bugs
                         ▼                                                │
        RUNTIME RESILIENCE (checklist C1–C12 / Stage-2 PRD): retry budget, │  ← closes
        fallback ladder, loop breaker, overload/isolation, tracing         │    runtime failure
                         ▼                                                │
             deployment bar ─▶ deliver | escalate ─▶ Polaris learning ─────┘  ← compounds across runs
        (tier calibration threads through all of it; Red Button halts all of it)
```

### 6.1 Tenet → mechanism → failure class

| Manifesto tenet | Mechanism | Failure class closed |
|---|---|---|
| Passing tests ≠ correctness; proof is multi-modal | Triad convergence (behavioral+empirical+hazard) | Product correctness |
| Assume agents game the verifier | Trust wall; harness-owned evidence; deterministic gates | Verifier gaming (systemic) |
| **Intent complete before code** | **Design proof (formal gate) + completeness gate** | **Design races/deadlock (systemic)** |
| Understanding is a first-class output | Self-instrumentation, runbooks, operating envelope | Un-operable code / observability (C9) |
| Reliability calibrated to project | Reliability tier → gate/rigor selection | Over/under-engineering; wasted proof |
| Small bounded steps + re-grounding | Decomposition, per-module context, anchor re-read | Error compounding, intent drift |
| Reliability knowledge compounds | Polaris write-back; opt-in evolve | Recurring failure (silent) |
| Reliability is the harness's job | Runtime resilience checklist (C1–C12) | Provider/dependency failure |
| The orchestrator is the one non-agentic component | Deterministic state machine + **formal verification of it (§6.2)** | **Control-plane bugs (systemic)** |

### 6.2 The dogfood corollary

The formal gate is built to verify *products*. But Orion's own orchestrator — the integration
queue, path leases, done-gate transitions, DAG scheduler — is **exactly the concurrent state
machine the gate checks**, and §5 already found a real latent defect in it. So the gate should be
pointed at Orion itself: the harness's own control-plane invariants (S1–S2 above, plus done-gate
soundness — "`proven` is reachable only through `Accept`", and DAG liveness — "every ready task
eventually integrates or escalates; no deadlock") become model-checked artifacts in Orion's CI.

This closes the recursive trap at the level the manifesto demands: not only does Orion *assert*
its orchestrator is the trustworthy non-agentic core — it **proves** it, with the same class of
tool it uses to prove everything else. That is what makes the SDLC *provably* correct rather than
*asserted*-correct: the prover is itself proven.

---

## 7. Staging / roadmap

Per the agreed "both, staged" plan.

**Stage 1 — this document.** The north-star picture. Locks the thesis, the reconciliation, and
the formal-gate design.

**Stage 2 — split-out build artifacts** (extracted from §2–4 with more operational detail):
- **Harness reliability checklist** (`docs/research/` or `docs/SPEC/`) — C1–C12 as actionable,
  each with acceptance criteria; the Stage-2 home of §2.
- **Checklist ↔ Orion gap reconciliation** — §4 as a living scorecard, cross-linked to beads
  issues and the `or-v9f.*` closure history.
- **SDLC / developer-loop doc** — §3 expanded into the canonical end-to-end usage guide (the doc
  the repo currently lacks: no single file walks the current `orion`-subcommand loop end to end;
  the README diagram is the shape, not the sequence).

**Stage 3 — formal-verification PRD/ADR.** The module as a shipped proof gate:
- Schema: model artifact type, per-tier trigger policy, FizzBee/TLA+ backend selection.
- Hooks: `internal/proof` (new mode, obligation compilation) + `internal/decomposer` /
  `internal/orchestrator` (design-time placement, STPA-UCA → invariant synthesis).
- Trust: model as proof-domain artifact; human ratification; deterministic check.
- CI: model-check Orion's *own* control-plane invariants (§6.2) as a gate.
- Fits the PRD lineage: complements `orion-v2` (the triad), `orion-v3` (the AlignmentGate — the
  other "new mode"), and `orion-brownfield` (the model must evolve with a changing design).

---

## 8. Open questions & risks

1. **Spec → model synthesis reliability.** The hardest LLM step. Mitigation: the model is a
   ratified, inspectable proof-domain artifact (STPA posture), and the check is deterministic —
   but a *model that doesn't faithfully abstract the design* yields a green check on the wrong
   thing. Faithful-abstraction is the real risk, not checker soundness.
2. **Green model ≠ correct code.** Model checking proves the *design*, not the implementation.
   The refinement chain (§5.1) is what carries the guarantee into code via behavioral obligations;
   if obligation generation is weak, the design proof is decorative. This chain is load-bearing.
3. **FizzBee maturity.** Deadlock detection and full liveness/fairness are early. Policy needed:
   when to auto-escalate a design to TLA+/Apalache (likely: any liveness/deadlock-critical
   invariant, or distributed consensus).
4. **Cost/latency at scale.** State-space explosion. Tier-gating bounds *when* it runs; model
   size discipline (abstract to the invariant, not the whole design) bounds *how big*. Needs a
   per-tier time budget and a "model too large → escalate to human design review" fallback.
5. **Brownfield model drift.** For `orion change`, the design evolves; the model must be
   maintained or regenerated per change, and reconciled against the existing code's implicit
   invariants (brownfield's "two masters"). Unowned models rot.
6. **Scope of the dogfood pass.** §6.2 is attractive but adds a formal-methods dependency to
   Orion's own CI. Decide whether control-plane verification is a blocking CI gate or an advisory
   check first (mirror the AlignmentGate's advisory→blocking rollout).

---

## Appendix — incident index

Cited from the Revelara corpus (MCP `revelara-prod`); each has a public `source_url`.

| short_name | What it demonstrates | Controls |
|---|---|---|
| `inc-qdi` | Azure OpenAI retry-amplification (1 req → 48 retries → OOM); misleading first fix; internal traffic bypassing overload controls | C1, C4, C5, C10 |
| `inc-9yf`, `inc-ii6`, `inc-vw0`, `inc-112`, `inc-6la` | Provider is a regularly-degraded dependency (model-wide error events, one month); Claude Code/Cowork sit directly on the API | C3, C11 |
| `inc-ubc` | Copilot lost "many models" ~85 min at 5–11% error | C3 |
| `inc-u12` | CrowdStrike Channel File 291 — malformed rapid-response content, no canary, global auto-deploy, parser crash | C6, C7 |
| `inc-3ik` | Railway/Bluesky/Cloudflare — hidden control-plane dependency, loopback death spiral / missing concurrency limits | C2, C8 |
