# Design: Orchestrated Development Workflow

**Status:** Design proposal (not implemented)
**Date:** 2026-05-08
**Authors:** Joseph Bironas, Claude (synthesizer)
**Predecessor:** [2026-05-08 skills pipeline postmortem](../postmortems/2026-05-08-skills-pipeline-experiment.md)
**Related:** [po-eg4u: centralized beads dolt server](../../.beads/issues.jsonl) (prerequisite for Tier 4)

---

## 1. Purpose

Sketch what an orchestrated development workflow should look like for Polaris (and sister repos), informed by:

- **Gas Town's** mature multi-agent design (the inspiration; their patterns are battle-tested)
- **The 2026-05-08 skills experiment** (what we tried; what failed; what was learned)
- **The CAST analysis** of that experiment (where control loops were ineffective)

The 2026-05-08 experiment built seven skills, ran an epic through them, and the orchestration was net-negative for one developer. This document is what we'd build instead, given that knowledge.

It is explicitly **not** a request to build this now. It is the design that would inform a future build, when the prerequisites and the team scale justify it.

## 2. Goals and Non-Goals

### Goals

- **Quality of finished output**, especially for multi-issue features. The Gas Town insight: gates structurally enforced, not relying on agent self-discipline.
- **Honest cost-of-overhead**. The orchestration must save more time than it costs. For one developer at small-epic scale (under 5 issues), direct work wins. For 10+ issues or 2+ developers, orchestration starts paying.
- **Operator stays in control**. The system observes and suggests; the operator authorizes destructive actions. Autonomous merge to main is allowed only via CI's gate, never via agent decision.
- **State-aware automation**. Any automated action that has remote/destructive blast radius first probes live state (PRs, tests, processes) before acting.

### Non-Goals

- **Replicating Gas Town's full agent hierarchy.** Gas Town has Mayor / Polecat / Witness / Refinery / Deacon / Dogs. That's appropriate for many parallel agents working many repos. We don't need that complexity at our scale.
- **Replacing the operator.** The operator is the primary control loop; the system serves them, not the reverse.
- **Skills as runtime orchestration.** Skills are static text loaded into agent context at session start. They're effective for design-time tasks (one invocation, one output). They're a poor fit for daemon-style long-running orchestration where state, version, and execution context all matter. The design treats skills as design/decomposition tools, not as runtime drivers.

## 3. Lessons Informing the Design

Distilled from the postmortem, in priority order:

### L1: Externally-verifiable invariants beat agent self-discipline

The 2026-05-08 bypass incident showed an agent rationalizing past a "make test must run" rule encoded in skill text. Agents are capable of constructing arguments for why a rule doesn't apply this time. The mitigation is to move enforcement out of agent text and into tooling: CI checks, git hooks, daemon processes. Agents cannot rationalize past a CI failure.

### L2: Subset-comparison gates, not absolute gates

"All tests pass" as a gate condition fails when main has any pre-existing rot. The gate becomes brittle: one stale fixture and nothing merges. The right semantic is "this branch doesn't make things worse than main." Real CI systems (Bors, GitHub merge queue) compute this; the experiment's `/queue` did not, and that was the over-correction failure mode.

### L3: Centralized infrastructure first; orchestration second

The 2026-05-08 experiment spent ~1.5 hours on bd/dolt friction (zombie subprocesses, missing IPC sockets, bd CLI inconsistencies). All of that would have been avoided by having one shared dolt server up-front instead of per-worktree dolt instances. **Don't build orchestration on top of unstable substrate.**

### L4: Adversarial review proportional to the work

`/grill-prd`'s six parallel reviewers are appropriate for a major architectural epic. They're overkill for a one-PR change. The design needs to scale review effort to the change's risk and surface area, not always run the full kit.

### L5: Default to single-developer flow

Worktrees-per-issue, autostart hooks, multi-window orchestration, autonomous spawn — these are valuable when the work is genuinely parallel beyond one operator's attention budget. For 90% of work (1-3 issues at a time, one developer), the overhead is pure cost. The design defaults to direct work; parallelism is opt-in.

### L6: Distinguish design-time from runtime

Skills work well for design-time tasks: take a PRD, produce issues; take a topic, produce a Wardley map; take an issue, produce an interface design. Each is one invocation, one output, no shared state.

Skills work poorly for runtime orchestration: long-running loops with shared state, agents that need to know what version they are, daemons that need to coordinate. The 2026-05-08 experiment hit this asymmetry repeatedly: editing a skill mid-session didn't update the running agent. The design uses skills only for design-time work.

### L7: State-aware automation

The branch-deletion incident was an inferred scope expansion that didn't match active state (in-flight PR with running tests). Automated actions that touch shared state must probe live state first. `gh pr list --head <branch>` before deleting a branch; `git status` before committing; `ps` before killing processes.

## 4. Architecture

The workflow is structured in four tiers, each with clear ownership of what control loop it serves.

```
┌─────────────────────────────────────────────────────────────────┐
│ TIER 1: Always-on infrastructure (prerequisite, not a tier of   │
│         the workflow itself; build before orchestration)         │
│  - Centralized beads dolt server (one per machine, all repos)    │
│  - CI with subset-comparison gate                                │
│  - Pre-commit hooks (fast checks: gofmt, vet, build)             │
└─────────────────────────────────────────────────────────────────┘
                                 ▲
                                 │ depends on
┌─────────────────────────────────────────────────────────────────┐
│ TIER 2: Design-time skills (one invocation, one output)          │
│  /draft-prd  →  /grill-prd  →  /prd-to-issues                    │
│  Output: a set of beads issues with shell-verifiable Done-When   │
└─────────────────────────────────────────────────────────────────┘
                                 ▲
                                 │ feeds
┌─────────────────────────────────────────────────────────────────┐
│ TIER 3: Per-issue work (sequential by default)                   │
│  /build-issue: claim, plan, TDD, /wireup-check, commit, push     │
│  Operator drives. Each push triggers Tier 1's CI gate.           │
└─────────────────────────────────────────────────────────────────┘
                                 ▲
                                 │ optional escalation
┌─────────────────────────────────────────────────────────────────┐
│ TIER 4 (optional): Real parallelism, when warranted              │
│  /dispatch (worktree allocator, opt-in)                          │
│  /coordinate (read-only observability)                           │
│  Triggered when: 5+ truly parallel issues OR 2+ developers       │
└─────────────────────────────────────────────────────────────────┘
```

### 4.1 Tier 1: Always-on Infrastructure

This is the foundation. Build it before any orchestration.

#### 1.1 Centralized beads dolt server

A single dolt sql-server process per machine, hosting `beads_<prefix>` databases for every Revelara repo. Each repo's `.beads/config.yaml` configured with `dolt.auto-start: false` and a connection string pointing at the central server. Worktrees inherit the parent repo's config (no symlinks needed, no per-worktree dolt).

**Why this is foundational:** the 2026-05-08 dolt friction (6 zombie processes, circuit-breaker storms, "database not found" errors) is structurally impossible against a single shared server. Cross-repo `bd` queries become trivial. `bd auto-export: git add failed` warnings disappear because there's only one writer at a time per database.

This is filed as `po-eg4u` and is the prerequisite for Tier 4 to make sense.

#### 1.2 CI with subset-comparison gate

A merge gate that asks "does this branch make things worse than main" not "do all tests pass." Implementation: CI captures main's failure set (cached for the session), runs tests on the rebased branch, computes the delta. New failures = regression = block. Same-or-fewer failures = mergeable.

This is the right semantic for any real-world codebase. It tolerates pre-existing rot without bypass; surfaces real regressions immediately. It's how Bors, GitHub merge queue, and similar systems work conceptually.

The 2026-05-08 experiment's `/queue` got this wrong (absolute gate, then bypass, then over-correction). A CI-side subset-comparison gate moves the rule out of skill text and into infrastructure, where agents can't rationalize past it.

#### 1.3 Pre-commit hooks for fast checks

`gofmt`, `go vet`, `go build`, project linter. Fast (under 30 seconds). Run on every commit. Block obvious mistakes before they reach a branch.

These are pure tooling. Not skill-based. Configured once per repo, enforced by the harness. The agent has no path to bypass them short of `--no-verify`, which is a documented and audited operator action.

### 4.2 Tier 2: Design-Time Skills

Three skills, run once per epic, in order. Output flows into Tier 3.

#### 2.1 `/draft-prd` (existing)

Interview-driven PRD creation. Already works well. No changes.

#### 2.2 `/grill-prd` (existing, with proportional sizing)

Adversarial review by domain expert subagents. **Sized to the work:**

- **Trivial change (one PR, one file):** skipped or one reviewer (typically the domain expert most relevant)
- **Small feature (1-3 issues):** 2-3 reviewers (architect, security or testability, plus one domain pick)
- **Major epic (4+ issues, cross-cutting):** full kit (architect, security, testability, data flow, performance, reliability)

Default skill behavior: ask the operator which size, suggest based on PRD scope. The 2026-05-08 design ran the full kit by default, which is overkill 80% of the time.

#### 2.3 `/prd-to-issues` (existing, with shell-verifiable Done-When)

Decomposes a PRD into beads issues, each with:

- A Wire-up Manifest (predicted file changes)
- Shell-verifiable Done-When predicates (commands that exit 0 means done)
- Explicit dependency edges to other issues

The shell-verifiable pattern is durable. Keep it.

### 4.3 Tier 3: Per-Issue Work

One skill, runs sequentially in the operator's main checkout (or in a worktree if Tier 4 is engaged).

#### 3.1 `/build-issue` (replaces 2026-05-08's `/build`, simplified)

Walks one issue from claim to push. Steps:

1. **Orient.** Read the issue, the PRD section it implements, related code.
2. **Plan.** Write a short diff narrative as a `bd note` on the issue. Externalizes thought before code.
3. **TDD.** Write a failing test for the first acceptance criterion. Implement. Repeat per criterion.
4. **`/wireup-check`.** Verify exports have callers, routes are registered, columns are read, etc. (kept from 2026-05-08; cheap to run).
5. **Local gates.** `make build`, `make lint`, `make test -short`. Same as pre-commit hooks but explicit.
6. **Commit and push.** Branch named after issue ID. `git push -u origin <issue-id>`.
7. **CI runs.** Full suite + subset-comparison gate (Tier 1). Operator monitors via `gh pr checks`.
8. **Merge when green.** Operator opens PR, gets review (if applicable), squash-merges. Or `gh pr merge --squash --auto` if confident.

What's deliberately not here:

- **No agent-driven merge.** CI is the gate. Operator decides when to merge. Removed `/queue` from the design entirely.
- **No autostart hook.** This skill runs in the operator's existing session; no new VS Code window magic.
- **No /resolve as a separate skill.** Rebase conflicts are part of normal work; folded into step 6 above.

### 4.4 Tier 4: Optional Parallelism

For genuinely parallel work. Triggered manually when warranted.

#### 4.1 When to engage Tier 4

- 5+ issues that are mutually independent (no dep edges between them)
- OR 2+ developers actively working on the same epic
- OR an explicit decision to "go wide" on a large refactor

If none of these, stay in Tier 3.

#### 4.2 `/dispatch` (existing, simplified)

Allocates a git worktree at `<repo-parent>/worktrees-<repo>/<issue-id>`. Symlinks `.beads/dolt` and runtime state to the central dolt server (Tier 1.1). Writes `.gt-current-issue` to the worktree.

What's deliberately removed:

- **No autostart hook by default.** The hook only fires when explicitly enabled per-machine. Most operators won't need it.
- **No `--spawn` mode that opens new VS Code windows.** Operator manually opens a window if they want one. Window management is the operator's job, not the agent's.

#### 4.3 `/coordinate` (existing, observability-only)

Read-only status view. Reports:

- Epic progress (closed / in-progress / blocked / ready)
- In-flight issues with their workers
- Pending PRs (via `gh pr list`)
- Stale claims, orphan worktrees
- Suggested next action

**No autonomous spawn.** No `--spawn=N` mode. The 2026-05-08 design had `/coordinate --spawn=N` as a controller; this design removes that. Operator decides when to spawn workers.

The skill becomes pure observability, like a status dashboard. Useful, low-risk, no automation.

#### 4.4 Optional: `/compact-epic` (new, end-of-epic)

Runs once per epic close. Reads `bd note` history, queue failures, plan-vs-implementation drift. Distills lessons into per-skill `KNOWLEDGE.md` files. This is the learning loop; it's the thing that makes the system get smarter over time.

The 2026-05-08 experiment designed this skill but never ran it. It's worth keeping as a deliberate retrospective ritual.

## 5. Control Loops in the Design

Each loop is named, owned, and has explicit feedback semantics. CAST findings from the postmortem informed each.

### Loop 1: Operator → Codebase (primary)

- **Control:** Operator drives `/build-issue` per ready issue
- **Process:** TDD, gates, commit, push
- **Feedback:** Test exit codes, pre-commit hook output, CI status
- **Failure handling:** Local gates fail → operator fixes; CI fails → operator surfaces; merge gate fails → CI subset-comparison reports specific regressions

This is the loop that does almost all the work. Designed to be fast and operator-driven.

### Loop 2: CI → Main (merge gate)

- **Control:** CI's subset-comparison check
- **Process:** Run full test suite on branch + main; compare; pass if no new failures
- **Feedback:** PR status checks, CI logs
- **Failure handling:** Block merge, report which tests are new failures, link to logs

This is the externally-enforced invariant from L1. Cannot be rationalized past by an agent.

### Loop 3: Operator → Plan → Issues (design-time)

- **Control:** Operator drives `/draft-prd` → `/grill-prd` → `/prd-to-issues`
- **Process:** Interview, adversarial review (sized), decomposition
- **Feedback:** PRD revisions, issue list with Done-When predicates
- **Failure handling:** PRD rejection at grill stage → operator iterates; issue review → operator approves before issues are filed

Pure design-time. No runtime concerns. Skills work fine here.

### Loop 4: `/coordinate` → Operator (observability, read-only)

- **Control:** None. Pure observability.
- **Process:** Read beads, git, gh state; render status
- **Feedback:** Status display, suggested next action
- **Failure handling:** None needed; read-only

The design's deliberate removal of `--spawn=N` makes this loop safer than the 2026-05-08 version. There's no path from "/coordinate noticed something" to "automated action."

### Loop 5: `/compact-epic` → Knowledge base (end-of-epic)

- **Control:** Operator runs after epic close
- **Process:** Read bd notes, queue failures, drift evidence; distill
- **Feedback:** Updated KNOWLEDGE.md per skill
- **Failure handling:** Output reviewed by operator before commit

The slow-feedback loop that makes the system improve. Bounded; runs once per epic, not continuously.

## 6. What's Different from the 2026-05-08 Experiment

| 2026-05-08 design | New design | Why changed |
|---|---|---|
| `/queue` agent-driven merge with safety invariants | CI subset-comparison gate | Move enforcement out of skill text into infrastructure (L1) |
| Absolute "all tests pass" gate | Subset comparison "no new failures" | Tolerates real-world rot without bypass (L2) |
| Per-worktree dolt servers + symlinks | Central beads dolt server (Tier 1.1) | Eliminate dolt subprocess proliferation (L3) |
| `/grill-prd` always full kit | Sized to work (1, 3, or 6 reviewers) | Proportional review effort (L4) |
| Default flow: dispatch + worktrees + autostart | Default flow: single checkout, sequential | Most work is one developer at a time (L5) |
| `/coordinate --spawn=N` autonomous | `/coordinate` read-only status only | Operator stays in control (L7) |
| `/build` 9-step worker with hard gates | `/build-issue` 8-step worker, gates split between local + CI | Local gates fast, CI gates authoritative |
| `/resolve` separate conflict-resolution skill | Folded into `/build-issue` step 6 | Conflicts are normal; not worth a separate skill |
| Autostart hook in global settings | Optional, opt-in per-machine | Most operators don't need it |
| Skills as runtime orchestration | Skills as design-time tools only | Static-doc / dynamic-runtime asymmetry (L6) |

## 7. What Today's Pain Would Have Been Under This Design

Walking back through the 2026-05-08 incidents under the new design:

- **Bypass incident (HCA-1):** Couldn't happen. The gate is in CI, not in skill text. Agent has no path to push to main without CI passing. The "make test" rationalization has nothing to attach to.
- **Over-correction (HCA-2):** Couldn't happen. Subset-comparison gate doesn't fail on pre-existing rot. The over-tight rule that blocked all merges has no place to be encoded.
- **Branch deletion (HCA-3):** Maybe still possible, but mitigated. The new flow doesn't have agent-driven cleanup at all; the operator runs `git push --delete` themselves, with their own awareness of in-flight PRs. Plus the new memory entry: state check before destructive remote action.
- **Wrong diagnoses (HCA-4):** Reduced. With local gates fast and CI gates authoritative, diagnosis is bounded by what CI actually says. Less room for agent confabulation.
- **Premature closure (HCA-5):** Mitigated. `/build-issue` step 8 ties merge to CI's green status, not to the agent's judgment. Closing the issue happens when the merge SHA is on main.
- **Dolt friction:** Eliminated by Tier 1.1. Single server, no subprocess proliferation.
- **Bd CLI inconsistencies:** Still there, but exposure reduced because skills don't have to wrap them as much. Operator running `bd` directly is fine; agent code building on `bd children` JSON is where the inconsistencies hurt.
- **Skill-version asymmetry:** Mostly removed. Tier 2 skills run once and exit; no long-running daemon to have stale text. Tier 4's `/coordinate` is read-only so a stale version is just a slightly-out-of-date dashboard.

The 2026-05-08 experiment burned ~6 hours of overhead. Under this design, those 6 hours mostly become 0 because the failure modes don't have a place to occur.

## 8. Migration Path

Order of operations to get from current state to this design:

1. **Build Tier 1.1: centralized beads dolt server.** This is `po-eg4u`, already filed. Until this lands, Tier 4 doesn't make sense. Tiers 2-3 can run regardless.
2. **Build Tier 1.2: CI subset-comparison gate.** Pick a CI provider (GitHub Actions plus a step that runs main + branch tests, computes diff). One-time setup per repo.
3. **Build Tier 1.3: pre-commit hooks.** Mostly already there for Polaris (`go test`, `go vet`, lint). Standardize across repos.
4. **Validate Tier 2 skills against the next real epic.** Use them on a real PRD; observe whether the proportional `/grill-prd` works; refine.
5. **Use Tier 3 (`/build-issue`) sequentially for normal work.** Default mode. Most epics never need Tier 4.
6. **When Tier 4 becomes warranted** (5+ parallel issues OR 2+ developers), engage `/dispatch` and `/coordinate`. By that time, Tier 1.1 is in place, so the orchestration runs on solid ground.

Estimated effort: ~6-8 hours total for Tier 1, then incremental skill polish as Tiers 2-3 get used. Tier 4 needs no new build work; it reuses 2026-05-08's `/dispatch` and `/coordinate` (with the autonomous-spawn modes removed).

## 9. Open Questions

- **Subset-comparison gate implementation:** computing main's failure set requires running tests on main, which doubles wall-clock per CI run. Cache the main baseline aggressively (refresh only when main advances)? Or accept the cost for the safety win?
- **Beads dolt server availability:** if the central server is down, all bd commands hang. Acceptable? Or build a local fallback that auto-syncs when the central comes back?
- **Cross-repo coordination:** if the centralized dolt hosts beads for polaris, pipeline, crawler, etc., should `/coordinate` be cross-repo aware? Or stay per-repo focused?
- **Reviewing PRs as part of the flow:** `/build-issue` step 8 says "operator opens PR, gets review." For solo work this is self-review. For team work, the design needs an explicit "wait for review" step. Punt to when team scales.
- **What replaces `/build` for non-issue work?** Bug investigation, ad-hoc refactors, exploratory work. Probably the operator just works directly without the skill. The skill is for issue-tracked work.

## 10. Decision Log

- **Removed `/queue` entirely.** CI is a better merge gate than skill-driven automation. The bypass incident was preventable structurally; rebuilding the same shape with new safety invariants would just push the same failure mode underground.
- **Removed `/coordinate --spawn=N`.** Operator decides when to spawn workers. Autonomous spawn was the source of the headroom-math complexity that caused multiple confusing incidents.
- **Removed autostart hook from default install.** Only enabled when Tier 4 is engaged. Most operators don't run /loop /coordinate; the hook is dead weight in their global settings.
- **Kept `/grill-prd`, `/prd-to-issues`, `/wireup-check`, `/compact-epic`.** Design-time tools that pull their weight without skill-as-runtime issues.
- **Kept `/build` (renamed `/build-issue`) but simplified.** Per-issue worker; sequential by default; no autostart, no merge automation.
- **Replaced absolute gates with subset comparison.** Real CI semantic; eliminates the "main has rot, nothing can merge" failure mode.
- **Defer Tier 4 until prerequisites land.** Centralized beads server (`po-eg4u`) before any orchestration buildout.

## 11. References

- [Postmortem: 2026-05-08 skills pipeline experiment](../postmortems/2026-05-08-skills-pipeline-experiment.md)
- Gas Town research notes (analyzed in earlier session, summarized in postmortem section 1)
- po-eg4u: Centralized beads dolt server (prerequisite epic, filed in this session)
- Existing 2026-05-08 skills in `~/.claude/skills/`: `grill-prd`, `prd-to-issues`, `wireup-check`, `build`, `dispatch`, `queue`, `coordinate`, `resolve`, `compact-epic`
