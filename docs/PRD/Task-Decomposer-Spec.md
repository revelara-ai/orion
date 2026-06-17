# PRD: Task Decomposition Engine Specification

## Problem Statement
The transition from high-level human intent (e.g., "Fix the auth bug") to a set of executable, machine-verifiable instructions is the most failure-prone phase in the Orion lifecycle. If the decomposition is too shallow, agents lack context (leading to much-discussed "hallucinated success"); if it is too deep, the system suffers from excessive latency and overhead. We need a deterministic, verifiable "Reasoning-to-Task" pipeline that transforms unstructured intent into a structured Directed Acyclic Graph (DAG) of A2A payloads.

## Solution
The **Task Decomposition Engine** acts as the "Strategic Planner" within the Orchestrator. It uses a multi-stage reasoning process (Context $\to$ Plan $\to$ Payload) to ensure that every node in the task graph is actionable, constrained, and—crucially—contains its own verification requirement.

## User Stories

1.  **As an Orchestrator**, I want to ingest a single, ambiguous human instruction and expand it into a sequence of unambiguous, verifiable tasks, so that the Micro-Agent Swarm can execute them without human intervention.
2.  **As a Developer**, I want to see the "Plan" (the DAG) before execution begins, so that I can review the proposed decomposition for logical errors or unsafe actions.
3.  **As a System Administrator**, I want the decomposition process to include "Context Retrieval" from the Memory OS, so that the resulting tasks are informed by historical precedents and known system constraints.

## Data Flow Traces

**Trace: The Decomposition Pipeline (Intent $\to$ A2A DAG)**

1.  **Ingestion (The Trigger):** An unstructured input (e.g., a Discord message, a GitHub issue, or a manual command) is received by the Orchestrator.
2.  **Contextualization (The Enrichment):** 
    *   The Engine identifies key entities (e.s., file paths, service names) in the input.
    *   The Engine queries the **Memory OS** (via `gbrain-explorer` patterns) to retrieve relevant historical context, recent incidents, or existing policies related to these entities.
3.  **Reasoning (The Decomposition):**
    *   The Engine passes the (Input + Context) to a high-reasoning LLM (e.g., Gemini Flash/Pro).
    *   Using a **Chain-of-Thought** approach, the LLM breaks the problem into a hierarchy of sub-tasks.
    *   Each sub-task is mapped to an `A2A Action Type` (`ANALYZE`, `IMPLEMENT`, `VERIFY`, etc.).
4.  **Payload Generation (The Materialization):**
    *   For every node in the graph, the Engine generates a full **A2A Payload Structure** as defined in the `A2A-Protocol-Spec`.
    *   This includes the `intent.target_resource`, `constraints` (e.s., `verification_required: true`), and the `payload.instructions`.
5.  **Graph Construction (The DAG):**
    *   The Engine establishes dependencies between tasks (e.s., Task B depends on the `output` of Task A).
    *   The resulting structure is serialized into a **Task Graph** stored within the `beads` (bd) tracker.
6.  **Commitment:** The Plan is presented to the Orchestrator (or Human) for approval, transitioning the state from `PLANNING` to `DISPATCHING`.

## Implementation Decisions

*   **Module: Reasoning-Kernel (LLM-driven):** Use a high-context-window model (e.able Gemini) to process the heavy lifting of the "Strategy Phase."
*   **Module: Context-Retriever (Memory Integration):** A specialized module that implements semantic search against the `Memory OS` to augment the initial prompt.
*   **Module: DAG-Serializer (beads-integration):** A module that translates the LLM's flat list of tasks into a dependency-aware graph structure within the `beads` (bd) database.
*   **Interface: The "Plan" Document:** A human-readable Markdown preview of the proposed task graph, allowing for manual review/override before execution.
*   **Decision: Iterative Refinement:** The Engine should support a "Review-and-Correct" loop, where the Orchestrator can reject a plan and provide feedback to trigger a re-decomposition.

## Testing Decisions

*   **Complexity Testing:** Testing the Engine's ability to decompose a "Large Problem" (e.g., "Upgrade the entire auth service") into a manageable DAG without losing depth.
*   **Integrity Testing:** Injecting "Garbage" context into the retrieval phase to ensure the Engine's reasoning remains robust against noisy or conflicting data.
*   **Conformance Testing:** Ensuring every node in the generated DAG strictly adheres to the `A2A-Protocol-Spec` JSON schema.

### Integration Acceptance Test

**Test: The "End-to-End Decomposition" Test**
1.  **Setup:** Prepare a `beads` issue describing a simple bug (e.s., "The login endpoint returns a 500 error").
2.  **Action:** Trigger the `Task-Decomposer` on that issue.
3.  **Verify (Graph Structure):** Inspect the generated DAG to ensure it contains at least three distinct nodes: `ANALYZE` (logs), `IMPLEMENT` (fix code), and `VERIFY` (test endpoint).
4.  **Verify (Payload Integrity):** Confirm that the `VERIFY` node contains `verification_required: true` and a valid `target_resource`.
5.  **Verify (System State):** Ensure all nodes are successfully registered as `pending` tasks in the `beads` tracker.

## Out of Scope
*   The actual execution of the shell commands (handled by the Micro-Agent Swarm).
*   The infrastructure for the LLM inference (handled by the Orchestrator's provider configuration).
*   Real-time monitoring of the execution (handled by the `Lookout` agent).

## Further Notes
This PRD is the technical sibling to the `Orchestrator-Logic-Spec.md`. While the Orchestrator manages the *lifecycle*, this specification describes the *intelligence* that creates the work itself.
