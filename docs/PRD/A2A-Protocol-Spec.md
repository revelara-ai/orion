# PRD: Orion A2A Protocol Specification

## Problem Statement
The current era of AI-assisted engineering is trapped in a "Single-Agent Bottleneck." Individual agents suffer from cognitive overload, a lack of verifiable autonomy, and fragmented skill silos. There is no standardized, machine-readable way for a "Senior" Orchestrator to delegate tasks to "Specialist" micro-agents and, crucially, to verify that the work performed matches the original intent without relying on the agent's subjective text output.

## Solution
The **Orion A2A Protocol** provides a structured, type-safe, and verifiable communication layer. It transforms agent interactions from "unstructured chat" into "structured contract exchanges." By enforcing a rigid payload structure containing `Intent`, `Payload`, and a `Verification Contract`, the protocol enables the Orchestrator to perform **Truth Alignment**—detecting "Liar" patterns where an agent claims success while the underlying evidence (exit codes, diffs) indicates failure.

## User Stories

1.  **As an Orchestrator (Senior Agent)**, I want to send a structured JSON payload to a Specialist, so that I can precisely communicate the task, constraints, and required proofs without ambiguity.
2.  **As a Micro-Agent (Specialist)**, I want to receive a standardized `Intent` object, so that I can execute domain-specific tasks (e.g., debugging, deploying) using a consistent interface regardless of my underlying language (Python, Rust, etc.).
3.  **As an Auditor (Orchestrator/Human)**, I want to inspect the `Verification Contract` of a completed task, so that I can mathematically verify that the `exit_code` and `artifacts` match the `assertion_status`.
4.  **As a Developer**, I want to build a new specialized agent (e.g., a "Security Scanner") that can immediately integrate into the Orion swarm by simply implementing the A2A payload schema.

## Data Flow Traces

**Trace: The Lifecycle of an Agentic Task (Intent to Verification)**

1.  **Origin:** The Orchestrator's Reasoning Engine identifies a task (e.g., "Fix a bug in `auth.go`").
2.  **Production:** The Orchestrable generates an `A2A Intent` object containing the `action: IMPLEMENT`, `target: "/path/to/auth.go"`, and `verification_required: true`.
3.  **Entry:** The payload is serialized to JSON and dispatched via the A2A routing layer (`A2A Protocol`) to a targeted Micro-Agent (e.g., `agent-debugger-01`).
4.  **Execution:** The Micro-Agent parses the `instructions`, executes the required shell commands or code changes, and collects `stdout`, `stderr`, and `diff_unified`.
5.  **Evidence Production:** The Micro-Agent constructs the `Response Envelope` containing the `assertion__status: "passed"` and the `Verification Contract` (Tier 1 Empirical evidence).
6.  **Return:** The JSON payload is transmitted back to the Orchestrator.
7.  **Verification:** The Orchestrator's **Truth Alignment Engine** intercepts the payload, parses the `exit_code`, and compares it against the `assertion_status`.
8.  **Outcome:** If `exit_code == 0` and `assertion_status == "passed"`, the task is marked **GREEN** and the change is promoted.

## Implementation Decisions

*   **Module: A2A-Parser (Rust):** A high-performance, type-safe parser implemented in Rust using `serde-json`. This module is responsible for the initial "Syntactic Check" of incoming payloads.
*   **Module: Truth-Alignment-Engine (Rust):** The core logic of the Orchestrator that implements the **Discrepancy Decision Matrix** (detecting "Liar" and "Silent Failure" patterns).
*   **Module: Verification-Registry (Schema):** A central registry of valid `Verification Contract` schemas (JSON Schema Draft 2020-12) that defines what constitutes valid "Evidence."
*   **Interface: A2A-Payload-Schema (JSON):** A strictly defined JSON Schema that governs the structure of the `Header`, `Intent`, `Payload`, `Response Envelope`, and `Verification Contract`.
*   **Module: Agent-Mock-Library (Python):** A utility library for developers to quickly spin up "dummy" agents that can participate in the swarm for testing purposes.

### Feature Flag Wiring: **Protocol Enforcement**
*   **Flag definition:** `a2a_strict_mode` defined in the Orchestrator configuration.
*   **Flag check:** Evaluated by the `Truth-Alignment-Engine` during the `Response Envelope` processing.
*   **Behavioral change:** When `ON`, any discrepancy between `assertion_status` and `exit_code` triggers an immediate **CRITICAL REJECT** and triggers the `Lookout` agent for investigation. When `OFF`, the system logs a warning but allows the task to proceed (for legacy/experimental agent support).

## Testing Decisions

*   **Unit Testing:** Every component of the `A2A-Parser` will be tested with malformed JSON and invalid Enum values.
*   **Integration Testing:** The `Truth-Alignment-Engine` will be tested against a suite of "Malicious Payloads" (e.g., payloads where `assertion_status` is `passed` but `exit_code` is `1`).
*   **Module Testing:** The `Verification-Registry` will be tested to ensure it correctly validates Tier 1 and Tier 2 artifacts.

### Integration Acceptance Test

**Test: The "Liar" Detection Test**
1.  **Setup:** Initialize the Orchestrator with `a2a_strict_mode: true`.
2.  **Action:** Dispatch a task to a Mock Agent with `verification_required: true`.
3.  **Payload Manipulation:** Force the Mock Agent to return a payload where `assertion_status: "passed"` but `outcome.exit_code: 1`.
4.  **Verify (Orchestrator):** Ensure the Orchestrator's `Truth-Alignment-Engine` identifies the discrepancy.
5.  **Verify (System State):** Ensure the task is marked as **REJECTED** in the `beads` (bd) tracker and an alert is logged.
6.  **Verify (Alerting):** Ensure the `Lookout` agent is triggered to investigate the mismatch.

## Out of Scope
*   The actual implementation of the Micro-Agent's domain-specific logic (e.g., how the `Debugger` actually fixes code).
*   The physical transport layer (we assume the protocol runs over standard JSON-over-HTTP/stdio/etc.).
*   Long-term persistence of payloads (we focus on the real-time interaction).

## Further Notes
This PRD is subject to change as the `A2A-Protocol-Spec.md` evolves alongside the structural design of the Orchestrator.
