---
title: Orion New-Behavior Proof — Design (multi-modal, ratified-case-driven)
status: approved
created: 2026-06-25
authors: Joseph Bironas
epic: or-3p5
issue: or-3p5.3
related:
  - docs/PRD/orion-v2.md                       # Phase E proof; Trust Domains; or-3ba (non-HTTP proof)
  - docs/SPEC/Orion-Memory-Recall-Design.md    # bounded-coordinator-inference invariant
supersedes_note: reborn scope — the original or-3p5.3 assumed the HTTP/entry-symbol testsynth; the dogfood showed real changes are non-HTTP, so this is multi-modal.
---

# Orion New-Behavior Proof — Design

> Brainstormed + approved 2026-06-25. Scope of **this** design = the proof **mechanism**
> (sub-project 1). The change-spec **elicitation/ratification** UX (model proposes cases,
> developer ratifies) is **sub-project 2**, a follow-on design that connects to or-26w.

## 1. Context & problem

Dogfood run #1 proved the brownfield change loop works end-to-end — `orion change` worktrees
off HEAD, a model writes the diff, the **regression gate** (the repo's own `go test`, green-
before → green-after) holds, and it commits a review branch. But that only proves **do no
harm**; it does *not* prove the change does **what was asked**. That gap is or-3p5.3.

Two facts shape the mechanism:
- **Real self-host changes are non-HTTP** (e.g. `Verdict.String()`, `Tier.String()`), so the
  existing HTTP-handler-shaped `testsynth`/`empirical` modes can't prove them.
- **`sandbox.Run(Spec{Argv,Workdir,Env,AllowNet})` already executes arbitrary argv** under
  bwrap isolation (egress denied, secrets scrubbed via `safeenv`). "Build the binary and curl
  it" / "run a shell assertion" need a *proof modality that drives `sandbox.Run`*, not new infra.
  The `ORION_OBLIGATION_RUN/PASS` markers and `truthalign.ModeResult` are already modality-agnostic.

**Oracle decision (the soundness core).** The expected behavior is captured as **ratified
behavioral cases**, established as part of the change spec and ratified by the developer
**before generation**. The model *proposes* cases (HYB), the developer *ratifies* (the trust
gate), the harness *proves* against the ratified cases. Because the cases predate the diff, the
proof oracle is independent of the generated code **by construction** — the cleanest form of "no
agent grades its own homework," and the same way greenfield already earns trust
(`spec → ratified ResponseContract.Cases → ProofObligation`). This sub-project consumes ratified
cases; how they are elicited/ratified is sub-project 2.

## 2. Where it lives & how it plugs in

New package `internal/proof/newbehavior`:

```go
func ProveNewBehavior(ctx context.Context, artifactDir string, cases []Case) (truthalign.ModeResult, error)
```

It is a **separate brownfield path**, NOT a 4th mode in greenfield `ProveAll` (that proves a
whole artifact; this proves change-cases). `ChangeAndProve` calls it **after** the regression
gate and **before** commit, gating the commit on **regression-held AND new-behavior=Accept**.
Reuses `sandbox.Run` (executor), `safeenv` (scrub), the `ORION_OBLIGATION_*` markers,
`truthalign.ModeResult`, and `proof.EnforceObligations`.

## 3. Case schema (two ratified modalities)

```go
type Case struct {
    ID       string // content-addressed (hash of the modality payload)
    Modality string // "synth_test" | "command"
    Synth    *SynthTest // set iff Modality == "synth_test"
    Command  *Command   // set iff Modality == "command"
}

// synth_test — prove a func/method in the changed package.
type SynthTest struct {
    Pkg  string // package dir/import path (the changed surface)
    Call string // a Go expression, e.g. `dependencyprovenance.Verdict{OK:true,Reason:"x"}.String()`
    Want string // expected value as a Go literal, e.g. `"OK: x"`
}

// command — prove a binary/endpoint/CLI via the sandbox.
type Command struct {
    Setup        [][]string // argv steps run first (e.g. [["go","build","-o","svc","."]])
    Assert       []string   // argv whose exit/stdout is asserted (e.g. ["curl","-s","localhost:$PORT/x"])
    ExpectExit   int
    ExpectStdout string     // substring, or /regex/ when wrapped in slashes
}
```

The oracle (`Want` / `ExpectExit` / `ExpectStdout`) is the **ratified case** — never the
generator. Cases are validated at intake (mirroring the or-y9d invariant): a `synth_test` needs
`Pkg`+`Call`+`Want`; a `command` needs a non-empty `Assert`. An invalid case is rejected, not
silently skipped.

## 4. `synth_test` modality

The harness synthesizes a plain-Go call/assert test into the changed package:

```go
func Test_obl_<id>(t *testing.T) {
    fmt.Println("ORION_OBLIGATION_RUN:<id>")
    got := <Call>
    want := <Want>
    if !reflect.DeepEqual(got, want) { t.Fatalf("...") }
    fmt.Println("ORION_OBLIGATION_PASS:<id>")
}
```

Run `go test -run Test_orionNB_<id> ./<Pkg>` under **`safeenv`** (host secrets scrubbed), like
the brownfield regression gate — so the existing module's deps resolve (the greenfield
`proofexec.GoToolchain` forces `GOPROXY=off`/fresh-GOPATH and cannot). Parse the markers. This
**generalizes `testsynth` beyond HTTP** — a plain call, not
`entry(w,req)`. The harness authors the test (proof domain); the generator wrote the code
(generation domain) → the wall holds. *Proves `Verdict.String()`.*

## 5. `command` modality

The harness runs the `Setup` argv(s), then the `Assert` argv, under **`safeenv`** (same
isolation as the brownfield regression gate — secrets scrubbed, module cache available so a
build resolves deps), asserting `exit == ExpectExit` and stdout matches `ExpectStdout`
(substring, or a regexp in `/slashes/`). The harness runs the command and records the
obligation directly (no synthesized-test markers). *Proves CLIs and the build-binary-and-curl
example — a service+curl is expressed as an `sh -c "go build … && ./svc & … && curl …"` Assert.*

**Networking (deferred hardening).** V1 runs at the regression-gate isolation level — network
is not yet sandbox-restricted (the regression gate already runs brownfield code under `safeenv`
without a net restriction, so this is consistent). Loopback-only bwrap (external egress denied)
is the same deferred hardening as for the synth_test exec; the `AllowNet`/bwrap seam extends it
later. (Accepted: "loopback-only fine for now, extend later.")

## 6. Verdict & gate

`ProveNewBehavior` returns `ModeResult{Mode:"new_behavior", Obligations: <per-case status>}`.
`EnforceObligations(requiredCaseIDs, …)`: every ratified case must **execute and pass** →
Accept; a case that didn't run → Inconclusive (coverage hole); a case that failed → Reject (the
reason names the case). `ChangeAndProve` commits only on **regression-held AND new-behavior
Accept**; otherwise it leaves the change uncommitted with the reason.

## 7. Trust wall (consolidated; each has a test)

1. Oracle = ratified case (`Want`/`ExpectStdout`/`ExpectExit`), never the generator.
2. The harness authors every synth test and runs every command — the generation agent supplies
   neither the test nor the assertion.
3. Generated code executes under the **same isolation as the brownfield regression gate** —
   `safeenv` (host secrets scrubbed) so the module's deps resolve; the greenfield path's full
   bwrap is a separate hardening (the regression gate doesn't bwrap either). The `command`
   modality adds loopback-only networking with external egress denied.
4. A test asserts the generator cannot supply the proof oracle (cases come from the ratified
   change-spec, a separate input).

## 8. Interim case input (bridge until sub-project 2)

`orion change --cases <file.json> "<intent>"` — the developer writes/ratifies the cases file
(the human *is* the oracle → sound). This makes the mechanism unit-testable (hand-fed cases) and
dogfoodable **now**, before the elicitation UX exists. Sub-project 2 (model proposes cases →
developer ratifies, in the change-spec flow) replaces the hand-written file later; the in-memory
`[]Case` contract `ProveNewBehavior` consumes is unchanged by that swap.

## 9. Testing

Fixture-repo tests: a pure-func change proven via `synth_test` (e.g. an `Add`/`String`); an
endpoint change proven via `command` (build + loopback curl); a wrong implementation → Reject; a
case that doesn't run → Inconclusive; the trust test (generator cannot supply the oracle); the
sandbox isolation test (no external egress, secrets scrubbed). Each modality and the
`ChangeAndProve` gate get coverage; the heavy conductor suite (~190s) remains the integration gate.

## 10. Out of scope (deferred)

- **Change-spec elicitation/ratification UX** (model proposes cases; developer ratifies in the
  change flow) → **sub-project 2**, connects to or-26w.
- **Intended-behavior-change vs. regression reconciliation** (a change that *intentionally*
  alters existing behavior makes an existing test "regress" legitimately) → sub-project 2.
- **Extra typed modalities** (`http_probe`, `cli_probe`, `job_status`) and the agent-facing
  `run_proof_step` tool → later, when cases demand them.
- **Hazard-on-blast-radius** for the change → or-3p5.4.

## 11. Decomposition

| Slice | Scope | Proves |
|---|---|---|
| **1a** | Case schema + validation, `synth_test` modality, `ProveNewBehavior`, `EnforceObligations` wiring, `ChangeAndProve` gate, `--cases` input | `Verdict.String()` end-to-end (no network) |
| **1b** | `command` modality (sandbox `Setup`+`Assert`, loopback net, exit/stdout assertion) | endpoints/CLIs (build + loopback curl) |

Slice 1a re-dogfoods the original `Verdict.String()` task — but now *proven*, not just do-no-harm.
Sub-project 2 (elicitation) is a separate design + decomposition.
