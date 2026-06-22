---
title: Git Handling Foundation — Managed Repo + Consolidated Worktree Lifecycle
status: approved-design
authors: Joseph Bironas (with Claude)
created: 2026-06-22
derived_from:
  - docs/SPEC/Orion-Worktree-Git.md
  - docs/research/Gastown-git-analysis.md
  - docs/PRD/orion-v2.md (Phase B repo/worktree; modules sandbox/integration)
scope: Foundation cycle only — managed-repo setup + one consolidated worktree
       lifecycle + agent↔worktree binding + the SPEC gaps. Integration (Phase E2)
       and the managed→developer delivery carve are explicit follow-on cycles.
---

# Git Handling Foundation

> How Orion gets the git repo its agents build in, and how it isolates concurrent
> agent edits in worktrees off that repo. Adapted from Gastown's worktree model
> (`docs/research/Gastown-git-analysis.md`), redesigned for Orion's proof-gated,
> local-first control plane.

## 1. Decisions (locked)

Two decisions were made before this design and govern everything below:

1. **Repo model = always a managed repo (Gastown-style).** Orion owns a git repo
   per project. Greenfield: `git init`. Brownfield: clone the target. All agent
   worktrees derive off it; **nothing touches the developer's working tree until
   delivery.** This kills the greenfield "no repo → fail" friction the current
   `GitRoot(cwd)` model forced, and gives the strongest isolation.

2. **Scope = foundation first.** This cycle delivers the managed-repo substrate +
   one consolidated worktree lifecycle + the agent binding + the gaps I found in
   the existing SPEC. The merge-back **integration** workflow (path leases,
   serialized queue, rebase, re-prove, rollback — PRD Phase E2) and the proper
   **managed→developer delivery** carve are their own later cycles.

## 2. Problem (current friction)

- The repo is "worktrees of the developer's cwd repo" (`GitRoot(ctx, ".")` in
  `internal/conductor/build.go:139`). Greenfield has no repo → the build had to
  *fail* or assume an empty repo, and building into the dev's repo risks mixing
  the generated service with their files.
- **Three** worktree-creation paths have grown up: `worktree.Manager`, my
  `clusterWorktreeSet` (`internal/conductor/dagworktree.go`), and `GitDeliver`
  (`internal/conductor/gitdeliver.go`). The SPEC says `worktree` is the single
  chokepoint; reality drifted.
- Loose git plumbing (`GitRoot`, `gitIn`) lives at the conductor layer.
- The existing SPEC (`docs/SPEC/Orion-Worktree-Git.md`) has unbuilt gaps: §6.6
  preserve-work-before-delete, §7 process/heartbeat reaping, the `Status` fields.

## 3. Architecture

A thin new `internal/repo` layer resolves the managed repo and feeds the existing
worktree chokepoint. Nothing else changes its shape.

```
internal/repo (NEW)          resolves/owns the managed repo per project
  Resolve(ctx, store, intake) → Repo{Path, Base}
  Path = <store.Dir()>/repo · Base = "main" (greenfield) | cloned default (brownfield)
        │ Path + Base
        ▼
internal/worktree.Manager    THE single chokepoint for `git worktree …` (enhanced)
  New(repo.Path, store).WithBase(repo.Base)
  Create / Reconcile / Remove(preserve-work) / Status(+Behind)
        │ wt.Path = the agent's only writable workdir
        ▼
internal/conductor.BuildDAG  repo.Resolve → manager → worktree-per-cluster → build/prove
internal/sandbox             mounts wt.Path as the agent's only writable root
```

**Why a separate `repo` package** (vs folding repo-init/clone into
`worktree.Manager`): "where is the project's repo and what's its integration base"
is a tiny, deterministic, shell-verifiable concern. Its own ~60-line package keeps
it independently testable and keeps `worktree.go` (already ~340 lines doing one job
— isolation) focused. Boundaries over fewer-files.

## 4. Components

### 4.1 `internal/repo` (new keystone)

```go
package repo

// Intake distinguishes how the managed repo is seeded.
type Intake struct {
    Brownfield bool   // false = greenfield (init); true = clone Source
    Source     string // brownfield only: path/URL of the target repo
}

// Repo is the resolved managed git repo a project's worktrees branch off.
type Repo struct {
    Path      string // <store.Dir()>/repo
    Base      string // integration base: "main" (greenfield) | default branch (brownfield)
    ProjectID string
}

// Resolve returns the project's managed repo, creating it if absent. Idempotent:
// an existing valid repo at the path is reused (re-resolve returns it unchanged).
func Resolve(ctx context.Context, store *contextstore.Store, intake Intake) (Repo, error)
```

- **Path** = `filepath.Join(store.Dir(), "repo")` — colocated with the project's
  Context Store; one managed repo per project. `ProjectID` from
  `store.CurrentProjectSpec(ctx).ID`.
- **Greenfield** (`Intake{}`): `git init -b main`; set the Orion commit identity;
  one empty initial commit on `main` (`commit --allow-empty`) so `main` is a real
  branch worktrees can branch off. Idempotent via `git -C <path> rev-parse
  --git-dir`.
- **Brownfield** (`Intake{Brownfield:true, Source:...}`): `git clone <source>
  <path>` (local source → shared objects), `Base` = the cloned default branch
  (`git -C <path> rev-parse --abbrev-ref HEAD`). Greenfield is the must-have this
  cycle; brownfield clone is the parallel branch of the same `Resolve`.
- **Deterministic primitive** in the Composition Model — proven with shell/git
  assertions, not LLM judgment (same status as `worktree`).

### 4.2 `internal/worktree.Manager` (enhanced — closes SPEC gaps)

Constructed off the managed repo, not `GitRoot(cwd)`. Three additions:

- **§6.6 preserve-work-before-delete.** In `Remove`, after the safety gates and
  before any filesystem deletion, when `opts.Force` and the tree is dirty,
  best-effort snapshot it as a WIP commit on the branch:
  `git add -A` then `commit --no-verify -m "orion: WIP snapshot before worktree
  removal"` with the Orion identity. A `--force` removal then never silently loses
  work — it's recoverable on the branch / via reflog in the managed repo. Failures
  are logged, never block the removal (crash-safety wins).
- **§4 `Status.Behind`.** Add `Behind int` (`git rev-list --count HEAD..<base>`)
  for rebase/integration decisions. `Clean` stays `!Uncommitted && Ahead==0`
  (behind is informational, not dirty). "Unpushed" is dropped: the managed repo has
  no remote in this model, so it's moot — noted, not implemented.
- **§7 reaping seam.** `Reconcile` gains an injectable liveness predicate
  `WithLivenessCheck(func(issueID string) bool)` (default: always-alive → no
  reaping). When an owner is not alive, reclaim its worktree (preserve-work then
  remove). This cycle wires the **seam + default + test**; the heartbeat/PID
  source is the agentruntime's to wire later, with no `Reconcile` rewrite needed.

### 4.3 `internal/conductor` rewire (consolidate the build substrate)

- **`BuildDAG`**: replace the `GitRoot(ctx,".")` → fail block
  (`build.go:139-142`) with `r, err := repo.Resolve(ctx, store, intake)` then
  `worktree.New(r.Path, store).WithBase(r.Base)`. **Greenfield never fails and
  never touches the developer's tree.** `intake` is greenfield by default;
  brownfield is derived from the existing `internal/brownfield` classification when
  a target is present.
- **`clusterWorktreeSet`**: base off `r.Base`, not the literal `"HEAD"`.
- **`GitDeliver` — untouched this cycle.** It is already guarded by `GitRoot(ctx,
  ".") != ""` (`build.go:252`), so greenfield-with-no-dev-repo already *skips*
  delivery. The build substrate moving to the managed repo does not change
  delivery: when the developer is in a repo and opts in (`ORION_GIT_DELIVERY`),
  proven code still lands on an `orion-<slug>` branch in *their* repo. The proper
  managed→developer carve (export from managed `main`, PR, handle the
  no-dev-repo case as a first-class delivery rather than a skip) is the delivery
  cycle. `GitRoot`/`gitIn` consolidation into `repo` also defers to then.

### 4.4 Agent↔worktree binding (formalized invariant)

One cluster ⟶ one `Manager.Create(clusterID, base)` ⟶ `wt.Path` is the agent's
writable root ⟶ the sandbox mounts **only** `wt.Path`. Mostly wired already
(`buildOneTask` points `buildDir` at `wt.Path`); this cycle makes it uniform, off
the managed repo, and records it as the isolation invariant: agents physically
cannot edit each other's files, and no agent can write outside its worktree.

## 5. Data flow

**Greenfield build (the primary path):**
```
submit spec → BuildDAG
  → repo.Resolve(store, Intake{})        # git init <store.Dir()>/repo @ main (idempotent)
  → decompose + cluster
  → per cluster: Manager.Create(off main) # worktree on branch <clusterID>
  → agent builds in wt.Path (sandboxed)   # only writable path
  → prove (behavioral + empirical + hazard)
  → [integration = next cycle]
  → Reconcile / Remove(preserve-work)
# the developer's working tree is never touched
```
**Brownfield** differs only in `repo.Resolve(Intake{Brownfield, Source})` cloning
the target and `Base` = its default branch.

## 6. Error handling

- `repo.Resolve`: init/clone failure → hard error (no base ⇒ no build). Idempotent
  re-resolve on an existing valid repo.
- `worktree.Manager`: existing safety gates unchanged; preserve-work is
  best-effort (log on failure, never block). Crash mid-create/remove → §7
  `Reconcile` repairs from the filesystem-as-truth on next startup — contract
  unchanged.

## 7. Testing (TDD; deterministic git assertions)

- `internal/repo`:
  - `TestResolveInitsManagedRepoForGreenfield` — creates `<store.Dir()>/repo` with
    `main` + an initial commit; re-resolve is idempotent (no second commit).
  - `TestResolveClonesBrownfieldTarget` — clones a source repo; `Base` = its
    default branch; objects shared.
- `internal/worktree`:
  - `TestRemovePreservesUncommittedWorkAsWipCommit` — `--force` over a dirty
    worktree leaves a recoverable WIP commit on the branch before deletion.
  - `TestStatusReportsBehind` — base ahead of branch ⇒ `Behind > 0`.
  - `TestReconcileReclaimsStaleViaLivenessHook` — inject a dead-owner predicate ⇒
    the stale worktree is preserved + reclaimed.
- `internal/conductor`:
  - `TestBuildDAGResolvesManagedRepoNotCwd` — with **no** cwd git repo, the build
    resolves/uses the managed repo and succeeds; the cwd is left untouched.

## 8. Scope / non-goals (explicit follow-on cycles)

- **Integration (Phase E2):** path leases, serialized integration queue,
  rebase-onto-head, re-prove on the merged tree, rollback-on-red, resolver. Out of
  this cycle; the `inIntegration` hook (`worktree.go:73`) stays a stub. (Tracked by
  the existing `or-tcs.1.5` + the reliability epic.)
- **Managed→developer delivery carve:** export from managed `main`, PR creation,
  no-dev-repo as first-class. Out; `GitDeliver` stays as-is.
- **`orion doctor`** out-of-band audit. Out; `Reconcile` covers the runtime path.
- **branch_template config.** YAGNI this cycle; the bare `<issue-id>` default
  stands.

## 9. Mapping to the existing SPEC

This refines `docs/SPEC/Orion-Worktree-Git.md` rather than replacing it. The SPEC's
"shared object store / single git-plumbing chokepoint / deletion-safety gates /
filesystem-as-truth reconciliation" all hold. The one substantive change: the
shared object store is now a **managed repo Orion owns** (`<store.Dir()>/repo`),
not the developer's existing repo — resolved by the new `internal/repo` package.
The SPEC's §6.6 and §7 gaps are closed here; its §4 `Status` gains `Behind` (and
formally drops `unpushed` for the no-remote managed model).

## 10. Future adjustments

Approved as "good enough for now; adjustable when we find issues." Likely first
adjustments: the managed→developer delivery boundary (when delivery UX matters),
and the liveness source for §7 reaping (when the agentruntime grows heartbeats).
