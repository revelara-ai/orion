---
title: Orion Triad Reconciliation (V1 component specs → V2)
status: draft
authors: Joseph Bironas
created: 2026-06-17
reconciles:  # now archived (this table is the bridge)
  - docs/archive/PRD/A2A-Protocol-Spec.md
  - docs/archive/PRD/Lookout-Agent-Spec.md
  - docs/archive/PRD/Orchestrator-Logic-Spec.md
  - docs/archive/PRD/Task-Decomposer-Spec.md
  - docs/archive/PRD/Verification-Engine-Spec.md
  - docs/archive/TDS/Orchestrator-Decision-Matrix.md
  - docs/archive/specs/A2A-Protocol-Spec.md (draft v0.1)
into:
  - docs/PRD/orion-v2.md
---

# Orion Triad Reconciliation

> **Why this exists.** The adversarial review found that the "Orion Triad" component specs are written for a **different architecture** than the V2 PRD: Rust, HTTP/stdio microservices, beads-as-source-of-truth, 2-tier verification, OPA/Rego, external "Memory OS"/gbrain/Gemini. They are conceptually aligned ("Liar pattern" = agents gaming the verifier; Lookout = empirical proof; Truth Alignment = proof-not-assertion) but cannot be built as-is against V2 (Go, local-first in-process, Context-Store-as-truth, 3-mode proof, commodity-model). This table is the bridge: it preserves the concepts, maps each to its V2 home, and renders a per-spec verdict. **It is the first design task; component specs are written from V2 modules + this table, not from the originals.**

## 1. The cross-cutting shifts

Every Triad spec is touched by these. Apply them globally when reading the originals.

| # | Shift | From (Triad) | To (Orion V2) | Rationale |
|---|---|---|---|---|
| 1 | Language | Rust | **Go** | Orion is Go; one toolchain. |
| 2 | Topology / transport | HTTP/stdio microservices, `POST /verify` gateway | **Local-first, in-process Go modules** over an internal event channel / `a2a` bus | Local-first runtime; no network hop in the core loop. |
| 3 | Source of truth | **beads** (Decision Matrix ties task closure to beads) | **Context Store** (SQLite); tracker (beads/GitHub) is a *projection* | PRD: facts ≠ tracker; store-enforced `done`-gate. |
| 4 | Verification model | **2-tier** (Tier-1 Empirical, Tier-2 Structural/OPA) | **3-mode multi-modal proof** (behavioral + empirical + hazard), convergence | PRD core thesis; Tier-2/OPA becomes the hazard+security gates. |
| 5 | Decision logic | single-tuple **DDM** `(Assertion, Evidence) → state` | per-mode **`Converge(behavioral, empirical, hazard) → {Accept\|Reject\|Inconclusive, dissenting_modes}`** | Round-1 hardening: keep per-mode provenance, add Inconclusive. |
| 6 | Memory | external **"Memory OS" via gbrain-explorer**, Gemini Flash/Pro | Orion **`memory` module** (MemoryOS-style STM/MTM/LTM, SQLite+vec), model-agnostic | PRD Memory section; commodity-model principle. |
| 7 | Naming | `Verification Contract` (overloaded: obligation *and* evidence) | **`ProofObligation`** (harness-owned, read-only to agent) vs **`EvidenceClaim`** (untrusted self-report) | Round-1 Core Data Model Hardening. |
| 8 | Trust | implicit | explicit **generation ⊥ proof trust domains**; proof reads spec directly; verdict computed only from harness-collected evidence | Trust-Domain invariants 1–9. |

## 2. Per-spec reconciliation

| Triad spec | Concepts to KEEP | What CHANGES (apply §1) | V2 home (module/section) | Verdict |
|---|---|---|---|---|
| **A2A-Protocol-Spec** (PRD) | Structured, immutable payload (Header/Intent/Payload/contract/Response Envelope); `correlation_id` trace; language-agnostic agent interface; verifiable-by-structure | Rust→Go in-process bus (not HTTP); split `Verification Contract` → `ProofObligation` (carried, read-only) + `EvidenceClaim` (returned, untrusted); payload never carries proof inputs | `a2a` module | **Refresh** → rewrite as the `a2a` component spec |
| **Lookout-Agent-Spec** (PRD) | Transient, high-trust *observer*; empirical probing of real state (ports, files, hashes, processes, `curl`); isolation from the worker; structured evidence | Docker/Podman → **pluggable sandbox (gVisor default)**; add **per-type probe adapters** (service/CLI/library/batch); isolate from the *generator* (Trust inv. 4); probe contract = the `ResponseContract` derived from the spec | `proof/empirical` (Proof Harness) | **Refresh** → folds into the `proof` spec |
| **Orchestrator-Logic-Spec** (PRD) | Truth Alignment (active auditor, not passive dispatcher); multi-stage verification; Liar/Silent-failure defense; spawn observer on discrepancy | Syntactic→Semantic→Empirical (2-tier) → **3-mode converge**; beads→Context Store; **separate `truth-align` from dispatch** (Trust inv. 5); Conductor narrowed (not a god-object); git/merge state → `integration` | `orchestrator` (Conductor) + `truth-align` | **Refresh** (split into two component specs) |
| **Task-Decomposer-Spec** (PRD) | Context→Plan→Payload pipeline; intent→**DAG**; show plan before execution; context retrieval; per-node verification requirement | Gemini-specific → model-agnostic; gbrain/Memory-OS → Orion **`memory`**; output **Epic{Tasks, deps}** each with a `ProofObligation` + declared **file scope**; add the **coverage gate** (every spec req → ≥1 obligation) | `decomposer` | **Refresh** |
| **Verification-Engine-Spec** (PRD) | Registry of verification schemas (JSON Schema 2020-12); artifact parsing/extraction; **policy-as-code** (Tier-2/OPA); strict-mode flag; fuzz/injection testing | Rust→Go; `POST /verify` HTTP → in-process `Prove(artifact, obligation) → Verdict`; Tier-1/Tier-2 → **3-mode**; `compliant\|non_compliant\|error` → `Accept\|Reject\|Inconclusive`; Tier-2/OPA policy → **hazard + security gates**; "alert Memory OS" → `memory` + `escalations` | `proof` (+ policy in `proof/hazard`, security gates) | **Refresh** |
| **Orchestrator-Decision-Matrix** (TDS) | Deterministic decision lookup; integrity-driven closure (cannot close on hallucinated success); spawn observer on artifact-missing | single `(Assertion, Evidence)` tuple → **per-mode `Converge`** with `dissenting_modes` + `Inconclusive`; **beads closure gate → store-enforced `done` requires `proof_id` with `verdict=Accept`**; tracker is a projection | `truth-align` (the `Converge` function) | **Refresh** → becomes the `truth-align` decision spec |
| **A2A-Protocol-Spec** (`docs/specs/`, draft v0.1) | (superseded by the PRD A2A spec) | — | `a2a` | **Archive** (redundant draft) |

## 3. Disposition

- **All six Triad documents are conceptual ancestors, not buildable specs for V2.** Their *concepts* are now captured here and mapped to the V2 module list in `orion-v2.md`.
- **Done (2026-06-17):** all six Triad docs + the redundant `specs/A2A-Protocol-Spec.md` draft have been **moved to `docs/archive/`** (`archive/PRD/`, `archive/TDS/`, `archive/specs/`). This reconciliation table is the bridge; refreshed component specs are written from the V2 modules + this table.
- **Net new in V2 with no Triad ancestor** (write fresh component specs): `memory` (+ self-evolution), `context-store`, `context-engine`, `integration` (Phase E2), `reliability-tier`, `delivery`/deployment-bar, `polaris-connector` (see the Polaris API Contract), `reliability-scan`, `budget`/governance, `tui`.

## 4. Term mapping (quick reference)

| Triad term | V2 term |
|---|---|
| Verification Contract (as obligation) | `ProofObligation` |
| Verification Contract (as returned evidence) | `EvidenceClaim` |
| Tier-1 (Empirical) | `proof/empirical` (Lookout adapters) |
| Tier-2 (Structural / OPA / Rego) | `proof/hazard` + security gates |
| Discrepancy Decision Matrix (DDM) | `Converge()` in `truth-align` |
| Truth-Alignment-Engine | `truth-align` |
| Memory OS / gbrain | `memory` (STM/MTM/LTM, SQLite+vec) |
| beads (as source of truth) | Context Store (beads = tracker projection) |
| Liar pattern / Hallucinated Success | the generation⊥proof trust wall + verification-gated closure |
| `verdict: compliant\|non_compliant\|error` | `Verdict: Accept\|Reject\|Inconclusive` |
