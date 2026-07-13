# Releasing Orion

Releases are semver-tagged, human-initiated, and built by CI — never by the
autonomous loop.

## Scheme

- `v0.MINOR.PATCH` pre-1.0: MINOR for features (any `feat:` since the last
  tag), PATCH for fix-only tags. Breaking changes are expected pre-1.0 and
  called out in the release notes.
- `v1.0.0` when the North-Star acceptance harness (56 predicates) is fully
  green and the public API (CLI surface + config format) is committed to.

## Cutting a release (maintainer)

```bash
git tag -a v0.2.0 -m "v0.2.0"
git push origin v0.2.0        # triggers .github/workflows/release.yml
```

The workflow builds CGO-free archives for linux/darwin × amd64/arm64,
generates `checksums.txt`, attests build provenance (SLSA), and opens a
**draft** GitHub release — review the notes, then click publish.

## Version stamping

`make build` injects `git describe --tags --always --dirty` into
`main.version`; release builds get the clean tag from goreleaser. Binaries
built via `go install .../cmd/orion@<tag>` report the module version through
`debug.ReadBuildInfo`.
