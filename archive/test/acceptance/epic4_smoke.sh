#!/usr/bin/env bash
#
# Epic 4 acceptance smoke test (bookend for orion-e4a).
#
# Pins what "Epic 4 done" looks like. Defines the contract that the
# Conductor + Lookout + worker slices (E4-1 through E4-9) must satisfy
# by closure of orion-e4f. The test will NOT pass end-to-end until
# those slices land; that is the point of a bookend acceptance test.
#
# Modes:
#
#   --dry-run         (default) build orion-cli + orion-worker, validate
#                     the fixture, the expected-shape file, and the
#                     compile-time well-formedness of the restart-drill
#                     test file. Safe to run anywhere with Go + git.
#
#   --live-minikube   Full restart-recovery drill against minikube. Two
#                     Conductor replicas + fixture backlog + kill the
#                     leader mid-run + verify no double-spawn. Refuses
#                     to run until the slices that ship the Conductor
#                     are merged. Exits 14 with a clear message until
#                     then.
#
# Exit codes (CONTRACT; downstream slices assert against these):
#   0    smoke passed
#   10   drill or fixture unit tests failed
#   11   fixture directory missing or malformed
#   12   expected_restart_drill_shape.json missing or malformed
#   13   restart-recovery invariant violated (live mode only)
#   14   pre-condition failed (missing env, missing binary, slice not yet built)
#   20   safety violation: target resolved to upstream
#   30   orion-cli or orion-worker build failed
#   99   unexpected error
#
# Environment (live-minikube mode):
#   POSTGRES_DSN                  e.g. postgres://orion:orion@127.0.0.1:5432/orion
#   ORION_TENANT_ID               UUID of the test tenant
#   ORION_NAMESPACE               K8s namespace the Conductors run in
#   ORION_LEADER_LEASE_SECONDS    advisory-lock lease TTL (default 30)
#   ORION_FIXTURE_REPO_PATH       absolute path to the fixture checkout
#                                 (worker mounts this read-only)
#   ORION_OFFLINE=1               treat as dry-run-equivalent

set -uo pipefail
# Do NOT set -e: dry-run interprets specific non-zero exits from sub-steps
# as expected (e.g., live-mode refusal before slices ship).

readonly REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly FIXTURE_DIR="$REPO_ROOT/test/acceptance/fixtures/epic4-conductor"
readonly EXPECTED_SHAPE="$REPO_ROOT/test/acceptance/expected_restart_drill_shape.json"
readonly DRILL_TEST="$REPO_ROOT/test/acceptance/epic4_restart_drill_test.go"
readonly EVIDENCE_DIR="${ORION_EVIDENCE_DIR:-$REPO_ROOT/test/acceptance/run-logs/$(date +%Y%m%dT%H%M%SZ)}"
readonly UPSTREAM_FORBIDDEN_OWNERS=("GoogleCloudPlatform" "googlecloudplatform")

usage() {
  cat <<'USAGE'
epic4_smoke.sh [--dry-run | --live-minikube]

Modes:
  --dry-run         (default) build orion-cli + orion-worker, validate fixture/shape/drill compile. Go + git only.
  --live-minikube   full restart-recovery drill. Requires every operator prerequisite documented in docs/runbooks/epic4_smoke.md.

Environment:
  ORION_EVIDENCE_DIR     evidence directory (default run-logs/<timestamp>)
  ORION_OFFLINE=1        treat as no live conductor state (mirrors epic1/2/3 convention)
USAGE
}

mode="dry"
for arg in "$@"; do
  case "$arg" in
    --dry-run)         mode="dry" ;;
    --live-minikube)   mode="live_minikube" ;;
    -h|--help)         usage; exit 0 ;;
    "")                ;;
    *)                 usage; exit 14 ;;
  esac
done

mkdir -p "$EVIDENCE_DIR"
exec > >(tee -a "$EVIDENCE_DIR/wrapper.log") 2>&1

echo "=== Epic 4 acceptance smoke test ==="
echo "Mode:           $mode"
echo "Repo root:      $REPO_ROOT"
echo "Fixture:        $FIXTURE_DIR"
echo "Expected shape: $EXPECTED_SHAPE"
echo "Drill test:     $DRILL_TEST"
echo "Evidence:       $EVIDENCE_DIR"
echo

# --- Safety guard: never operate against upstream-owned fixtures ---
fixture_id="${FIXTURE_REPO:-epic4-conductor}"
owner="${fixture_id%%/*}"
if [ "$owner" != "$fixture_id" ]; then
  for forbidden in "${UPSTREAM_FORBIDDEN_OWNERS[@]}"; do
    if [ "$owner" = "$forbidden" ]; then
      echo "FATAL: target $fixture_id resolves to forbidden upstream owner $owner" >&2
      exit 20
    fi
  done
fi

# --- Common pre-conditions ---
require_bin() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "FATAL: $1 not in PATH" >&2
    exit 14
  fi
}
require_bin go
require_bin git

# --- Step 1: validate fixture + expected-shape + drill files ---
echo "[step 1/4] Validating fixture + expected-shape + drill artifacts..."
if [ ! -d "$FIXTURE_DIR" ]; then
  echo "FATAL: fixture directory missing: $FIXTURE_DIR" >&2
  exit 11
fi
if [ ! -f "$FIXTURE_DIR/go.mod" ]; then
  echo "FATAL: fixture is not a Go module: $FIXTURE_DIR/go.mod missing" >&2
  exit 11
fi
if [ ! -f "$FIXTURE_DIR/backlog.json" ]; then
  echo "FATAL: fixture missing backlog.json (the bookend pins a 2-issue backlog)" >&2
  exit 11
fi
fixture_go_files=$(find "$FIXTURE_DIR" -maxdepth 2 -name '*.go' -type f | wc -l)
if [ "$fixture_go_files" -lt 3 ]; then
  echo "FATAL: fixture has only $fixture_go_files Go files; need at least 3 (one per pattern)" >&2
  exit 11
fi
if [ ! -f "$EXPECTED_SHAPE" ]; then
  echo "FATAL: expected_restart_drill_shape.json missing: $EXPECTED_SHAPE" >&2
  exit 12
fi
if [ ! -f "$DRILL_TEST" ]; then
  echo "FATAL: restart-drill test missing: $DRILL_TEST" >&2
  exit 11
fi
if command -v jq >/dev/null 2>&1; then
  if ! jq empty < "$EXPECTED_SHAPE" >/dev/null 2>&1; then
    echo "FATAL: expected_restart_drill_shape.json is not valid JSON" >&2
    exit 12
  fi
  replicas=$(jq -r '.invariants.conductor_replicas // 0' < "$EXPECTED_SHAPE")
  if [ "$replicas" -lt 2 ]; then
    echo "FATAL: invariants.conductor_replicas=$replicas; bookend pins >=2" >&2
    exit 12
  fi
  issues=$(jq -r '.backlog.issues // 0' < "$EXPECTED_SHAPE")
  if [ "$issues" != "2" ]; then
    echo "FATAL: backlog.issues=$issues; bookend pins 2" >&2
    exit 12
  fi
  if ! jq empty < "$FIXTURE_DIR/backlog.json" >/dev/null 2>&1; then
    echo "FATAL: fixture backlog.json is not valid JSON" >&2
    exit 11
  fi
  backlog_count=$(jq -r '.issues | length' < "$FIXTURE_DIR/backlog.json")
  if [ "$backlog_count" != "2" ]; then
    echo "FATAL: fixture backlog has $backlog_count issues; bookend pins 2" >&2
    exit 11
  fi
fi
echo "  ok (fixture: $fixture_go_files Go files, backlog: 2 issues, shape: replicas>=2)"

# --- Step 2: build orion-cli (and orion-worker once it lands) ---
# orion-worker does not exist yet (orion-e44). Until then, only the
# orion-cli build is exercised. The smoke does NOT abort on a missing
# `make build-worker` target because that target is itself part of the
# slices the bookend pins as failing.
echo "[step 2/4] Building orion-cli..."
if ! (cd "$REPO_ROOT" && make build) > "$EVIDENCE_DIR/build.log" 2>&1; then
  echo "FATAL: make build failed; see $EVIDENCE_DIR/build.log" >&2
  exit 30
fi
if grep -q '^build-worker:' "$REPO_ROOT/Makefile" 2>/dev/null; then
  echo "  build-worker target detected; building..."
  if ! (cd "$REPO_ROOT" && make build-worker) >> "$EVIDENCE_DIR/build.log" 2>&1; then
    echo "FATAL: make build-worker failed; see $EVIDENCE_DIR/build.log" >&2
    exit 30
  fi
fi
echo "  ok"

# --- Step 3: compile-check the restart-drill test ---
# The drill is behind a build tag; we exercise the test-build path
# without running the tests so the file stays well-formed across
# changes by E4-1..E4-9. The always-on acceptance contract tests
# (TestEpic4*) are NOT invoked here on purpose: they exec this script
# via exec.Command, so running them inside the script would fork-bomb
# the box. The contract tests run from `make test` via the normal
# go test ./... path.
echo "[step 3/4] Compile-checking restart-drill test (tags=epic4_live_minikube)..."
if ! (cd "$REPO_ROOT" && go vet -tags=epic4_live_minikube ./test/acceptance/...) > "$EVIDENCE_DIR/drill_vet.log" 2>&1; then
  echo "FATAL: drill test failed go vet; see $EVIDENCE_DIR/drill_vet.log" >&2
  exit 10
fi
echo "  ok"

# --- Dry-run terminates here ---
if [ "$mode" = "dry" ] || [ "${ORION_OFFLINE:-0}" = "1" ]; then
  echo
  echo "=== DRY-RUN COMPLETE ==="
  echo "Evidence: $EVIDENCE_DIR"
  echo
  echo "Live-minikube mode requires operator action AND merged slices E4-1..E4-9."
  echo "See docs/runbooks/epic4_smoke.md."
  if [ "${ORION_OFFLINE:-0}" = "1" ]; then
    echo "ORION_OFFLINE=1 requested; treating as no live conductor state (exit 10)."
    exit 10
  fi
  exit 0
fi

# --- Step 4 (live mode): refuse until the slices land ---
# Until orion-e41..orion-e49 close, live mode cannot meaningfully run.
# Surface a clear exit-14 with the pre-conditions the operator needs.
echo "[step 4/4] Validating live-minikube pre-conditions..."
missing=""
for var in POSTGRES_DSN ORION_TENANT_ID ORION_NAMESPACE ORION_LEADER_LEASE_SECONDS ORION_FIXTURE_REPO_PATH; do
  if [ -z "${!var:-}" ]; then
    missing="$missing $var"
  fi
done
if [ -n "$missing" ]; then
  echo "FATAL: missing required env vars:$missing" >&2
  exit 14
fi
require_bin kubectl
require_bin minikube
require_bin psql
require_bin jq

# Refuse to drill until the Conductor binary exists. Replace this guard
# with the actual drill invocation when orion-e48 (the keystone) lands.
worker_bin="$REPO_ROOT/bin/orion-worker"
if [ ! -x "$worker_bin" ]; then
  echo "FATAL: $worker_bin not built; orion-e44 has not yet shipped." >&2
  echo "       Live restart-recovery drill cannot run until E4-1..E4-9 close." >&2
  exit 14
fi

echo
echo "=== LIVE-MINIKUBE drill not yet wired ==="
echo "The smoke wrapper is the bookend; orion-e4f closes the loop."
echo "When orion-e48 (Conductor scheduler tick) lands, replace this exit"
echo "with the kill-and-recover invocation."
exit 14
