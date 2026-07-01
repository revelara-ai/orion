.PHONY: vet lint check test test-short

default: build

build:
	go build -o bin/orion ./cmd/orion

# Full suite, including the heavy generate→prove e2e. The timeout is bumped past Go's 600s default
# because the conductor package alone is ~740s of real compile+prove work (or-tcs.9); a bare
# `go test ./...` would fail the default per-package timeout.
test:
	go test -timeout 1200s ./...

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
