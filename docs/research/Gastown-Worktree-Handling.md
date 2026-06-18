How Gastown handles git worktrees

source in: /home/josebiro/src/gastown/


Gastown uses git worktrees as the isolation mechanism for polecats — its ephemeral per-task worker agents. They all share one bare object store and differ only by branch + directory. Two layers are involved:

Git plumbing — internal/git/git.go:1646-1852 wraps git worktree add/remove/move/prune/list.
Lifecycle — internal/polecat/manager.go owns creation, naming, and cleanup. (Rig-level worktrees for refinery/mayor live in internal/rig/manager.go; the per-worker story below is the polecat one.)
Creation
A polecat worktree is added off the shared bare repo <rig>/.repo.git (falling back to mayor/rig) rather than a fresh clone, so objects are shared — fast and cheap (repoBase, manager.go:462-478).

The core call is WorktreeAddFromRef(clonePath, branchName, startPoint) (manager.go:803, wrapping git.go:1664 → git worktree add -b <branch> <path> <startPoint>, with the LFS smudge filter skipped). The start point is normally origin/<defaultBranch>, or an explicit BaseBranch, or an existing PR head when resuming (manager.go:780-786). A resumed polecat instead reattaches an existing branch via WorktreeAddExistingForce.

Naming convention
Directory layout (clonePath, manager.go:495-514):


<townRoot>/<rig>/polecats/<name>/<rigname>/   ← the worktree (current structure)
<townRoot>/<rig>/polecats/<name>/             ← legacy fallback
The nested <rigname>/ segment exists deliberately to give the agent a recognizable repo-named directory.

Worker name (<name>) — drawn from a themed NamePool (namepool.go), not random:

Bounded pool of 50 slots; themes are mad-max (default), minerals, wasteland, plus custom theme files. Names like furiosa, nux, obsidian (namepool.go:45-82).
Each rig gets a deterministic theme via name hash, and ThemeForRigAvoiding keeps sibling rigs on different themes so names stay globally distinct (namepool.go:432-482).
Allocation is in-order, first-free; on exhaustion it overflows to plain numbers 51, 52, … (Allocate, namepool.go:270). Infra names (witness, mayor, refinery, etc.) are reserved/filtered (namepool.go:35). Allocation is guarded by a pool flock plus a <name>.pending reservation marker to close the TOCTOU window before the directory exists (AllocateName, manager.go:1369).
Branch name (buildBranchName, manager.go:555) — default format:


polecat/<name>/<issue>@<timestamp>   (when an issue is hooked)
polecat/<name>-<timestamp>           (otherwise)
where <timestamp> is base-36 epoch-millis. Fully overridable via a polecat_branch_template config supporting {user} {year} {month} {name} {issue} {description} {timestamp}.

Cleanup
Two paths — explicit removal and automatic reconciliation.

Explicit removal (gt polecat nuke / gt done) — RemoveWithOptions, manager.go:1124. It is heavily safety-gated:

Per-polecat flock; refuse if not found.
Block on uncommitted/unpushed/stashed work unless force/nuclear (trusts the polecat's self-reported cleanup_status bead first, falls back to live git status) (:1142).
Refuse if the polecat has an open MR in the merge queue — even nuclear mode (:1174).
Refuse if your shell is cd'd inside the worktree, unless self-nuking (:1210).
Reset the agent bead and unassign work beads before touching the filesystem (avoids races with a concurrent re-allocation of the same name) (:1185).
Best-effort push of any unpushed commits before deletion so work isn't lost (:1230).
WorktreeRemove → fall back to os.RemoveAll → also RemoveAll the parent dir (git can leave untracked overlay/.beads files) → WorktreePrune → verifyRemovalComplete → release the name back to the pool (:1248-1301).
Automatic reconciliation — ReconcilePool runs on allocation and startup (:1907). It treats the filesystem as source of truth (the in-use set is never persisted — "ZFC"):

git worktree prune for manually-deleted dirs (:1959).
Kill orphan tmux sessions (session exists, no directory) and stale ones (directory exists but the agent's heartbeat is stale / PID dead) (ReconcilePoolWith, :1973; liveness via isSessionProcessDead, :2018).
cleanupOrphanPolecatState (:2067) removes: stale .pending markers older than 5 min, empty polecat dirs with no clone, and incomplete worktrees missing .git.
There's also a third, separate auditing layer: the doctor checks (internal/doctor/rig_check.go, worktree_gitdir_check.go, land_worktree_gitignore_check.go) that detect and repair broken/migrated worktree structures out-of-band — but the runtime create/cleanup lifecycle is the polecat manager above.