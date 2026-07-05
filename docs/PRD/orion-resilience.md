---
title: Orion Runtime Resilience — Hardening the Loop Against a Degraded Dependency
status: draft
authors: Joseph Bironas
created: 2026-07-03
last_updated: 2026-07-03
derived_from:
  - docs/MANIFESTO.md
  - docs/research/2026-07-02-provably-correct-agentic-sdlc.md          # Stage 1 north-star (§2, §4)
  - docs/research/Backlog-harness-gap-audit-2026-07-01.md              # the internal gap audit that agrees
references:
  - internal/llmclient
  - internal/agentruntime
  - internal/budget
  - internal/actuation           # Red Button
  - internal/reliabilitytier
---

# Orion Runtime Resilience — Hardening the Loop Against a Degraded Dependency

> **Orion's reliability is dominated not by the model but by how its loop behaves when the
> model misbehaves.** A harness is structurally a resilience wrapper around a stochastic,
> rate-limited, frequently-degraded remote dependency — the generation model's API. The
> product-proof triad and the design-proof gate prove the *software Orion builds*. This PRD
> hardens *Orion itself*: the twelve controls (C1–C12) that keep the loop alive, bounded, and
> honest when the provider degrades.

## 0. Why this exists

Manifesto failure mode #9 ("The harness itself degrades under its own dependency") and tenet #10
("reliability is the harness's job") now cover two distinct things: the reliability of the
*product* Orion ships, and the reliability of the *loop* that ships it. This PRD owns the second.

The controls are not theoretical. They are distilled from the Revelara production incident corpus
(§2 of the north-star doc) — real, recent, public postmortems. Two facts set the stakes:

- **The model API is a regularly-degraded dependency, not an occasional one.** Provider-wide
  elevated-error events recur monthly; harnesses (Claude Code, Copilot, Cowork) sit directly on
  the API with no bulkhead.
- **The best-documented case is harness-shaped.** `inc-qdi` (Azure OpenAI PIR): a change made
  capacity-exhaustion errors look retriable, multiple layers each retried without backoff, and
  **one failed request became up to 48 retries**, OOM-crashing a shared load balancer across
  regions. Read it as Orion's cautionary tale.

Six of the twelve controls are control-*systems* problems — retry feedback, concurrency limits,
overload propagation, blast-radius isolation — **not "the AI was wrong."** That is the tell that
harness resilience is an *engineering* problem with an *engineering* answer.

**Two independent analyses agree on where the work is:** the incident-corpus checklist below and
Orion's own `Backlog-harness-gap-audit-2026-07-01.md`. The empirical outside view and the
internal architectural view converge — that agreement is the point.

## 1. Scope

**In scope:** C1–C12 — the runtime-resilience controls on Orion's own agent loop. Each is
tier-gated where the Manifesto's calibration principle applies (a throwaway run does not need a
cross-provider fallback ladder).

**Out of scope — deliberately, to avoid conflation:**
- **Systemic control-plane correctness** (wrong state transition, lease race, queue deadlock) →
  `docs/PRD/orion-formal-verification.md`. Neither the checklist nor the triad reaches it.
- **Product correctness** (the triad) → `orion-v2` / `orion-v3`.

## 2. The checklist (C1–C12)

Each control names the failure mode it prevents, the incident evidence, the **current status**
(from the reconciliation, §3), and a **shell-verifiable acceptance criterion**.

Legend: **✅ ADDRESSED** · **◑ PARTIAL** · **✗ GAP**.

### C1 — Bounded retry / anti-amplification · ◑
One retry budget across the *whole* call stack (SDK × agent-loop × user × subagent fan-out),
backoff + jitter, precise retriable-vs-terminal classification.
- **Prevents:** retry storm — multiplicative retries overwhelm the provider (or self). `inc-qdi`
  (1 req → 48 retries → OOM).
- **Now:** per-call bound is solid (`llmclient.Do` caps `MaxRetries` + jittered backoff +
  breaker). **No stack-wide budget** summing coordinator + agent turns + proof re-loops into one
  ceiling. The multiplicative case is only partly defended.
- **Acceptance:** a fault-injection test where every layer sees a retriable error asserts total
  attempts across the stack ≤ one configured budget (never the product of per-layer limits).

### C2 — Concurrency cap · ✅
A bounded cap on in-flight model/agent calls.
- **Prevents:** self-inflicted DoS / loopback death spiral. `inc-3ik` (Bluesky "missing
  concurrency limits").
- **Now:** `agentruntime.Dispatcher.sem` bounded semaphore + backpressure
  (`WithMaxConcurrency`); DAG scheduler bounds cluster parallelism (`inflight >= maxConc`).
- **Acceptance:** a load test confirms in-flight calls never exceed the configured cap;
  excess work queues rather than launching.

### C3 — Model fallback ladder · ✗
Degrade Opus→Sonnet→Haiku or cross-provider on error/exhaustion.
- **Prevents:** hard-fail mid-task when the provider is down. `inc-9yf` cluster, `inc-ubc`.
- **Now:** `internal/llm` has a multi-provider interface but **no automatic degrade chain** on
  rate-limit/exhaustion. Tracked as gap **#A12** (failover + auth-profile rotation).
- **Acceptance:** injecting a 429/529 on the primary model yields an automatic, logged fallback
  to the next rung and the task completes; the ladder is tier-configurable.

### C4 — Loop-level circuit breaker · ◑
Trip the agent *loop*, not just the HTTP client, after N bad turns.
- **Prevents:** a wedged/hung loop burning budget on a dead dependency. `inc-qdi`.
- **Now:** HTTP breaker is solid (`llmclient`) but wraps coordinator calls only. Loop resilience
  is per-step deadline + confidence escalation — **no failure-accumulating breaker** that opens
  after N bad turns.
- **Acceptance:** after N consecutive bad turns the loop opens the breaker and escalates to a
  human rather than continuing; the threshold is configurable and logged.

### C5 — Overload protection incl. internal/background traffic · ◑
Subagents, routines, speculative/shadow work share the interactive limiter; shed background first.
- **Prevents:** your own background traffic bypasses rate limits and causes the outage. `inc-qdi`
  (internal first-party traffic bypassed the overload controls external traffic was subject to).
- **Now:** the dispatcher semaphore caps dispatched subagent work; budget records generation-path
  spend (`or-v9f.18`). But **shadow/speculative** work (ModuleProposer shadow runs) and
  coordinator inference aren't counted against the same in-flight cap — the exact `inc-qdi` hole.
- **Acceptance:** a test proves shadow/speculative and coordinator calls draw from the *same*
  in-flight cap as interactive work, and that background classes are shed first under pressure.

### C6 — Staged rollout + kill switch · ◑
Canary / ring-deploy / feature-flag / fast rollback for prompt/model/config/skill changes.
- **Prevents:** a bad "content" update auto-deploys everywhere at once. `inc-u12` (CrowdStrike
  Channel File 291: malformed rapid-response content, no canary, global).
- **Now:** kill switch ✅ (Red Button, wired `or-v9f.14`). Staged rollout ◑: shadow→cutover
  exists for ModuleProposer + reversible LTM promotion + env feature-flags, but
  **prompts/roles/checklists are hardcoded in Go** with no versioned-config canary; no model-swap
  canary.
- **Acceptance:** a prompt/role/checklist change ships behind a versioned flag with a canary
  fraction and a one-command rollback; changing it needs no recompile.

### C7 — Validate untrusted output at every boundary · ◑
Model output *and* tool/command output (2nd-order injection, malformed JSON, oversized/binary).
- **Prevents:** one bad payload crashes the loop / injects. `inc-u12` (parser crash on malformed
  content); OWASP ASI01.
- **Now:** model *artifacts* are well-defended (sandboxed bwrap, default-deny egress, independent
  proof, secretscan, promptguard). **Tool/command *output* fed back to the loop is not guarded**
  — gap **#B1**.
- **Acceptance:** a malformed / oversized / binary tool result is rejected or quarantined at the
  boundary and cannot crash or inject the loop (fuzz test over tool-output ingestion).

### C8 — Control-plane dependency isolation · ◑
Auth/config/telemetry failures must not wedge the loop.
- **Prevents:** a hidden control-plane dependency takes everything down. `inc-3ik` (Railway: GCP
  suspension exposed a hidden control-plane dep → 8h SEV-1).
- **Now:** Polaris reachability injected; provider keys scrubbed from the sandbox (`safeenv`);
  tracker/gopls/sandbox degrade to Warn not Fail. Weakness: coordinator inference sits on the
  critical path (ADR-0001) with no shown bypass; Context Store is a single in-process mutex.
  **No test that a telemetry/config outage cannot wedge the loop.**
- **Acceptance:** a test kills each control-plane dependency (telemetry, config, Polaris,
  tracker) in turn and asserts the loop degrades (Warn) rather than wedges (Fail/hang).

### C9 — Per-step tracing / loop observability · ◑
A queryable trace per tool call and model round-trip, with retry/token/latency counts.
- **Prevents:** can't tell the agent is drifting; can't distinguish symptom from cause. `inc-qdi`
  (4h chasing a wrong mitigation).
- **Now:** self-instrumentation exists (log per state transition, phase events, per-dispatch
  trace, budget snapshots). **No first-class trace/drift surface** (Day1 Gap 3); no longitudinal
  loop-quality eval (Bayer R1).
- **Acceptance:** `orion` exposes a queryable per-step trace (tool call + model round-trip) with
  retry/token/latency counts for a completed run.

### C10 — Guard against the "misleading first fix" · ◑
Don't let auto-remediation mask the real signal; detect net-negative refinement.
- **Prevents:** auto-fix confirms a coincidence; iterative degradation. `inc-qdi` (disabled a
  feature, traffic coincidentally dropped, recurred worse); iterative-security-degradation
  research.
- **Now:** structural defense is strong (generation⊥proof wall; `EnforceObligations` →
  un-run obligation = Inconclusive, not false green; blocking AlignmentGate). **Iterative
  degradation is not specifically instrumented** — no net-negative-refinement detector.
- **Acceptance:** a refinement pass that degrades the artifact (security/quality vs the prior
  iteration) terminates the loop rather than prompting for more, and logs the regression.

### C11 — Checkpoint / resume across a provider outage · ◑
Pause-and-resume across an outage, not lost work.
- **Prevents:** an outage mid-task discards everything. Frequency of `inc-9yf`-class events.
- **Now:** crash/restart resume exists (`worktree.CreateResume`, integration crash-recovery,
  idempotent re-run skips unchanged clusters, WAL). But mid-task death restarts the task from
  scratch; **no provider-outage checkpoint** (resume a half-generated turn after a 529).
- **Acceptance:** injecting a provider outage mid-turn, then restoring, resumes the task from the
  last checkpoint without re-running proven clusters.

### C12 — Token / cost budget enforcement · ✅
Accounting + ceilings that actually halt.
- **Prevents:** unbounded spend from a runaway loop.
- **Now:** `internal/budget` — always-on token/$/wall accounting, env ceilings, warn at 80% /
  halt at 100%, generation-path recording + dispatch-gate (`or-v9f.18`).
- **Acceptance:** a run configured with a low ceiling halts at 100% and warns at 80%; the halt is
  enforced by the dispatch gate, not advisory.

## 3. Reconciliation scorecard

Living scorecard (north-star §4), cross-linked to beads and the `or-v9f.*` closure history.
Staleness note: three audit items were closed *after* the 2026-07-01 audit by `or-v9f.*` (Red
Button `Guard()` wiring, budget ceilings, AlignmentGate blocking); reflected above.

| # | Control | Status |
|---|---|---|
| C1 | Bounded retry / anti-amplification | ◑ |
| C2 | Concurrency cap | ✅ |
| C3 | Model fallback ladder | ✗ (#A12) |
| C4 | Loop-level circuit breaker | ◑ |
| C5 | Overload protection incl. internal traffic | ◑ |
| C6 | Staged rollout + kill switch | ◑ (kill switch ✅) |
| C7 | Validate untrusted output | ◑ (#B1) |
| C8 | Control-plane isolation | ◑ |
| C9 | Per-step tracing / observability | ◑ |
| C10 | Guard against misleading first fix | ◑ |
| C11 | Checkpoint/resume across outage | ◑ |
| C12 | Budget enforcement | ✅ |

**Reading of the matrix.** Orion is strong exactly where it invested in *structural* defenses
(C2, C12 done; C7/C10 well-defended at the artifact via the trust wall). The open items are
mostly "wire up a known pattern," not research.

## 4. Priority & sequencing

Two groups, ordered by blast radius:

**P0 — the `inc-qdi` cluster (multiplicative failure + overload).** C1 (stack-wide retry
budget), C5 (fold shadow/speculative/coordinator traffic into the one in-flight cap), C4
(loop-level breaker). These are the controls the corpus screams loudest about; they interlock.

**P1 — provider-degradation survival.** C3 (fallback ladder, gap #A12), C11 (outage checkpoint).
Turns a provider blip from a lost task into a pause.

**P2 — isolation, validation, observability.** C8 (dependency-outage tests), C7 (tool-output
validation, gap #B1), C9 (trace/drift surface), C6 (versioned-config canary for
prompts/roles/checklists), C10 (net-negative-refinement detector).

Each control lands behind its own fault-injection test (its acceptance criterion) and is
tier-gated where the Manifesto's calibration principle applies.

## 5. Relationship to the PRD lineage

- **`orion-formal-verification.md`** — the sibling Stage-2/3 artifact. This PRD hardens the loop
  against a degraded *dependency*; the formal PRD proves the loop's own *control-plane logic*.
  Together with the triad (product proof) they are the three failure classes the north-star doc
  separates: product correctness, runtime resilience, systemic control-plane correctness.
- **`orion-v2` / `orion-v3`** — unaffected structurally; these controls wrap the loop those PRDs
  define. C5's "shadow/speculative" hole is specifically V3's ModuleProposer shadow runs.
- **Manifesto** — this PRD is the operational expansion of failure mode #9 and the
  "Harness runtime resilience" mechanism in Part IV.
