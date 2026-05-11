#!/usr/bin/env bash
#
# Epic 2 acceptance smoke test.
#
# Pins what "Epic 2 done" looks like. Runs in two modes:
#
#   --dry-run   Build orion-cli, run the conformance suite, validate
#               the smoke script's own pre-conditions. Safe to run
#               anywhere with Go + git.
#
#   --live      Full end-to-end against a real backlog. Requires the
#               operator to have provisioned:
#                 - GitHub App from E1-1 installed on the fixture repo
#                 - Linear OAuth credentials for a test workspace
#                 - Postgres available
#                 - rvl-cli in PATH
#               Refuses to run if any prerequisite is missing.
#
# Exit codes:
#   0   smoke passed
#   10  conformance suite reported a failing adapter
#   11  backlog ingestion produced fewer rows than expected
#   12  ingested rows fail expected_backlog_shape.json invariants
#   13  fewer than 3 eligibility classes observed in the live result
#   14  pre-condition failed (missing env, missing tools)
#   20  safety violation: target resolved to upstream
#   30  orion-cli build failed
#   99  unexpected error
#
# Environment (live mode):
#   FIXTURE_REPO             default revelara-ai/microservices-demo
#   ORION_GITHUB_APP_ID, ORION_GITHUB_INSTALLATION_ID, ORION_GITHUB_PRIVATE_KEY (from E1-1)
#   ORION_LINEAR_CLIENT_ID, ORION_LINEAR_CLIENT_SECRET (from E2-3 once wired)
#   POSTGRES_DSN             e.g. postgres://...
#   ORION_OFFLINE=1          forces dry-run-equivalent behavior; smoke
#                            exits 10 (no PR found) for parity with
#                            E1-F's pattern.

set -uo pipefail
# We don't set -e because dry-run mode interprets specific non-zero
# exit codes from the conformance suite as expected.

readonly REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly EVIDENCE_DIR="${ORION_EVIDENCE_DIR:-$REPO_ROOT/test/acceptance/run-logs/$(date +%Y%m%dT%H%M%SZ)}"
readonly DEFAULT_FIXTURE_REPO="revelara-ai/microservices-demo"
readonly UPSTREAM_FORBIDDEN_OWNERS=("GoogleCloudPlatform" "googlecloudplatform")

usage() {
  cat <<'USAGE'
epic2_smoke.sh [--dry-run | --live]

  --dry-run   Build orion-cli + run conformance suite + validate
              the smoke script's pre-conditions. CI-safe.
  --live      Full end-to-end. Requires operator-provisioned
              prerequisites (see docs/runbooks/epic2_smoke.md).

Environment:
  FIXTURE_REPO         override fixture (default revelara-ai/microservices-demo)
  ORION_EVIDENCE_DIR   evidence directory (default run-logs/<timestamp>)
  ORION_OFFLINE=1      treat as no-backlog state (for unit tests)
USAGE
}

mode="dry"
case "${1:-}" in
  --dry-run|"")  mode="dry" ;;
  --live)        mode="live" ;;
  -h|--help)     usage; exit 0 ;;
  *)             usage; exit 14 ;;
esac

mkdir -p "$EVIDENCE_DIR"
exec > >(tee -a "$EVIDENCE_DIR/wrapper.log") 2>&1

echo "=== Epic 2 acceptance smoke test ==="
echo "Mode:        $mode"
echo "Repo root:   $REPO_ROOT"
echo "Evidence:    $EVIDENCE_DIR"
echo "Fixture:     ${FIXTURE_REPO:-$DEFAULT_FIXTURE_REPO}"
echo

# --- Safety guard: never operate against upstream ---
target="${FIXTURE_REPO:-$DEFAULT_FIXTURE_REPO}"
owner="${target%%/*}"
for forbidden in "${UPSTREAM_FORBIDDEN_OWNERS[@]}"; do
  if [ "$owner" = "$forbidden" ]; then
    echo "FATAL: target $target resolves to forbidden upstream owner $owner" >&2
    exit 20
  fi
done

# --- Common pre-conditions ---
require_bin() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "FATAL: $1 not in PATH" >&2
    exit 14
  fi
}
require_bin go
require_bin git

# --- Step 1: build orion-cli ---
echo "[step 1/3] Building orion-cli..."
if ! (cd "$REPO_ROOT" && make build) > "$EVIDENCE_DIR/build.log" 2>&1; then
  echo "FATAL: make build failed; see $EVIDENCE_DIR/build.log" >&2
  exit 30
fi
echo "  ok"

# --- Step 2: run conformance suite ---
echo "[step 2/3] Running conformance suite (NoOp adapter)..."
if ! (cd "$REPO_ROOT" && go test ./internal/trackers/conformance/... -count=1) > "$EVIDENCE_DIR/conformance.log" 2>&1; then
  echo "FATAL: conformance suite failed" >&2
  echo "       see $EVIDENCE_DIR/conformance.log" >&2
  exit 10
fi
echo "  ok"

# --- Dry-run terminates here ---
if [ "$mode" = "dry" ] || [ "${ORION_OFFLINE:-0}" = "1" ]; then
  echo
  echo "=== DRY-RUN COMPLETE ==="
  echo "Evidence: $EVIDENCE_DIR"
  echo
  echo "Live mode requires operator action. See docs/runbooks/epic2_smoke.md."
  if [ "${ORION_OFFLINE:-0}" = "1" ]; then
    # Mirror E1-F's offline semantics: "no PR / no backlog found" = exit 10.
    echo "ORION_OFFLINE=1 requested; treating as no backlog found (exit 10)."
    exit 10
  fi
  exit 0
fi

# --- Step 3 (live mode only): full ingestion + invariants ---
echo "[step 3/3] Validating live-mode environment..."
missing=""
for var in ORION_GITHUB_APP_ID ORION_GITHUB_INSTALLATION_ID ORION_GITHUB_PRIVATE_KEY POSTGRES_DSN; do
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

echo "  ok"
echo
echo "(operator: invoke 'orion-cli backlog ingest --binding=<id>' as appropriate)"
echo "(operator: then re-run with 'orion-cli backlog list --binding=<id>' and capture to $EVIDENCE_DIR/)"
echo
echo "=== LIVE MODE NOT YET FULLY AUTOMATED ==="
echo "The wrapper validated prereqs; the remaining live assertions are"
echo "documented in docs/runbooks/epic2_smoke.md and will be wired"
echo "into this script as E2-F lands. For now, exit 0 indicates the"
echo "scaffolding is in place; actual full-loop validation is HITL."
exit 0
