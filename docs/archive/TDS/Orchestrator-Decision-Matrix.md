# TDS: Orchestrator Decision Matrix & Beads Integration

**Name:** Orchestrator Decision Matrix & Beads Integration  
**Type:** Technical Design Spec (TDS)  
**Origin:** Refinement of `Orchestrator-Logic-Spec.md`  
**Created:** 2026-06-05  
**Last Updated:** 2026-06-05

## 1. Problem Statement

The Orchestrator must solve two fundamental problems:
1.  **Determinism in Uncertainty:** How to convert subjective agent claims (e.g., "Task Passed") and raw empirical evidence (e.s., "Exit Code 1" or "File not found") into a binary, actionable decision (`Accept` or `Reject`) using a repeatable logic structure.
2.  **Integrity-Driven Task Lifecycle:** How to interface with the `beads` (task tracker) such that an agent's failure or an inconclusive verification *physically prevents* the closing of a work item, preventing the "Hallucinated Success" pattern in the project's backlog.

## 2. Solution: The Discrepancy Decision Matrix (DDM)

The **DDM** is a lookup-based engine that consumes a tuple of `(Assertion, Evidence)` and produces a `ResultingState`.

### 2.1 Decision Matrix Logic

| Input: Assertion Status | Input: Observed Evidence | Decision | Next Action | Beads Integration |
| :--- | :--- | :--- | :--- | :--- |
| `Passed` | `Exit Code: 0` | **ACCEPT** | Move to `COMPLETED` | `beads: close (success)` |
| `Passed` | `Exit_code: != 0` | **REJECT** | Move to `RETRY_REQUIRED` | `beads: error` (Blocked) |
| `Passed` | `Artifact Missing` | **REJECT** | Spawn `Lookout` Agent | `beads: error` (Blocked) |
| `Failed` | `Exit Code: != 0` | **REJECT** | Move to `RETRY_REQUIRED` | `beads: error` (Blocked) |
| `Failed` | `Exit Code: 0` | **REJECT (Liar Pattern)** | Spawn `Lookout` Agent | `beads: error` (Blocked) |
| `Inconclusive` | `Any` | **AUDIT** | Move to `RETRY_REQUIRED` | `beads: in_progress` (Blocked) |
| `Error` | `Any` | **REJECT** | Move to `ERROR` | `beads: error` (Blocked) |

### 2.2 Implementation: The Truth-Alignment-Engine (Rust)

The engine will be implemented as a pure, stateless function:
`fn evaluate_discrepancy(assertion: AssertionStatus, evidence: Evidence) -> Decision`

*   **Input Structs:** `AssertionStatus` (Enum), `Evidence` (Struct containing `exit_code: i32`, `artifact_present: bool`, `log_snippet: String`).
*   **Output Structs:** `Decision` (Enum: `Accept`, `Reject`, `Audit`) and `Action` (Enum: `Complete`, `Retry`, `Investigate`).

## 3. The Beads Integration Guard (The Closing Gate)

The `beads-integrator` acts as a **Gatekeeper**. It is responsible for synchronizing the Orion internal state with the `beads` task tracker via the `bd` CLI.

### 3.1 The Integrity Constraint

The integration must enforce the following invariant:
**A `beads` issue ID can only be transitioned to `completed` if and only if the associated Orion `A2A-Payload` has reached the `Decision: Accept` state.**

### 3.2 State Transition Mapping

| Orion Internal State | Action via `bd` | Resulting `beads` Status |
| :--- | :--- | :--- |
| `Decision: Accept` | `bd update <id> --status=completed` | `Completed` |
| `Decision: Reject` | `bd update <int> --status=error` | `Error` (Blocked) |
| `Decision: Audit` | `bd update <id> --status=in_progress` | `In_Progress` (Blocked/Review Required) |

### 3.3 Error Handling

If the `beads-integrator` attempts to close an issue that is in an `Error` or `Inconclusive` state, it must:
1.  Fail the execution.
2.  Emit a high-priority alert to the `Memory OS`.
3.  Log a "Protocol Violation" event in the `Audit Log`.

## 4. Data Flow

1.  **Ingestion:** `A2A-Parser` parses JSON $\to$ `A2A-Payload`.
2.  **Analysis:** `Truth-Alignment-Engine` performs `evaluate_discrepancy(payload.assertion, observed_evidence)`.
3.  **Decision:** Engine produces `Decision: Accept` or `Decision: Reject`.
4.  **Sync:** `beads-integrator` receives `Decision`.
    *   If `Accept`: Executes `bd update <id> --status=completed`.
    *   If `Reject/Audit`: Executes `bd update <id> --status=error` or `in_progress`.

## 5. Testing Strategy

*   **Unit Tests:** Table-driven tests in Rust for `evaluate_discrepancy` covering 100% of the DDM permutation matrix.
*   **Integration Tests:** A mock `beads` environment where the `beads-integrator` is tested against simulated `Accept` and `Reject` decisions to ensure the `bd` command execution is correct and the "Closing Gate" cannot be bypassed.
