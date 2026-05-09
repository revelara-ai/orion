---
title: Orion Specification
status: Draft v3
authors: Joseph Bironas
created: 2026-05-09
last_updated: 2026-05-09
related:
  - docs/PRD/orion-v1.md
  - docs/research/SPEC.md (Symphony service spec, used as template)
  - docs/research/orchestrated-development-workflow.md
  - docs/research/2026-05-08-skills-pipeline-experiment.md
  - docs/research/reliability-conductor.md
  - docs/SPEC/Orion-SPEC.draft1.md (round 1 review consumed)
  - docs/SPEC/Orion-SPEC.draft2.md (round 2 review consumed)
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

**Orion progressively converts a *named, narrow* slice of a customer's backlog into verified, human-mergeable pull requests. In v1 the slice is exactly: missing or incorrect timeout coverage, missing or incorrect retry hygiene, and missing idempotency-key handling, in Go services.**

The phrase "reliability-eligible" is shorthand. The actual contract with the customer is the three patterns named above and Appendix A defines. Marketing materials and onboarding conversations MUST use the pattern names, not the "reliability-eligible" abstraction, because customer intuition for "reliability" is much wider than v1's coverage and the gap is the most predictable source of buyer disappointment.

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

- **Eligible slice is small and per-customer.** v1 covers three patterns. The eligible slice as a fraction of the customer's reliability backlog varies dramatically by codebase maturity, labeling discipline, and pattern relevance. The Inventory Loop (§3.1, §1.5) measures this *per customer* in the first run and reports it before any tracker writes. Pre-sale projections SHOULD be presented as ranges, not point estimates, with the explicit note that the actual figure is determined by the customer's first inventory run. Internal Revelara dogfood data on polaris/pipeline/crawler is the only empirical anchor for v1; external customers MUST receive their own inventory before yield discussion.
- **Verification is bounded by the synthesized harness.** Orion has no production access (§2.2). The verification claim is "within the synthesized operating envelope, the patched system shows statistically significant improvement on every measured axis with no statistically significant regression." Real production envelopes may differ; every report carries an *envelope-confidence* score and an explicit invitation for the customer to supplement.
- **Three human checkpoints remain.** Cadence trigger, escalation acknowledgement, PR merge. Orion does not auto-merge. v2 may auto-merge low-risk classes against explicit opt-in.
- **Yield is measured first, projected second, contractually backed third.** §1.5 defines a yield model with measurement, projection, and contractual remedy.

### 1.4 Within the Revelara Platform

| Product | Role | Buyer | Pricing tier |
|---|---|---|---|
| **Polaris** | Human-augmenting reliability product. Discovers risks, surfaces controls, guides engineers. | SRE Manager | All tiers |
| **Orion** | Machine-synthesizing reliability product. Synthesizes verified patches; humans merge. | VP Eng / CTO | Architect intelligence multiplier on Growth ($1,999) and Enterprise ($5K+) |

Orion is to software reliability what an EDA synthesis-and-verification toolchain is to hardware: a closed loop that takes a high-level design and emits a verified, production-ready *candidate* implementation. The Conductor 2.0 paradigm applies this idea to hardware engineering agents; Orion applies it to distributed software systems with the explicit acknowledgement that software production is wider and more variable than hardware fabrication, so the loop ends at "verified candidate," not "shipped artifact."

### 1.5 Yield Model and Contractual Remedy

A spec without yield expectations is a vendor promise without numbers. The yield model below is what Orion's onboarding team uses to set customer expectations, what the verification engine reports against, and what the **customer contract redresses against on shortfall**.

For a connected Go service repo with `S` services, `G_total` reliability gaps, and `G_eligible` gaps in the v1 pattern allowlist:

```
expected_PRs_per_run ≈ G_eligible × P_dominance × P_compose × (1 - P_dedup)

where:
  P_dominance, P_compose, P_dedup are calibrated PER REPO from the inventory run
  and recomputed every 90 days using observed per-pattern, per-repo data.
```

Initial calibration values used for pre-sale projection (and immediately overwritten on first real inventory data) are derived from Revelara internal dogfood on polaris/pipeline/crawler and are documented in `internal/inventory/calibration_priors.go` with provenance comments. These are **priors, not promises**.

Onboarding MUST:

1. Run a one-time inventory pass on the connected repo and report `G_total`, `G_eligible`, calibration-recomputed projections, and the projected first-quarter PR count to the customer **before any tracker writes**.
2. Surface the projection in the Polaris Orion runs view as a pinned metric ("Projected: N PRs/quarter; Observed: M to date; calibration version: V").
3. Recompute calibrations on every run using the new observation; report calibration drift in the run report (§21.3).

**Contractual remedy on shortfall.** When observed PR delivery falls below 50% of projection over a rolling 60-day window AND the customer has not introduced a new tracker filter or `// orion:ignore` blanket since projection, Revelara MUST:

- Within 5 business days, run a Revelara-side diagnostic on the customer's inventory and recent runs.
- Within 15 business days, deliver one of: (a) a recalibrated projection with an explanation of the gap, (b) at-no-charge customer-supplied envelope onboarding (§12.3) to lift envelope confidence, (c) at-no-charge harness customization for the customer's stack, or (d) a contract amendment offering downgrade or pro-rated credit for the affected period.

Which of (a)-(d) applies is a Revelara-side decision based on the diagnostic. The remedy is contractual, not aspirational; it is included in the standard Orion service agreement template. The yield model is load-bearing because it is contractually backed.

### 1.6 Honest Renewal Conversation

Spec-internal acknowledgement of the slice-vs-backlog gap. At every renewal, Orion reports four numbers per repo:

1. **In-pattern yield**: PRs delivered for in-allowlist patterns vs. the projection.
2. **Slice growth**: change in `G_eligible / G_total` over the renewal period (is the customer's reliability labeling improving? is Orion's pattern set covering more of the backlog through v1.x additions?).
3. **Post-merge regression rate**: incidents in `post_merge_window` with Refiner relevance ≥ 0.7 over total Orion-merged PRs (is Orion's confidence justified?).
4. **Pattern roadmap delta**: patterns added in the renewal period and patterns shipped on the v1.x roadmap.

These four numbers ARE the renewal conversation. Marketing collateral that omits any of them is contradicting the spec.

### 1.7 Customer Time Investment

The customer's time investment in enabling Orion is not zero. The spec MUST quantify it so onboarding can set expectations and so pricing can be defended.

| Activity | Frequency | Estimated time per occurrence |
|---|---|---|
| Initial inventory review (one time) | Once at onboarding | 1-2 hours per repo |
| Classify "would-have" PRs in shadow mode | Per shadow run (typically weekly) | 5-10 minutes per would-have PR |
| Review draft-mode PRs (against `orion-staging`) | Per draft PR | Standard PR-review time minus implementation time |
| Maintain `// orion:ignore` annotations | Ad-hoc, declining as patterns stabilize | 1-2 minutes per annotation |
| Classify rejection comments per `wrong_pattern`/`wrong_diagnosis` | Per closed-unmerged PR | 1 minute (one-click after closing) |
| Flag `not_caused_by_orion` on incidents within 48h | Per relevant post-merge incident | 1 minute (one-click in Polaris) |
| Upload customer-supplied envelope (optional) | One-time per repo | 2-4 hours of platform-team work |
| Re-enable auto-suppressed pattern | Rare, on operator review | 5 minutes |

**Onboarding MUST publish a customer-time-vs-yield table per repo at inventory time** projecting the customer's expected weekly time investment against the projected weekly PR delivery, so the pricing conversation is grounded. If the projected ratio is unfavorable (customer time exceeds saved engineering time), Orion is the wrong product for that repo and onboarding SHOULD say so.

### 1.8 What Orion Is Not (Competitive Honesty)

Orion is NOT a general-purpose coding agent. v1 differentiation:

| Product | Mode | Scope |
|---|---|---|
| **GitHub Copilot Workspace, Cursor Agents, Devin** | General coding assistant | Any task; broad and shallow per-task |
| **Linters (golangci-lint, etc.)** | Pattern detection, no fix | Fast feedback, no verification |
| **Chaos engineering tools (Gremlin, Litmus)** | Runtime fault injection | Surfaces weakness; does not fix |
| **APM (Datadog, New Relic)** | Production observability | Reports the past |
| **Polaris** | Human-augmenting reliability | Discovery + guidance |
| **Orion (v1)** | Machine-synthesizing reliability for three named patterns in Go, with verification | Narrow, deep, verified |

The v1 differentiation is *verified synthesis on a narrow surface*. A buyer comparing Orion to a general-purpose coding agent should be told: Orion will not write your features; it will not refactor your codebase; it will not improve your tests. It will, for three reliability patterns in Go, produce verified candidate patches that materially reduce the probability of specific failure modes.

If the buyer's reliability problem is dominated by patterns Orion does not cover (e.g., schema migrations, query performance, capacity planning), v1 is not the right product. v1.x and v2 expand the pattern set; v1 is honest about its narrow surface.

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
| `reproduction_bundle_url` | string | Object-storage URL for the harness reproduction bundle (§12.8). |
| `polaris_service_id` | string | Snapshotted Polaris service identifier at PR open time, used by Refiner (§16.6). Resyncable via Polaris webhook. |
| `affected_paths` | string[] | List of file paths touched by patches in this PR; used by Refiner relevance scoring (§16.6). |
| `pattern_set` | string[] | Patterns this PR addresses; used by Refiner pattern-keyword match. |
| `opened_at`, `closed_at`, `merged_at` | timestamp | |
| `post_merge_window_ends_at` | timestamp | Until this time, related incidents in Polaris trigger re-evaluation (§16.6). |

#### 4.1.11 PatternTrustScore

Per-tenant, per-repo, per-pattern trust state, updated by the rejection-learning loop (§16.5) and the post-merge incident hooks (§16.6).

| Field | Type | Notes |
|---|---|---|
| `id` | UUID | |
| `org_id`, `repo_id` | UUID | |
| `pattern` | string | e.g., `timeout_coverage`. |
| `total_proposed`, `total_accepted_by_customer`, `total_rejected_by_customer`, `total_post_merge_incidents_relevant` | int | |
| `current_trust` | float | 0.0-1.0; smoothed exponential moving average. **Initialization rules** (§16.5.1): new patterns introduced in v1.x init at 0.7 (slightly above neutral, below earned trust); newly-connected repos init each pattern at 0.6 (cautious neutral); demotion-then-re-promotion preserves accumulated history. |
| `auto_suppressed` | bool | If true, Orion will not synthesize new patches for this (repo, pattern) until operator re-enables. Re-enable resets `auto_suppressed=false` but does NOT reset `current_trust`. |
| `last_updated_at` | timestamp | |
| `last_observation_window_start_at` | timestamp | EMA observation window anchor; rolls forward as PRs are observed. |

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

Customers do NOT grant Orion full operational scope on day one. Each `(org_id, repo_id)` pair has a `trust_mode` that gates capabilities.

**Honest framing of `shadow` mode**: shadow is a *trial mode*, not an *operating mode*. Default `trial_period_days = 30`; after this expires without promotion, the run schedule pauses with an operator-visible prompt: *"Shadow trial complete. Promote to draft mode to continue, or pause Orion for this repo."* Indefinite shadow is not a viable shipping configuration; the spec acknowledges this and requires the operator to make a decision rather than letting shadow run forever.

| Mode | Auto-files issues? | Opens PRs? | Notifies CODEOWNERS? | Targets default branch's PR base? | Submits evidence to Polaris? |
|---|---|---|---|---|---|
| `shadow` | No (produces *would-file* report) | No (produces *would-PR* report) | No | N/A | No |
| `draft` | No (produces *would-file* report) | Yes, opened as **draft** PRs to a non-protected `orion-staging` base branch Orion creates | No | No (always to `orion-staging`) | No |
| `staging` | Yes, with reduced caps | Yes, ready-for-review PRs to `orion-staging`; customer manually rebases to feature branches | No (notifies a specific reviewer set the customer configures) | No | Yes (preview) |
| `full` | Yes | Yes, ready-for-review PRs to default-branch base | Yes (per repo CODEOWNERS) | Yes | Yes |

**Shadow mode is not silence.** In shadow, Orion runs the full pipeline (inventory, scan, synthesis, verification) and produces a *would-have* report at the end of each run: a list of issues it would have filed, PRs it would have opened, with verification reports attached. The customer reviews these and marks each as `would_have_merged`, `would_have_rejected`, or `unsure`. This is the customer signal the ladder progresses on.

**Promotion criteria** (configurable per `.orion/config.yaml` `trust_ladder.promote_after`, §5.1; defaults favor caution AND require customer signal):

| Promotion | Default criterion | What the customer is signaling |
|---|---|---|
| `shadow → draft` | At least 5 *would-have* PRs marked `would_have_merged` AND zero `would_have_rejected` from `wrong_pattern` or `wrong_diagnosis` cause classes | Synthesis quality is acceptable for surfaced PRs |
| `draft → staging` | At least 5 draft PRs reviewed; merge-equivalence rate (`would_have_merged_if_full` flag set during draft review) ≥ 70% | Draft PRs would have been mergeable in real review |
| `staging → full` | At least 20 staging PRs merged with zero post-merge incidents whose Refiner relevance score (§16.6) exceeds the demotion threshold | Production trust earned, demonstrable on real merges |

Promotion is operator-initiated (a one-click action) once criteria are met; Orion never auto-promotes. Promotion is logged and reversible.

**Demotion is evidence-bounded, not event-triggered.** A `critical` Revelara-side escalation (safety violation) drops trust mode by one level immediately. A post-merge incident triggers Refiner re-evaluation but does NOT auto-demote unless ALL of:

1. The Refiner relevance score (§16.6) exceeds `demotion_relevance_threshold` (default 0.7).
2. The customer has not provided a counter-signal within 48h (a `not_caused_by_orion` flag on the incident).
3. The Pattern's per-repo trust score (§4.1.11) crosses the auto-suppress threshold as a result.

Operator-issued demotion is always allowed without thresholds. Re-promotion follows the same criteria as initial promotion. PatternTrustScores are NOT reset on demotion (history is preserved); only the `trust_mode` changes.

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

### 8.3 Unified Backlog, Semantic Dedup, and Annotation Scoping

The Conductor merges issues from all bindings of a connected repo into a single in-memory backlog.

Deduplication operates at three levels:

1. **Polaris-risk dedup**: issues from different trackers sharing the same `polaris_risk_id` are merged (one canonical, others marked superseded).
2. **Semantic dedup against existing human-filed issues**: each NormalizedIssue computes a `dedup_signature = sha256(pattern || normalized_call_site)` where `normalized_call_site` is the canonical AST path of the affected call (resilient to refactor and file rename). Before filing a new issue (§8.7), Orion MUST check for an existing open issue with the same dedup signature in any binding; if found, Orion comments on the existing issue rather than filing a new one.
3. **Annotation-based suppression**: a code site annotated `// orion:ignore` MUST NOT be re-detected, re-filed, or re-patched. Suppression is enforced at synthesis time (no candidate generated for suppressed sites).

**Annotation scope rules** (v1):

| Annotation form | Scope |
|---|---|
| `// orion:ignore <pattern> reason="..."` on the line before a statement | The next single statement (call, assignment, declaration). Per-pattern. |
| `// orion:ignore <pattern> file=true reason="..."` at file head | Entire file, for that pattern only. |
| `// orion:ignore-all reason="..."` at file head | Entire file, all patterns. Use sparingly; surfaced in run report as a "fully suppressed" file. |

**Pattern additions** (v1.x): when a new pattern (e.g., `rate_limit_inference`) is added, existing `// orion:ignore <pattern>` annotations are unaffected (they specify the pattern they cover). Sites that need suppression for the new pattern require new annotations. `// orion:ignore-all` covers new patterns automatically.

The annotation grammar is parsed at scan time; malformed annotations emit a warning in the run report (and do NOT suppress; better to over-detect than to silently honor a mistyped suppression).

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

### 9.2 Greenfield (v2+, Honest Non-Commitment)

**Honest scoping**: greenfield is NOT in v1 scope. It is also NOT a guaranteed extension of v1's architecture. The v2 greenfield design will require its own L3 Synthesis pipeline AND will likely require changes to L2 (run state machine adds a `mode` discriminator; worker lifecycle branches on mode) and L4 (workers for greenfield read design docs, not code).

The v1 spec MAKES NO COMMITMENT that v2 greenfield will plug in without architectural change. Marketing the v1 architecture as "greenfield-ready" or "greenfield-friendly" is misleading and SHOULD NOT appear in customer-facing materials.

What v1 DOES preserve for v2's benefit:

- The Conductor's per-tenant scope (§14.1) is mode-agnostic and will accommodate greenfield runs alongside brownfield runs without re-architecting.
- The tracker adapters (§8) are mode-agnostic and will accept greenfield issue types when v2 defines them.
- The trust ladder (§6.4) is mode-agnostic and will gate greenfield outputs by the same mechanism.
- Observability (L6) and audit logging (§19.6) are mode-agnostic.

L3 (Synthesis) and the run state machine WILL change for v2 greenfield. The v1 implementation does not pre-build for that change.

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

### 12.6 Verification with Adaptive Statistical Confidence (`internal/verify`, `internal/stats`)

**The strict-dominance claim from draft 1 was unrealistic** (real benchmarks have variance). **The fixed-trial statistical claim from draft 2 was unsound** (n=5 t-tests have unreliable p-values). Draft 3 uses **adaptive trial counts** with a sequential-analysis-aware termination rule.

Inputs: CandidatePatch, baseline metrics, Harness, per-repo `verification_thoroughness` (default `standard`; values: `fast`, `standard`, `thorough`).
Outputs: Verdict, metrics with confidence intervals, trial count consumed.

The verifier:

1. Applies the patch.
2. Verifies the build.
3. Begins interleaved trials (one baseline trial, then one patched trial; alternation controls cluster-level noise).
4. After each pair, computes confidence intervals per axis at `confidence_level` (default 0.95) using a bias-corrected accelerated bootstrap (BCa), which is robust at small samples and does not assume normality.
5. Terminates as soon as ANY of:
   - All axes show CI bounds that decisively favor patched (strict statistical dominance) AND CI widths are below `decisive_width` → `accepted`.
   - Any axis shows CI bounds that decisively favor baseline → `rejected_regression`.
   - No axis CI bound favors patched after `min_paired_trials` (default 8) → `rejected_no_dominance`.
   - Trial count reaches `max_paired_trials` for the thoroughness level (default `fast=12`, `standard=24`, `thorough=48`) without decision → `rejected_low_confidence` with explicit recommendation to lift thoroughness or supply envelope inputs (§12.3).
6. Emits one of: `accepted`, `rejected_no_dominance`, `rejected_regression`, `rejected_low_confidence`, with the trial count consumed and the per-axis CI evolution recorded.

Sequential-analysis correction (Pocock or O'Brien-Fleming boundaries) is applied to control family-wise Type I error across the early-termination checks. Per-axis confidence levels are Bonferroni-adjusted for the number of axes evaluated.

**Cost-vs-fidelity tradeoff is explicit.** A repo with very noisy benchmarks may have a high `rejected_low_confidence` rate at `standard` thoroughness; the customer can opt into `thorough` (4× harness compute, recorded in token/CPU accounting) or into customer-supplied envelope (§12.3) to lift signal. Pricing surfaces the cost difference.

The verification report includes per-axis: baseline mean, patched mean, BCa CI bounds, trial count, decision, and an "evidence quality" score derived from CI width and trial count.

### 12.7 Optimizer Composition

Accepted patches are composed greedily, with re-verification at each composition step (interactions matter; e.g., timeout + retry without backoff produces retry storms).

The composer terminates when no candidate improves the composition under statistical confidence.

**Rejected-candidate visibility (the long-tail problem from Round 1 §1C #11)**: every rejected candidate is recorded with its rejection class. The run report (§21.3) includes a "Rejected Candidates" section with counts per class. If the per-pattern rejection rate exceeds a threshold (default 60%), an `info` escalation is filed suggesting customer review of pattern fitness for this repo.

### 12.8 Operating Envelope Reporting and Reproduction Bundle

The verification report MUST include the operating envelope. Additionally, every run produces a **reproduction bundle** archived to object storage with explicit supported-runtime envelope:

- **Supported runtime** (v1): x86_64 Linux with Docker 24+ and 16+ GB RAM. The bundle is guaranteed to reproduce on this envelope (within statistical noise).
- **Best-effort runtimes**: ARM64 Linux (Graviton, M-series Macs via emulation), podman-instead-of-Docker, alternative Linux distros. Reproduction MAY differ; the bundle ships a `compatibility_check.sh` that validates the runtime and warns on incompatibilities.
- **Not supported**: Windows hosts, air-gapped networks (the bundle pulls container images by SHA from public registries; air-gapped customers MUST mirror images first), any runtime without Linux containers.

Bundle contents:

- A docker-compose or testcontainer manifest.
- The toxiproxy configuration.
- The pinned commit SHA.
- The harness seed.
- The LLM model identifier and provider seed (where available).
- The container image SHAs for harness components.
- A README describing the supported runtime envelope and how to run the bundle.
- The `compatibility_check.sh` script.

The bundle URL is included in the PR body. **Honest caveats**: LLM-provider nondeterminism and CPU contention may cause minor metric variation; reproduction is "behaviorally equivalent within reported CI bounds on the supported runtime," not "bit-identical."

For air-gapped or alternative-runtime customers, Revelara onboarding offers (at no additional charge during onboarding) an air-gapped reproduction bundle variant with bundled images. v1 makes no commitment to support this for every release; it is opt-in.

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

## 14. Orchestration Architecture (Inspired by Symphony and Gastown, but Orion's Own)

This section was titled "Symphony × Gastown Synthesis" in earlier drafts. After three rounds of review, the honest description is that Orion's architecture takes inspiration from Symphony's spec discipline and Gastown's multi-worker primitives, but ships a third architecture that neither author would call a synthesis. Symphony's in-memory single-orchestrator simplicity is gone; Gastown's three-tier human escalation chain (deacon/mayor/overseer) is replaced by a code-decided classifier table. Naming this honestly.

### 14.1 The Conductor and Its Per-Tenant Scope

The Conductor is a single logical authority for orchestration **per tenant**. In v1 the cluster runs N Conductor replicas; leader election (§14.2) is keyed by `(deployment_id, tenant_id)`, so each tenant has exactly one leader at a time and many tenants can be served by the same fleet without contention.

Replica count is sized as `ceil(tenants_per_replica / target_load) + standby` per cluster. v1 supports up to 50 tenants per replica with hot-standbys; beyond that, the cluster scales horizontally by adding replicas (each replica handles a disjoint subset of tenants, claimed by leader election). The advisory-lock approach is per-tenant; a single replica may hold leader locks for multiple tenants simultaneously.

For a single tenant with 30+ repos, the per-tenant leader serializes orchestration across all those repos. Each repo runs its five loops (§3.1) under that single leader's coordination; the leader interleaves work across repos using the per-repo concurrency caps and the global `max_concurrent_workers_per_tenant` (default 12). This bounds the per-tenant blast radius of a leader stall while keeping cross-repo dependency awareness (e.g., shared library pattern updates).

**Vertical scaling ceiling (per tenant)**:

| Repos in tenant | Standard config | Notes |
|---|---|---|
| ≤ 20 | Supported, default config | Standard onboarding |
| 21-100 | Supported with engagement (raised concurrency caps, dedicated Conductor pinning) | Customer success qualifies; opt-in tuning |
| > 100 | Architectural conversation required before onboarding | A single per-tenant leader becomes the bottleneck; v2 may shard a tenant across multiple leaders |

These numbers are honest v1 limits. Sales MUST honor them; a customer with 200 repos cannot be onboarded without engineering review.

### 14.2 Leader Election Substrate (Per-Tenant)

The leader-election substrate is a **PostgreSQL advisory lock with fencing token, scoped per tenant**:

1. Each Conductor replica attempts `pg_try_advisory_lock(hash(deployment_id, tenant_id))` for tenants it is permitted to serve.
2. The successful replica reads its `fencing_token` from the `orion_leadership` table (composite key on `(tenant_id)`) and increments it on acquisition.
3. Every state mutation transaction for that tenant includes `WHERE fencing_token = $current_token AND tenant_id = $tenant` as a guard.
4. A former leader whose token is stale will fail every transaction; in-flight mutations roll back.

The advisory lock TTL is `leader_lease_seconds` (default 30); a lease holder MUST renew within `leader_renew_seconds` (default 10). A replica may hold leader leases for many tenants concurrently.

Per-tenant scoping means: one tenant's leader thrash never affects another tenant; one Conductor replica failure causes leader handover for the tenants it served, not for all tenants in the deployment.

### 14.3 What the Conductor Does

1. Reads the unified backlog.
2. Applies eligibility, dedup, and priority.
3. Issues claim transactions including the cap check and worker spawn intent (§7.4 #1, §7.4 #6).
4. Spawns worker pods via the `orion-worker-controller` (idempotent on workspace key).
5. Routes escalations.
6. Drives runs from `created` to terminal state.
7. Emits structured events.

The Conductor does NOT directly observe worker liveness. That is the Lookout's job (§14.4).

### 14.4 The Lookout (Per-Run Worker Reconciler) and Its Supervision

Drawn from Gastown's witness pattern but adapted for SaaS: each active run has a dedicated `Lookout` process (a separate pod, NOT the Conductor) that:

1. Watches its assigned run's worker pods at `lookout_tick` (default 30s) frequency.
2. Detects stalled workers (`now - last_event_at > stall_timeout`) and instructs the K8s controller to terminate them.
3. Detects dead pods (eviction, OOM) and reports failure.
4. Reads tracker state per active issue and compares to expected; if drift detected (issue closed externally, label changed), instructs the worker to terminate.
5. Forwards summarized observations to the Conductor.
6. Emits a heartbeat record to the database every `lookout_heartbeat` interval (default 60s).

**Lookout supervision (witness-of-the-witness).** The Conductor's tick (§23.1) includes a `verify_lookout_liveness(run)` step that reads the Lookout heartbeat for each active run. If a Lookout heartbeat is older than `lookout_dead_threshold` (default 3× heartbeat interval), the Conductor:

1. Marks the prior Lookout as `presumed_dead`.
2. Spawns a replacement Lookout pod for the run (idempotent on `(run_id, lookout_generation)`; the new generation increments).
3. The replacement Lookout reads the run's WorkerSession state from the DB and resumes observation; no in-flight worker is lost.

Lookouts are stateless. Their durable state is the WorkerSession rows the workers write directly. Lookout death therefore costs (lookout_dead_threshold) of observation latency, not work.

The Conductor itself is leader-elected with fencing (§14.2); a Conductor death is recovered by the standby leader. The chain is: workers → Lookouts → Conductor → leader-election quorum, and each layer has explicit liveness mechanism distinct from the layer above.

### 14.5 The Refiner (Post-Merge Composition Tracker)

Drawn from Gastown's refinery pattern but adapted for the no-auto-merge constraint. The Refiner does NOT auto-merge. Instead:

1. After Orion's PR is merged by a customer, the Refiner records the merged SHA against the run.
2. The Refiner watches Polaris's incidents API (`GET /api/v1/incidents?service=...&since=merged_at`) for `post_merge_window` (default 30 days).
3. If an incident in the same service is filed within that window, the Refiner flags the run for re-evaluation, augments the harness with the new failure mode (where extractable from the incident report), and re-verifies any other PRs Orion shipped sharing patches from the same synthesizer (§16.6).
4. Customers can disable the post-merge watch per repo or per service.

This is Gastown's refinery's contribution at the right semantic level: not "merging," but "isolating regressions and feeding back to verification." It works without requiring auto-merge or binary-bisect of customer trunk.

### 14.6 Snapshot Discipline and the Two Snapshot Systems

Orion has **two distinct snapshot systems** that must reconcile cleanly:

1. **Per-run pinned snapshot** (immutable for the run). Captured at run-start: ArchitecturalModel, ConstraintSurface, controls catalog, knowledge enrichment results. Workers MUST read the per-run snapshot, NOT live Polaris.
2. **Tenant-wide cached snapshot** (refreshable). Updated whenever Polaris is reachable; consulted as the source for run-start pinning.

**Reconciliation rules**:

- A new run's per-run snapshot is built from the **freshest available source**: live Polaris if reachable; tenant cache if Polaris is unreachable but cache age < `polaris_disconnected_grace` (default 7d); otherwise the run is rejected with `config_invalid` (do not synthesize against expired data).
- A run that started during the Polaris-disconnected window keeps its per-run snapshot for the entire run, even if Polaris reconnects mid-run. Snapshot discipline is preserved.
- The tenant cache is refreshed first-thing on Polaris reconnect, but in-flight runs continue with their already-pinned snapshots.
- If a run was created in `degraded_input` state (snapshot from cache > 24h old at pin time), the run report MUST surface this prominently and PRs opened from this run MUST mention it in their body.
- A NEW Polaris control added during a worker's run is NOT visible to the worker. Trade-off: snapshot integrity beats freshness during a worker's session. The next run picks it up.

This closes both the Pattern C staleness window (within a worker session) AND the multi-snapshot collision (across systems).

### 14.7 Communication Substrate

1. **The database** for all durable state.
2. **A pub/sub channel** (NATS or Redis Streams) for ephemeral coordination (worker spawn notifications, completion notifications, Lookout reports).

### 14.8 Escalation Routing Matrix and Code-Level Classifier

Escalations are routed by class to the right responder. **No escalation pages the wrong party.**

| Class | Examples | Routes to | SLA |
|---|---|---|---|
| `customer:patch_review` | PR rejected with comment; pattern-fit review needed | Customer's configured Slack channel (notification only); Polaris escalations view (actionable ack) | None (customer pace) |
| `customer:eligibility_question` | Repo has 0 eligible issues; envelope confidence below floor | Customer's configured Slack channel; Polaris escalations view | None |
| `customer:safety_quarantine` | Pattern auto-suppressed; customer must re-enable | Customer's configured Slack channel; Polaris escalations view | None |
| `revelara:harness_failure` | Sandbox provisioning failed; harness materialization error | Revelara on-call PagerDuty | 30 min ack |
| `revelara:platform_critical` | Agent attempted out-of-workspace write; safety violation | Revelara on-call PagerDuty | 5 min ack |
| `revelara:integration_break` | Polaris callback exhausted; tracker adapter health-check failing | Revelara on-call PagerDuty | 30 min ack |

**Classification is deterministic and code-decided, never agent-decided.** The classifier is a switch on the structured failure type that emerges from the runtime. Definitive table:

| Failure source (runtime type) | Class |
|---|---|
| `sandbox.ProvisionError`, `harness.MaterializationError`, `harness.CompiledHarnessFailure` | `revelara:harness_failure` |
| `safety.OutOfWorkspaceWrite`, `safety.UnauthorizedToolCall`, `safety.NamespaceEgressViolation`, `safety.ScopeRequest{decision: denied_out_of_scope}` (any, even from agent) | `revelara:platform_critical` |
| `polaris.CallbackExhausted`, `tracker.HealthCheckFailing`, `llm.QuotaExceeded`, `infra.LeaderElectionThrash` | `revelara:integration_break` |
| `verify.RejectedLowConfidence` (post-threshold), `verify.NoCandidatesAccepted` (post-threshold per repo), `pr.ReviewerCommentRequiresReply` | `customer:patch_review` |
| `inventory.ZeroEligibleGaps`, `architect.EnvelopeConfidenceBelowFloor`, `tracker.NoLabeledIssuesPolled` | `customer:eligibility_question` |
| `pattern.AutoSuppressTriggered`, `trust.DemotionRequested` | `customer:safety_quarantine` |

Implementation: `internal/escalation/classifier.go` is a single function with one `switch` statement. There is no fallback to "the LLM decides"; an unrecognized failure type is a programming error and emits `revelara:platform_critical` with the unrecognized type as evidence (so the Revelara team learns about classifier gaps immediately, while the customer is never paged for ambiguity).

Customer-side escalations NEVER page Revelara. Revelara-side escalations NEVER page the customer (the customer sees them in the Orion runs view as "Revelara investigating"; Slack messages do NOT go to the customer).

Stale escalations re-escalate within their class only.

**Slack v1 is notification-only.** The Slack integration delivers a message linking to the Polaris escalations view. Acknowledgement happens in Polaris (UI or API); Slack carries no actionable buttons in v1. v2+ may add a Slack-bot dependency, which would require a corresponding entry in §3.3 External Dependencies. v1 has no such dependency.

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

### 15.3.1 Empty-Backlog Behavior (Quiescent Mode)

When `G_eligible` reaches zero (or close to zero with all eligible issues `released` or `escalated`) for a connected repo, the run transitions to `quiescent` rather than `completed` for the next cycle. Quiescent runs:

1. Reduce harness compute spend (no synthesis attempted).
2. Surface "you are caught up" status in the Polaris Orion runs view with positive framing.
3. Continue running scan-loop on cadence to detect newly-introduced gaps.
4. Recommend pattern-set expansion: which v1.x roadmap patterns would surface non-zero `G_eligible` for this repo?
5. Reduce per-tenant orchestration cost while preserving readiness for new gaps.

A quiescent repo is a *success state*, not a failure state. The customer-facing surface MUST frame it accordingly. Renewal conversations for quiescent repos focus on roadmap pattern delivery (§1.6 #4), not yield.

### 15.4 Self-Referential-Loop Guard

Round 1 noted the risk that Orion files issues for the same patterns Orion remediates, creating a closed loop with itself rather than with the customer's actual backlog.

Mitigation: every run report (§21.3) breaks down issues processed by **provenance**:

- Customer-filed issues processed.
- Orion-filed issues processed.
- Polaris-prior risks processed (existing risks in Polaris regardless of tracker filing).

**Warning threshold**: the report flags `self_referential_loop_warning` when `Orion-filed > 3 × Customer-filed` for 3 consecutive runs over a window of at least 30 days. The warning is informational, not blocking. It recommends one of:

1. Review the pattern allowlist (perhaps Orion is filing for patterns the customer doesn't prioritize).
2. Improve labeling discipline so reliability work in the backlog is detected by Orion's tracker filters (§8.5).
3. Acknowledge that the customer's reliability backlog is genuinely sparse outside Orion's scope; this is a healthy state and the warning may be permanently dismissed.

For new customers (first 3 runs), the warning is suppressed because the customer-filed count is naturally near-zero before workflow integration. The warning surfaces only after the customer has had reasonable opportunity to label their backlog.

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

### 16.4.1 Architecturally-Coupled Patterns and Concurrent PR Ordering

Two cases the spec MUST address explicitly:

**Architecturally-coupled patterns** (one shared library, N callers). Adding a timeout to a shared `httpClient` used by 50 call sites would file 50 issues and open 50 PRs, denial-of-servicing the customer's reviewers. v1 rule:

1. The architectural inferer (§12.1) detects shared-library patterns (a single declaration consumed by ≥ `shared_library_caller_threshold` callers, default 5).
2. For shared-library patterns, Orion files a single *consolidated* issue covering the declaration plus a list of affected call sites.
3. The patch synthesizer emits ONE patch that fixes the shared declaration (e.g., introducing a default timeout on the shared client).
4. The verification harness exercises a representative sample of caller sites, NOT all of them.
5. The PR body lists every call site affected so the reviewer can audit blast radius before merge.

**Concurrent PRs against the same code path, merged out of order**. Two Orion PRs for two different issues touch the same file; the customer merges the second-opened first. v1 rule:

1. Orion tracks `affected_paths` per PR (§4.1.10).
2. Before opening a new PR, the worker checks the Conductor for any open Orion PR with overlapping `affected_paths` for the same `repo_id`.
3. If overlap detected, the new PR's body includes a "depends-on" reference and the new PR is opened as a draft pinned to the prior PR's branch (NOT default branch).
4. The customer is alerted that the PRs have an order constraint; merging the dependent PR first will require Orion to rebase.
5. On detection of out-of-order merge (the dependent's base is no longer reachable), Orion auto-rebases or marks the orphaned PR `superseded_by_merge_conflict` for human review.

These two rules eliminate the most common architectural-coupling failures and preserve customer reviewer sanity.

### 16.5 Rejection Learning (v1, Real)

Each PR closure (merged or unmerged) updates the per-pattern, per-repo PatternTrustScore via exponential moving average. The Refiner extends this with post-merge incident signals scored by relevance (§16.6).

#### 16.5.1 Trust Score Update Rules

| Event | EMA contribution to `current_trust` (weight × signal) |
|---|---|
| PR merged with no post-merge incident triggered within `post_merge_window` | +1.0 × 1.0 |
| PR closed unmerged with `would_have_merged_if_full` flag (in draft mode) | +1.0 × 0.7 |
| PR closed unmerged with rejection comment classified as `wrong_pattern` or `wrong_diagnosis` | +1.0 × -0.5 |
| PR closed unmerged without classified rejection signal | +1.0 × -0.2 |
| Post-merge incident, relevance ≥ 0.7 | +5.0 × -1.0 |
| Post-merge incident, 0.4 ≤ relevance < 0.7 | +2.0 × -0.4 |
| Post-merge incident, relevance < 0.4, customer flagged `not_caused_by_orion` | 0 |

EMA smoothing factor `alpha = 0.2` per observation; rolling window is the most recent 10 observations.

#### 16.5.2 Auto-Suppression

If the score drops below `pattern_auto_suppress_threshold` (default 0.4) over the rolling 10 observations, the pattern is auto-suppressed for that repo: §8.4 rule 7 makes ineligibility automatic.

Auto-suppression files a `customer:safety_quarantine` escalation with:

- The rejection signal evidence (which PRs, which incidents, which relevance scores).
- The recommended remediations (review pattern fitness, supply envelope, narrow `synthesis.patterns` allowlist).
- A one-click re-enable action (operator-only).

Re-enablement clears `auto_suppressed=false` but preserves `current_trust` and EMA history; if the underlying signal does not improve, auto-suppression will trigger again on the next observation. This is intentional: re-enable is "I'm overriding for now," not "the trust is restored."

#### 16.5.3 Initialization

- New patterns shipped in v1.x: `current_trust = 0.7`, no auto-suppression for first 3 observations (allows signal to accumulate).
- Newly-connected repos: each pattern in the allowlist initializes at `current_trust = 0.6`.
- Existing repos when v1.x adds a pattern: that new pattern initializes at `0.7` for that repo, regardless of other patterns' history.
- Demotion-then-re-promotion: `current_trust` and EMA history are preserved across trust-mode changes.

### 16.5.4 Mid-Session Auto-Suppression Reconciliation

A worker mid-session may be operating on a pattern that the Refiner just auto-suppressed (e.g., a high-relevance post-merge incident landed in the same tenant for the same pattern on a different repo). The reconciliation rule:

1. The worker's current synthesis cycle for the *currently-being-verified* candidate completes; the run snapshot it loaded (§14.6) governs that cycle.
2. The Lookout (§14.4) detects the pattern auto-suppression for the worker's repo + pattern via the per-tick PatternTrustScore check.
3. The worker is signaled to terminate after the current verification cycle, NOT in the middle of harness execution (which would orphan harness namespaces).
4. The worker reports `superseded_by_auto_suppression` and exits cleanly.
5. The issue is marked `released` with reason `auto_suppressed_during_run`; no PR opens for it.

This resolves the snapshot-vs-live tension Round 3 §11 identified: snapshot integrity is preserved within a verification cycle, but eligibility checks at cycle boundaries honor the new auto-suppression.

### 16.6 Post-Merge Incident Hooks with Relevance Scoring

The Refiner (§14.5) watches Polaris incidents for `post_merge_window` (default 30 days) per merged Orion PR. **Same-service is not the same as caused-by-Orion.** The Refiner computes a **relevance score** before taking destructive action:

```
relevance = w1 * path_overlap(incident.affected_paths, pr.patches.target_paths)
          + w2 * temporal_proximity(incident.detected_at, pr.merged_at)
          + w3 * pattern_keyword_match(incident.report_text, pr.patches.pattern)
          + w4 * customer_link_signal(incident.linked_to_pr_id == pr.id ? 1 : 0)

with default weights w1=0.35, w2=0.20, w3=0.20, w4=0.25
```

The four signals:

- **Path overlap**: did the incident's affected files include any file Orion patched? Highest-signal heuristic.
- **Temporal proximity**: is the incident within `tight_post_merge_window` (default 48h) of merge? Recent changes are more often causative; older incidents are usually unrelated drift.
- **Pattern keyword match**: do the incident report's first 500 tokens contain keywords associated with Orion's patched pattern (e.g., "timeout", "retry", "idempotency")? Crude but cheap.
- **Customer link**: did the customer manually link the incident to the Orion PR via Polaris UI or API? Highest-trust signal; overrides others when present.

**Action thresholds**:

| Relevance score | Action |
|---|---|
| ≥ 0.7 | Run is re-opened in `re_evaluation_queued`; harness augmented; PatternTrustScore decremented heavily; `customer:patch_review` escalation filed. Demotion threshold (§6.4) may trigger. |
| 0.4 ≤ score < 0.7 | Informational note attached to the run; PatternTrustScore decremented modestly; NO escalation, NO demotion; surfaces in run report. |
| < 0.4 | No action. Logged for future calibration. |

The customer always retains override: a `not_caused_by_orion` flag on the incident in Polaris (one-click action) immediately pulls the relevance score to 0 and reverses any auto-action taken. The customer also has `caused_by_orion` to force the highest-action path.

If re-verification under augmented harness shows regression, Orion files a follow-up PR that backs out the offending patch with a verification report explaining the regression.

This implements the Round 2 §8 fix: same-service is not enough, and false-positive demotions are kept out of the customer's experience.

**Service-taxonomy mapping.** Each ConnectedRepo (§4.1.1) carries a `polaris_service_id` field set at install time and resyncable. The Refiner uses this to query Polaris for incidents `?service=<polaris_service_id>`. If Polaris's taxonomy changes (service rename, split, merge), Polaris emits a `service_taxonomy_change` webhook; Orion updates the `polaris_service_id` and notifies the operator. If a Refiner query returns "service not found" three times consecutively, the Refiner emits a `revelara:integration_break` escalation rather than silently dropping the post-merge watch.

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
| `orion_lookout_heartbeat_age_seconds` | gauge, by `(run_id, lookout_generation)` | New in draft 3 (Lookout liveness) |
| `orion_refiner_relevance_score` | histogram, by `(repo, pattern)` | New in draft 3 (post-merge relevance distribution) |
| `orion_verification_trial_count` | histogram, by `(repo, pattern, thoroughness)` | New in draft 3 (adaptive verification cost) |
| `orion_escalation_classification_unrecognized_total` | counter | New in draft 3 (classifier-gap detection) |
| `orion_per_tenant_leader_handovers_total` | counter, by `tenant_id` | New in draft 3 |

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
| Centralized infrastructure precedes orchestration | §3.3, §10.2 (per-tenant repo cache), §14.2 (per-tenant PG advisory lock with fencing) |
| Adversarial review proportional to work | §15 (scan-loop opt-in cadence; auto-file caps; trust ladder) |
| Default to single-developer / sequential flow; parallelism opt-in | §20 #8 (concurrency cap; trust-ladder gates) |
| Distinguish design-time from runtime | §11 (workers are service code; agents turn-scoped); §20 (structural enforcement, not lessons table, is the contract) |
| State-aware automation | §16.4 (PR reconciliation polls live state); §10.3 (sandbox isolation); §14.6 (snapshot discipline reconciled across two systems) |
| Inference-without-state-verification is most autonomous-failure shape | §11.4 (Lookout re-checks); §20 #2 (tool whitelist absence); §16.6 (Refiner relevance scoring before destructive action) |
| Confidence without grounding cascades errors | §12.6 (adaptive CI with explicit cost-vs-fidelity tradeoff); §20 #3 |
| Static-prompt / dynamic-runtime asymmetry breaks long-running loops | §5.3, §14.6 (two snapshot systems reconciled) |
| Autonomous merge fails | §20 #6, §16, trust ladder |
| Infrastructure friction swallows orchestration gains | §20 #7, §10.2 |
| Reliability-by-construction inverts the SRE model | The entire spec; this is Orion's premise |
| Yield must be modeled and contractually backed, not promised | §1.5 (Yield Model with contractual remedy); §1.6 (renewal honesty) |
| Trust must be staged on customer signal, not granted on time | §6.4 (Trust Ladder with would-have signal in shadow) |
| Envelope mismatch is the foreseeable customer failure | §1.3, §12.1 (envelope_confidence), §12.3 (customer-supplied envelope) |
| Post-merge regressions are the renewal-killer if treated naively | §16.6 (relevance scoring; customer override; demotion is evidence-bounded) |
| Witnesses need witnesses | §14.4 (Conductor monitors Lookout heartbeat; replacement is automatic) |
| Classifier rules belong in code, not in the agent | §14.8 (deterministic classifier table; unrecognized failures escalate to Revelara) |
| Aspirational architectural extensibility is honest only when verifiable | §9.2 (greenfield non-commitment) |

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
