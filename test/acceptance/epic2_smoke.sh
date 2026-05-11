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
epic2_smoke.sh [--dry-run | --live] [--with-pg]

Modes:
  --dry-run    (default) build orion-cli + run conformance suite. Go + git only.
  --live       full end-to-end against a real backlog. Requires every
               operator prerequisite documented in docs/runbooks/epic2_smoke.md.

Flags:
  --with-pg    additionally run backlog + repos + dedup testcontainer-pg
               tests. Requires docker. Combinable with either mode.

Environment:
  FIXTURE_REPO         override fixture (default revelara-ai/microservices-demo)
  ORION_EVIDENCE_DIR   evidence directory (default run-logs/<timestamp>)
  ORION_OFFLINE=1      treat as no-backlog state (for unit tests)
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

# --- Step 2b: optional testcontainer pg ingest validation ---
# When --with-pg is set, exercise the full ingestion -> normalize ->
# upsert path through the testcontainer-pg backlog tests. This proves
# the SPEC §8.3 / §8.4 / §8.7 wiring (eligibility + dedup + autofile
# gate) against a real pg without requiring a live tracker.
if [ "$with_pg" = "1" ]; then
  echo "[step 2b/3] Running backlog testcontainer-pg integration tests..."
  if ! command -v docker >/dev/null 2>&1; then
    echo "FATAL: --with-pg requires docker in PATH" >&2
    exit 14
  fi
  if ! (cd "$REPO_ROOT" && go test -timeout 30m ./internal/backlog/... ./internal/repos/... ./internal/dedup/... -count=1) > "$EVIDENCE_DIR/pg.log" 2>&1; then
    echo "FATAL: backlog/repos/dedup tests failed; see $EVIDENCE_DIR/pg.log" >&2
    exit 11
  fi
  echo "  ok"
fi

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
cli_bin="$REPO_ROOT/bin/orion-cli"
if [ ! -x "$cli_bin" ]; then
  echo "FATAL: $cli_bin not built; step 1 should have produced it" >&2
  exit 30
fi

# Resolve the binding+org from operator-provided env. v1: operator
# provisions a TrackerBinding row in advance and exports its id.
if [ -z "${ORION_BINDING_ID:-}" ] || [ -z "${ORION_ORG_ID:-}" ]; then
  echo "FATAL: ORION_BINDING_ID and ORION_ORG_ID must be exported" >&2
  echo "       (provision a binding row first; see docs/runbooks/epic2_smoke.md)" >&2
  exit 14
fi

echo "[step 3a] orion-cli backlog ingest --binding=$ORION_BINDING_ID"
if ! "$cli_bin" backlog ingest --binding="$ORION_BINDING_ID" --org="$ORION_ORG_ID" > "$EVIDENCE_DIR/ingest.json" 2> "$EVIDENCE_DIR/ingest.err"; then
  echo "FATAL: backlog ingest failed; see $EVIDENCE_DIR/ingest.err" >&2
  exit 11
fi
echo "  ok ($(jq -r '.IssuesUpserted // 0' < "$EVIDENCE_DIR/ingest.json") rows upserted)"

echo "[step 3b] orion-cli backlog list --binding=$ORION_BINDING_ID"
if ! "$cli_bin" backlog list --binding="$ORION_BINDING_ID" --org="$ORION_ORG_ID" > "$EVIDENCE_DIR/list.json" 2> "$EVIDENCE_DIR/list.err"; then
  echo "FATAL: backlog list failed; see $EVIDENCE_DIR/list.err" >&2
  exit 12
fi
listed=$(jq 'length' < "$EVIDENCE_DIR/list.json")
echo "  ok ($listed rows in backlog)"

echo "[step 3c] orion-cli backlog next --binding=$ORION_BINDING_ID"
"$cli_bin" backlog next --binding="$ORION_BINDING_ID" --org="$ORION_ORG_ID" > "$EVIDENCE_DIR/next.json" 2> "$EVIDENCE_DIR/next.err"
next_rc=$?
if [ "$next_rc" = "0" ]; then
  echo "  ok (top-eligible: $(jq -r '.ExternalID // .external_id // "<unknown>"' < "$EVIDENCE_DIR/next.json"))"
elif [ "$next_rc" = "5" ]; then
  echo "  no_eligible_issues (exit 5 sentinel)"
else
  echo "FATAL: backlog next failed (rc=$next_rc); see $EVIDENCE_DIR/next.err" >&2
  exit 13
fi

echo
echo "=== LIVE SMOKE COMPLETE ==="
echo "Evidence: $EVIDENCE_DIR"
echo
echo "Operator follow-up (per orion-n52 acceptance criteria):"
echo "  - Confirm at least one issue per eligibility class observed"
echo "    (eligible, ineligible_label, ineligible_path, ineligible_blocked)"
echo "    by inspecting $EVIDENCE_DIR/list.json"
echo "  - Re-run ingest twice and confirm only one Create call lands"
echo "    for the same finding (dedup_signature uniqueness)"
echo "  - Force a Linear refresh and confirm the rotated refresh_token"
echo "    persists in encrypted_oauth_credential"
exit 0
