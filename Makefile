.PHONY:

default: build

build:
	go build -o bin/orion ./cmd/orion

test:
	go test ./...

install: build
	cp bin/orion ~/.local/bin/orion
