---
title: Orion Specification
status: Draft v2
authors: Joseph Bironas
created: 2026-05-09
last_updated: 2026-05-09
related:
  - docs/PRD/orion-v1.md
  - docs/research/SPEC.md (Symphony service spec, used as template)
  - docs/research/orchestrated-development-workflow.md
  - docs/research/2026-05-08-skills-pipeline-experiment.md
  - docs/research/reliability-conductor.md
  - docs/SPEC/Orion-SPEC.draft1.md (round 1 review consumed this)
---

# Orion Specification

> **Purpose**: Define a long-running service that takes a software project (existing Go codebase in v1, design documents in v2+), plus a backlog of issues from one or more trackers, and progressively converts the **reliability-eligible** slice of that backlog into verified, human-mergeable pull requests. Orion is the autonomous closed-loop layer of the Revelara platform; Polaris is the human-augmenting layer.

> **Conformance language**: The keywords MUST, MUST NOT, REQUIRED, SHALL, SHALL NOT, SHOULD, SHOULD NOT, RECOMMENDED, MAY, and OPTIONAL are interpreted as in RFC 2119.

> **Scope of v1**: Go codebases. Three reliability patterns (timeout coverage, retry hygiene, idempotency-key insertion). Brownfield only. Cadence-triggered execution. SaaS deployment. No production access. No auto-merge to protected branches. v2+ extends languages, patterns, greenfield design synthesis, and continuous mode as defined in §9 and Appendix D.

> **Honest framing**: Orion is **machine-synthesizing, human-merging**. The closed loop has three explicit human checkpoints (cadence trigger, escalation acknowledgement, PR merge). The "autonomous closed-loop" framing applies to the *synthesis-and-verification interior*, not the end-to-end mission. v2 may auto-merge low-risk classes against explicit opt-in; v1 does not.

---

## 1. Mission and Honest Scope

### 1.1 The Reliability Debt Problem

Engineering organizations operating non-trivial systems accumulate reliability and performance debt faster than they pay it down. Reliability work loses to feature work in every sprint that does not immediately follow an incident. Existing tooling does not solve this:

- **Linters and static analysis** flag patterns but produce too many false positives and offer no verification that proposed fixes improve the system.
- **Chaos engineering tools** require running systems and human-driven experiments. They surface weaknesses but do not fix them.
- **AI code assistants** propose changes but cannot reason about behavior under load, fault, or partial-failure conditions.
- **APM tools** report what already broke, after it broke.
- **Polaris**, today, is a human-in-the-loop discovery and guidance product. Developers initiate scans, review risks, and apply fixes one at a time.

The org has a *systemic* reliability and performance debt problem, and it cannot get out of it without a substantial productivity multiplier on reliability work.

### 1.2 What Orion Actually Delivers

**Orion progressively converts the reliability-eligible slice of a customer's backlog into verified, human-mergeable pull requests.**

Concretely, in v1, Orion:

1. **Discovers reliability gaps** in the connected Go codebase that fall in the v1 pattern allowlist (timeout coverage, retry hygiene, idempotency keys).
2. **Files reliability-risk issues** into the customer's tracker(s) of choice, with explicit caps and semantic dedup against pre-existing issues.
3. **Pulls eligible issues** (Orion-filed and human-filed alike) from a unified backlog, prioritizes them, and dispatches each one to an isolated worker.
4. **Synthesizes a constraint surface** from the architectural model and Polaris's controls catalog, materializes a verification harness, generates candidate patches, and verifies each patch against the harness with statistical confidence.
5. **Opens a pull request** containing only patches that statistically dominate the baseline within explicit confidence intervals, with a reproducible verification report attached including an *operating envelope confidence* score.
6. **Reports completion** to Polaris, marking the source risk as remediated and linking the PR.
7. **Loops** at the configured cadence until the eligible backlog is empty or a stop signal is received.
8. **Learns from rejection**: PRs the customer closes unmerged adjust per-pattern, per-repo trust scores; patterns whose rejection rate exceeds a threshold are auto-suppressed for that repo until the operator re-enables them.

Orion never writes to protected branches. Orion never touches production. Orion never trains on customer code.

### 1.3 What Orion Does Not Promise

Honest disclaimers, surfaced here and again wherever they apply:

- **Eligible slice is small.** v1 covers three patterns. On a typical 30-service Go org, the eligible slice is plausibly 5-15% of the reliability backlog. Orion is not a complete reliability automation; it is a *high-confidence wedge* that grows as patterns are added in v1.x and v2.
- **Verification is bounded by the synthesized harness.** Orion has no production access (§2.2). The verification claim is "within the synthesized operating envelope, the patched system shows statistically significant improvement on every measured axis with no statistically significant regression." Real production envelopes may differ; every report carries an *envelope-confidence* score and an explicit invitation for the customer to supplement.
- **Three human checkpoints remain.** Cadence trigger, escalation acknowledgement, PR merge. Orion does not auto-merge. v2 may auto-merge low-risk classes against explicit opt-in.
- **Yield is measurable, not promised.** §1.5 defines a yield model the customer can use to set realistic expectations.

### 1.4 Within the Revelara Platform

| Product | Role | Buyer | Pricing tier |
|---|---|---|---|
| **Polaris** | Human-augmenting reliability product. Discovers risks, surfaces controls, guides engineers. | SRE Manager | All tiers |
| **Orion** | Machine-synthesizing reliability product. Synthesizes verified patches; humans merge. | VP Eng / CTO | Architect intelligence multiplier on Growth ($1,999) and Enterprise ($5K+) |

Orion is to software reliability what an EDA synthesis-and-verification toolchain is to hardware: a closed loop that takes a high-level design and emits a verified, production-ready *candidate* implementation. The Conductor 2.0 paradigm applies this idea to hardware engineering agents; Orion applies it to distributed software systems with the explicit acknowledgement that software production is wider and more variable than hardware fabrication, so the loop ends at "verified candidate," not "shipped artifact."

### 1.5 Yield Model

A spec without yield expectations is a vendor promise without numbers. The yield model below is what Orion's onboarding team uses to set customer expectations and what the verification engine reports against.

For a connected Go service repo with `S` services, `G_total` reliability gaps, and `G_eligible` gaps in the v1 pattern allowlist:

```
expected_PRs_per_run ≈ G_eligible × P_dominance × P_compose × (1 - P_dedup)

where:
  P_dominance ≈ probability a candidate patch passes the statistical verifier
                (target: 0.6-0.8 for v1 patterns)
  P_compose   ≈ probability a candidate composes with already-accepted patches
                (target: 0.7-0.9)
  P_dedup     ≈ probability the gap is already covered by an open issue
                (varies by customer maturity, target tracking ≤ 0.3 in scan-loop output)

historical_merge_rate ≈ measured per-tenant, per-pattern, per-repo
                        (target: ≥ 0.6 within first 90 days)
```

Onboarding MUST:

1. Run a one-time inventory pass on the connected repo and report `G_total`, `G_eligible`, and the projected first-month PR count to the customer **before any tracker writes**.
2. Surface the projection in the Polaris Orion runs view as a pinned metric ("Expected: N PRs/month; Observed: M to date").
3. Treat sustained shortfall (observed < 50% of projection over 60 days) as an account health signal that triggers Revelara-side review.

The yield model is part of the spec because it is part of the contract.

---

## 2. Goals and Non-Goals

### 2.1 Goals

Orion MUST:

1. Operate as a long-running service that polls one or more trackers, reconciles state, and dispatches work without per-issue human invocation.
2. Run worker sessions in isolated workspaces that are network-restricted, ephemeral, and per-tenant.
3. Maintain durable orchestration state so that restart, crash, leader handover, or operator stop-and-start does not lose claimed work, in-flight verification, or pending PR delivery.
4. Generate reliability-risk issues into the customer's tracker(s) when scanning surfaces a control gap and no equivalent open issue exists, subject to per-tenant caps and semantic dedup.
5. Drive an issue from `claimed` through `verified` through `pull-request-open` through `closed` without prompting a human for any step that does not require human judgment.
6. Stop and surface a clear escalation when human judgment IS required (ambiguous requirement, irrecoverable conflict, sensitivity boundary, verification failure with no remediation, scope-expansion attempt). Each escalation has a routing class (§14.5) so customer-side escalations do not page Revelara and vice versa.
7. Produce a reproducible verification report for every PR, including harness configuration, baseline metrics, patched metrics, deltas, statistical confidence intervals, operating envelope, and *operating-envelope confidence score*.
8. Enforce all merge-eligibility gates outside the agent's reasoning loop (in CI, infrastructure, signed-off automation), so the agent has no path to rationalize past them.
9. Tolerate pre-existing rot in the customer's main branch via subset-comparison gates ("does this make things worse than main?") rather than absolute gates ("do all tests pass?"), with a reference CI integration that Revelara onboarding ships for the top three CI providers.
10. Isolate every customer's codebase, harness, secrets, and metrics from every other customer's, including in-process memory and persistent state.
11. Provide a **staged trust ladder** (§6.4) so customers can run Orion in shadow, draft, and full modes without all-or-nothing trust grants.
12. Learn from rejection: PR closures, customer comments, and incident reports filed within `post_merge_window` of an Orion-merged PR feed back into per-pattern, per-repo trust scores, harness augmentation, and pattern auto-suppression.
13. Provide **annotation-level suppression** (`// orion:ignore <pattern> reason=...`) so intentional design choices are honored without re-detection on every cadence.

### 2.2 Non-Goals

Orion MUST NOT:

1. **Auto-merge** to protected branches in v1. Branch-protection rules, code-owner review, and CI-required-checks are honored as the customer configured them. (v2 may auto-merge low-risk classes against explicit opt-in.)
2. **Connect to production** systems, consume customer telemetry, scrape Grafana, or call into runtime infrastructure. Codebase plus IaC plus design documents only. Customers MAY upload anonymized request distributions or fault profiles as harness inputs (§12.3).
3. **Train models on customer code.** Per-tenant LLM calls, no retention beyond the run window, no cross-customer signal extraction.
4. **Drive multi-repo or monorepo with multiple services in a single run** in v1. One repo, one service per run.
5. **Be a general-purpose coding agent.** Orion's prompts, tooling, harness, and verification are scoped to reliability and performance synthesis. Feature work is out of scope.
6. **Perform destructive remote operations** (force-push, branch-delete on origin, issue-close-without-merge, repository deletion, workflow-disable) under any agent-driven path.
7. **Run skills-style design-time prompts** as long-running orchestration. The orchestrator is implemented in service code, not in agent prompt text. (Lesson from skills-pipeline experiment, §22.)
8. **Operate without an explicit, verifiable per-tenant scope** on every tracker write, every Polaris API call, every PR open, and every harness namespace.
9. **File new auto-issues** for gaps the customer has explicitly suppressed via `// orion:ignore` annotations. Suppression is sticky across runs.
10. **Open a PR for a pattern whose per-repo rejection rate exceeds the per-pattern threshold** without first surfacing an escalation requesting operator re-enablement. (§16.5 rejection learning.)

### 2.3 Things Orion Is Deliberately Silent About

Orion does not prescribe:

- The customer's branch-protection model. Whatever exists, Orion respects.
- The customer's review process. Orion's PRs enter review like any other PR.
- The customer's CI provider. Orion runs its harness in its own infrastructure, then opens a PR which the customer's CI evaluates as configured. Onboarding ships reference subset-comparison integrations for GitHub Actions, CircleCI, and Buildkite (§17.3); other CIs are operator-or-customer responsibility.
- The customer's tracker of record. Orion adapts to GitHub Issues, Linear, or beads (internal-only in v1, see §8.2); the customer chooses.

---

## 3. System Overview

### 3.1 Conceptual Architecture

Orion is built as five interlocking loops:

1. **Inventory Loop.** On repo connection and on operator demand, Orion produces a baseline inventory: total reliability gaps detected, eligible gaps, projected first-month PR count, current trust-score per pattern. **No tracker writes.** Output is the contract anchor for §1.5.
2. **Scan Loop.** Reads codebase periodically per cadence, infers an architectural model, derives a constraint surface, identifies new control gaps, and files reliability-risk issues into the customer's tracker(s) when no equivalent open issue exists and scan-loop is enabled.
3. **Backlog Loop.** Polls connected trackers, normalizes issues into a unified backlog, applies eligibility, deduplication, and priority rules, and emits a stream of dispatch-ready issues.
4. **Synthesis Loop.** For each dispatched issue: instantiates a sandbox, materializes a verification harness, generates candidate patches, verifies each patch against the harness with statistical confidence, composes accepted patches into a sequence, and opens a PR.
5. **Reconciliation Loop.** Tracks open PRs, watches for merge or close, updates Polaris with run completion, monitors post-merge incidents in Polaris within `post_merge_window`, and feeds outcomes back into trust scores, harness augmentation, and the next scan-loop's prioritization.

These five loops share state through a single durable substrate (§7), are coordinated by a single authority (the Conductor, §14), and emit observations on a single event stream (§21).

### 3.2 Layered Architecture

Orion is organized in six horizontal layers. Each layer has a stable port to the layer below it. New trackers, new languages, new patch synthesizers, and new verifiers are added by writing adapters, not by modifying core orchestration.

| Layer | Responsibility | Examples of components |
|---|---|---|
| **L1 Policy** | Repo-defined and tenant-defined configuration, trust ladder state. What to scan, what trackers to use, what controls to enforce, what trust mode applies. | `.orion/config.yaml`, `// orion:ignore` annotations, Polaris feature flags, GitHub App scopes, trust-ladder state per-tenant per-repo |
| **L2 Coordination** | The Conductor, the backlog, the run state machine, dispatch, reconciliation, escalation routing. | `internal/conductor`, `internal/backlog`, `internal/runs`, `internal/escalation` |
| **L3 Synthesis** | Inventory, architectural inference, constraint inference, harness synthesis, patch synthesis, verification, optimization, statistical analysis. | `internal/inventory`, `internal/architect`, `internal/constraints`, `internal/harness`, `internal/patches`, `internal/verify`, `internal/stats` |
| **L4 Worker Execution** | Per-issue sandbox, worker session lifecycle, agent runner protocol, per-run reconciler co-located with workers (the *Lookout*, §14.4). | `internal/sandbox`, `internal/worker`, `internal/agent`, `internal/lookout` |
| **L5 Integration** | Tracker adapters, Polaris client, GitHub App, LLM providers, storage, post-merge incident watcher. | `internal/trackers/{github,linear,beads}`, `internal/polaris`, `internal/github`, `internal/llm`, `internal/storage`, `internal/postmerge` |
| **L6 Observability** | Logging, run reports, metrics, status surface, customer escalation UI surface, reproduction artifact bundling. | `internal/log`, `internal/report`, `internal/metrics`, `internal/api`, `internal/repro` |

The Conductor (L2) is the only component permitted to mutate orchestration state. All worker outcomes flow back to the Conductor via the Lookout and are converted into explicit state transitions (§7).

### 3.3 External Dependencies

| Dependency | Purpose | Trust |
|---|---|---|
| **Polaris API** | Risk register source, controls catalog, knowledge enrichment, evidence sink, run-completion callback, post-merge incident source. | Trusted (same operator) |
| **Customer's tracker(s)** | Issue source and sink. GitHub Issues, Linear, beads (internal-only) in v1. | Customer-controlled |
| **GitHub App** (per repo) | Clone, branch-create, PR-open, comment-post. Scoped per install. | Customer-controlled |
| **LLM provider** (Vertex AI in v1) | Patch synthesis. Per-tenant configuration. Specific model + provider seed pinned per run for reproducibility. | Vendor-trusted |
| **Container runtime** (Kubernetes in v1) | Per-run namespace for sandbox and harness. | Operator-controlled |
| **PostgreSQL** (Orion's own DB) | Durable orchestration state, run records, accepted patches, metrics, leader-election lease. RLS-enforced per `org_id`. | Operator-controlled |
| **Object storage** (GCS or S3) | Verification report archives, harness artifact archives, reproduction bundles. | Operator-controlled |

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
| `trust_mode` | enum {`shadow`, `draft`, `staging`, `full`} | §6.4 trust ladder. Default `shadow`. |
| `created_at`, `updated_at` | timestamp | |

#### 4.1.2 TrackerBinding

A customer-configured tracker connected to a repo. A repo MAY have multiple bindings. Issues from all bindings flow into a single unified backlog (§8).

| Field | Type | Notes |
|---|---|---|
| `id` | UUID | |
| `org_id` | UUID | RLS. |
| `repo_id` | UUID | Foreign key to ConnectedRepo. |
| `kind` | enum {`github_issues`, `linear`, `beads`} | beads is internal-only in v1; see §8.2. |
| `config` | JSONB | Adapter-specific (e.g., Linear project slug, GitHub label filter). |
| `credentials_ref` | string | Reference to encrypted secret in vault. |
| `enabled` | bool | |
| `auto_file` | bool | If true, Orion may file new issues here. Subject to trust mode. |

#### 4.1.3 Run

One unit of work. A Run executes the configured loops for one connected repo at one moment.

| Field | Type | Notes |
|---|---|---|
| `id` | UUID | |
| `org_id`, `repo_id` | UUID | |
| `mode` | enum {`inventory_only`, `scan_only`, `synthesis_only`, `full_loop`} | `inventory_only` produces yield projections, no tracker writes. |
| `trigger` | enum {`manual`, `scheduled`, `webhook`, `onboarding`} | |
| `status` | enum (see §7.1) | |
| `commit_sha` | string | The commit at which the run is anchored. Pinned for the duration of the run. |
| `controls_snapshot_id` | UUID | Snapshot of the Polaris controls catalog as of run start. Workers MUST read this snapshot, NOT the live API. (§14.6) |
| `started_at`, `finished_at` | timestamp | |
| `stop_reason` | string or null | Set when status is terminal. |
| `inventory_summary` | JSONB | `G_total`, `G_eligible`, projected_PRs from §1.5. |

#### 4.1.4 ArchitecturalModel

Per-run inference of the system under analysis. Persisted as JSONB.

| Field | Type | Notes |
|---|---|---|
| `id` | UUID | |
| `run_id` | UUID | One per run. |
| `services` | JSONB | List of services, endpoints, downstream dependencies. |
| `hot_paths` | JSONB | Inferred high-frequency request paths. |
| `persistent_stores` | JSONB | Databases, queues, object stores. |
| `envelope_confidence` | float | 0.0-1.0. Reflects how much of the model is grounded vs. inferred-with-low-evidence. Surfaced in reports. |
| `inferred_at` | timestamp | |

#### 4.1.5 ConstraintSurface (SLO Fabric)

The set of constraints the patched system MUST satisfy. Combination of explicit Polaris controls (snapshotted) and code-derived implicit constraints.

| Field | Type | Notes |
|---|---|---|
| `id` | UUID | |
| `run_id` | UUID | |
| `controls` | JSONB | Polaris controls in scope at snapshot time. |
| `implicit_constraints` | JSONB | Inferred from code. |
| `customer_supplied_envelope` | JSONB or null | Optional anonymized request distributions or fault profiles uploaded by the customer (§12.3). |
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
| `seed` | int64 | Deterministic seed for workload and fault synthesis. |

#### 4.1.7 NormalizedIssue

An issue from any tracker, normalized into a canonical shape for the backlog.

| Field | Type | Notes |
|---|---|---|
| `id` | UUID | Orion's internal identifier. |
| `org_id`, `repo_id`, `tracker_binding_id` | UUID | Provenance. |
| `external_id` | string | `<provider>:<scope>#<id>` format (§4.2). |
| `external_url` | string | Direct link in the source tracker. |
| `title` | string | |
| `description` | string | |
| `priority` | int or null | Tracker-native priority normalized to a 0-4 scale. |
| `state` | enum {`open`, `in_progress`, `blocked`, `closed`, `cancelled`} | Normalized from tracker-native states. |
| `labels` | string[] | Normalized labels (lowercased, deduplicated). |
| `polaris_risk_id` | UUID or null | Set if this issue corresponds to a Polaris-tracked risk. |
| `orion_filed` | bool | True if Orion created this issue (scan-loop output). |
| `claim_status` | enum (see §7.2) | |
| `eligibility` | enum {`eligible`, `ineligible_pattern`, `ineligible_path`, `ineligible_label`, `ineligible_branch`, `ineligible_blocked`, `ineligible_suppressed`, `ineligible_trust_mode`} | Computed at backlog ingestion. |
| `dedup_signature` | string | Semantic dedup signature (§8.3). |
| `last_synced_at` | timestamp | |

#### 4.1.8 CandidatePatch and AcceptedPatch

| Field | Type | Notes |
|---|---|---|
| `id` | UUID | |
| `run_id`, `issue_id` | UUID | |
| `target_path`, `target_range` | string, JSONB | File and line range. |
| `diff` | text | Unified diff. |
| `control_id` | UUID | The Polaris control this patch addresses. |
| `verdict` | enum {`pending`, `accepted`, `rejected_no_dominance`, `rejected_regression`, `rejected_unsafe_combination`, `rejected_low_confidence`, `error`} | Includes statistical-confidence rejection class. |
| `metrics` | JSONB | Baseline and patched metrics with confidence intervals on each axis. |
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
| `last_event_at` | timestamp | Used for stall detection by the Lookout. |
| `tokens_in`, `tokens_out` | int | Token accounting. |
| `lookout_id` | string | Per-run reconciler instance observing this worker. |

#### 4.1.10 PullRequest

| Field | Type | Notes |
|---|---|---|
| `id` | UUID | |
| `run_id`, `issue_id` | UUID | |
| `provider_pr_url` | string | e.g., `https://github.com/customer/repo/pull/123`. |
| `branch_name` | string | Orion-created. |
| `state` | enum {`open`, `merged`, `closed_unmerged`, `superseded`} | Reconciled from provider. |
| `report_url` | string | Object-storage URL for the verification report archive. |
| `reproduction_bundle_url` | string | Object-storage URL for the harness reproduction bundle (§12.7). |
| `opened_at`, `closed_at`, `merged_at` | timestamp | |
| `post_merge_window_ends_at` | timestamp | Until this time, related incidents in Polaris trigger re-evaluation (§16.6). |

#### 4.1.11 PatternTrustScore

Per-tenant, per-repo, per-pattern trust state, updated by the rejection-learning loop (§16.5).

| Field | Type | Notes |
|---|---|---|
| `id` | UUID | |
| `org_id`, `repo_id` | UUID | |
| `pattern` | string | e.g., `timeout_coverage`. |
| `total_proposed`, `total_accepted_by_customer`, `total_rejected_by_customer` | int | |
| `current_trust` | float | 0.0-1.0; smoothed exponential moving average. |
| `auto_suppressed` | bool | If true, Orion will not synthesize new patches for this (repo, pattern) until operator re-enables. |
| `last_updated_at` | timestamp | |

#### 4.1.12 ScopeRequest

A record of every operation an agent attempts that requires confirmation it stays within scope. Used as evidence in escalation reviews and as a structural enforcement point for §20 #2.

| Field | Type | Notes |
|---|---|---|
| `id` | UUID | |
| `worker_id`, `run_id` | UUID | |
| `requested_action` | string | Tool name and arguments. |
| `inferred_scope` | JSONB | Files, paths, controls implicated. |
| `decision` | enum {`allowed_in_scope`, `denied_out_of_scope`, `escalated`} | |
| `decided_at` | timestamp | |

The agent never directly performs scope-expanding actions; the worker's tool dispatch (§11.3) computes the inferred scope and rejects out-of-scope tool calls before they execute. This is the structural enforcement of §20 #2.

### 4.2 Stable Identifiers and Normalization Rules

- **`external_id` format**: `<provider>:<scope>#<id>` where provider is `gh`, `lin`, or `bd`; scope is `owner/repo`, project key, or beads prefix.
- **Branch naming**: `orion/<run_id_short>-<issue_external_id_sanitized>`. Example: `orion/r3dq8a-gh-customer-svc-123`.
- **Sandbox namespace naming**: `orion-run-<run_id>`. Sanitized to `[a-z0-9-]` only, max 63 chars.
- **Workspace key (per worker)**: `<run_id>-<issue_internal_id>`. Used as a directory name and a sandbox sub-namespace label.
- **Dedup signature**: `sha256(pattern || normalized_call_site)` where `normalized_call_site` is the canonical AST path of the affected call (file path agnostic, robust to refactor; see §8.3).

All sanitization MUST reject characters outside the documented set rather than silently rewriting them.

---

## 5. Project Contract (`.orion/config.yaml`)

Each connected repository MAY contain a `.orion/config.yaml` file at the repo root. If absent, Orion uses tenant-level defaults from the Polaris organization settings.

### 5.1 File Format

```yaml
version: 1

repo:
  service_path: cmd/svc
  language: go

trackers:
  - kind: github_issues
    label_filter: ["reliability", "orion-eligible"]
    auto_file: true
  - kind: linear
    project_slug: ABC
    state_active: ["Todo", "In Progress"]
    state_terminal: ["Done", "Cancelled"]
    auto_file: false

scan:
  cadence: weekly              # on_demand | daily | weekly | on_push
  excludes:
    - vendor/
    - **/*_generated.go
    - testdata/

synthesis:
  patterns:                    # subset of the v1 allowlist
    - timeout_coverage
    - retry_hygiene
    - idempotency_keys
  ineligible_paths:
    - internal/auth/
    - internal/billing/
    - internal/payments/

gates:
  pre_pr:
    - command: go build ./...
    - command: go vet ./...
    - command: golangci-lint run
  pr_body_template: .orion/pr_template.md
  require_signed_commits: true
  require_subset_comparison: true

orchestration:
  max_concurrent_workers: 4
  worker_timeout: 1h
  stall_timeout: 15m
  max_retries_per_issue: 2
  ineligible_branches:
    - main
    - master
    - release/*

verification:
  min_trial_count: 5            # statistical floor for dominance check
  confidence_level: 0.95        # default; see §12.5
  envelope_confidence_floor: 0.4 # block PR if envelope confidence below

escalation:
  human_review_label: orion-needs-review
  ineligible_labels:
    - do-not-touch
    - human-only

post_merge:
  window: 30d                  # incident-watch window per merged PR

trust_ladder:
  initial_mode: shadow         # see §6.4
  promote_after:
    shadow_to_draft: 7d_clean
    draft_to_staging: 5_PRs_merged_or_30d
    staging_to_full: 20_PRs_merged_with_zero_post_merge_incidents
```

### 5.2 Validation

Orion MUST validate `.orion/config.yaml` at repo connection time and again at the start of every run. Validation errors MUST:

1. Block the run from starting (status set to `config_invalid` with explicit error).
2. Surface the error in the Orion runs list in Polaris.
3. NOT proceed with stale or partial config.

Unknown keys MUST be rejected, not silently ignored.

### 5.3 Dynamic Reload (Restricted)

Orion does NOT hot-reload `.orion/config.yaml` mid-run. The config that was valid at the start of a run is the config used for the entire run. Mid-run changes via `.orion/config.yaml` take effect on the next run.

**Exception**: the operator MAY issue a mid-run pattern-disable command via API (§21.4 `POST /api/v1/runs/{id}/disable-pattern`). This:

- Halts dispatch of any unstarted issues whose primary pattern is the disabled one.
- Allows in-flight workers on disabled-pattern issues to finish their current verification (so harness state is not orphaned), then prevents PR open.
- Logs the operator, timestamp, and reason.

This exception exists because the §22 lesson ("no mid-run reload") is correctly applied to *config-as-data*, but operators must retain emergency-stop authority over *runtime classes of behavior*.

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

Secrets MUST come from environment variables, mounted secret files, or a vault. Secrets MUST NOT appear in `.orion/config.yaml`. Logged config dumps MUST redact known secret keys.

### 6.3 Per-Tenant Isolation

All persisted state with an `org_id` MUST follow the Polaris RLS pool selection rule: per-tenant queries use `*db.RLSPool`, cross-tenant system queries use the raw pool with `SET LOCAL ROLE polaris_admin` and explicit org filtering.

### 6.4 Trust Ladder

Customers do NOT grant Orion full operational scope on day one. Each `(org_id, repo_id)` pair has a `trust_mode` that gates capabilities:

| Mode | Auto-files issues? | Opens PRs? | Notifies CODEOWNERS? | Targets default branch's PR base? | Submits evidence to Polaris? |
|---|---|---|---|---|---|
| `shadow` | No (logs intent only) | No (logs intent only) | No | N/A | No |
| `draft` | No (logs intent only) | Yes, opened as **draft** PRs to a non-protected `orion-staging` base branch Orion creates | No | No (always to `orion-staging`) | No |
| `staging` | Yes, with reduced caps | Yes, ready-for-review PRs to `orion-staging`; customer manually rebases to feature branches | No (notifies a specific reviewer set the customer configures) | No | Yes (preview) |
| `full` | Yes | Yes, ready-for-review PRs to default-branch base | Yes (per repo CODEOWNERS) | Yes | Yes |

Promotion criteria are configured per `.orion/config.yaml` `trust_ladder.promote_after` (§5.1). Default criteria favor caution: 7 days clean for shadow→draft, 5 merged PRs for draft→staging, 20 merged PRs with zero post-merge incidents for staging→full.

Demotion is automatic and aggressive: any `critical` escalation, any post-merge incident filed in Polaris within `post_merge_window`, or operator-issued demotion drops the trust mode by one level. Re-promotion follows the normal criteria.

The trust ladder is structural, not advisory. The capability table above is enforced at the API layer in service code. Agents have no path to bypass.

---

## 7. Orchestration State Machine

### 7.1 Run States

```
created → inventorying → scanning → backlog_active → draining → completed
                              ↓                ↓                       ↑
                           paused ───────────-┘                        │
                              ↓                                         │
                          cancelled                                     │
                              ↓                                         │
                           failed                                       │
                              ↓                                         │
                       config_invalid ────────────────────────────────-┘
```

| State | Meaning |
|---|---|
| `created` | Run record persisted; no work started yet. |
| `inventorying` | Generating yield projection; no tracker writes. |
| `scanning` | Scan loop active; architectural inference and risk filing in progress. |
| `backlog_active` | At least one worker is running OR the backlog still has eligible issues. |
| `draining` | Operator or schedule signaled stop; finishing in-flight workers; no new dispatch. |
| `completed` | Backlog empty, no in-flight workers, all PRs delivered. |
| `paused` | Operator-paused; in-flight workers paused at next safe point; resumable. |
| `cancelled` | Operator-cancelled; in-flight workers cleaned up; non-resumable. |
| `failed` | Unrecoverable error (e.g., Polaris unreachable for callbacks past retry exhaustion). |
| `config_invalid` | `.orion/config.yaml` failed validation. |

### 7.2 Issue Claim States

```
unclaimed → claimed → dispatched → in_progress → pr_open → reconciling → released
                                       ↓             ↓                      ↑
                                   escalated   superseded ──────────────────┤
                                       ↓                                     │
                                  human_review ─────────────────────────────┤
                                                                             │
                                                            post_merge_incident → re_evaluation_queued
                                                                                                    ↓
                                                                                            re_dispatched
```

Definitions of new states:

- `post_merge_incident`: A customer incident in Polaris within `post_merge_window` of an Orion-merged PR for this issue's service. Triggers harness augmentation (§16.6).
- `re_evaluation_queued`: The patch shipped under this issue is queued for re-verification under augmented harness.
- `re_dispatched`: A new worker has been spawned to re-evaluate.

Other state semantics unchanged from draft 1.

The `claimed` state is durable (DB row), not in-memory only. This makes restart and leader-handover recovery robust.

### 7.3 Worker Session Phases

Within `dispatched` and `in_progress`:

```
preparing_sandbox → loading_run_snapshot → synthesizing_patches
                  → verifying_patches → composing_patches
                  → opening_pr_or_draft → succeeded
                                              ↓
                                          failed | timed_out | stalled | cancelled
```

`loading_run_snapshot` is new in draft 2: the worker reads the run's pinned ArchitecturalModel, ConstraintSurface, and Harness at this phase. The worker MUST NOT re-read the live Polaris controls catalog mid-session (§14.6).

### 7.4 Idempotency and Recovery Rules

1. **Issue claim is durable** with a UNIQUE constraint on `(org_id, issue_external_id)` in a transaction that includes the per-run `max_concurrent_workers` cap check AND the worker spawn intent record. Cap check + claim + spawn-intent are one transaction; the actual K8s pod create happens after commit and is idempotent on workspace-key.
2. **Sandbox creation is idempotent on namespace name.**
3. **PR creation is idempotent on branch name.**
4. **Polaris callbacks are retried with exponential backoff** until acknowledged or `max_callback_retries` exhausted.
5. **Restart and leader-handover recovery**: on becoming leader, the new Conductor reads all runs in non-terminal states and reconciles. Fencing tokens (§14.2) prevent former leaders from committing post-handover writes.
6. **Worker spawn intent vs. actual spawn**: a Conductor records spawn intent transactionally with the claim. The actual K8s pod create operation is performed against a downstream controller that idempotently rejects duplicate workspace-keys. This eliminates double-spawn under leader handover.

---

## 8. Issue Ingestion and Backlog Drive

### 8.1 Tracker Adapter Contract

```go
type TrackerAdapter interface {
    Kind() TrackerKind
    FetchCandidates(ctx context.Context, binding TrackerBinding, since time.Time) ([]NormalizedIssue, error)
    FetchByExternalIDs(ctx context.Context, binding TrackerBinding, ids []string) ([]NormalizedIssue, error)
    Create(ctx context.Context, binding TrackerBinding, draft IssueDraft) (NormalizedIssue, error)
    UpdateState(ctx context.Context, binding TrackerBinding, externalID string, state NormalizedState) error
    Comment(ctx context.Context, binding TrackerBinding, externalID, body string) error
    Capabilities() TrackerCapabilities
    HealthCheck(ctx context.Context, binding TrackerBinding) error
}
```

`HealthCheck` is new and required: every adapter MUST expose a structured health probe. Adapters whose health check fails MUST be excluded from polling until recovery.

### 8.2 v1 Adapters

| Kind | Read | Create | Update | Comment | Webhook | v1 customer-facing |
|---|---|---|---|---|---|---|
| `github_issues` | yes | yes | yes | yes | yes | YES |
| `linear` | yes | yes | yes | yes | yes | YES |
| `beads` | yes | yes | yes | yes (notes) | no (poll-only) | NO (Revelara internal dogfood only; see below) |

**Beads is internal-only in v1.** The skills-pipeline lesson explicitly forbids subprocess-based orchestration substrates (§20 #7). Beads in v1 is supported only inside Revelara-controlled SaaS pods that mount a colocated dolt server with explicit health checks; it is NOT exposed to external customers as a tracker option. v2+ may add a hosted-beads HTTP API and re-enable this path.

### 8.3 Unified Backlog and Semantic Dedup

The Conductor merges issues from all bindings of a connected repo into a single in-memory backlog.

Deduplication operates at three levels:

1. **Polaris-risk dedup**: issues from different trackers sharing the same `polaris_risk_id` are merged (one canonical, others marked superseded).
2. **Semantic dedup against existing human-filed issues**: each NormalizedIssue computes a `dedup_signature = sha256(pattern || normalized_call_site)` where `normalized_call_site` is the canonical AST path of the affected call (resilient to refactor and file rename). Before filing a new issue (§8.7), Orion MUST check for an existing open issue with the same dedup signature in any binding; if found, Orion comments on the existing issue rather than filing a new one.
3. **Annotation-based suppression**: a code site annotated `// orion:ignore <pattern> reason="..."` MUST NOT be re-detected, re-filed, or re-patched. Suppression is enforced at synthesis time (no candidate generated for suppressed sites).

### 8.4 Eligibility Rules

A NormalizedIssue is eligible for dispatch if and only if all of the following hold:

1. `state ∈ {open}`.
2. `claim_status = unclaimed`.
3. None of the issue's labels are in the binding's `ineligible_labels` set.
4. None of the issue's referenced file paths fall in the repo's `ineligible_paths` set.
5. If the issue declares a target branch via convention, that branch is not in `ineligible_branches`.
6. The issue has no open blockers.
7. The issue's pattern is in the synthesis `patterns` allowlist AND the pattern's per-repo trust score is above the auto-suppress threshold (§16.5).
8. The customer's tier permits Orion (Architect intelligence multiplier active).
9. The repo's trust mode permits the relevant action (§6.4).
10. The affected call site has no `// orion:ignore` annotation for this pattern.

Issues that fail eligibility for reasons other than `claim_status` are surfaced in the run report with their specific `eligibility` enum value, so the customer can see what was considered and why it was skipped (no silent skips).

### 8.5 Ineligible-Issue Handling

When a tracker contains 95%+ ineligible-by-pattern issues (the realistic case for general-purpose trackers), Orion MUST:

1. NOT poll non-reliability-labeled issues by default. Each binding's `label_filter` (§5.1) defaults to a strict reliability label set; tickets without those labels are skipped at fetch time.
2. Surface a per-run summary of "fetched / eligible / ineligible-with-reason" counts in the run report.
3. NEVER flood the Orion runs view with "ineligible: pattern_not_supported" entries for ordinary feature work. Ineligibility is a fetch-time filter for unlabeled issues; only labeled-but-rejected issues appear in the run report.

### 8.6 Priority

Among eligible issues:

1. Issues linked to Polaris risks with `severity=critical` first.
2. Then by Polaris risk `score` descending.
3. Then by tracker priority.
4. Then by `created_at` ascending (FIFO).

Ties broken deterministically by `external_id` lexical order.

### 8.7 Auto-Filed Risk Issues (Scan Loop Output)

When the scan loop identifies a control gap and dedup (§8.3) finds no equivalent, Orion MAY file a new issue if all of the following hold:

1. The trust mode permits filing (`shadow` does not file; `draft` does not file; `staging` files with reduced caps; `full` files normally).
2. The binding has `auto_file: true`.
3. The pattern's per-repo trust score is above the auto-suppress threshold.

Caps:

- Per-run: `scan.max_auto_filed_per_run` (default 25 for `full`, 10 for `staging`, 0 for `draft`/`shadow`).
- Per-tenant per 24h: `scan.max_auto_filed_per_24h` (default 100 for `full`, 30 for `staging`).

Filed issues:

1. Carry the label `orion-filed`, the binding's `auto_file_labels`, and the corresponding Polaris risk ID in the body.
2. Include the inferred pattern, the affected file:line, the dedup signature, the inventory yield context (e.g., "this is gap 3 of 18 detected; 14 above the trust threshold"), and a link to the Polaris risk detail.
3. Are eligible for Orion to claim and remediate immediately on the next backlog tick (subject to §8.4).

---

## 9. Brownfield and Greenfield Modes

### 9.1 Brownfield (v1)

The default and only v1 mode. Orion is given a connected repo with existing code and follows §3.1 loops 1-5.

### 9.2 Greenfield (v2+, Acknowledged Rewrite)

**Honest scoping**: greenfield is NOT an extension of v1 brownfield. It is a parallel pipeline that shares the L2 Coordination, L5 Integration, and L6 Observability layers but replaces the L3 Synthesis layer's front end.

The v1 implementation MUST NOT pretend otherwise. Greenfield in v2 will require:

- A new `internal/architect/greenfield` module that parses design documents into a proto-architectural model (today's `internal/architect` parses Go source; the two share no implementation code).
- A new `internal/harness/greenfield` module that bootstraps a baseline-free harness (today's verification compares patched vs. baseline; greenfield has no baseline, so the harness instead validates against the constraint surface directly).
- A new `internal/patches/scaffolding` module that emits initial-implementation scaffolds rather than diffs.

The v1 spec defines the L2/L5/L6 contracts cleanly enough that the v2 greenfield modules can plug in without reshaping coordination. That is the extent of the v1 commitment to greenfield-extensibility. **Marketing the v1 architecture as "greenfield-ready" is a mistake; the architecture is greenfield-friendly at the integration seam, no more.**

### 9.3 Hybrid (v2+)

Out of v1 scope. Same caveat as §9.2.

---

## 10. Workspace and Sandbox Management

### 10.1 Workspace Layout

For each WorkerSession, Orion provisions:

```
/sandbox-root/<workspace_key>/
├── repo/                  # ephemeral checkout from per-tenant repo cache
├── harness/               # synthesized harness materialization
├── patches/               # candidate patches as files
├── reports/               # in-progress verification artifacts
└── .orion-meta/           # run_id, issue_id, agent session metadata
```

### 10.2 Per-Tenant Repo Cache

To avoid the cost trap of full repo clones per worker pod (the Gastown-worktree property Orion does not literally inherit), Orion maintains a **per-tenant repo cache**: a read-only persistent volume at `/cache/<tenant>/<repo_full_name>/.git` that holds the repo's full object store. Worker pods mount this cache read-only and create a working tree (via `git --git-dir=<cache>/.git --work-tree=<workspace> checkout`) for the run's pinned commit SHA.

This restores the shared-object-store property from Gastown's worktree pattern at SaaS scale: spawning N workers on one repo costs O(working tree) per worker, not O(repo history). The cache itself is refreshed on a per-tenant cron and on first use.

### 10.3 Sandbox Isolation Requirements

Each per-run namespace MUST:

1. Have **no egress** to the public internet except to: the LLM provider endpoint, the customer's Git provider, Polaris, and Orion's own control plane.
2. Have **no ingress** except from Orion's own control plane.
3. Have **no shared volumes** with any other namespace EXCEPT the per-tenant read-only repo cache (§10.2).
4. Have **no shared secrets** with any other tenant's namespace.
5. Be **destroyed within 24 hours** of run termination.

### 10.4 Safety Invariants

1. The agent runs only inside the workspace. `cwd == /sandbox-root/<workspace_key>/repo` is validated before agent launch.
2. The workspace path stays inside `/sandbox-root`. Symbolic-link traversal MUST be rejected.
3. The workspace key is sanitized to `[A-Za-z0-9._-]`.
4. The agent never receives credentials for the customer's production systems.
5. Orion never operates on `main`, `master`, or `ineligible_branches`-matching branches.
6. The repo cache mount is read-only at the kernel layer (mount option `ro`); worker writes to the cache are impossible regardless of agent behavior.

### 10.5 Cleanup Hooks

Each worker phase boundary fires a cleanup hook. On exhaustion, the namespace is forcibly deleted by an operator-controlled reaper.

---

## 11. Worker and Agent Runner Protocol

### 11.1 Worker Spawn Mechanism

A worker is a Kubernetes pod in the run's namespace. The pod runs the `orion-worker` binary, which:

1. Reads its assignment.
2. Mounts the per-tenant repo cache (§10.2) and creates a working tree at the pinned commit.
3. Loads the run snapshot (ArchitecturalModel, ConstraintSurface, Harness) from Orion's database.
4. Connects to the LLM provider for patch synthesis.
5. Runs the verification loop.
6. Opens the PR via the GitHub App (if trust mode permits).
7. Reports completion to the Conductor and exits.

Worker pods are stateless. Worker death is recoverable via the Lookout (§14.4).

### 11.2 Agent Runner Contract

Inside the worker, the `AgentRunner` mediates LLM interaction:

```go
type AgentRunner interface {
    StartSession(ctx context.Context, system Prompt) (SessionID, error)
    Turn(ctx context.Context, sid SessionID, userMsg string, tools []ToolDef) (TurnResult, error)
    Cancel(ctx context.Context, sid SessionID) error
}
```

A `Turn` MUST emit incremental events (`tokens_in_progress`, `tool_call_requested`, `tool_result`, `turn_complete`).

`last_event_at` is the heartbeat used for stall detection by the Lookout.

### 11.3 Tool Policy and Structural Scope Enforcement

The agent has access to a strictly limited tool set:

| Tool | Scope | Out-of-scope rejection |
|---|---|---|
| `apply_patch` | Apply a unified diff inside `repo/`. | Path validation: target MUST be inside the workspace, MUST NOT touch `ineligible_paths`, MUST NOT match `// orion:ignore`-annotated sites. Out-of-scope writes return a `ScopeRequest` rejection event. |
| `run_command` | Run a command from a static whitelist (`go build`, `go test`, `go vet`, `golangci-lint run`, `git status`, `git diff`). NO arbitrary shell. NO network. | Commands not on the whitelist are silently unavailable (the agent literally cannot call them). |
| `read_file` | Read a file inside the workspace. | Path validation. |
| `query_run_snapshot` | Read the pinned controls snapshot, pinned architectural model, and pinned constraint surface from the run record. | Read-only against snapshot, NOT live Polaris. (§14.6.) |
| `submit_patch_for_verification` | Hand a candidate patch to the verifier. | Verifier rejects out-of-scope patches before execution. |

Tools MUST NOT include: arbitrary shell, arbitrary HTTP, package install, git push, git remote modify, kubectl, or anything that can mutate state outside the workspace.

**Structural enforcement of §20 #2 (no scope expansion)**: the agent has no tool that can perform a scope-expanding action. The defense is the absence of the capability, not a runtime confirmation prompt. Every tool dispatch records a `ScopeRequest` row (§4.1.12) with the inferred scope; out-of-scope rejections are preserved as evidence for escalation review.

### 11.4 Continuation Turns and Snapshot Discipline

A worker MAY run multiple agent turns in one session. After each turn:

1. The Lookout re-checks the issue state in the tracker.
2. If state is no longer `open`, the worker terminates with status `superseded`.
3. If `tokens_in + tokens_out > token_budget_per_issue`, the worker terminates with status `budget_exhausted` and escalates.
4. The agent's view of controls, architectural model, and constraint surface is the **run snapshot only** (§14.6). The agent CANNOT read live Polaris state mid-session.

Continuation prompts SHOULD be terse to avoid token waste.

---

## 12. Synthesis Pipeline

### 12.1 Architectural Inference (Brownfield, `internal/architect`)

Inputs: cloned repo at pinned SHA, language config.
Outputs: ArchitecturalModel including `envelope_confidence` (§4.1.4).

The inferer parses Go source, builds a service-level dependency graph, identifies HTTP/gRPC endpoints, traces downstream client calls, and identifies persistent stores. Hot paths are inferred from request handler complexity and call frequency in test fixtures (no production telemetry).

`envelope_confidence` is computed from coverage signals: how many endpoints have at least one fixture-based call, how much of the call graph is reachable from a fixture, what fraction of declared SLOs have at least one corresponding code constraint inferable. Low confidence (< 0.4) BLOCKS synthesis (§verification.envelope_confidence_floor) and emits an escalation requesting customer-supplied envelope inputs (§12.3).

The inferer MUST be deterministic on a given commit SHA.

### 12.2 Constraint Inference (`internal/constraints`)

Inputs: ArchitecturalModel, snapshotted Polaris controls catalog (NOT live).
Outputs: ConstraintSurface.

The inferer:

1. Reads the controls snapshot from the run record (the live Polaris API was queried at run-start; its result is now immutable for this run).
2. Derives implicit constraints from code.
3. Optionally merges customer-supplied envelope inputs (§12.3).
4. Resolves conflicts by preferring explicit Polaris controls over inferred constraints, logged.

### 12.3 Customer-Supplied Envelope (Optional)

Customers MAY upload anonymized envelope inputs to address the §1.3 envelope-mismatch limit:

- Request distributions per endpoint (k6/Gatling/Locust formats).
- Fault profiles (toxiproxy configs, chaos manifests).
- Resource utilization snapshots (Prometheus exports).

Uploads are scoped per repo, encrypted at rest, and consumed by the harness synthesizer (§12.4). If customer-supplied inputs exist, `envelope_confidence` is recomputed and reported. Customer-supplied envelope is the supported route for raising harness fidelity without granting Orion production access.

### 12.4 Harness Synthesis (`internal/harness`)

Inputs: ArchitecturalModel, ConstraintSurface, optional customer envelope.
Outputs: Harness with deterministic `seed`.

### 12.5 Patch Synthesis (`internal/patches`)

Inputs: ArchitecturalModel, ConstraintSurface, control gaps.
Outputs: CandidatePatches.

For each detected control gap, the patch synthesizer prompts the LLM with: the affected code, the Polaris control text (from snapshot), the relevant Polaris knowledge enrichment (from snapshot), and a constrained patch grammar. The LLM model name and the LLM provider seed (where available) are recorded with the candidate.

### 12.6 Verification with Statistical Confidence (`internal/verify`, `internal/stats`)

**The strict-dominance claim from draft 1 was unrealistic.** Real benchmarks have variance. Draft 2 replaces strict dominance with statistical dominance.

Inputs: CandidatePatch, baseline metrics, Harness.
Outputs: Verdict, metrics with confidence intervals.

The verifier:

1. Applies the patch.
2. Verifies the build.
3. Runs the harness `min_trial_count` times against both baseline and patched (interleaved, to control for cluster-level noise).
4. Computes a confidence interval per measured axis at `confidence_level`.
5. Emits one of:
   - `accepted` iff ALL measured axes show statistically significant improvement (CI bounds favor patched) AND no axis shows statistically significant regression.
   - `rejected_no_dominance`: improvement on no axis is statistically significant.
   - `rejected_regression`: at least one axis shows statistically significant regression.
   - `rejected_low_confidence`: confidence intervals are too wide to determine (typically due to too-few trials or too-noisy harness); the verdict suggests increasing `min_trial_count` or providing more harness fidelity.

The verification report includes per-axis: baseline mean, patched mean, CI bounds, p-value, trial count, decision.

### 12.7 Optimizer Composition

Accepted patches are composed greedily, with re-verification at each composition step (interactions matter; e.g., timeout + retry without backoff produces retry storms).

The composer terminates when no candidate improves the composition under statistical confidence.

**Rejected-candidate visibility (the long-tail problem from Round 1 §1C #11)**: every rejected candidate is recorded with its rejection class. The run report (§21.3) includes a "Rejected Candidates" section with counts per class. If the per-pattern rejection rate exceeds a threshold (default 60%), an `info` escalation is filed suggesting customer review of pattern fitness for this repo.

### 12.8 Operating Envelope Reporting and Reproduction Bundle

The verification report MUST include the operating envelope. Additionally, every run produces a **reproduction bundle** archived to object storage:

- A docker-compose or testcontainer manifest that reproduces the harness on any modern Linux machine.
- The toxiproxy configuration.
- The pinned commit SHA.
- The harness seed.
- The LLM model identifier and provider seed (where available).
- The container image SHAs for harness components.
- A README describing how to run the bundle.

The bundle URL is included in the PR body. The customer SHOULD be able to replay the harness from the bundle alone. **Honest caveats**: LLM-provider nondeterminism and CPU contention may cause minor metric variation; reproduction is "behaviorally equivalent within reported CI bounds," not "bit-identical."

---

## 13. Polaris Integration Contract and Sequencing

### 13.1 Authentication

Per-tenant API key with scopes: `risks:read`, `controls:read`, `knowledge:read`, `evidence:write`, `orion:claim`, `orion:complete`, `incidents:read` (for post-merge watch).

### 13.2 Polaris Endpoints Orion Calls (Read)

| Method | Path | Use |
|---|---|---|
| `GET` | `/api/v1/controls?categories=...` | Constraint inference (snapshot at run start). |
| `GET` | `/api/v1/risks?status=applicable` | Backlog seeding from existing risks. |
| `GET` | `/api/v1/risks/{id}` | Per-issue context. |
| `POST` | `/api/search` | Knowledge enrichment for patch synthesis (snapshot at run start). |
| `POST` | `/api/knowledge/foresight` | Causal-chain analysis for verification (snapshot at run start). |
| `GET` | `/api/v1/incidents?service=...&since=...` | Post-merge incident watch (§16.6). |

### 13.3 Polaris Endpoints Orion Calls (Write)

| Method | Path | Use |
|---|---|---|
| `POST` | `/api/v1/evidence` | Submit accepted patch as evidence for the relevant control. |
| `POST` | `/api/v1/risks/{id}/claim-by-orion` | Reserve a risk during synthesis. NEW endpoint. |
| `POST` | `/api/v1/orion/run-complete` | Notify Polaris on PR open with `{run_id, pr_url, remediated_risk_ids[]}`. NEW endpoint. |

### 13.4 Polaris Endpoints Polaris Surfaces for Customers

| Method | Path | Use |
|---|---|---|
| `GET` | `/api/v1/orion/runs` | List Orion runs for the tenant. NEW endpoint. |
| `GET` | `/api/v1/orion/runs/{id}` | Run detail with verification report. NEW endpoint. |
| `GET` | `/api/v1/orion/escalations` | List open Orion escalations with severity, evidence. NEW endpoint. |
| `POST` | `/api/v1/orion/escalations/{id}/ack` | Acknowledge an escalation from the Polaris UI. NEW endpoint. |

### 13.5 Sequencing and Fallback

**Orion v1 release does NOT depend on Polaris's new endpoints landing first.** If Polaris's new endpoints are unavailable, Orion:

1. Falls back to a **local audit log** stored in Orion's own database.
2. Serves `/api/v1/orion/runs` and `/api/v1/orion/escalations` from Orion's own HTTP surface (§21.4) for the Polaris frontend to consume via reverse-proxy or direct call.
3. Skips evidence and claim-by-orion calls; logs the gap.
4. Resumes full Polaris integration once endpoints are available, with a backfill job replaying queued callbacks.

This eliminates Orion's release dependency on Polaris's release.

### 13.6 Failure Semantics

Per §13.5 plus exponential backoff for transient failures.

---

## 14. Conductor Pattern (Symphony × Gastown Synthesis), Honestly

### 14.1 The Conductor

The Conductor is a single logical authority for orchestration. In v1 it runs as a single replica with leader election; the cluster MAY run multiple replicas for hot-standby, but only the leader executes mutations.

### 14.2 Leader Election Substrate

The leader-election substrate is a **PostgreSQL advisory lock with fencing token**:

1. Each Conductor replica attempts `pg_try_advisory_lock(orion_conductor_lock_id)`.
2. The successful replica reads its `fencing_token` from a single-row `orion_leadership` table and increments it on acquisition.
3. Every state mutation transaction includes `WHERE fencing_token = $current_token` as a guard.
4. A former leader whose token is stale will fail every transaction; in-flight mutations roll back. No further writes are possible without re-acquiring the lock and bumping the token.

This eliminates split-brain at the cost of a small write overhead. The advisory lock TTL is `leader_lease_seconds` (default 30); a lease holder MUST renew within `leader_renew_seconds` (default 10).

### 14.3 What the Conductor Does

1. Reads the unified backlog.
2. Applies eligibility, dedup, and priority.
3. Issues claim transactions including the cap check and worker spawn intent (§7.4 #1, §7.4 #6).
4. Spawns worker pods via the `orion-worker-controller` (idempotent on workspace key).
5. Routes escalations.
6. Drives runs from `created` to terminal state.
7. Emits structured events.

The Conductor does NOT directly observe worker liveness. That is the Lookout's job (§14.4).

### 14.4 The Lookout (Per-Run Worker Reconciler)

Drawn from Gastown's witness pattern but adapted for SaaS: each active run has a dedicated `Lookout` process (a separate pod, NOT the Conductor) that:

1. Watches its assigned run's worker pods at `lookout_tick` (default 30s) frequency.
2. Detects stalled workers (`now - last_event_at > stall_timeout`) and instructs the K8s controller to terminate them.
3. Detects dead pods (eviction, OOM) and reports failure.
4. Reads tracker state per active issue and compares to expected; if drift detected (issue closed externally, label changed), instructs the worker to terminate.
5. Forwards summarized observations to the Conductor.

The Lookout is deliberately decentralized: one Lookout death affects one run's observation, not the whole platform. The Conductor's failure no longer causes worker observation gaps; the Lookouts continue.

### 14.5 The Refiner (Post-Merge Composition Tracker)

Drawn from Gastown's refinery pattern but adapted for the no-auto-merge constraint. The Refiner does NOT auto-merge. Instead:

1. After Orion's PR is merged by a customer, the Refiner records the merged SHA against the run.
2. The Refiner watches Polaris's incidents API (`GET /api/v1/incidents?service=...&since=merged_at`) for `post_merge_window` (default 30 days).
3. If an incident in the same service is filed within that window, the Refiner flags the run for re-evaluation, augments the harness with the new failure mode (where extractable from the incident report), and re-verifies any other PRs Orion shipped sharing patches from the same synthesizer (§16.6).
4. Customers can disable the post-merge watch per repo or per service.

This is Gastown's refinery's contribution at the right semantic level: not "merging," but "isolating regressions and feeding back to verification." It works without requiring auto-merge or binary-bisect of customer trunk.

### 14.6 Snapshot Discipline (Pattern C Mitigation Extended)

Every Run has a `controls_snapshot_id` referencing an immutable snapshot of the Polaris controls catalog as of run start. Workers MUST read the snapshot, NOT the live API, for any controls or knowledge data used in synthesis.

This extends the §5.3 "no mid-run config reload" lesson to Polaris-side mutable state, closing the staleness window the Round 1 reviewer identified.

### 14.7 Communication Substrate

1. **The database** for all durable state.
2. **A pub/sub channel** (NATS or Redis Streams) for ephemeral coordination (worker spawn notifications, completion notifications, Lookout reports).

### 14.8 Escalation Routing Matrix

Escalations are routed by class to the right responder. **No escalation pages the wrong party.**

| Class | Examples | Routes to | SLA |
|---|---|---|---|
| `customer:patch_review` | PR rejected with comment; pattern-fit review needed | Customer's configured Slack channel | None (customer pace) |
| `customer:eligibility_question` | Repo has 0 eligible issues; trust mode demoted | Customer's configured Slack channel | None |
| `customer:safety_quarantine` | Pattern auto-suppressed; customer must re-enable | Customer's configured Slack channel + Polaris escalations view | None |
| `revelara:harness_failure` | Sandbox provisioning failed; harness materialization error | Revelara on-call PagerDuty | 30 min ack |
| `revelara:platform_critical` | Agent attempted out-of-workspace write; safety violation | Revelara on-call PagerDuty | 5 min ack |
| `revelara:integration_break` | Polaris callback exhausted; tracker adapter health-check failing | Revelara on-call PagerDuty | 30 min ack |

Customer-side escalations NEVER page Revelara. Revelara-side escalations NEVER page the customer (the customer sees them in the Orion runs view as "Revelara investigating"). This is the operator-confusion fix from Round 1 §1B #6.

Stale escalations re-escalate within their class only (customer escalation re-escalates to a customer escalation manager, never to Revelara; Revelara escalation re-escalates within Revelara's pager rotation).

### 14.9 What the Conductor Does NOT Do

- Execute synthesis work (worker).
- Mutate code, branches, or PRs (worker, gated by trust mode).
- Decide whether to merge (customer).
- Bypass any externally-enforced gate.
- Observe individual worker liveness (Lookout).
- Watch for post-merge incidents (Refiner).

---

## 15. Brownfield Scan Loop

### 15.1 When the Scan Loop Runs

Per `.orion/config.yaml` cadence: `on_demand`, `daily`, `weekly`, or `on_push`.

### 15.2 Scan Phases

1. **Clone** (or refresh repo cache).
2. **Infer ArchitecturalModel** with `envelope_confidence` (§12.1).
3. **Cross-reference** with Polaris risks, controls catalog (snapshot for this scan), and existing tracker issues. Compute the set of new control gaps vs. already-tracked risks AND vs. customer-suppressed sites (§8.3 annotations).
4. **Auto-file or skip** per trust mode (§6.4) and caps (§8.7).
5. **Persist** scan results in `orion.scans` for trend analysis. Persist the rejection of suppressed-or-deduped gaps too, so the customer can audit Orion's decisions.
6. **Tear down**.

### 15.3 Scan Output as Polaris Risks

For each filed issue, Orion calls `POST /api/v1/risks` (or the equivalent) to ensure the risk exists in Polaris.

### 15.4 Self-Referential-Loop Guard

Round 1 §1A #2 noted the risk that Orion files issues for the same patterns Orion remediates, creating a closed loop with itself rather than with the customer's actual backlog.

Mitigation: every run report (§21.3) breaks down issues processed by **provenance**:

- Customer-filed issues processed.
- Orion-filed issues processed.
- Polaris-prior risks processed.

If `Orion-filed >> customer-filed` over a sustained period, the run report flags a `self_referential_loop_warning` and recommends pattern allowlist review (perhaps the patterns are not what the customer's actual backlog is dominated by).

---

## 16. PR Delivery, Merge Semantics, and Post-Merge Hooks

### 16.1 PR Composition

For each accepted-patch composition, a fresh branch is created via the GitHub App. Each accepted patch becomes a separate signed commit, ordered by composition sequence, with a conventional-commit message. The PR body is the verification report rendered as markdown plus the reproduction-bundle URL plus a footer linking to the Polaris run ID.

PR base depends on trust mode (§6.4): `orion-staging` for `draft` and `staging` modes; default branch for `full` mode.

PR title follows: `orion: <issue-title> [<count> patches across <controls>]`.

### 16.2 Merge Gate (Subset-Comparison)

Orion does NOT auto-merge. Customer CI is the gate.

**Onboarding ships a reference subset-comparison gate** for GitHub Actions, CircleCI, and Buildkite, installable in the customer's `.github/workflows/` or equivalent. Other CIs are documented but require customer or operator integration work.

The gate compares the failing-test set on the PR's branch to the failing-test set on `main` at the same SHA. The PR is mergeable iff the branch's failing set is a subset of main's failing set.

### 16.3 Branch Protection and Required Reviewers

If the customer's repo has branch protection, Orion's PR sits in `awaiting review` until a human approves. **This is correct behavior.**

### 16.4 PR Reconciliation

The reconciler polls the PR state:

| PR state | Orion action |
|---|---|
| `open` | Continue polling. |
| `merged` | Update Polaris (risk `mitigated`). Submit evidence. Mark issue `released`. Set `post_merge_window_ends_at`. Refiner begins watching. |
| `closed_unmerged` | Mark issue `released` with reason `customer_rejected`. Parse comment for rejection signal (§16.5). Do NOT auto-reopen. |
| `superseded` | Close the old PR with link to the new. |

### 16.5 Rejection Learning (v1, Real)

Round 1 §1A #9 noted that draft 1's "v1 logs and surfaces" was insufficient. Draft 2's v1 minimum:

1. Each PR closure (merged or unmerged) updates the per-pattern, per-repo PatternTrustScore (§4.1.11) via exponential moving average.
2. If the score drops below `pattern_auto_suppress_threshold` (default 0.4 over rolling 10 PRs), the pattern is auto-suppressed for that repo: §8.4 rule 7 makes ineligibility automatic.
3. Auto-suppression files a `customer:safety_quarantine` escalation with the rejection signal and recommended remediations.
4. Customer can re-enable with one click (or one API call); re-enablement resets the EMA to neutral.
5. The Refiner extends this with post-merge incident signals (§16.6).

This is genuinely structural, not "logs and surfaces."

### 16.6 Post-Merge Incident Hooks (NEW IN DRAFT 2)

The Refiner (§14.5) watches Polaris incidents for `post_merge_window` (default 30 days) per merged Orion PR. If an incident is filed in the same service:

1. The run is re-opened in `re_evaluation_queued` state for the affected issue.
2. The harness is augmented with the new failure mode where extractable from the incident.
3. Other PRs from this run sharing patches with the same synthesizer are flagged for re-verification.
4. A `customer:patch_review` escalation is filed with the incident link, the affected patches, and the re-verification status.
5. The PatternTrustScore is decremented heavily (post-merge incidents weigh 5× ordinary rejections in the EMA).

If re-verification under augmented harness shows regression, Orion files a follow-up PR that backs out the offending patch with a verification report explaining the regression.

This is the Round 1 §1B #12 fix and the §22 lesson "Confidence without grounding cascades errors" applied at the post-merge boundary.

---

## 17. Quality Gates

### 17.1 Layered Gate Architecture

| Tier | Where enforced | Examples |
|---|---|---|
| **Tier 1: Pre-synthesis** | Worker, before patch generation | Workspace integrity, sandbox isolation, sanitized identifiers, snapshot integrity |
| **Tier 2: Per-patch verification** | Verifier, before patch acceptance | Build, harness pass with statistical confidence, dominance check, no regression |
| **Tier 3: Pre-PR delivery** | Worker, before opening the PR | Composition re-verification, lint/format checks, customer's `.orion gates.pre_pr` commands, envelope-confidence floor check |
| **Tier 4: Customer CI** | Outside Orion's control | Subset-comparison gate (Orion ships reference for top 3 CIs), customer's required checks |

### 17.2 Gate Execution Invariants

1. Gate logic lives in service code, NEVER in agent prompt text. (Skills-pipeline lesson, §22.)
2. Gate failures MUST produce a structured error.
3. Gates SHOULD be deterministic on the same inputs; non-determinism MUST be quarantined and surfaced separately.

### 17.3 Reference Subset-Comparison Gate

Orion onboarding ships maintained reference implementations for GitHub Actions, CircleCI, and Buildkite. Each implementation:

1. Captures `main`'s failing test set (cached per session).
2. Runs tests on the rebased branch.
3. Computes the delta.
4. Sets a PR check status with the delta as the message.

These are open source under the Orion docs repo, versioned, and tested. Customers using other CIs can adapt, but Orion guarantees first-class support for the top 3.

---

## 18. Failure Model and Recovery

### 18.1 Failure Classes

| Class | Examples | Recovery | Routing (§14.8) |
|---|---|---|---|
| **Configuration** | `.orion/config.yaml` invalid | Reject `config_invalid`. | `customer:eligibility_question` |
| **Workspace** | Sandbox provisioning failed | Retry; on exhaustion `revelara:harness_failure`. | Revelara |
| **Agent session** | LLM 500; budget exhausted; stall | Retry per `max_retries_per_issue`. | Customer or Revelara depending on cause |
| **Tracker** | GitHub API down; webhook signature invalid | Retry; circuit-breaker. | Revelara if sustained |
| **Polaris callback** | Polaris unreachable | Retry; fallback to local audit (§13.5). | Revelara |
| **Verification** | No patch dominates with confidence | Close issue `no_improvement`; positive signal. | None (informational) |
| **Safety violation** | Out-of-workspace write attempt | Halt; preserve evidence; `revelara:platform_critical`. | Revelara |
| **Post-merge regression** | Incident in `post_merge_window` | Re-evaluate; augment harness. | `customer:patch_review` |

### 18.2 Recovery on Restart and Leader Handover

On Conductor restart or leader handover (§14.2): the new leader reads runs in non-terminal states and reconciles via the Lookouts. Fencing tokens prevent former-leader split-brain.

### 18.3 Operator Intervention Points

Operators may pause, resume, cancel, acknowledge escalations, quarantine issues, disable a pattern mid-run (§5.3 exception), or demote trust mode. All actions are audited.

---

## 19. Security and Operational Safety

### 19.1 Trust Boundary

| Trusted | Untrusted |
|---|---|
| Orion's control plane. | Customer code. Customer issues. |
| Polaris API. | LLM provider responses (filtered, never executed unfiltered). |
| Operator-controlled K8s cluster. | Anything in the per-run namespace beyond control plane. |

### 19.2 Secret Handling

Per §6.2.

### 19.3 No Cross-Tenant Leakage

Per Polaris RLS rule. LLM context tenant-scoped. Per-tenant repo cache (§10.2) is strictly per-tenant; no cross-tenant cache sharing.

### 19.4 No Production Reach

Network policy whitelist only. Defense in depth via NetworkPolicy.

### 19.5 Customer Data Export and Off-Ramp (NEW IN DRAFT 2)

On customer cancellation, Orion MUST:

1. Within 30 days, export all run reports, verification reports, reproduction bundles, and audit logs to a customer-controlled bucket (or a signed download URL).
2. Within 60 days, delete all customer code clones, harness artifacts, and per-tenant secrets.
3. Retain audit logs for the contracted period, accessible to the customer and to Revelara legal only.

If the customer cancels Polaris but keeps Orion, Orion enters a degraded mode: synthesis continues using cached snapshots from the last successful Polaris call, with explicit operator warnings. Cached snapshots expire after `polaris_disconnected_grace` (default 7 days), after which Orion enters `failed`.

If the customer cancels Orion but keeps Polaris, Orion's historical PRs remain in the customer's repo (Orion never owned them). Polaris's risk register reflects the merged remediations.

### 19.6 Audit Logging

Tamper-evident, signed, retained per contract.

---

## 20. Forbidden Behaviors (Anti-Patterns), Each Structurally Enforced

These are codified directly from the skills-pipeline experiment. **Each MUST be paired with the structural enforcement point in code.**

| # | Forbidden behavior | Structural enforcement |
|---|---|---|
| 1 | Rationalize past externally-enforced gates | Verifier (§12.6) and Tier 4 CI evaluate after agent emits patch; agent has no control flow back into the gate |
| 2 | Expand scope beyond a literal request | Tool whitelist (§11.3) lacks any tool that performs scope-expanding actions; ScopeRequest records (§4.1.12) capture rejection evidence |
| 3 | Assert diagnoses as fact without evidence | Verification report format (§12.6) requires per-axis CI, p-value, trial count; reports without grounded numbers are rejected by report-builder |
| 4 | Long-running daemon-style agent loops with stale prompts | Workers are per-issue Kubernetes pods (§11.1); agent sessions are turn-scoped within one worker; snapshot discipline (§14.6) prevents mid-session state drift |
| 5 | Destructive remote actions | Tool whitelist excludes git push, remote modify, repo modify; GitHub App scopes exclude branch-delete on non-Orion-created branches |
| 6 | Auto-merge to protected branches | GitHub App lacks merge permission; trust ladder (§6.4) gates branch-target choice; Refiner does not auto-merge |
| 7 | Build orchestration on unstable substrate | Tracker adapter `HealthCheck` (§8.1); subprocess-based beads is internal-only (§8.2); leader election uses PostgreSQL advisory lock with fencing (§14.2); per-tenant repo cache (§10.2) eliminates per-pod clone storm |
| 8 | Default to autonomous parallelism | `max_concurrent_workers` default 4 per run; trust ladder gates aggressive concurrency raising |
| 9 | Absolute test-pass gates against possibly-rotting main | Subset-comparison gate is Tier 4 default; reference implementations shipped (§17.3) |
| 10 | Agent grants itself elevated privileges | Tool whitelist enforced in worker process binary (which the agent is sandboxed inside); tools the agent does not see do not exist for it |
| 11 | Treadmill of re-detecting suppressed sites | `// orion:ignore` annotation enforced at synthesis-time, before scan filing (§8.3) |
| 12 | Spam tracker with auto-filed issues | Hard caps (§8.7); semantic dedup (§8.3); trust-ladder gating |
| 13 | Page the wrong operator | Escalation routing matrix (§14.8) |
| 14 | Lose track of long-tail rejected candidates | Run report rejected-candidates section (§12.7); pattern-rejection threshold escalation (§16.5) |

A forbidden behavior without a structural enforcement point is not a real constraint. Reviewers MUST flag any addition to this table that lacks an enforcement column.

---

## 21. Observability

### 21.1 Logging Conventions

Structured, key=value. Required fields: `run_id`, `issue_id`, `worker_id`, `phase`, `outcome`, `failure_class`, `escalation_class`.

### 21.2 Metrics

| Metric | Type | Use |
|---|---|---|
| `orion_runs_total` | counter, by `outcome` | |
| `orion_issues_dispatched_total` | counter | |
| `orion_patches_accepted_total` / `_rejected_total` (by reason) | counter | |
| `orion_prs_opened_total` / `_merged_total` / `_closed_unmerged_total` | counter | |
| `orion_worker_phase_duration_seconds` | histogram, by `phase` | |
| `orion_escalations_total` | counter, by `class` and `severity` | |
| `orion_polaris_callback_failures_total` | counter | |
| `orion_token_consumed_total` | counter, by `provider` | |
| `orion_post_merge_incidents_observed_total` | counter | New in draft 2 |
| `orion_pattern_trust_score` | gauge, by `(repo, pattern)` | New in draft 2 |
| `orion_envelope_confidence` | gauge, by `repo` | New in draft 2 |
| `orion_repo_cache_size_bytes` | gauge, by `tenant` | New in draft 2 (cost visibility) |
| `orion_inventory_yield_projection_prs` | gauge, by `repo` | New in draft 2 |
| `orion_inventory_yield_observed_prs` | counter, by `repo` | New in draft 2 |

### 21.3 Run Report

Each terminal run produces a markdown report archived to object storage:

```
# Orion Run Report
Run ID: <uuid>
Repo: <full_name> @ <commit_sha>
Tenant: <org_id>  Trust Mode: <mode>
Started: ...   Finished: ...   Duration: ...
Outcome: <status>

## Summary
- Issues considered: N
  - Customer-filed: A
  - Orion-filed:    B
  - Polaris-prior:  C
- Issues dispatched: N
- PRs opened: N (draft: X, staging: Y, full: Z)
- PRs merged: N (with merge latency distribution)
- Patches accepted: N
- Patches rejected: N (by reason)
- Self-referential-loop warning: Yes/No

## Yield vs Projection
- Projected this run: N PRs
- Observed: M PRs
- Cumulative projection: N PRs over period
- Cumulative observed: M PRs

## Per-Issue Outcomes
[ table including eligibility status, verdicts, PR URL, post-merge status ]

## Rejected Candidates
[ table by pattern with rejection reason class and counts; suggestions ]

## Pattern Trust Scores
[ table per pattern: trust score, auto-suppressed flag, suggestion ]

## Escalations
[ list with class, severity, evidence, ack status ]

## Token and Compute Accounting
[ tokens by provider, harness CPU-hours, repo cache hit rate ]

## Operating Envelope
[ harness configuration summary, fault matrix, seeds, envelope confidence ]

## Reproduction Bundle
URL: <object-storage-link>
Bundle SHA: <hash>
Honest caveats: <LLM nondeterminism, CPU contention notes>
```

### 21.4 HTTP Status Surface

```
GET    /api/v1/runs                        - list runs for tenant
GET    /api/v1/runs/{id}                   - run detail
GET    /api/v1/runs/{id}/report            - markdown report
GET    /api/v1/runs/{id}/issues            - per-issue outcomes
GET    /api/v1/runs/{id}/reproduction      - reproduction bundle metadata
POST   /api/v1/runs                        - start a run
POST   /api/v1/runs/inventory              - start an inventory-only run
DELETE /api/v1/runs/{id}                   - cancel
POST   /api/v1/runs/{id}/pause             - pause
POST   /api/v1/runs/{id}/resume            - resume
POST   /api/v1/runs/{id}/disable-pattern   - operator: disable pattern mid-run (§5.3 exception)
GET    /api/v1/state                       - global Conductor state snapshot
GET    /api/v1/escalations                 - open escalations for tenant, filtered by class
POST   /api/v1/escalations/{id}/ack        - acknowledge an escalation
POST   /api/v1/repos/{id}/trust-mode       - set trust mode (operator action)
GET    /api/v1/repos/{id}/yield            - current yield model state for repo
POST   /api/v1/repos/{id}/envelope-upload  - customer uploads anonymized envelope
```

All responses are JSON; report endpoint returns markdown.

---

## 22. Lessons Learned (Codified)

| Lesson | Where this spec applies it |
|---|---|
| Externally-verifiable invariants beat agent self-discipline | §17.2, §20 (every forbidden behavior paired with structural enforcement) |
| Subset-comparison beats absolute gates | §16.2, §17.3 (reference CI integrations shipped) |
| Centralized infrastructure precedes orchestration | §3.3, §10.2 (per-tenant repo cache), §14.2 (PG advisory lock for leader) |
| Adversarial review proportional to work | §15 (scan-loop opt-in cadence; auto-file caps; trust ladder) |
| Default to single-developer / sequential flow; parallelism opt-in | §20 #8 (concurrency cap; trust-ladder gates) |
| Distinguish design-time from runtime | §11 (workers are service code; agents turn-scoped); §22 (lessons table is a doc artifact, structural enforcement lives in §20) |
| State-aware automation | §16.4 (PR reconciliation polls live state); §10.3 (sandbox isolation); §14.6 (snapshot discipline) |
| Inference-without-state-verification is most autonomous-failure shape | §11.4 (Lookout re-checks); §20 #2 (tool whitelist absence) |
| Confidence without grounding cascades errors | §12.6 (CI bounds + p-values required); §20 #3 |
| Static-prompt / dynamic-runtime asymmetry breaks long-running loops | §5.3, §14.6 (snapshot discipline) |
| Autonomous merge fails | §20 #6, §16, trust ladder |
| Infrastructure friction swallows orchestration gains | §20 #7, §10.2 |
| Reliability-by-construction inverts the SRE model | The entire spec; this is Orion's premise |
| Yield must be modeled, not promised | §1.5 (Yield Model) |
| Trust must be staged, not granted at once | §6.4 (Trust Ladder) |
| Envelope mismatch is the foreseeable customer failure | §1.3, §12.1 (envelope_confidence), §12.3 (customer-supplied envelope) |
| Post-merge regressions are the renewal-killer if ignored | §16.6 (post-merge incident hooks) |

---

## 23. Reference Algorithms

### 23.1 Conductor Tick

```
loop forever:
    if not has_leader_lock_with_fencing(): continue
    runs = db.fetch_runs(state in {created, inventorying, scanning, backlog_active, draining, paused})
    for run in runs:
        if run.state == created:
            if validate_config(run): transition(run, inventorying) else transition(run, config_invalid)
        elif run.state == inventorying:
            if inventory_complete(run):
                if run.mode == inventory_only: transition(run, completed)
                else: transition(run, scanning)
        elif run.state == scanning:
            if scan_complete(run):
                if backlog_has_eligible(run): transition(run, backlog_active) else transition(run, completed)
        elif run.state == backlog_active:
            ensure_lookout_running(run)
            if any_eligible_and_under_cap(run):
                issue = pick_next_eligible(run)
                if claim_and_record_spawn_intent(run, issue):
                    request_worker_spawn(run, issue)  # idempotent on workspace key
            elif no_workers_and_no_eligible(run): transition(run, completed)
        elif run.state == draining:
            if no_workers(run): transition(run, completed)
        elif run.state == paused:
            pass
        process_lookout_observations(run)
        process_refiner_observations(run)
    sleep(conductor_tick_interval)
```

### 23.2 Worker Lifecycle

```
worker(run_id, issue_id):
    workspace = provision_sandbox(run_id, issue_id)
    try:
        snapshot = load_run_snapshot(run_id)  # NOT live Polaris
        gaps = identify_gaps(snapshot.model, snapshot.constraints, issue, workspace)
        gaps = exclude_suppressed(gaps, workspace)  # honor // orion:ignore
        candidates = synthesize_patches(workspace, gaps, snapshot)
        accepted = []
        for c in candidates:
            v = verify_with_confidence(workspace, snapshot.harness, c, baseline=accepted)
            record_candidate(c, v)
            if v.verdict == accepted: accepted.append(c)
        if not accepted:
            report(run_id, issue_id, "no_improvement")
            return
        composition = compose_patches(workspace, accepted, snapshot.harness)
        if not composition:
            report(run_id, issue_id, "no_improvement")
            return
        if envelope_confidence(snapshot) < envelope_confidence_floor:
            escalate(run_id, issue_id, "customer:eligibility_question",
                     reason="envelope_confidence_below_floor",
                     suggestion="upload envelope inputs (§12.3)")
            return
        pr = open_pr_per_trust_mode(workspace, composition, verification_report, snapshot.trust_mode)
        bundle = build_reproduction_bundle(workspace, snapshot)
        attach_bundle_to_pr(pr, bundle)
        report_to_polaris(run_id, issue_id, pr.url, [risk_id_for(issue)])
        report(run_id, issue_id, "succeeded")
    except SafetyViolation as e:
        escalate(run_id, issue_id, "revelara:platform_critical", evidence=e)
    except RecoverableError as e:
        record_failure(run_id, issue_id, e)
    finally:
        teardown_sandbox(workspace)
```

### 23.3 Verification with Confidence

```
verify_with_confidence(workspace, harness, candidate, baseline_set):
    apply_patch(workspace, candidate)
    if not build(workspace): return Verdict(rejected, "build_failed")
    metrics_baseline = []
    metrics_patched = []
    for trial in 1..min_trial_count:
        metrics_baseline.append(run_harness(workspace_without(candidate), harness, trial_seed=trial))
        metrics_patched.append(run_harness(workspace, harness, trial_seed=trial))
    for axis in ALL_AXES:
        ci = compute_confidence_interval(metrics_patched[axis], metrics_baseline[axis], confidence_level)
        if ci.too_wide: return Verdict(rejected_low_confidence, axis)
        if ci.regression_significant: return Verdict(rejected_regression, axis)
    if not any_axis_improvement_significant(metrics_patched, metrics_baseline, confidence_level):
        return Verdict(rejected_no_dominance)
    return Verdict(accepted, metrics=metrics_patched, ci=cis)
```

---

## 24. Test and Validation Matrix

### 24.1 Conformance Profiles

| Profile | Tests |
|---|---|
| Core Conformance | State machine, claim atomicity, fencing-token correctness under leader handover, sandbox isolation, dominance check with confidence, optimizer composition with rejection capture, tracker normalization, semantic dedup |
| Tracker Adapter Conformance | Each adapter passes the standard contract suite including HealthCheck |
| Real Integration Profile | End-to-end against fixture repo + fixture Polaris + fixture LLM provider, including trust-ladder progression and post-merge incident replay |
| Trust-Ladder Conformance (NEW) | Verify each trust mode permits exactly the actions in §6.4's table; demotion triggers fire on simulated incident |

### 24.2 v1 Acceptance Test

Per PRD plus draft-2 additions:

1. Provision a fixture Go service repo with three known reliability gaps.
2. Trigger an Orion run.
3. **Verify**: inventory yield projection is generated and reported.
4. **Verify**: in `shadow` mode, no PR opens and no issue files (only logs).
5. **Verify**: in `full` mode, a PR is opened with three commits.
6. **Verify**: each commit's diff modifies the expected file.
7. **Verify**: PR body contains verification report with CI bounds, p-values, envelope confidence, reproduction bundle URL.
8. **Verify**: in Polaris, Remediations view shows three risks with Orion badges.
9. **Verify**: the Orion run is recorded with metrics deltas matching report.
10. **Verify**: `POST /api/v1/orion/run-complete` was called and acknowledged.
11. **Verify**: a simulated post-merge incident in Polaris triggers Refiner re-evaluation within `post_merge_window`.
12. **Verify**: `// orion:ignore timeout_coverage reason=intentional` annotation suppresses the corresponding gap on next run.
13. **Verify**: rejection of a PR drops PatternTrustScore; threshold breach triggers auto-suppression.

### 24.3 Negative Tests

- Out-of-workspace write attempt: rejected, `revelara:platform_critical` filed, run halted.
- Destructive remote action attempt: rejected by tool whitelist, no escalation needed.
- Malformed LLM patch: rejected at parse.
- Polaris callback exhausted: fallback to local audit; resume on Polaris recovery.
- Tracker webhook signature invalid: dropped, security log emitted.
- Two Conductors elected simultaneously (split-brain): fencing token causes loser's writes to fail; loser steps down.
- Cross-tenant repo cache access attempt: rejected by mount permissions.

---

## 25. Implementation Checklist

### 25.1 Required for v1 Conformance

- [ ] `internal/conductor` with PG advisory-lock leader election and fencing tokens.
- [ ] `internal/lookout` per-run reconciler.
- [ ] `internal/postmerge` Refiner watching Polaris incidents.
- [ ] `internal/database/migrations/` with all entity schemas, RLS policies, indices, fencing-token table, trust-mode column, PatternTrustScore table.
- [ ] `internal/trackers/{github,linear,beads}` adapters with HealthCheck. Beads is internal-only (not exposed to external customers).
- [ ] `internal/sandbox` with K8s namespace, NetworkPolicy, per-tenant repo cache mount.
- [ ] `internal/worker` binary with snapshot loading.
- [ ] `internal/agent` runner with tool whitelist and ScopeRequest recording.
- [ ] `internal/inventory`, `internal/architect`, `internal/constraints`, `internal/harness`, `internal/patches`, `internal/verify`, `internal/stats` per §12.
- [ ] `internal/polaris` client with retry, circuit-breaker, snapshot caching, fallback mode (§13.5).
- [ ] `internal/github` GitHub App handler with branch creation, PR open, signed commits, trust-mode-aware PR base.
- [ ] `internal/report` markdown generator with all draft-2 sections.
- [ ] `internal/repro` reproduction-bundle builder.
- [ ] `internal/api` HTTP surface per §21.4.
- [ ] `cmd/orion` service entrypoint.
- [ ] `cmd/orion-cli` for dogfooding.
- [ ] Reference subset-comparison CI integrations for GitHub Actions, CircleCI, Buildkite (open source under Orion docs repo).
- [ ] All forbidden-behavior tests passing.
- [ ] v1 acceptance test passing.
- [ ] Trust-ladder conformance tests passing.

### 25.2 Polaris-Side Required (NOT a v1 release blocker per §13.5)

- [ ] `internal/orion_link/handler.go` with new endpoints (claim, run-complete, runs list, run detail, escalations).
- [ ] Migration adding `remediations` (or extending `risks`) with claim and PR fields.
- [ ] `orion_enabled`, `orion_autopr` feature flags wired.
- [ ] Frontend Remediations view with Orion badge, Send-to-Orion action, Orion runs sub-page, Escalations view with ack action.

### 25.3 Operational Validation Before Production

- [ ] Restart-recovery test (kill Conductor mid-run; observe convergence; verify no double-spawn).
- [ ] Leader-handover test (force re-election under load; verify fencing works).
- [ ] Cross-tenant isolation test (concurrent tenants; verify no DB, network, filesystem, or repo-cache leakage).
- [ ] Network policy enforcement test.
- [ ] Token-budget exhaustion test.
- [ ] Subset-comparison gate test against fixture main with pre-existing failures.
- [ ] Audit log integrity test.
- [ ] Trust-ladder demotion drill (simulate post-merge incident; verify auto-demote and re-promote).
- [ ] Repo cache cost test (measure clone storage savings vs. naive per-pod clone).
- [ ] Polaris-fallback drill (Polaris offline; verify Orion continues with local audit; verify replay on recovery).

---

## Appendix A: v1 Pattern Set

(unchanged from draft 1)

### A.1 Timeout Coverage
### A.2 Retry Hygiene
### A.3 Idempotency-Key Insertion

---

## Appendix B: Tracker Adapter Examples

### B.1 GitHub Issues
- Read: REST `GET /repos/{owner}/{repo}/issues?state=open&labels=...`.
- Create: REST `POST /repos/{owner}/{repo}/issues`.
- State update: REST `PATCH /repos/{owner}/{repo}/issues/{n}`.
- Webhook: `issues`, `issue_comment` events; HMAC-SHA256 verified.
- HealthCheck: REST `GET /rate_limit`.

### B.2 Linear
- Read: GraphQL `issues(filter: ...)`.
- Create: GraphQL `issueCreate(...)`.
- State update: GraphQL `issueUpdate(...)`.
- Webhook: signature header verified.
- HealthCheck: GraphQL `viewer { id }`.

### B.3 Beads (Revelara-Internal Only)
- HTTP API against an internal beads coordinator (NOT subprocess).
- Customer-facing exposure deferred to v2+ pending hosted-beads HTTP API.

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

1. **Greenfield mode** (§9.2): Acknowledged rewrite of L3, not extension. Out of v1.
2. **Cross-language support**: v2+. The synthesizer is parameterized over language; v1 ships only Go.
3. **Multi-PR per issue** for very large patches: v1 emits one PR per issue.
4. **Customer-pluggable verification**: v1 uses Orion's harness only. v2+ may permit customer-supplied harness adapters.
5. **Pricing-tier enforcement** at the API layer: v1 trusts Polaris.
6. **Continuous mode** (always-on instead of cadence): v2+; v1 is cadence-driven with `on_push` as nearest-to-continuous option.
7. **Auto-merge for low-risk classes** with explicit opt-in: v2+. v1 is human-merge only.
8. **Cross-repo learning** (PatternTrustScore aggregation across repos in same tenant): v2+. v1 is per-(repo, pattern).
