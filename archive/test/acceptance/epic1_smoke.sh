#!/usr/bin/env bash
#
# Epic 1 acceptance smoke test.
#
# Pins what "Orion v1 tracer-bullet success" looks like against the test
# target (revelara-ai/microservices-demo). Runs in two modes:
#
#   --dry-run         Validate own pre-conditions; exit 0 if OK.
#   (no flag, default) Probe live state; exit per documented codes.
#
# Exit codes (CONTRACT; do not change without updating
# expected_pr_shape.json, the Go contract test, and downstream Epic 1
# work that asserts against these):
#
#   0   PR exists and matches the documented shape; bullet has shipped.
#   10  No Orion PR found (Orion has not run, or run produced no patches).
#   11  PR exists but commit count below floor.
#   12  PR exists but commits do not modify any expected source paths.
#   13  PR exists but body is missing one or more required report fields.
#   14  Pre-condition failed (gh auth missing, jq missing, env unset).
#   20  SAFETY VIOLATION: target resolved to upstream (GoogleCloudPlatform);
#       script refuses to operate against the read-only upstream.
#   99  Unexpected error (caller should investigate).
#
# Environment:
#   ORION_OFFLINE=1  Skip the live `gh pr list` call; behave as if no PR
#                    found (used by Go contract tests).
#   ORION_APP_NAME   GitHub App slug to filter PRs by author.
#                    Defaults to "orion[bot]".
#   FIXTURE_REPO     Override the target repo. Defaults to
#                    "revelara-ai/microservices-demo".
#                    SAFETY: any value matching GoogleCloudPlatform/* or
#                    starting with "upstream:" is rejected with exit 20.

set -uo pipefail

readonly DEFAULT_FIXTURE_REPO="revelara-ai/microservices-demo"
readonly DEFAULT_APP_NAME="orion[bot]"
readonly UPSTREAM_FORBIDDEN_OWNERS=("GoogleCloudPlatform" "googlecloudplatform")

readonly EXPECTED_SHAPE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/expected_pr_shape.json"

usage() {
  cat <<'USAGE'
epic1_smoke.sh [--dry-run]

Validates that Orion has shipped the Epic 1 tracer-bullet against the
test target repo. See script header for exit code contract.
USAGE
}

dry_run=0
case "${1:-}" in
  --dry-run) dry_run=1 ;;
  ""        ) ;;
  -h|--help ) usage; exit 0 ;;
  *         ) usage; exit 14 ;;
esac

# --- Safety guard: never operate against upstream / GoogleCloudPlatform ---
fixture_repo="${FIXTURE_REPO:-$DEFAULT_FIXTURE_REPO}"
fixture_owner="${fixture_repo%%/*}"
for forbidden in "${UPSTREAM_FORBIDDEN_OWNERS[@]}"; do
  if [[ "$fixture_owner" == "$forbidden" ]]; then
    echo "FATAL [exit 20]: refuses to operate against upstream owner '$fixture_owner'." >&2
    echo "  microservices-demo upstream is read-only; only the revelara-ai fork is a valid Orion target." >&2
    exit 20
  fi
done
if [[ "$fixture_repo" == upstream:* ]]; then
  echo "FATAL [exit 20]: FIXTURE_REPO 'upstream:*' aliases are not allowed." >&2
  exit 20
fi

# --- Pre-conditions ---
require() {
  local name="$1" purpose="$2"
  if ! command -v "$name" >/dev/null 2>&1; then
    echo "FAIL [exit 14]: missing required command '$name' ($purpose)" >&2
    exit 14
  fi
}

require gh "GitHub CLI; used to inspect PRs against $fixture_repo"
require jq "JSON tooling; used to parse PR list and PR body"

if [[ ! -f "$EXPECTED_SHAPE" ]]; then
  echo "FAIL [exit 14]: expected_pr_shape.json missing at $EXPECTED_SHAPE" >&2
  exit 14
fi

orion_app_name="${ORION_APP_NAME:-$DEFAULT_APP_NAME}"

if [[ "$dry_run" == "1" ]]; then
  echo "OK [dry-run]: pre-conditions satisfied"
  echo "  fixture_repo  = $fixture_repo"
  echo "  app_name      = $orion_app_name"
  echo "  shape_file    = $EXPECTED_SHAPE"
  echo "  upstream_safe = yes (owner '$fixture_owner' not in forbidden list)"
  exit 0
fi

# --- Live probe ---
if [[ "${ORION_OFFLINE:-0}" == "1" ]]; then
  echo "FAIL [exit 10]: ORION_OFFLINE=1 requested; treating as no PR found." >&2
  echo "  This mode exists for unit tests of the script itself." >&2
  exit 10
fi

if ! gh auth status >/dev/null 2>&1; then
  echo "FAIL [exit 14]: gh CLI not authenticated; run 'gh auth login'" >&2
  exit 14
fi

# Find the most recent open PR authored by the Orion App.
pr_list_json="$(gh pr list \
  --repo "$fixture_repo" \
  --state open \
  --author "$orion_app_name" \
  --json number,title,author,baseRefName,headRefName,body \
  --limit 5 2>/dev/null || true)"

if [[ -z "$pr_list_json" || "$pr_list_json" == "[]" ]]; then
  echo "FAIL [exit 10]: no open PR by '$orion_app_name' against $fixture_repo." >&2
  echo "  Orion has not run, or the run produced no patches that passed verification." >&2
  exit 10
fi

pr_number="$(echo "$pr_list_json" | jq -r '.[0].number')"
pr_body="$(echo "$pr_list_json" | jq -r '.[0].body')"

# Required report fields per SPEC §12.7, §16.1
required_fields="$(jq -r '.pr.body.must_contain[]' "$EXPECTED_SHAPE")"
missing_fields=()
while IFS= read -r field; do
  [[ -z "$field" ]] && continue
  if ! grep -qiF "$field" <<<"$pr_body"; then
    missing_fields+=("$field")
  fi
done <<<"$required_fields"

if (( ${#missing_fields[@]} > 0 )); then
  echo "FAIL [exit 13]: PR #$pr_number body missing required report fields:" >&2
  printf '  - %s\n' "${missing_fields[@]}" >&2
  exit 13
fi

# Commit count + path coverage
commits_json="$(gh pr view "$pr_number" --repo "$fixture_repo" --json commits 2>/dev/null || true)"
commit_count="$(echo "$commits_json" | jq -r '.commits | length')"
min_commits="$(jq -r '.pr.commits.min' "$EXPECTED_SHAPE")"

if (( commit_count < min_commits )); then
  echo "FAIL [exit 11]: PR #$pr_number has $commit_count commit(s); shape requires >= $min_commits" >&2
  exit 11
fi

# At least one commit must touch a file under one of the documented prefixes.
expected_path_prefixes="$(jq -r '.pr.must_modify_at_least_one_path_under[]' "$EXPECTED_SHAPE")"
files_touched="$(gh pr view "$pr_number" --repo "$fixture_repo" --json files 2>/dev/null | jq -r '.files[].path')"

matched=0
while IFS= read -r prefix; do
  [[ -z "$prefix" ]] && continue
  if grep -q "^$prefix" <<<"$files_touched"; then
    matched=1
    break
  fi
done <<<"$expected_path_prefixes"

if (( matched == 0 )); then
  echo "FAIL [exit 12]: PR #$pr_number does not modify any file under expected prefixes:" >&2
  while IFS= read -r prefix; do
    [[ -z "$prefix" ]] || echo "  - $prefix" >&2
  done <<<"$expected_path_prefixes"
  echo "  Files touched:" >&2
  while IFS= read -r f; do
    [[ -z "$f" ]] || echo "  + $f" >&2
  done <<<"$files_touched"
  exit 12
fi

echo "OK [exit 0]: PR #$pr_number matches Epic 1 acceptance shape:"
echo "  commits = $commit_count (>= $min_commits)"
echo "  body fields satisfied"
echo "  modified ≥ 1 path under expected prefixes"
exit 0
