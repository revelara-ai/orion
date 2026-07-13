## What & why

<!-- The intent this change serves, not just the mechanical what. -->

## Proof

- [ ] Tests cover the new/changed behavior (a mutation of the change would fail them)
- [ ] `make build && make test-short && make vet` green locally
- [ ] No orphan code: new packages reachable from `cmd/orion` (or a tracked `deferredOrphans` entry)
- [ ] No weakened acceptance predicates (`test/acceptance/`)

## Tracker

<!-- Link the GitHub issue. Maintainers mirror accepted work into beads. -->
