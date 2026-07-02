---
title: Orion-Obligation-Vocabulary-Design
issue: or-v9f.3
status: approved-design
date: 2026-07-02
supersedes: none
related: [Orion-NewBehavior-Proof-Design.md]
---

# Orion Obligation Vocabulary — Ratified Command Cases with a Single Shared Oracle

## 1. Problem (or-v9f.3, audit-rated BLOCKER)

Orion's unit of proof is `spec.BehavioralCase{RequestShape, ExpectShape}` — an HTTP request/response. Every gate (`proof.EnforceObligations`, the or-y9d kill) keys on content-addressed case IDs, so "done means proven" holds ONLY for single-file Go HTTP endpoints. CLIs, libraries, workers, lifecycle behavior, side-effects, file artifacts, concurrency invariants, persistence, and exit-code contracts (R1–R4, R6–R10 of the reference set) cannot become executed obligations. This design generalizes the vocabulary without touching the verdict core.

## 2. Decision

Adopt a **kind-tagged closed case union** (Design 1's skeleton) executed as **two observation channels over one oracle-semantics source** (Design 3's unification, re-grounded), shipped by **maximal reuse of the marker/obligation machinery that already works** (Design 4's plan), hardened by **an entry-conformance fast diagnostic** (Design 2's graft).

Non-negotiable invariants carried forward:

- **C1** Verdicts are mechanical: closed op/assertion vocabulary, compile-validated; an unknown kind is a compile error, never a silent pass (or-y9d at the source).
- **C2** Trust wall: the corpus, the prober, and marker parsing are harness-authored from ratified case data and unreachable from generation. (Precision note, per judge review: the generator DOES read case data — `GenSpec.Cases` is the contract, today and after. The wall is who authors and executes the CHECKS, not case secrecy.)
- **C3** Dual-mode by default; a case may narrow modes only for an enumerated, machine-checked rationale, and `aggregateObligations` already implements "passed in every mode that ran it" — zero gate changes.
- **C4** IDs hash the authored SURFACE case data (never lowered/compiled output); legacy IDs are byte-identical.
- **C5** Additive: `checkCaseLive`, the HTTP testsynth path, `legacyCorpus`, `ComputeHash`'s legacy carve-out, and `proof.go` are untouched. Shadow → measured cutover.
- **C6** The grill authors JSON surface shapes a human can review; `preview_spec` renders one imperative line per case.
- **C7** Execution obeys proofexec/bwrap reality: CGO_ENABLED=0 static binaries run in the lib-less cell; no network; nothing load-bearing assumes loopback inside bwrap.

## 3. Case model (`internal/orchestrator/spec/requirement.go`)

```go
type CaseKind string

const (
    KindHTTP CaseKind = ""     // legacy — JSON and hashing byte-identical
    KindExec CaseKind = "exec" // ratified argv against the built artifact ($BIN)
    KindUnit CaseKind = "unit" // Phase 2 — exported/in-package call/assert (from newbehavior.SynthTest)
    KindFile CaseKind = "file" // Phase 2 — static artifact-tree assertions
)

type BehavioralCase struct {
    ID     string   `json:"id"`
    Kind   CaseKind `json:"kind,omitempty"`
    // http (legacy, VALUE-typed on purpose — zero churn at existing call sites;
    // non-http kinds must leave these zero-valued, enforced by the validator;
    // their serialized empty objects are cosmetic: identity never includes them).
    Request RequestShape `json:"request"`
    Expect  ExpectShape  `json:"expect"`
    Exec *ExecCase `json:"exec,omitempty"`
    Unit *UnitCase `json:"unit,omitempty"` // Phase 2
    File *FileCase `json:"file,omitempty"` // Phase 2
    // ModesApply: default ["behavioral","empirical"]. Narrowing is INVALID on http
    // and on exec run-op cases; where allowed (Phase 2+) it requires ModesRationale
    // from the closed enum {process_exit, cross_process_persistence,
    // concurrent_scheduling}. ModesApply is hashed into the case ID.
    ModesApply     []string `json:"modes_apply,omitempty"`
    ModesRationale string   `json:"modes_rationale,omitempty"`
}

type ExecCase struct {
    Seed  []FileSeed `json:"seed,omitempty"` // scratch-dir pre-state
    Steps []ExecStep `json:"steps"`          // Slice 1: exactly one; final: 1..16 sequential
}
type FileSeed struct{ Path, Content string }

type ExecStep struct {
    Op    string            `json:"op,omitempty"` // ""=="run"; Phase 3: start|signal|await|put_file; Phase 4: sink
    Argv  []string          `json:"argv"`         // argv[0] MUST be "$BIN"
    Stdin string            `json:"stdin,omitempty"`
    Env   map[string]string `json:"env,omitempty"` // key-allowlisted (Slice: TZ)
    // Phase 3 (signal op): Signal string; Target int; InFlight *RequestShape (R4 drain clause)
    // Phase 4 (run op):    Concurrent int (2..8)
    Expect StepExpect `json:"expect"`
}

type StepExpect struct {
    Exit     *int              `json:"exit,omitempty"`
    WithinMS int               `json:"within_ms,omitempty"` // semantic; hashed; default 15000, cap 60000
    Stdout   []StreamAssertion `json:"stdout,omitempty"`
    Stderr   []StreamAssertion `json:"stderr,omitempty"`
    // Phase 3: Files []FileAssertion (post-state) ; Phase 4: ExitZeroCount *int, Sink []SinkAssertion
}

type StreamKind string // closed: exact | contains | regex | empty | rfc3339_utc  (Phase 2: json_key_present, json_key_rfc3339)
type StreamAssertion struct {
    Kind  StreamKind `json:"kind"`
    Value string     `json:"value,omitempty"`
    Key   string     `json:"key,omitempty"`
}
```

Semantic kinds are deliberate (judge consensus): the grill never hand-authors an RFC3339 regex a human cannot audit — this is the direct heir of `BodyAssertion`/`AssertionKind`.

### 3.1 Validation battery (compile-time; extends `validateCase` by kind dispatch)

Exec cases are rejected unless ALL hold: `Exec != nil`; Request/Expect zero-valued; step count within cap; `Op` in the closed set; `Argv` non-empty with `Argv[0] == "$BIN"` and no other `$` tokens; Stdin ≤ 64KiB; Env keys on the allowlist and never matching reserved prefixes (`PATH`, `HOME`, `GO*`, `LD_*`, `ORION_*`); Seed paths `filepath.Clean`ed, relative, no `..`, ≤ 32 files / 256KiB total; at least one of Exit/Stdout/Stderr (no vacuous obligations); `WithinMS ≤ 60000`; every `regex` compiles as RE2 **and does not match the empty string**; `contains`/`exact` values non-empty. A case the proof domain cannot mechanically run **never anchors** — the or-y9d invariant, extended.

### 3.2 Identity (C4)

```go
func caseID(c BehavioralCase) string {
    if c.Kind == KindHTTP {
        // EXACT legacy bytes: {"r":RequestShape,"e":ExpectShape} — every anchored
        // ID, spec hash, and contradiction group is untouched.
    }
    // non-http: sha256 over {"k":kind, "m":modes_apply, "x":<kind payload>}, first 12 hex.
}
```

- Legacy `requirementID` and `ExecutableSpec.ComputeHash` are byte-stable: new fields are `omitempty` and absent on legacy data; scalar-only specs keep the existing Cases-exclusion carve-out.
- Timeouts/mode-sets are semantics, so they ARE hashed. Infra flake is handled at execution by `ORION_PROOF_TIME_SCALE` (float ≥ 1.0, multiplies every deadline, recorded in the ModeReport detail) — calibration never re-anchors a spec.

### 3.3 Contradictions (compile-time, `contradiction.go`)

`requestKey` generalizes to `stimulusKey`: http unchanged; exec = canonical JSON of {seed, argv, stdin, env} (expects excluded). Decidable conflicts only, same conservative posture as today: identical stimulus demanding different `Exit`; mutually exclusive `exact` values or `exact`+`empty` on the same stream. Regex-vs-regex conflicts are documented out of scope (they compose or fail at proof time; flagged in preview when both cases render adjacently).

## 4. Two channels, one oracle

### 4.1 `internal/proof/casecheck` — the single source of assertion semantics

One stdlib-only Go file defines the oracle: `OrionCheckExit(want, got int) (bool, string)` and `OrionCheckStream(kind, value, key, got string) (bool, string)`. It is:

1. **compiled into the harness** — the empirical prober calls it directly; and
2. **embedded verbatim into the behavioral corpus** — `//go:embed casecheck.go`; `casecheck.Source("main")` rewrites the package clause; testsynth ships it as `orion_casecheck_test.go` beside the corpus.

One implementation, two compilation contexts. The dual-oracle divergence class (Design 1's structural risk) is eliminated; mode independence survives because the CHANNELS differ (below). Guarded by golden semantic vectors and a test that compiles the embedded copy standalone (proving stdlib-only).

### 4.2 Behavioral channel (in-process)

The CLI/worker generation contract (GenSpec `ProgramFamily:"cli"`, `EntrySymbol:"run"`) requires:

```go
func run(args []string, stdin io.Reader, stdout, stderr io.Writer, env map[string]string) int
func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, orionEnv())) } // thin main
```

Per exec case, testsynth emits `Test_obl_<id>` bracketed by the existing `ORION_OBLIGATION_RUN/PASS:<id>` markers: seed `t.TempDir()`, `t.Chdir`, call `run(...)` with buffers, assert via the embedded casecheck. `behavioral.Prove`, `parseObligations`, and the **mutation gate run unchanged** — mutants target `run` exactly as they target `handleTime`, in-process, so there is **no per-mutant binary rebuild** (this resolves Design 1's 10–50× mutation-cost risk outright).

### 4.3 Empirical channel (real process)

`empirical.Prove` splits cases by kind. Exec cases: stage + `proofexec.GoToolchain build -o bin .` once (CGO_ENABLED=0 already set in `toolEnv` — the static binary is exactly what runs inside the lib-less bwrap cell, the or-qoa lesson). Per round (`ORION_PROOF_RUN_COUNT`, existing `modeFromRuns` merge — any-round-executed, every-round-passed, mix → Inconclusive): fresh scratch dir, write seeds, execute the REAL binary with the ratified argv under `sandbox.Backend.Run` (bwrap: rw scratch workdir, ro-bind of the bin dir, no network, scrubbed env + case env; "none" fallback = `safeenv` + Setpgid — today's probe posture, warn-once). Capture exit/stdout/stderr; evaluate via the compiled-in casecheck. Obligations flow into `ProbeResult.Cases` → `aggregateObligations` → `EnforceObligations` **with zero changes to proof.go's gate logic**.

### 4.4 Why this is genuine dual-mode

Behavioral proves the entry function's behavior and carries the mutation ratchet; empirical proves the SHIPPED process — argv parsing in `main`, real exit codes, env plumbing, the OS boundary. Different failure surfaces (the run()/main drift Designs 2/4 worried about is caught empirically), shared assertion meaning (no divergent-oracle bug possible). For exec run cases both modes are REQUIRED — the ModesApply rationale enum contains no reason to drop either.

### 4.5 Entry-conformance fast diagnostic (Design 2's graft)

`diagnostics.CheckEntry(artifactDir, "run")` (go/parser; go/types with the `unit` kind in Phase 2) runs in `ProveAllWithThreshold`'s fast tier when exec cases are present. A missing/mis-signed `run` yields a targeted Reject diagnostic fed to the refinement loop — never a corpus-compile failure cascading into all-obligations-Inconclusive spin.

## 5. Elicitation (C6)

`add_requirement`'s `cases` schema becomes a oneOf on `kind` with every closed enum published in the JSON schema (defense in depth: schema rejects, then `ValidateRequirement` re-rejects). Raw plumbing shapes never exist — the case IS the surface shape, which is also what is hashed and what the human reviews (this simultaneously satisfies Design 3's reviewability rule and fixes its anchor flaw).

R10 sample:

```json
{"text":"verify exits 0 on a clean tree and 3 when secrets are found, listing each file:line on stderr",
 "cases":[
  {"kind":"exec","exec":{"seed":[{"path":"src/a.go","content":"var key = \"AKIA...\""}],
   "steps":[{"argv":["$BIN","verify"],"expect":{"exit":3,
     "stderr":[{"kind":"regex","value":"(?m)^\\S+\\.go:\\d+"}]}}]}},
  {"kind":"exec","exec":{"seed":[{"path":"src/a.go","content":"package a"}],
   "steps":[{"argv":["$BIN","verify"],"expect":{"exit":0}}]}}]}
```

`preview_spec` renders one imperative line per case: `$ verify → exit 3, stderr ~ /^\S+\.go:\d+/ [seeded: src/a.go]`; R1: `$ orion-date --tz=Bogus → exit 2, stderr contains "Bogus"`. Compile errors (bad op, vacuous regex, reserved env) surface in-conversation; `stimulusKey` contradiction detection catches "same argv+seed, different exit" while the developer is present.

## 6. Coverage vs the ten reference requirements

| Req | Kind / mechanism | Modes | Phase |
|---|---|---|---|
| R1 CLI time | exec run ×2 (`rfc3339_utc` stdout; exit 2 + `contains` stderr) | both | **Slice 1** |
| R2 library error | unit: Call `ParseConfig` + seed, `WantErrRE` | both (in-pkg test / driver process) | 2 |
| R3 worker | exec `--once` reshaping (run + files-after) dual-mode; watch-loop lifecycle via start/put_file/await/signal | both / empirical-only (`process_exit`) | 3 |
| R4 SIGTERM | signal op: exit-half (`exit 0 within 2000ms`); full drain via `in_flight` RequestShape | empirical-only (`process_exit`) | 3 / 4 |
| R5 HTTP | legacy kind, byte-identical | both | shipped |
| R6 webhook | sink capture; behavioral httptest via run() env, empirical harness loopback listener | both (cli-shaped) | 4 |
| R7 artifact file | file kind, identical deterministic check | both (trivial convergence) | 2 |
| R8 concurrency | run + Concurrent:2 + ExitZeroCount:1; variance→Inconclusive is the CORRECT verdict for a racy artifact; honestly a sampling gate, not a race-freedom proof | empirical-only (`concurrent_scheduling`) | 4 |
| R9 persistence | unit multi-step; Restart step = fresh driver process (genuine boundary); needs recursive staging | behavioral for API shape; restart steps empirical-only (`cross_process_persistence`) | 2 |
| R10 exit codes | exec run ×2 with seeds | both | **Slice 1** |

## 7. Migration (C5) and the measured cutover

- **Slice 1 (Phase 0+1, one landable unit):** types + validation + IDs + contradictions; casecheck; exec testsynth emitter; execprobe; empirical routing; entry diagnostic; GenSpec.ProgramFamily; add_requirement/preview; `ORION_EXEC_CASES` shadow (exec IDs recorded in the report, filtered from `EnforceObligations`' required set in `conductor/build.go`). Legacy goldens pin byte-identity throughout.
- **Phase 2:** unit + file kinds; go/types conformance; recursive `stageArtifact`; json_key stream kinds; ModesApply narrowing + rationale enum.
- **Phase 3:** multi-step exec, files-after, put_file/await/start/signal; `sandbox.Backend.Start(Spec) Handle{Signal,Wait}` extension (bwrap pgid signaling spike first; "none" fallback keeps today's posture).
- **Phase 4:** sink capture (harness-side loopback; in-cell lo-up is a gated spike, never an assumption), Concurrent/ExitZeroCount, signal.in_flight.
- **Phase 5 (cutover):** flip `ORION_EXEC_CASES=required` when the shadow metric passes: **≥ 50 real proof runs carrying exec cases, infra-attributed false-Inconclusive < 2%, zero unexplained behavioral/empirical divergence on passing artifacts.** Then converge brownfield newbehavior onto casecheck/execprobe as shared `internal/proof/execcase`.

Untouched forever by this design: `EnforceObligations`, `aggregateObligations`, `truthalign.Converge`, the RUN/PASS marker protocol, `checkCaseLive`, the legacy corpus, and every anchored legacy hash.

## 8. Governance ratchet

A new op or assertion kind DOES NOT EXIST until a single PR lands: validator entry, casecheck semantics, both channel lowerings, golden semantic vectors, and preview rendering. Vocabulary pressure (async HTTP, timers, pipes, multi-binary topologies) is answered by design review against this checklist, not by accretion.

## 9. Risks and mitigations

1. **Runner flake → Inconclusive storms** (signals, deadlines, loaded CI): shadow phase measures it before any verdict depends on it; `ORION_PROOF_TIME_SCALE` absorbs infra slowness without re-anchoring; timing-sensitive ops (signal/await) arrive only in Phase 3, after the run-op baseline is proven quiet.
2. **run()/main drift**: empirical real-binary channel is mandatory for exec cases; thin-main convention is stated in the generation contract; `CheckEntry` gives targeted feedback. Residual: main-only behavior not reachable via argv — accepted and documented, same honesty bar as R8's sampling gate.
3. **Vocabulary creep**: governance ratchet (§8); the closed-set validator makes every unknown op a compile error, so creep is visible in review, never silent.
4. **Oracle bug passes both channels** (single casecheck source): mitigated by golden vectors, the standalone-compile test, and the fact that a casecheck bug fails LOUD in both channels symmetrically (fails-safe toward Reject/Inconclusive, not false Accept, for every check whose predicate is "must match").
