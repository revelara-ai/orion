---
title: Orion Worktree & Git Handling (multi-agent editing)
status: draft
authors: Joseph Bironas
created: 2026-06-18
derived_from:
  - docs/research/Gastown-Worktree-Handling.md
  - docs/PRD/orion-v2.md (Phase B repo/worktree; Phase E2 integration; modules sandbox/integration)
---

# Orion Worktree & Git Handling

> How Orion isolates concurrent agent edits and reconciles them back to `main`. Adapted from Gastown's worktree model, **simplified**: no polecat/refinery/rig hierarchy, no themed name pool. One worktree per task, named by the issue id. We keep the parts that earned their place — shared object store, a git-plumbing wrapper, deletion-safety gates, and filesystem-as-source-of-truth reconciliation.

## 1. Principles (what we keep, what we drop)

**Keep (from Gastown):**
- **Shared object store, not clones.** A worktree is added off the developer's existing repo (`git worktree add`), so objects are shared — fast, cheap, no re-clone.
- **A git-plumbing wrapper library** (`worktree` module) — the only thing that shells out to `git worktree …`; everything else calls it.
- **Heavily safety-gated deletion** — never destroy unmerged/uncommitted work; never remove a worktree mid-integration; best-effort preserve work before removal.
- **Filesystem as source of truth + reconciliation** — the authoritative in-use set is not persisted; reconcile from `git worktree list` + the filesystem on startup/allocation.

**Drop (Gastown complexity Orion doesn't need):**
- **No polecat / refinery / mayor / rig layers.** Orion has one structure: the main repo + per-task worktrees.
- **No themed NamePool / flock / `.pending` TOCTOU reservation.** The **beads issue id is the unique name** — deterministic, globally unique, traceable. This removes an entire subsystem (name allocation, theme hashing, pool locks).
- **No nested `<rigname>/` directory segment.** Flat sibling layout.

## 2. Filesystem layout & naming

```
<parent>/<repo>                         ← main repo, on `main` (the developer's working copy)
<parent>/worktrees-<repo>/<issue-id>    ← one worktree per active task
```
Concretely (matches current practice):
```
…/revelara-ai/orion                     c4a16c5 [main]
…/revelara-ai/worktrees-orion/or-9xl    [or-9xl]
```
- **Worktree dir:** `worktrees-<repo>/<issue-id>` (sibling to the repo; keeps the main working tree clean and is trivially `.gitignore`-irrelevant since it's outside the repo).
- **Branch name:** `<issue-id>` by default (bare, traceable to the task). Configurable via a `worktree.branch_template` (tokens: `{issue}`, `{repo}`, `{timestamp}`) for teams that want e.g. `orion/{issue}` — but the default is the bare issue id, matching current practice.
- **One worktree ⟷ one task ⟷ one branch.** This is the isolation unit; agents never share a working tree.

## 3. Creation (Phase B3/B4)

When a task is claimed and ready to execute:
1. `worktree.Create(issueID, startPoint)` → wraps `git worktree add <worktrees-repo>/<issueID> -b <branch> <startPoint>` (LFS smudge skipped, as Gastown does). **Start point** = the configured integration base (default `main`; `origin/main` if a remote exists). **Resume** reattaches the existing branch via an add-existing-force path instead of `-b`.
2. The worktree path + branch are recorded **transactionally** in the Context Store (`worktrees` entity / `tasks.worktree_path`, `tasks.branch`) so a crash leaves a recoverable record (ties to Harness Reliability crash-safe writes).
3. The **sandbox** (gVisor, per the Security Requirements) mounts *this worktree's scoped workdir* as the agent's only writable filesystem — the worktree is the agent's world.

## 4. The `worktree` module (git-plumbing wrapper)

A deterministic primitive — the single chokepoint for `git worktree` operations; no other module shells out to git worktree. Interface:

- `Create(issueID, startPoint) → Worktree` / `CreateResume(issueID, branch) → Worktree`
- `List() → []Worktree` (wraps `git worktree list --porcelain`)
- `Remove(issueID, opts) → error` (safety-gated; see §6)
- `Prune()` (wraps `git worktree prune`)
- `Reconcile()` (see §7)
- `Status(issueID) → {uncommitted, unpushed, ahead, behind, clean}` (live `git status`/rev-list)

It is a **functional primitive** in the Composition Model (deterministic; the prover verifies it with shell/`git` assertions, not LLM judgment).

## 5. Lifecycle

```
claim task → Create worktree (off main) → agent works (sandboxed in worktree)
   → proof runs against the worktree artifact → INTEGRATE (Phase E2) → Remove worktree
```
- **Work:** the agent edits only inside its worktree; proof (behavioral/empirical/hazard) runs against that worktree's artifact.
- **Integrate (Phase E2, V2.1):** the `integration` module rebases the worktree branch onto the current integration head, runs pre-merge gates, merges, **re-proves on the merged tree**, advances head or rolls back. Path **leases** (declared file scope per task) prevent two worktrees from racing on the same files; the serialized integration queue merges one at a time. (Full detail in the PRD "Phase E2 Reliability & Recovery".)
- **Remove:** on integration success (or explicit abandon), the worktree is removed via the safety-gated path (§6).

## 6. Deletion safety (mapped from Gastown `RemoveWithOptions`)

`worktree.Remove(issueID, opts)` MUST, in order:
1. **Take a per-worktree lock**; refuse if the worktree is unknown.
2. **Refuse on unsafe work** unless `--force`: block if there are uncommitted / unpushed / un-integrated commits. Trust the Context Store's delivery/integration state first, fall back to live `git status` + `rev-list`.
3. **Refuse if mid-integration** — if the task is in the integration queue or holds an active lease, refuse even under `--force` (don't yank a worktree out from under a merge). Releasing requires the integration to finish or be cancelled first.
4. **Refuse self-removal** — if the developer's shell `cwd` is inside the worktree, refuse (unless it's an explicit self-nuke from elsewhere).
5. **Reset task/lease state before touching the filesystem** — unassign the task and release leases first, so a concurrent re-allocation can't collide with the half-removed dir.
6. **Best-effort preserve work** — commit/push any unpushed commits (or persist a patch into the Context Store) before deletion, so work is never silently lost.
7. **Remove** — `git worktree remove` → fall back to `os.RemoveAll` → also remove the (now-empty) parent if git left overlay/untracked files → `git worktree prune` → verify removal complete.

## 7. Reconciliation (filesystem as source of truth)

`worktree.Reconcile()` runs on **startup** and before **allocation** — the in-use set is never persisted authoritatively; it is derived. It:
- `git worktree prune` for manually-deleted directories.
- **Orphan sandboxes** (sandbox/process exists, no worktree dir) → kill (reuses the Harness-Reliability signal-cleanup / process-group reaping).
- **Stale worktrees** (dir exists, but the agent's heartbeat is dead / PID gone) → reclaim: best-effort preserve, then remove.
- Remove **incomplete worktrees** missing `.git`, and **empty** worktree dirs with no checkout.
- Cross-check the Context Store `worktrees` records against `git worktree list`; repair drift (a recorded worktree with no dir → mark gone; a dir with no record → adopt or remove).

An optional out-of-band **`orion doctor`** audit (mapping Gastown's doctor checks) detects/repairs broken or migrated worktree structures; it is not on the runtime create/cleanup path.

## 8. Crash safety & Context Store integration

- Worktree create/remove and their Context Store records commit as one transactional unit (idempotency key = issue id) — a crash mid-create/mid-remove is repaired by §7 on next startup, never leaving an orphaned dir or a dangling record silently.
- On `SIGINT`/`SIGTERM`, the signal handler (Harness Reliability) cancels in-flight agents, reaps sandbox process groups, and leaves worktrees in a reconcilable state (it does **not** force-delete unmerged work — that's §6's job with its gates).

## 9. Concurrency model (how this prevents multi-agent collisions)

- **Isolation:** one worktree per task → agents physically cannot edit each other's files mid-build.
- **Collision avoidance at integration:** the decomposer partitions tasks to minimize file-scope overlap; **path leases** (declared file scope) make an overlapping task *wait* rather than edit concurrently; the **serialized integration queue** (singleton lock) merges one worktree to the integration head at a time, each with a mandatory **re-proof on the merged tree**; conflicts dispatch the resolver or escalate; red post-merge proof rolls back. (This is the PRD Phase E2 layer; the worktree module is its substrate.)

## 10. Phasing

| Capability | Phase |
|---|---|
| `worktree` wrapper: Create/List/Remove(safety)/Prune/Reconcile; one worktree per task off `main`; sandbox mounts the worktree | **V2.0** (skeleton — `t-sandbox` depends on it) |
| Startup reconciliation + crash repair + signal-cleanup integration | **V2.0** |
| Path leases + serialized integration queue + rebase/re-proof/rollback + resolver | **V2.1** (Phase E2 / `ep-v21`) |
| GitHub-Issues tracker projection alongside worktree branches; PR delivery | **V2.1** |

## 11. Gastown → Orion mapping

| Gastown | Orion | Note |
|---|---|---|
| `internal/git` worktree wrapper | `worktree` module | kept; deterministic primitive |
| `internal/polecat/manager` lifecycle | folded into `worktree` (create/remove/reconcile) + `integration` (merge) | no separate polecat entity |
| themed `NamePool` + flock + `.pending` | **issue id as the name** | whole subsystem dropped |
| `<rig>/polecats/<name>/<rigname>/` | `worktrees-<repo>/<issue-id>` | flat, no nesting |
| shared bare `.repo.git` | the developer's existing repo | local-first; objects already present |
| `RemoveWithOptions` safety gates | §6 deletion safety | mapped 1:1 |
| `ReconcilePool` (FS source of truth) | §7 `Reconcile()` | mapped; tmux→sandbox/process-group |
| tmux orphan/stale session cleanup | sandbox/process-group reaping (Harness Reliability) | same idea, Orion's runtime |
| doctor checks | optional `orion doctor` | out-of-band audit |
| refinery / mayor / rig worktrees | — | dropped |
