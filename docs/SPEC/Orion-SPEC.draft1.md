---
title: Orion Specification
status: Draft v1
authors: Joseph Bironas
created: 2026-05-09
last_updated: 2026-05-09
related:
  - docs/PRD/orion-v1.md
  - docs/research/SPEC.md (Symphony service spec, used as template)
  - docs/research/orchestrated-development-workflow.md
  - docs/research/2026-05-08-skills-pipeline-experiment.md
  - docs/research/reliability-conductor.md
---

# Orion Specification

> **Purpose**: Define a long-running service that takes a software project (existing codebase, design document, or both) plus a backlog of issues from one or more trackers, and autonomously drives that backlog to completion through verified, human-reviewable pull requests. Orion is the autonomous closed-loop layer of the Revelara platform; Polaris is the human-augmenting layer.

> **Conformance language**: The keywords MUST, MUST NOT, REQUIRED, SHALL, SHALL NOT, SHOULD, SHOULD NOT, RECOMMENDED, MAY, and OPTIONAL are interpreted as in RFC 2119.

> **Scope of v1**: Go codebases. Three reliability patterns (timeout coverage, retry hygiene, idempotency-key insertion). Brownfield scan, autonomous risk-issue generation, autonomous remediation through one PR per issue. SaaS deployment. No production access. No auto-merge to protected branches. v2+ extends languages, patterns, and greenfield design synthesis as defined in §9.

---

## 1. Mission and Problem Statement

### 1.1 Mission

Engineering organizations operating non-trivial systems accumulate reliability and performance debt faster than they pay it down. Reliability work loses to feature work in every sprint that does not immediately follow an incident. Existing tooling does not solve this:

- **Linters and static analysis** flag patterns but produce too many false positives and offer no verification that proposed fixes improve the system.
- **Chaos engineering tools** require running systems and human-driven experiments. They surface weaknesses but do not fix them.
- **AI code assistants** propose changes but cannot reason about behavior under load, fault, or partial-failure conditions.
- **APM tools** report what already broke, after it broke.
- **Polaris**, today, is a human-in-the-loop discovery and guidance product. Developers initiate scans, review risks, and apply fixes one at a time.

The org has a *systemic* reliability and performance debt problem, and it cannot get out of it without an order-of-magnitude productivity multiplier on reliability work.

### 1.2 What Orion Is

**Orion takes a codebase (and/or a design document) plus a backlog of issues, and produces a stream of verified pull requests that strictly improve the system along every reliability and performance axis measured.**

Orion runs as a long-lived service. It:

1. **Discovers risks** in the connected codebase or design (the *scan loop*).
2. **Files reliability-risk issues** into the customer's tracker of choice (GitHub Issues, Linear, beads), normalized through a single connector contract.
3. **Pulls eligible issues** from the unified backlog (Orion-filed risks and human-filed issues alike), prioritizes them, and dispatches each one to an isolated worker.
4. **Synthesizes a constraint surface** from the architectural model and Polaris's controls catalog, materializes a verification harness, generates candidate patches, and verifies each patch against the harness.
5. **Opens a pull request** containing only patches that strictly dominate the baseline, with a reproducible verification report attached.
6. **Reports completion** to Polaris, marking the source risk as remediated and linking the PR.
7. **Loops** until the eligible backlog is empty or a stop signal is received.

Orion never writes to protected branches. Orion never touches production. Orion never trains on customer code.

### 1.3 Within the Revelara Platform

| Product | Role | Buyer | Pricing tier |
|---|---|---|---|
| **Polaris** | Human-augmenting reliability product. Discovers risks, surfaces controls, guides engineers. | SRE Manager | All tiers |
| **Orion** | Machine-acting reliability product. Removes risks autonomously, ships verified improvements. | VP Eng / CTO | Architect intelligence multiplier on Growth ($1,999) and Enterprise ($5K+) |

Orion is to software reliability what an EDA synthesis-and-verification toolchain is to hardware: a closed loop that takes a high-level design and emits a verified, production-ready implementation. The Conductor 2.0 paradigm applies this idea to hardware engineering agents; Orion applies it to distributed software systems.

---

## 2. Goals and Non-Goals

### 2.1 Goals

Orion MUST:

1. Operate as a long-running service that polls one or more trackers, reconciles state, and dispatches work without per-issue human invocation.
2. Run worker sessions in isolated workspaces that are network-restricted, ephemeral, and per-tenant.
3. Maintain durable orchestration state so that restart, crash, or operator stop-and-start does not lose claimed work, in-flight verification, or pending PR delivery.
4. Generate reliability-risk issues into the customer's tracker(s) when scanning surfaces a control gap and no equivalent open issue exists.
5. Drive an issue from `claimed` through `verified` through `pull-request-open` through `closed` without prompting a human for any step that does not require human judgment.
6. Stop and surface a clear escalation when human judgment IS required (ambiguous requirement, irrecoverable conflict, sensitivity boundary, verification failure with no remediation).
7. Produce a reproducible verification report for every PR Orion opens, including harness configuration, baseline metrics, patched metrics, deltas, and operating envelope.
8. Enforce all merge-eligibility gates outside the agent's reasoning loop (in CI, infrastructure, and signed-off automation), so the agent has no path to rationalize past them.
9. Tolerate pre-existing rot in the customer's main branch via subset-comparison gates ("does this make things worse than main?") rather than absolute gates ("do all tests pass?").
10. Isolate every customer's codebase, harness, secrets, and metrics from every other customer's, including in-process memory and persistent state.

### 2.2 Non-Goals

Orion MUST NOT:

1. **Auto-merge** to protected branches. Branch-protection rules, code-owner review, and CI-required-checks are honored as the customer configured them.
2. **Connect to production** systems, consume customer telemetry, scrape Grafana, or call into runtime infrastructure. Codebase plus IaC plus design documents only.
3. **Train models on customer code.** Per-tenant LLM calls, no retention beyond the run window, no cross-customer signal extraction.
4. **Drive multi-repo or monorepo with multiple services in a single run** in v1. One repo, one service per run.
5. **Be a general-purpose coding agent.** Orion's prompts, tooling, harness, and verification are scoped to reliability and performance synthesis. Feature work is out of scope.
6. **Perform destructive remote operations** (force-push, branch-delete on origin, issue-close-without-merge, repository deletion, workflow-disable) under any agent-driven path.
7. **Run skills-style design-time prompts** as long-running orchestration. The orchestrator is implemented in service code, not in agent prompt text. (Lesson from skills-pipeline experiment, §22.)
8. **Operate without an explicit, verifiable per-tenant scope** on every tracker write, every Polaris API call, every PR open, and every harness namespace.

### 2.3 Things Orion Is Deliberately Silent About

Orion does not prescribe:

- The customer's branch-protection model. Whatever exists, Orion respects.
- The customer's review process. Orion's PRs enter review like any other PR.
- The customer's CI provider. Orion runs its harness in its own infrastructure, then opens a PR which the customer's CI evaluates as configured.
- The customer's tracker of record. Orion adapts to GitHub Issues, Linear, or beads; the customer chooses.

---

## 3. System Overview

### 3.1 Conceptual Architecture

Orion is built as four interlocking loops:

1. **Scan Loop.** Reads codebase and design documents, infers an architectural model, derives a constraint surface (the SLO Fabric), identifies control gaps, and files reliability-risk issues into the customer's tracker(s) when no equivalent open issue exists.
2. **Backlog Loop.** Polls connected trackers, normalizes issues into a unified backlog, applies eligibility and priority rules, and emits a stream of dispatch-ready issues.
3. **Synthesis Loop.** For each dispatched issue: instantiates a sandbox, materializes a verification harness, generates candidate patches, verifies each patch against the harness, composes accepted patches into a sequence, and opens a PR.
4. **Reconciliation Loop.** Tracks open PRs, watches for merge or close, updates Polaris with run completion, and feeds outcomes back into the scan loop's prioritization.

These four loops share state through a single durable substrate (§7), are coordinated by a single authority (the Conductor, §16), and emit observations on a single event stream (§21).

### 3.2 Layered Architecture

Orion is organized in six horizontal layers. Each layer has a stable port to the layer below it. New trackers, new languages, new patch synthesizers, and new verifiers are added by writing adapters, not by modifying core orchestration.

| Layer | Responsibility | Examples of components |
|---|---|---|
| **L1 Policy** | Repo-defined and tenant-defined configuration. What to scan, what trackers to use, what controls to enforce. | `.orion/config.yaml`, Polaris feature flags, GitHub App scopes |
| **L2 Coordination** | The Conductor, the backlog, the run state machine, dispatch, reconciliation. | `internal/conductor`, `internal/backlog`, `internal/runs` |
| **L3 Synthesis** | Architectural inference, constraint inference, harness synthesis, patch synthesis, verification, optimization. | `internal/architect`, `internal/constraints`, `internal/harness`, `internal/patches`, `internal/verify` |
| **L4 Worker Execution** | Per-issue sandbox, worker session lifecycle, agent runner protocol. | `internal/sandbox`, `internal/worker`, `internal/agent` |
| **L5 Integration** | Tracker adapters, Polaris client, GitHub App, LLM providers, storage. | `internal/trackers/{github,linear,beads}`, `internal/polaris`, `internal/github`, `internal/llm`, `internal/storage` |
| **L6 Observability** | Logging, run reports, metrics, status surface. | `internal/log`, `internal/report`, `internal/metrics`, `internal/api` |

The Conductor (L2) is the only component permitted to mutate orchestration state. All worker outcomes flow back to the Conductor and are converted into explicit state transitions (§7).

### 3.3 External Dependencies

| Dependency | Purpose | Trust |
|---|---|---|
| **Polaris API** | Risk register source, controls catalog, knowledge enrichment, evidence sink, run-completion callback. | Trusted (same operator) |
| **Customer's tracker(s)** | Issue source and sink. GitHub Issues, Linear, beads in v1. | Customer-controlled |
| **GitHub App** (per repo) | Clone, branch-create, PR-open, comment-post. Scoped per install. | Customer-controlled |
| **LLM provider** (Vertex AI in v1) | Patch synthesis. Per-tenant configuration. | Vendor-trusted |
| **Container runtime** (Kubernetes in v1) | Per-run namespace for sandbox and harness. | Operator-controlled |
| **PostgreSQL** (Orion's own DB) | Durable orchestration state, run records, accepted patches, metrics. RLS-enforced per `org_id`. | Operator-controlled |
| **Object storage** (GCS or S3) | Verification report archives, harness artifact archives. | Operator-controlled |

Orion's database is **separate from Polaris's database**. The two services communicate over signed HTTPS only.

---

## 4. Core Domain Model

### 4.1 Entities

This section defines the durable and in-memory entities Orion manipulates. Field types are illustrative; concrete schema lives in `internal/database/migrations/`.

#### 4.1.1 ConnectedRepo

A customer-installed repository under Orion's GitHub App.

| Field | Type | Notes |
|---|---|---|
| `id` | UUID | Primary key. |
| `org_id` | UUID | Tenant. RLS-enforced. |
| `provider` | enum {`github`} | v1 is GitHub-only. v2+ may add GitLab, Bitbucket. |
| `app_install_id` | string | Provider-specific install identifier. |
| `repo_full_name` | string | e.g., `customer/service`. |
| `default_branch` | string | Resolved at install. |
| `service_path` | string or null | Optional sub-path for monorepos. v1 supports one service per repo. |
| `enabled` | bool | Operator can pause without uninstalling. |
| `created_at`, `updated_at` | timestamp | |

#### 4.1.2 TrackerBinding

A customer-configured tracker connected to a repo. A repo MAY have multiple bindings (e.g., GitHub Issues plus Linear). Issues from all bindings flow into a single unified backlog (§8).

| Field | Type | Notes |
|---|---|---|
| `id` | UUID | |
| `org_id` | UUID | RLS. |
| `repo_id` | UUID | Foreign key to ConnectedRepo. |
| `kind` | enum {`github_issues`, `linear`, `beads`} | v1. v2+ adds `jira`. |
| `config` | JSONB | Adapter-specific (e.g., Linear project slug, GitHub label filter). |
| `credentials_ref` | string | Reference to encrypted secret in vault. |
| `enabled` | bool | |

#### 4.1.3 Run

One unit of work. A Run executes the full pipeline for one connected repo: scan, file risks (if any), drive backlog, deliver PRs.

| Field | Type | Notes |
|---|---|---|
| `id` | UUID | |
| `org_id`, `repo_id` | UUID | |
| `mode` | enum {`scan_only`, `synthesis_only`, `full_loop`} | `scan_only` files risks but does not synthesize. `synthesis_only` skips scan and pulls from existing backlog. `full_loop` is default. |
| `trigger` | enum {`manual`, `scheduled`, `webhook`} | |
| `status` | enum (see §7.1) | |
| `commit_sha` | string | The commit at which the scan and synthesis are anchored. |
| `started_at`, `finished_at` | timestamp | |
| `stop_reason` | string or null | Set when status is terminal. |

#### 4.1.4 ArchitecturalModel

Per-run inference of the system under analysis. Persisted as JSONB.

| Field | Type | Notes |
|---|---|---|
| `id` | UUID | |
| `run_id` | UUID | One per run. |
| `services` | JSONB | List of services, endpoints, downstream dependencies. |
| `hot_paths` | JSONB | Inferred high-frequency request paths. |
| `persistent_stores` | JSONB | Databases, queues, object stores. |
| `inferred_at` | timestamp | |

#### 4.1.5 ConstraintSurface (SLO Fabric)

The set of constraints the patched system MUST satisfy. Combination of explicit Polaris controls and code-derived implicit constraints.

| Field | Type | Notes |
|---|---|---|
| `id` | UUID | |
| `run_id` | UUID | |
| `controls` | JSONB | Polaris controls in scope (queried via Polaris API). |
| `implicit_constraints` | JSONB | Inferred from code (existing timeouts → assumed budgets, etc.). |
| `inferred_at` | timestamp | |

#### 4.1.6 Harness

The synthesized verification environment for one run.

| Field | Type | Notes |
|---|---|---|
| `id` | UUID | |
| `run_id` | UUID | |
| `workload_config` | JSONB | Synthesized request distributions per endpoint. |
| `fault_config` | JSONB | Synthesized network/latency/error fault profiles. |
| `materialization` | JSONB | testcontainers + toxiproxy + namespace metadata. Ephemeral; namespace cleaned up on run end. |

#### 4.1.7 NormalizedIssue

An issue from any tracker, normalized into a canonical shape for the backlog.

| Field | Type | Notes |
|---|---|---|
| `id` | UUID | Orion's internal identifier. |
| `org_id`, `repo_id`, `tracker_binding_id` | UUID | Provenance. |
| `external_id` | string | Tracker-native identifier (e.g., `gh:owner/repo#123`, `lin:ABC-456`, `bd:po-r3dq`). |
| `external_url` | string | Direct link in the source tracker. |
| `title` | string | |
| `description` | string | |
| `priority` | int or null | Tracker-native priority normalized to a 0-4 scale. |
| `state` | enum {`open`, `in_progress`, `blocked`, `closed`, `cancelled`} | Normalized from tracker-native states. |
| `labels` | string[] | Normalized labels (lowercased, deduplicated). |
| `polaris_risk_id` | UUID or null | Set if this issue corresponds to a Polaris-tracked risk. |
| `orion_filed` | bool | True if Orion created this issue (scan-loop output). |
| `claim_status` | enum (see §7.2) | |
| `last_synced_at` | timestamp | |

#### 4.1.8 CandidatePatch and AcceptedPatch

| Field | Type | Notes |
|---|---|---|
| `id` | UUID | |
| `run_id`, `issue_id` | UUID | |
| `target_path`, `target_range` | string, JSONB | File and line range. |
| `diff` | text | Unified diff. |
| `control_id` | UUID | The Polaris control this patch addresses. |
| `verdict` | enum {`pending`, `accepted`, `rejected_no_dominance`, `rejected_regression`, `rejected_unsafe_combination`, `error`} | |
| `metrics` | JSONB | Baseline and patched metrics for each measured axis. |
| `created_at`, `verified_at` | timestamp | |

AcceptedPatch is a view: `CandidatePatch WHERE verdict = 'accepted'`.

#### 4.1.9 WorkerSession

In-memory record of one running worker. Recoverable on restart by inspecting durable state plus tracker.

| Field | Type | Notes |
|---|---|---|
| `id` | UUID | |
| `run_id`, `issue_id` | UUID | |
| `phase` | enum (see §7.3) | |
| `sandbox_namespace` | string | K8s namespace name. |
| `agent_session_id` | string | LLM provider session identifier. |
| `last_event_at` | timestamp | Used for stall detection. |
| `tokens_in`, `tokens_out` | int | Token accounting. |

#### 4.1.10 PullRequest

| Field | Type | Notes |
|---|---|---|
| `id` | UUID | |
| `run_id`, `issue_id` | UUID | |
| `provider_pr_url` | string | e.g., `https://github.com/customer/repo/pull/123`. |
| `branch_name` | string | Orion-created. |
| `state` | enum {`open`, `merged`, `closed_unmerged`, `superseded`} | Reconciled from provider. |
| `report_url` | string | Object-storage URL for the verification report archive. |
| `opened_at`, `closed_at` | timestamp | |

### 4.2 Stable Identifiers and Normalization Rules

- **`external_id` format**: `<provider>:<scope>#<id>` where provider is `gh`, `lin`, or `bd`; scope is `owner/repo`, project key, or beads prefix.
- **Branch naming**: `orion/<run_id_short>-<issue_external_id_sanitized>`. Example: `orion/r3dq8a-gh-customer-svc-123`.
- **Sandbox namespace naming**: `orion-run-<run_id>`. Sanitized to `[a-z0-9-]` only, max 63 chars.
- **Workspace key (per worker)**: `<run_id>-<issue_internal_id>`. Used as a directory name and a sandbox sub-namespace label.

All sanitization MUST reject characters outside the documented set rather than silently rewriting them; rejection produces an explicit error with the offending character logged.

---

## 5. Project Contract (`.orion/config.yaml`)

Each connected repository MAY contain a `.orion/config.yaml` file at the repo root. This file is the customer-owned policy layer for that repo. It is version-controlled, reviewable, and changes follow the customer's normal PR workflow.

If absent, Orion uses tenant-level defaults from the Polaris organization settings.

### 5.1 File Format

```yaml
# .orion/config.yaml
version: 1

repo:
  service_path: cmd/svc       # for monorepos; defaults to repo root
  language: go                 # v1 supports go only; rejected with clear error otherwise

trackers:
  - kind: github_issues
    label_filter: ["reliability", "orion-eligible"]
    auto_file: true            # Orion may create issues here
  - kind: linear
    project_slug: ABC
    state_active: ["Todo", "In Progress"]
    state_terminal: ["Done", "Cancelled"]
    auto_file: false           # Orion reads but does not file here

scan:
  cadence: weekly              # one of: on_demand, daily, weekly, on_push
  excludes:
    - vendor/
    - **/*_generated.go
    - testdata/

synthesis:
  patterns:                    # v1 must be a subset of these three
    - timeout_coverage
    - retry_hygiene
    - idempotency_keys
  ineligible_paths:            # Orion will not patch these
    - internal/auth/
    - internal/billing/
    - internal/payments/

gates:
  pre_pr:
    - command: go build ./...
    - command: go vet ./...
    - command: golangci-lint run
  pr_body_template: .orion/pr_template.md   # optional override
  require_signed_commits: true              # default true
  require_subset_comparison: true           # default true; see §17.3

orchestration:
  max_concurrent_workers: 4
  worker_timeout: 1h
  stall_timeout: 15m
  max_retries_per_issue: 2
  ineligible_branches:         # never operate on these
    - main
    - master
    - release/*

escalation:
  human_review_label: orion-needs-review
  ineligible_labels:           # Orion will not claim issues with these
    - do-not-touch
    - human-only
```

### 5.2 Validation

Orion MUST validate `.orion/config.yaml` at repo connection time and again at the start of every run. Validation errors MUST:

1. Block the run from starting (status set to `config_invalid` with explicit error).
2. Surface the error in the Orion runs list in Polaris.
3. NOT proceed with stale or partial config.

Unknown keys MUST be rejected, not silently ignored, to prevent typo-induced silent misconfiguration.

### 5.3 Dynamic Reload

Orion does NOT hot-reload `.orion/config.yaml` mid-run. The config that was valid at the start of a run is the config used for the entire run. This eliminates a class of mid-run state inconsistency (skills-pipeline lesson, Pattern C).

A new run picks up the config at the head of the default branch at the moment the run starts.

---

## 6. Configuration Specification

### 6.1 Resolution Pipeline

Configuration resolves in this order, later overriding earlier:

1. Code defaults (compiled in).
2. Operator-set deployment defaults (`/etc/orion/orion.yaml`).
3. Tenant-level config from Polaris organization settings (fetched per run).
4. Repo-level config from `.orion/config.yaml`.
5. Per-run override from API call body or CLI flag (operator-only, audited).

### 6.2 Secret Handling

Secrets MUST come from environment variables, mounted secret files, or a vault. Secrets MUST NOT appear in `.orion/config.yaml` (which is committed to the repo) or in any persisted run record. Logged config dumps MUST redact known secret keys.

### 6.3 Per-Tenant Isolation

All persisted state with an `org_id` MUST follow the Polaris RLS pool selection rule: per-tenant queries use `*db.RLSPool`, cross-tenant system queries use the raw pool with `SET LOCAL ROLE polaris_admin` and explicit org filtering. Background jobs (scheduled scans, reaper, restart-recovery) seed RLS context with `db.WithRLSContext(ctx, userID, orgID, nil)` at the boundary where a system path becomes a per-tenant path.

---

## 7. Orchestration State Machine

### 7.1 Run States

```
created → scanning → backlog_active → draining → completed
                          ↓                 ↑
                       paused ────────────-┘
                          ↓
                       cancelled
                          ↓
                       failed
                          ↓
                  config_invalid
```

| State | Meaning |
|---|---|
| `created` | Run record persisted; no work started yet. |
| `scanning` | Scan loop active; architectural inference and risk filing in progress. |
| `backlog_active` | At least one worker is running OR the backlog still has eligible issues. |
| `draining` | Operator or schedule signaled stop; finishing in-flight workers; no new dispatch. |
| `completed` | Backlog empty, no in-flight workers, all PRs delivered. |
| `paused` | Operator-paused; in-flight workers paused at next safe point; resumable. |
| `cancelled` | Operator-cancelled; in-flight workers cleaned up; non-resumable. |
| `failed` | Unrecoverable error (e.g., Polaris unreachable, GitHub App revoked). |
| `config_invalid` | `.orion/config.yaml` failed validation. |

### 7.2 Issue Claim States

For each NormalizedIssue in the unified backlog:

```
unclaimed → claimed → dispatched → in_progress → pr_open → reconciling → released
                                       ↓             ↓                      ↑
                                   escalated   superseded ──────────────────┤
                                       ↓                                     │
                                  human_review ─────────────────────────────┘
```

| State | Meaning |
|---|---|
| `unclaimed` | Visible in backlog, eligible per filter rules. |
| `claimed` | Reserved by Orion (in DB) so duplicate dispatch is impossible. |
| `dispatched` | Worker session created; sandbox provisioning. |
| `in_progress` | Worker actively running synthesis or verification. |
| `pr_open` | PR delivered; awaiting customer review and merge. |
| `reconciling` | Reconciler observed PR state change; updating Polaris. |
| `released` | Terminal: issue closed, merged, superseded, or escalated and acknowledged. |
| `escalated` | Worker hit an unresolvable condition; surfaced to operator/human. |
| `human_review` | Customer requested human review on the issue side; Orion will not re-claim. |
| `superseded` | Same issue claimed under a newer Orion run; this claim is stale. |

The `claimed` state is durable (DB row), not in-memory only. This makes restart recovery robust: on startup, Orion re-reads claimed-but-not-released issues, reconciles their actual state with the tracker and the worker session, and either resumes, releases, or escalates. (Symphony's in-memory-only claim model is REJECTED here because Orion is multi-process and multi-host.)

### 7.3 Worker Session Phases

Within `dispatched` and `in_progress`:

```
preparing_sandbox → synthesizing_model → synthesizing_constraints
                  → synthesizing_harness → synthesizing_patches
                  → verifying_patches → composing_patches
                  → opening_pr → succeeded
                                              ↓
                                          failed | timed_out | stalled | cancelled
```

Each phase has:

- A maximum duration (`worker_timeout` is the total cap; per-phase caps are derived).
- A clean cancellation point (sandbox teardown, harness namespace deletion, no orphan resources).
- A structured error class on failure (§19).

### 7.4 Idempotency and Recovery Rules

1. **Issue claim is durable.** A claim is a DB row with `(org_id, issue_external_id)` UNIQUE. Re-claim attempts on the same issue MUST be rejected at the database layer.
2. **Sandbox creation is idempotent on namespace name.** If the namespace exists, Orion verifies it belongs to this run and reuses it; otherwise treats as a conflict and fails fast.
3. **PR creation is idempotent on branch name.** If the branch and PR exist, Orion reconciles state and does NOT open a duplicate.
4. **Polaris callbacks are retried with exponential backoff** until acknowledged or `max_callback_retries` (default 10) exhausted; on exhaustion, the run is marked `failed` and operator notified.
5. **Restart recovery**: on startup, the Conductor reads all runs in non-terminal states, reconciles each (alive workers, dead workers needing cleanup, runs needing retry), and resumes orchestration. No manual intervention is required for routine restarts.

---

## 8. Issue Ingestion and Backlog Drive

### 8.1 Tracker Adapter Contract

Every tracker integration implements the `TrackerAdapter` interface:

```go
type TrackerAdapter interface {
    Kind() TrackerKind
    FetchCandidates(ctx context.Context, binding TrackerBinding, since time.Time) ([]NormalizedIssue, error)
    FetchByExternalIDs(ctx context.Context, binding TrackerBinding, ids []string) ([]NormalizedIssue, error)
    Create(ctx context.Context, binding TrackerBinding, draft IssueDraft) (NormalizedIssue, error)
    UpdateState(ctx context.Context, binding TrackerBinding, externalID string, state NormalizedState) error
    Comment(ctx context.Context, binding TrackerBinding, externalID, body string) error
    Capabilities() TrackerCapabilities
}
```

`Capabilities` declares whether the adapter supports (a) reading, (b) creating, (c) updating state, (d) commenting, and (e) webhooks. Orion MUST gracefully degrade when capabilities are missing (e.g., if a tracker doesn't support state updates, Orion comments instead).

### 8.2 v1 Adapters

| Kind | Read | Create | Update | Comment | Webhook |
|---|---|---|---|---|---|
| `github_issues` | yes | yes | yes (close/reopen, label) | yes | yes |
| `linear` | yes | yes | yes | yes | yes |
| `beads` | yes | yes | yes | yes (notes) | no (poll-only) |

### 8.3 Unified Backlog

The Conductor merges issues from all bindings of a connected repo into a single in-memory backlog. The backlog is keyed by Orion's internal `id` and deduplicated where the same risk has been filed in two trackers (e.g., a GitHub Issue and a beads bead both pointing to the same Polaris risk; deduplication is by `polaris_risk_id` when set).

### 8.4 Eligibility Rules

A NormalizedIssue is eligible for dispatch if and only if all of the following hold:

1. `state ∈ {open}` per the tracker's normalized active states.
2. `claim_status = unclaimed`.
3. None of the issue's labels are in the binding's `ineligible_labels` set.
4. None of the issue's referenced file paths fall in the repo's `ineligible_paths` set (auth, billing, payments by default).
5. If the issue declares a target branch via convention, that branch is not in `ineligible_branches`.
6. The issue has no open blockers (where the tracker exposes blocker semantics).
7. The issue's pattern (inferred from labels or Polaris risk linkage) is in the synthesis `patterns` allowlist.
8. The customer's tier permits Orion (Architect intelligence multiplier active).

### 8.5 Priority

Among eligible issues, the dispatch order is:

1. Issues linked to Polaris risks with `severity=critical` first.
2. Then by Polaris risk `score` descending.
3. Then by tracker priority (1 = highest).
4. Then by `created_at` ascending (FIFO).

Ties broken deterministically by `external_id` lexical order.

### 8.6 Concurrency Control

The Conductor MUST NOT exceed `max_concurrent_workers` per run. Across runs, a global cap MAY be enforced operator-side. Before dispatching a worker, the Conductor MUST claim the issue in the database (state transition `unclaimed → claimed`) in a transaction that includes the cap check; this prevents over-dispatch under restart-recovery races.

### 8.7 Auto-Filed Risk Issues (Scan Loop Output)

When the scan loop identifies a control gap and no equivalent open issue exists in any tracker (matched by Polaris risk ID and by content hash), Orion MAY file a new issue if the binding has `auto_file: true`. Filed issues:

1. Carry the label `orion-filed`, the binding's `auto_file_labels`, and the corresponding Polaris risk ID in the body.
2. Include the inferred pattern, the affected file:line, and a link to the Polaris risk detail.
3. Are eligible for Orion to claim and remediate immediately on the next backlog tick (subject to §8.4).
4. Are subject to the customer's normal CODEOWNERS / branch-protection rules.

To prevent issue spam, Orion MUST:

- Deduplicate by `(polaris_risk_id, pattern, file_path)`.
- Cap auto-filed issues per run to `scan.max_auto_filed_per_run` (default 25).
- Honor a tenant-wide cap (`scan.max_auto_filed_per_24h`, default 100).

---

## 9. Brownfield and Greenfield Modes

### 9.1 Brownfield (v1)

The default. Orion is given a connected repo with existing code. Orion:

1. Clones the repo at the head of the default branch.
2. Runs static analysis to infer the architectural model (§4.1.4).
3. Cross-references with the Polaris controls catalog and risk register.
4. Files reliability-risk issues if scan-loop is enabled.
5. Drives the resulting backlog through synthesis.

This is the v1 PRD scope and the only mode required for v1 release.

### 9.2 Greenfield (v2+)

Orion is given a design document (Markdown, possibly with embedded diagrams) plus an empty or near-empty repo. Orion:

1. Parses the design document into a proto-architectural model.
2. Synthesizes a constraint surface from the design.
3. Generates scaffolding patches (skeleton service, baseline reliability patterns wired from the start).
4. Verifies the scaffolding under the synthesized harness.
5. Opens the initial PR(s) to populate the repo.

Greenfield mode is OUT OF SCOPE for v1 and MUST NOT be selectable from the v1 API. The spec defines it here so that the L3 Synthesis layer is designed to support both modes from day one (a single architectural-model interface, with brownfield and greenfield as alternative front-ends).

### 9.3 Hybrid (v2+)

A connected repo plus a design document for a planned new component. Orion synthesizes the new component as in greenfield mode and integrates it with the existing codebase as in brownfield mode. Out of v1 scope.

---

## 10. Workspace and Sandbox Management

### 10.1 Workspace Layout

For each WorkerSession, Orion provisions:

```
/sandbox-root/<workspace_key>/
├── repo/                  # ephemeral clone, single commit deep
├── harness/               # synthesized harness materialization
├── patches/               # candidate patches as files
├── reports/               # in-progress verification artifacts
└── .orion-meta/           # run_id, issue_id, agent session metadata
```

`/sandbox-root` is a tmpfs-backed directory inside the per-run Kubernetes namespace. The entire workspace is destroyed on worker terminate (success or failure), with a 24-hour grace window for debugging when the operator opts in.

### 10.2 Sandbox Isolation Requirements

Each per-run namespace MUST:

1. Have **no egress** to the public internet except to: the LLM provider endpoint, the customer's Git provider, and Orion's own control plane (Polaris, run database, object storage).
2. Have **no ingress** except from Orion's own control plane.
3. Have **no shared volumes** with any other namespace.
4. Have **no shared secrets** with any other tenant's namespace; secrets are per-run, scoped, and rotated.
5. Be **destroyed within 24 hours** of run termination, regardless of debug grace.

### 10.3 Safety Invariants

Numbered, non-negotiable:

1. **The agent runs only inside the workspace.** Before launching the agent subprocess, Orion validates `cwd == /sandbox-root/<workspace_key>/repo`. Validation failure aborts the worker.
2. **The workspace path stays inside `/sandbox-root`.** Symbolic-link traversal MUST be rejected.
3. **The workspace key is sanitized.** Only `[A-Za-z0-9._-]` allowed.
4. **The agent never receives credentials for the customer's production systems.** The only credentials in scope are: an ephemeral GitHub App token scoped to the connected repo, and an LLM provider token scoped to this run.
5. **Orion never operates on `main`, `master`, or branches matching `ineligible_branches` patterns.** Branch validation occurs before any push.

### 10.4 Cleanup Hooks

Each worker phase boundary fires a cleanup hook. On hook failure, the cleanup is retried up to `cleanup_max_retries`; on exhaustion, the namespace is forcibly deleted by an operator-controlled reaper.

---

## 11. Worker and Agent Runner Protocol

### 11.1 Worker Spawn Mechanism

Orion does NOT spawn workers as VS Code windows, tmux panes, or operator-side processes. (This is a deliberate departure from Gastown's design, which is local-environment-specific and does not translate to SaaS.)

A worker is a Kubernetes pod in the run's namespace. The pod runs the `orion-worker` binary, which:

1. Reads its assignment (run_id, issue_id, workspace_key) from a downward-API-injected env var.
2. Pulls the issue, model, constraints, and harness from Orion's database.
3. Materializes the harness in-namespace.
4. Connects to the LLM provider for patch synthesis.
5. Runs the verification loop.
6. Opens the PR via the GitHub App.
7. Reports completion to the Conductor and exits.

Worker pods are stateless. All durable state is in Orion's database. Worker death (OOM, eviction, network) is recoverable: the Conductor's reconciler observes the dead worker, marks the WorkerSession `failed`, and either retries or escalates per `max_retries_per_issue`.

### 11.2 Agent Runner Contract

Inside the worker, the `AgentRunner` interface mediates LLM interaction:

```go
type AgentRunner interface {
    StartSession(ctx context.Context, system Prompt) (SessionID, error)
    Turn(ctx context.Context, sid SessionID, userMsg string, tools []ToolDef) (TurnResult, error)
    Cancel(ctx context.Context, sid SessionID) error
}
```

A `Turn` MUST emit incremental events through a streaming channel:

| Event | Meaning |
|---|---|
| `tokens_in_progress` | Partial response chunk. Updates `last_event_at`. |
| `tool_call_requested` | Agent wants to invoke a tool. Subject to approval policy. |
| `tool_result` | Tool output (success or failure) returned to agent. |
| `turn_complete` | Final response with token counts and finish reason. |

`last_event_at` is the heartbeat used for stall detection. If `now - last_event_at > stall_timeout`, the Conductor's reconciler kills the worker.

### 11.3 Tool Policy

The agent has access to a strictly limited tool set:

| Tool | Scope |
|---|---|
| `apply_patch` | Apply a unified diff inside `repo/`. Validated for path safety. |
| `run_command` | Run a whitelisted command (`go build`, `go test`, `go vet`, `golangci-lint run`, `git status`, `git diff`). NO arbitrary shell. NO network calls. |
| `read_file` | Read a file inside the workspace. |
| `query_polaris_knowledge` | Read-only query against Polaris knowledge base, scoped to the tenant's accessible content. |
| `submit_patch_for_verification` | Hand a candidate patch to the verifier. |

Tools MUST NOT include: arbitrary shell, arbitrary HTTP, package install, git push, git remote modify, kubectl, or anything that can mutate state outside the workspace.

### 11.4 Continuation Turns and Token Budgeting

A worker MAY run multiple agent turns in one session. After each turn:

1. The Conductor's reconciler re-checks the issue state in the tracker.
2. If state is no longer `open`, the worker terminates with status `superseded`.
3. If `tokens_in + tokens_out > token_budget_per_issue`, the worker terminates with status `budget_exhausted` and escalates.

Continuation prompts SHOULD be terse ("continue from where you left off") rather than re-sending the original task prompt, to avoid token waste.

---

## 12. Synthesis Pipeline

### 12.1 Architectural Inference (`internal/architect`)

Inputs: cloned repo, language config.
Outputs: ArchitecturalModel.

The inferer parses Go source, builds a service-level dependency graph, identifies HTTP/gRPC endpoints, traces downstream client calls, and identifies persistent stores. Hot paths are inferred from request handler complexity and call frequency in test fixtures (no production telemetry).

The inferer MUST be deterministic on a given commit SHA: the same repo at the same commit produces the same ArchitecturalModel. This is testable with golden files.

### 12.2 Constraint Inference (`internal/constraints`)

Inputs: ArchitecturalModel, Polaris controls catalog.
Outputs: ConstraintSurface.

The inferer:

1. Calls `GET /api/v1/controls?categories=resilience,latency,idempotency` on the Polaris API, scoped to the tenant.
2. Derives implicit constraints from code: existing timeouts become assumed budgets, existing retry config becomes assumed error rates, IaC resource limits become resource constraints.
3. Resolves conflicts by preferring explicit Polaris controls over inferred constraints, logged.

### 12.3 Harness Synthesis (`internal/harness`)

Inputs: ArchitecturalModel, ConstraintSurface.
Outputs: Harness (workload + faults + materialization).

The workload synthesizer generates request distributions per endpoint based on inferred hot paths. The fault synthesizer generates network/latency/error fault profiles based on inferred dependencies. Both are seeded from a deterministic per-run seed so that the same run is reproducible.

### 12.4 Patch Synthesis (`internal/patches`)

Inputs: ArchitecturalModel, ConstraintSurface, control gaps.
Outputs: CandidatePatches.

For each detected control gap, the patch synthesizer prompts the LLM with: the affected code, the Polaris control text, the Polaris knowledge enrichment (`POST /api/knowledge/foresight` results for the relevant control), and a constrained patch grammar. Each candidate is stored as a CandidatePatch row.

### 12.5 Verification (`internal/verify`)

Inputs: CandidatePatch, baseline metrics, Harness.
Outputs: Verdict, metrics.

The verifier:

1. Applies the patch to a working copy in the sandbox.
2. Verifies the build succeeds.
3. Runs the harness against the patched system in container.
4. Collects metrics: tail latency under fault, cascade probability, baseline performance, resource usage.
5. Emits a Verdict.

A Verdict is `accepted` iff the patched metrics **strictly dominate** the baseline metrics on every measured axis with **zero regression**. Any regression on any axis MUST cause `rejected_regression`.

### 12.6 Optimizer Composition

Accepted patches are composed into a sequence. The composer:

1. Starts with the empty composition.
2. Greedily adds the highest-marginal-gain accepted patch.
3. Re-verifies the composition (interactions can produce regressions; e.g., timeout + retry without backoff produces retry storms).
4. If a composition step regresses, the offending patch is removed and the next-best candidate tried.
5. Terminates when no candidate improves the composition.

The composer is **greedy + verification-gated**, not formal. Orion does not claim formal optimality. Orion claims: "no patch in the candidate set strictly dominates what we shipped, given the harness, within the operating envelope of the synthesized workload."

### 12.7 Operating Envelope Reporting

The verification report MUST include the operating envelope:

- Workload type and request distributions.
- Fault types and intensities tested.
- Duration of each verification run.
- Hardware envelope (CPU, memory, network bandwidth).
- Seed used for reproducibility.

The customer SHOULD be able to replay the harness from the report alone.

---

## 13. Polaris Integration Contract

### 13.1 Authentication

Orion authenticates to Polaris as a per-tenant API key holder. Each customer's Polaris organization provisions an Orion service principal at install time. Orion holds one API key per tenant, stored encrypted at rest, rotated quarterly.

Required scopes per tenant API key: `risks:read`, `controls:read`, `knowledge:read`, `evidence:write`, `orion:claim`, `orion:complete`.

### 13.2 Polaris Endpoints Orion Calls (Read)

| Method | Path | Use |
|---|---|---|
| `GET` | `/api/v1/controls?categories=...` | Constraint inference. |
| `GET` | `/api/v1/risks?status=applicable` | Backlog seeding from existing risks. |
| `GET` | `/api/v1/risks/{id}` | Per-issue context. |
| `POST` | `/api/search` | Knowledge enrichment for patch synthesis. |
| `POST` | `/api/knowledge/foresight` | Causal-chain analysis for verification. |

### 13.3 Polaris Endpoints Orion Calls (Write)

| Method | Path | Use |
|---|---|---|
| `POST` | `/api/v1/evidence` | Submit accepted patch as evidence for the relevant control. |
| `POST` | `/api/v1/risks/{id}/claim-by-orion` | Reserve a risk during synthesis. New endpoint, MUST be added to Polaris (per PRD §4.1). |
| `POST` | `/api/v1/orion/run-complete` | Notify Polaris on PR open with `{run_id, pr_url, remediated_risk_ids[]}`. New endpoint, MUST be added to Polaris. |

### 13.4 Polaris Endpoints Polaris Surfaces for Customers (Orion-aware)

| Method | Path | Use |
|---|---|---|
| `GET` | `/api/v1/orion/runs` | List Orion runs for the tenant. |
| `GET` | `/api/v1/orion/runs/{id}` | Run detail with verification report. |

These are NEW Polaris endpoints. Polaris's frontend Remediations view consumes them.

### 13.5 Failure Semantics

If Polaris is unreachable when Orion needs to claim a risk or report run completion, Orion:

1. Retries with exponential backoff up to `polaris_callback_max_retries` (default 10, max delay 5min).
2. On exhaustion, the run transitions to `failed` with `stop_reason = "polaris_callback_exhausted"`.
3. Operator is notified via the standard observability channel.
4. The PR remains open in the customer's tracker; on operator recovery, the run is replayable to re-emit the callback.

---

## 14. Conductor Pattern (Symphony × Gastown Synthesis)

The Conductor is a single logical authority responsible for orchestration. In v1 it runs as a single replica with leader election (no concurrent Conductor instances per Orion deployment); v2+ may shard by tenant.

### 14.1 What the Conductor Does

1. Reads the unified backlog (§8).
2. Applies eligibility, deduplication, and priority rules.
3. Issues claim transactions in the database (state transition `unclaimed → claimed`).
4. Spawns worker pods in the per-run K8s namespace.
5. Reconciles worker liveness, tracker state, and PR state.
6. Handles escalation requests from workers.
7. Drives runs from `created` to terminal state.
8. Emits structured events on the observability stream (§21).

### 14.2 What the Conductor Does NOT Do

The Conductor does NOT:

1. Execute synthesis work. (That's the worker.)
2. Mutate code, branches, or PRs. (That's the worker via the GitHub App.)
3. Decide whether to merge. (That's the customer.)
4. Bypass any externally-enforced gate. (CI is authoritative; see §17.)

### 14.3 Communication Substrate

Workers and the Conductor communicate through:

1. **The database** (durable, primary). All state transitions, all WorkerSession updates, all CandidatePatch verdicts.
2. **A pub/sub channel** (ephemeral, observability and coordination). NATS or Redis Streams. Used for: stall detection events, worker-spawn notifications, worker-completion notifications.

There is no agent-to-agent direct communication. There is no in-memory orchestrator state that survives a restart unaided. (Symphony's in-memory-only model is a deliberate departure.)

### 14.4 Escalation Chain

When a worker hits an unresolvable condition, it emits an `EscalationRequest` with one of:

| Severity | Trigger | Conductor action |
|---|---|---|
| `info` | Verification produced no accepted patches; "clean run" | Close run for issue with status `no_improvement`; do NOT open PR. |
| `warn` | Patch synthesis exceeded budget without convergence | Mark issue `escalated`; do not retry without operator. |
| `error` | Sandbox or harness failure | Retry once; on second failure, mark issue `escalated`. |
| `critical` | Safety boundary touched (e.g., agent attempted out-of-workspace write) | Halt run immediately; mark `failed`; notify operator with full evidence. |

`error` and `critical` MUST NOT auto-resolve. They require operator acknowledgement.

### 14.5 Escalation Surfaces

Escalations surface in:

1. The Polaris Orion runs view, with severity, evidence, and operator-action button.
2. Operator notification channel (Slack, PagerDuty per tenant config).
3. Orion's structured log stream.

Stale escalations (unacknowledged past `stale_threshold`, default 4h) are re-escalated with bumped severity up to `max_reescalations` (default 3).

---

## 15. Brownfield Scan Loop

### 15.1 When the Scan Loop Runs

Per `.orion/config.yaml` cadence:

| Cadence | Trigger |
|---|---|
| `on_demand` | Manual API call only. |
| `daily` | Scheduled at tenant-configured hour. |
| `weekly` | Scheduled at tenant-configured day and hour. |
| `on_push` | GitHub App receives a push to default branch; Orion scans the new HEAD within `on_push_debounce` (default 10min). |

### 15.2 Scan Phases

1. **Clone.** Shallow clone at default-branch HEAD into a per-scan ephemeral sandbox.
2. **Infer.** Build ArchitecturalModel.
3. **Cross-reference.** Pull the tenant's existing Polaris risks, controls catalog, and existing tracker issues. Compute the set of new control gaps vs. already-tracked risks.
4. **File or skip.** For each new gap, if `auto_file: true` on at least one binding, file an issue (subject to caps in §8.7). Else: emit a `gap_unfiled` event to observability.
5. **Persist.** Record scan results in `orion.scans` for trend analysis.
6. **Tear down.** Destroy the scan sandbox.

### 15.3 Scan Output as Polaris Risks

For each filed issue, Orion also calls `POST /api/v1/risks` (or the tenant's equivalent) to ensure the risk exists in Polaris. The Polaris risk_id is included in the filed tracker issue's body, so the backlog drive can dedupe by risk.

---

## 16. PR Delivery and Merge Semantics

### 16.1 PR Composition

For each accepted-patch composition (§12.6):

1. A fresh branch is created via the GitHub App.
2. Each accepted patch becomes a separate signed commit, ordered by composition sequence, with a conventional-commit message including the Polaris control ID and the verification axis improved.
3. The PR body is the verification report (§12.7) rendered as markdown, plus a footer linking to the Polaris run ID and the report archive.
4. PR title follows: `orion: <issue-title> [<count> patches across <controls>]`.

### 16.2 Merge Gate (Subset-Comparison)

Orion does NOT auto-merge. Whatever CI the customer has configured runs against the PR.

**Critical lesson from skills-pipeline experiment**: an absolute "all tests must pass" gate fails when the customer's main branch already has failing tests. Orion's PR template MUST instruct customer CI to apply a **subset-comparison gate**:

> Compare the failing-test set on this PR's branch to the failing-test set on `main` at the same SHA. The PR is mergeable iff the branch's failing set is a subset of main's failing set.

Orion's verification report SHOULD include both the absolute CI result and the subset-comparison result. The customer remains the final authority on merge.

### 16.3 Branch Protection and Required Reviewers

If the customer's repo has branch protection requiring reviewers, Orion's PR sits in `awaiting review` until a human approves. **This is correct behavior and explicitly preserved.** Orion does NOT request reviewers automatically; it leaves the PR for the customer's CODEOWNERS automation.

### 16.4 PR Reconciliation

The reconciler polls the PR state (or consumes webhooks where supported):

| PR state | Orion action |
|---|---|
| `open` | Continue polling. |
| `merged` | Update Polaris: risk `mitigated`. Submit evidence. Mark issue `released`. |
| `closed_unmerged` | Mark issue `released` with reason `customer_rejected`. Do NOT auto-reopen. |
| `superseded` (Orion opened a newer PR for same issue) | Close the old PR with a link to the new. |

### 16.5 Rejection Learning

When a customer closes an Orion PR unmerged with a comment, the comment SHOULD be parsed and surfaced in the next scan-loop's prioritization (deprioritize patterns that get rejected). v1 logs and surfaces; v2+ adapts the patch synthesizer's prompts.

---

## 17. Quality Gates

### 17.1 Layered Gate Architecture

Orion's quality gates are organized into three tiers, mirroring the orchestrated-workflow design's lessons:

| Tier | Where enforced | Examples |
|---|---|---|
| **Tier 1: Pre-synthesis** | In the worker, before patch generation | Workspace integrity, sandbox isolation, sanitized identifiers |
| **Tier 2: Per-patch verification** | In the verifier, before patch acceptance | Build success, harness pass, dominance check, no regression |
| **Tier 3: Pre-PR delivery** | In the worker, before opening the PR | Composition re-verification, lint/format checks, customer's `.orion` `gates.pre_pr` commands |

Customer CI is **Tier 4**: outside Orion's control, authoritative for merge.

### 17.2 Gate Execution Invariants

1. Gate logic lives in service code, NEVER in agent prompt text. Agents have no path to rationalize past a gate, because the gate evaluates after the agent's output. (Skills-pipeline lesson.)
2. Gate failures MUST produce a structured error with the failing axis, the offending input, and the remediation hint (if any).
3. Gates SHOULD be deterministic on the same inputs; non-determinism (e.g., flaky tests) MUST be quarantined and surfaced separately.

### 17.3 Subset-Comparison Gate Detail

For Tier 4 (customer CI), Orion provides a reference implementation in the PR body that the customer can wire into their CI:

```bash
# Capture main's failing test set
git fetch origin main && git checkout origin/main
go test ./... -json | jq -r 'select(.Action=="fail") | .Test' | sort > /tmp/main_fails.txt

# Capture branch's failing test set
git checkout $PR_BRANCH
go test ./... -json | jq -r 'select(.Action=="fail") | .Test' | sort > /tmp/branch_fails.txt

# PR is mergeable iff branch fails ⊆ main fails
new_fails=$(comm -23 /tmp/branch_fails.txt /tmp/main_fails.txt)
test -z "$new_fails" || (echo "New failures introduced:"; echo "$new_fails"; exit 1)
```

This is illustrative; production implementations will use the customer's existing CI orchestration.

---

## 18. Failure Model and Recovery

### 18.1 Failure Classes

| Class | Examples | Recovery |
|---|---|---|
| **Configuration** | `.orion/config.yaml` invalid; required Polaris API key missing | Run rejected with `config_invalid`; no retry. |
| **Workspace** | Sandbox provisioning failed; namespace quota exceeded | Retry with backoff; on exhaustion, escalate `error`. |
| **Agent session** | LLM provider 500; token budget exhausted; stall | Retry per `max_retries_per_issue`; on exhaustion, escalate `warn`. |
| **Tracker** | GitHub API down; Linear webhook signature invalid | Retry with backoff; tracker-level circuit-breaker; surface to operator if sustained. |
| **Polaris callback** | Polaris API unreachable | Retry per §13.5; on exhaustion, run `failed`. |
| **Verification** | Harness inconclusive; no patch dominates | Close issue with `no_improvement`; positive signal, NOT a failure. |
| **Safety violation** | Agent attempted out-of-workspace write; agent attempted production-system call | Halt run immediately; `critical` escalation; full evidence preserved. |

### 18.2 Recovery on Restart

On Conductor restart:

1. Read all runs in non-terminal states.
2. For each: reconcile WorkerSessions against pod liveness and tracker state.
3. Workers alive and healthy: continue.
4. Workers dead: mark `failed` and either retry or escalate per the failure class.
5. Resume backlog dispatch where it left off.

This is testable: kill the Conductor mid-run, restart it, observe convergence.

### 18.3 Operator Intervention Points

The operator MAY:

- **Pause** a run: in-flight workers complete current phase then idle.
- **Resume** a paused run.
- **Cancel** a run: in-flight workers terminated, sandboxes torn down.
- **Acknowledge** an escalation: clears the escalation, optionally re-arms the issue for retry.
- **Quarantine** an issue: marks `human_review`; Orion will not re-claim until quarantine cleared.

---

## 19. Security and Operational Safety

### 19.1 Trust Boundary

| Trusted | Untrusted |
|---|---|
| Orion's control plane (Conductor, database, control-plane secrets). | Customer code. Customer issues. Customer-provided design documents. |
| Polaris API (same operator). | LLM provider responses (treated as untrusted text; never executed unfiltered). |
| Operator-controlled K8s cluster. | Anything in the per-run namespace beyond Orion's control plane. |

### 19.2 Secret Handling

Per §6.2, plus:

1. Per-run sandbox tokens (GitHub App, LLM) MUST be ephemeral, scoped to the run, and revocable.
2. Customer-provided secrets in `.orion/config.yaml` are forbidden; if detected, the config is rejected.
3. Logged events MUST redact known secret patterns (tokens, keys, passwords).

### 19.3 No Cross-Tenant Leakage

1. Database queries MUST go through `*db.RLSPool` for per-tenant paths (per Polaris RLS rule).
2. LLM context MUST NOT include cross-tenant data. Knowledge enrichment is scoped to the tenant's accessible knowledge.
3. Object-storage paths are tenant-prefixed and access-controlled.
4. Per-run K8s namespaces are NetworkPolicy-isolated.

### 19.4 No Production Reach

Orion's network policy MUST whitelist only:

- The customer's Git provider (e.g., `api.github.com`).
- The configured LLM provider.
- Polaris's API.
- Orion's own database and storage.

Any attempt to reach a customer-production hostname or IP MUST be denied at the NetworkPolicy layer, NOT at the application layer (defense-in-depth: app-layer denial is best-effort, network-layer denial is enforceable).

### 19.5 Audit Logging

Every state transition, every PR open, every Polaris callback, every escalation MUST be logged with:

- Tenant ID.
- Run ID.
- Operator (if human-triggered).
- Inputs and outputs (redacted).
- Timestamp.

Audit logs MUST be tamper-evident (signed) and retained per the customer's contracted period.

---

## 20. Forbidden Behaviors (Anti-Patterns)

These are codified directly from the skills-pipeline experiment postmortem (§22). Each is a hard constraint on the implementation.

1. **Orion MUST NOT rationalize past externally-enforced gates.** Gate logic lives in code, not prompts. Agents have no instruction-level path to bypass. (Lesson: HCA-1, the bypass incident.)

2. **Orion MUST NOT expand scope beyond a literal request without explicit re-confirmation.** Any operator action interpreted as scope extension (e.g., "cleanup" interpreted as "delete remote branches") MUST surface as a confirmation prompt. (Lesson: HCA-3, branch-deletion incident.)

3. **Orion MUST NOT assert diagnoses as fact without citing specific evidence** (commit hash, file:line, command output). Verification reports use Observed / Hypothesis / Verified-by structure. Unverified claims are tagged. (Lesson: HCA-4, mis-diagnosis cascade.)

4. **Orion MUST NOT use long-running daemon-style agent loops with prompts loaded at session start.** Orchestration is service code; agent invocations are scoped to one synthesis or verification turn. (Lesson: Pattern C, static-skill / dynamic-runtime asymmetry.)

5. **Orion MUST NOT perform destructive remote actions** (force-push, branch-delete on origin, repo-modify) **under any agent-driven path.** All destructive operations require operator-issued, audited API calls. (Lesson: HCA-3.)

6. **Orion MUST NOT auto-merge** to any protected branch under any circumstance. CI is the gate; the customer is the merge authority. (Lesson: HCA-1 root cause.)

7. **Orion MUST NOT build orchestration on top of unstable substrate.** All infrastructure dependencies (database, message bus, container runtime, secret store) MUST have explicit health checks, structured failure semantics, and fall-closed defaults. (Lesson: dolt subprocess proliferation, IPC socket staleness.)

8. **Orion MUST NOT default to autonomous parallelism.** Worker concurrency is bounded per run (`max_concurrent_workers`, default 4) and per tenant. Operator must explicitly raise the cap. (Lesson: 2026-05-08 experiment overhead exceeded gains.)

9. **Orion MUST NOT use absolute test-pass gates** when the customer's main branch may have pre-existing failures. Subset-comparison gates only. (Lesson: §17.3 over-correction incident.)

10. **Orion MUST NOT expose APIs that allow a worker (or any agent) to grant itself elevated privileges.** Tool whitelists are enforced server-side, not agent-side.

---

## 21. Observability

### 21.1 Logging Conventions

Structured, key=value, one event per line. Required fields:

- `run_id` on every event.
- `issue_id` on every issue-scoped event.
- `worker_id` on every worker-scoped event.
- `phase` on every phase-transition event.
- `outcome` on every terminal event (`completed`, `failed`, `escalated`, `superseded`).

Failures include `failure_class` (§18.1) and a concise human-readable reason.

### 21.2 Metrics

Counters per tenant, per run, per phase. Standard Prometheus exposition.

| Metric | Type | Use |
|---|---|---|
| `orion_runs_total` | counter, labeled by `outcome` | Run rate and outcome distribution. |
| `orion_issues_dispatched_total` | counter | Backlog throughput. |
| `orion_patches_accepted_total` / `orion_patches_rejected_total` | counter | Synthesis quality. |
| `orion_prs_opened_total` / `orion_prs_merged_total` | counter | End-to-end conversion. |
| `orion_worker_phase_duration_seconds` | histogram, labeled by `phase` | Phase latency profile. |
| `orion_escalations_total` | counter, labeled by `severity` | Operator load. |
| `orion_polaris_callback_failures_total` | counter | Integration health. |
| `orion_token_consumed_total` | counter, labeled by `provider` | Cost attribution. |

### 21.3 Run Report

Each terminal run produces a markdown report archived to object storage:

```
# Orion Run Report
Run ID: <uuid>
Repo: <full_name> @ <commit_sha>
Tenant: <org_id>
Started: <iso8601>  Finished: <iso8601>  Duration: <hms>
Outcome: <status>

## Summary
- Issues considered: <n>
- Issues dispatched: <n>
- PRs opened: <n>
- PRs merged: <n>
- Patches accepted: <n>
- Patches rejected: <n>

## Per-Issue Outcomes
[ table ]

## Escalations
[ list with severity, reason, operator acknowledgement ]

## Token and Compute Accounting
[ tokens by provider, harness CPU-hours, peak memory ]

## Operating Envelope
[ harness configuration summary, fault matrix, seeds ]
```

### 21.4 HTTP Status Surface

```
GET  /api/v1/runs                 - list runs for tenant
GET  /api/v1/runs/{id}            - run detail
GET  /api/v1/runs/{id}/report     - markdown report
GET  /api/v1/runs/{id}/issues     - per-issue outcomes
POST /api/v1/runs                 - start a run
DELETE /api/v1/runs/{id}          - cancel
POST /api/v1/runs/{id}/pause     - pause
POST /api/v1/runs/{id}/resume    - resume
GET  /api/v1/state                - global Conductor state snapshot
GET  /api/v1/escalations          - open escalations for tenant
POST /api/v1/escalations/{id}/ack - acknowledge an escalation
```

All responses are JSON. The `report` endpoint returns markdown.

---

## 22. Lessons Learned (Codified)

This section codifies the learnings from the 2026-05-08 skills-pipeline experiment and the orchestrated-workflow redesign. Each lesson maps to a specific structural choice in this spec.

| Lesson | Where this spec applies it |
|---|---|
| Externally-verifiable invariants beat agent self-discipline. | §17.2, §20 (gates in code, not prompts). |
| Subset-comparison beats absolute gates. | §16.2, §17.3. |
| Centralized infrastructure precedes orchestration. | §3.3, §14.3 (single durable substrate, leader-elected Conductor). |
| Adversarial review proportional to work. | §15 (scan-loop is opt-in cadence; auto-file caps). |
| Default to single-developer / sequential flow; parallelism opt-in. | §20 #8 (concurrency cap, opt-in raise). |
| Distinguish design-time from runtime. | §11 (workers are service code; agents are turn-scoped). |
| State-aware automation. | §16.4 (PR reconciliation polls live state); §10.2 (sandbox isolation). |
| Inference-without-state-verification is the shape of most autonomous failures. | §20 #2; §11.4 (reconciler re-checks tracker state between turns). |
| Confidence without grounding cascades errors. | §20 #3 (Observed / Hypothesis / Verified-by). |
| Static-prompt / dynamic-runtime asymmetry breaks long-running loops. | §20 #4; §5.3 (no mid-run config reload). |
| Autonomous merge fails. | §20 #6, §16. |
| Infrastructure friction swallows orchestration gains. | §20 #7. |
| Reliability-by-construction inverts the SRE model. | The entire spec; this is Orion's premise. |

---

## 23. Reference Algorithms

### 23.1 Conductor Tick

```
loop forever:
    runs = db.fetch_runs(state in {created, scanning, backlog_active, draining, paused})
    for run in runs:
        if run.state == created:
            if validate_config(run): transition(run, scanning) else transition(run, config_invalid)
        elif run.state == scanning:
            if scan_complete(run):
                if backlog_has_eligible(run): transition(run, backlog_active) else transition(run, completed)
        elif run.state == backlog_active:
            reconcile_workers(run)
            if any_eligible_and_under_cap(run):
                issue = pick_next_eligible(run)
                if claim_issue(run, issue): spawn_worker(run, issue)
            elif no_workers_and_no_eligible(run): transition(run, completed)
        elif run.state == draining:
            reconcile_workers(run)
            if no_workers(run): transition(run, completed)
        elif run.state == paused:
            # in-flight workers complete current phase then idle; no new dispatch
            pass
    sleep(conductor_tick_interval)
```

### 23.2 Worker Lifecycle

```
worker(run_id, issue_id):
    workspace = provision_sandbox(run_id, issue_id)
    try:
        model = synthesize_or_load_model(run_id)
        constraints = synthesize_or_load_constraints(run_id, model)
        harness = synthesize_or_load_harness(run_id, model, constraints)
        gaps = identify_gaps(model, constraints, issue)
        candidates = synthesize_patches(workspace, gaps)
        accepted = []
        for c in candidates:
            v = verify(workspace, harness, c, baseline=accepted)
            if v.verdict == accepted: accepted.append(c)
        if not accepted:
            report(run_id, issue_id, "no_improvement")
            return
        composition = compose_patches(workspace, accepted, harness)
        if not composition:
            report(run_id, issue_id, "no_improvement")
            return
        pr = open_pr(workspace, composition, verification_report)
        report_to_polaris(run_id, issue_id, pr.url, [risk_id_for(issue)])
        report(run_id, issue_id, "succeeded")
    except SafetyViolation as e:
        escalate(run_id, issue_id, "critical", evidence=e)
    except RecoverableError as e:
        record_failure(run_id, issue_id, e)
        # Conductor decides retry vs escalate per max_retries_per_issue
    finally:
        teardown_sandbox(workspace)
```

### 23.3 Verification

```
verify(workspace, harness, candidate, baseline_set):
    apply_patch(workspace, candidate)
    if not build(workspace): return Verdict(rejected, "build_failed")
    metrics_patched = run_harness(workspace, harness)
    metrics_baseline = run_harness(workspace_without(candidate), harness)
    for axis in ALL_AXES:
        if metrics_patched[axis] is_worse_than metrics_baseline[axis]:
            return Verdict(rejected_regression, axis)
    if not strictly_dominates(metrics_patched, metrics_baseline):
        return Verdict(rejected_no_dominance)
    return Verdict(accepted, metrics=metrics_patched)
```

---

## 24. Test and Validation Matrix

### 24.1 Conformance Profiles

| Profile | Scope | Tests |
|---|---|---|
| **Core Conformance** | All implementations | Deterministic unit tests for: state machine transitions, claim atomicity, sandbox isolation invariants, dominance check, optimizer composition, tracker normalization. |
| **Tracker Adapter Conformance** | Per adapter | Each implementation MUST pass the standard adapter contract test suite (read, create, update, comment, capability declaration). |
| **Real Integration Profile** | Operator pre-prod | End-to-end against a fixture repo + fixture Polaris instance + fixture LLM provider. Includes: scan loop, backlog drive, full synthesis, PR open, Polaris callback, run report archive. |

### 24.2 v1 Acceptance Test (the FIRST issue and the LAST issue)

Identical to PRD §Testing Decisions:

1. Provision a fixture Go service repo `github.com/revelara-test/orion-fixture-svc` containing three known reliability gaps (one missing context timeout, one retry without backoff, one POST without idempotency-key).
2. Trigger an Orion run.
3. **Verify**: a PR is opened by the Orion GitHub App.
4. **Verify**: the PR contains exactly three commits, each addressing one gap.
5. **Verify**: each commit's diff modifies the expected file.
6. **Verify**: the PR body contains a verification report (harness config summary, baseline metrics, patched metrics, deltas, operating envelope).
7. **Verify**: in Polaris with `orion_enabled` on, the Remediations view shows three risks with Orion badges linking to the PR.
8. **Verify**: the Orion run is recorded in `orion.runs` with `status=completed` and metrics deltas matching the PR report.
9. **Verify**: Polaris `POST /api/v1/orion/run-complete` was called and acknowledged.

This test MUST pass before v1 release.

### 24.3 Negative Tests

Required, per Forbidden Behaviors (§20):

- Attempted out-of-workspace write by agent: worker terminated, `critical` escalation, run halted.
- Attempted destructive remote action by agent: tool whitelist rejects, no escalation needed.
- LLM returns malformed patch: rejected at parse, no PR opened.
- Polaris callback exhausted: run `failed`, operator notified.
- Tracker webhook signature invalid: event dropped, security log emitted.

---

## 25. Implementation Checklist

### 25.1 Required for v1 Conformance

- [ ] `internal/conductor` with leader election.
- [ ] `internal/database/migrations/` with all entity schemas, RLS policies, and indices.
- [ ] `internal/trackers/{github,linear,beads}` adapters passing the contract test suite.
- [ ] `internal/sandbox` with K8s namespace provisioning, NetworkPolicy enforcement, teardown.
- [ ] `internal/worker` binary plus pod spec.
- [ ] `internal/agent` runner with strict tool whitelist.
- [ ] `internal/architect`, `internal/constraints`, `internal/harness`, `internal/patches`, `internal/verify` per §12.
- [ ] `internal/polaris` client with retry, circuit-breaker, scope-checked endpoints.
- [ ] `internal/github` GitHub App handler with branch creation, PR open, signed commits.
- [ ] `internal/report` markdown generator.
- [ ] `internal/api` HTTP surface per §21.4.
- [ ] `cmd/orion` service entrypoint.
- [ ] `cmd/orion-cli` for dogfooding (`orion-cli run --repo=...`).
- [ ] All forbidden-behavior tests (§24.3) passing.
- [ ] v1 acceptance test (§24.2) passing.

### 25.2 Polaris-Side Required

- [ ] `internal/orion_link/handler.go` with: `POST /api/v1/risks/{id}/claim-by-orion`, `POST /api/v1/orion/run-complete`, `GET /api/v1/orion/runs`, `GET /api/v1/orion/runs/{id}`.
- [ ] Migration adding `remediations` table (or extending `risks`) with `claimed_by_orion`, `pr_url`, `run_id`.
- [ ] `orion_enabled` and `orion_autopr` feature flags wired into handlers and frontend nav.
- [ ] Frontend Remediations view: Orion badge column, Send-to-Orion action, Orion runs sub-page.

### 25.3 Operational Validation Before Production

- [ ] Restart-recovery test (kill Conductor mid-run, observe convergence).
- [ ] Cross-tenant isolation test (run two tenants concurrently, verify no DB, network, or filesystem leakage).
- [ ] Network policy enforcement test (attempt reach to a non-whitelisted host from a worker pod, verify denial).
- [ ] Token-budget exhaustion test (force a worker to exceed budget, verify clean termination and escalation).
- [ ] Subset-comparison gate test (PR against a fixture main with pre-existing failures, verify gate logic correctly distinguishes new vs pre-existing).
- [ ] Audit log integrity test (verify signed-log chain unbroken across a multi-day run).

---

## Appendix A: v1 Pattern Set

The three patterns Orion v1 ships with:

### A.1 Timeout Coverage

Detected: outbound HTTP/gRPC/database call without an explicit per-call deadline.
Patch: introduce a context with deadline derived from the constraint surface (default conservative budget if not specified).
Verification: under simulated downstream latency, total request time bounded; cascade probability reduced.

### A.2 Retry Hygiene

Detected: retry loop without exponential backoff, jitter, or bounded attempts.
Patch: introduce backoff + jitter with bounded attempts; if downstream supports idempotency, also gate retries on idempotency key.
Verification: under simulated downstream errors, retry storm probability reduced; total request count bounded.

### A.3 Idempotency-Key Insertion

Detected: POST/PUT endpoint that mutates state and accepts no idempotency key.
Patch: introduce an idempotency-key parameter, persistence (with TTL), and dedup logic.
Verification: under simulated client retry, exactly-once mutation observed.

---

## Appendix B: Tracker Adapter Examples

### B.1 GitHub Issues

- Read: REST `GET /repos/{owner}/{repo}/issues?state=open&labels=...`.
- Create: REST `POST /repos/{owner}/{repo}/issues`.
- State update: REST `PATCH /repos/{owner}/{repo}/issues/{n}`.
- Webhook: `issues`, `issue_comment` events; HMAC-SHA256 verified.

### B.2 Linear

- Read: GraphQL `issues(filter: {project: {slugId: {eq: ...}}, state: {type: {in: [...]}}})`.
- Create: GraphQL `issueCreate(...)`.
- State update: GraphQL `issueUpdate(...)`.
- Webhook: Linear webhook with signature header.

### B.3 Beads

- Read: subprocess `bd ready --format=json` (or future API once beads exposes one).
- Create: subprocess `bd create --type=task --title=... --description=...`.
- State update: subprocess `bd update <id> --status=...`.
- No webhook in v1; poll-only on `polling.interval_ms`.

For v1, beads is supported only against locally-mounted beads workspaces (since `bd` is a CLI). Hosted-beads support is deferred.

---

## Appendix C: Mapping Polaris Controls to v1 Patterns

| Polaris control category | v1 pattern set member |
|---|---|
| `fault_tolerance` (timeout subcategory) | timeout_coverage |
| `fault_tolerance` (retry subcategory) | retry_hygiene |
| `change_management` (idempotency subcategory) | idempotency_keys |

The full catalog mapping is in `internal/constraints/control_pattern_map.yaml`.

---

## Appendix D: Open Questions

1. **Greenfield mode** (§9.2): full design deferred to v2. The L3 architecture is intentionally extensible to support it; the v1 implementation may stub greenfield-only entry points.
2. **Cross-language support**: v2+. The synthesizer is parameterized over language; v1 ships only Go.
3. **Auto-file dedup against legacy issues** (§8.7): v1 dedups by Polaris risk ID and content hash; v2 may add semantic similarity matching.
4. **Multi-PR per issue** for very large patches: v1 emits one PR per issue. v2+ may chunk.
5. **Customer-pluggable verification**: v1 uses Orion's harness only. v2+ may permit customer-supplied harness adapters.
6. **Pricing-tier enforcement** at the API layer: v1 trusts Polaris; v2+ may add Orion-side enforcement for redundancy.
7. **Continuous mode** (always-on instead of scan-cadence): v2+ if customers ask; v1 is cadence-driven.
