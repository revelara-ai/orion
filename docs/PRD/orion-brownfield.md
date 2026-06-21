# Orion Brownfield: Proof-Gated Change

## Thesis

Orion's proof + validation layers should apply not just to from-scratch services
(greenfield) but to **changes in existing repositories** (brownfield). Completion is
still independent multi-modal proof — but the unit of work and the baseline differ.

## The unification: Orion operates on a target git repo

Today the proof machine assumes a from-scratch, self-contained HTTP service: the
generator writes a fresh `main.go` with a fixed `handleTime`, the behavioral proof
calls that symbol, the empirical proof boots it. Greenfield and brownfield are the
**same machine** with a different baseline:

- **Greenfield** — no prior code. The change *is* the whole artifact. The spec is the
  sole ground truth.
- **Brownfield** — existing code + tests. The change is a *diff*. The proof has **two
  masters**: the new behavior (the spec) AND the preserved behavior (existing tests
  stay green).

What unifies them is **git**: Orion operates on a target repo. An Accept becomes a
**commit of proven code**. Greenfield `git init`s a fresh one (or commits into the
current repo); brownfield branches off an existing one. Same plumbing both ways.

## Decisions (2026-06-21)

- **Greenfield delivery**: commit proven code **into the repo the developer is working
  in** (a subpath + branch), not a separate per-service repo. The existing export
  (`internal/conductor/output.go`) becomes a git commit.
- **Next focus**: build the **brownfield foundation** directly (not the greenfield git
  layer first).

## The proof generalizes

| Tier | Greenfield (today) | Brownfield |
|---|---|---|
| Behavioral | synth corpus calls `handleTime` | **regression** (repo's own tests stay green) + **new** (synth corpus targets the changed surface) |
| Empirical | boot the fresh service | run the repo's existing integration/app tests |
| Hazard | STPA on the service | STPA on the **change's blast radius** |
| Alignment | diff serves intent | same, but *more* critical — a change can pass tests yet betray intent |

**Reusable** (the payoff of the V1–V3 core): requirements→obligations, EnforceObligations,
the refinement loop, the AlignmentGate, the trust wall (synth corpus for *new* behavior),
safeenv.

**New engineering:**
1. **Repo introspection** (or-3ba): detect language + build/test commands; capture the
   green-before baseline.
2. **Diff as the unit**: the generator *edits* the existing repo (reads real files,
   surgical changes) instead of writing fresh. It's the user's real code, so the bwrap
   sandbox (or-5ym) stops being optional.
3. **Proof surface generalizes off `handleTime`**: the change declares what to exercise
   (route / function / CLI), or the proof leans on the repo's own tests + new
   synthesized tests targeting the diff.
4. **Regression gate** (green→green): arguably the most important brownfield gate, and
   genuinely new (greenfield has no "before").

### Core invariant

Greenfield's ground truth is the spec; brownfield's ground truth is the **existing
code + tests**, and the change must *preserve* them while *adding* the spec'd behavior.

## Increment roadmap

- **1 — Regression baseline** ✅ (`internal/brownfield`, `orion baseline <dir>`).
  Detect a repo's toolchain (Go first) and run its existing tests to capture
  green/red, with the safeenv security boundary proven (host secrets never reach the
  untrusted repo's tests). This is the "before" half of green→green.
- **2 — Change unit (diff generator)**: a generator that edits an existing repo to a
  change intent (reads real files, writes surgical edits under the sandbox), producing
  a diff rather than a fresh service.
- **3 — Proof surface generalization** (or-3ba): let the spec/change declare the
  surface to exercise; behavioral/empirical target it instead of the fixed `handleTime`.
- **4 — New-behavior obligations on the diff**: synthesize a corpus for the change's
  stated behavior (the requirements model), targeting the changed surface; the trust
  wall holds (harness authors the corpus).
- **5 — Full change-proof flow + git commit**: regression (green→green) + new behavior
  + alignment + hazard → Accept → commit the proven diff to a branch in the target repo.

## Security

Running a target repo's code (its tests, its build) is running UNTRUSTED code. Every
exec uses `internal/proof/safeenv` (deny-by-default env) so the host environment —
which holds `ANTHROPIC_API_KEY` — never reaches it. Full process isolation
(network/fs via bwrap) is or-5ym, and it becomes mandatory once the diff generator
edits real repos.
