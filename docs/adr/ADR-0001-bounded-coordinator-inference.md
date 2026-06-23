# ADR-0001: Bounded Coordinator Inference

- **Status:** Accepted
- **Date:** 2026-06-23
- **Deciders:** Joseph Bironas
- **Epic / slice:** or-hd3 → or-hd3.1 (slice 0)
- **Related:** [docs/PRD/orion-v2.md § Bounded Coordinator Inference](../PRD/orion-v2.md); [docs/SPEC/Orion-Memory-Recall-Design.md §2](../SPEC/Orion-Memory-Recall-Design.md); or-5ym (`internal/proof/safeenv`)

## Context

The PRD's control-plane thesis originally asserted Orion *"holds no API key and makes no inference calls"* — Orion spawns the developer's own agents and drives them over ACP/A2A under the developer's authentication. Two forces make the absolute form untenable:

1. **Memory & recall (or-hd3) needs embeddings.** sqlite-vec ANN retrieval requires embedding vectors; there is no clean way to produce them purely by delegating to a spawned coding agent.
2. **Orion-as-coordinator benefits from in-process reasoning.** Relevance ranking, memory summarization, and drift/anchor scoring are coordination concerns, not artifact generation.

The bend already exists in the codebase: `internal/llm/anthropic.go` is a real LLM client, `internal/tui/conversation.go` selects a **native LLM Conductor** when `ANTHROPIC_API_KEY` is present, and `internal/proof/safeenv` already scrubs that key from proof execs (deny-by-default). An absolute "no inference" principle is therefore both impractical and already contradicted.

## Decision

Reformulate the absolute into a **bounded** invariant:

> Orion is a control plane, not a generation engine. It makes **no generation or proof inference calls** — producing artifacts and computing proof verdicts are delegated to the sandboxed fleet under the developer's auth and gated by independent proof. Orion **may make bounded coordinator-role calls** directly (embeddings, recall/relevance ranking, memory summarization, drift/anchor scoring, lightweight intent/plan reasoning) via a configured, swappable provider (local default, cloud opt-in). Coordinator output is control-domain context only: never a shippable artifact, never a proof input.

The boundary chosen is **coordinator-role** (over the narrower "memory-infra-only" and the broader "full in-process LLM client"). The normative direct-vs-delegated classification lives in the PRD section. A new memory provenance `coordinator` is introduced; coordinator output is control-domain only.

## Consequences

**Positive**
- Memory can embed (local default, cloud opt-in) and the Conductor can reason in-process; the principle now matches the code.
- The structural proof walls are unchanged: coordinator output is never a proof input, the verdict stays deterministic and harness-collected, and proof reads the spec directly (Trust invariant 7).

**Negative / risks**
- Orion holds a provider key (already true post native-harness pivot). Key-hygiene surface grows; mitigated by `safeenv` deny-by-default, OS-keychain / `0600` storage, and the key being unreachable inside the sandbox.
- Coordinator inference costs tokens; it MUST be tracked by the budget accountant and surfaced in the TUI.
- Scope-creep risk toward a "broad LLM client." Mitigation: the classification table is normative; generation and proof stay delegated; revisit this ADR if coordinator calls expand beyond the listed set.

## Alternatives considered

- **Keep the absolute** ("no API key / no inference"). Rejected: impractical (blocks vec recall), and already contradicted by the native Conductor.
- **Memory-infra-only** (key only for embeddings/summarization/reindex; all reasoning delegated). Rejected: too narrow — Conductor relevance/drift reasoning benefits from in-process calls too.
- **Broad coordinator** (full in-process LLM client except sandboxed execution + deterministic verdict). Rejected: erodes the delegation thesis and maximizes key-exposure surface.
