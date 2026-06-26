---
title: "Gap Analysis — Google 'Day 1: The New SDLC With Vibe Coding' vs. Orion"
type: research
origin: paper analysis (C:\Users\josep\Cowork OS\Papers\Day_1_v3.pdf)
created: 2026-06-26
paper:
  title: "The New SDLC With Vibe Coding: From ad-hoc prompting to Agentic Engineering"
  authors: Addy Osmani, Shubham Saboo, Sokratis Kartakis (Google)
  series: "Google Agents Whitepaper Series — Day 1 of 5"
  date: May 2026
---

# Day 1 SDLC paper → Orion gap analysis

## The paper in one paragraph

Software engineering is shifting from **writing code** to **expressing intent**, on a
spectrum from casual **vibe coding** (unstructured prompting, "does it seem to work?")
to disciplined **agentic engineering** (AI as an implementation engine inside human-
designed constraints, tests, feedback loops, guardrails). The differentiator is *not*
whether you use AI but **how much structure, verification, and human judgment surrounds
its output**. Core mental models: **Agent = Model + Harness** (the harness is ~90% of the
system, "the team's surface area, not the model provider's"); the **factory model** (the
developer's output is not code but *the system that produces code*); **context
engineering** (the central skill — providing structured codebase/architecture/intent
context) over prompt engineering; verification via **tests** (deterministic) **+ evals**
(non-deterministic — trajectory + quality, scored by datasets/rubrics/**LM judges**).

## Verdict

Orion is a **rigorous, opinionated instantiation of the paper's agentic-engineering
endpoint** — further down that axis than the paper describes as *current* practice. The
paper's top verification primitive is **evals/LM-judges** (an AI grading an AI); Orion's
is **independent multi-modal proof** ("no agent grades its own homework"). On the paper's
own central risk — *"a fluent output that skipped its verification steps is a more
dangerous failure than one with a visible error"* — **Orion's architecture is the
stronger answer.** But Orion under-builds the *soft* harness layers the paper spends most
of its pages on: context-engineering-as-versioned-config, **skills**, **memory
population**, **observability**, and an eval layer for the **non-provable residual**
(intent-alignment, taste, trajectory sanity).

## Where Orion already embodies the paper (often exceeding it)

| Paper concept | Orion principle | Orion practice |
|---|---|---|
| Express **intent**, not code; "specs become eval criteria" | Intent as an *executable contract* | Completeness gate → anchored/hashed `ExecutableSpec` + behavioral cases as **proof obligations** (machine-checkable + tamper-anchored, stronger than "specs as eval criteria") |
| **Agent = Model + Harness** (harness ≈ 90%) | The **commodity-model principle** | Orion *is* the harness; drives the developer's own agent over ACP — model-agnostic by construction |
| **Factory model** (output = the system that makes code) | Proof-gated control plane | Conductor → decompose → worktree-DAG → prove → deliver |
| Verification mandatory; tests **+** evals | `proof.Accept` is the *sole* right-to-ship | Multi-modal proof: behavioral + empirical (live probe) + **hazard/STPA** + mutation testing |
| Trust boundary / sandboxes / deterministic guardrails | Side-effect sandboxing, trust-domain isolation | bwrap netns + `safeenv`, network-deny-by-default, **Red Button** autonomy revocation |
| Provenance / auditability | Durable Context Store as source of truth | **Cryptographic anchoring** (spec hash) + content-hash artifacts + idempotent records — the paper *explicitly disclaims this level* |
| Context engineering | The contextengine + completeness gate | Trust-**tiered** bundle assembly with a generation-tier **poisoning quarantine** — a dimension the paper (context-rot only) doesn't reach |

## The sharpest divergence: proof vs. evals

The paper's verification stack tops out at **evals + LM-judges + trajectory evaluation**
— at root, *an agent grading an agent*. Orion's founding move is the **generation⊥proof
wall**: re-verify the *artifact* independently with deterministic, sandboxed checks, and
never trust the agent's own verification. So Orion does not need "trajectory eval" to
catch *"the agent skipped verification"* — it never trusts that verification at all. That
is Orion's structural advantage.

But evals also cover what proof does **not**: the **non-deterministic, qualitative**
layer — *does the code serve the intent (not just pass the cases)? is the trajectory
sane? is it tasteful?* Orion only **stubs** this today (the **log-only `AlignmentGate`**,
V3 Step 1). **The productive synthesis:** grow a real judgment/eval layer for the
non-provable residual **without breaking the wall** — advisory + independently arbitrated,
never the generation agent grading itself. → **Gap 1.**

## Gaps to close (paper concepts Orion under-implements)

1. **Wall-safe eval/judgment layer for the non-provable residual** — grow the AlignmentGate
   beyond log-only (intent-alignment, trajectory sanity, "serves the intent not just the
   cases"), kept strictly advisory + independent of the generation agent. → **bd: filed.**
2. **Harness config as versioned, reviewable artifacts** — the paper: treat AGENTS.md /
   prompts / checklists / eval suites "as code, reviewed in PRs, owned by named
   engineers." Orion's equivalents (`generationRole`, completeness checklists) are
   **hardcoded in Go**. Externalize to user-versioned, reviewable config. → **bd: filed.**
3. **First-class observability / tracing / agent-drift detection** — the paper: *"without
   observability there's no way to tell if the agent is quietly drifting."* Orion has the
   budget accountant + phase events but no trace/drift surface. (Adjacent to parity A17
   `doctor`/`status`, but broader.) → **bd: filed.**
4. **Per-task model routing / token-aware dispatch** — frontier-for-hard, cheap-for-easy.
   Orion's commodity stance partly sidesteps this; per-task routing is absent. → **bd: filed.**
5. **Memory & context (already prioritized).** The paper validates this hard (memory is 1
   of 5 agent parts; "context rot"; an entire companion paper, Day 3). Orion's
   `memory`+`contextengine` are *structurally ahead* (tiered + trust-quarantined) but
   **functionally empty** — exactly **or-hd3 / or-3ba.6**. The paper's **static-vs-dynamic
   context** split and "treat the boundary as first-class versioned config" is direct
   design guidance for the write-side schema (noted on or-hd3).
6. **Skills / procedural memory (already in the parity backlog).** The paper's progressive-
   disclosure "Agent Skills" validates **or-ykz.8/.9/.10** (A7/A8/A9).

## Deliberate divergences (hold the line — not gaps)

- **Orchestrator-only.** The paper embraces both *Conductor* (real-time, in-IDE, keystroke)
  and *Orchestrator* (async delegation). Orion is async-proof-gated **by design** — you
  cannot proof-gate keystroke-level co-editing. Do **not** chase Conductor mode.
- **Local-first** vs the paper's Google-Cloud/ADK lean.
- **Proof as the authority** vs evals: evals may be *added* (advisory), but proof stays the
  gate.

## What to steal

- The **static/dynamic context** framing + "first-class versioned config" discipline →
  directly shapes the **or-hd3 memory write-side schema** (static/pinned vs dynamic/
  windowed; the **eviction policy that *is* the degradation defense**).
- A real **judgment/eval layer** for the AlignmentGate residual — wall-safe.
- The **80% problem** as a roadmap test: Orion bets *"proof + elicitation closes the 20%."*
  The paper's reminder that the 20% is *architecture + ambiguity + subtle correctness* is a
  good audit of whether the completeness gate + hazard proof actually cover it.

## Net

This paper is the **market description of the category Orion is building the rigorous end
of.** Value: external validation, a shared vocabulary (harness, context engineering,
factory model), and a checklist of the soft layers to add **around** Orion's hard proof
core — without diluting the proof core itself.
