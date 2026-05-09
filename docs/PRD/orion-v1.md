---
title: Orion v1 — Autonomous Reliability Synthesis
status: draft
authors: Joseph Bironas
created: 2026-05-07
last_updated: 2026-05-07
---

# Orion v1 PRD: Autonomous Reliability Synthesis

## Problem Statement

Engineering organizations operating non-trivial systems accumulate reliability and performance debt faster than they pay it down. Missing timeouts, naive retries, unbounded resources, idempotency gaps, single points of failure, anti-patterns: every team knows these exist in their codebase, and almost no team has the engineering capacity to address them systematically. Reliability work loses to feature work in every sprint planning meeting that doesn't immediately follow an incident.

Existing tooling does not solve this:

- **Linters and static analysis** flag patterns but produce too many false positives and offer no verification that proposed fixes improve the system.
- **Chaos engineering tools** (Gremlin, Litmus, ChaosMesh) require running systems and human-driven experiments. They surface weaknesses but do not fix them.
- **AI code assistants** (Copilot, Cursor) propose changes but cannot reason about behavior under load, fault, or partial-failure conditions.
- **APM tools** (Datadog, New Relic) report what already broke, after it broke.
- **Polaris itself**, today, is a human-in-the-loop discovery and guidance tool: developers initiate scans, review risks, and apply fixes one at a time. Even with the existing `--auto` mode, fixes ship without independent verification that the system is meaningfully better afterwards.

The org has a *systemic* reliability/performance debt problem, and they cannot get out of it without an order-of-magnitude productivity multiplier on reliability work.

## Solution

**Orion takes a codebase as input and produces a provably better codebase as output.**

The customer grants Orion read access to a Git repository via a GitHub App. Orion analyzes the codebase, infers an architectural model and constraint surface, synthesizes a generative simulation harness, proposes patches across the constraint surface, verifies each patch against the harness, and delivers a pull request consisting of patches that strictly dominate the baseline along every reliability and performance axis measured. The customer reviews the PR (commits + verification report) and merges. Orion never writes to main. Orion never touches production.

Conceptually, Orion is to software reliability what an EDA synthesis-and-verification toolchain is to hardware: a closed loop that takes a high-level design and emits a verified, production-ready implementation. The Conductor 2.0 paper applies this idea to hardware engineering agents; Orion applies it to distributed software systems.

Within the Revelara platform, Orion is the **autonomous closed-loop layer**:

- **Polaris** is the human-augmenting reliability product. It discovers risks, surfaces controls, and guides engineers and SREs through risk remediation. Per-seat pricing, SRE-Manager buyer.
- **Orion** is the machine-acting reliability product. It removes risks autonomously and ships verified improvements. Per-repo pricing in the Architect intelligence tier on top of Growth ($1,999) and Enterprise ($5K+) platform fees, VP Eng / CTO buyer.

Orion v1 targets three foundational patterns in Go codebases — **timeout coverage, retry hygiene, and idempotency-key insertion** — delivered through a SaaS service with GitHub App access. Internal Revelara codebases (polaris, pipeline, crawler) are the v1 dogfood targets. External design partners are deferred until dogfooding closes the loop end-to-end.

## User Stories

1. As a VP of Engineering, I want to point Orion at a service repo and receive a pull request with verified reliability improvements, so that my team's reliability debt decreases without competing for sprint capacity.
2. As a VP of Engineering, I want each Orion-produced PR to include a verification report, so that I can audit what changed, why it is better, and what envelope the proof holds in.
3. As a senior engineer, I want to review an Orion PR using the same workflow as any other PR (diff + tests + CI), so that I do not have to learn a new review surface.
4. As a senior engineer, I want to reject specific Orion commits without rejecting the whole PR, so that I can take the wins I agree with and push back on the rest.
5. As an SRE manager, I want Orion's output linked to the Polaris risk register, so that risks identified during human review get autonomously remediated when Orion's confidence is high.
6. As an SRE manager, I want to mark certain risks as "Orion-ineligible," so that sensitive areas (auth, billing, payments) stay human-driven.
7. As a developer, I want Orion to skip files matching project conventions (gitignore-style excludes), so that generated code, vendored dependencies, and experimental code are excluded from synthesis.
8. As a developer, I want Orion's commits to follow our conventional-commit style, so that they integrate cleanly with our existing tooling.
9. As an internal Revelara engineer, I want to run Orion against polaris/pipeline/crawler from a single CLI command, so that we can iterate quickly during v1 development.
10. As an internal Revelara engineer, I want Orion's verification report to be reproducible (same inputs, same harness, same deltas), so that I can debug regressions in the synthesizer.
11. As a CISO, I want guarantees that Orion never exfiltrates customer code beyond the verification harness, so that I can approve the GitHub App scope.
12. As a CISO, I want guarantees that Orion never writes to production branches or runs against production systems, so that the safety boundary is enforced architecturally.
13. As a customer success engineer, I want to see which control categories Orion successfully addressed in a customer's run, so that I can guide their reliability roadmap.
14. As a finance/ops analyst, I want each Orion run to log harness compute consumed, so that we can attribute usage and price the service correctly.
15. As a Polaris user, I want to see in the Remediations view which risks have been claimed by Orion for autonomous remediation, so that I do not duplicate work.
16. As a developer, I want Orion to detect when its proposed patches interact negatively (e.g., timeout + retry resulting in retry storms), so that the optimizer rejects unsafe combinations before I see them.
17. As a developer, I want Orion to publish the synthesized workload and harness configuration as artifacts, so that I can replay them in my own CI and verify the claimed improvements.
18. As an SRE manager, I want a fitness-score trend per service across multiple Orion runs, so that I can track reliability trajectory over time.
19. As a developer, I want Orion to separate high-confidence patches from speculative ones, so that I can configure my org's risk tolerance.
20. As a developer, I want Orion's verification report to clearly state the operating envelope (workload type, fault types, duration), so that I understand what has and has not been verified.
21. As a customer, I want a clear "no improvements found" signal when Orion finds nothing to do, so that empty runs are not ambiguous.
22. As a customer, I want my codebase to be isolated from every other customer's codebase in the harness environment, so that no cross-contamination is possible.
23. As a customer, I want Orion runs to be cancellable mid-run, so that I can stop unbounded work if I change my mind.
24. As a developer, I want Orion's PRs to respect existing branch-protection rules, so that they sit in `awaiting review` until an authorized human approves.
25. As a Revelara growth lead, I want Orion runs to be discoverable from the Polaris UI, so that customers on the Architect tier see Orion as part of the platform, not a separate product to onboard.

## Data Flow Traces

### Trace 1: Repo ingestion → architectural model

1. Customer installs Orion GitHub App on `github.com/customer/repo` → `internal/github/app.go`
2. Orion clones repo into ephemeral, network-isolated working directory → `internal/sandbox/clone.go`
3. Static analyzer parses Go source and produces a service-level dependency graph → `internal/architect/inference.go`
4. Inference produces an `ArchitecturalModel` containing services, endpoints, downstream deps, persistent stores, hot paths → `internal/architect/model.go`
5. Model persisted to Postgres `orion.architectural_models` table keyed by `run_id` → migration `001_runs_and_models.sql`

### Trace 2: Constraint inference → SLO Fabric

1. Architectural model + Polaris controls catalog → `internal/constraints/inferer.go`
2. Inferer queries Polaris API for applicable controls (`GET /api/v1/controls?categories=resilience,latency,idempotency`) → uses Polaris API
3. Inferer derives implicit constraints from code: existing timeouts → assumed budgets, existing retry config → assumed error rates, CPU/mem limits in IaC → resource constraints → `internal/constraints/inferer.go`
4. Constraints written to `orion.constraints` table keyed by `architectural_model_id` → migration `002_constraints.sql`
5. SLO Fabric is the input to harness synthesis and verification → consumed by `internal/harness/synthesizer.go`

### Trace 3: Harness synthesis → workload + faults

1. ArchitecturalModel + SLO Fabric → `internal/harness/synthesizer.go`
2. Workload synthesizer generates request distributions per endpoint based on inferred hot paths → `internal/harness/workload.go`
3. Fault synthesizer generates network/latency/error fault profiles based on inferred deps → `internal/harness/faults.go`
4. Harness config persisted to `orion.harnesses` table → migration `003_harnesses.sql`
5. Harness materialized as testcontainers + toxiproxy config in an isolated Kubernetes namespace → ephemeral, not persisted

### Trace 4: Patch synthesis → candidate patches

1. ArchitecturalModel + SLO Fabric + control catalog → `internal/patches/synthesizer.go`
2. For each detected control gap (e.g., missing timeout on a downstream call), the LLM generates patch candidates → `internal/llm` (independent provider config; code shared via Go module dep on Polaris's `internal/llm` interface)
3. Each candidate stored as `orion.candidate_patches` row with `diff`, `target_path`, `target_range`, `control_id` → migration `004_patches.sql`

### Trace 5: Verification → accepted patches

1. Candidate patch applied to a working copy in the sandbox → `internal/verify/runner.go`
2. Build verified, then harness run against patched system in container → runtime in sandbox
3. Metrics collected: tail latency under fault, cascade probability, baseline performance, resource usage → `internal/verify/metrics.go`
4. Patch accepted iff strictly dominates baseline AND zero regression on any axis → `internal/verify/optimizer.go`
5. Accepted patches persisted to `orion.accepted_patches` rows → migration `004_patches.sql`
6. Optimizer composes accepted patches into a sequence, re-verifying combinations to catch interactions → `internal/verify/composer.go`

### Trace 6: PR delivery → customer review

1. Accepted patches written as commits in a fresh branch via GitHub App write-to-branch permission → `internal/github/branch.go`
2. Orion creates a PR with the branch + verification report as the PR body → `internal/github/pr.go`
3. Verification report formatted as markdown: harness config summary, baseline metrics, patched metrics, deltas, operating envelope → `internal/report/formatter.go`
4. Orion calls Polaris (`POST /api/v1/orion/run-complete`) with `run_id`, `pr_url`, list of remediated risk IDs → handled by Polaris `internal/orion_link/handler.go`
5. Polaris updates `remediations` table marking listed risks as "claimed by Orion" with `pr_url` → migration on Polaris side
6. Customer sees the PR in their normal GitHub workflow and a linked entry in the Polaris Remediations view → frontend updates in Polaris

## UI Navigation

| Page / View | URL | Nav Location | Empty State | Feature Flag |
|---|---|---|---|---|
| Orion runs list | `/orion/runs` (Polaris UI) | Remediations dropdown → "Orion runs" | "No Orion runs yet — connect a repo to start" | `orion_enabled` |
| Orion run detail | `/orion/runs/:id` | Linked from runs list | "Run in progress" with live status, or final report after completion | `orion_enabled` |
| Orion repo settings | `/settings/orion/repos` | Settings → Integrations → Orion | "No repos connected — install the Orion GitHub App" | `orion_enabled` |
| Polaris risk detail (existing) | (existing) | (existing) | New "Claimed by Orion" badge appears when applicable | `orion_enabled` |

Existing pages requiring updates:

- **Polaris Remediations view**: add a column/badge showing Orion-claimed risks and a link to the GitHub PR.
- **Polaris risk detail page**: add a "Send to Orion" action button when the underlying control is in Orion's pattern set and the org has `orion_enabled`.

## Implementation Decisions

### Modules to build (Orion repo)

1. **`internal/github`**: GitHub App auth, clone, branch creation, PR creation, comment posting. Deep module: `Connector` interface with `Clone()`, `OpenPR()`, `PostComment()`. Internals: token rotation, rate limiting.
2. **`internal/sandbox`**: Ephemeral working directories, network-isolated containers, cleanup on completion or abort. Deep module: `Sandbox` with `Run()`, `Cancel()`.
3. **`internal/architect`**: Static analysis, dependency graph extraction, hot-path inference. Deep module: `Inferer.Infer(repoPath) ArchitecturalModel`.
4. **`internal/constraints`**: SLO Fabric inference combining Polaris controls catalog with code-derived implicit constraints. Deep module: `Inferer.Infer(model, catalog) Constraints`.
5. **`internal/harness`**: Workload synthesizer + fault synthesizer + harness orchestrator. Deep module: `Harness.Synthesize(model) HarnessConfig`, `Harness.Run(config, system) Metrics`.
6. **`internal/patches`**: Patch synthesis via LLM, constrained by architectural model + control catalog. Deep module: `Synthesizer.Synthesize(model, gaps) []Patch`.
7. **`internal/verify`**: Patch application, metric collection, dominance check, optimizer composing patch sequences. Deep module: `Verifier.Verify(patch, baseline, harness) Verdict`.
8. **`internal/report`**: Markdown verification report generator. Shallow module — straightforward formatting.
9. **`cmd/orion`**: Service entrypoint.
10. **`cmd/orion-cli`**: CLI for dogfooding (`orion-cli run --repo=... --service=...`).

### Modules to build/modify (Polaris repo)

1. **`internal/orion_link`**: Polaris ↔ Orion integration. New endpoints: `POST /api/v1/risks/:id/claim-by-orion`, `POST /api/v1/orion/run-complete`, `GET /api/v1/orion/runs`. Surfaces Orion-claimed risks in the Remediations view.
2. **Frontend updates**: Remediations view gains an Orion badge column; risk detail gains a "Send to Orion" button gated by `orion_enabled` and pattern eligibility.

### Schema changes (Orion's own Postgres)

- `orion.repos`: connected GitHub repos, app install IDs, status.
- `orion.runs`: each run is a unit of work with `status`, `started_at`, `finished_at`, `org_id`, `repo_id`.
- `orion.architectural_models`: JSONB blob keyed by `run_id`.
- `orion.constraints`: SLO Fabric per run.
- `orion.harnesses`: harness config per run.
- `orion.candidate_patches`: pre-verification patches.
- `orion.accepted_patches`: post-verification patches that ship.
- `orion.runs_metrics`: verification metrics per run.

Orion has its own database, not shared with Polaris. This isolates blast radius and allows independent scaling of harness compute. RLS is applied to all tables containing `org_id` from day one, following the Polaris pattern.

### API contracts (selective)

Orion external API:

- `POST /api/v1/runs` — start a run; body `{repo_url, service_path, controls?}`; returns `run_id`.
- `GET /api/v1/runs/:id` — status.
- `GET /api/v1/runs/:id/report` — verification report (markdown).
- `DELETE /api/v1/runs/:id` — cancel a run.

Polaris ↔ Orion (server-to-server, signed):

- `POST /api/v1/risks/:id/claim-by-orion` (on Polaris) — Polaris reserves a remediation.
- `POST /api/v1/orion/run-complete` (on Polaris, called by Orion) — Orion notifies completion + PR URL + remediated risk IDs.

### Architectural decisions

1. **Orion has its own database.** Server-to-server integration with Polaris via API.
2. **Orion has its own LLM budget and provider configuration.** Code shared with Polaris's `internal/llm` via Go module dependency, but each service is configured independently.
3. **Harness compute runs in isolated Kubernetes namespaces.** Each run gets its own namespace, cleaned up on completion or after a 24-hour timeout.
4. **The optimizer is greedy + verification-gated, not formal.** Patches are composed by accepting the highest-marginal-gain candidate that passes verification, with combinatorial re-checks at each composition step. Orion does not claim formal optimality; it claims "no patch in the candidate set strictly dominates what we shipped, given the harness."
5. **Persistence is RLS-enforced.** All Orion tables with `org_id` follow Polaris's RLS pool selection rule from day one.
6. **Branch creation is authoritative; merge is the customer's.** Orion has write-to-branch permission via the GitHub App, but never write-to-main and never auto-merge.

### Feature Flag Wiring

- **`orion_enabled`** (org-level, defined in Polaris flags registry):
  - Checked in: Polaris API handlers (gates `/api/v1/orion/*` and `/risks/:id/claim-by-orion`), Polaris frontend nav (gates Orion menu items), Polaris controls catalog API (returns `orion_eligible: bool` per control).
  - On: Orion menu visible, Orion-eligible risks show "Send to Orion" button, claimed risks show Orion badge.
  - Off: no Orion UI surfaces, claim API returns 404.

- **`orion_autopr`** (org-level, defined in Polaris flags registry):
  - Checked in: Orion's PR delivery step (`internal/github/pr.go`).
  - On: Orion opens the PR automatically after verification.
  - Off: Orion produces the verification report only; no PR is opened. Used for early dogfooding and high-sensitivity orgs.

## Testing Decisions

### Test philosophy

Test external behavior, not implementation. Inference, harness, optimizer, and verifier all have observable outputs (architectural model JSON, synthesized workloads, metric deltas, accept/reject verdicts) that can be asserted against without poking internals. Mock GitHub API for unit tests; use ephemeral test repos and fixtures for integration tests. Follow Polaris's testing conventions (`make test`, `make test-integration`, `make test-security`).

### Modules with priority test coverage

1. **`internal/architect`**: golden-file tests against fixture repos. Given repo X, the produced ArchitecturalModel matches Y.
2. **`internal/harness`**: workload synthesizer determinism (same inputs, same workload). Fault profiles cover the documented matrix.
3. **`internal/verify`**: dominance check unit tests. Given baseline and patched metrics, the verdict matches expectation.
4. **`internal/patches`**: regression tests against fixture control gaps. Generated patch addresses the gap (no formal proof, just regression).
5. **`internal/orion_link` (Polaris)**: contract tests verifying claim/complete API behavior with RLS context set.

### Integration Acceptance Test

**Orion v1 acceptance test (FIRST issue created, LAST issue closed):**

1. Provision a fixture Go service repo `github.com/revelara-test/orion-fixture-svc` containing three known reliability gaps:
   - One outbound HTTP call without a context timeout in `client.go`.
   - One retry loop with no backoff in `retry.go`.
   - One POST endpoint without idempotency-key handling in `handler.go`.
2. Trigger an Orion run: `orion-cli run --repo=github.com/revelara-test/orion-fixture-svc --service=cmd/svc`.
3. **Verify**: a PR is opened against the fixture repo by the Orion GitHub App.
4. **Verify**: the PR contains exactly three commits, each addressing one of the gaps.
5. **Verify**: each commit's diff modifies the expected file (timeout patch in `client.go`, retry patch in `retry.go`, idempotency patch in `handler.go`).
6. **Verify**: the PR body contains a verification report including harness config summary, baseline metrics, patched metrics for each axis, deltas exceeding configured thresholds, and operating envelope.
7. **Verify**: in Polaris with `orion_enabled` on for the test org, the Remediations view shows three risks with Orion badges linking to the PR.
8. **Verify**: the Orion run is recorded in `orion.runs` with `status=completed` and metrics deltas matching the PR report.

This test pins down end-to-end correctness of the closed loop on a known-good fixture and gates v1 release.

## Out of Scope

- **Languages other than Go.** TypeScript, Python, Java, etc. deferred to v2+.
- **Patterns beyond the v1 three.** No SPOF/blast-radius patches, schema evolutions, query/algorithm rewrites, or memory-allocation rewrites in v1.
- **On-prem deployment.** SaaS only.
- **Production/runtime access.** Orion does not call into production systems, does not consume customer telemetry, does not scrape Grafana. Codebase-only.
- **Auto-merge or auto-deploy.** Hard rule.
- **Multi-repo / monorepo with multiple services in a single run.** v1 targets one service per run.
- **Continuous mode.** v1 is batch only — customer triggers a run, gets a PR, run ends.
- **Direct customer billing/usage metering.** v1 is part of the Architect intelligence tier on Growth/Enterprise platform fees; metering is logged but not billed individually.
- **Cross-customer model training.** Orion's LLM calls are per-tenant; no training data is retained from customer code.

## Further Notes

- **Internal dogfooding sequence:** start with `polaris` (we know its risks intimately), then `pipeline`, then `crawler`. This drives v1 fixture coverage and exposes harness weaknesses before any external partner sees Orion.
- **Pricing:** Architect intelligence multiplier on Growth ($1,999) and Enterprise ($5K+) tiers. Customers below Growth do not get Orion access.
- **Branch protection:** if a customer's repo has branch protection requiring reviewers, Orion's PR sits in `awaiting review` until a human approves. This is correct behavior and explicitly preserved.
- **Failure modes:** if Orion finds no patches that strictly dominate, it produces a "clean run" report and does NOT open a PR. This is a positive signal, not a failure.
- **Provenance:** every Orion commit is signed with the Orion GitHub App key and includes a footer linking to the Polaris run ID and the verification report.
- **Verification honesty:** the verification claim is "within the operating envelope of the synthesized workload, the patched system shows X% improvement on every measured axis with zero regression." This is not a formal proof. The operating envelope is published with every run, and the customer can replay it.
