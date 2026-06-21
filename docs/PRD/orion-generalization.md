# Orion Generalization: De-Time-Service the Harness

## Why this exists

Orion is meant to be a **general** proof-gated harness that builds *any* trustworthy
software from a ratified spec. An alignment audit (2026-06-21, 4 read-only agents, 39
couplings catalogued) found that it is **not yet general** — it is a time-service
harness with three real generalization *seams* bolted on that no upstream code
populates for a non-time spec.

## Verdict (the honest version)

Two seams are genuinely general and load-bearing:
- **Cases-driven proof** — when a spec carries behavioral `Cases`, testsynth/empirical
  execute per-case obligations over a pluggable `AssertionKind` set.
- **Native generation** — `nativegen.go`'s prompt is domain-agnostic; it builds
  whatever logic satisfies the cases behind a stable `handleTime` symbol.

But **everything upstream of those seams is hardwired to HTTP-time, and nothing forces
a non-time spec to populate the general path**, so the time defaults silently fill the
gap. The residue is **not cosmetic** — it sits directly on the two places a wrong
default silently passes or blocks a build: the **proof verdict** and the **elicitation
gate**.

## Strategy

Not a rewrite. The seams exist; the fix is: **stop defaulting, force the spec to supply
the value, and let the dead seams carry the load.** Every "default to time" becomes
"require the spec to declare it; fail loudly if absent."

## Status (2026-06-21)

The misaligned DEFAULTS that silently shaped every build toward the time domain are
removed. The harness no longer (a) rejects a correct non-time program (hazard), (b)
imposes a "time" body contract on every JSON spec, (c) defaults the proof probe to
/time or a "time" key, or (d) grills a non-HTTP project on route/port/timezone.

| # | Item | State |
|---|------|-------|
| 1 | STPA time-service fallback → SkeletonModel | ✅ 928e4b6 |
| 2 | controlPresent → model-driven (UCA.Verify) | ✅ 928e4b6 |
| 4 | defaultCase: no "time" body assertion | ✅ 7a10475 |
| 5 | buildResponseContract: generic JSON schema | ✅ 7a10475 |
| 8 | JSONKey/legacyCorpus: no "time" default | ✅ 0d6f440 |
| 10 | empirical: no /time route default; de-timed legacy probe | ✅ 0d6f440 |
| 3 | projectType registry (structural) | ✅ 1d58c19 |

**Remaining (follow-ups, not silent-default bugs):**
- **#3 threading** — wire the conductor to CHOOSE projectType from intent (inference),
  instead of the hardcoded "http-service". The registry is ready; this is a feature.
- **#6 decomposer per-type task templates** — the task tree bakes HTTP into obligations,
  but the decomposer only builds `Tasks[0]`; real impact awaits multi-task execution, so
  this folds into the workflow/brownfield build-out.
- **#7 fixture rename / no silent gap-fill** — audit-rated a legitimate labeled fallback;
  cosmetic.

## Ranked cleanup (from the audit)

**Verdict-bearing (worst — a correct non-time program is silently rejected):**
1. ✅ **STPA fallback** (`conductor/build.go`, `proof/hazard/stpa/defaults.go`) — DONE
   (commit 928e4b6). The store-miss fallback is now the domain-neutral
   `stpa.SkeletonModel()`, never the time-service model. `DefaultModel()` is documented
   as the time-service example only.
2. ✅ **`controlPresent`** (`proof/hazard/hazard.go`) — DONE (928e4b6). Now a generic
   evaluator over tokens each UCA DECLARES (`UCA.Verify`); the time-service tokens moved
   into the example model. Proven: the skeleton passes an arbitrary non-time program;
   the time-service model fails that same code (declared tokens absent).

**Elicitation gate (blocks non-HTTP work at the front door):**
3. **`checklistFor` / `projectType`** (`completeness/completeness.go`, `conductor.go:63,69`):
   `projectType` is a dead parameter (switch with only a `default`), always constructed
   with the literal `"http-service"`; every project is grilled on route/port/timezone.
   → Make `projectType` a real registry; forbid unknown types; thread it from intent.

**Spec/contract (injects the time domain into the contract itself):**
4. **`defaultCase`** (`spec/spec.go:110-122`): every compiled spec gets a default case
   hardcoding `AssertJSONKeyRFC3339 key="time"`. → Drop the time-specific body assertion
   (assert only status + content-type, which the scalar contract actually declares); the
   time-service example must DECLARE its `time` requirement. **Entanglement:** this
   weakens scalar-only proofs (correct — undeclared body shape isn't proven), so the
   canonical `ratifiedTimeService` test helper must add an explicit `time` behavioral
   requirement to stay rigorous; spec/testsynth tests asserting the `time` default need
   updating.
5. **`buildResponseContract` schema + `ResponseContract` scalars** (`spec/spec.go:29-36,148-181`):
   the JSON branch hardcodes a `time`-string schema; the contract has no notion of a
   non-HTTP transport. → `response_schema` becomes an explicit decision; move toward a
   transport-tagged contract. **Entanglement:** `rc.Schema` is part of `ComputeHash`, so
   changing it re-anchors specs — fine in tests (fresh stores) but a persisted-spec
   migration note for real projects.

**Decomposition + proof surface:**
6. **`Decompose`** (`decomposer/decomposer.go:39-97`): the task tree bakes HTTP into
   obligations ("GET <route> on port <port> returns <format>"). → Per-`projectType` task
   templates sharing the checklist registry.
9. **`handleTime` binding** (`testsynth`, `behavioral/mutation.go`): deliberate HTTP-family
   coupling. → Keep as the HTTP contract symbol; make the entry point a declared field on
   the contract for other transports.
10. **empirical `/time` default + HTTP probe** (`empirical/empirical.go`): remove the
   silent `c.Route="/time"` default; split probing by declared transport.

**Dead generalization seams (look general, aren't):**
8. **`RequiredJSONKey`** (`testsynth/testsynth.go:32-48`): declared but never set, so
   `JSONKey()` always returns `("time", true)`. → Wire it from the contract's schema key,
   or delete it and route everything through the Cases-driven corpus.

**Legitimate (keep, but stop silent gap-filling + rename):**
7. **`GenerateFixtureService`** (`sandbox/codegen.go`): a labeled no-LLM fallback. → Keep
   as the canonical time-service example; rename `GenerateTimeServiceFixture`; require a
   complete `GenSpec` (error on empty), don't silently default Module/Route/Port.

## Sequencing

This realignment is **prerequisite to brownfield**: generalizing the proof to existing
repos on top of a secretly-time-service harness would only spread the residue. The
verdict-bearing items (#1, #2) are the highest priority — a correct non-time program is
currently *rejected*, not just mis-elicited.

This will churn the test suite (all tests are time-service-based and rely on the
defaults). Each item lands behind its own tests; the time-service example becomes an
explicit fixture/registered type, not the silent default.
