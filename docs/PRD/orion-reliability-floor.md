---
title: Orion — The Corpus-Sourced Reliability Floor
status: draft
authors: Joseph Bironas
created: 2026-07-03
last_updated: 2026-07-03
derived_from:
  - docs/PRD/orion-v3.md            # this is V3's "reliability floor", made concrete
  - docs/MANIFESTO.md
references:
  - docs/PRD/orion-formal-verification.md   # the design-proof sibling gate
---

# Orion — The Corpus-Sourced Reliability Floor

> **One retrieval, two consumers, one house rule.** Reliability knowledge from the
> Revelara corpus is retrieved **once** in the trusted control plane, then used
> **twice** — as advisory *context* the coding agent sees before it writes, and as
> deterministic *obligations* the proof harness enforces after. It obeys the same
> house rule as V3's other new gates: **it can only ever remove a green light, never
> add one.** `proof.Accept` remains the sole right-to-ship.

## 0. What this is, in one line

V3 plans to demote the hardcoded `completeness` checklist into "a project-type
**reliability floor**" (orion-v3.md §5, Step 5). This document specifies that floor and
makes it **corpus-sourced**: instead of a hand-coded list of implementation primitives
(`response_format`, `port`, `route`), the floor is populated from the Revelara
reliability corpus — org-grounded, incident-derived controls, risks, and knowledge —
and split into an advisory half and a deterministic half.

## 1. The problem

- The generator writes code with **no knowledge of the org's reliability posture**.
  The controls catalog and incident corpus that would tell it "outbound HTTP needs a
  timeout, and here's the incident where it didn't" never reach the point of
  generation. `internal/conductor/reliabilitycontext.go:loadReliabilityContext()`
  already authenticates and fetches this material — and then discards it to a `Reduced`
  bool. The knowledge is fetched and thrown away.
- The brownfield change generator's system prompt
  (`internal/conductor/diffgen.go:diffGenRole()`) has a `repoContext` parameter that is
  **currently unused** — a ready-made seam with nothing flowing through it.
- There is **no reliability gate**. A change can pass the regression gate and the
  new-behavior proof while introducing a classic reliability defect (missing timeout,
  unclosed body, swallowed error) that the org has already been burned by.

## 2. Positioning within V3 (and the MCP tension, addressed)

This is a **third sibling** in V3's advisory→blocking gate family, alongside the
AlignmentGate (post-proof semantic drift) and the design-proof gate (pre-generation
formal check). All three share three house rules, which this floor adopts verbatim:

1. **Tier-gated**, and rolls out **log-only → advisory → blocking** — never big-bang.
2. **Can only ever remove a green light.** `proof.Accept` is the sole right-to-ship.
3. **No skill computes a verdict in prose.** The deterministic half is owned by the
   orchestrator; the advisory half can escalate/annotate but never asserts `Accept`.

**The MCP tension (orion-v3.md §1: "V3 does NOT depend on MCP").** V3's rule is that the
*spine* must not depend on MCP, because a tool called at the agent's discretion enforces
nothing. This design honors that:

- MCP is used here as a **data source for advisory knowledge**, not an enforcement
  transport. All enforcement stays with the deterministic obligations + the DB
  done-gate, exactly as V3 requires.
- Retrieval sits behind a **`SignalSource` interface**. The Revelara MCP is
  implementation #1; the spine depends only on the interface. This is also the seam for
  the eventual **rvl-cli backport** and a future **offline local pack** — same
  interface, different source, zero spine change.

## 3. Architecture — one shared type, five bounded units

The currency is a single value type produced once and consumed by both halves:

```
Signal {
  ID        // RC-XXX | R-XXX | incident short_name (stable, citable)
  Title     // "Outbound HTTP without timeout"
  Why       // grounded rationale; cites the control/incident it came from
  Severity  // CRITICAL..LOW (maps to Orion's existing severity tiers)
  Source    // control | risk | incident | knowledge
  Check     // mechanization: { Kind: golangci-lint | grep | file | none, Linters/Pattern }
}
```

`Check.Kind == none` ⇒ advisory-only. `Check.Kind != none` ⇒ eligible to become a
deterministic obligation. **That one field is the hinge of the hybrid.**

| Unit | Responsibility | Trust domain | Depends on |
|---|---|---|---|
| `Signal` | data type + severity mapping | — | nothing |
| `SignalSource` | interface: `Fetch(query) → []raw` | **trusted** | — |
| `Retrieve()` | change intent + changed-file feature scan → source queries → ranked/deduped `[]Signal`, with `Check` attached via a concern→mechanization map | **trusted** | `SignalSource`, extends `loadReliabilityContext` |
| `RenderContext()` | pure `[]Signal → string` (prompt-optimized block) | — | `Signal` |
| `Obligations()` | `[]Signal → ([]verify_command Case, []Advisory)` — splits mechanizable vs not | **trusted** | `Signal`, `newbehavior.Case` |
| `AdvisoryVerify()` | nested harness.Loop reviews diff vs advisory signals → non-blocking findings; **reuses AlignmentGate advisory plumbing** | **trusted** | AlignmentGate infra *(deferred past slice 1)* |

**Trust boundary.** Retrieval, obligation-emission, and advisory verification all run in
the trusted control plane. The **untrusted generator never calls the source** — signals
reach it only as rendered text (`RenderContext`). This preserves the trust wall
(`internal/agentruntime` records claims, never verdicts).

## 4. Data flow — brownfield `orion change` (first consumer)

```
ChangeAndProve  (internal/conductor/changeproof.go:52)
  ├─ worktree create                                   (unchanged)
  ├─ Retrieve(intent, changedFileFeatures) ──► []Signal          ← NEW (trusted)
  ├─ DiffGenerator(..., RenderContext(signals))                  ← fills the UNUSED
  │       into diffGenRole()'s repoContext seam                     repoContext arg
  ├─ regression gate (green→green)                     (unchanged; safeenv lane)
  ├─ Obligations(signals):
  │     mechanizable → golangci-lint verify_command obligations  ← deterministic
  │       run in the SAFEENV lane → truthalign verdict              teeth
  │     Check=none   → listed for the fix loop (LLM AdvisoryVerify ← non-blocking
  │       deferred to slice 2, §6)
  ├─ [fix closure, deferred: re-run diffgen with findings, bounded]
  └─ commit + deliver; signals + verdicts recorded in the envelope
```

Greenfield (`build.go` / GenSpec.Context) and the V3 per-module PROVE step are **later
consumers of the same units** — no rework, since the units are loop-agnostic.

## 5. The verify integration and its hard constraint

The mechanizable half maps a concern to an existing linter and runs it as a
`verify_command` obligation (`internal/proof/newbehavior/newbehavior.go`,
`Tool=golangci-lint`). The Go reliability linters that already exist and map cleanly:

| Concern (from corpus) | Linter |
|---|---|
| outbound call without timeout/context | `noctx`, `contextcheck` |
| leaked HTTP body / sql rows / stmt | `bodyclose`, `rowserrcheck`, `sqlclosecheck` |
| swallowed error | `errcheck` |
| common security sinks | `gosec` (curated subset) |

**Hard constraint (the reason the placement matters).** Running linters needs the
**host module cache**. Orion's hermetic `proofexec` sandbox (GOPROXY=off, empty
GOCACHE) **cannot** run them — deps won't resolve. Therefore the reliability
obligations execute in the **`safeenv` / regression-gate lane** (host module cache
available), and via `safeenv.Build()`, never `os.Environ()` (the API-key exfil guard).
`golangci-lint` is **v2**, so emitted configs use `version: "2"` and
`linters.exclusions.paths`. This mirrors the constraints already learned wiring
`verify_command`; it is a proven lane, not new sandboxing work.

The advisory half is modeled on the **AlignAgent** (orion-v3.md §4): adversarially
framed, non-deterministic, advisory-only, can escalate/annotate but never `Accept`. It
**reuses the AlignmentGate's advisory-verdict plumbing** rather than introducing a
parallel path.

## 6. Migration discipline (mirrors V3 Step 1)

- **Slice 1 — log-only.** Retrieve (intent + import scan) → render into `diffGenRole` →
  emit one golangci-lint obligation set in the safeenv lane, **verdict logged, blocks
  nothing**; advisories printed. Dogfood on a real `orion change` against a Go repo with
  a planted missing-timeout. Acceptance: the signal is retrieved, injected into the
  generator's context, the linter obligation fires, and it is logged — with zero gating
  and zero upstream change. (The exact shape of V3's Step 1 for the AlignmentGate.)
- **Slice 2 — advisory.** Add `AdvisoryVerify` (LLM half) for non-mechanizable signals;
  surface findings into a bounded fix-retry of the diff generator.
- **Slice 3 — blocking, tier-gated & severity-tiered.** High-severity mechanizable
  obligations gate; medium surface for a human; low stay advisory. Wire greenfield /
  the V3 reliability floor as a second consumer.

**Reversibility:** slice 1 touches only the (currently dead) `repoContext` seam and adds
a logged obligation; removing the `Retrieve` call fully reverts it.

## 7. Testing

- **Pure unit:** `RenderContext` (golden output for a fixed `[]Signal`); `Obligations`
  split logic (mechanizable vs advisory partition; correct linter selection); severity
  mapping.
- **Fake `SignalSource`:** deterministic canned signals — no network — for all
  orchestration tests.
- **Integration:** the log-only path end-to-end with the fake source, asserting (a) the
  rendered block reaches `diffGenRole` output, (b) the golangci-lint obligation is
  emitted into the safeenv lane, (c) nothing blocks.
- **Acceptance / dogfood:** the planted-missing-timeout `orion change` demonstration is
  the bar for slice 1.

## 8. Risks (honest)

1. **Advisory-vs-structural** (the recurring lesson): only the deterministic obligation
   half is real enforcement; the advisory half is gameable prose. Mitigation: the
   advisory half can *only ever remove* a green light, and blocking authority is limited
   to the deterministic linter obligations.
2. **Relevance / noise.** A weak feature scan retrieves generic signals and buries the
   relevant one. Mitigation: rank by changed-file feature match; cap at top-N; log-only
   first so noise is observed before it can gate.
3. **False positives gating a build** (slice 3). Mitigation: gate only on low-FP,
   battle-tested linters; severity-tier the rest; `gosec` enters as a curated subset.
4. **Sandbox lane drift.** If a future refactor moves obligation execution into the
   hermetic `proofexec`, the linters silently fail (no module cache). Mitigation: an
   explicit test asserting the reliability obligation runs under safeenv.
5. **MCP dependence creep.** If the spine starts assuming MCP shape, V3's independence
   erodes. Mitigation: everything behind `SignalSource`; a fake source is the default in
   tests; the MCP impl is one file.
6. **Corpus auth in headless runs.** The Revelara MCP needs credentials that may be
   absent in cron/CI. Mitigation: `SignalSource` returns empty on auth failure and the
   floor no-ops (fails open to "no extra context, no extra obligations") — it must never
   block a change because the corpus was unreachable.

## 9. Open questions

- Does the concern→mechanization map live in Orion (curated Go table) or ride on the
  corpus record itself (a control that declares its own linter)? Slice 1 uses a small
  in-Orion table; the corpus-declared form is the cleaner long-term answer and the
  rvl-cli backport may want it.
- Where exactly does the fix-closure retry sit — inside the existing 40-iteration
  `DiffGenerator` harness loop as tool-observable feedback, or as an outer bounded
  re-invocation? (Slice 2 decision.)
