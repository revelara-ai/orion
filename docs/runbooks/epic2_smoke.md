# Epic 2 Smoke Test Runbook

This runbook documents how to run the Epic 2 acceptance smoke test
against a real backlog, what each failure mode means, and what
operator prerequisites must be in place before the live (non-dry-run)
mode will work.

## Overview

Epic 2 ships the tracker adapter contract, Postgres-backed
backlog, and ingestion driver. The smoke test
(`test/acceptance/epic2_smoke.sh`) asserts that:

1. Both adapters (GitHub Issues from E2-2, Linear from E2-4) pass
   the conformance suite (E2-A).
2. Backlog ingestion against `revelara-ai/microservices-demo`
   produces NormalizedIssue rows matching
   `expected_backlog_shape.json`.
3. Eligibility evaluation tags at least three classes (eligible +
   two ineligible variants).
4. Semantic dedup prevents double-filing.
5. Pre-flight LLM cache hits on subsequent ingestion ticks.

## Modes

### Dry-run (default)

```bash
./test/acceptance/epic2_smoke.sh --dry-run
```

Builds orion-cli, runs the conformance suite against the in-package
NoOp adapter, validates the script's pre-conditions. Requires only
Go + git. Use this as a CI gate.

### Live

```bash
./test/acceptance/epic2_smoke.sh --live
```

Requires every prerequisite below. Refuses to run if any are missing.

## Operator Prerequisites (Live Mode)

### 1. GitHub App (from E1-1)

The same GitHub App provisioned for Epic 1 covers Epic 2's GitHub
Issues operations. Permissions required (in addition to the E1
set):

- **Repository contents**: Read (E1 has this)
- **Issues**: Read & Write (NEW for Epic 2)
- **Pull requests**: Read & Write (E1 has this)
- **Metadata**: Read

Install on `revelara-ai/microservices-demo`. Export:

```bash
export ORION_GITHUB_APP_ID=<numeric app id>
export ORION_GITHUB_INSTALLATION_ID=<numeric install id>
export ORION_GITHUB_PRIVATE_KEY="$(cat orion-app-private-key.pem)"
```

**Safety guard**: the wrapper refuses to operate against
`GoogleCloudPlatform/microservices-demo` (the upstream).

### 2. Linear OAuth (once E2-3 ships)

Provision a Linear OAuth app at https://linear.app/settings/api
with scopes: `read`, `write`, `issues:create`, `comments:create`.
Set up a test workspace populated with synthetic issues.

```bash
export ORION_LINEAR_CLIENT_ID=<client id>
export ORION_LINEAR_CLIENT_SECRET=<secret>
# After running the OAuth flow once:
export ORION_LINEAR_ACCESS_TOKEN=<token>
export ORION_LINEAR_REFRESH_TOKEN=<refresh>
export ORION_LINEAR_EXPIRES_AT=<RFC3339>
```

The Linear adapter's token rotation (E2-3 / polaris fork) means
ORION_LINEAR_ACCESS_TOKEN may rotate during the smoke test. The
wrapper logs the rotation event to evidence.

### 3. Postgres

Orion's backlog persists to Postgres (E2-1 ships the schema).
Local Docker Compose works:

```bash
docker-compose up -d postgres
export POSTGRES_DSN="postgres://orion:orion@localhost:5432/orion?sslmode=disable"
# Apply migrations
make migrate
```

Or a managed Postgres works too — anything reachable that supports
RLS policies.

### 4. rvl-cli

The dedup-signature path uses rvl-cli (E2-7) to compute canonical
AST paths. Install rvl per its README; `rvl --version` must succeed
before running this smoke test.

## Running the Live Smoke Test

```bash
# 1. Provision prereqs as above

# 2. Configure a binding (no API yet; seed via config in v1):
cat > /tmp/orion-test-config.yaml <<EOF
postgres:
  dsn: $POSTGRES_DSN
trackers:
  - kind: github_issues
    repo: revelara-ai/microservices-demo
    auto_file: false
  - kind: linear
    workspace: orion-test
    auto_file: false
EOF

# 3. Run the wrapper.
./test/acceptance/epic2_smoke.sh --live

# 4. On success, inspect the backlog.
./bin/orion-cli backlog list --binding=<id>
./bin/orion-cli backlog next --binding=<id>
```

## Exit-Code Map

| Code | Meaning | What to do |
|---|---|---|
| `0`  | Smoke passed | Capture evidence; close E2-F. |
| `10` | Conformance suite failed | Inspect `$EVIDENCE_DIR/conformance.log`. Likely an adapter regression. |
| `11` | Ingestion produced fewer rows than expected | Check tracker connectivity; verify `gh issue list` returns the expected set. |
| `12` | Rows fail expected_backlog_shape.json | Inspect schema columns and adapter normalization code. |
| `13` | Fewer than 3 eligibility classes observed | Either microservices-demo's backlog is too homogeneous, OR the eligibility evaluator has a bug (E2-8). |
| `14` | Pre-condition failed | Check missing env vars in `$EVIDENCE_DIR/wrapper.log`. |
| `20` | Safety violation: target resolved to upstream | Verify FIXTURE_REPO points at the Revelara fork, not GoogleCloudPlatform. |
| `30` | orion-cli build failed | Check `$EVIDENCE_DIR/build.log`. |
| `99` | Unexpected error | Inspect `$EVIDENCE_DIR/wrapper.log` and surface. |

## Debugging First-Run Failures

The first live run is expected to surface integration gaps:

- **Conformance pass but live ingest empty.** Check the GitHub App
  has Issues read scope (not just code+PR). Check Linear
  workspace_id is correct.
- **NormalizedIssue rows have empty `dedup_signature`.** rvl-cli
  isn't being invoked, or it returns empty for the affected
  call-site. Inspect `$EVIDENCE_DIR/ingest.log` for the rvl call.
- **Token rotation panic.** The polaris-fork's
  TokenRefresher callback didn't fire. See polaris bd memory
  `oauth-token-refresh-callback` and verify E2-3's wiring.
- **Pre-flight LLM cache miss every run.** body_signature
  computation is non-deterministic; check that the same issue body
  hashes consistently across runs.

## Closing E2-F

Per E2-F's own acceptance criterion ("If the smoke test fails, this
issue is NOT closed; the failing capability is filed as a follow-up
and this issue stays open"):

1. Live smoke must exit 0 against a freshly-reset fixture.
2. NormalizedIssue rows must match the expected shape.
3. Eligibility classes must show real diversity.
4. Cleanup confirms no orphan rows, no orphan OAuth state, no
   orphan Linear-API rate-limit holds.

The dry-run is what we ship in this issue (E2-A). Live mode lands
incrementally as each downstream slice closes; E2-F is the final
sign-off.
