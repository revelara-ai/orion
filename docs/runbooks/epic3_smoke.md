# Epic 3 Smoke Test Runbook

This runbook documents how to run the Epic 3 acceptance smoke test
(`test/acceptance/epic3_smoke.sh`) — both the dry-run mode that is safe
to ship as a CI gate today, and the live 3-tick drill that orion-e3f
extends once the Epic 3 slices are merged.

## Overview

Epic 3 ships the continuous detection loop with backlog-depth-aware
progressive disclosure per `docs/SPEC/Orion-SPEC.md` §15. The smoke
test asserts:

1. The detection fixture (3 known gaps, one per curated rvl-cli matcher)
   exists and is well-formed.
2. The pinned expected-shape (`expected_detection_shape.json`) is a
   valid contract describing what "Epic 3 done" looks like.
3. The orion detection subprocess wrapper is buildable and its unit
   tests are green.
4. (live) A 3-tick drill against an operator-provisioned binding
   produces the documented provenance, dedup, and quiescence behavior.

This is a **bookend** test. The dry-run mode passes today (orion-e3a
ships the fixture + shape + dry-run gate). The live mode FAILS until
slices `orion-e31` (schema), `orion-e32` (LoopDriver), `orion-e33`
(scheduler), `orion-e34` (quiescence), `orion-e35` (risksink),
`orion-e36` (cap), and `orion-e37` (loopguard) are merged. That is
deliberate — the bookend pins the failing target.

## The fixture

Located at `test/acceptance/fixtures/epic3-detection/`. A self-contained
Go module with three files, one per pattern:

| File          | rvl-cli slug      | Control |
|---------------|-------------------|---------|
| `client.go`   | `missing-timeout` | RC-018  |
| `external.go` | `missing-retry`   | RC-019  |
| `errors.go`   | `swallowed-error` | RC-021  |

### Pattern substitution note

orion-3q8 (the parent epic) names the third gap as "idempotency".
rvl-cli does not currently ship a curated idempotency matcher; the
closest curated matchers in the `fault_tolerance` family are
`missing-timeout`, `missing-retry`, and `swallowed-error`. The bookend
uses `swallowed-error` as the third gap so the fixture exercises three
distinct, deterministic, currently-shipping matchers. When an
idempotency matcher lands (likely in rvl-cli's `dev_practices` family
or a new family), update `expected_detection_shape.json` and add a
fourth fixture file rather than removing this one.

## Modes

### Dry-run (default — passes today)

```bash
./test/acceptance/epic3_smoke.sh --dry-run
```

Builds orion + orion-cli, runs detection unit tests, validates the
fixture and expected-shape JSON exist and are well-formed. Requires
only Go + git. Use this as a CI gate.

### With Postgres (failing target until orion-e31)

```bash
./test/acceptance/epic3_smoke.sh --dry-run --with-pg
```

Additionally exercises the `detection_runs` / `detection_findings`
testcontainer-pg integration tests. This is **expected to fail** until
orion-e31 ships the schema and the repo layer. The smoke script
surfaces the failure as `expected-fail` rather than aborting the run,
so it can sit in CI as a leading indicator without breaking the build.

### Live (failing target until orion-e31..e37 all merge)

```bash
./test/acceptance/epic3_smoke.sh --live
```

Requires every prerequisite below. Refuses to run if any are missing.
Also refuses to run if the `orion-cli detection trigger` subcommand
(from orion-e33) is not present in the built binary — exits with code
14 and a clear message naming the missing slices.

## Operator Prerequisites (Live Mode)

### 1. Postgres

Orion's detection results persist to Postgres. Local Docker Compose
works:

```bash
docker-compose up -d postgres
export POSTGRES_DSN="postgres://orion:orion@localhost:5432/orion?sslmode=disable"
# Apply migrations
make migrate
```

The `detection_runs` and `detection_findings` tables land in
orion-e31. Until then `make migrate` will not create them.

### 2. Operator-provisioned binding

The detection loop runs per-binding. orion-e33's scheduler reads the
binding row to know which repo to scan and which tracker to file
into. v1 seeds the binding via SQL; the API endpoint lands in
orion-e80+.

```bash
export ORION_BINDING_ID=<uuid of TrackerBinding row>
export ORION_ORG_ID=<uuid of the owning organization>
```

### 3. Polaris API (optional in v1)

If `POLARIS_BASE_URL` is set, the risksink (orion-e35) calls
`POST /api/v1/risks` once per new finding with
`origin = orion-detection`. If unset or unreachable, the local
fallback queues each risk to `risksink_pending` and the detection
tick still exits 0.

```bash
export POLARIS_BASE_URL="https://polaris.relynce-dev.example/api/v1"
# (optional auth — depends on polaris auth mode)
export POLARIS_API_KEY=<token>
```

### 4. rvl-cli

The detection loop invokes `rvl-cli scan --local --format=json` as a
subprocess. `rvl --version` must succeed before running the live mode.

```bash
which rvl                # must succeed
rvl --version            # confirms binary is reachable
```

## The 3-Tick Drill

Once all Epic 3 slices are merged, orion-e3f extends `--live` to drive
the full drill. Until then this section is operator-manual via the
`orion-cli detection trigger` subcommand (lands in orion-e33).

```bash
# Tick 1: initial scan against the fixture.
orion-cli detection trigger --binding=$ORION_BINDING_ID --org=$ORION_ORG_ID

# Expected outcome:
#   - DetectionRun row inserted; phase=completed
#   - 3 detection_findings rows (one per fixture gap)
#   - 3 tracker issues filed (or 3 risksink_pending rows if Polaris unreachable)

# Tick 2: re-run against unchanged fixture.
orion-cli detection trigger --binding=$ORION_BINDING_ID --org=$ORION_ORG_ID

# Expected outcome:
#   - DetectionRun row inserted; phase=completed (or quiescent if backlog drained)
#   - 0 new findings (dedup short-circuit)
#   - 0 new autofile calls

# Operator action: introduce one new gap in the fixture (e.g., add a
# fourth file with another http.Client{ … } pattern) and commit it.
git -C test/acceptance/fixtures/epic3-detection commit -am "drill: new gap"

# Tick 3: scan after fresh commit.
orion-cli detection trigger --binding=$ORION_BINDING_ID --org=$ORION_ORG_ID

# Expected outcome:
#   - DetectionRun row inserted; phase=completed
#   - 1 new finding
#   - 1 new autofile call
```

### Assertions

After the drill, `expected_detection_shape.json` documents every
invariant the e3f live smoke asserts. The high-points:

- `detection_runs` row count = 3
- All rows in phase `completed` or `quiescent`
- Self-referential-loop warning did NOT fire (first-3-runs suppression
  per SPEC §15.4)
- Risksink behavior matches polaris-reachable vs polaris-unreachable
  branch based on `POLARIS_BASE_URL`

## Failure-Mode Table

| Exit code | Meaning                                              | Likely cause |
|-----------|------------------------------------------------------|--------------|
| 0         | Smoke passed                                         | -            |
| 10        | Detection unit tests failed                          | Regression in `internal/detection` |
| 11        | Fixture directory missing or malformed               | `test/acceptance/fixtures/epic3-detection/` was deleted or `go.mod`/Go files are missing |
| 12        | `expected_detection_shape.json` missing or invalid   | File was deleted, edited to invalid JSON, or `expected_findings` count drifted from 3 |
| 13        | 3-tick drill invariant violated (live only)          | DetectionRun row count wrong, dedup failed, provenance miscount, or loopguard fired |
| 14        | Pre-condition failed                                 | Missing env var, missing binary, or slice not yet merged (clear message names the slice) |
| 20        | Safety violation                                     | `FIXTURE_REPO` resolved to a forbidden upstream owner (e.g. GoogleCloudPlatform/*) |
| 30        | `make build` failed                                  | Compilation regression; inspect `$EVIDENCE_DIR/build.log` |
| 99        | Unexpected                                           | Investigate `$EVIDENCE_DIR/wrapper.log` |

## Environment Notes

### Go toolchain mismatch (gvm + system go)

If `go env GOROOT` reports a gvm-managed Go that does not match the
`go` binary in PATH (symptom: `compile: version "go1.X.Y" does not
match go tool version "go1.A.B"`), unset `GOROOT` before invoking the
smoke script:

```bash
env -u GOROOT ./test/acceptance/epic3_smoke.sh --dry-run
```

This is a per-environment issue, not a script bug; the script does
NOT bake in the workaround so it remains portable.
