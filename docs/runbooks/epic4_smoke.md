# Epic 4 Smoke Test Runbook

This runbook documents how to run the Epic 4 acceptance smoke test
(`test/acceptance/epic4_smoke.sh`), both the dry-run mode that is safe
to ship as a CI gate today, and the live-minikube restart-recovery
drill that `orion-e4f` extends once the Epic 4 slices are merged.

## Overview

Epic 4 ships the Conductor + Lookout + worker architecture and the
per-run K8s sandbox per `docs/SPEC/Orion-SPEC.md` §7, §10.2-3, §11,
§14, §18.2. The smoke test asserts:

1. The conductor fixture (1 service Go module with 3 known gaps + a
   2-issue backlog manifest) exists and is well-formed.
2. The pinned expected-shape (`expected_restart_drill_shape.json`) is
   a valid contract describing what "Epic 4 done" looks like.
3. The restart-drill Go test file compiles under the
   `epic4_live_minikube` build tag.
4. The orion-cli build (and `orion-worker` once that target lands) is
   buildable.
5. (live-minikube) A two-replica Conductor drill against minikube kills
   the leader mid-run and the standby leader resumes the in-flight
   work without double-spawning, respecting the fencing token, with
   the per-run namespace torn down by run-end.

This is a **bookend** test. The dry-run mode passes today (orion-e4a
ships the fixture + shape + dry-run gate). The live mode FAILS until
slices `orion-e41` (leader election), `orion-e42` (run + claim state
machines), `orion-e43` (WorkerSession + idempotent pod create),
`orion-e44` (orion-worker binary + AgentRunner), `orion-e45` (per-tenant
repo cache + NetworkPolicy), `orion-e46` (live K8s harness materializer),
`orion-e47` (Lookout reconciler), `orion-e48` (Conductor scheduler tick),
and `orion-e49` (live PR opening) are merged. That is deliberate. The
bookend pins the failing target.

## The fixture

Located at `test/acceptance/fixtures/epic4-conductor/`. A self-contained
Go module with three files (one per pattern) and a backlog manifest:

| File           | rvl-cli slug      | Control |
|----------------|-------------------|---------|
| `client.go`    | `missing-timeout` | RC-018  |
| `external.go`  | `missing-retry`   | RC-019  |
| `errors.go`    | `swallowed-error` | RC-021  |

`backlog.json` references two of these (one issue consumed pre-kill,
one post-leader-handover). The third gap exists so detection against
this fixture remains shape-compatible with the Epic 3 drill.

## Modes

### Dry-run (default, passes today)

```bash
./test/acceptance/epic4_smoke.sh --dry-run
```

Builds orion-cli, vets the restart-drill test under
`-tags=epic4_live_minikube`, runs the always-on acceptance contract
tests, and validates the fixture + expected-shape JSON. Requires only
Go + git. Use this as a CI gate.

### Live-minikube (failing target until E4-1..E4-9 close)

```bash
./test/acceptance/epic4_smoke.sh --live-minikube
```

Runs the kill-and-recover drill against a minikube cluster. The
operator must provision:

- A Postgres instance reachable as `POSTGRES_DSN` with the orion
  schema applied (`migrations/` from orion-e42 onward).
- A K8s namespace named by `ORION_NAMESPACE` with two Conductor
  Deployments wired to the same `POSTGRES_DSN` and `ORION_TENANT_ID`.
- The fixture checkout mounted into both Conductor pods at
  `ORION_FIXTURE_REPO_PATH` (mirroring the per-tenant repo cache that
  orion-e45 ships).
- A leader-lease TTL via `ORION_LEADER_LEASE_SECONDS` (default 30).

Until orion-e48 (the keystone) closes, the smoke script will exit 14
with a clear message instructing you to wait for the slices.

## Required binaries

| Binary       | Reason                                              |
|--------------|-----------------------------------------------------|
| `go`         | Build orion-cli + vet the drill                     |
| `git`        | Repo introspection inside the fixture               |
| `kubectl`    | (live-minikube) Kill the leader Conductor pod       |
| `minikube`   | (live-minikube) Provision the local cluster         |
| `psql`       | (live-minikube) Inspect the runs + claims tables    |
| `jq`         | (live-minikube) Parse fixture and shape JSON        |

## Required environment (live-minikube only)

| Variable                       | Notes                                   |
|--------------------------------|-----------------------------------------|
| `POSTGRES_DSN`                 | e.g. `postgres://orion:orion@127.0.0.1:5432/orion` |
| `ORION_TENANT_ID`              | UUID of the test tenant                 |
| `ORION_NAMESPACE`              | K8s namespace the Conductors run in     |
| `ORION_LEADER_LEASE_SECONDS`   | Advisory-lock lease TTL (default 30)    |
| `ORION_FIXTURE_REPO_PATH`      | Absolute path to fixture checkout       |

## Exit codes (CONTRACT)

| Code | Meaning                                                |
|------|--------------------------------------------------------|
| 0    | smoke passed                                           |
| 10   | drill or contract tests failed                         |
| 11   | fixture directory missing or malformed                 |
| 12   | expected_restart_drill_shape.json missing or malformed |
| 13   | restart-recovery invariant violated (live mode only)   |
| 14   | pre-condition failed (missing env, missing binary, slice not yet built) |
| 20   | safety violation: target resolved to upstream          |
| 30   | orion-cli or orion-worker build failed                 |
| 99   | unexpected error                                       |

## What the live drill will assert (when wired)

When `orion-e4f` closes the loop, `--live-minikube` will:

1. Apply the migrations from `orion-e42` against `POSTGRES_DSN`.
2. Bring up two Conductor pods both wired to the same DSN and tenant.
3. Wait for one to leader-elect via the PG advisory lock; record its
   `fencing_token`.
4. Insert the 2-issue backlog from `backlog.json`.
5. Wait for the Conductor to claim `fixture-issue-1` and spawn an
   orion-worker pod (idempotent on workspace key).
6. While the worker is `Running`, `kubectl delete pod` the leader
   Conductor.
7. Wait for the standby to acquire the lease (token incremented).
8. Assert: `runs` table shows exactly one row for the in-flight run,
   and the `worker_sessions` row carries the new fencing token.
9. Assert: only one worker pod with the workspace key exists in the
   cluster at any point in the drill.
10. Wait for the worker to complete; assert the per-run namespace is
    deleted by reaper grace.

The exact assertion shell is the `TestEpic4RestartRecoveryDrill`
function in `epic4_restart_drill_test.go`, invoked via
`go test -tags=epic4_live_minikube`.

## Reset between runs

```bash
# Drop and recreate the per-test schema, then redeploy the Conductors.
psql "$POSTGRES_DSN" -c "DROP SCHEMA orion CASCADE; CREATE SCHEMA orion;"
kubectl -n "$ORION_NAMESPACE" delete deployment -l app=conductor
kubectl -n "$ORION_NAMESPACE" delete pod -l app=orion-worker || true
kubectl -n "$ORION_NAMESPACE" delete ns -l owner=orion --field-selector status.phase!=Terminating || true
```

The smoke wrapper does not perform this reset automatically. Operators
run it before each drill.

## Pattern substitution note

`orion-cd7` names "K8s namespace teardown" as part of the §10.3 sandbox
isolation criteria. The drill asserts namespace teardown indirectly via
the `namespace_torn_down_on_exit` invariant; the orion-e4f wiring will
replace that with a live `kubectl get ns` watch.
