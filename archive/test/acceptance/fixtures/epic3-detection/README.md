# Epic 3 Detection Fixture

A deliberately-broken Go module used as the fixture for the Epic 3
detection-loop acceptance smoke test (`test/acceptance/epic3_smoke.sh`).

Each Go file in this directory embeds exactly one reliability gap that
an rvl-cli curated matcher should detect. The expected behavior is
pinned by `../../expected_detection_shape.json`.

| File          | Pattern (rvl-cli slug) | Control | Why it triggers |
|---------------|------------------------|---------|-----------------|
| `client.go`   | `missing-timeout`      | RC-018  | `http.Client{...}` constructed without a `Timeout:` field. |
| `external.go` | `missing-retry`        | RC-019  | `http.Get(...)` with no retry/backoff library reference within 8 lines. |
| `errors.go`   | `swallowed-error`      | RC-021  | `if err != nil { return nil }` — error value dropped. |

## Note on the third pattern

The parent epic (orion-3q8) mentioned "idempotency" as the third gap.
rvl-cli does not currently ship a curated idempotency matcher, so the
fixture substitutes `swallowed-error` to keep the bookend deterministic
against patterns that actually fire. See
`docs/runbooks/epic3_smoke.md` for the rationale.

## Do NOT "fix" these gaps

The fixture exists to exercise detection. If you find yourself wanting
to add a `Timeout` field to `client.go`, you are reading the wrong file —
this is the failing target the smoke test pins.

## Self-contained module

This fixture is its own Go module (`go.mod` declares
`github.com/revelara-ai/orion/test/acceptance/fixtures/epic3-detection`).
Keeping it separate prevents the broken patterns from polluting orion's
production module graph and lets rvl-cli scan it cleanly.
