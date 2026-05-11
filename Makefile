.DEFAULT_GOAL := build

# Build metadata. Override on the command line: make VERSION=v0.1.0 build
VERSION   ?= dev
COMMIT    ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -X github.com/revelara-ai/orion/internal/version.Version=$(VERSION) \
           -X github.com/revelara-ai/orion/internal/version.Commit=$(COMMIT) \
           -X github.com/revelara-ai/orion/internal/version.BuildDate=$(BUILD_DATE)

DOCKER_IMAGE ?= orion
DOCKER_TAG   ?= $(VERSION)

.PHONY: build
build: bin/orion bin/orion-cli ## Build both binaries

bin/orion:
	@mkdir -p bin
	go build -ldflags "$(LDFLAGS)" -o bin/orion ./cmd/orion

bin/orion-cli:
	@mkdir -p bin
	go build -ldflags "$(LDFLAGS)" -o bin/orion-cli ./cmd/orion-cli

.PHONY: test
test: ## Run unit tests
	go test ./...

.PHONY: lint
lint: ## Run golangci-lint
	golangci-lint run

.PHONY: docker-build
docker-build: ## Build the orion server container image
	docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		-t $(DOCKER_IMAGE):$(DOCKER_TAG) .

.PHONY: run
run: bin/orion ## Run orion locally
	./bin/orion

.PHONY: db-up
db-up: ## Start local Postgres for dev/test via docker compose
	docker compose up -d postgres
	@echo "Postgres on localhost:5432 user=orion db=orion (POSTGRES_DSN=postgres://orion:orion@localhost:5432/orion?sslmode=disable)"

.PHONY: db-down
db-down: ## Stop and remove local Postgres
	docker compose down -v

.PHONY: migrate
migrate: bin/orion ## Apply Postgres migrations (cmd/orion runs them at startup; use this to apply without serving)
	POSTGRES_DSN=$${POSTGRES_DSN:-postgres://orion:orion@localhost:5432/orion?sslmode=disable} \
		./bin/orion -addr=:0 & \
	OPID=$$!; sleep 2; kill $$OPID || true

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf bin/

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'
