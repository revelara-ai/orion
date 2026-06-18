---
title: Google SRE AI practices — extrapolations for Orion
type: research
origin: synthesis of two Google SRE publications (read 2026-06-18)
created: 2026-06-18
last_updated: 2026-06-18
sources:
  - "AI in SRE: Where and how Google is deploying agentic AI to improve operations" (Google Cloud Blog, 2026-05-28) — Malesevic & Heiser
  - "AI in SRE: How Google is Engineering the Future of Reliable Operations" (sre.google whitepaper) — Papapanagiotou, Malesevic, Heiser & Meshenberg
---

# Google SRE AI practices — extrapolations for Orion

Two Google SRE publications describe how Google is moving from deterministic automation to governed agentic AI across the SDLC and production operations. The overlap with Orion's design is striking — to the point that the whitepaper independently states Orion's most-contested invariant. This note records the validations, the practices to adopt now, and the practices that shape Orion's direction.

## 1. External validation (corroboration of Orion's contested choices)

| Orion principle | Google SRE statement |
|---|---|
| **Generation ⊥ proof** ("no agent grades its own homework") | **"SRE mandates the use of Independent Harnesses. The AI agent that generates the source code must be strictly isolated from the AI agent that defines the test cases or reviews the output … so that untested correctness requirements are caught mechanically rather than assumed by the authoring LLM."** |
| Intent-completeness gate; ratify specs before building | "Human oversight must shift left and move up the abstraction ladder. Engineers must focus on reviewing Designs, Intent, and Policies. By co-authoring and approving detailed specifications with AI before code generation, engineers validate the architecture and safety constraints." |
| Proof-mechanism hierarchy: deterministic > LLM-judge | "strict deterministic scoring to evaluate the final mitigation output … only scored 'correct' if the agent's output deterministically matches the exact parameters of the Golden data, rather than a vague, LLM-generated suggestion." |
| Deterministic delivery/sandbox gate, caller-agnostic | "Zero-Trust, Safe-by-Default Actuation … underlying tools must be incapable of single-handedly taking down production, regardless of who or what is calling them … It does not care if the caller is an agent or a human." |
| Decouple reasoning from execution | "By decoupling the AI's reasoning engine (AI Operator) from the execution engine (Actuation Agent), we ensure that no matter how rapidly AI models evolve, their ability to mutate production remains strictly governed by deterministic, human-controlled safety boundaries." |
| Least privilege + strong agent identity | "No Ambient Access & Least Privilege … Agent identities must be distinct from human users, strongly authenticated, and granted access only on-demand." + "every autonomous agent action must be attributed to a unique agent principal with a complete, immutable record." |
| Circuit breaker; bounded steps | "Agentic Circuit Breakers … agent-specific rate limits and automated circuit breakers … Any action performed by an agent must be highly interruptible." + "strict token management … prevents the LLM from losing context or hallucinating over time." |
| MCP for tools, A2A between agents | "AI-Friendly Tool Interfaces (MCP)" + "Inter-Agent Communication Protocols (A2A) … specialized agents collaborate … similar to how microservices interact." |

**Use:** cite Google SRE as authoritative corroboration of Orion's proof-gated, independent-harness stance in the manifesto and PRD.

## 2. Adopt into the build loop now (V2.0–V2.1)

1. **Mandatory dry-run on every mutating tool.** "Any system or API intended for agent interaction must support a declarative `dry_run=true` mode" so the agent, safety framework, and humans can predict outcome + blast radius before mutation. → Make dry-run a contract on every state-mutating tool/MCP Orion exposes; the reversibility gate uses it. Reinforces the deterministic actuation gateway agents cannot bypass.

2. **Hybrid eval — LLM-judge for *reasoning/trajectory*, deterministic for *final output*.** Nightly Evals "combine LLM-as-a-Judge (qualitative grading of intermediate reasoning, investigation trajectory, tool calls) with strict deterministic scoring of the final output." → Refines Orion's `SkillEval`: LLM-judge permitted for trajectory quality, never for the final pass/fail. (Already aligned with `docs/prompts/build-orion.md`; make it explicit in the SkillEval contract.)

3. **Bronze / Silver / Gold evaluation data + "True vs Observed precision."** Gold = human-verified; Silver = calibrated against Gold with a minimum quality threshold; Bronze = heuristic autolabels. Stratified sampling continuously surfaces incidents for human review to keep Silver calibrated, "enabling statistically significant safety margins before an agent acts." → Adopt as Orion's golden-data model behind self-evolution + the proof harness; never trust an eval against imperfect data without calibration.

4. **Frictionless golden-data capture from the human's normal workflow.** "When an oncaller declares an incident mitigated, the system proactively generates structured suggestions of the exact mitigations applied. By accepting, modifying, or rejecting these hints … SREs continuously feed high-quality Golden labels back into the system … without overhead." → Every time the Orion developer ratifies a spec, answers the completeness gate, or amends an STPA artifact, capture accept/modify/reject as a Gold label for that skill (feeds self-evolution + memory).

5. **A "Red Button."** The Actuation Agent provides "emergency 'Red Button' endpoints that allow SREs to instantly pause all in-flight agentic actions, block new actions, or globally revoke L3 permissions." → Elevate Orion's per-step circuit breaker into an explicit global pause-all / kill-autonomy control.

## 3. Direction / later phases (V2.2–V2.3+)

6. **Formal Autonomy Levels L0–L4.** A maturity ladder across operational functions (Monitor · Investigate · Mitigate · Actuate · Self-Direct): L0 Manual → L1 Assisted → L2 Partial (human approval) → L3 High (bounded autonomy, humans notified) → L4 Full (multi-step resolution). Progression is **gated by demonstrated, statistically-significant success against Golden data**, not by calendar. → This is the concrete ladder Orion's "earned autonomy" needs. Adopt L0–L4 for *delivery* (V2.3), later for ops.

7. **Safety Trifecta — add dynamic, per-action risk evaluation.** Transparency (log chain-of-thought: signals, hypotheses, options, confidence) + **Real-time Risk Evaluation** (per-action risk considering error budget, ongoing deployments, active incidents, time of day; auto-downgrade L3→L2 and route to a human when risk is elevated) + Progressive Authorization. → Orion's reliability tier is *static*; add a real-time risk check at the delivery/actuation gate that can downgrade autonomy.

8. **AI-Assisted Fix-Forward over binary rollback (the "Intervening PR Problem").** At high change velocity, rolling back to last-known-good can unwind concurrent fixes/security patches. Prefer **feature-flag disable + targeted fix-forward**. → Revisit Orion's Phase-E2 "rollback on red": for multi-change integration prefer flag-disable/fix-forward; reserve hard rollback for isolated changes.

9. **Adaptive progressive rollouts / continuous production validation at machine speed** — soak/canary become bottlenecks; need automated "continuous production validation," explicitly including data pipelines ("anomalies caught before propagating downstream"). → For V2.3 autonomous delivery, evolve the deployment bar from one-shot pre-merge proof toward continuous post-deploy validation.

10. **North star — Orion grows from *building* to *operating*.** Both papers span the full lifecycle (observe → investigate → mitigate → actuate), all of which Polaris already exposes endpoints for (control-structure, incidents, risk register). Orion is today the build/SDLC reliability layer; the same proof-gated, independent-harness, autonomy-laddered doctrine extends to **operating the software it builds**, closing the loop with Polaris. Credible direction for "the reliability layer of the agentic SDLC" to become the reliability layer of the agentic *lifecycle* — build *and* run.

## 4. Enabling-tech checklist (Google's foundation ≈ Orion's)

High-quality production data + metadata (topology, dependency graphs, historical incidents, playbooks, SLOs, **a catalog of tools and their effects**) · RAG grounding · domain fine-tuning · MCP tool interfaces · robust agent identity · A2A. Orion's Polaris-context + memory (sqlite-vec) + reliability-scan + controls catalog + MCP + A2A map onto this directly; the one explicit gap to track is a **tool-effects catalog** (what each mutating tool does + its blast radius), which the dry-run contract (#1) and the deterministic actuation gate depend on.
