---
title: Orion Formal Verification — The Design-Proof Gate
status: draft
authors: Joseph Bironas
created: 2026-07-03
last_updated: 2026-07-03
derived_from:
  - docs/MANIFESTO.md
  - docs/research/2026-07-02-provably-correct-agentic-sdlc.md   # Stage 1 north-star (§5, §6.2, §7, §8)
complements:
  - docs/PRD/orion-v2.md            # the triad — product proof; consumes this gate's obligations
  - docs/PRD/orion-v3.md            # the AlignmentGate — the *other* new proof mode
  - docs/PRD/orion-brownfield.md    # the model must evolve with a changing design
references:
  - docs/SPEC/Orion-Worktree-Git.md          # §9 — the S1/S2 integration invariants
  - docs/SPEC/Orion-Obligation-Vocabulary-Design.md
  - internal/reliabilitytier
  - internal/proof
---

# Orion Formal Verification — The Design-Proof Gate

> **The second proof.** Orion's triad proves the *product* — that the code does what the
> design meant. It cannot prove the *design itself* is coherent: no race, no deadlock, no
> reachable unsafe state under concurrency and failure. That is a different question, it is
> answerable, and the tool for answering it is a **model checker**. This PRD specifies the
> design-time formal-verification gate — a fourth proof mode, run *before* generation — and
> the refinement chain that carries its guarantee into the artifact-time triad.

## 0. Why this exists

The Manifesto now commits to two proofs, not one (thesis #3, "multi-modal *and multi-phase*";
thesis #4, "completeness *and* soundness"). The triad establishes **product proof** at
artifact-time. This module establishes **design proof** at design-time. The failure class it
closes is invisible to everything Orion has today:

- The **triad proves products**, each in isolation — it cannot see a bug that only manifests
  in the *interleaving* of two independently-correct modules.
- The **runtime-resilience checklist** (`docs/PRD/orion-resilience.md`) hardens the *loop*
  against a degraded dependency — it says nothing about design correctness.
- The **AlignmentGate** (V3) catches *semantic* drift the behavioral cases under-specified —
  it is an LLM judge, not a proof, and it reasons about intent, not about state-space safety.

None of them reach a **systemic control-plane bug**: a wrong state transition, a lease race, a
queue deadlock. §5.5 of the north-star doc found a *real, currently-latent* one in Orion's own
integration queue (tracked as `or-1lz`, P1). The design-proof gate is what reaches that class.

## 1. What it is and where it sits

A **formal-methods module**: a proof gate Orion applies to *the product it was asked to build*,
tailored per-project. When the ChangeSpec involves a concurrent protocol, a state machine, or an
ordering/safety invariant, Orion synthesizes a formal model of *that specific design*,
model-checks it for safety and liveness, and the result becomes a verdict.

It runs **design-time** — after the executable spec and STPA, **before generation** — for four
reasons: (1) model checkers verify *models*, not code, so this is the honest placement;
(2) it is the cheapest place to kill a concurrency bug — before 20 files exist; (3) it composes
with the STPA hazard work Orion already does; (4) it satisfies the Manifesto tenet *"intent must
be complete before code is written"* — specifically its *soundness* half.

In the V3 pipeline it is the gate between `[GRILL]` and `[DECOMPOSE]`
(`docs/PRD/orion-v3.md §3`). Conceptually it is a **fourth proof mode** — *formal* —
alongside behavioral / empirical / hazard.

### 1.1 The refinement chain (load-bearing)

The gate does **not** re-check the artifact at convergence. Its verified invariants **compile
into behavioral proof obligations** (model-based test generation), so the artifact-time triad
proves the code *implements the verified design*:

```
STPA UCA  →  formal invariant  →  model-check (safety+liveness)  →  behavioral obligation  →  generated test  →  code
 (hazard)      (design proof)         (FizzBee/TLA+)                  (coverage gate)          (behavioral)   (empirical)
```

This chain is what carries a design-time guarantee into shipped code. If obligation generation
is weak, the design proof is decorative (see Risk R2). The chain is the product, not the check.

## 2. Trust story

Identical posture to STPA, which Orion already trusts:

- An **LLM drafts** the model.
- The **developer ratifies** it (like the STPA questionnaire) — the model is a **proof-domain
  artifact**, not a generation-fleet artifact. The untrusted generation fleet never authors its
  own formal proof.
- The **model checker is deterministic.**

A wrong model yields a wrong-but-*inspectable* spec that the human ratifies and the checker
exercises; it cannot manufacture a false green, because a green model-check is only ever
`Accept`-eligible for the *design*, and the design is human-ratified. Like the AlignmentGate,
this gate can **only ever remove a green light, never add one** — `proof.Accept` remains the
sole right-to-ship.

## 3. Tier-gating

Per the Manifesto tenet *"reliability is calibrated to the project, not maximized blindly,"* the
gate is **tier-gated** off `internal/reliabilitytier` and the design's shape:

- **Fires** when tier = `critical`, **or** the design exhibits concurrency / ordering /
  shared-state / protocol / distributed-consensus structure — detectable from the spec + the
  STPA control structure.
- **Skips** a stateless CRUD endpoint at `throwaway` / `standard` tier. Model-checking it is
  waste, and the Manifesto forbids over-engineering as explicitly as under-protecting.

## 4. Backend: FizzBee by default, TLA+/Apalache as the escape hatch

**FizzBee** is the default. Its language is **Starlark (a Python subset)** — specs are
Python-readable and REPL-testable, which matches a Python-first team. It checks **safety**
(`always assertion`; TLA+ `[]`), **liveness** (`always eventually` / `eventually always`), and
**invariants**, with atomicity made explicit (`atomic` / `serial` actions, `oneof`, `parallel`,
`any`) and built-in probabilistic/performance analysis via Markov reachability.

Design around its current limits: **deadlock detection and full liveness/fairness are still
early, and functions don't take parameters yet.** So the module keeps **TLA+/Apalache as the
escape hatch** for hard liveness/deadlock cases and distributed consensus, with FizzBee as the
default for safety-invariant checking on ordinary designs. Backend selection is a per-model
decision recorded on the model artifact (§6).

## 5. Worked example — the dogfood defect (`or-1lz`)

The canonical demonstration is a genuine open invariant in Orion's own control plane:
**file-scope mutual exclusion during integration.** Full model in
`docs/research/2026-07-02-provably-correct-agentic-sdlc.md §5.5`; summary:

- **Invariant S1** — no two tasks with overlapping declared file scope integrate concurrently.
- **Invariant S2** — at most one integration onto the head at a time.
- **The gap** — `internal/integration.AcquireLease` / `ReleaseLease` implement S1 exactly, but
  are **dead code** (called only in tests). S1 holds today only *incidentally*, because
  `integrateEpic` (`internal/conductor/dagintegrate.go`) loops sequentially. Parallelize that
  loop — the obvious next step, since the build phase is already `maxConc`-parallel — and S1 is
  silently violated, with only after-the-fact git rebase-conflict detection as a backstop.

With the lease guard modeled, FizzBee proves both invariants hold across the state space;
without it, the checker returns a counterexample interleaving in milliseconds. Orion then cashes
the verified guard out as a **behavioral obligation** — a generated test asserting
`Integrator.AcquireLease` refuses overlapping scope and that `integrateEpic` acquires before
merging — which the artifact-time triad enforces on the code. *A systemic control-plane bug,
invisible to per-task product proof, caught by design proof, and kept caught by a behavioral
obligation.*

## 6. Schema & artifacts

| Artifact | Description |
|---|---|
| **Model artifact** | A first-class, hash-anchored proof-domain object: the model source (FizzBee/TLA+), the backend selection + rationale, the ratification record (who/when), the seeding STPA UCAs, and the emitted behavioral obligations. Stored in the Context Store, versioned with the spec. |
| **Trigger policy** | Per-tier + per-shape rule set (§3): the predicate that decides *fire vs skip* from `reliabilitytier` + the spec/STPA control structure. |
| **Verdict** | `Pass` (invariants hold) · `Fail` (counterexample trace attached, blocks/flags the design) · `Inconclusive` (model too large / unsupported property → escalate to human design review — never a silent pass). |
| **Obligation set** | The behavioral obligations compiled from verified invariants, handed to `internal/proof` for the artifact-time triad (the §1.1 chain). |

## 7. Hooks (where the code changes)

- **`internal/proof`** — register `formal` as a new proof mode; add obligation compilation
  (verified invariant → behavioral obligation) so the triad consumes design-proof output.
- **`internal/decomposer` / `internal/orchestrator`** — place the gate design-time (after
  spec+STPA, before decompose); implement **STPA-UCA → invariant synthesis** (the LLM draft
  step) and the ratification handoff.
- **`internal/reliabilitytier`** — expose the tier + shape predicate the trigger policy reads.
- **New: `internal/proof/formal`** (or equivalent) — the FizzBee/TLA+ runner, deterministic,
  executed under the same sandbox constraints as other proof execs (`safeenv`; the model
  checker binary bound into the sandbox, not `make`/dynamically-linked shells — see the
  `tooling-change-proof-sandbox-constraints` memory).

## 8. The dogfood corollary — verify Orion's own control plane

The gate is built to verify *products*, but Orion's own orchestrator — the integration queue,
path leases, done-gate transitions, DAG scheduler — is **exactly the concurrent state machine
the gate checks**, and §5 already found a real latent defect in it. So the gate is pointed at
Orion itself. Control-plane invariants that become model-checked artifacts in Orion's CI:

- **S1 / S2** — integration file-scope mutual exclusion + singleton head integration (`or-1lz`).
- **Done-gate soundness** — `proven` is reachable *only* through an `Accept` proof row.
- **DAG liveness** — every ready task eventually integrates or escalates; no deadlock.

This closes the recursive trap at the level the Manifesto now demands: Orion does not merely
*assert* its orchestrator is the trustworthy non-agentic core — it **proves** it, with the same
class of tool it uses to prove everything else. *The prover is itself proven.*

**Rollout decision (Risk R6):** control-plane verification starts as an **advisory** CI check
and flips to **blocking** only once stable — mirroring the AlignmentGate's advisory→blocking
path — so a flaky/immature checker cannot wedge Orion's own CI.

## 9. Staging

Mirrors the V3 discipline: land each step behind its own tests; keep it advisory before blocking.

1. **Backend spike** — stand up the FizzBee runner (+ TLA+/Apalache escape hatch) under the
   proof sandbox; model the `or-1lz` S1/S2 invariants by hand; confirm the counterexample.
2. **Dogfood, advisory** — wire the hand-written S1/S2/done-gate/DAG models as an *advisory* CI
   gate on Orion itself. Ship the `or-1lz` fix (independent of this PRD) alongside.
3. **Obligation compilation** — verified invariant → behavioral obligation → generated test;
   prove the §1.1 chain end-to-end on the `or-1lz` model.
4. **Synthesis, advisory** — STPA-UCA → model synthesis for a *product* spec (LLM draft + human
   ratify); run design-proof log-only in the V3 pipeline against a concurrent-design fixture.
5. **Blocking + tier-gating** — flip the gate to blocking under the trigger policy; flip the
   Orion-CI dogfood gate to blocking once stable.

## 10. Open questions & risks

1. **R1 — Spec → model synthesis reliability.** The hardest LLM step. Mitigation: the model is a
   ratified, inspectable proof-domain artifact (STPA posture) and the check is deterministic —
   but a model that doesn't *faithfully abstract the design* yields a green check on the wrong
   thing. **Faithful abstraction is the real risk, not checker soundness.**
2. **R2 — Green model ≠ correct code.** Model checking proves the *design*, not the
   implementation. The §1.1 refinement chain is what carries the guarantee into code; if
   obligation generation is weak, the design proof is decorative. This chain is load-bearing.
3. **R3 — FizzBee maturity.** Deadlock detection and full liveness/fairness are early. Policy
   needed: when to auto-escalate to TLA+/Apalache (likely: any liveness/deadlock-critical
   invariant, or distributed consensus).
4. **R4 — Cost/latency at scale.** State-space explosion. Tier-gating bounds *when* it runs;
   model-size discipline (abstract to the invariant, not the whole design) bounds *how big*.
   Needs a per-tier time budget and a "model too large → `Inconclusive` → human design review"
   fallback (never a silent pass).
5. **R5 — Brownfield model drift.** For `orion change`, the design evolves; the model must be
   maintained or regenerated per change and reconciled against the existing code's implicit
   invariants (brownfield's "two masters" — see `docs/PRD/orion-brownfield.md`). Unowned models
   rot.
6. **R6 — Scope of the dogfood pass.** §8 adds a formal-methods dependency to Orion's own CI.
   Advisory-first (mirror the AlignmentGate) before blocking.

## 11. Relationship to the PRD lineage

- **`orion-v2`** (the triad) — unchanged as the migration oracle; its `internal/proof` becomes
  the **downstream consumer** of this gate's obligations (§1.1). One-line note, no structural
  edit.
- **`orion-v3`** (the AlignmentGate) — the *sibling* new proof mode. See the comparison table in
  `orion-v3.md §4`: alignment is post-proof/semantic/advisory-LLM; this is
  pre-generation/structural/deterministic. Complementary, not redundant.
- **`orion-brownfield`** — inherits R5 (model drift): the formal model is a *third* artifact
  under version pressure alongside new-behavior and preserved-behavior.
- **`orion-generalization`** — mild prerequisite: shape-detection for the trigger policy (§3)
  depends on the projectType/tier threading that generalization is completing (its open item #3).
