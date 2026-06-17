# Epic 4 Conductor Fixture

Self-contained Go module that feeds the Epic 4 restart-recovery drill
(`test/acceptance/epic4_restart_drill_test.go`). The Conductor +
Lookout + worker pipeline consumes this fixture end-to-end:

1. Conductor reads `backlog.json` (2 issues).
2. Conductor claims issue-1 and spawns a worker pod that scans the
   service's Go files for the planted gap.
3. The drill kills the leader Conductor mid-run.
4. The standby leader takes over via the PG advisory lock and resumes
   the run without double-spawning, respecting the fencing token.
5. The worker completes; the namespace is torn down.

## Planted gaps

| File          | rvl-cli slug      | Control |
|---------------|-------------------|---------|
| `client.go`   | `missing-timeout` | RC-018  |
| `external.go` | `missing-retry`   | RC-019  |
| `errors.go`   | `swallowed-error` | RC-021  |

Mirrors `fixtures/epic3-detection`: three deterministic, currently
shipping rvl-cli matchers. The backlog only references two of these
because the bookend pins a 2-issue backlog (one consumed pre-kill, one
post-leader-handover); the third gap exists so detection scans against
this fixture return the same 3-gap shape Epic 3's drill expects.

## Why the fixture has 3 gaps but only 2 backlog issues

The restart drill is about orchestration, not detection. It needs:

- One issue the leader claims and spawns a worker for.
- One issue the post-handover leader claims after the first finishes.

The third gap (`errors.go`) is unclaimed by backlog.json so detection
can also be exercised against the fixture without exhausting the
backlog. orion-e4f (the e2e smoke) decides whether the third issue
gets a backlog entry at smoke time.
