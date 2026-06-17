# PRD: Orion Lookout Agent Specification

## Problem Statement
In an autonomous agentic swarm, the "Liar Pattern" (where an agent claims success but the code change is invalid) and "Silent Failure" (where an agent fails but reports success) are the primary threats to system reliability. While the Orchestrator can perform **Semantic Verification** (checking the reported `exit_code`), it has no way to independently verify the **Physical Reality** of the filesystem, network, or process state without an external, trusted observer. We need a specialized, transient entity to act as the "Eyes" of the Orchestrator.

## Solution
The **Lookout Agent** is a lightweight, ephemeral, and high-trust "Observer" agent. Its sole purpose is to perform **Empirical Probing** to validate the `Verification Contract` provided by a Specialist agent. It does not perform the primary task; it only validates the *side-effects* of the task. By operating in a trusted, bounded sandbox, the Lookout Agent provides the "Truth Alignment" necessary to close the loop on the STAMP lifecycle.

## User Stories

1.  **As an Orchestrator**, I want to spawn a Lookout Agent whenever a task requires `verification_required: true`, so that I can verify the physical integrity of the claimed outcome without trusting the Specialist.
2.  **As a Security Engineer**, I want the Lookout Agent to run in a restricted, immutable sandbox, so that it cannot be compromised or influenced by the malicious actions of a "L_Liar" Specialist agent.
3.  **As a Developer**, I want the Lookout Agent to provide structured, machine-readable evidence (e.s., `diff_unified`, `file_hashes`, `curl` output), so that the results can be fed directly into the `Truth-Alignment-Engine`.
4.  **As a System Administrator**, I want the Lookout Agent to be transient and ephemeral, so that it does not consume long-term resources or contribute to "Agent Bloat" in the swarm.

## Data Flow Traces

**Trace: The Lookout Probing Loop (Trigger $\to$ Probe $\to$ Evidence)**

1.  **Trigger:** The Orchestrable identifies a `Verification Contract` in an incoming A2A payload that requires `verification_required: true`.
2.  **Instantiation:** The Orchestrator spawns a transient `Lookout` process/container, injecting the `target_resource` and the `verification_contract` as the primary instructions.
3.  **Execution (The Probe):**
    *   The Lookout Agent executes a set of predefined, low-level "Probe Commands" (e.g., `ls -l`, `curl -I`, `pgrep`, `sha256sum`).
    *   It focuses exclusively on the `artifacts` defined in the contract (e.s., checking if a file exists or a port is listening).
4.  **Evidence Collection:** The Lookout Agent captures the `stdout`, `stderr`, and the exit status of these probe commands.
5.  **Packaging:** The Agent wraps these findings into a new `A2A Response Envelope` (acting as a "Sub-Agent") and returns it to the Orchestrator.
6.  **Handover:** The Orchestrator receives the Lookout's report and passes it to the `Truth-Alignment-Engine` for final judgment.

## Implementation Decisions

*   **Module: Probe-Runner (Shell/Python):** A minimalist execution engine designed to run high-speed, low-dependency shell commands within a bounded environment.
*   **Module: Sandbox-Manager (Docker/Namespace):** Integration with `cgroups` and `namespaces` (or a lightweight Docker/Podman container) to ensure the Lookout Agent is isolated from the host and the Specialist's workspace.
*   **Module: Artifact-Extractor (Regex/Parsing):** A specialized parser that extracts structured data (e.s., JSON from `stdout`, or specific patterns from logs) to transform raw text into `Tier 1` evidence.
*   **Interface: The "Lookout" Command Set:** A predefined library of "Safe Probes" (e.s., `check_path`, `check_port`, `check_hash`, `check_process`) that the Orchestrator can request.
*   **Module: Ephemeral-Lifecycle (Cleanup):** A self-destruct mechanism that ensures the Lookout process/container is terminated and its logs are archived immediately after the verification report is delivered.

### Feature Flag Wiring: **Lookout-Enforcement**
*   **Flag definition:** `lookout_enabled` defined in the Orchestrator configuration.
*   **Flag check:** Evaluable by the `Task-Decomposer` and `Orchestrator-Logic` modules.
*   **Behavioral change:** When `ON`, the Orchestrator mandates a Lookout spawn for all `verification_required` tasks. When `OFF`, the Orchestrator relies solely on the Specialist's self-reported `assertion_status` (allowing for "untrusted" mode).

## Testing Decisions

*   **Robustness Testing:** Injecting "Malicious Probes" (e.s., commands attempting to escape the sandbox) to ensure the Lookout Agent's isolation holds.
*   **Accuracy Testing:** Testing the `Artifact-Extractor` against a wide variety of messy, unstructured `stdout` to ensure it correctly identifies `diffs` and `hashes`.
*   **Performance Testing:** Measuring the latency overhead of spawning a Lookout Agent to ensure it does not significantly delay the total task lifecycle.

### Integration Acceptance Test

**Test: The "Truth-Alignment" Verification Test**
1.  **Setup:** Orchestrator is running with `lookout_enabled: true`.
2.  **Action:** Dispatch an A2A task to a Specialist that claims to have created a file at `/tmp/success.txt`, but the file does not actually exist.
3.  **Verify (Lookout):** The Lookout Agent must run `ls /tmp/success.txt`, detect the `ENOENT` error, and return an `assertion_status: "failed"` in its response.
4.  **Verify (Orchestrator):** The Orchestrator must receive the Lookout's report and transition the primary task status to `REJECTED`.
5.  **Verify (Alerting):** An alert must be emitted to the `Memory OS` indicating a "Verification Mismatch" between the Specialist's claim and the Lookout's discovery.

## Out of Scope
*   The high-level reasoning or "strategy" of the task (this is the Orchestrator's job).
*   The heavy-duty computational work (the "payload" execution is the Specialist's job).
*   Long-term monitoring of the system (the Lookout is strictly transient).

## Further Notes
The Lookout Agent is the "Trust Anchor" of the Orion ecosystem. If the Lookout is compromised, the entire concept of "Verifiable Autonomy" vanishes. Therefore, security and isolation are the highest priority design constraints for this module.
