# ADR-0002: Semantic ModuleProposer, per-module loop, blocking AlignmentGate

- **Status:** Accepted — **provisional** (expect follow-up tweaks; see "Revisit triggers")
- **Date:** 2026-07-02
- **Deciders:** Joseph Bironas
- **Epic / slice:** or-7et (V3 Anchored Module Pipeline) → or-809 (Step 3); tracer slice = shadow proposer + high-only blocking align gate
- **Related:** [docs/SPEC/Orion-ModuleProposer-Design.md](../SPEC/Orion-ModuleProposer-Design.md); [docs/PRD/orion-v3.md §3–§6](../PRD/orion-v3.md); [docs/research/Backlog-harness-gap-audit-2026-07-01.md](../research/Backlog-harness-gap-audit-2026-07-01.md); or-97z (beads intake); or-3ba (proof generalization, kept separate)

## Context

V2 decomposes a ratified spec with a **fixed 6-task template** (`decomposer.Decompose` → scaffold/handler/capacity/observability/operability/security). Two consequences the audit flagged: it cannot slice an arbitrary intent into semantic vertical modules, and it cannot consume an externally-sourced issue set as the work plan. Separately, the **AlignmentGate** (`internal/conductor/align.go`) — the adversarial LLM judge that catches "passes the cases, serves the wrong intent" drift — is **advisory/log-only**; the audit rates that the top drift blocker.

or-809 is V3 Step 3: replace the mechanical decomposer with a **semantic ModuleProposer** (shadow → cutover), wire the per-module loop (already a real DAG since or-tcs.1), and make the AlignmentGate **block**, severity-tiered.

## Decision

Adopt the **Dimension-Checklist Hybrid** design (3-design judge panel; full design in the SPEC doc). Load-bearing choices:

1. **The proposer is assumed adversarial.** Its proposals never get the last word. Three *deterministic* layers form the trust wall, none trusting the proposer's labels:
   - **ReconcileFloor** (new) — the reliability-floor dimensions (`completeness.Dim*`) must all be present regardless of how the LLM sliced, independent of both `es.Dimensions` and the proposer's `Covers` labels.
   - **CoverageGate** (unchanged) — every requirement + case ID → ≥1 obligation.
   - **EnforceObligations** (proof time) — re-derives required case IDs from the spec; a false coverage label dies when the case fails to execute.
2. **AlignmentGate blocks by gating the aligner *call* on `proof==Accept`** — a Reject→Accept path is structurally unreachable (the "only ever removes a green light" invariant). Severity-tiered: `high` blocks only when a two-pass corroboration agrees (else downgraded to `medium`); `medium` surfaces to a human and never auto-fails an under-specified spec. Behind `ORION_ALIGN_GATE` (**default advisory** = today's behavior).
3. **Shadow → cutover.** The proposer runs in shadow alongside the oracle `Decompose`, which keeps driving the build; a `shadow_plans` record captures coverage-superset, floor, and **cluster-count** diffs. Cutover (a later slice) requires coverage-superset **and** cluster-count non-regression over a measured window. The template is never deleted — it stays the permanent oracle/diff target (PRD §6). Behind `ORION_MODULE_PROPOSER` (**default off**).

The tracer slice ships shadow + the gated blocking path only: **at defaults it changes nothing.**

## Consequences

**Positive**
- The proposer can be exercised and measured against the proven template with zero behavior change at rest; the align gate can catch real drift the cases miss, on demand.
- The trust wall is deterministic and proposer-independent; the proven spine (proof/truthalign/EnforceObligations, contextstore anchor+done-gate, delivery bar, native harness, the DAG loop) is untouched.

**Negative / risks**
- **Blocking a non-deterministic judge** (validated on 3 cases) is the biggest new correctness risk; mitigated by call-gating on Accept, high-only, two-pass corroboration with downgrade, medium=surface, and default-advisory.
- **Coverage superset is necessary, not sufficient** — it proves labels/cases covered, not that slices compose to intent; mitigated by a deterministic bookend acceptance module + the assembled-tree ProveAll, and named as residual.
- **Dimension coverage is still label-trust** (no `EnforceObligations` analogue); narrowed by ReconcileFloor, closed later by or-809.dim-backstop.

## Revisit triggers (why this ADR is provisional)

This is Step 3 of a staged migration and is expected to be tweaked. Revisit when:
- The measured shadow window is analyzed and cutover to `live` is proposed (or-809.cutover) — the cutover criterion may need tuning.
- The align-judge validation corpus is widened enough to consider making `ORION_ALIGN_GATE=block` the default (or-809.aligncorpus).
- External-issue ingestion lands (or-809.external / or-97z) and the proposer becomes the adapter for a real backlog.
- The dimension proof-time backstop (or-809.dim-backstop) changes how coverage is validated.
- Any observed pathological slicing (e.g. cluster collapse) survives the deterministic gates in shadow.

## Amendments

- **2026-07-02 (tracer slice, post-implementation adversarial review).** The shadow coverage/floor/superset metrics are computed over the proposer's **raw** modules, *not* the bookend-inflated plan. An earlier implementation measured the plan after the deterministic acceptance bookend was appended — since the bookend covers every floor dimension + case id, `SupersetOK/FloorOK/CoverageGateOK` were structurally constant-true and could never detect a coverage-dropping proposer, silently gutting the future cutover gate. The runtime bookend remains the safety backstop; it just must not participate in the *measurement* that judges the proposer's own slicing. Minor deferred review findings are tracked in or-7et.1.

## Alternatives considered

- **Design A (proposer + pure coverage oracle):** superset-over-labels only; rejected — no proposer-independent floor, missed the cluster-collapse hazard. Its "keep `Decompose` as the literal default" call-site shape was adopted.
- **Design C (issues-first unifier):** best end-state (issues ARE the spec), but a day-one inversion (proposer primary, template demoted to fallback, new tracker Reader + CLI) that is less reversible than Step 3 warrants; deferred, with its bookend-acceptance and ChainProposer grafts kept.
