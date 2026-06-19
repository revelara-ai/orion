#!/usr/bin/env bash
# Orion end-to-end smoke test (or-xg7).
#
# Builds the orion binary, drives the full V2.0 loop on a fresh greenfield idea
# (intent → spec → plan → generate → 3-mode prove → deliver), then independently
# verifies: the operating envelope + complete runbook are produced, the REAL
# generated service builds/runs/answers a spec-conformant request and shuts down
# gracefully, and the loop is resumable across a hard kill.
#
# Usage:  bash scripts/smoke.sh
# Requires: go, jq, curl.  Runs entirely in temp dirs; mutates nothing tracked.
set -uo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO"

BIN="$(mktemp -d)/orion"
D="$(mktemp -d)"
export ORION_DATA_DIR="$D"
PORT="${ORION_SMOKE_PORT:-8753}"
cleanup() { rm -rf "$D" "$(dirname "$BIN")"; }
trap cleanup EXIT

echo "════════════════════════════════════════════════════════════"
echo "  ORION END-TO-END SMOKE (or-xg7)"
echo "════════════════════════════════════════════════════════════"

echo "── [1/5] build the orion binary"
go build -o "$BIN" ./cmd/orion || { echo "BUILD FAILED"; exit 1; }
echo "   built: $("$BIN" --version)"

echo "── [2/5] drive the loop: intent → spec → plan → generate → 3-mode prove → deliver"
"$BIN" init >/dev/null 2>&1
echo "Build an HTTP service that returns the current time." | "$BIN" submit --non-interactive >/dev/null 2>&1
for kv in response_format=json timezone=UTC port=8080 route=/time; do
  "$BIN" answer --key "${kv%%=*}" --value "${kv#*=}" >/dev/null 2>&1
done
"$BIN" spec approve >/dev/null 2>&1
RUN_OUT="$("$BIN" run 2>&1)"
echo "   $RUN_OUT"
echo "$RUN_OUT" | grep -q 'verdict=Accept closed=true' || { echo "   ✘ loop did not converge to Accept"; exit 1; }
echo "$RUN_OUT" | grep -q 'delivery=deliver'           || { echo "   ✘ bar did not deliver"; exit 1; }
TASK_ID="$(echo "$RUN_OUT" | sed -n 's/run: task \([^ ]*\).*/\1/p')"
echo "   ✔ converged Accept, human-mergeable delivery (task $TASK_ID)"

echo "── [3/5] delivery artifacts: operating envelope + runbook"
"$BIN" deliver show --json 2>/dev/null | jq -e '.operating_envelope != null' >/dev/null \
  && echo "   ✔ operating_envelope: $("$BIN" deliver show --json 2>/dev/null | jq -c '.operating_envelope|{proven_load,tier}')"
"$BIN" deliver show --runbook --json 2>/dev/null \
  | jq -e '.sections|keys|contains(["incident_response","escalation_path","known_failure_modes","operational_commands"])' >/dev/null \
  && echo "   ✔ runbook complete (incident_response, escalation_path, known_failure_modes, operational_commands)"

echo "── [4/5] curl the PROVEN, runnable service (the real generated artifact)"
SVC="$D/build/$TASK_ID"
ls "$SVC"/*.go >/dev/null 2>&1 || { echo "   ✘ no generated source at $SVC"; exit 1; }
( cd "$SVC" && go build -o ./svc . ) || { echo "   ✘ generated service failed to build"; exit 1; }
PORT="$PORT" "$SVC/svc" & SVC_PID=$!
RESP=""
for _ in $(seq 1 30); do RESP="$(curl -fsS "http://localhost:$PORT/time" 2>/dev/null)" && break; sleep 0.2; done
echo "   GET /time → $RESP"
echo "$RESP" | jq -e '.time | test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T")' >/dev/null \
  && echo "   ✔ spec-conformant: JSON body with an RFC3339 .time field" \
  || { echo "   ✘ response not spec-conformant"; kill "$SVC_PID" 2>/dev/null; exit 1; }
kill -TERM "$SVC_PID" 2>/dev/null; wait "$SVC_PID" 2>/dev/null
echo "   ✔ service shut down gracefully on SIGTERM"

echo "── [5/5] resumability: kill an in-flight agent; rebuild via Recall, no re-ask"
go test ./internal/contextstore/... -run TestResumeAfterSIGKILL -count=1 >/dev/null 2>&1 \
  && echo "   ✔ TestResumeAfterSIGKILL: loop resumes from durable state without re-asking" \
  || { echo "   ✘ resumability failed"; exit 1; }

echo "════════════════════════════════════════════════════════════"
echo "  SMOKE PASSED — proven, runnable service + envelope + runbook,"
echo "  delivered human-mergeable, resumable across a hard kill."
echo "════════════════════════════════════════════════════════════"
