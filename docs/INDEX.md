# 🌌 Orion Project: Master Index

This document serves as the central directory for the Orion project documentation, providing a high-level overview of the architecture, the engineering primitives, and the technical specifications for the orchestration of reliable, multi-agent software engineering.

---

## 📜 Start Here

- **[The Orion Manifesto](./MANIFESTO.md)** — the vision and beliefs. The source of truth everything else inherits from.
- **[Orion V2 PRD](./PRD/orion-v2.md)** — the current product requirements: a local-first, TUI-driven agentic harness whose completion criterion is independent, multi-modal *proof* of correctness. Supersedes the V1 lineage.
- **[Orion Triad Reconciliation](./SPEC/Orion-Triad-Reconciliation.md)** — bridges the V1 Triad component specs (Rust/HTTP/beads/2-tier) to V2 (Go/local/Context-Store/3-mode). First design task; component specs are written from this + the V2 modules.
- **[Orion ↔ Polaris API Contract](./SPEC/Orion-Polaris-API-Contract.md)** — audit of the Polaris OpenAPI spec mapping each `polaris-connector` capability to real endpoints (incl. the STPA `control-structure`/`ucas`/`loss-scenarios` family the hazard mode consumes).
- **[Archive](./archive/README.md)** — the V1 "reliability-debt remediation" PRD/SPEC, kept as historical artifacts (superseded by the manifesto + V2 PRD).

## 🏗️ System Architecture

Orion V2's architecture is defined by the **[V2 PRD](./PRD/orion-v2.md)**: the **module list** (17 modules — Conductor, decomposer, context-store, context-engine, memory, a2a, agent-runtime, sandbox, proof, dependency-provenance, reliability-tier, delivery, integration, polaris-connector, reliability-scan, cmd/orion), the **Opinionated Reliability Loop** (the canonical execution map, Phases A–G), and the **Composition Model** (Primitives / Skills / Workflows).

The original **"Orion Triad"** component specs (A2A Protocol, Orchestrator Logic, Lookout Agent, Verification Engine, Task Decomposer, Decision Matrix) were written for a different architecture (Rust / HTTP microservices / beads-as-truth / 2-tier verification). Their concepts are absorbed into the V2 modules; the specs themselves are now **[archived](./archive/)**, bridged by the **[Triad Reconciliation](./SPEC/Orion-Triad-Reconciliation.md)**.

## 🚀 Roadmap

V2 is phased **V2.0 → V2.3** (tracer bullet → brownfield + tracker → polyglot → earned autonomy + learning write-back); see the V2 PRD "Phasing" table. Two blocking first design tasks precede implementation, both now complete: the [Triad Reconciliation](./SPEC/Orion-Triad-Reconciliation.md) and the [Polaris API Contract](./SPEC/Orion-Polaris-API-Contract.md). Next: `/prd-to-issues` to slice V2.0.

---
*Note: This document is a living index. As new modules and specifications are added, they must be registered here.*
