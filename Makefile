.PHONY: vet lint check test test-short test-ci license-check

default: build

# Version stamping (or-c6zf.6): releases get the tag; dev builds get
# git-describe (tag-distance-sha, -dirty when the tree is modified).
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.0.0-dev)

build:
	go build -ldflags "-X main.version=$(VERSION)" -o bin/orion ./cmd/orion

# Full suite, including the heavy generate→prove e2e. The timeout is bumped past Go's 600s default
# because the conductor package alone is ~740s of real compile+prove work (or-tcs.9); a bare
# `go test ./...` would fail the default per-package timeout.
test:
	go test -timeout 1200s ./...

# CI lane: the heavy proof packages all serialize through the machine-wide
# proof flock (prooflock), so running packages in PARALLEL on a small shared
# runner starves per-test deadlines while one package holds the lock. -p 1
# runs packages sequentially — slower wall-clock, but honest and deterministic
# on 2-core runners; the per-package cap is generous for the same reason.
test-ci:
	go test -timeout 2400s -p 1 ./...

# Fast lane: skips the heavy build+prove e2e tests (each guarded by `if testing.Short()`) for quick
# feedback while iterating. Run `make test` before relying on a green result.
test-short:
	go test -short ./...

install: build
	cp bin/orion ~/.local/bin/orion

vet:
	go vet $$(go list ./... | grep -v /archive/)

lint:
	golangci-lint run

check: vet lint

# Dependency license audit (or-c6zf.1): forbidden/unknown licenses in the module
# graph fail the build. Install: go install github.com/google/go-licenses@latest
# modernc.org/mathutil is a licenseclassifier false negative — its LICENSE is
# standard BSD-3-Clause (verified by hand 2026-07-12); re-check on version bumps.
license-check:
	go-licenses check ./... --ignore github.com/revelara-ai/orion --ignore modernc.org/mathutil
