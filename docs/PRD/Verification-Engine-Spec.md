# PRD: Orion Verification Engine Specification

## Problem Statement
In an autonomous agentic swarm, "trust" is a liability. An agent may claim a task is complete (the "Liar" pattern) while the underlying state remains broken. The Orchestrator cannot rely on the text-based success messages of an LLM. Without a centralized, automated way to validate that the provided "Evidence" (e.g., 1st-order diffs, exit codes, structural schemas) adheres to the required quality and integrity, the entire Orion ecosystem is vulnerable to "Hallucinated Success."

## Solution
The **Verification Engine** is the authoritative registry and validation authority for all Orion "Proof of Work." It provides a centralized factory for defining and verifying **Tier 1 (Empirical)** and **Tier 2 (Structural)** verification contracts. It ensures that every piece of evidence provided in an A2A payload is structurally sound, schema-compliant, and contains the necessary metadata to support the Orchestrator's **Truth Alignment** logic.

## User Stories

1.  **As a Micro-Agent Developer**, I want to register a new verification artifact type (e.g., a `JUnit XML` report), so that the Orchestrator can automatically validate my task results.
2.  **As the Orchestrator**, I want to query the Verification Engine to see if a provided `diff_unified` is structurally valid, so that I can reject malformed payloads before they reach the deployment phase.
3.  **As a Security Engineer**, I want to define a "Policy-as-Code" (Tier 2) rule that prevents any agent from executing tasks that touch sensitive paths (e.g., `/etc/shadow`), so that the swarm remains within a safe operating envelope.
4.  **As a DevOps Engineer**, I want the Verification Engine to automatically extract `file_hashes` from an execution artifact, so that the integrity of the deployment is cryptographically verifiable.

## Data Flow Traces

**Trace: The Verification Pipeline (Evidence $\to$ Verdict)**

1.  **Ingestion:** The Orchestrator receives an `A2A Payload` containing a `verification_contract` block.
2.  **Registry Lookup:** The Engine identifies the `tier` (Tier 1 or Tier 2) and retrieves the corresponding JSON Schema/Policy template from its internal registry.
3.  **Syntactic Validation:** The Engine performs a structural check on the `evidence` object (e.g., checking if the `exit_code` field is present and is an integer).
4.  **Semantic/Integrity Validation:**
    *   **For Tier 1:** The Engine inspects the `artifacts` (e.g., checking if the `diff_unified` is a valid unified diff format).
    *   **For Tier 2:** The Engine executes a policy check (e.g., running a `regex` or `opa` check against the `validation_trace`).
5.  **Verdict Generation:** The Engine produces a `Verdict` object: `compliant` | `non_compliant` | `error`.
6.  **Feedback Loop:** The `verdict` is returned to the `Truth-Alignment-Engine`, which then decides whether to `COMMIT` or `REJECT` the task.

## Implementation Decisions

*   **Module: Verification-Registry (The Library):** A central repository of JSON Schemas (JSON Schema Draft 2020-12) and Policy Templates (Rego/OPA) stored within the `verification-engine` module.
*   **Module: Artifact-Parser (The Extractor):** A high-performance parsing engine (Rust-based) capable of extracting structured data from raw streams (e.g., parsing `stdout` for JSON, or extracting `diffs` from text).
*   **Module: Policy-Enforcer (The Auditor):** An integration with an OPA (Open Policy Agent) or similar engine to handle complex, logic-based Tier 2 structural checks.
*   **Interface: Verification-API (The Gateway):** A simple, high-throughput API that allows the Orchestrator to submit payloads for validation: `POST /verify {payload}` $\to$ `200 OK {verdict, trace}`.
*   **Integration: Truth-Alignment-Engine Connection:** The engine must be callable as a sub-routine of the Orchestrator, ensuring that "Truth Alignment" is an atomic operation.

### Feature Flag Wiring: **Strict Schema Enforcement**
*   **Flag definition:** `verification_strict_mode` defined in the Orchestrator configuration.
*   **Flag check:** Evaluable by the `Artifact-Parser` during the verification phase.
*   **Behavioral change:** When `ON`, any payload that fails a schema check is treated as a `CRITICAL REJECT` (Integrity Violation). When `OFF`, the engine logs a `WARNING` but allows the payload to pass (for backward compatibility with legacy agents).

## Testing Decisions

*   **Fuzz Testing:** Using randomized, malformed JSON payloads to ensure the `Artifact-api` does not crash or allow injection attacks.
*   **Policy Injection Testing:** Testing the `Policy-Enforcer` with conflicting rules to ensure the "most restrictive" rule always wins.
*   **Compatibility Testing:** Verifying that the engine can correctly process both high-volume Tier 1 (low complexity) and high-complexity Tier 2 (structural) workloads.

### Integration Acceptance Test

**Test: The "Malformed Artifact" Detection Test**
1.  **Setup:** Initialize the Verification Engine with a standard `Tier 1` schema for `exit_code`.
2.  **Action:** Inject an A2A payload where the `exit_code` is provided as a string (`"zero"`) instead of an integer (`0`).
3.  **Verify (Engine):** The engine must identify the type mismatch and return a `verdict: error`.
4.  **Verify (Orchestrator):** The Orchestrator must intercept this error and transition the task status to `REJECTED`.
5.  **Verify (Alerting):** An alert must be sent to the `Memory OS` indicating a `Verification Failure`.

## Out of Scope
*   The physical execution of the `diff` or `curl` commands (this is the responsibility of the `Lookout` agent or the specialist).
*   The long-term storage of all historical verification artifacts (we only care about the "current" verification for the task at hand).
*   The UI for managing the registry (this is a headless, machine-to-machine service).

## Further Notes
This PRD is intrinsically linked to both the `A2A-Protocol-Spec.md` (which defines the input) and the `Orchestrator-Logic-Spec.md` (which consumes the output).
