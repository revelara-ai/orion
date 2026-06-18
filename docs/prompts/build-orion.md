# Build Orion — Provable Implementation Loop

> Implement Orion V2 by running a miniature Orion loop on itself: walk the epics in dependency order, **build each task, then independently prove it**, and only advance when proof converges. Orion's whole thesis is that completion means *proven*, not *asserted* — so we hold our own build to that bar. **Proof is the right to call a task done; an unproven task is not done, no matter how green its tests are.**
>
> Source of truth: `docs/MANIFESTO.md` + `docs/PRD/orion-v2.md` (+ `docs/SPEC/Orion-Triad-Reconciliation.md`, `docs/SPEC/Orion-Polaris-API-Contract.md`). Tracker: beads, master epic **`or-gb1` (Orion V2)`**. The codebase is **greenfield** — modules are created from scratch per the PRD module list + the Triad Reconciliation (which says, per module, refresh-from-ancestor vs. build-fresh).

## The loop (one task at a time, in dependency order)

```
bd ready  ─▶  PLAN  ─▶  BUILD (generation)  ─▶  PROVE (independent)  ─▶  GATE
   ▲                          │                        │                 │
   └───────── advance ◀── PROVEN ◀────────────────── converge ──────────┘
                              ▲                                          │
                              └──── remediate (bounded) ◀── not PROVEN ──┘
                                                                         │
                                                       budget exhausted ─▶ escalate to human (stop)
```

Repeat until every task under the target epic is `PROVEN`. Default target: the whole `or-gb1` tree; you may be pointed at a sub-epic.

## The non-negotiable rule: the verdict is computed by something the builder can't influence

This is the manifesto's credibility hinge, applied to ourselves. The generating LLM does not get to declare its own work done. But "independent proof" does **not** mean "a second LLM's opinion" — the *strongest* prover is a **deterministic mechanism with no judgment to game.**

**Proof-mechanism hierarchy — prefer the highest available for each ProofObligation:**

1. **Deterministic tool / command (best, default).** A shell predicate, `curl … | jq -e`, a `go test` / `-race` exit code, a port/file/hash probe, a mutation runner, `/wireup-check`. The verdict *is* the exit code — there is no opinion to game. This is exactly what the PRD's shell-verifiable acceptance criteria are for, what the empirical (Lookout) mode is, and why the SkillEval contract bans LLM-as-judge. **Most ProofObligations can and should be proven this way.**
2. **Independent (fresh) LLM — only for what tooling genuinely can't decide.** E.g., "does the runbook actually cover the failure modes," "is this PRD requirement semantically met." Use a separate prover context that never saw the build reasoning and reads the spec straight from the PRD; still ground every claim in artifacts, never the builder's narrative.
3. **Same LLM as judge — last resort.** Only when neither of the above fits. Flag it as the weakest evidence, switch hats hard (discard the build rationale, re-read the PRD fresh, reason from artifacts), and never let it overturn a deterministic FAIL.

So in practice: **the builder is the LLM; the prover is, wherever possible, a tool.** BUILD produces artifacts + an untrusted **EvidenceClaim**. PROVE *runs the deterministic checks* (escalating to tier 2/3 only for the residue tooling can't decide) and recomputes the verdict from their observed output. The orchestrator gates on that verdict — never on the builder's claim or a `bd close`. **A deterministic FAIL is final, regardless of any LLM opinion; an LLM judge may never upgrade a tool's FAIL to PASS.**

A corollary that shapes the BUILD phase: every ProofObligation should be expressed as a runnable check *before* implementation, so proof is mechanical. If a task's "done" can't be reduced to a command or named test, that's a design gap — surface it, don't paper over it with a judgment call.

## Per-task procedure

### 0 — Pick & Plan
- `bd ready --exclude-type=epic` → take the next ready task (the first ready task is `or-9xl`, the acceptance test that *defines* done). Claim it: `bd update <id> --claim`, set `in_progress`.
- Read `bd show <id>` **and the full PRD section it cites** (the epic links the PRD via `--design`; work from the PRD, not just the issue text). Read the task's **Manifesto principle**, **Wire-up (planned)** modules, and **Done-when** criteria. For build-fresh-vs-refresh, check the Triad Reconciliation entry for the module.
- Restate the task's **ProofObligation** in one line: what must be empirically true for this to be done.
- Confirm blockers are `PROVEN`. Never build on an unproven dependency.

### 1 — Build (generation domain)
Implement the task to the **full** PRD spec, with the manifesto's disciplines baked in:
- **Test-first / proof-oriented:** write the behavioral tests from the *spec/PRD* before the implementation; assert intended behavior, not "what the code does." (These belong to the proof domain — author them from the spec, and never let implementation shape them.)
- **Wire it up:** no orphans — exports called from production code, interfaces instantiated at startup, flags checked where they gate, columns read+written, TUI panes reachable. Keep the Wire-up manifest accurate.
- **Operability (3 a.m. test):** where the PRD area implies it, emit structured logs/traces/metrics, a runbook, escalation/reversibility — as deliverables, not afterthoughts.
- **Reliability primitives:** timeouts/retries/circuit-breakers on external calls, bounded concurrency, crash-safe transactional writes, context cancellation — Orion eats its own dog food.
- **Honor the trust-domain invariants** the task touches (proof reads spec directly; generation can't read the held-out corpus; verdicts from harness-collected evidence; safety signals harness-derived; secrets redacted on capture).
- Run the local gates (build, `go test`, `go vet`/`-race`, lint, `/wireup-check`). Produce an EvidenceClaim of what was done. **This is a claim, not a verdict.**

### 2 — Prove (proof domain — tools first, independent)
Proof means **running the deterministic checks and recomputing the verdict from their observed output** — not asking an LLM whether it looks done. Default to tier-1 tooling (shell/`curl`/`go test`/probe/`/wireup-check`); reach for a fresh independent LLM (tier 2) only for the residue tooling can't decide, and never let any LLM opinion overturn a tool FAIL. When an LLM does run a check, give it the task id + the PRD section ref **only** — never the build narrative. The four modes, in mechanism terms:

- **Behavioral:** run every Done-when test/predicate itself; record command + exit + output. Then *attack* the suite — tautologies, hardcoded expecteds, fixtures the code reads to pass, skipped/commented assertions; where a mutation/quality gate is claimed, confirm a deliberately-broken mutant is actually caught.
- **Empirical:** exercise the real artifact — run the binary/command, hit the port, inspect the DB row, check restart-survival, confirm the sandbox denies egress. Reality beats the report; a passing test with a non-working artifact is `GAMED`.
- **Wire-up:** every manifest file exists and plays its role; integration verified at call sites (not just definitions); flags checked; columns read+written; panes reachable. Note any file created-but-unlisted or listed-but-missing.
- **PRD-completeness & manifesto-conformance:** compare against the **full** PRD section — anything missing, stubbed, `TODO`, `panic("not implemented")`, or happy-path-only is incompleteness. Verify the stated manifesto principle and the relevant trust-domain invariants hold; a violation is a `FAIL` even with green tests.
- **Verdict (converge):** `PROVEN` only when behavioral ∧ empirical ∧ wire-up ∧ PRD-completeness all pass with cited evidence and no trust-domain violation. Otherwise `INCOMPLETE` / `GAMED` / `FAILED` / `BLOCKED-BY-UNPROVEN`, each with concrete, shell-verifiable remediation.

Emit the per-task proof report:
```
### <id> <title> — VERDICT: <...>
PRD: <section> · Manifesto: <principle>
Behavioral:  <PASS/FAIL> — <cmd + observed>
Empirical:   <PASS/FAIL> — <ran against real artifact + observed>
Wire-up:     <PASS/FAIL> — <call-site/instantiation/flag evidence or orphan>
PRD-complete:<PASS/FAIL> — <PRD requires vs exists; stubs/TODOs>
Trust-domain:<OK/VIOLATION> — <invariant + evidence>
Remediation (if not PROVEN): - <concrete shell-verifiable item> ...
```

### 3 — Gate, remediate, advance
- **`PROVEN`** → record the proof evidence on the task (`bd update <id> --notes=...`), `bd close <id>`, run quality gates, and advance to the next ready task. (Commit per your repo policy — branch off `main` first; don't push without authorization.)
- **Not `PROVEN`** → feed the prover's remediation list back to a builder pass. This is a **bounded iteration loop** (manifesto: iteration budget + degradation guard): cap retries (e.g., 3) and, each pass, compare against the previous — if a pass makes things *worse* (fewer modes passing, new failures), **terminate rather than loop**. On budget exhaustion or degradation, **escalate to the human** with the exact remaining gap, and stop the loop.
- **HITL tasks** (the acceptance test `or-9xl`, the completeness gate, the STPA questionnaire, the smoke test) require human interaction — pause and bring the human in; do not fake their input. The acceptance test is built first and defines the target the rest is proven against; the smoke test is proven last, end-to-end.

## Sequencing notes (from the PRD)
- Start with **`or-9xl` (Integration Acceptance Test)** — it encodes the V2.0 shell predicates and is the target everything else is proven against. It will be RED until the loop fills it in; that's the point.
- Front-loaded infra (harness reliability, budget/governance, recall) is gated *before* the proof chain by the dependency graph — respect `bd ready` order; don't jump ahead.
- The proof chain (behavioral → mutation/empirical → STPA → hazard/Converge) is the credibility core — hold it to the strictest bar; trust-domain violations here are fatal.
- Finish with **`or-xg7` (smoke test)**: re-prove the whole loop end-to-end on a real greenfield idea (idea → proven runnable service + runbook + envelope, plus resumability).

## Running it
- One pass: `bd ready` → build+prove the next task → advance. Drive it repeatedly (or under `/loop`) until `bd stats` shows the `or-gb1` tree complete.
- Keep a running **epic rollup** (`id | title | verdict`, counts, ordered remediation backlog) so progress reflects *proven* completion against the PRD, not closed-issue count.
- **Never** advance on an unproven task. **Never** let the builder's claim stand in for the prover's verdict. If you can't prove it, it isn't done.
```
