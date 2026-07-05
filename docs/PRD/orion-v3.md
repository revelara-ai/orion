---
title: Orion V3 — The Anchored Module Pipeline
status: draft
authors: Joseph Bironas
created: 2026-06-20
last_updated: 2026-06-20
supersedes_flow_of:
  - docs/PRD/orion-v2.md   # V2 is RETAINED as the migration oracle; only the flow reshapes
derived_from:
  - docs/MANIFESTO.md
  - docs/research/seven-factors-orion-research.md
references:
  - docs/SPEC/Orion-TUI-ACP-Conductor.md
---

# Orion V3 — The Anchored Module Pipeline

> **Don't rewrite — evolve the deterministic spine.** ~80% of V2's value (the 3-tier
> proof, the obligation/coverage gates, the Requirement→BehavioralCase model, the
> trust wall, the Context Store + DB done-gate, the deployment bar, the native
> harness) is the proven core and carries forward untouched. V3 reshapes only the
> *flow* around it, one gate at a time, each step independently revertable.

## 0. The problem with V2

V2 **interrogates intent through fixed programmatic gates instead of listening to
clarify it.** Three structural consequences:

1. **Elicitation is a questionnaire, not a grill.** `internal/orchestrator/completeness`
   hardcodes implementation primitives (`response_format`, `timezone`, `port`,
   `route`) — it asks at the *operation* altitude when the thing that needs
   capturing is *intent*.
2. **The build is a monolith.** `internal/conductor/build.go` does
   `taskID := pv.Tasks[0].ID` — it decomposes into a DAG and then **only ever builds
   the first task.** One spec → one build → one proof.
3. **It can't survive scale.** For larger systems, context and memory are lost as
   size grows; one big proof at the end cannot catch drift. Each unit of work must
   be a **module that fits in current context and can be proven working AND aligned
   back to the original intent.**

The two things the monolith is structurally blind to are exactly the gaps: **an
adversarial review of the decomposition**, and a **per-module alignment-to-intent
gate.** That is precisely where misalignment (the or-y9d false-pass class) hides at
scale — green cases against a contract that quietly dropped the intent.

## 1. The keystone: ownership of the state machine (NOT a protocol)

A gate is *structural* — unskippable — only when the verdict is a **precondition on a
state transition, checked by the code that owns the transition**, not a tool the
reasoning agent is trusted to call. V2 already has this: the DB done-gate
(`contextstore`: a task cannot reach `proven` without an `Accept` proof row) and the
orchestrator that *runs* `ProveAll` itself (the agent never grades its own work).

**This is why V3 does NOT depend on MCP.** MCP is a transport; an MCP tool is still
called at the agent's discretion, so it provides no enforcement the orchestrator +
DB gate don't already provide for free. A foreign-agent-driven topology (your own
Claude Code drives Orion's proof core) is a *deliberate, separate* product decision —
and a plain CLI answers it first; MCP is reached for only if the ergonomics genuinely
hurt. The keystone is **state-machine ownership**, full stop.

## 2. The split of duties (seven-factors)

| Layer | Owns | Decides |
|---|---|---|
| **Reasoning** (skills/subagents — probabilistic, assumed adversarial, holds the key) | grilling, semantic vertical-slicing, adversarial issue review, code generation, alignment *judgment* | **proposes** |
| **Control plane** (deterministic Go — holds no verdict authority) | the spec anchor + `VerifyAnchor`, the completeness/provability gates, `CoverageGate`, the trust wall, 3-tier convergence + `EnforceObligations`, the `AlignmentGate` threshold, the deployment bar + tier + Red Button, the DB done-gate | **verifies** |

The **orchestrator is the one non-agentic component** — the Manifesto's defense
against the recursive trap ("an orchestrator of subagents is itself an agentic
workflow"). The hard rule that makes the split real: **no skill computes a proof
verdict in prose; the verdict is only what the deterministic proof returns.**

## 3. The flow

```
INTENT IN
  → [GRILL]            relentless listening (grill-me + grill-with-docs → write-a-prd
                       form) → a PRD-grade spec, hash-anchored. The anchor is the
                       contract; everything downstream re-grounds to it.
  → [DESIGN PROOF]     tier-gated formal model-check of the ratified spec + STPA control
                       structure (FizzBee, TLA+/Apalache escape hatch) — safety + liveness
                       on the DESIGN, before any module exists. Fires on concurrency /
                       ordering / shared-state / protocol shape or critical tier; skips a
                       stateless CRUD slice. Verified invariants compile into behavioral
                       obligations the per-module PROVE step then enforces. ← the SECOND new
                       gate. See docs/PRD/orion-formal-verification.md.
  → [DECOMPOSE]        semantic vertical-slice ISSUES (prd-to-issues tracer-bullet:
                       never a horizontal layer), each carrying its subset of
                       BehavioralCases + Done-When + DAG edges + bookend acceptance/
                       smoke issues. CoverageGate: every Requirement maps to ≥1 issue.
  → [REVIEW-ISSUES]    grill-prd's six reviewers over the ISSUE SET + its gaps;
                       IssueReviewGate blocks until patched.  ← the gate V2 never had
  → [PER-MODULE LOOP ×N]  fresh bounded context per issue, re-anchored to intent:
        spec-slice → generate (/build's gates) → 3-tier PROVE → ALIGN-to-intent
        → integrate (DAG, file-scope leases). mark_ready only if proof=Accept AND
        alignment=ok.
  → [ASSEMBLE]         DAG merge + system-level ProveAll + EvaluateBar + the bookend
                       smoke test → Deliver (human-mergeable) | Escalate (named reason).
```

Context-loss is solved by construction: per-step context is **O(module)**; the intent
anchor is **O(1) and un-evictable** (pinned in the store, hash-verified, re-read per
module — not carried in the window).

## 4. The AlignmentGate — the novel bet

A module can pass every behavioral case yet implement the *wrong thing the cases
under-specified* (Manifesto failure-mode #2: "facts stay correct; trajectory goes
wrong"). The **AlignAgent** is an adversarial LLM judge — *"does this serve what was
MEANT, not just pass the cases?"* — framed to hunt misalignment.

**It is ADVISORY and bounded. It can escalate or trigger a re-grind; it can NEVER
assert `Accept`.** `proof.Accept` remains the sole right-to-ship. It leans on
deterministic signals where it can (anchor-hash match, diff-scope vs the Wire-up
Manifest, obligation-set subset) and escalates genuine semantic drift to a human. A
rubber-stamping align judge is the single biggest correctness risk in V3 — so it is
gated to *only ever remove* a green light, never add one.

**Validated (2026-06-20, live, Step 1):** against time-service probes the judge
(a) caught a hardcoded constant that passed the RFC3339 case ("hardcoded-value-
matching-format failure"), (b) did NOT false-positive on an honest `time.Now().UTC()`
service, and (c) caught SUBTLE drift — local-time when UTC was intended — reasoning
that "RFC3339 permits any offset, so the case is satisfied while the UTC intent is
violated," and naming the missing `.UTC()`. Good recall on obvious AND subtle drift,
clean precision. (Three cases ≠ statistical proof; the judge is non-deterministic;
advisory-only stays essential.)

**When it flips to blocking (Step 3): SEVERITY-TIERED.** High-severity (a clear
intent violation) → block/escalate. Medium (ambiguity / a judgment call about an
under-specified spec, like the zone-drift case) → surface for a human, do not
auto-fail. This is the Manifesto's "escalate rather than compound," and it keeps the
judge from tyrannizing on genuinely-ambiguous specs.

**Companion design-time gate (the other new mode).** The AlignmentGate is one of *two*
new proof modes V3 introduces. The other is the **design-proof / formal-methods gate**
(`docs/PRD/orion-formal-verification.md`). They are complementary, not redundant, and sit
at opposite ends of the loop:

| | AlignmentGate | Design-proof gate |
|---|---|---|
| Asks | *post*-proof: "did the built module serve what was MEANT?" | *pre*-generation: "is the design itself race-/deadlock-free?" |
| Catches | semantic drift the behavioral cases under-specified | systemic design races no per-module product proof can see |
| Placement | end of the per-module loop (align stage) | after GRILL+STPA, before DECOMPOSE |
| Verifier | adversarial LLM judge (non-deterministic, advisory) | deterministic model checker (over a human-ratified model) |

Both are tier-gated, both roll out advisory→blocking, and **both can only ever *remove* a
green light, never add one** — `proof.Accept` remains the sole right-to-ship.

## 5. Reuse vs replace (honest)

- **KEEP (the proven core):** `internal/proof` (behavioral+mutation, empirical, hazard)
  + `truthalign.ConvergeFull` + `EnforceObligations`; the Requirement→BehavioralCase
  model; the trust wall; `internal/contextstore` (hash-anchored, DB done-gate,
  Recall-after-kill); `internal/delivery` bar + tier + Red Button; the native harness
  (`internal/harness` + `internal/llm` + `internal/tools` + `NativeGenerator`).
- **REPLACE:** the fixed `completeness` checklist *as the elicitation driver* (demote
  to a project-type reliability *floor*); the mechanical `decomposer` (→ semantic
  ModuleProposer + adversarial review, keeping `CoverageGate` as the backstop); the
  monolithic `BuildAndProve` (→ the per-module loop ×N).
- **SEPARATE EPIC, do not conflate (the thrash to avoid):** or-3ba — the proof is
  HTTP-shaped (empirical port-probe, behavioral's single `handleTime` symbol).
  Generalizing beyond HTTP is the prerequisite for "any software" but **not** for the
  reshape; Steps 0–3 work on the existing HTTP path.

## 6. Migration — incremental, the spine never moves

- **Step 0** — extract the phase state machine from `BuildAndProve` as a *refactor*
  (no behavior change; green tests = safe).
- **Step 1 — THE FIRST BET (cheapest demonstration of the whole thesis):** add the
  **AlignmentGate, log-only, AFTER existing proof.** Dogfood it against a
  proof-*passing*-but-misaligned service and show it **flags the drift the cases
  missed.** Touches nothing upstream; fully reversible.
- **Step 2** — *if/when* foreign-agent interop is wanted: a CLI (`orion prove <module>`)
  first; not MCP.
- **Step 3** — semantic ModuleProposer in *shadow* against the old decomposer (assert
  new-coverage ⊇ old), then cut over + wire the per-module **loop** (the real DAG, not
  `Tasks[0]`); the AlignmentGate now **blocks**.
- **Step 4** — the IssueReviewGate (grill-prd fan-out over the issue set).
- **Step 5 (riskiest, last)** — swap the elicitation driver: demote the fixed checklist
  to a reliability floor; let the GrillAgent drive open-endedly.

Delete the V2 checklist / decomposer / monolithic build **only** when their
replacements have shipped a real epic each. The V2 monolith stays runnable as the
**oracle** to diff against.

## 7. Risks

1. **The AlignAgent is itself an LLM judgment** — the exact "agent grading homework"
   the Manifesto forbids. Mitigation: advisory-only, adversarial framing,
   deterministic signals where possible, *can only ever escalate/re-grind*.
2. **Advisory-vs-structural** (the 2026-05-08 postmortem): only the parts owned by the
   deterministic state machine / DB gate are real; skill prose is gameable.
3. **Composition:** `CoverageGate` proves every requirement is *mapped*, not that the
   modules *compose* to the intent — weight lands on grill quality + the bookend
   acceptance test.
4. **Cross-module assembly is genuinely new** (V2's `FileScope` integration is a stub)
   — its own epic. Worse than a stub: the integration-time file-scope leases
   (`internal/integration.AcquireLease` / `ReleaseLease`) are **dead code** — never called
   in production. Invariant **S1** (no two overlapping-scope tasks integrate concurrently)
   holds only *incidentally*, because `integrateEpic` loops sequentially; the moment someone
   parallelizes it, overlapping merges can race with only after-the-fact git conflict
   detection as a backstop. Tracked as **`or-1lz`** (P1); its formal model is the worked
   example for the design-proof gate in
   `docs/research/2026-07-02-provably-correct-agentic-sdlc.md §5.5`.
5. **Cost/latency:** fresh-agent-per-module is far more spend than the monolith — needs
   a "small spec → single module" fast path (the budget accountant bounds it).
6. **The orchestrator must stay non-agentic** — its bugs are systemic; never make it
   "agentic to help."

## 8. The first experiment (Step 1)

**Does a log-only AlignmentGate flag the drift that the green cases hid?** Dogfood it
against a service that *passes* proof but violates intent (e.g. returns a hardcoded
RFC3339 timestamp — every "time" case passes, but it isn't the current time). If the
judge catches it, the most novel part of V3 is validated for a few days' work, against
the proven core, with zero risk upstream. If it doesn't, we've learned the align judge
is the hard problem *before* betting the architecture on it.
