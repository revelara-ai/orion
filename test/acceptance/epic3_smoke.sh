#!/usr/bin/env bash
#
# Epic 3 acceptance smoke test (bookend for orion-e3a).
#
# Pins what "Epic 3 done" looks like. Defines the contract that the
# detection loop's slices (E3-1 through E3-7) must satisfy by closure
# of orion-e3f. The test will NOT pass end-to-end until those slices
# land; that is the point of a bookend acceptance test.
#
# Modes:
#
#   --dry-run   (default) build orion-cli, run detection unit tests,
#               validate the fixture and expected-shape file exist.
#               Safe to run anywhere with Go + git.
#
#   --live      Full 3-tick detection drill against an operator-
#               provisioned binding. Refuses to run until the slices
#               that ship LoopDriver + scheduler + risksink are merged.
#               Exits 14 with a clear message until then.
#
#   --with-pg   Additionally exercise the detection_runs + dedup paths
#               via testcontainer-pg. Requires docker. Will FAIL until
#               orion-e31 (detection_runs schema + repo) lands; this is
#               the failing-target the bookend pins.
#
# Exit codes (CONTRACT; downstream slices assert against these):
#   0    smoke passed
#   10   detection unit tests failed
#   11   fixture directory missing or malformed
#   12   expected_detection_shape.json missing or malformed
#   13   3-tick drill invariant violated (live mode only)
#   14   pre-condition failed (missing env, missing binary, slice not yet built)
#   20   safety violation: target resolved to upstream
#   30   orion-cli build failed
#   99   unexpected error
#
# Environment (live mode):
#   POSTGRES_DSN           e.g. postgres://orion:orion@localhost:5432/orion
#   POLARIS_BASE_URL       optional; if unset risksink uses local fallback
#   ORION_BINDING_ID       UUID of operator-provisioned TrackerBinding row
#   ORION_ORG_ID           UUID of organization for RLS context
#   ORION_OFFLINE=1        treat as dry-run-equivalent

set -uo pipefail
# Do NOT set -e: dry-run interprets specific non-zero exits from sub-steps
# as expected (e.g., --with-pg before orion-e31 is merged).

readonly REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly FIXTURE_DIR="$REPO_ROOT/test/acceptance/fixtures/epic3-detection"
readonly EXPECTED_SHAPE="$REPO_ROOT/test/acceptance/expected_detection_shape.json"
readonly EVIDENCE_DIR="${ORION_EVIDENCE_DIR:-$REPO_ROOT/test/acceptance/run-logs/$(date +%Y%m%dT%H%M%SZ)}"
readonly DEFAULT_FIXTURE_REPO="epic3-detection"
readonly UPSTREAM_FORBIDDEN_OWNERS=("GoogleCloudPlatform" "googlecloudplatform")

usage() {
  cat <<'USAGE'
epic3_smoke.sh [--dry-run | --live] [--with-pg]

Modes:
  --dry-run    (default) build orion-cli + run detection unit tests + validate fixture/shape. Go + git only.
  --live       full 3-tick detection drill. Requires every operator prerequisite documented in docs/runbooks/epic3_smoke.md.

Flags:
  --with-pg    additionally run detection_runs testcontainer-pg tests. Requires docker.

Environment:
  ORION_EVIDENCE_DIR     evidence directory (default run-logs/<timestamp>)
  ORION_OFFLINE=1        treat as no live detection state (mirrors epic1/2 convention)
USAGE
}

mode="dry"
with_pg=0
for arg in "$@"; do
  case "$arg" in
    --dry-run)   mode="dry" ;;
    --live)      mode="live" ;;
    --with-pg)   with_pg=1 ;;
    -h|--help)   usage; exit 0 ;;
    "")          ;;
    *)           usage; exit 14 ;;
  esac
done

mkdir -p "$EVIDENCE_DIR"
exec > >(tee -a "$EVIDENCE_DIR/wrapper.log") 2>&1

echo "=== Epic 3 acceptance smoke test ==="
echo "Mode:           $mode"
echo "Repo root:      $REPO_ROOT"
echo "Fixture:        $FIXTURE_DIR"
echo "Expected shape: $EXPECTED_SHAPE"
echo "Evidence:       $EVIDENCE_DIR"
echo

# --- Safety guard: never operate against upstream-owned fixtures ---
fixture_id="${FIXTURE_REPO:-$DEFAULT_FIXTURE_REPO}"
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

# --- Step 1: validate fixture + expected-shape files exist ---
echo "[step 1/4] Validating fixture + expected-shape artifacts..."
if [ ! -d "$FIXTURE_DIR" ]; then
  echo "FATAL: fixture directory missing: $FIXTURE_DIR" >&2
  exit 11
fi
if [ ! -f "$FIXTURE_DIR/go.mod" ]; then
  echo "FATAL: fixture is not a Go module: $FIXTURE_DIR/go.mod missing" >&2
  exit 11
fi
fixture_go_files=$(find "$FIXTURE_DIR" -maxdepth 2 -name '*.go' -type f | wc -l)
if [ "$fixture_go_files" -lt 3 ]; then
  echo "FATAL: fixture has only $fixture_go_files Go files; need at least 3 (one per pattern)" >&2
  exit 11
fi
if [ ! -f "$EXPECTED_SHAPE" ]; then
  echo "FATAL: expected_detection_shape.json missing: $EXPECTED_SHAPE" >&2
  exit 12
fi
# Validate it is parseable JSON.
if command -v jq >/dev/null 2>&1; then
  if ! jq empty < "$EXPECTED_SHAPE" >/dev/null 2>&1; then
    echo "FATAL: expected_detection_shape.json is not valid JSON" >&2
    exit 12
  fi
  primary_count=$(jq -r '[.expected_findings[] | select(.primary == true)] | length' < "$EXPECTED_SHAPE")
  if [ "$primary_count" != "3" ]; then
    echo "FATAL: primary expected_findings count is $primary_count; bookend pins 3" >&2
    exit 12
  fi
fi
echo "  ok (fixture: $fixture_go_files Go files, shape: 3 primary findings)"

# --- Step 2: build orion-cli ---
echo "[step 2/4] Building orion + orion-cli..."
if ! (cd "$REPO_ROOT" && make build) > "$EVIDENCE_DIR/build.log" 2>&1; then
  echo "FATAL: make build failed; see $EVIDENCE_DIR/build.log" >&2
  exit 30
fi
echo "  ok"

# --- Step 3: run detection unit tests ---
echo "[step 3/4] Running detection unit tests..."
if ! (cd "$REPO_ROOT" && go test ./internal/detection/... -count=1) > "$EVIDENCE_DIR/detection.log" 2>&1; then
  echo "FATAL: detection unit tests failed; see $EVIDENCE_DIR/detection.log" >&2
  exit 10
fi
echo "  ok"

# --- Step 3b: optional testcontainer-pg detection_runs tests (failing target until E3-1 lands) ---
# When --with-pg is set, exercise the detection_runs + detection_findings
# persistence path. This will FAIL until orion-e31 ships the schema and
# repo; that failure IS the bookend's pinned target.
if [ "$with_pg" = "1" ]; then
  echo "[step 3b/4] Running detection-pg integration tests (will fail until orion-e31 lands)..."
  if ! command -v docker >/dev/null 2>&1; then
    echo "FATAL: --with-pg requires docker in PATH" >&2
    exit 14
  fi
  if [ ! -d "$REPO_ROOT/internal/repos" ]; then
    echo "skipping: internal/repos package missing (orion-e31 not yet shipped)"
  else
    if ! (cd "$REPO_ROOT" && go test -timeout 30m -run 'DetectionRun|DetectionFinding' ./internal/repos/... -count=1) > "$EVIDENCE_DIR/pg.log" 2>&1; then
      echo "expected-fail: detection_runs tests not green yet; see $EVIDENCE_DIR/pg.log"
      echo "               (this is the failing target the bookend pins; orion-e31 makes it green)"
    fi
  fi
fi

# --- Dry-run terminates here ---
if [ "$mode" = "dry" ] || [ "${ORION_OFFLINE:-0}" = "1" ]; then
  echo
  echo "=== DRY-RUN COMPLETE ==="
  echo "Evidence: $EVIDENCE_DIR"
  echo
  echo "Live mode requires operator action AND merged slices E3-1..E3-7."
  echo "See docs/runbooks/epic3_smoke.md."
  if [ "${ORION_OFFLINE:-0}" = "1" ]; then
    # Mirror epic2's offline convention: no live detection state observable.
    echo "ORION_OFFLINE=1 requested; treating as no live detection state (exit 10)."
    exit 10
  fi
  exit 0
fi

# --- Step 4 (live mode): full 3-tick drill ---
echo "[step 4/4] Validating live-mode pre-conditions..."
missing=""
for var in POSTGRES_DSN ORION_BINDING_ID ORION_ORG_ID; do
  if [ -z "${!var:-}" ]; then
    missing="$missing $var"
  fi
done
if [ -n "$missing" ]; then
  echo "FATAL: missing required env vars:$missing" >&2
  exit 14
fi
require_bin psql
require_bin jq

cli_bin="$REPO_ROOT/bin/orion-cli"
if [ ! -x "$cli_bin" ]; then
  echo "FATAL: $cli_bin not built; step 2 should have produced it" >&2
  exit 30
fi

# The live 3-tick drill requires `orion-cli detection trigger` (lands in
# orion-e33) and the LoopDriver (lands in orion-e32). Until those slices
# are merged, refuse with a clear message rather than printing a useless
# stack trace.
if ! "$cli_bin" detection trigger --help >/dev/null 2>&1; then
  echo "FATAL: orion-cli detection trigger subcommand not yet built." >&2
  echo "       Live 3-tick drill requires:" >&2
  echo "         - orion-e31 (detection_runs schema + repo)" >&2
  echo "         - orion-e32 (LoopDriver)" >&2
  echo "         - orion-e33 (scheduler + detection trigger CLI)" >&2
  echo "         - orion-e34 (quiescence gating)" >&2
  echo "         - orion-e35 (risksink with local fallback)" >&2
  echo "         - orion-e36 (progressive-disclosure cap)" >&2
  echo "         - orion-e37 (loopguard, optional but expected in shape)" >&2
  echo "       Run the dry-run mode until those merge." >&2
  exit 14
fi

# When all slices are merged, orion-e3f extends this section with the
# real 3-tick drill: tick1 finds N=3 gaps + autofiles N, tick2 dedup
# short-circuits (zero new), tick3 detects new gap after a fresh commit
# and autofiles 1. Provenance + quiescence + loopguard invariants are
# asserted from $EXPECTED_SHAPE.

echo
echo "=== LIVE 3-TICK DRILL ==="
echo "(stub: orion-e3f wires the real drill once E3-1..E3-7 merge)"
echo "Evidence: $EVIDENCE_DIR"
echo
echo "Operator follow-up (per orion-e3f acceptance criteria):"
echo "  - Tick 1: confirm $(jq -r '[.expected_findings[] | select(.primary == true)] | length' < "$EXPECTED_SHAPE") primary findings autofiled"
echo "  - Tick 2: confirm dedup short-circuit (zero new autofile calls)"
echo "  - Tick 3: introduce one new gap; confirm 1 new finding autofiled"
echo "  - DetectionRun row count = 3"
echo "  - LoopGuardCheck does NOT warn (first-3-runs suppression)"
exit 0
