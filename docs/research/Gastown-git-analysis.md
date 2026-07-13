# Gas Town — Git Worktree, Repo, and Workflow Analysis

> **Source:** `~/src/gastown/` (module `github.com/steveyegge/gastown`)
> **Analyzed at commit:** `51183512` (main)
> **Method:** Understand-Anything knowledge graph (`.understand-anything/knowledge-graph.json`) + source inspection.
> **Related:** [Gastown-Worktree-Handling.md](./Gastown-Worktree-Handling.md), [Gastown-Overview.md](./Gastown-Overview.md)

Gas Town is a multi-agent orchestration system that coordinates AI coding agents (Claude Code, Copilot, Codex, Gemini). Its entire isolation and concurrency model is built on **git worktrees off a shared bare repository**, and it enforces a deliberately rigid git workflow at four independent layers. This document covers three things:

1. **Repo architecture** — how repositories are laid out per rig.
2. **Worktree usage** — how worktrees are created, named, and cleaned up, per agent role.
3. **Enforced git workflow** — the branch/merge policy and where each rule is enforced.

All file references are relative to the Gas Town repo root.

---

## 1. Repo architecture — one shared bare repo per rig

A **rig** is a project container wrapping one git repository. When a rig is added, Gas Town clones the remote **once** as a bare object store and then derives everything else as worktrees against it.

- **Bare store:** `<rig>/.repo.git` — created by `AddRig` → `cloneBareWith` in [internal/rig/manager.go](../../../../../src/gastown/internal/rig/manager.go) (`internal/rig/manager.go:424-446`). All worktrees share this object database, so adding a worker is cheap and the Refinery can see polecat branches **without anything being pushed to the remote**.
- **Mayor is the exception:** the Mayor gets a *separate full clone* at `<rig>/mayor/rig`, not a worktree (`internal/rig/manager.go:528-533`). This is deliberate — it lets the Mayor stay on the default branch without competing for branch checkout with the Refinery (git forbids the same branch being checked out in two worktrees of one repo).

### Layout per rig

```
<townRoot>/<rig>/
├── .repo.git/                      # shared bare object store
├── .land-worktree/                 # transient merge-queue landing worktree (integration/*)
├── mayor/rig/                      # SEPARATE clone (default branch) — coordinator
├── refinery/rig/                   # worktree on default branch — merge queue worker
├── witness/                        # worktree — infra agent
├── crew/<member>/                  # worktree(s) on default branch — human/agent workspace
└── polecats/<name>/<rig>/          # worktree on polecat/* — ephemeral task worker
```

| Worktree / clone | Path | Branch | Role |
|---|---|---|---|
| Bare store | `<rig>/.repo.git` | — | shared object DB |
| Mayor | `<rig>/mayor/rig` | default (separate clone) | coordinator |
| Refinery | `<rig>/refinery/rig` | default branch | merge-queue worker |
| Polecat | `<rig>/polecats/<name>/<rig>` | `polecat/*` | ephemeral task worker |
| Crew | `<rig>/crew/<member>` | default branch | human/agent workspace |
| Witness / Deacon | `<rig>/witness`, … | default | infra agents |
| Land worktree | `<rig>/.land-worktree` | `integration/*` | merge-queue landing (`internal/cmd/mq_integration.go:133`) |

---

## 2. Worktree usage — creation, naming, cleanup

Two layers are involved:

- **Git plumbing** — `internal/git/git.go:1646-1852` wraps `git worktree add/remove/move/prune/list`.
- **Lifecycle** — `internal/polecat/manager.go` owns the per-worker (polecat) story; rig-level worktrees (refinery, witness) are created in `internal/rig/manager.go`. The `gt worktree` command family ([internal/cmd/worktree.go](../../../../../src/gastown/internal/cmd/worktree.go)) plus `internal/worktree/integrity.go` provide role-worktree create/list/remove with integrity metadata.

### Creation

A polecat worktree is added off the shared bare repo `<rig>/.repo.git` (falling back to `mayor/rig`), not a fresh clone, so objects are shared (`repoBase`, `internal/polecat/manager.go:462-478`).

The core call is `WorktreeAddFromRef(clonePath, branchName, startPoint)` (`internal/polecat/manager.go:803`, wrapping `internal/git/git.go:1664` → `git worktree add -b <branch> <path> <startPoint>`, LFS smudge filter skipped). Start point is normally `origin/<defaultBranch>`, or an explicit `BaseBranch`, or an existing PR head when resuming (`manager.go:780-786`). A resumed polecat reattaches an existing branch via `WorktreeAddExistingForce`.

### Naming convention

**Directory** (`clonePath`, `internal/polecat/manager.go:495-514`):

```
<townRoot>/<rig>/polecats/<name>/<rigname>/   ← current structure
<townRoot>/<rig>/polecats/<name>/             ← legacy fallback
```

The nested `<rigname>/` segment is deliberate — it gives the agent a recognizable repo-named working directory.

**Worker name** `<name>` — drawn from a bounded, themed **NamePool** ([internal/polecat/namepool.go](../../../../../src/gastown/internal/polecat/namepool.go)), not random:
- 50 slots; built-in themes `mad-max` (default), `minerals`, `wasteland`, plus custom theme files (`namepool.go:45-82`).
- Each rig gets a deterministic theme via name hash; `ThemeForRigAvoiding` keeps sibling rigs on different themes so names stay globally distinct (`namepool.go:432-482`).
- Allocation is in-order/first-free; on exhaustion it overflows to numbers `51, 52, …` (`Allocate`, `namepool.go:270`). Infra names (`witness`, `mayor`, `refinery`, …) are reserved (`namepool.go:35`). Guarded by a pool flock + a `<name>.pending` reservation marker to close the TOCTOU window before the directory exists (`AllocateName`, `manager.go:1369`).

**Branch name** (`buildBranchName`, `internal/polecat/manager.go:555`), default format:

```
polecat/<name>/<issue>@<timestamp>   (when an issue is hooked)
polecat/<name>-<timestamp>           (otherwise)
```

`<timestamp>` is base-36 epoch-millis. Overridable via a `polecat_branch_template` config supporting `{user} {year} {month} {name} {issue} {description} {timestamp}`.

### Cleanup

Two paths:

**Explicit removal** (`gt polecat nuke` / `gt done`) — `RemoveWithOptions`, `internal/polecat/manager.go:1124`. Safety-gated:
1. Per-polecat flock; refuse if not found.
2. Block on uncommitted/unpushed/stashed work unless `force`/`nuclear` (trusts the polecat's self-reported `cleanup_status` bead first, falls back to live `git status`).
3. Refuse if the polecat has an **open MR in the merge queue** — even nuclear mode (`manager.go:1174`).
4. Refuse if your shell is `cd`'d inside the worktree, unless self-nuking.
5. Reset the agent bead and unassign work beads *before* filesystem ops (race-safety with concurrent re-allocation).
6. **Best-effort push** of unpushed commits before deletion so work isn't lost (`manager.go:1230`).
7. `WorktreeRemove` → fall back to `os.RemoveAll` → also `RemoveAll` parent dir → `WorktreePrune` → `verifyRemovalComplete` → release name to pool.

**Automatic reconciliation** — `ReconcilePool` (on allocation + startup, `manager.go:1907`). Filesystem is the source of truth ("ZFC" — in-use set never persisted):
- `git worktree prune` for manually-deleted dirs.
- Kill **orphan** tmux sessions (session, no directory) and **stale** ones (directory, but heartbeat stale / PID dead) — `isSessionProcessDead`, `manager.go:2018`.
- `cleanupOrphanPolecatState` (`manager.go:2067`) removes stale `.pending` markers (>5 min), empty polecat dirs, and incomplete worktrees missing `.git`.

A third, out-of-band layer — the **doctor** checks (`internal/doctor/rig_check.go`, `worktree_gitdir_check.go`, `land_worktree_gitignore_check.go`) — detects and repairs broken/migrated worktree structures.

---

## 3. Enforced git workflow

The policy is blunt and intentional:

> **Crew push directly to the default branch. No feature branches. Never open internal PRs. Polecats push to `polecat/*` branches that the Refinery merges. PRs are for external forks only.**

It is enforced at **four independent layers** so it can't be bypassed by convention drift.

### Layer 1 — Client-side pre-push hook ([.githooks/pre-push](../../../../../src/gastown/.githooks/pre-push))

Wired in by setting `core.hooksPath = .githooks` on every repo/worktree (`ConfigureHooksPath` / `configureHooksPath`, `internal/git/git.go:755-776`), installed during rig setup. It enforces:

- **Branch allowlist** — only `<default_branch>`, `beads-sync`, `polecat/*`, `integration/*` may be pushed. Anything else is **blocked** *unless an `upstream` remote exists* (the fork/contribution escape hatch, GH#848).
- **HEAD-mismatch guard** — refuses `git push origin <B>` when HEAD is on a different branch (that silently no-ops or pushes a stale ref, stranding work). Override: `GT_ALLOW_OFFBRANCH_PUSH=1`.
- **Integration-landing guardrail** — a push to the default branch that introduces integration-branch content is blocked unless `GT_INTEGRATION_LAND=1`, which **only** `gt mq integration land` sets (`internal/cmd/mq_integration.go:721`).

The hook detects the default branch dynamically (origin/HEAD symref → origin/master → origin/main), matching the Go fallback in `git.RemoteDefaultBranch`.

### Layer 2 — Server-side CI ([.github/workflows/block-internal-prs.yml](../../../../../src/gastown/.github/workflows/block-internal-prs.yml))

Any PR opened **from the same repo** (not a fork, not renovate) is auto-closed with a comment instructing the author to merge to main directly. This is the GitHub-side mirror of the pre-push allowlist — internal PRs are structurally impossible to land.

### Layer 3 — The merge path: Refinery merge queue ([internal/refinery/engineer.go](../../../../../src/gastown/internal/refinery/engineer.go))

`polecat/*` branches reach the default branch **only** through the Refinery, which runs **two-phase quality gates** (`engineer.go:58-117`):

- **pre-merge gates** (`GatePhasePreMerge`, default) — validate the source branch on the target baseline, before the squash.
- **post-squash gates** (`GatePhasePostSquash`) — validate the *actual combined merged result* before pushing, catching issues that only appear after merge (broken imports, boot failures, missing templates). **On post-squash failure the merge is reset (rolled back) and never pushed.**
- Conflict policy is configurable per rig: `assign_back` or `auto_rebase`; optional `DeleteMergedBranches` (`MergeQueueConfig`, `engineer.go:103`).

This is the exact gate sequence the `/queue` skill automates: rebase → pre-merge gates → squash-merge locally → post-squash gates → push if green → hard-reset rollback if red. Before completion, `gt done` also auto-rebases onto the target ([internal/cmd/done_rebase.go](../../../../../src/gastown/internal/cmd/done_rebase.go)).

**Integration branches:** `gt mq integration` creates `integration/*` branches; `gt mq integration land` batch-lands them onto the default branch through a dedicated, transient `.land-worktree` (created via `WorktreeAddExistingForce`, removed after), pushing with `GT_INTEGRATION_LAND=1` (`internal/cmd/mq_integration.go:133-174, 721`).

### Layer 4 — Continuous hygiene: doctor checks + daemon dogs

The `gt doctor` suite (aggregated in `internal/doctor/rig_check.go`) keeps the topology valid:

- `branch_check.go` — role dirs are on their expected branches; detects clone divergence (worktree-aware checkout retry).
- `sparse_checkout_check.go` — disables legacy sparse-checkout to restore full checkouts.
- `land_worktree_gitignore_check.go` — ensures `.land-worktree` artifacts are git-ignored.
- plus `town_git_check`, `foreign_remote_check`, `worktree_gitdir_check`, bare-repo-exists and default-branch checks.

Two daemon **dogs** keep history healthy:
- **checkpoint dog** (`internal/daemon/checkpoint_dog.go`) — periodically commits polecat worktrees to preserve in-flight work.
- **compactor dog** (`internal/daemon/compactor_dog.go`) — surgical rebase + GC + fetch/verify + force-push to bound the Dolt-backed Beads history.

---

## Summary diagram

```
bare .repo.git ──worktrees──> { mayor*(separate clone), refinery(default), polecats(polecat/*),
                                crew(default), witness, deacon }

crew ── push default branch directly
polecats ── push polecat/* ──> Refinery merge queue
                                 (pre-merge gates → squash → post-squash gates → push | rollback)
integration: gt mq integration → integration/* → `land` into default via .land-worktree (GT_INTEGRATION_LAND=1)

enforced by:
  1. pre-push hook        (branch allowlist + HEAD guard + integration-land guard)
  2. CI block-internal-prs (forks only)
  3. Refinery gates        (two-phase, rollback on post-squash failure)
  4. doctor checks + dogs  (branch/worktree hygiene, history compaction)
```

**Net:** no internal feature-branch PRs; crew commit straight to the default branch, polecats fan out to `polecat/*` and are gated through the Refinery, and `integration/*` is the only sanctioned batch-land path — each rule backed by both a client hook and a server check.

---

## Key files reference

| Concern | File |
|---|---|
| Bare repo + rig worktrees | `internal/rig/manager.go` |
| Polecat worktree lifecycle | `internal/polecat/manager.go` |
| Worktree name pool / themes | `internal/polecat/namepool.go` |
| Git worktree plumbing | `internal/git/git.go` (`:1646-1852`) |
| hooksPath wiring | `internal/git/git.go` (`:755-776`) |
| Pre-push enforcement | `.githooks/pre-push` |
| Internal-PR blocking | `.github/workflows/block-internal-prs.yml` |
| Merge queue + gates | `internal/refinery/engineer.go` |
| Integration land | `internal/cmd/mq_integration.go` |
| `gt worktree` commands | `internal/cmd/worktree.go` |
| Worktree integrity | `internal/worktree/integrity.go` |
| Hygiene checks | `internal/doctor/{rig_check,branch_check,sparse_checkout_check,land_worktree_gitignore_check}.go` |
| WIP / history dogs | `internal/daemon/{checkpoint_dog,compactor_dog}.go` |
