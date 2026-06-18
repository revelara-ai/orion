---
title: Orion ↔ Polaris API Contract (audit)
status: draft
authors: Joseph Bironas
created: 2026-06-17
derived_from:
  - polaris/api/openapi.yaml (canonical contract — 2025 lines, ~150 endpoints)
  - rvl-cli/internal/api/client.go (the endpoints rvl-cli actually calls today)
  - docs/PRD/orion-v2.md (Scope: Orion as the Reliability Touchpoint; polaris-connector module)
---

# Orion ↔ Polaris API Contract

> **Source of truth:** `polaris/api/openapi.yaml` (also `openapi.bundled.yaml`; Go server types generated to `polaris/internal/apispec/openapi.gen.go`). This document is an **audit/mapping**, not a re-specification — Orion's `polaris-connector` **generates its client from the OpenAPI spec** (e.g., `oapi-codegen`) so the contract never drifts. Field-level schemas live in the spec; this doc maps *which* endpoints Orion uses, for *which* loop step, read vs. write, and the phase.

## 1. Headline findings (these change the PRD's assumptions for the better)

1. **Polaris exposes STPA *primitives* + reasonable-default loss scenarios — not finished models.** There is a full `control-structure` family plus `ucas`, `loss-scenarios`, `loss-definitions`, and `risk-controls/defense-layers`, and Polaris seeds *reasonable-default* loss scenarios. **But the actual STPA artifacts for a given system — the control-structure model, the UCAs, and refined loss scenarios — must still be developed by Orion + the developer through modeling that requires developer questions and context** (the defaults are a starting point the developer can change). Polaris is the **substrate, starting point, and persistence layer** for STPA; the *modeling work* is Orion's hazard-mode job, done with the developer in the loop (read defaults → model/refine with developer input → write back). (See §4.)
2. **Polaris already issues plugin signing keys** (`/api/v1/plugin`, `/api/v1/plugin/signing-key`). This is the natural backbone for Orion's **signed agent-definition / package supply chain** (Security Requirements + Extension & Package Security) — Orion need not invent its own signing authority.
3. **Evidence is a first-class Polaris resource** (`/api/v1/evidence`, `…/verify`, `…/tests`, and `/api/v1/risks/{id}/evidence`) — consistent with the decision that Orion auto-submits evidence (proof-of-work) on proof-pass.
4. **The "61-control catalog" is `/api/v1/controls`** (with `by-code`, `by-id`, `categories`, `interactions`, `prerequisites`, `stats`). rvl-cli reads `controls/by-code/{code}` today.
5. **Scan is `/api/v1/risks/scan` + `/api/v1/scanner/matchers`** (matchers = the detector definitions; `/api/v1/scans/{id}/summary` for results). Orion's `reliability-scan` writes findings here.
6. **Knowledge is a rich graph API** (`/api/knowledge/{search,facts,patterns,procedures,promotions,foresight,insights,graph-search,communities,contradictions,relationships,feedback,extract}`) — Orion's "search KB / facts / patterns / procedures" (Phase C4) and knowledge-contribute (G3) map directly.

## 2. Auth model

| Concern | Endpoint(s) | Orion use |
|---|---|---|
| Login / token | `POST /api/v1/auth/login` | `orion login` (C1) |
| Org selection / switch | `POST /api/v1/auth/select-organization`, `/switch-organization` | multi-org developers |
| Refresh / identity | `POST /api/v1/auth/refresh`, `GET /api/v1/auth/me` | session refresh; `orion status` |
| Logout | `POST /api/v1/auth/logout` | `orion logout` (erases cached credential) |
| Plugin signing key | `GET /api/v1/plugin/signing-key`, `/api/v1/plugin` | **agent/package signing supply chain** |
| CLI version gate | `GET /api/v1/cli/version` | compatibility check on startup |

Token storage per PRD Security Requirements: OS keychain / encrypted-`0600`, never in the Context Store, never reachable from the sandbox.

## 3. Capability → endpoint map (Orion `polaris-connector`)

R = read/consume, W = write. "Phase" = loop-map step; "rvl" = rvl-cli already calls it (parity baseline).

| Orion capability | Polaris endpoint(s) | R/W | Phase | rvl | Notes |
|---|---|---|---|---|---|
| Controls catalog | `GET /api/v1/controls`, `…/by-code/{code}`, `…/categories`, `…/by-id/{id}/{interactions,prerequisites,risks}`, `…/stats` | R | C2 | ✓ | the reliability-context backbone |
| Knowledge / facts / patterns / procedures | `GET /api/knowledge/{search,graph-search,facts,patterns,procedures,foresight,insights}` | R | C4 | ✓ | feeds `memory`/`context-engine` |
| Risk register (read) | `GET /api/v1/risks`, `…/{id}`, `…/stats`, `…/stale` | R | C2, `:risks` | ✓ | |
| Reliability scan (write findings) | `POST /api/v1/risks/scan`; `GET /api/v1/scanner/matchers`; `GET /api/v1/scans/{id}/summary` | R/W | C3 | ✓ | `reliability-scan` fleet ↔ matchers |
| Risk open | `POST /api/v1/risks` | W | C3/G1 | ✓ | additive, auto |
| Risk resolve / status | `PATCH/POST /api/v1/risks/{id}/status`, `…/dismiss` | W | G2 | ✓ | auto + idempotent + provenance |
| Risk ↔ controls / evidence / knowledge links | `/api/v1/risks/{id}/controls`, `…/evidence`, `…/knowledge` | R/W | E/G | partial | wire proven controls to risks |
| Evidence submit (proof-of-work) | `POST /api/v1/evidence`; `GET/POST /api/v1/evidence/{id}/tests`, `…/verify` | W | F6 | ✓ | **automatic on proof-pass (all tiers)** |
| Knowledge contribute (V2.3) | `POST /api/knowledge/{facts,patterns,procedures,extract,feedback,promotions}` | W | G3 | partial | deterministic-redact + diff confirm; default off |
| STPA control structure | `GET …/control-structure/{latest,model,snapshots,diff}`; `POST …/build`; `…/overrides` | **R/W** | E10 | ✓ (`/model`) | read substrate → **model with developer** → persist back |
| UCAs / loss scenarios / defense layers | `GET/POST /api/v1/ucas`, `/api/v1/loss-scenarios` (+ `…/{id}/controls`), `/api/v1/loss-definitions`, `/api/v1/risk-controls/defense-layers/…` | **R/W** | E10 | ✓ | read defaults → **develop/refine (developer-changeable)** → persist |
| Control checklist (MCP) | `GET /api/v1/mcp/control-checklist` | R | E10/proof | | ready-made checklist |
| Service catalog | `GET /api/v1/catalog/services`, `…/search`, `…/{name}` | R | B5/C | | brownfield architectural context |
| Actions / remediations | `/api/v1/actions`, `…/{id}/{approve,complete,defer,wont-do}`, `…/generate` | R/W | G | | maps to remediation tracking |
| Skills / agents registry | `GET /api/v1/skills`, `/api/v1/agents` | R | composition | ✓ | reconcile with Orion skill registry |

## 4. The STPA reconciliation (important)

The PRD's hazard mode (Phase E10) says Orion "models the STPA control structure (controllers, control actions, feedback loops), derives UCAs, checks controls." Polaris provides the **primitives and persistence** for this — but not finished models. It exposes:

- `control-structure/{model,latest,build,snapshots,diff,overrides}` — the control-structure data model + history + override mechanism.
- `ucas` — the unsafe-control-action store.
- `loss-scenarios`, `loss-definitions`, `loss-scenarios/{id}/controls` — the STPA loss model (Polaris seeds **reasonable defaults**) and the controls that mitigate each.
- `risk-controls/defense-layers` — the defense-in-depth layering per risk/control.

**Decision (corrected):** Polaris supplies the **schema, defaults, and persistence** for STPA; **developing the artifacts is Orion's hazard-mode work, done with the developer in the loop** — it is *not* a pure read. Concretely, hazard mode:

1. **Reads** the existing control structure, UCAs, and default loss scenarios from Polaris as a *starting point*.
2. **Models/refines** them for the system under test — building or extending the control-structure model (`POST /control-structure/build`, `…/overrides`), deriving UCAs, and **adjusting the default loss scenarios** (the developer can change them). This step is **HYB and developer-involved**: it surfaces questions and requires context the developer provides (tie this to the Phase A spec dimensions — security/data/deps/scale — and to spec-elicitation; loss scenarios and the control structure are *ratified by the developer* much like the executable spec).
3. **Persists** the developed artifacts back to Polaris (so they are shared, versioned, and reusable) and uses them as the source for proof.
4. **Proves** that the change under test maps onto the (developed) control structure and that the relevant control actions and feedback loops are present and observable.

So Polaris removes the "invent the STPA *schema/store* and bootstrap defaults" burden, but the modeling, the questions to the developer, and the artifact refinement remain Orion's job. "UCA catalog provenance: derived from the ProofObligation and/or the Polaris risk register, not the generator" still holds — the developed UCAs/control structure (developer-ratified, Polaris-persisted) are the trusted source, never a generation-agent's self-assertion.

**The modeling mechanism already exists: the `stpa-design-review` skill.** It is a four-phase, gated, directed questionnaire toward the developer (it refuses to proceed without per-gate confirmation, and confirms each control-structure edge individually to prevent rubber-stamping). Orion's STPA-development step (loop step C7) adapts it and maps its phase outputs to Polaris:

| `stpa-design-review` phase | Output | Polaris endpoint |
|---|---|---|
| 1. Define losses | numbered loss list | `loss-definitions` (seeded by Polaris defaults; developer-changeable) |
| 2. Model control structure | controllers, control actions, feedback paths (every action must have a feedback path) | `control-structure/build`, `…/overrides`, `…/model` |
| 3. Identify UCAs (4 STPA questions: not-provided / incorrect / wrong-timing / wrong-duration) | UCA list per control action | `ucas` |
| 4. Trace loss scenarios | trigger → sustaining condition → loss | `loss-scenarios`, `…/{id}/controls` |

The Phase-2 completeness rule ("flag every control action with no feedback path") is precisely what makes the control actions and feedback loops **testable** in hazard/empirical proof (E10/E9). `STPA_REFERENCE.md` (bundled with the skill) is the format/example prior art.

## 5. Phasing (aligned to PRD)

| Capability | Phase |
|---|---|
| Auth, controls read, knowledge read, risk read, scan, control-structure/UCA read | **V2.0** |
| Risk write (open/resolve), evidence submit, risk↔control/evidence links | **V2.1** |
| Knowledge contribute, failure-mode write-back, control-structure contribute | **V2.3** |

## 6. Open items for the connector

1. **Client generation:** generate the Go client from `openapi.yaml` (oapi-codegen) and pin to a spec version; CI fails if Orion's pinned spec drifts from Polaris's published one.
2. **rvl-cli parity gaps:** rvl currently uses `compound-risks`, `loss-definitions`, `review`, `onboarding/milestone`, `agents`, `skills`, `ucas`. Confirm which are part of Orion's required parity surface vs. rvl-only.
3. **Idempotency:** the spec does not obviously expose an idempotency-key header — confirm whether Polaris dedupes writes; if not, Orion tracks `polaris_write_attempts` locally (per PRD) and suppresses retries of committed ops.
4. **Evidence schema:** confirm `POST /api/v1/evidence` accepts the proof-provenance fields Orion wants to attach (`orion_run_id`, `proof_metrics`, active skill/package versions); if not, attach as metadata/annotations.
5. **Org scoping / RLS:** all writes are org-scoped via the auth token; confirm Orion's write-credential scope can be limited by tier (a `throwaway` project should not hold evidence-write scope).
