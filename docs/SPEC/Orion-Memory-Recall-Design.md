---
title: Orion Memory & Recall — Design (functional recall + context-degradation defense)
status: approved
created: 2026-06-23
authors: Joseph Bironas
epic: or-hd3
related:
  - docs/PRD/orion-v2.md            # module 6 (memory), Memory & Self-Evolution Hardening
  - docs/SPEC/Orion-Triad-Reconciliation.md  # row 6: MemoryOS lineage
supersedes_issue: or-3ba.6          # absorbed into slice 1 (the spine)
---

# Orion Memory & Recall — Design

> Design pass for epic **or-hd3** (P0). Brainstormed + approved 2026-06-23. Terminal output of
> this doc is an implementation plan (writing-plans) + an 8-slice beads decomposition built via the
> tracer-bullet loop.

## 1. Context & problem

Orion has `internal/memory` (tiered STM/MTM/LTM store, SQLite/WAL, trust-tier + heat fields) and
`internal/contextengine` (the sole bundle assembler — trusted/untrusted partitioning with a
generation-tier quarantine block, plus `AssembleForProof`). `or-b73` wired the **read/assemble** side
into `BuildDAG`. But the subsystem is **operationally hollow**: nothing writes memory during the loop,
so recall returns only spec constraints; `Store.Write/Pin/EvictToCapacity` and `Engine.AssembleForProof`
are deadcode-flagged (test-only callers). Heat is static (the MemoryOS feedback loop isn't live),
eviction is never triggered (no degradation defense), there is no summarize-then-evict and no MTM→LTM
promotion.

**Lineage.** The Triad reconciliation (row 6) shows the original design used an *external* "Memory OS
via gbrain-explorer" (Gemini Flash/Pro). V2 replaced it with Orion's own `memory` module —
**MemoryOS-style STM/MTM/LTM, SQLite+vec, model-agnostic** — and added what MemoryOS lacks:
**trust-tier partitioning (generation ⊥ proof)**, so one substrate defends against *both* context
erosion *and* memory poisoning.

**Goal of or-hd3.** Make recall **functional end-to-end** (a build populates memory → a later task's
assembled bundle recalls it → a generation-tier item is quarantined into the `<<<UNTRUSTED` block) and
**defeat context degradation** (heat-retained, bounded, summarize-don't-drop tiers + within-project
promotion), with semantic (vector) retrieval.

**Three failure modes, kept distinct** (this subsystem owns the first; the others are cross-checks):
*context erosion* (slow death of long runs → tiering + heat + eviction + pins), *drift*
(re-anchor; existing), *poisoning* (trust-tier quarantine; existing, extended to the new paths).

## 2. Foundational decision: bounded coordinator inference

This epic forced a reformulation of a load-bearing PRD principle. The PRD says Orion *"holds no API
key and makes no inference calls."* Memory needs embeddings, and the Conductor / Orion-as-coordinator
benefit from in-process reasoning (relevance, summarization, drift). Approved boundary: **coordinator-role**.

> **Reformulated invariant.** Orion is a control plane, not a generation engine. It makes **no
> generation or proof inference calls** — producing artifacts (code/tests/instrumentation) and computing
> proof verdicts are delegated to the sandboxed fleet under the developer's auth and gated by
> independent proof. Orion **may make bounded coordinator-role calls** directly — embeddings,
> recall/relevance ranking, memory summarization, drift/anchor scoring, and lightweight intent/plan
> reasoning — via a **configured, swappable provider** (local default, cloud opt-in). Coordinator output
> is **control-domain context only**: never a shippable artifact, never a proof input. The
> commodity-model principle applies to coordinator calls too — no requirement may depend on a specific
> model.

**Classification (normative).**

| Call | Who | Notes |
|---|---|---|
| Embeddings (recall) | **Orion direct** | local default; cloud opt-in |
| Recall relevance / rerank | **Orion direct** | control-domain |
| Memory summarization | **Orion direct** (opt-in) | default extractive/deterministic; LLM only when configured |
| Drift / anchor scoring | **Orion direct** | cheap-first; existing path may move in-process |
| Lightweight intent/plan reasoning | Orion direct *or* spawned Conductor | status quo = spawned agent; direct allowed |
| Code/test/instrumentation generation | **Delegated (fleet)** | never Orion-direct |
| Proof (behavioral/empirical/hazard) + verdict | **Delegated + deterministic** | verdict harness-collected; proof reads spec directly |
| Conflict-resolution merges | **Delegated (fleet)** | generation domain |

**Trust-domain treatment.** A new provenance, `coordinator`, joins `human|proof|generation`.
Coordinator output is **control-domain context only**: a coordinator *summary of a proof fact* is
control-tier context (not a proof input — proof still reads the spec directly, Trust invariant 7); a
coordinator *summary of a generation narrative* stays `generation`-tier (quarantined). The proof bundle
continues to exclude all generation memory.

**Security.** The coordinator provider key lives in the OS keychain (or encrypted `0600`), is loaded
via `safeenv`, and is **never reachable from inside the sandbox** — formalizing the posture Orion
already partly has (it holds `ANTHROPIC_API_KEY` post native-harness pivot; see or-5ym). Generated/proof
execs keep running under deny-by-default env.

**Impact (tracked as slice 0).** This is a PRD-level change: a PRD amendment section (reformulating the
core-thesis sentences) + an ADR ("Bounded coordinator inference; generation/proof stay delegated") +
cost-governance note (coordinator spend counts against the budget accountant). It unblocks the
embedder, vec, and optional-LLM-summarizer work.

## 3. Write-side schema — what is written, when, at which tier, with what trust tag

Single new writer in `internal/conductor/build.go::buildOneTask`, after `ProveAndCloseReport`.

| Trigger | Content | Kind | Tier | Trust tag |
|---|---|---|---|---|
| Spec accepted | intent + constraints | `spec` | LTM | **human** · pinned |
| Decision ratified (grill) | decision key=value | `decision` | MTM | **human** · pin if security/critical |
| Proof **Accept** | structured outcome (modes, mutation score, empirical pass-rate, hazard counts) | `pattern` | MTM | **proof** |
| Proof **Reject/Inconclusive** | structured failure facts (which mode, metric deltas) | `failure` | MTM | **proof** |
| Proof **Reject/Inconclusive** | agent narrative ("what went wrong / try X") | `failure` | MTM | **generation** → quarantined |
| Coordinator derivation | summaries / relevance notes Orion produces | `summary` | MTM | **coordinator** |

**Correctness hinge:** harness-derived ⇒ `proof`; agent-narrated ⇒ `generation` (quarantined);
Orion-coordinator-derived ⇒ `coordinator` (control-domain, never a proof input). Mis-tiering here is the
poisoning vector the trust wall exists to stop.

## 4. Heat model (live MemoryOS dynamics)

Replace static `Heat` with `heat = w_r·recency_decay(last_accessed_at) + w_f·log(1+visit_count)`.
Add `visit_count` to the schema. `Retrieve` increments `visit_count` + updates `last_accessed_at` for
every returned item (the recency/frequency feedback loop). Decay is computed **lazily at query time**
(no background job). Weights `w_r`, `w_f`, and the decay half-life are config.

## 5. Retention: eviction + summarize-then-evict (the degradation defense)

The loop calls `EvictToCapacity` per tier after each task (caps in config). Replace today's hard-drop
with **summarize-then-evict (2PC)**: (1) write+flush an extractive summary (`Kind=summary`,
deterministic — no model, preserves the offline path) → (2) only then drop the raw page; idempotent by
`content_hash`. A crash between phases leaves the raw page intact. `pinned` and `security_relevant`
items are **never lossy-summarized** (carried as structured records). An optional coordinator-LLM
summarizer (slice 3, behind provider config) may produce richer summaries when configured; extractive
remains the default.

## 6. Promotion MTM→LTM (within-project only)

On `heat > promote_threshold` **and** `visit_count ≥ promote_min_visits`, promote MTM→LTM,
provenance-tagged with a reversible `promotion_id`. **Trust tier is preserved across promotion** — a
promoted generation item stays quarantined and can never reach proof. **Cross-project LTM and
self-evolution candidate-generation are out of scope (or-lrr).**

## 7. Embedder interface + vector retrieval

```go
type Embedder interface {
    Embed(ctx context.Context, texts []string) ([][]float32, error)
    Dim() int
    ID() string // model identity, stored alongside each vector
}
```

- **Impls:** `LocalEmbedder` (small ONNX/GGUF, e.g. bge-small / all-MiniLM — default, offline, no key)
  and `GeminiEmbedder` (cloud, reusing the Polaris/pipeline prior art). Selected by config
  `memory.embedder = local|gemini`, `memory.embedding_model = <name>`.
- **Storage:** `sqlite-vec` virtual table sized to the active embedder's `Dim()`; each vector stores its
  embedder `ID()`.
- **Retrieval:** **hybrid fusion** — semantic (vec) + keyword + heat + pin priority, weighted (tunable;
  PRINCE-style default ≈ 0.7 semantic / 0.3 keyword). The existing keyword path is never lost.
- **Model/dim change** = config flip + a **`reindex`** pass (re-embed existing items). A dim mismatch
  between stored vectors and the active embedder triggers reindex; it must **never** silently run
  wrong-dimension distance math.

## 8. AssembleForProof wiring + coordinator-memory consumer

- Wire `Engine.AssembleForProof` into the proof-context path (kills the last dead export). Proof bundle
  excludes all generation-tier memory (coded + tested); proof reads spec/`ProofObligation` **directly**
  from the Context Store (Trust invariant 7).
- The Conductor/Orion-as-coordinator recalls its own `coordinator`/`human`-provenance memory via the
  same `Assemble` path (covers "Orion-as-agent needs memory too"); generation items stay quarantined for
  it as well. This is provenance + recall-wiring on existing paths (folded into slice 1).

## 9. Trust-domain invariants (consolidated, must hold on every new path)

1. generation-tier items are quarantined in **retrieval, promotion, and vec results** alike.
2. proof bundle excludes generation memory; proof reads spec directly (never via the bundle).
3. coordinator output is control-domain context only — never a proof input, never a shippable artifact.
4. embedder-produced vectors are infrastructure, not a trust input to proof.
5. the coordinator key is never reachable from inside the sandbox (`safeenv`).

## 10. Testing (each slice ships its proof; mutation-scored)

E2E recall (write→recall→quarantine); heat decay + frequency bump; summarize-no-loss-on-crash (2PC);
promotion preserves trust tier; vec semantic recall beats keyword on a paraphrase; embedder swap +
reindex; security item never summarized away; `TestGenerationLTMNeverReachesProofPrompt` extended to
promotion + vec paths; coordinator key absent from sandbox env.

## 11. Build strategy

**Tracer-bullet vertical, then independent enhancement slices.** Make the thinnest write→recall→quarantine
path real first (slice 1), then layer heat / summarize / promotion / vec as independently provable
slices. Each slice is wired the moment it lands (wireup-gate: per-unit done-when alone previously shipped
orphan packages — gate on system wireup).

## 12. Decomposition (8 slices)

| # | Slice | Trust-sensitive | Depends |
|---|---|---|---|
| 0 | **Coordinator-inference boundary**: reformulate PRD invariant + ADR + cost-gov note + `safeenv` key handling for the coordinator provider | yes | — |
| 1 | **Spine**: write-on-Accept + ratified-decision writes + coordinator provenance + wire `AssembleForProof` + `EvictToCapacity` (fixed caps, keyword retrieval) — *satisfies or-3ba.6 DONE-WHEN; kills deadcode* | yes | — |
| 2 | **Heat model**: `visit_count`, recency/frequency, retrieve-bump, lazy decay | — | 1 |
| 3 | **Summarize-then-evict (2PC)** + security-preserving retention + optional coordinator-LLM summarizer (config) | yes | 1 (LLM part: 0) |
| 4 | **Failure-analysis writes** on Reject/Inconclusive (proof facts vs generation narrative split) | yes | 1 |
| 5 | **MTM→LTM promotion** (within-project, reversible, trust-preserving) | yes | 2 |
| 6 | **Embedder interface** + LocalEmbedder + GeminiEmbedder + config + `reindex` | — | 0 |
| 7 | **Vec retrieval** (sqlite-vec table + hybrid fusion), replace keyword default | — | 1, 6 |

Slices 1 and 6 can proceed in parallel (6 depends only on slice 0). or-3ba.6 is **absorbed** into slice 1.

## 13. Out of scope (deferred)

- **Cross-project LTM promotion + self-evolution candidate generation** → or-lrr (V2.3).
- **A7 background-review writer** (or-ykz.8) and **A4 forkable sessions** (or-ykz.5) — *consumers* of
  this substrate; stay under or-ykz, dep-linked to slice 1.
- **LLM-based drift re-anchor** beyond the existing cheap-first path — not required here.
