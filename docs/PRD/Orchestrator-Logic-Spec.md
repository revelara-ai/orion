# PRD: Orion Orchestrator Logic Specification

## Problem Statement
The "Single-Agent Bottleneck" is caused by the Orchestrator's lack of structural intelligence. Current orchestrators act as simple dispatchers, blindly relaying instructions and accepting "success" messages based purely on text output. This leads to the acceptance of "Hallucinated Success" (the Liar Pattern) and "Silent Failures" (where an agent fails but doesn't report it), undermining the entire reliability of the software engineering lifecycle.

## Solution
The **Orion Orchestrator Logic** provides a deterministic "Truth Alignment" engine. It moves the Orchestrator from a passive dispatcher to an active auditor. By implementing a multi-stage verification loop (Syntactic $\to$ Semantic $\to$ Empirical), the Orchestrator ensures that every agent action is validated against its `Verification Contract` before being promoted to a "Completed" state.

## User Stories

1.  **As an Orchestrator (Senior Agent)**, I want to decompose complex human intents into a series of verifiable agent tasks, so that I can manage the lifecycle of a large-scale engineering problem.
2.  **As a Micro-Agent (Specialist)**, I want to receive a task with precise constraints (e.g., `max_execution_time`), so that I can operate within a bounded, safe environment.
3.  **As a System Administrator**, I want the Orchestrator to automatically trigger a `Lookout` agent when a "Liar" pattern is detected, so that I can investigate integrity violations without manual intervention.
4.  **As a DevOps Engineer**, I want the Orchestrator to use the `Verification Contract` to ensure that all deployment tasks are "Green" (verified) before updating the cluster state.

## Data Flow Traces

**Trace: The Truth Alignment Loop (Assertion to Commitment)**

1.  **Origin:** An `A2A Payload` (as defined in the A2A Spec) is received by the Orchestrator's `A2A-Parser`.
2.  **Syntactic Validation:** The `A2A-Parser` validates the JSON schema. If invalid, the trace terminates with a `protocol-error`.
3.  **Semantic Verification (The Truth Alignment Engine):**
    *   The engine extracts `assertion_status` and `exit_code` (for Tier 1) or `verdict` (for Tier 2).
    *   **Decision Logic:** The engine compares the agent's claim against the observable evidence using the **Discrepancy Decision Matrix**.
4.  **Empirical Probing (The Lookout Loop):** If `verification_required: true` is set, the Orchestrator (or a spawned `Lookout` agent) independently probes the `artifacts` (e.s., checking if a file exists or a port is listening).
5.  **State Transition:**
    *   If **ALL** checks pass $\to$ The task state moves to `COMPLETED` in the `beads` tracker.
    reference: `bd update <id> --status=completed`
    *   If **ANY** check fails $\to$ The task state moves to `REJECTED` or `RETRY_REQUIRED`.
6.  **Final Outcome:** The updated status is propagated back to the `Memory OS` and the user.

## Implementation Decisions

*   **Module: Truth-Alignment-Engine (Rust):** The core logic component that implements the Discremand Decision Matrix. It must be highly performant and side-effect-free.
*   **Module: Task-Decomposer (LLM-driven):** Uses the Orchestrator's reasoning engine to break high-level `Intent` into a directed acycl_graph (DAG) of `A2A Payloads`.
*   **Module: Lookout-Agent-Dispatcher (Rust/Python):** A lightweight routine that spaws transient `Lookout` containers or processes to run probes (e.g., `curl`, `ls`, `pgrep`) specifically for the Empirical Verification phase.
*   **Module: Beads-Integrator (Rust):** A bridge that translates Orion A2A task states (`passed`, `failed`, `inconclusive`) into `beads` issue statuses (`completed`, `error`, `in_progress`).
*   **Interface: Decision Matrix (Logic):** A structured lookup table (or decision tree) used by the engine to map `(Assertion, Evidence)` pairs to `(Decision, Action)`.

### Feature Flag Wiring: **Strict Mode Enforcement**
*   **Flag definition:** `orchestrator_strict_verification` defined in `config.yaml`.
*   **Flag check:** Evaluated by the `Truth-Alignment-Engine` during the `Semantic Verification` phase.
*   **Behavioral change:** When `ON`, any discrepancy (e.g., `passed` but `exit_code: 1`) triggers an immediate `REJECT` and spawans a `Lookout` agent. When `OFF`, discrepancies are logged as `WARNING` but the task is allowed to proceed.

## Testing Decisions

*   **Unit Testing:** Exhaustive testing of the `Discrepancy Decision Matrix` using a large set of permutation tests (all combinations of `assertion_status` and `exit_code`).
*   **Mutation Testing:** Injecting "Liar" payloads into the parser to ensure the `Truth-Alignment-Agent` catches them 100% of the time.
*   **Integration Testing:** End-to-end testing of the `Lookout` agent's ability to independently verify a `diff_unified` artifact.

### Integration Acceptance Test

**Test: The "Liar" Pattern Detection Test**
1.  **Setup:** Orchestrator is running with `orchestrator_strict_verification: true`.
2.  **Action:** An A2A payload is injected into the Orchestrator containing `assertion_status: "passed"` but `outcome.exit_code: 1`.
3.  **Verify (Internal Logic):** The `Truth-Alignment-Engine` must identify the mismatch and set the internal task state to `REJECTED`.
4.  **Verify (External Side-Effect):** The `beads` issue status for the corresponding task ID must be updated to `error`.
5.  **Verify (Alerting):** An alert must be emitted to the `Memory OS` indicating a "High-Severity Integrity Violation."

## Out of Scope
* The physical transport mechanism of the A2A protocol (e.g., the underlying network/stdio layer).
* The transport mechanism of the A2A protocol (see `A2A-Protocol-Spec.md`).
* The management of the Micro-Agents' local environments.

## Further Notes
This PRD is intrinsically linked to the `A2A-Protocol-Spec.md`. Any change to the payload structure in the Protocol spec must be reflected in the `Truth-Alignment-Engine` implementation decisions here.
