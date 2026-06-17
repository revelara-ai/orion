# 🌌 Orion Project: Master Index

This document serves as the central directory for the Orion project documentation, providing a high-level overview of the architecture, the engineering primitives, and the technical specifications for the orchestration of reliable, multi-agent software engineering.

---

## 📜 Start Here

- **[The Orion Manifesto](./MANIFESTO.md)** — the vision and beliefs. The source of truth everything else inherits from.
- **[Orion V2 PRD](./PRD/orion-v2.md)** — the current product requirements: a local-first, TUI-driven agentic harness whose completion criterion is independent, multi-modal *proof* of correctness. Supersedes the V1 lineage.
- **[Archive](./archive/README.md)** — the V1 "reliability-debt remediation" PRD/SPEC, kept as historical artifacts (superseded by the manifesto + V2 PRD).

> The "Orion Triad" component specs below are conceptually aligned with V2 and reused as component-level design, but need a vocabulary/scope refresh pass to match the V2 PRD (local-first, Context Store, multi-modal proof, reliability tiers). See the V2 PRD's "Triad refresh" note.

---

## 🏗️ System Architecture: The Orion Triad

The core of Orion is built upon three interlocking architectural pillars. Each pillar handles a distinct stage of the autonomous software engineering lifecycle.

### 1. [The Interface: A2A Protocol](./PRD/A2A-Protocol-Spec.md)
**Role:** The "Nervous System"
**Responsibility:** Defines the standardized, machine-readable communication layer for Agent-to-Agent (A2A) interaction. It ensures that all messages—from intent to verifiable proof—are structured, type-safe, and immutable.
*   **Key Components:** Header, Intent, Payload, Response Envelope, and the Verification Contract.

### 2. [The Brain: Orchestrator Logic](./PRD/Orchestrator-Logic-Spec.md)
**Role:** The "Senior Engineer"
**Responsibility:** The higher-order reasoning engine that manages the lifecycle of complex engineering tasks. It decomposes human intent into actionable tasks and performs the ultimate "Truth Alignment" between agent claims and reality.
*   **Key Components:** Task Decomposition, Truth Alignment Engine (Decision Matrix), and the STAMP-driven lifecycle management.

### 3. [The Eyes: Lookout Agent](./PRD/Lookout-Agent-Spec.md)
**Role:** The "Trusted Observer"
**Responsibility:** A transient, high-trust entity designed to perform independent, empirical probes. It validates the physical evidence (e.s., filesystem changes, network ports) to confirm or reject the work performed by the Micro-Agent Swarm.
*   **Key Components:** Sandbox-based execution, Empirical Probing (Path, Port, Hash), and Evidence Reporting.

---

## 🛠️ Engineering Primitives

### [The Verification Engine](./PRD/Verification-Engine-Spec.md)
The "Truth Authority" that maintains the registry of all valid verification schemas (Tier 1 and Tier 2), ensuring that every artifact presented to the Orchestrator is structurally sound and policy-compliant.

### [The Task Decomposer](./PRD/Task-Decomposer-Spec.md)
The reasoning pipeline that transforms unstructured human requirements into a directed acyclic graph (DAG) of actionable, verifiable A2A payloads.

---

## 🚀 Project Roadmap & Progress

- [x] **Phase 1: Foundations** (Protocol, Orchestrator, and Lookout Specifications)
- [ ] **Phase 2: Implementation** (Rust/Python implementation of the A2A Parser and Truth-Alignment Engine)
- [ ] **Phase 0: Implementation** (Verification Registry and Schema definition)

---
*Note: This document is a living index. As new modules and specifications are added, they must be registered here.*
