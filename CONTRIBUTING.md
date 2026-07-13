# Contributing to Orion

Thanks for your interest in Orion — the reliability layer of the agentic SDLC.
Read [docs/MANIFESTO.md](docs/MANIFESTO.md) first: it is the contract this
project is built against, and contributions are reviewed against it.

## Build, test, lint

```bash
make build        # go build -o bin/orion ./cmd/orion
make test-short   # the fast lane (what the PR CI runs)
make test         # the full suite (~20 min; heavy generate→prove e2e)
make vet          # go vet
make lint         # golangci-lint v2 (see .golangci.yml)
make license-check# dependency license audit (go-licenses)
```

Prerequisites: Go (version in `go.mod`), `git >= 2.28`, and **bubblewrap**
(`bwrap`) — the proof sandbox requires it, including parts of the short test
lane. On Ubuntu 24.04+ you may need
`sudo sysctl -w kernel.apparmor_restrict_unprivileged_userns=0`.

## The doctrine: done means proven

Orion's own development follows the loop it implements:

- **Tests first, mutation-checked.** A change lands with tests that would
  catch its removal — if breaking the invariant doesn't fail a test, the test
  is missing (we routinely verify this by mutation).
- **Built ≠ wired.** New packages must be reachable from `cmd/orion` or carry
  a tracked entry on the `deferredOrphans` ratchet
  (`test/acceptance/wireup_test.go`).
- **The acceptance harness is the North Star.** `TestV20Loop` encodes the full
  target as 56 predicates; some are red by design for unbuilt surfaces (the
  `deferredPredicates` ratchet). Never "fix" a predicate by weakening it.
- **PR lane must be green**: build + `make test-short` + lint (new issues
  only) — see `.github/workflows/ci.yml`.

## Issues and the tracker

Orion's internal planning uses **beads**, which lives in an embedded Dolt DB
synced via `refs/dolt/data` on the git remote — external contributors cannot
write to it. The policy:

- **Open GitHub issues** for bugs and proposals. Maintainers mirror accepted
  work into beads and link the issue.
- PRs are welcome without a beads entry; reference the GitHub issue instead.

## Pull requests

- Keep changes small and bounded (batch size is a reliability lever).
- Include the *why* in the commit message, not just the what.
- New behavior needs a test; changed behavior needs the changed test to say so.
- Run `make test-short && make vet` before pushing; CI runs the same.
