# 🧬 Orion A2A Protocol Specification (Draft v0.1)

## 1. Context & Purpose
The **Agent-to-Agent (A2A) Protocol** is the standardized, machine-readable communication layer for the Orion ecosystem. It defines how the **Orchestrator** (the "Senior Engineer") communicates intent, constraints, and requirements to the **Micro-Agent Swarm** (the "Specialists"), and how those specialists return **Verifiable Proofs** of task completion.

The goal is to move away from ambiguous, unstructured chat-based communication toward a structured,-type-safe, and **verifiable** messaging system that enables "Truth Alignment."

## 2. High-Level Goals
* **Interoperability:** Any agent (Python, Rust, Go, etc.) can join the swarm if it implements the payload schema.
* **Verifiability:** Every response must contain the evidence required by the `Verification Contract`.
* **Observability:** The protocol structure allows the Orchestrator to audit the entire lifecycle of a task without interpreting unstructured text.
* **Resilience:** The protocol must handle "Uncertain" and "Failed" states explicitly to trigger the **STAMP** remediation loops.

## _3. The A2A Payload Architecture (The "Nervous System")_

Every interaction is encapsulated in a single, immutable JSON object. The payload is divided into four functional zones:

### A. The Header (Metadata & Routing)
Defines the "who, when, and where" of the message.
* `protocol_version`: (string) e.g., `"1.0"`.
* `correlation_id`: (UUID) A unique trace ID that persists across the entire task lifecycle.
* `timestamp`: (ISO-8601) When the message was dispatched.
* `sender_id`: (string) The unique identifier of the initiating agent.
* `receiver_id`: (string) The target specialist agent or `"ORCHESTRATOR"` for broadcast.

### B. The Intent (The "Why")
Defines the high-level goal and the parameters of the work.
* `action`: (Enum) `ANALYZE`, `IMPLEMENT`, `VERIFY`, `DEPLOY`, `PROBE`.
* `target_resource`: (string) The URI or file path the task centers on.
_**Example:** `"~/project/src/main.go"`_
* `context_ref`: (string) A link to the **Memory OS** (e.g., `memory://task-id-123`) providing necessary background information.
* `constraints`: (Object)
    * `max_execution_time`: (duration) e.g., `"60s"`.
    * `budget_limit`: (string) Token or compute cost cap.
    * `verification_required`: (boolean) If `true`, an explicit `VerificationContract` must be returned.

### C. The Payload (The "What")
The actual instructions or data to be processed.
* `instructions`: (string) The actionable, text-based directive.
* `input_data`: (Object/JSON) Any structured data required to perform the action (e.g., a JSON snippet of a log file or a configuration object).

### D. The Response Envelope (The "Result")
The agent's subjective claim about the task outcome.
* `assertion_status`: (Enum) `passed` | `failed` | `inconclusive`.
* `task_state`: (Enum) `completed` | `error` | `interrupted` | `retry_required`.
* `error_message`: (string, optional) Human-readable description of failures.

### E. The Verification Contract (The "Proof")
**Mandatory if `intent.constraints.verification_required == true`.**
This is the "Evidence" required to achieve **Truth Alignment**.

* **Tier 1: Empirical Evidence (1st-Order)**
    * `exit_code`: (integer) The shell exit status of the performed action.
    * `stdout` / `stderr`: (string) The literal output from the execution.
    * `artifacts`:
        * `diff_unified`: (string) A unified diff of any filesystem changes.
        * `file_hashes`: (Map) `path -> sha256`.
        * `structured_data`: (Object) Parsed data from tools like `jq` or `pytest`.

* **Tier 2: Structural Evidence (2nd-Order)**
    * `verdict`: (Enum) `compliant` | `non_compliant` | `error`.
    * `policy_id`: (string) The identifier of the policy being validated.
    * `validation_trace`: (Array of Objects) A list of rules checked, their results, and the evidence for each.

## 4. Error Handling & The "Liar" Pattern
The primary security concern in the A2A protocol is the **"Liar Pattern"**: where `assertion_status == "passed"` but the `exit_code != 0`.

**The Orchestrator must enforce the following invariants:**
1. **Invariant 1 (Integrity):** If `assertion_status == "passed"`, then `exit_code` (for Tier 1) **must** be `0`.
2. **Invariant 2 (Completeness):** If `verification_required` was requested, the response **must** contain the `verification_contract` object.
3. **Invariant 3 (Traceability):** The `correlation_id` from the original `Intent` must be present in the `Response Envelope`.

## 5. Next Steps for Implementation
* [ ] Define the formal **JSON Schema** (JSON Schema Draft 2020-12) for validation.
* [ ] Implement the **Rust-based parser** for the Orchestrator.
* [ ] Create a **Python Mock Agent** to test the protocol's compliance.
