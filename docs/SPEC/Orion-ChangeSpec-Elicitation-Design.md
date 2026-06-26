---
title: Orion Change-Spec Elicitation & Ratification — Design (brownfield change as a TUI-conductor flow)
status: approved
created: 2026-06-26
authors: Joseph Bironas
epic: or-3p5
issue: or-3p5.9
related:
  - docs/SPEC/Orion-NewBehavior-Proof-Design.md   # the proof mechanism this feeds ([]newbehavior.Case)
  - docs/PRD/orion-v2.md                           # TUI-first; Conductor; gates-as-tools; or-26w
---

# Orion Change-Spec Elicitation & Ratification — Design

> Sub-project 2 of the new-behavior proof (or-3p5.9). Brainstormed + approved 2026-06-26.
> Sub-project 1 (the proof MECHANISM — synth_test + command modalities) is merged; this
> elicits + ratifies the behavioral cases that feed it.

## 1. Context

The new-behavior proof (slices 1a/1b) proves a brownfield change does what was asked — but only
against ratified `[]newbehavior.Case`, supplied today as a hand-written `--cases` file. That is
the gap: there is no elicitation/ratification of those cases, and a hand-written file is not the
experience we want.

**Direction (the steering decision):** *everything goes through the TUI — one seamless,
intuitive experience.* The developer never memorizes esoteric command lines; the CLI
(`orion change`) becomes a headless mechanism the harness calls itself. So sub-project 2 is not
"a `--cases` elicitation" — it is **brownfield change as a first-class conductor/TUI flow.**

**The substrate already exists.** The TUI drives the native conductor (`internal/tui/conversation.go`
→ `conductor.NewOrionAgent`), and greenfield's grill→ratify→build already runs through it as
**gates-as-tools** (`internal/conductor/oriontools.go`: `submit_intent` → `record_answer` /
`add_requirement` → `preview_spec` → `ratify_spec` → `build_service`). Sub-project 2 **adds the
brownfield change flow to that same surface** — the change tools mirror the build tools.

## 2. Shape

The developer tells the TUI "change X in this repo." The conductor proposes behavioral cases in
the conversation, the developer ratifies, and the conductor drives generate → prove → deliver,
streaming results back. It is the greenfield experience, for changes — one seamless conversation.
`orion change` remains the headless entrypoint the conductor invokes; the developer touches only
the TUI.

## 3. Conductor change-tools (gates-as-tools)

Added to the conductor's tool surface, mirroring the build tools:

| Tool | Purpose |
|---|---|
| `submit_change_intent` | open a change against the existing repo; returns the repo map / affected surface |
| `propose_cases` | coordinator proposes `[]newbehavior.Case` from the intent + repo map (see §4) |
| `add_case` / `edit_case` | the developer (via the conductor) adds or refines a case |
| `ratify_cases` | **the oracle gate** — lock the cases before any code is generated (see §5) |
| `build_change` | drive `ChangeAndProve` with the ratified cases (see §6) |

The RoleTemplate teaches the conductor to use the change tools when the developer wants to change
an existing repo.

## 4. `propose_cases` (coordinator, grounded)

A coordinator-LLM call proposes `[]newbehavior.Case` (synth_test and/or command) from the change
intent + `brownfield.RepoMap`, then **deterministically grounds** each: the named package/symbol
exists, the modality payload is well-formed (synth_test needs Pkg+Call+Want; command needs a
non-empty Assert). Ungrounded proposals are surfaced, not silently kept — the same
propose-then-validate pattern as `intentmap.MapIntent`. The grounded cases are shown in the
conversation for the developer to accept, edit, or extend.

## 5. `ratify_cases` (the oracle gate, before generation)

The developer ratifies in the conversation; the conductor calls `ratify_cases`, which **locks the
cases as the proof oracle before `DiffGenerator` runs** (mirrors `ratify_spec`). This is the trust
hinge: the case **proposer is a coordinator step, distinct from the generator** (`DiffGenerator`),
and the oracle predates the diff — so the proof is independent of the generated code by
construction. (Ratification is a conversational confirm the conductor acts on, mirroring greenfield
`ratify_spec`; an explicit ACP `session/request_permission` approval is an optional hardening.)

## 6. `build_change` → the proven change

Drives `ChangeAndProve(ctx, repo, store, provider, intent, ratifiedCases)` — the regression gate
(do-no-harm) + the new-behavior gate (slices 1a/1b) → commit on green — streaming phase progress to
the TUI; the result (proven + committed branch, or not-committed + reason) is shown there.

## 7. Persistence — in-session first

The conductor holds the ratified cases between `ratify_cases` and `build_change` within one
session. A durable `change_specs` store (intent + ratified `[]Case` + anchor hash + status, a
repo mirroring `Specs()`) for resume/audit is a later enhancement — not needed for the first slice
or the dogfood.

## 8. Routing & scope boundaries

- **First slice:** the RoleTemplate routes to the change tools when the developer wants to change
  an existing repo. **Auto-detect routing** (the conductor runs `brownfield.Classify` on the
  resolved repo and chooses build-vs-change so it "just knows") is a clean follow-on — the TUI
  experience is identical either way.
- **Deferred:** **intended-behavior-change vs. regression** reconciliation — a change that
  *intentionally* alters existing behavior makes an existing test legitimately "regress." The
  first dogfood is additive, so it doesn't trigger this; it is a later slice.

## 9. Trust domains

The proposer (coordinator) is distinct from the generator (`DiffGenerator`); cases are ratified
**before** generation, so the oracle is independent of the generated code. The harness authors and
runs every proof (slices 1a/1b); coordinator output is control-domain context, never a proof
input or a shippable artifact.

## 10. Acceptance — the next dogfood (#2)

In the TUI, in the Orion repo: *"add a `Severity()` method to `Verdict` returning critical|warn|ok
from its fields."* The conductor proposes ~3 cases (one per branch), the developer ratifies, the
conductor proves + commits — entirely in the TUI, no CLI typed. More complex than the `String()`
dogfood: multi-branch, multi-case, model-elicited + developer-ratified.

## 11. Decomposition

| Slice | Scope |
|---|---|
| **A** | Change-flow tools (`submit_change_intent`, `propose_cases`, `add_case`/`edit_case`, `ratify_cases`, `build_change`) + RoleTemplate routing + in-session ratified cases + wiring to `ChangeAndProve`. *Enables the §10 dogfood.* |
| **B** | Auto-detect routing (`brownfield.Classify` → build-vs-change) so the conductor chooses without a role hint. |
| **C** | Intended-behavior-change vs. regression reconciliation (enables dogfood #3, a behavior-modifying change). |

## 12. Out of scope (deferred)

- Durable `change_specs` persistence (resume/audit) — in-session first (§7).
- The MCP bridge so an *external* agent (real Claude Code) drives the change tools — that's the
  or-26w MCP slice; here the native conductor drives.
- Intended-change reconciliation (→ slice C); the deeper "default to http-service" generalization
  (→ or-3ba).
