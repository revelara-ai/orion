.PHONY: vet lint check

default: build

build:
	go build -o bin/orion ./cmd/orion

test:
	go test ./...

install: build
	cp bin/orion ~/.local/bin/orion

vet:
	go vet $$(go list ./... | grep -v /archive/)

lint:
	golangci-lint run

check: vet lint
