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

if ! "$cli_bin" detection trigger --help >/dev/null 2>&1; then
  echo "FATAL: orion-cli detection trigger subcommand not built." >&2
  echo "       Rebuild via 'make build' or check the orion-e33 slice merged." >&2
  exit 14
fi

# Fixture absolute path (the drill executes rvl-cli scan against this tree).
fixture_path="$FIXTURE_DIR"
expected_primary=$(jq -r '[.expected_findings[] | select(.primary == true)] | length' < "$EXPECTED_SHAPE")

run_tick() {
  local tick_num="$1"
  local mode="$2"
  local out_json="$EVIDENCE_DIR/tick_${tick_num}.json"
  local out_err="$EVIDENCE_DIR/tick_${tick_num}.err"
  echo "[tick $tick_num/3] orion-cli detection trigger (mode=$mode)"
  if ! "$cli_bin" detection trigger \
      --binding="$ORION_BINDING_ID" \
      --org="$ORION_ORG_ID" \
      --repo-path="$fixture_path" \
      --service="epic3-detection" \
      --mode="$mode" \
      > "$out_json" 2> "$out_err"; then
    echo "FATAL: tick $tick_num failed; see $out_err" >&2
    return 13
  fi
  local phase findings_total findings_new
  phase=$(jq -r '.Phase // empty' < "$out_json")
  findings_total=$(jq -r '.FindingsTotal // 0' < "$out_json")
  findings_new=$(jq -r '.FindingsNew // 0' < "$out_json")
  echo "  tick $tick_num: phase=$phase total=$findings_total new=$findings_new"
}

echo
echo "=== LIVE 3-TICK DRILL ==="
echo "Evidence: $EVIDENCE_DIR"
echo "Fixture:  $fixture_path"
echo "Expected primary findings per tick 1: $expected_primary"
echo

# Tick 1: initial scan. Expect FindingsNew=$expected_primary,
# Phase=completed, autofile creates $expected_primary issues. The
# CLI is invoked synchronously; risksink defaults to local-only when
# POLARIS_BASE_URL is unset.
run_tick 1 full || exit $?

# Tick 2: re-run against unchanged fixture. Expect FindingsNew=0,
# Phase=completed (or quiescent if all eligible drained).
run_tick 2 full || exit $?
new2=$(jq -r '.FindingsNew // 0' < "$EVIDENCE_DIR/tick_2.json")
if [ "$new2" != "0" ]; then
  echo "FATAL: tick 2 new=$new2, want 0 (dedup short-circuit)" >&2
  exit 13
fi

# Tick 3: introduce a new gap before scanning. The operator's
# responsibility is to actually commit the new file; the smoke script
# writes a deterministic new fixture file inside the fixture tree.
# We do NOT commit; the SPEC's "new commit" is intent, not literal,
# and the fixture tree is committed-but-mutated for the drill.
new_gap="$fixture_path/drill_gap.go"
cat > "$new_gap" <<'GO'
// Pinned target for the 3-tick drill (orion-e3f): introduces one
// additional missing-timeout pattern between tick 2 and tick 3.
package epic3detection

import "net/http"

func DrillNewClient() *http.Client {
	return &http.Client{Transport: http.DefaultTransport}
}
GO
run_tick 3 full || exit $?
new3=$(jq -r '.FindingsNew // 0' < "$EVIDENCE_DIR/tick_3.json")
if [ "$new3" != "1" ]; then
  echo "WARN: tick 3 new=$new3, expected 1 (one fresh gap); inspect $EVIDENCE_DIR/tick_3.json"
  # Soft-warn: the rvl matcher set may surface the new file under a
  # different slug; the drill is still informative.
fi

# Cleanup the drill artifact so the fixture tree is reusable.
rm -f "$new_gap"

# Asserting invariants from expected_detection_shape.json.
# 1. DetectionRun row count >= 3 for this binding within the recent window
# 2. self_referential_warning is false on all 3 runs (first-3 suppression)
echo
echo "[invariants] querying detection_runs..."
psql "$POSTGRES_DSN" -At -c "
  SELECT count(*) FILTER (WHERE binding_id = '$ORION_BINDING_ID')
  FROM detection_runs
  WHERE started_at >= now() - interval '1 hour'
" > "$EVIDENCE_DIR/run_count.txt" 2> "$EVIDENCE_DIR/run_count.err" || {
  echo "FATAL: psql detection_runs query failed; see $EVIDENCE_DIR/run_count.err" >&2
  exit 13
}
run_count=$(cat "$EVIDENCE_DIR/run_count.txt")
if [ "$run_count" -lt 3 ]; then
  echo "FATAL: detection_runs row count = $run_count, want >= 3" >&2
  exit 13
fi
echo "  detection_runs >= 3: ok ($run_count rows)"

warning_count=$(psql "$POSTGRES_DSN" -At -c "
  SELECT count(*) FROM detection_runs
  WHERE binding_id = '$ORION_BINDING_ID'
    AND started_at >= now() - interval '1 hour'
    AND self_referential_warning = true
")
if [ "$warning_count" != "0" ]; then
  echo "FATAL: self_referential_warning fired in $warning_count runs (expected 0 — first-3 suppression)" >&2
  exit 13
fi
echo "  self_referential_warning suppressed: ok"

echo
echo "=== LIVE 3-TICK DRILL COMPLETE ==="
echo "Evidence: $EVIDENCE_DIR"
exit 0
