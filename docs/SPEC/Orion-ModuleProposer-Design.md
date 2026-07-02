---
title: Orion-ModuleProposer-Design
issue: or-809
---

# or-809 — Semantic ModuleProposer, per-module LOOP, blocking AlignmentGate

V3 Step 3 of `docs/PRD/orion-v3.md`. Replace the mechanical decomposer with a SEMANTIC ModuleProposer (run in shadow, then cut over), wire the per-module LOOP, and make the AlignmentGate BLOCK (severity-tiered). Winner: Design 2 (Dimension-Checklist Hybrid) with grafts from Designs 1 and 3.

## Thesis

The orchestrator assumes the ModuleProposer (an LLM) is ADVERSARIAL. Its proposals never get the last word: three deterministic layers (ReconcileFloor, CoverageGate, proof-time EnforceObligations) form the trust wall, and `proof.Accept` remains the sole right-to-ship. The proposer runs in SHADOW alongside the fixed decomposer first; cutover is a deterministic assertion plus a measured window. The AlignmentGate can only ever REMOVE a green light.

## Hard invariants (held)

- **I1** — Proposer is adversarial. `decomposer.CoverageGate` stays the deterministic backstop (every Requirement + every RequiredCaseID → >=1 non-empty obligation), plus a new `ReconcileFloor` (floor dims proposer-independent) and proof-time `EnforceObligations`.
- **I2** — Shadow→cutover. Proposer runs in shadow; cutover criterion is `coverageSuperset(proposer,oracle) && floorOK && cluster non-regression` (deterministic) over a measured window (≥`ORION_MODULE_PROPOSER_SHADOW_MIN` runs). The V2 `Decompose` stays runnable as the permanent oracle; nothing deleted until the proposer ships a real epic.
- **I3** — AlignmentGate only ever removes a green light. The aligner is CALLED only when `report.Outcome.Verdict == "Accept"` (gate the call, not just the verdict). Severity-tiered: high=block (corroborated), medium=surface-to-human, low/none=pass. A Reject never reaches the aligner.
- **I4** — Per-module context is O(module); intent anchor is O(1) un-evictable (RecallSpec re-verifies the hash per plan; the loop re-grounds each module and re-reads `es.Intent`).
- **I5** — Reuse the proven spine (proof/truthalign/EnforceObligations, contextstore anchor+done-gate, delivery bar, native harness). No generalization beyond the HTTP proof path (that is or-3ba).
- **I6** — Incremental + reversible; BuildDAG keeps working throughout; every step is a single env flag defaulting to today's behavior.

## Architecture

### The ModuleProposer (`internal/decomposer/proposer.go`, new)

```
type ProposedModule struct { Key, Title, ProofObligation, FileScope string; Covers, DependsOn []string }
type ModuleProposer func(ctx, es spec.ExecutableSpec, projectType string, floor []completeness.Dimension) ([]ProposedModule, error)
NativeModuleProposer(provider llm.Provider) ModuleProposer   // tool-calling LLM, report_modules tool
Propose(ctx, es, projectType, floor, mp) (Epic, error)       // LLM propose + deterministic bookend synthesis
ReconcileFloor(floor, epic) error                            // floor dims present regardless of slicing
DefaultFloor() []completeness.Dimension                      // the 8 Dim* constants
CoverageDiff(proposer, oracle, es) (superset bool, missing []string)
```

The proposer emits SEMANTIC vertical slices (a feature end-to-end), converted 1:1 onto the existing `decomposer.Task` so `BuildDAG` runs unchanged. `Propose` deterministically SYNTHESIZES a bookend `acceptance` module (depends on every leaf; covers all floor dims + `RequiredCaseIDs()`; whole-intent obligation) — the composition defense.

### Trust wall (I1)

1. `ReconcileFloor(DefaultFloor(), epic)` — the 8 `completeness.Dim*` reliability dimensions must all be present, independent of `es.Dimensions` AND the proposer's `Covers` labels.
2. `decomposer.CoverageGate(es, epic)` UNCHANGED — every dimension + every `RequiredCaseIDs()` case → >=1 non-empty obligation.
3. `proof.EnforceObligations(requiredIDs, &r)` (`build.go:641`) — re-derives required case IDs from the SPEC; downgrades any verdict whose required cases did not execute+pass.

**Named residual (no design closes it):** the dimension check has no proof-time re-derivation analogue to `EnforceObligations` — a thin non-empty obligation labeled with all dims passes CoverageGate. ReconcileFloor narrows WHICH dims must appear, not obligation STRENGTH. Follow-up **or-809.dim-backstop**: assert each dimension obligation binds to a known deterministic probe.

### AlignmentGate (I3), `internal/conductor/build.go buildOneTask`

Behind `ORION_ALIGN_GATE=block` (default `advisory` = today's log-only). Only when proof=Accept is the aligner CALLED. Then: `high` (corroborated by a second pass; uncorroborated → downgraded to `medium`) → re-grind with the concern as feedback via the existing `feedback`/`maxBuildAttempts` loop, escalate on exhaustion via `Escalations().CreateDetailed`. `medium` → file an `align-review` escalation, close normally (surface-to-human, never auto-fail an under-specified spec). `low`/`none` → close. driftMonitor (`build.go:251`) unchanged: the run-level ≥3-concern DISPATCH pause is complementary (node gate stops one module; run gate stops a trend).

### Shadow→cutover (I2), `internal/orchestrator/spec_flow.go ensurePlan`

`Decompose` (oracle) is always run and, in slice 1, always persisted (byte-identical build). `ORION_MODULE_PROPOSER=shadow` ALSO runs `Propose` + `ReconcileFloor` + `CoverageGate` + `CoverageDiff` + cluster-count diff, persisting a `ShadowRecord{spec_hash, proposer/oracle module & cluster counts, superset_ok, floor_ok, coverage_gate_ok, missing[]}` — best-effort, never fails the plan. Cutover to `live` (follow-up or-809.cutover) requires the deterministic assertion live + ≥N clean recorded runs. `shadow` is instant rollback; the oracle runs forever as the live diff.

### External-issue path (Design 2 + Design 3, deferred to or-809.external)

`ExternalIssueProposer` reads `.beads/issues.jsonl` via a new `internal/tracker` Reader (contention-safe per the single-writer Dolt memory), `ChainProposer` merges internal + external with internal-decomposition backfill of uncovered floor dims; the union passes the same ReconcileFloor + CoverageGate. `orion run --from-beads <epic-id>` selects it. Interface fixed now; implementation deferred.

## First slice (tracer bullet)

SHADOW proposer + HIGH-only blocking align gate, on the existing HTTP path. Two flags, both default to today's behavior.

Files: NEW `internal/decomposer/proposer.go`; `internal/orchestrator/conductor.go` (injectable `proposer` field + `SetModuleProposer`); `internal/conductor/oriontools.go` (~L353, set proposer when provider present); `internal/orchestrator/spec_flow.go` `ensurePlan` (shadow slot, persist oracle unchanged); `internal/conductor/build.go` `buildOneTask` (gate-the-call + severity routing + corroboration behind `ORION_ALIGN_GATE=block`).

Gates: full suite + lint + build; ReconcileFloor/CoverageDiff/bookend unit tests; shadow no-op plan-equality invariant; align I3 tests (spy call-count = 0 on Reject; block-branch re-grind; corroboration downgrade). New `shadow_plans` table.

## Out of slice (where each lands)

- Cutover to `live` → or-809.cutover (after the measured window).
- External-issue ingestion + `internal/tracker` Reader + `--from-beads` → or-809.external.
- Dimension proof-time backstop → or-809.dim-backstop.
- `ORION_ALIGN_GATE=block` as DEFAULT (needs wider corpus) → or-809.aligncorpus.
- Generalization beyond HTTP → or-3ba (I5).
- Deleting the V2 template → never (permanent oracle per PRD §6).

## Migration

Step A: add `proposer.go` (pure new code, template still live). Step B: wire shadow into `ensurePlan` behind `ORION_MODULE_PROPOSER=shadow` (default off) + `SetModuleProposer` in `oriontools.go`; persist `ShadowRecord`s. Step C (or-809.cutover): after the window asserts superset+floor+cluster non-regression, flip to `live`; template retained as oracle. Step D: flip the align gate to blocking behind `ORION_ALIGN_GATE=block` (default advisory). Each step is a single flag, independently revertable.

## Risks

1. **Superset necessary, not sufficient** — proves labels/cases covered, not that slices compose to intent or that obligations are as STRONG as the template's. Defense: bookend acceptance module + assembled-tree ProveAll + grill quality. Residual; tracked on or-809.cutover.
2. **Blocking a non-deterministic judge** (validated on 3 cases) — the single biggest new correctness risk. Defense: gate the call on Accept (I3), high-only, two-pass corroboration with downgrade-to-medium, medium=surface default, `ORION_ALIGN_GATE` default advisory. Widening the corpus before default-on is or-809.aligncorpus.
3. **FileScope-collision / cluster collapse** — a proposer free to re-slice can emit FileScopes that collapse the DAG to one cluster (`build.go:172-175` on `Cluster()` error), silently killing parallelism. Defense: cluster-count in the shadow record; cutover requires cluster non-regression, not just coverage superset.
4. **Dimension label-trust** — a thin non-empty obligation labeled with all dims passes CoverageGate. Defense (deferred): or-809.dim-backstop.