# Orion

Autonomous software-engineering orchestrator with continuous Polaris reliability context. Part of the [Revelara](https://revelara.ai) platform.

See [`docs/SPEC/Orion-SPEC.md`](docs/SPEC/Orion-SPEC.md) for the full specification and [`docs/PRD/orion-v1.md`](docs/PRD/orion-v1.md) for the v1 product requirements.

## Status

E0-1 (foundation): repo skeleton, build, test, lint, container, k8s manifests, skaffold. Synthesis pipeline lands in Epic 1.

## Quickstart

Requires Go 1.22+, Docker, and (for the deploy path) `kubectl` + `skaffold` + `minikube`.

```bash
# Build
make build
./bin/orion --addr :8080 &
curl -sf http://localhost:8080/health   # → {"status":"ok"}
./bin/orion-cli --version

# Test + lint
make test
make lint

# Container
make docker-build

# Deploy to minikube
minikube start
skaffold run            # build + deploy
kubectl port-forward svc/orion 8080:8080 &
curl -sf http://localhost:8080/health
```

## Layout

```
cmd/orion/           server entrypoint
cmd/orion-cli/       operator + dogfood CLI
internal/version/    build-time version
k8s/base/            kustomize base manifests
docs/                PRD, SPEC, design notes
```

## Development workflow

Issue tracking via [beads](https://github.com/oddjobs/beads) (`bd ready`, `bd show <id>`, etc.).

```bash
bd ready                       # find next unblocked work
bd show <id>                   # review the issue
bd update <id> --claim         # claim it
# ... implement, test, commit, push ...
bd close <id>                  # mark complete
```

For Epic-driven multi-session work, see `AGENTS.md`.
